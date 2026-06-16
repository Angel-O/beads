package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/plane"
	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

var planeCmd = &cobra.Command{
	Use:     "plane",
	GroupID: "advanced",
	Short:   "Plane integration commands",
	Long: `Synchronize issues between beads and Plane (https://github.com/makeplane/plane).

Targets self-hosted Plane Community Edition via the /api/v1 REST API.
Work items are linked by Plane's native external_id/external_source fields
(external_id = bead ID), making creation idempotent and duplicate-safe.

Configuration:
  bd config set plane.api_key "YOUR_API_KEY"   # personal token from Plane profile settings
  bd config set plane.base_url "https://plane.example.com"
  bd config set plane.workspace "myworkspace"  # workspace slug
  bd config set plane.project_id "UUID"        # target project UUID

Environment variables (alternative to config):
  PLANE_API_KEY     - Plane personal API token
  PLANE_BASE_URL    - Instance root URL
  PLANE_WORKSPACE   - Workspace slug
  PLANE_PROJECT_ID  - Project UUID

Field mapping notes:
  - Plane CE has no work item types and no blocked state: beads issue
    types and blocked status round-trip via beads:type:* and
    beads:blocked labels on the Plane side.
  - Status maps through Plane state groups rather than state names, so
    custom project workflow states work out of the box.
  - Descriptions convert between Markdown (beads) and HTML (Plane).

Examples:
  bd plane sync --pull         # Import issues from Plane
  bd plane sync --push         # Export issues to Plane
  bd plane sync                # Bidirectional sync (pull then push)
  bd plane sync --dry-run      # Preview sync without changes
  bd plane status              # Show sync status`,
}

var planeSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Synchronize issues with Plane",
	Long: `Synchronize issues between beads and Plane.

Modes:
  --pull         Import issues from Plane into beads
  --push         Export issues from beads to Plane
  (no flags)     Bidirectional sync: pull then push, with conflict resolution

Filtering:
  --state open|closed|all   Restrict sync to open or closed issues
  --include-ephemeral       Include ephemeral issues (wisps, etc.) when
                            pushing; default is to keep them local

Conflict Resolution:
  By default, newer timestamp wins. Override with:
  --prefer-local   Always prefer local beads version
  --prefer-plane   Always prefer Plane version

Examples:
  bd plane sync --pull                # Import from Plane
  bd plane sync --push --create-only  # Push new issues only
  bd plane sync --dry-run             # Preview without changes
  bd plane sync --prefer-local        # Bidirectional, local wins`,
	Run: runPlaneSync,
}

var planeStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show Plane sync status",
	Long: `Show the current Plane sync status, including:
  - Last sync timestamp
  - Configuration status
  - Number of issues with Plane links
  - Issues pending push (no external_ref)`,
	Run: runPlaneStatus,
}

func init() {
	planeSyncCmd.Flags().Bool("pull", false, "Pull issues from Plane")
	planeSyncCmd.Flags().Bool("push", false, "Push issues to Plane")
	planeSyncCmd.Flags().Bool("dry-run", false, "Preview sync without making changes")
	planeSyncCmd.Flags().Bool("prefer-local", false, "Prefer local version on conflicts")
	planeSyncCmd.Flags().Bool("prefer-plane", false, "Prefer Plane version on conflicts")
	planeSyncCmd.Flags().Bool("create-only", false, "Only create new issues, don't update existing")
	planeSyncCmd.Flags().String("state", "all", "Issue state to sync: open, closed, all")
	planeSyncCmd.Flags().Bool("include-ephemeral", false, "Include ephemeral issues (wisps, etc.) when pushing to Plane")
	registerSelectiveSyncFlags(planeSyncCmd)

	planeCmd.AddCommand(planeSyncCmd)
	planeCmd.AddCommand(planeStatusCmd)
	rootCmd.AddCommand(planeCmd)
}

func runPlaneSync(cmd *cobra.Command, args []string) {
	pull, _ := cmd.Flags().GetBool("pull")
	push, _ := cmd.Flags().GetBool("push")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	preferLocal, _ := cmd.Flags().GetBool("prefer-local")
	preferPlane, _ := cmd.Flags().GetBool("prefer-plane")
	createOnly, _ := cmd.Flags().GetBool("create-only")
	state, _ := cmd.Flags().GetString("state")
	includeEphemeral, _ := cmd.Flags().GetBool("include-ephemeral")

	if !dryRun {
		CheckReadonly("plane sync")
	}

	if preferLocal && preferPlane {
		FatalError("cannot use both --prefer-local and --prefer-plane")
	}

	if err := ensureStoreActive(); err != nil {
		FatalError("database not available: %v", err)
	}

	if err := validatePlaneConfig(); err != nil {
		FatalError("%v", err)
	}

	ctx := rootCtx

	pt := &plane.Tracker{}
	if err := pt.Init(ctx, store); err != nil {
		FatalError("initializing Plane tracker: %v", err)
	}

	engine := tracker.NewEngine(pt, store, actor)
	engine.OnMessage = func(msg string) { fmt.Println("  " + msg) }
	engine.OnWarning = func(msg string) { fmt.Fprintf(os.Stderr, "Warning: %s\n", msg) }
	// Plane cannot represent beads assignees; without these hooks a pull
	// update would wipe the local assignee field.
	engine.PullHooks = plane.NewPullHooks()

	opts := tracker.SyncOptions{
		Pull:       pull,
		Push:       push,
		DryRun:     dryRun,
		CreateOnly: createOnly,
		State:      state,
		// Wisps and other ephemeral beads stay local by default: pushing
		// them would pollute the Plane project with permanent work items.
		ExcludeEphemeral: !includeEphemeral,
	}

	if err := applySelectiveSyncFlags(cmd, &opts, push); err != nil {
		FatalError("%v", err)
	}

	if preferLocal {
		opts.ConflictResolution = tracker.ConflictLocal
	} else if preferPlane {
		opts.ConflictResolution = tracker.ConflictExternal
	} else {
		opts.ConflictResolution = tracker.ConflictTimestamp
	}

	result, err := engine.Sync(ctx, opts)
	if err != nil {
		if jsonOutput {
			outputJSON(result)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}

	if jsonOutput {
		outputJSON(result)
	} else if dryRun {
		fmt.Println("\n✓ Dry run complete (no changes made)")
	} else {
		if result.Stats.Pulled > 0 {
			fmt.Printf("✓ Pulled %d issues (%d created, %d updated)\n",
				result.Stats.Pulled, result.Stats.Created, result.Stats.Updated)
		}
		if result.Stats.Pushed > 0 {
			fmt.Printf("✓ Pushed %d issues\n", result.Stats.Pushed)
		}
		if result.Stats.Conflicts > 0 {
			fmt.Printf("→ Resolved %d conflicts\n", result.Stats.Conflicts)
		}
		fmt.Println("\n✓ Plane sync complete")
		if len(result.Warnings) > 0 {
			fmt.Println("\nWarnings:")
			for _, w := range result.Warnings {
				fmt.Printf("  - %s\n", w)
			}
		}
	}
}

// planeStatusCounts tallies issues linked to Plane and issues with no
// external ref at all (pending push candidates). Issues linked to OTHER
// trackers are neither.
func planeStatusCounts(issues []*types.Issue) (withRef, pending int) {
	for _, issue := range issues {
		switch {
		case issue.ExternalRef != nil && plane.IsPlaneExternalRef(*issue.ExternalRef):
			withRef++
		case issue.ExternalRef == nil:
			pending++
		}
	}
	return withRef, pending
}

func runPlaneStatus(cmd *cobra.Command, args []string) {
	ctx := rootCtx

	if err := ensureStoreActive(); err != nil {
		FatalError("%v", err)
	}

	baseURL, _ := store.GetConfig(ctx, "plane.base_url")
	if baseURL == "" {
		baseURL = os.Getenv("PLANE_BASE_URL")
	}
	workspace, _ := store.GetConfig(ctx, "plane.workspace")
	if workspace == "" {
		workspace = os.Getenv("PLANE_WORKSPACE")
	}
	projectID, _ := store.GetConfig(ctx, "plane.project_id")
	if projectID == "" {
		projectID = os.Getenv("PLANE_PROJECT_ID")
	}
	lastSync, _ := store.GetLocalMetadata(ctx, "plane.last_sync")

	configured := baseURL != "" && workspace != "" && projectID != ""

	allIssues, err := store.SearchIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		FatalError("%v", err)
	}

	withRef, pending := planeStatusCounts(allIssues)

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"configured":       configured,
			"plane_base_url":   baseURL,
			"plane_workspace":  workspace,
			"plane_project_id": projectID,
			"last_sync":        lastSync,
			"total_issues":     len(allIssues),
			"with_plane_ref":   withRef,
			"pending_push":     pending,
		})
		return
	}

	fmt.Println("Plane Sync Status")
	fmt.Println("=================")
	fmt.Println()

	if !configured {
		fmt.Println("Status: Not configured")
		fmt.Println()
		fmt.Println("To configure Plane integration:")
		fmt.Println("  bd config set plane.api_key \"YOUR_API_KEY\"")
		fmt.Println("  bd config set plane.base_url \"https://plane.example.com\"")
		fmt.Println("  bd config set plane.workspace \"myworkspace\"")
		fmt.Println("  bd config set plane.project_id \"PROJECT_UUID\"")
		return
	}

	fmt.Printf("Plane URL:    %s\n", baseURL)
	fmt.Printf("Workspace:    %s\n", workspace)
	fmt.Printf("Project:      %s\n", projectID)
	if lastSync != "" {
		fmt.Printf("Last Sync:    %s\n", lastSync)
	} else {
		fmt.Println("Last Sync:    Never")
	}
	fmt.Println()
	fmt.Printf("Total Issues: %d\n", len(allIssues))
	fmt.Printf("With Plane:   %d\n", withRef)
	fmt.Printf("Local Only:   %d\n", pending)

	if pending > 0 {
		fmt.Println()
		fmt.Printf("Run 'bd plane sync --push' to push %d local issue(s) to Plane\n", pending)
	}
}

// validatePlaneConfig checks that required Plane configuration is present.
func validatePlaneConfig() error {
	if err := ensureStoreActive(); err != nil {
		return fmt.Errorf("database not available: %w", err)
	}

	ctx := rootCtx

	// plane.api_key is yaml-only (a secret), so it is read from config.yaml
	// or the environment, never the Dolt database.
	apiKey := config.GetString("plane.api_key")
	if apiKey == "" && os.Getenv("PLANE_API_KEY") == "" {
		return fmt.Errorf("Plane API key not configured\nRun: bd config set plane.api_key \"YOUR_API_KEY\"\nOr: export PLANE_API_KEY=YOUR_API_KEY")
	}

	baseURL, _ := store.GetConfig(ctx, "plane.base_url")
	if baseURL == "" && os.Getenv("PLANE_BASE_URL") == "" {
		return fmt.Errorf("plane.base_url not configured\nRun: bd config set plane.base_url \"https://plane.example.com\"")
	}

	workspace, _ := store.GetConfig(ctx, "plane.workspace")
	if workspace == "" && os.Getenv("PLANE_WORKSPACE") == "" {
		return fmt.Errorf("plane.workspace not configured\nRun: bd config set plane.workspace \"myworkspace\"")
	}

	projectID, _ := store.GetConfig(ctx, "plane.project_id")
	if projectID == "" && os.Getenv("PLANE_PROJECT_ID") == "" {
		return fmt.Errorf("plane.project_id not configured\nRun: bd config set plane.project_id \"PROJECT_UUID\"")
	}

	return nil
}

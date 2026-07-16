package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/backendmigration"
	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/configfile"
	"golang.org/x/term"
)

var migrateBackendAmbientBeadsDir bool

var backendMigrationStorageSelectionEnvKeys = []string{
	"BEADS_DIR", "BEADS_DB", "BD_DB", "BEADS_DOLT_SERVER_DATABASE", "BEADS_DOLT_DATA_DIR",
	"BEADS_DOLT_SERVER_MODE", "BEADS_DOLT_SHARED_SERVER", "BD_IGNORE_SCHEMA_SKEW",
}

var migrateBackendCmd = &cobra.Command{
	Use:   "backend",
	Short: "Preview or apply a storage backend migration",
	Long: `Preview or apply an embedded Dolt to SQLite backend migration.

Preview is the default and has no persistent effect. Applying preserves the
embedded Dolt source (including its history), copies current issue-tracker
state into a new workspace-local SQLite database, verifies it, and only then
switches metadata.json to SQLite.

This is the only state-preserving backend change. Do not substitute
bd init --reinit-local --backend=sqlite: reinitialization is destructive and
transfers neither current issue state nor Dolt history.

Examples:
  bd migrate backend --to=sqlite
  bd migrate backend --to=sqlite --sqlite-path=beads-migrated.db
  bd migrate backend --to=sqlite --apply
  bd migrate backend --to=sqlite --apply --yes --json`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		to, _ := cmd.Flags().GetString("to")
		apply, _ := cmd.Flags().GetBool("apply")
		yes, _ := cmd.Flags().GetBool("yes")
		sqlitePath, _ := cmd.Flags().GetString("sqlite-path")
		if err := validateBackendMigrationWorkspaceSelection(cmd); err != nil {
			return HandleErrorRespectJSON("%v", err)
		}

		if to != "sqlite" {
			return HandleErrorRespectJSON("--to=sqlite is required; this migration slice supports SQLite only")
		}
		if yes && !apply {
			return HandleErrorRespectJSON("--yes is only valid together with --apply")
		}
		beadsDir := strings.TrimSpace(os.Getenv("BEADS_DIR"))
		if beadsDir == "" {
			return HandleErrorRespectJSON("no active beads workspace found")
		}
		ctx := rootCtx
		if ctx == nil {
			ctx = context.Background()
		}
		requestedPath := sqlitePath
		if !cmd.Flags().Changed("sqlite-path") {
			requestedPath = ""
		}
		admission, err := backendmigration.Inspect(beadsDir, requestedPath)
		if err != nil {
			return handleBackendMigrationFailure(beadsDir, requestedPath, err)
		}
		validatedPath := admission.SQLitePath

		if !apply {
			result, err := backendmigration.Preview(ctx, beadsDir, requestedPath)
			if err != nil {
				return handleBackendMigrationFailure(beadsDir, requestedPath, err)
			}
			return emitBackendMigrationResult(result)
		}

		if readonlyMode {
			return HandleErrorRespectJSON("operation 'migrate backend' is not allowed in read-only mode")
		}
		if !yes {
			if jsonOutput || !term.IsTerminal(int(os.Stdin.Fd())) {
				return HandleErrorRespectJSON("backend migration apply requires --yes in JSON or non-interactive mode")
			}
			confirmed, err := confirmBackendMigration(admission)
			if err != nil {
				return HandleErrorRespectJSON("read backend migration confirmation: %v", err)
			}
			if !confirmed {
				fmt.Println("Backend migration canceled; no changes were made.")
				return nil
			}
		}

		result, err := backendmigration.Apply(ctx, beadsDir, validatedPath)
		if err != nil {
			return handleBackendMigrationFailure(beadsDir, validatedPath, err)
		}
		return emitBackendMigrationResult(result)
	},
}

func captureBackendMigrationAmbientSelection(cmd *cobra.Command) {
	migrateBackendAmbientBeadsDir = cmd == migrateBackendCmd && strings.TrimSpace(changeDir) == "" && strings.TrimSpace(os.Getenv("BEADS_DIR")) != ""
}

func validateBackendMigrationWorkspaceSelection(cmd *cobra.Command) error {
	if migrateBackendAmbientBeadsDir {
		return errors.New("backend migration does not accept BEADS_DIR; unset it and run from the physical workspace being migrated")
	}
	if redirect := beads.GetRedirectInfo(); redirect.IsRedirected {
		return fmt.Errorf("backend migration cannot run through a redirected workspace; run it from the repository that owns the target .beads directory")
	}
	if cmd != nil && cmd.Root() != nil && cmd.Root().PersistentFlags().Changed("db") {
		return fmt.Errorf("backend migration does not accept --db; run it from the workspace being migrated")
	}
	for _, name := range backendMigrationStorageSelectionEnvKeys {
		if name == "BEADS_DIR" || name == "BD_IGNORE_SCHEMA_SKEW" {
			continue
		}
		if os.Getenv(name) != "" {
			return fmt.Errorf("backend migration does not accept the %s storage-selection override; unset it and run from the workspace being migrated", name)
		}
	}
	if dbPath != "" {
		return errors.New("backend migration does not accept a configured db selection; clear it and run from the physical workspace being migrated")
	}
	if cmd != nil && cmd.Root() != nil && cmd.Root().PersistentFlags().Changed("global") && globalFlag {
		return errors.New("backend migration does not accept --global; run from the physical workspace being migrated")
	}
	if os.Getenv("BD_IGNORE_SCHEMA_SKEW") == "1" {
		return errors.New("backend migration does not accept BD_IGNORE_SCHEMA_SKEW or --ignore-schema-skew; update the source schema first")
	}
	return nil
}

func confirmBackendMigration(admission backendmigration.Admission) (bool, error) {
	switch {
	case !admission.RecoveryRequired:
		fmt.Fprintf(os.Stderr, "This will copy current issue state and switch the workspace backend to SQLite at %s.\n", admission.SQLitePath)
	case admission.Authority == backendmigration.AuthoritySQLite:
		fmt.Fprintf(os.Stderr, "SQLite at %s is already authoritative; this will verify it and finish recovery.\n", admission.SQLitePath)
	default:
		fmt.Fprintf(os.Stderr, "A previous migration stopped before cutover; this will safely retry migration to SQLite at %s.\n", admission.SQLitePath)
	}
	fmt.Fprintln(os.Stderr, "The embedded Dolt source and its history will be preserved.")
	fmt.Fprintf(os.Stderr, "Type %s to continue: ", admission.SQLitePath)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false, err
	}
	return backendMigrationConfirmationMatches(line, admission.SQLitePath), nil
}

func backendMigrationConfirmationMatches(line, sqlitePath string) bool {
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return line == sqlitePath
}

func handleBackendMigrationFailure(beadsDir, sqlitePath string, cause error) error {
	return emitBackendMigrationSafeError(backendmigration.SafeFailure(beadsDir, cause, sqlitePath))
}

func handlePendingBackendMigration(beadsDir string) error {
	return emitBackendMigrationSafeError(backendmigration.PendingError(beadsDir))
}

func rejectPendingBackendMigrationForCommand(beadsDir string) error {
	err := configfile.RejectPendingBackendMigration(beadsDir)
	if err == nil {
		return nil
	}
	if errors.Is(err, configfile.ErrBackendMigrationPending) {
		return handlePendingBackendMigration(beadsDir)
	}
	return handleBackendMigrationFailure(beadsDir, "", err)
}

func emitBackendMigrationSafeError(safe *backendmigration.SafeError) error {
	if jsonOutput {
		if err := outputJSON(safe); err != nil {
			return HandleError("encode backend migration error")
		}
		return &exitError{Code: 1}
	}
	fmt.Fprintf(os.Stderr, "Error: %s\n", safe.Error())
	fmt.Fprintf(os.Stderr, "Authority: %s\n", safe.Authority())
	fmt.Fprintf(os.Stderr, "Source preserved: %t\n", safe.SourcePreserved())
	if safe.RetryCommand() != "" {
		fmt.Fprintf(os.Stderr, "Retry: %s\n", safe.RetryCommand())
	}
	return &exitError{Code: 1}
}

func emitBackendMigrationResult(result backendmigration.Result) error {
	if jsonOutput {
		return outputJSON(result)
	}
	if result.Status == "planned" {
		fmt.Println("Backend migration plan")
		fmt.Println("  Source: embedded Dolt (preserved)")
		fmt.Printf("  Target: SQLite (.beads/%s)\n", result.SQLitePath)
		fmt.Println("  Current issue state: will be copied and verified on apply")
		fmt.Println("  Dolt history: preserved in the source, not transferred to SQLite")
		fmt.Println("  Effect: none (preview only)")
		fmt.Println()
		fmt.Printf("Apply with: %s\n", backendMigrationApplyCommand(result.SQLitePath))
		return nil
	}
	fmt.Printf("Migrated backend from embedded Dolt to SQLite at .beads/%s.\n", result.SQLitePath)
	fmt.Println("Verified current state and preserved .beads/embeddeddolt.")
	return nil
}

func backendMigrationApplyCommand(sqlitePath string) string {
	command := "bd migrate backend --to=sqlite"
	if sqlitePath != "" && sqlitePath != "beads.db" {
		command += " --sqlite-path=" + sqlitePath
	}
	return command + " --apply"
}

func init() {
	migrateBackendCmd.Flags().String("to", "", "Target backend (required: sqlite)")
	migrateBackendCmd.Flags().String("sqlite-path", "beads.db", "Lowercase workspace-local SQLite database basename")
	migrateBackendCmd.Flags().Bool("apply", false, "Apply the planned migration")
	migrateBackendCmd.Flags().Bool("yes", false, "Confirm apply for automation")
	migrateCmd.AddCommand(migrateBackendCmd)
}

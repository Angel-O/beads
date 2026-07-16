package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/cmd/bd/doctor"
	"github.com/steveyegge/beads/cmd/bd/doctor/fix"
	backendmigrationcontrol "github.com/steveyegge/beads/internal/backendmigration/control"
	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/doltserver"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage/dolt"
	"github.com/steveyegge/beads/internal/storage/doltutil"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/storage/schema"
	"github.com/steveyegge/beads/internal/storage/versioncontrolops"
	"golang.org/x/term"
)

var resolveBootstrapAuthoritativeMetadata = fix.ResolveAuthoritativeServerMetadata
var cloneBootstrapRemote = cloneFromRemote
var warmupSyncedBootstrap = warmupSyncedBootstrapStore
var bootstrapGitOriginHasDoltDataRef = gitOriginHasDoltDataRef

var (
	errBootstrapAuthorityChanged     = errors.New("bootstrap backend authority changed")
	errBootstrapAuthorityUnavailable = errors.New("bootstrap backend authority is unavailable")
)

type bootstrapAuthority struct {
	targetExists bool
	ownerDir     string
	source       string
	canonical    []byte
}

type bootstrapRefusal struct {
	message string
	hint    string
}

func (e *bootstrapRefusal) Error() string { return e.message }

type bootstrapCloneFailure struct {
	cause error
}

func (e *bootstrapCloneFailure) Error() string {
	return "clone from configured remote failed; verify the remote URL, credentials, and provider availability"
}

func (e *bootstrapCloneFailure) Unwrap() error { return e.cause }

type bootstrapAuthorityControl struct {
	guards map[string]*backendmigrationcontrol.Guard
}

type bootstrapServerProbeConfig struct {
	host     string
	port     int
	user     string
	pass     string
	database string
	tls      bool
}

type bootstrapServerDBCheck struct {
	Exists    bool
	Reachable bool
	Err       error
}

var checkBootstrapServerDB = func(probeCfg bootstrapServerProbeConfig) bootstrapServerDBCheck {
	host := probeCfg.host
	port := probeCfg.port
	dbName := probeCfg.database
	dsn := doltutil.ServerDSN{
		Host:     host,
		Port:     port,
		User:     probeCfg.user,
		Password: probeCfg.pass,
		TLS:      probeCfg.tls,
	}.String()

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return bootstrapServerDBCheck{Reachable: false, Err: err}
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return bootstrapServerDBCheck{Reachable: false, Err: err}
	}

	rows, err := db.QueryContext(ctx, "SHOW DATABASES")
	if err != nil {
		return bootstrapServerDBCheck{Reachable: true, Err: err}
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return bootstrapServerDBCheck{Reachable: true, Err: err}
		}
		if name == dbName {
			return bootstrapServerDBCheck{Exists: true, Reachable: true}
		}
	}
	if err := rows.Err(); err != nil {
		return bootstrapServerDBCheck{Reachable: true, Err: err}
	}

	return bootstrapServerDBCheck{Exists: false, Reachable: true}
}

var bootstrapCmd = &cobra.Command{
	Use:     "bootstrap",
	GroupID: "setup",
	Short:   "Non-destructive database setup for fresh clones and recovery",
	Long: `Bootstrap sets up the beads database without destroying existing data.
Unlike 'bd init --force', bootstrap will never delete existing issues.

Bootstrap auto-detects the right action:
  • If sync.remote is configured: clones from the remote
  • If git origin has Dolt data (refs/dolt/data): clones from git and wires origin for future push/pull
  • If .beads/backup/*.jsonl exists: restores from backup
  • If .beads/issues.jsonl exists: imports from git-tracked JSONL
  • If no database exists: creates a fresh one
  • If database already exists: validates and reports status

This is the recommended command for:
  • Setting up beads on a fresh clone
  • Recovering after moving to a new machine
  • Repairing a broken database configuration

Non-interactive mode (--non-interactive, --yes/-y, or BD_NON_INTERACTIVE=1):
  Skips the confirmation prompt before executing the bootstrap plan.
  Also auto-detected when stdin is not a terminal or CI=true is set.

Examples:
  bd bootstrap              # Auto-detect and set up
  bd bootstrap --dry-run    # Show what would be done
  bd bootstrap --json       # Output plan as JSON
  bd bootstrap --yes        # Skip confirmation prompt
`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("bootstrap")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		dryRun, _ := cmd.Flags().GetBool("dry-run")
		yesFlag, _ := cmd.Flags().GetBool("yes")
		nonInteractiveFlag, _ := cmd.Flags().GetBool("non-interactive")

		// Resolve non-interactive mode: flag > env var > CI env > terminal detection.
		nonInteractive := isNonInteractiveBootstrap(yesFlag || nonInteractiveFlag)

		// Find beads directory
		deferredFreshCloneRemoteCheck := false
		beadsDir := beads.FindBeadsDir()
		if beadsDir == "" {
			// No .beads directory exists yet. Before giving up, probe the
			// git remote for Dolt data stored in git (refs/dolt/data). This
			// is the "fresh second clone" case: clone1 pushed Beads state
			// to a git remote, and clone2 needs to bootstrap from it.
			// Only applies to git remotes — Dolt-native remotes (DoltHub,
			// S3, etc.) should be configured via sync.remote. (GH#2792)
			//
			// If found, synthesize the theoretical .beads path and fall
			// through to the normal detectBootstrapAction + executeBootstrapPlan
			// flow. Actual directory creation is deferred to executeSyncAction
			// to preserve --dry-run semantics.
			if isGitRepo() && !isBareGitRepo() {
				if originURL, err := gitOriginGetURL(); err == nil && originURL != "" {
					shouldSynthesize := false
					if dryRun {
						// Whether refs/dolt/data exists requires provider access. Keep
						// dry-run local-only and report that decision as deferred.
						deferredFreshCloneRemoteCheck = true
						shouldSynthesize = true
					} else {
						shouldSynthesize = bootstrapGitOriginHasDoltDataRef()
					}
					if shouldSynthesize {
						if fallbackDir := beads.GetWorktreeFallbackBeadsDir(); fallbackDir != "" {
							beadsDir = fallbackDir
						} else {
							cwd, err := os.Getwd()
							if err != nil {
								return HandleError("failed to get working directory: %v", err)
							}
							beadsDir = filepath.Join(cwd, ".beads")
						}
					}
				}
			}
		}

		if beadsDir == "" {
			if jsonOutput {
				if err := outputJSON(noWorkspaceBootstrapPayload()); err != nil {
					return err
				}
				return SilentExit()
			}
			fmt.Fprintf(os.Stderr, "Hint: %s\n", diagHint())
			fmt.Fprintf(os.Stderr, "Bootstrap is for existing projects that need database setup.\n")
			return HandleError("%s", activeWorkspaceNotFoundMessage())
		}
		if err := rejectBootstrapNonDoltBackend(beadsDir); err != nil {
			return err
		}

		// Capture the exact target-or-parent metadata authority used for this
		// plan. Apply-mode repairs are recaptured from disk; dry-run keeps the
		// resolver's hypothetical repaired config without publishing it.
		authority, cfg, repairMsg, err := resolveBootstrapConfigAndAuthority(beadsDir, dryRun)
		if err != nil {
			return handleBootstrapCommandError(err)
		}
		if err := validateBootstrapConfigBackend(cfg); err != nil {
			return handleBootstrapCommandError(err)
		}

		// Remote and server probes can activate retained Dolt state. Apply-mode
		// planning runs them under migration control. Dry-run remains strictly
		// read-only, so it defers provider probes and revalidates authority around
		// its local inspection instead of publishing a control artifact.
		var plan BootstrapPlan
		if deferredFreshCloneRemoteCheck {
			if err := revalidateBootstrapAuthorityReadOnly(beadsDir, authority); err != nil {
				return handleBootstrapCommandError(err)
			}
			plan = BootstrapPlan{
				Action:   "deferred",
				Reason:   "No local Beads workspace exists; checking git origin for Dolt data is deferred until bootstrap runs without --dry-run",
				BeadsDir: beadsDir,
				Database: cfg.GetDoltDatabase(),
			}
		} else if dryRun {
			plan, err = detectBootstrapDryRunAction(beadsDir, cfg, authority)
		} else {
			plan, err = detectBootstrapActionWithAuthority(beadsDir, cfg, authority)
		}
		if err != nil {
			return handleBootstrapCommandError(err)
		}
		plan.authority = authority

		if jsonOutput {
			if plan.Action == "none" || dryRun {
				return outputJSON(plan)
			}
		} else {
			if repairMsg != "" {
				fmt.Fprintf(os.Stderr, "Bootstrap metadata repair: %s\n", repairMsg)
			}
			printBootstrapPlan(plan)
			if plan.Action == "none" || dryRun {
				return nil
			}
		}

		if err := executeBootstrapPlan(plan, cfg, nonInteractive); err != nil {
			return handleBootstrapCommandError(err)
		}
		if jsonOutput {
			return outputJSON(plan)
		}
		return nil
	},
}

func resolveBootstrapConfigAndAuthority(beadsDir string, dryRun bool) (*bootstrapAuthority, *configfile.Config, string, error) {
	authority, cfg, err := captureBootstrapAuthority(beadsDir)
	if err != nil {
		return nil, nil, "", errors.Join(errBootstrapAuthorityUnavailable, err)
	}
	if cfg == nil {
		cfg = configfile.DefaultConfig()
	}
	resolved, repairMsg, err := applyBootstrapMetadataRepair(beadsDir, cfg, !dryRun)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to reconcile shared-server metadata: %w", err)
	}
	if resolved != nil {
		cfg = resolved
	}
	if dryRun {
		return authority, cfg, repairMsg, nil
	}

	authority, onDisk, err := captureBootstrapAuthority(beadsDir)
	if err != nil {
		return nil, nil, "", errors.Join(errBootstrapAuthorityUnavailable, err)
	}
	if onDisk != nil {
		cfg = onDisk
	}
	return authority, cfg, repairMsg, nil
}

func applyBootstrapMetadataRepair(beadsDir string, cfg *configfile.Config, apply bool) (*configfile.Config, string, error) {
	if beadsDir == "" {
		return cfg, "", nil
	}
	if _, err := os.Stat(beadsDir); err != nil {
		return cfg, "", nil
	}
	resolved, msg, err := resolveBootstrapAuthoritativeMetadata(filepath.Dir(beadsDir), apply)
	if err != nil {
		return nil, "", err
	}
	if resolved == nil {
		return cfg, msg, nil
	}
	return resolved, msg, nil
}

// BootstrapPlan describes what bootstrap will do.
type BootstrapPlan struct {
	Action      string `json:"action"` // "sync", "restore", "jsonl-import", "init", "deferred", "none"
	Reason      string `json:"reason"` // Human-readable explanation
	BeadsDir    string `json:"beads_dir"`
	Database    string `json:"database"`
	SyncRemote  string `json:"sync_remote,omitempty"`
	BackupDir   string `json:"backup_dir,omitempty"`
	JSONLFile   string `json:"jsonl_file,omitempty"`
	HasExisting bool   `json:"has_existing"`
	authority   *bootstrapAuthority
	syncRemote  string
}

func (p *BootstrapPlan) setSyncRemote(raw string) {
	p.syncRemote = raw
	p.SyncRemote = bootstrapRemoteForDisplay(raw)
}

func (p BootstrapPlan) rawSyncRemote() string {
	if p.syncRemote != "" {
		return p.syncRemote
	}
	return p.SyncRemote
}

func bootstrapRemoteForDisplay(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return "configured remote"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	if display := parsed.String(); display != "" {
		return display
	}
	return "configured remote"
}

func noWorkspaceBootstrapPayload() map[string]interface{} {
	return map[string]interface{}{
		"action":     "none",
		"reason":     activeWorkspaceNotFoundError(),
		"suggestion": diagHint(),
	}
}

// rejectBootstrapNonDoltBackend is the effect-free backend admission gate for
// bootstrap. A successful Dolt-to-SQLite migration intentionally retains the
// embedded Dolt source for rollback and audit, but that retained directory is
// no longer authoritative. Bootstrap is a Dolt recovery command, so letting it
// plan from sync.remote would reopen the retained engine and attempt DOLT_CLONE
// before the later metadata transition guard could stop it.
func rejectBootstrapNonDoltBackend(beadsDir string) error {
	if beadsDir == "" {
		return nil
	}

	cfg, err := readBootstrapAuthoritativeConfig(beadsDir)
	if err != nil {
		if errors.Is(err, backendmigrationcontrol.ErrRecoveryPending) {
			return handleBootstrapCommandError(err)
		}
		return handleBootstrapCommandError(errBootstrapAuthorityUnavailable)
	}
	return rejectBootstrapConfigBackend(cfg)
}

func rejectBootstrapConfigBackend(cfg *configfile.Config) error {
	return handleBootstrapCommandError(validateBootstrapConfigBackend(cfg))
}

func validateBootstrapConfigBackend(cfg *configfile.Config) error {
	if cfg == nil || cfg.Backend == "" || cfg.Backend == configfile.BackendDolt {
		return nil
	}

	switch cfg.Backend {
	case configfile.BackendPostgres, configfile.BackendMySQL, configfile.BackendSQLite:
		return &bootstrapRefusal{
			message: fmt.Sprintf("bd bootstrap is only available for Dolt workspaces; this workspace uses the %s backend", cfg.Backend),
			hint:    "run 'bd status' to verify the local data or 'bd doctor' to diagnose it",
		}
	default:
		return &bootstrapRefusal{
			message: "bd bootstrap could not safely identify the workspace backend",
			hint:    "run 'bd doctor' to repair workspace metadata before retrying bootstrap",
		}
	}
}

func readBootstrapAuthoritativeConfig(beadsDir string) (*configfile.Config, error) {
	return configfile.LoadAuthoritativeReadOnly(beadsDir)
}

func handleBootstrapCommandError(err error) error {
	if err == nil {
		return nil
	}
	var refusal *bootstrapRefusal
	if errors.As(err, &refusal) {
		return HandleErrorWithHintRespectJSON(refusal.message, refusal.hint)
	}
	switch {
	case errors.Is(err, backendmigrationcontrol.ErrBusy):
		return HandleErrorWithHintRespectJSON(
			"bd bootstrap could not safely continue because the workspace backend is changing",
			"wait for the backend operation to finish, then retry 'bd bootstrap'",
		)
	case errors.Is(err, backendmigrationcontrol.ErrRecoveryPending):
		return HandleErrorWithHintRespectJSON(
			"bd bootstrap cannot continue while backend migration recovery is pending",
			"finish recovery with 'bd migrate backend --to=sqlite --apply', then retry bootstrap if Dolt is still authoritative",
		)
	case errors.Is(err, errBootstrapAuthorityChanged):
		return HandleErrorWithHintRespectJSON(
			"bd bootstrap stopped because the workspace backend changed after planning",
			"verify the active backend with 'bd status', then rerun 'bd bootstrap' only for a Dolt workspace",
		)
	case errors.Is(err, errBootstrapAuthorityUnavailable):
		return HandleErrorWithHintRespectJSON(
			"bd bootstrap could not safely identify the workspace backend",
			"run 'bd doctor' to repair workspace metadata before retrying bootstrap",
		)
	default:
		return HandleErrorRespectJSON("Bootstrap failed: %v", err)
	}
}

func captureBootstrapAuthority(beadsDir string) (*bootstrapAuthority, *configfile.Config, error) {
	info, err := os.Lstat(beadsDir)
	targetExists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	if targetExists && !info.IsDir() {
		return nil, nil, errors.New("bootstrap workspace metadata directory is not a directory")
	}

	cfg, err := configfile.LoadAuthoritativeReadOnly(beadsDir)
	if err != nil {
		return nil, nil, err
	}
	if cfg == nil {
		var ownerDir string
		cfg, ownerDir, err = findParentConfigWithOwner(beadsDir)
		if err != nil {
			return nil, nil, err
		}
		if cfg == nil {
			return &bootstrapAuthority{targetExists: targetExists}, nil, nil
		}
		return marshalBootstrapAuthority(targetExists, ownerDir, cfg)
	}
	return marshalBootstrapAuthority(targetExists, beadsDir, cfg)
}

func marshalBootstrapAuthority(targetExists bool, ownerDir string, cfg *configfile.Config) (*bootstrapAuthority, *configfile.Config, error) {
	source := configfile.ConfigFileName
	if _, err := os.Lstat(configfile.ConfigPath(ownerDir)); errors.Is(err, os.ErrNotExist) {
		source = "config.json"
	} else if err != nil {
		return nil, nil, err
	}
	canonical, err := json.Marshal(cfg)
	if err != nil {
		return nil, nil, err
	}
	return &bootstrapAuthority{
		targetExists: targetExists,
		ownerDir:     filepath.Clean(ownerDir),
		source:       source,
		canonical:    canonical,
	}, cfg, nil
}

func sameBootstrapAuthority(expected, current *bootstrapAuthority) bool {
	return expected != nil && current != nil &&
		expected.targetExists == current.targetExists &&
		filepath.Clean(expected.ownerDir) == filepath.Clean(current.ownerDir) &&
		expected.source == current.source && bytes.Equal(expected.canonical, current.canonical)
}

func detectBootstrapActionWithAuthority(beadsDir string, cfg *configfile.Config, authority *bootstrapAuthority) (plan BootstrapPlan, resultErr error) {
	control, _, err := acquireBootstrapAuthorityControl(beadsDir, authority, false)
	if err != nil {
		return BootstrapPlan{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, control.Close()) }()
	if err := validateBootstrapConfigBackend(cfg); err != nil {
		return BootstrapPlan{}, err
	}
	return detectBootstrapAction(beadsDir, cfg), nil
}

func detectBootstrapDryRunAction(beadsDir string, cfg *configfile.Config, authority *bootstrapAuthority) (BootstrapPlan, error) {
	if err := validateBootstrapConfigBackend(cfg); err != nil {
		return BootstrapPlan{}, err
	}
	if err := revalidateBootstrapAuthorityReadOnly(beadsDir, authority); err != nil {
		return BootstrapPlan{}, err
	}
	plan := detectBootstrapActionWithoutProviderProbes(beadsDir, cfg)
	if err := revalidateBootstrapAuthorityReadOnly(beadsDir, authority); err != nil {
		return BootstrapPlan{}, err
	}
	return plan, nil
}

func revalidateBootstrapAuthorityReadOnly(beadsDir string, expected *bootstrapAuthority) error {
	if expected == nil {
		return errBootstrapAuthorityUnavailable
	}
	dirs := make([]string, 0, 2)
	if expected.targetExists {
		dirs = append(dirs, filepath.Clean(beadsDir))
	}
	if expected.ownerDir != "" && filepath.Clean(expected.ownerDir) != filepath.Clean(beadsDir) {
		dirs = append(dirs, filepath.Clean(expected.ownerDir))
	}
	for _, dir := range dirs {
		if err := backendmigrationcontrol.RejectPending(dir); err != nil {
			return err
		}
	}
	current, currentCfg, err := captureBootstrapAuthority(beadsDir)
	if err != nil {
		return errors.Join(errBootstrapAuthorityUnavailable, err)
	}
	if !sameBootstrapAuthority(expected, current) {
		return errBootstrapAuthorityChanged
	}
	if err := validateBootstrapConfigBackend(currentCfg); err != nil {
		return err
	}
	for _, dir := range dirs {
		if err := backendmigrationcontrol.RejectPending(dir); err != nil {
			return err
		}
	}
	return nil
}

func acquireBootstrapAuthorityControl(beadsDir string, expected *bootstrapAuthority, createTarget bool) (*bootstrapAuthorityControl, *configfile.Config, error) {
	if expected == nil {
		return nil, nil, errBootstrapAuthorityUnavailable
	}
	control := &bootstrapAuthorityControl{guards: make(map[string]*backendmigrationcontrol.Guard)}
	fail := func(err error) (*bootstrapAuthorityControl, *configfile.Config, error) {
		return nil, nil, errors.Join(err, control.Close())
	}

	dirs := make([]string, 0, 2)
	if expected.targetExists {
		dirs = append(dirs, filepath.Clean(beadsDir))
	}
	if expected.ownerDir != "" && filepath.Clean(expected.ownerDir) != filepath.Clean(beadsDir) {
		dirs = append(dirs, filepath.Clean(expected.ownerDir))
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		if err := control.acquire(dir); err != nil {
			return fail(err)
		}
	}

	current, currentCfg, err := captureBootstrapAuthority(beadsDir)
	if err != nil {
		return fail(errors.Join(errBootstrapAuthorityUnavailable, err))
	}
	if !sameBootstrapAuthority(expected, current) {
		return fail(errBootstrapAuthorityChanged)
	}
	if err := validateBootstrapConfigBackend(currentCfg); err != nil {
		return fail(err)
	}

	if createTarget && !expected.targetExists {
		if err := os.Mkdir(beadsDir, 0o750); err != nil {
			if errors.Is(err, os.ErrExist) {
				return fail(errBootstrapAuthorityChanged)
			}
			return fail(fmt.Errorf("create beads directory: %w", err))
		}
		if err := control.acquire(filepath.Clean(beadsDir)); err != nil {
			return fail(err)
		}
		afterCreate, afterCreateCfg, err := captureBootstrapAuthority(beadsDir)
		if err != nil {
			return fail(errors.Join(errBootstrapAuthorityUnavailable, err))
		}
		expectedAfterCreate := *expected
		expectedAfterCreate.targetExists = true
		if !sameBootstrapAuthority(&expectedAfterCreate, afterCreate) {
			return fail(errBootstrapAuthorityChanged)
		}
		currentCfg = afterCreateCfg
	}
	return control, currentCfg, nil
}

func (c *bootstrapAuthorityControl) acquire(beadsDir string) error {
	if c == nil {
		return errBootstrapAuthorityUnavailable
	}
	dir := filepath.Clean(beadsDir)
	if _, ok := c.guards[dir]; ok {
		return nil
	}
	if err := backendmigrationcontrol.RejectPending(dir); err != nil {
		return err
	}
	guard, err := backendmigrationcontrol.TryAcquire(dir)
	if err != nil {
		return err
	}
	if err := backendmigrationcontrol.RejectPending(dir); err != nil {
		_ = guard.Close()
		return err
	}
	c.guards[dir] = guard
	return nil
}

func (c *bootstrapAuthorityControl) release(beadsDir string) error {
	if c == nil {
		return nil
	}
	dir := filepath.Clean(beadsDir)
	guard := c.guards[dir]
	delete(c.guards, dir)
	if guard == nil {
		return nil
	}
	return guard.Close()
}

func (c *bootstrapAuthorityControl) Close() error {
	if c == nil {
		return nil
	}
	dirs := make([]string, 0, len(c.guards))
	for dir := range c.guards {
		dirs = append(dirs, dir)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	var resultErr error
	for _, dir := range dirs {
		resultErr = errors.Join(resultErr, c.release(dir))
	}
	return resultErr
}

func detectBootstrapAction(beadsDir string, cfg *configfile.Config) BootstrapPlan {
	return detectBootstrapActionWithProviderProbes(beadsDir, cfg, true)
}

func detectBootstrapActionWithoutProviderProbes(beadsDir string, cfg *configfile.Config) BootstrapPlan {
	return detectBootstrapActionWithProviderProbes(beadsDir, cfg, false)
}

func detectBootstrapActionWithProviderProbes(beadsDir string, cfg *configfile.Config, probeProviders bool) BootstrapPlan {
	plan := BootstrapPlan{
		BeadsDir: beadsDir,
		Database: cfg.GetDoltDatabase(),
	}

	// When bootstrap synthesized a fallback beadsDir for a fresh clone or
	// worktree recovery, the path may not exist yet. In that case we must let
	// sync.remote / refs/dolt/data detection run before treating an existing
	// shared-server database as "nothing to do", otherwise an unrelated default
	// "beads" database can mask the real recovery path.
	beadsDirExists := false
	if info, err := os.Stat(beadsDir); err == nil && info.IsDir() {
		beadsDirExists = true
	}

	// Check for existing database (path differs between server and embedded mode).
	// Determine server/shared-server mode from the target workspace itself
	// (metadata.json, env vars, and the target config.yaml when present) rather
	// than unrelated global config loaded from the caller's current repo.
	isSharedServer := bootstrapSharedServerMode(beadsDir)
	isServer := cfg.IsDoltServerMode() || isSharedServer

	// Check sync.remote (primary) or sync.git-remote (deprecated fallback)
	syncRemote := resolveSyncRemote()
	if syncRemote != "" {
		// User-provided sync.remote — trust the URL format as-is.
		// normalizeRemoteURL would convert http:// to git+http://,
		// breaking Dolt remotesapi endpoints (GH#3339).
		plan.setSyncRemote(syncRemote)
		plan.Action = "sync"
		plan.Reason = "sync.remote configured — will clone from " + plan.SyncRemote
		return plan
	}

	// Auto-detect: probe git origin for Dolt data stored in git
	// (refs/dolt/data). This only applies to git remotes — Dolt-native
	// remotes (DoltHub, S3, etc.) must be configured via sync.remote.
	if probeProviders && isGitRepo() && !isBareGitRepo() {
		if originURL, err := gitOriginGetURL(); err == nil && originURL != "" {
			if bootstrapGitOriginHasDoltDataRef() {
				plan.setSyncRemote(normalizeRemoteURL(originURL))
				plan.Action = "sync"
				plan.Reason = "Found Dolt data on git origin (refs/dolt/data) — will clone from " + plan.SyncRemote
				return plan
			}
		}
	}

	if dbAction, ok := existingBootstrapDBPlan(beadsDir, cfg, isServer, isSharedServer, probeProviders); ok {
		// If the local beadsDir does not exist yet, prefer recovering via sync
		// first. This avoids false "nothing to do" results when the default
		// shared-server database name happens to exist for another project.
		if beadsDirExists || dbAction.Action != "none" {
			return dbAction
		}
		// For synthesized paths with no local workspace directory yet, defer the
		// existing-db no-op until we've ruled out all other recovery paths.
		// This preserves the sync-precedence fix without downgrading the
		// legitimate "database already exists" case into a fresh init.
		plan = dbAction
	}

	// Check for backup JSONL files (must be non-empty to be useful)
	backupDir := filepath.Join(beadsDir, "backup")
	issuesFile := filepath.Join(backupDir, "issues.jsonl")
	if info, err := os.Stat(issuesFile); err == nil && info.Size() > 0 {
		plan.BackupDir = backupDir
		plan.Action = "restore"
		plan.Reason = "Backup files found — will restore from " + backupDir
		return plan
	}

	// Check for git-tracked JSONL (the portable export format)
	gitJSONL := filepath.Join(beadsDir, "issues.jsonl")
	if _, err := os.Stat(gitJSONL); err == nil {
		plan.JSONLFile = gitJSONL
		plan.Action = "jsonl-import"
		plan.Reason = "Git-tracked issues.jsonl found — will import from " + gitJSONL
		return plan
	}

	if plan.Action == "none" {
		return plan
	}

	// Fresh setup
	plan.Action = "init"
	plan.Reason = "No existing database, remote, or backup — will create fresh database"
	return plan
}

func existingBootstrapDBPlan(beadsDir string, cfg *configfile.Config, isServer, isSharedServer, probeProviders bool) (BootstrapPlan, bool) {
	plan := BootstrapPlan{
		BeadsDir: beadsDir,
		Database: cfg.GetDoltDatabase(),
	}

	var dbPath string
	if isServer {
		dbPath = bootstrapServerDoltDir(beadsDir, cfg, isSharedServer)
	} else {
		dbPath = filepath.Join(beadsDir, "embeddeddolt")
	}
	if info, err := os.Stat(dbPath); err != nil || !info.IsDir() {
		return BootstrapPlan{}, false
	}

	entries, _ := os.ReadDir(dbPath)
	if len(entries) == 0 {
		return BootstrapPlan{}, false
	}

	if isServer {
		if !probeProviders {
			plan.HasExisting = true
			plan.Action = "none"
			plan.Reason = fmt.Sprintf("Server data found for %s; live verification is deferred until bootstrap runs without --dry-run", cfg.GetDoltDatabase())
			return plan, true
		}
		probeCfg := bootstrapServerProbeConfig{
			host:     cfg.GetDoltServerHost(),
			port:     bootstrapServerPort(beadsDir, cfg, isSharedServer),
			user:     cfg.GetDoltServerUser(),
			pass:     cfg.GetDoltServerPassword(),
			database: cfg.GetDoltDatabase(),
			tls:      cfg.GetDoltServerTLS(),
		}
		result := checkBootstrapServerDB(probeCfg)
		if result.Err != nil {
			plan.Action = "none"
			plan.Reason = fmt.Sprintf("Could not verify existing server database %s: %v", cfg.GetDoltDatabase(), result.Err)
			return plan, true
		}
		if result.Exists {
			plan.HasExisting = true
			plan.Action = "none"
			plan.Reason = fmt.Sprintf("Database %s already exists on server at %s:%d", probeCfg.database, probeCfg.host, probeCfg.port)
			return plan, true
		}
		return BootstrapPlan{}, false
	}

	plan.HasExisting = true
	plan.Action = "none"
	plan.Reason = "Database already exists at " + dbPath
	return plan, true
}

func bootstrapSharedServerMode(beadsDir string) bool {
	if v := os.Getenv("BEADS_DOLT_SHARED_SERVER"); v == "1" || strings.EqualFold(v, "true") {
		return true
	}
	return strings.EqualFold(config.GetStringFromDir(beadsDir, "dolt.shared-server"), "true")
}

func bootstrapServerDoltDir(beadsDir string, cfg *configfile.Config, isSharedServer bool) string {
	if isSharedServer {
		if dir, err := doltserver.SharedDoltDir(); err == nil {
			return dir
		}
	}

	if d := cfg.GetDoltDataDir(); d != "" {
		if filepath.IsAbs(d) {
			return d
		}
		return filepath.Join(beadsDir, d)
	}

	return filepath.Join(beadsDir, "dolt")
}

func bootstrapServerPort(beadsDir string, cfg *configfile.Config, isSharedServer bool) int {
	if p := os.Getenv("BEADS_DOLT_SERVER_PORT"); p != "" {
		if port, err := strconv.Atoi(p); err == nil && port > 0 {
			return port
		}
	}

	if isSharedServer {
		if sharedDir, err := doltserver.SharedServerDir(); err == nil {
			if port := doltserver.ReadPortFile(sharedDir); port > 0 {
				return port
			}
		}
		return doltserver.DefaultSharedServerPort
	}

	if port := doltserver.ReadPortFile(beadsDir); port > 0 {
		return port
	}

	if p := config.GetStringFromDir(beadsDir, "dolt.port"); p != "" {
		if port, err := strconv.Atoi(p); err == nil && port > 0 {
			return port
		}
	}

	if cfg.DoltServerPort > 0 {
		return cfg.DoltServerPort
	}

	return configfile.DefaultDoltServerPort
}

func printBootstrapPlan(plan BootstrapPlan) {
	switch plan.Action {
	case "none":
		fmt.Printf("✓ Database already exists: %s\n", plan.BeadsDir)
		if !usesSQLServer() {
			fmt.Printf("  Nothing to do.\n")
		} else {
			fmt.Printf("  Nothing to do. Use 'bd doctor' to check health.\n")
		}
	case "sync":
		fmt.Printf("Bootstrap plan: clone from remote\n")
		fmt.Printf("  Remote: %s\n", plan.SyncRemote)
		fmt.Printf("  Database: %s\n", plan.Database)
	case "restore":
		fmt.Printf("Bootstrap plan: restore from backup\n")
		fmt.Printf("  Backup dir: %s\n", plan.BackupDir)
	case "jsonl-import":
		fmt.Printf("Bootstrap plan: import from git-tracked JSONL\n")
		fmt.Printf("  JSONL file: %s\n", plan.JSONLFile)
		fmt.Printf("  Database: %s\n", plan.Database)
	case "init":
		fmt.Printf("Bootstrap plan: create fresh database\n")
		fmt.Printf("  Database: %s\n", plan.Database)
	case "deferred":
		fmt.Println("Bootstrap plan: remote check deferred")
		fmt.Printf("  %s\n", plan.Reason)
	}
}

// confirmPrompt asks the user to confirm an action. Returns true if
// nonInteractive is set, stdin is not a terminal, or the user confirms.
func confirmPrompt(message string, nonInteractive bool) bool {
	if nonInteractive {
		return true
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return true
	}
	fmt.Fprintf(os.Stderr, "%s [Y/n] ", message)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "" || line == "y" || line == "yes"
}

func executeBootstrapPlan(plan BootstrapPlan, cfg *configfile.Config, nonInteractive bool) error {
	if !confirmPrompt("Proceed?", nonInteractive) {
		fmt.Fprintf(os.Stderr, "Aborted.\n")
		return nil
	}

	ctx := context.Background()

	switch plan.Action {
	case "sync":
		return executeSyncAction(ctx, plan, cfg)
	case "restore", "jsonl-import", "init":
		return executeBootstrapLocalActionWithAuthority(ctx, plan, cfg)
	}
	return nil
}

func executeBootstrapLocalActionWithAuthority(ctx context.Context, plan BootstrapPlan, cfg *configfile.Config) (resultErr error) {
	if plan.authority == nil {
		return errBootstrapAuthorityUnavailable
	}
	control, currentCfg, err := acquireBootstrapAuthorityControl(plan.BeadsDir, plan.authority, true)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, control.Close()) }()

	executionCfg := cfg
	if currentCfg != nil {
		executionCfg = currentCfg
	}
	if executionCfg == nil {
		executionCfg = configfile.DefaultConfig()
	}
	if err := validateBootstrapConfigBackend(executionCfg); err != nil {
		return err
	}
	if plan.Database != executionCfg.GetDoltDatabase() {
		return errBootstrapAuthorityChanged
	}

	switch plan.Action {
	case "restore":
		return executeRestoreAction(ctx, plan, executionCfg)
	case "jsonl-import":
		return executeJSONLImportAction(ctx, plan, executionCfg)
	case "init":
		return executeInitAction(ctx, plan, executionCfg)
	default:
		return nil
	}
}

func executeInitAction(ctx context.Context, plan BootstrapPlan, cfg *configfile.Config) error {
	prefix := inferPrefix(cfg)
	dbName := cfg.GetDoltDatabase()

	s, err := newDoltStore(ctx, &dolt.Config{
		Path:            doltserver.ResolveDoltDir(plan.BeadsDir),
		Database:        dbName,
		CreateIfMissing: true,
		AutoStart:       true,
		BeadsDir:        plan.BeadsDir,
	})
	if err != nil {
		return fmt.Errorf("create database: %w", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.SetConfig(ctx, "issue_prefix", prefix); err != nil {
		return fmt.Errorf("set issue prefix: %w", err)
	}
	if err := s.CommitWithConfig(ctx, "bd bootstrap"); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	if !jsonOutput {
		fmt.Fprintf(os.Stderr, "Created fresh database with prefix %q\n", prefix)
	}
	return nil
}

func executeRestoreAction(ctx context.Context, plan BootstrapPlan, cfg *configfile.Config) error {
	prefix := inferPrefix(cfg)
	dbName := cfg.GetDoltDatabase()

	s, err := newDoltStore(ctx, &dolt.Config{
		Path:            doltserver.ResolveDoltDir(plan.BeadsDir),
		Database:        dbName,
		CreateIfMissing: true,
		AutoStart:       true,
		BeadsDir:        plan.BeadsDir,
	})
	if err != nil {
		return fmt.Errorf("create database: %w", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.SetConfig(ctx, "issue_prefix", prefix); err != nil {
		return fmt.Errorf("set issue prefix: %w", err)
	}
	if err := s.CommitWithConfig(ctx, "bd bootstrap: init"); err != nil {
		return fmt.Errorf("commit init: %w", err)
	}

	if err := runBackupRestore(ctx, s, plan.BackupDir, false); err != nil {
		return fmt.Errorf("restore from backup: %w", err)
	}

	if !jsonOutput {
		fmt.Fprintln(os.Stderr, "Restored from backup")
	}
	return nil
}

func executeJSONLImportAction(ctx context.Context, plan BootstrapPlan, cfg *configfile.Config) error {
	prefix := inferPrefix(cfg)
	dbName := cfg.GetDoltDatabase()

	s, err := newDoltStore(ctx, &dolt.Config{
		Path:            doltserver.ResolveDoltDir(plan.BeadsDir),
		Database:        dbName,
		CreateIfMissing: true,
		AutoStart:       true,
		BeadsDir:        plan.BeadsDir,
	})
	if err != nil {
		return fmt.Errorf("create database: %w", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.SetConfig(ctx, "issue_prefix", prefix); err != nil {
		return fmt.Errorf("set issue prefix: %w", err)
	}
	if err := s.CommitWithConfig(ctx, "bd bootstrap: init"); err != nil {
		return fmt.Errorf("commit init: %w", err)
	}

	count, err := importFromLocalJSONL(ctx, s, plan.JSONLFile)
	if err != nil {
		return fmt.Errorf("import from JSONL: %w", err)
	}

	if err := s.Commit(ctx, "bd bootstrap: import from issues.jsonl"); err != nil {
		return fmt.Errorf("commit import: %w", err)
	}

	if !jsonOutput {
		fmt.Fprintf(os.Stderr, "Imported %d issues from %s\n", count, plan.JSONLFile)
	}
	return nil
}

func executeSyncAction(ctx context.Context, plan BootstrapPlan, cfg *configfile.Config) (resultErr error) {
	// Confirmation and plan output have already happened. Revalidate the exact
	// backend authority under migration control immediately before any provider
	// or retained-Dolt effect, and keep that control through finalization and
	// warmup. A changed plan is refused rather than silently retargeted.
	if plan.authority == nil {
		return errBootstrapAuthorityUnavailable
	}
	control, currentCfg, err := acquireBootstrapAuthorityControl(plan.BeadsDir, plan.authority, true)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, control.Close())
	}()
	executionCfg := cfg
	if currentCfg != nil {
		executionCfg = currentCfg
	}
	if executionCfg == nil {
		executionCfg = configfile.DefaultConfig()
	}
	if err := validateBootstrapConfigBackend(executionCfg); err != nil {
		return err
	}
	dbName := executionCfg.GetDoltDatabase()
	if plan.Database != dbName {
		return errBootstrapAuthorityChanged
	}
	remoteURL := plan.rawSyncRemote()
	if err := cloneBootstrapRemote(ctx, plan.BeadsDir, remoteURL, dbName, executionCfg); err != nil {
		return &bootstrapCloneFailure{cause: err}
	}

	prepareSyncedBootstrapConfig(executionCfg, dbName)
	if err := finalizeSyncedBootstrapFiles(plan.BeadsDir, remoteURL); err != nil {
		return err
	}

	// Open and close the store to ensure dolt_ignore'd wisp tables are
	// created in the working set. Clone does not include these tables
	// (they are never committed), so they must be recreated after clone.
	// Both embedded and server mode handle this in their store init paths.
	var postCloneErr error
	err = warmupSyncedBootstrap(ctx, plan, executionCfg, dbName)
	if err != nil {
		// #4259: the cloned remote is behind this binary, so the remote-migrate
		// gate held migration for an explicit operator decision. Surface that
		// now with bootstrap-specific guidance and a non-zero exit. Returning
		// silent success here (as this path once did) sent operators in a
		// loop: the first real command failed with the gate message, whose
		// generic "adopt" remedy is `bd bootstrap` — which re-clones the same
		// behind database and silently "succeeds" again (bd-6dnrw.31).
		var gateErr *schema.RemoteMigrateGateError
		if errors.As(err, &gateErr) {
			if !jsonOutput {
				printBootstrapRemoteBehindGuidance(os.Stderr, gateErr, "bd bootstrap")
			}
			unit := "migrations"
			if gateErr.Pending == 1 {
				unit = "migration"
			}
			postCloneErr = fmt.Errorf("clone from the configured remote succeeded, but the database needs %d schema %s (v%d -> v%d) that bd will not auto-apply to a remote-backed database (#4259)",
				gateErr.Pending, unit, gateErr.CurrentVersion, gateErr.LatestVersion)
		} else {
			// Non-fatal: wisp tables will be created on the next command that
			// opens the store. Warn so the user knows to retry if they hit
			// "table not found: wisp_*" errors.
			if !jsonOutput {
				fmt.Fprintf(os.Stderr, "Warning: post-clone store init failed (wisp tables may be missing): %v\n", err)
			}
		}
	}

	// Publish target metadata last. Config.Save acquires target control itself,
	// so release only that guard. Parent-derived plans keep the owning parent's
	// control through publication; target-owned plans retain an exact source
	// witness that makes Config.Save reject a handoff race.
	if err := control.release(plan.BeadsDir); err != nil {
		return err
	}
	if err := executionCfg.Save(plan.BeadsDir); err != nil {
		return fmt.Errorf("write metadata.json: %w", err)
	}
	return postCloneErr
}

func warmupSyncedBootstrapStore(ctx context.Context, plan BootstrapPlan, cfg *configfile.Config, dbName string) error {
	mode := resolveRemoteCloneMode(plan.BeadsDir, cfg, remoteCloneAuto)
	storeCfg := &dolt.Config{
		Path:           doltserver.ResolveDoltDir(plan.BeadsDir),
		BeadsDir:       plan.BeadsDir,
		Database:       dbName,
		ServerMode:     mode == remoteCloneExternalServer,
		ServerSocket:   cfg.GetDoltServerSocket(),
		ServerHost:     cfg.GetDoltServerHost(),
		ServerPort:     serverClonePort(plan.BeadsDir, cfg),
		ServerUser:     cfg.GetDoltServerUser(),
		ServerPassword: cfg.GetDoltServerPasswordForPort(serverClonePort(plan.BeadsDir, cfg)),
		ServerTLS:      cfg.GetDoltServerTLS(),
	}
	store, err := newDoltStore(ctx, storeCfg)
	if err != nil {
		return err
	}
	configureInitDoltRemote(ctx, store, plan.rawSyncRemote(), false)
	return store.Close()
}

// printBootstrapRemoteBehindGuidance explains a remote-migrate gate refusal in
// bootstrap terms. The gate's generic remedy ("adopt the migrated database:
// bd bootstrap") is wrong from inside a bootstrap-style clone — the database
// was just cloned from the remote, so the REMOTE is what is behind this binary
// and re-cloning can never help. The way out is exactly one designated machine
// migrating and pushing. rerunCmd is the command the operator just ran ("bd
// bootstrap", "bd init") so the don't-bother-retrying line names it.
func printBootstrapRemoteBehindGuidance(w io.Writer, e *schema.RemoteMigrateGateError, rerunCmd string) {
	unit := "migrations"
	if e.Pending == 1 {
		unit = "migration"
	}
	fmt.Fprintf(w, "\nThe database cloned from the configured remote needs %d schema %s (v%d -> v%d).\n",
		e.Pending, unit, e.CurrentVersion, e.LatestVersion)
	fmt.Fprint(w,
		"  bd will not migrate it automatically: migrating clones independently forks\n"+
			"  the schema so `bd dolt pull` can no longer merge (#4259).\n"+
			"\n"+
			"  Re-running `"+rerunCmd+"` will NOT fix this — the remote itself is behind.\n"+
			"  Choose one:\n"+
			"    • This machine is the designated migrator (exactly ONE machine should be):\n"+
			"        bd migrate --force\n"+
			"        bd dolt push\n"+
			"      then other machines re-run `bd bootstrap` to adopt the migrated database.\n"+
			"    • Another machine is the designated migrator: wait for it to push, then\n"+
			"      re-run `bd bootstrap`, or keep using a bd version that matches the remote.\n\n")
}

// finalizeSyncedBootstrap writes metadata.json and config.yaml after a
// successful sync clone, matching the on-disk layout that bd init produces.
// It is idempotent: re-running over an already-finalized workspace leaves
// existing files intact (createConfigYaml skips if config.yaml exists; the
// metadata.json write is a full rewrite that preserves caller fields).
func finalizeSyncedBootstrap(beadsDir, syncRemote string, cfg *configfile.Config, dbName string) error {
	prepareSyncedBootstrapConfig(cfg, dbName)
	if err := cfg.Save(beadsDir); err != nil {
		return fmt.Errorf("write metadata.json: %w", err)
	}
	return finalizeSyncedBootstrapFiles(beadsDir, syncRemote)
}

func prepareSyncedBootstrapConfig(cfg *configfile.Config, dbName string) {
	// Preserve whatever upstream fields were already set in cfg (which may
	// be DefaultConfig when metadata.json was absent, or a parent workspace
	// config propagated by findParentConfig), then fill in the bits
	// required by configfile.Load consumers.
	cfg.Backend = configfile.BackendDolt
	cfg.DoltDatabase = dbName
	switch {
	case cfg.IsDoltProxiedServerMode():
		cfg.DoltMode = configfile.DoltModeProxiedServer
	case cfg.IsDoltServerMode() || doltserver.IsSharedServerMode():
		cfg.DoltMode = configfile.DoltModeServer
	default:
		cfg.DoltMode = configfile.DoltModeEmbedded
	}
	// Mirror init's convention: metadata.json database points at the Dolt
	// directory rather than the legacy "beads.db" placeholder.
	if cfg.Database == "" || cfg.Database == beads.CanonicalDatabaseName {
		cfg.Database = "dolt"
	}
}

func finalizeSyncedBootstrapFiles(beadsDir, syncRemote string) error {
	if err := createConfigYaml(beadsDir, false, ""); err != nil {
		return fmt.Errorf("create config.yaml: %w", err)
	}
	if err := doctor.EnsureGitignoreForBeadsDir(beadsDir); err != nil {
		return fmt.Errorf("ensure .beads/.gitignore: %w", err)
	}

	// Persist sync.remote so subsequent fresh clones (and bd bootstrap
	// retries) can rediscover the remote without re-probing origin refs.
	if syncRemote != "" {
		if err := config.SetYamlConfigInDir(beadsDir, "sync.remote", syncRemote); err != nil {
			return fmt.Errorf("persist sync.remote to config.yaml: %w", err)
		}
	}

	return nil
}

type remoteCloneMode int

const (
	remoteCloneAuto remoteCloneMode = iota
	remoteCloneEmbedded
	remoteCloneExternalServer
	remoteCloneCLI
)

// cloneFromRemote clones a Dolt database from a remote URL.
// In embedded mode, uses the embedded engine's DOLT_CLONE procedure.
// In external server mode, connects to the running server via MySQL and
// executes DOLT_CLONE so the server places the database in its own data
// directory. In owned-server mode, shells out to dolt clone via
// BootstrapFromRemoteWithDB.
// Shared by bd init and bd bootstrap to keep clone logic in one place.
func cloneFromRemote(ctx context.Context, beadsDir, remoteURL, dbName string, cfg *configfile.Config) error {
	return cloneFromRemoteWithMode(ctx, beadsDir, remoteURL, dbName, cfg, remoteCloneAuto)
}

func cloneFromRemoteWithMode(ctx context.Context, beadsDir, remoteURL, dbName string, cfg *configfile.Config, cloneMode remoteCloneMode) error {
	mode := resolveRemoteCloneMode(beadsDir, cfg, cloneMode)

	switch mode {
	case remoteCloneEmbedded:
		return cloneViaEmbedded(ctx, beadsDir, remoteURL, dbName)

	case remoteCloneExternalServer:
		if cfg == nil {
			// Caller didn't provide config; fall back to loading from disk.
			if loaded, err := configfile.Load(beadsDir); err == nil && loaded != nil {
				cfg = loaded
			}
		}
		if cfg != nil {
			return cloneViaServer(ctx, beadsDir, remoteURL, dbName, cfg)
		}
		// No config available — fall through to CLI clone.
		if !jsonOutput {
			fmt.Fprintln(os.Stderr, "Warning: server mode detected but no config available, falling back to CLI clone")
		}
		return cloneViaCLI(ctx, beadsDir, remoteURL, dbName)

	default:
		return cloneViaCLI(ctx, beadsDir, remoteURL, dbName)
	}
}

func resolveRemoteCloneMode(beadsDir string, cfg *configfile.Config, cloneMode remoteCloneMode) remoteCloneMode {
	if cloneMode != remoteCloneAuto {
		return cloneMode
	}

	if cfg != nil {
		if cfg.IsDoltServerMode() || doltserver.IsSharedServerMode() || os.Getenv("BEADS_DOLT_SERVER_MODE") == "1" {
			return remoteCloneExternalServer
		}
		return remoteCloneEmbedded
	}

	switch doltserver.ResolveServerMode(beadsDir) {
	case doltserver.ServerModeEmbedded:
		return remoteCloneEmbedded
	case doltserver.ServerModeExternal:
		return remoteCloneExternalServer
	default:
		return remoteCloneCLI
	}
}

// cloneViaEmbedded clones using the embedded Dolt engine (CGO required).
func cloneViaEmbedded(ctx context.Context, beadsDir, remoteURL, dbName string) error {
	dataDir := filepath.Join(beadsDir, "embeddeddolt")
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return fmt.Errorf("create embeddeddolt directory: %w", err)
	}
	db, cleanup, err := embeddeddolt.OpenSQL(ctx, dataDir, "", "")
	if err != nil {
		return fmt.Errorf("open embedded engine for clone: %w", err)
	}
	defer func() { _ = cleanup() }()

	if err := versioncontrolops.DoltClone(ctx, db, remoteURL, dbName, os.Getenv("DOLT_REMOTE_USER")); err != nil {
		return fmt.Errorf("clone from remote: %w", err)
	}
	printBootstrapCloneSuccess(os.Stderr, "")
	return nil
}

// cloneViaServer clones by connecting to the external Dolt server and
// executing CALL DOLT_CLONE. The server places the database in its own
// data directory, which is the correct behavior for externally managed
// servers where bd does not know the filesystem layout.
func cloneViaServer(ctx context.Context, beadsDir, remoteURL, dbName string, cfg *configfile.Config) error {
	port := serverClonePort(beadsDir, cfg)
	dsn := doltutil.ServerDSN{
		Socket:   cfg.GetDoltServerSocket(),
		Host:     cfg.GetDoltServerHost(),
		Port:     port,
		User:     cfg.GetDoltServerUser(),
		Password: cfg.GetDoltServerPasswordForPort(port),
		TLS:      cfg.GetDoltServerTLS(),
		// No Database — DOLT_CLONE creates the database.
	}.String()

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("connect to dolt server for clone: %w", err)
	}
	defer db.Close()

	cloneCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	if err := db.PingContext(cloneCtx); err != nil {
		return fmt.Errorf("dolt server unreachable at %s:%d (is dolt sql-server running?): %w",
			cfg.GetDoltServerHost(), port, err)
	}

	if err := versioncontrolops.DoltClone(cloneCtx, db, remoteURL, dbName, os.Getenv("DOLT_REMOTE_USER")); err != nil {
		return fmt.Errorf("clone from remote via server: %w", err)
	}
	printBootstrapCloneSuccess(os.Stderr, fmt.Sprintf("%s:%d", cfg.GetDoltServerHost(), port))
	return nil
}

func serverClonePort(beadsDir string, cfg *configfile.Config) int {
	if cfg != nil && cfg.DoltServerPort > 0 {
		return cfg.DoltServerPort
	}
	if p := os.Getenv("BEADS_DOLT_SERVER_PORT"); p != "" {
		if port, err := strconv.Atoi(p); err == nil && port > 0 {
			return port
		}
	}
	if p := os.Getenv("BEADS_DOLT_PORT"); p != "" {
		if port, err := strconv.Atoi(p); err == nil && port > 0 {
			return port
		}
	}
	if resolved := doltserver.DefaultConfig(beadsDir); resolved.Port > 0 {
		return resolved.Port
	}
	if cfg != nil {
		return cfg.GetDoltServerPort()
	}
	return configfile.DefaultDoltServerPort
}

// cloneViaCLI clones by shelling out to the dolt CLI.
// Used for owned-server mode where bd manages the server lifecycle.
func cloneViaCLI(ctx context.Context, beadsDir, remoteURL, dbName string) error {
	doltDir := doltserver.ResolveDoltDir(beadsDir)
	synced, err := dolt.BootstrapFromRemoteWithDB(ctx, doltDir, remoteURL, dbName)
	if err != nil {
		return fmt.Errorf("sync from remote: %w", err)
	}
	if synced {
		printBootstrapCloneSuccess(os.Stderr, "")
	}
	return nil
}

func printBootstrapCloneSuccess(w io.Writer, serverAddress string) {
	if jsonOutput {
		return
	}
	if serverAddress != "" {
		fmt.Fprintf(w, "Synced database from configured remote (via server at %s)\n", serverAddress)
		return
	}
	fmt.Fprintln(w, "Synced database from configured remote")
}

func inferPrefix(cfg *configfile.Config) string {
	db := cfg.GetDoltDatabase()
	if db != "" && db != "beads" {
		return db
	}
	cwd, _ := os.Getwd()
	return filepath.Base(cwd)
}

// isNonInteractiveBootstrap returns true if bootstrap should skip confirmation prompts.
// Precedence: explicit flag > BD_NON_INTERACTIVE env > CI env > terminal detection.
func isNonInteractiveBootstrap(flagValue bool) bool {
	if flagValue {
		return true
	}
	if v := os.Getenv("BD_NON_INTERACTIVE"); v == "1" || v == "true" {
		return true
	}
	if v := os.Getenv("CI"); v == "true" || v == "1" {
		return true
	}
	return !term.IsTerminal(int(os.Stdin.Fd()))
}

// findParentConfig walks up from beadsDir's parent looking for a
// .beads/metadata.json in ancestor directories. This handles the case where a
// rig subdirectory (its own git repo) doesn't have a local .beads but its
// parent workspace does. Invalid nearer metadata is an authority boundary: it
// is returned as an error rather than skipped in favor of a farther ancestor.
func findParentConfig(beadsDir string) (*configfile.Config, error) {
	cfg, _, err := findParentConfigWithOwner(beadsDir)
	return cfg, err
}

func findParentConfigWithOwner(beadsDir string) (*configfile.Config, string, error) {
	// Start from the parent of beadsDir's enclosing directory.
	// beadsDir is typically "<project>/.beads", so we start from <project>'s parent.
	start := filepath.Dir(filepath.Dir(beadsDir))
	homeDir, _ := os.UserHomeDir()

	for dir := start; dir != "/" && dir != "."; {
		candidate := filepath.Join(dir, ".beads")
		cfg, err := configfile.LoadAuthoritativeReadOnly(candidate)
		if err != nil {
			return nil, "", err
		}
		if cfg != nil {
			return cfg, candidate, nil
		}

		// Don't search above $HOME
		if homeDir != "" && dir == homeDir {
			break
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil, "", nil
}

func init() {
	bootstrapCmd.Flags().Bool("dry-run", false, "Show what would be done without doing it")
	bootstrapCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompts (for CI/automation)")
	bootstrapCmd.Flags().Bool("non-interactive", false, "Alias for --yes")
	rootCmd.AddCommand(bootstrapCmd)
}

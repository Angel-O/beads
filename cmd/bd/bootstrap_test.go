//go:build cgo

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backendmigrationcontrol "github.com/steveyegge/beads/internal/backendmigration/control"
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/schema"
)

func snapshotBootstrapEnv(t *testing.T) func() {
	t.Helper()
	saved := make(map[string]string)
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "BD_") || strings.HasPrefix(env, "BEADS_") {
			parts := strings.SplitN(env, "=", 2)
			key := parts[0]
			saved[key] = os.Getenv(key)
			_ = os.Unsetenv(key)
		}
	}
	return func() {
		for _, env := range os.Environ() {
			if strings.HasPrefix(env, "BD_") || strings.HasPrefix(env, "BEADS_") {
				parts := strings.SplitN(env, "=", 2)
				_ = os.Unsetenv(parts[0])
			}
		}
		for key, val := range saved {
			_ = os.Setenv(key, val)
		}
	}
}

func TestDetectBootstrapAction_NoneWhenDatabaseExists(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// Create embeddeddolt directory with content so it's detected as existing.
	// Default config uses embedded mode, so the detection logic looks for
	// beadsDir/embeddeddolt (not beadsDir/dolt).
	embeddedDir := filepath.Join(beadsDir, "embeddeddolt")
	if err := os.MkdirAll(filepath.Join(embeddedDir, "beads"), 0o750); err != nil {
		t.Fatal(err)
	}

	// Run from tmpDir so auto-detect doesn't find parent git repo
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	cfg := configfile.DefaultConfig()
	plan := detectBootstrapAction(beadsDir, cfg)

	if plan.Action != "none" {
		t.Errorf("action = %q, want %q", plan.Action, "none")
	}
	if !plan.HasExisting {
		t.Error("HasExisting = false, want true")
	}
}

func TestDetectBootstrapAction_RestoreWhenBackupExists(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	backupDir := filepath.Join(beadsDir, "backup")
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "issues.jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run from tmpDir so auto-detect doesn't find parent git repo
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	cfg := configfile.DefaultConfig()
	plan := detectBootstrapAction(beadsDir, cfg)

	if plan.Action != "restore" {
		t.Errorf("action = %q, want %q", plan.Action, "restore")
	}
	if plan.BackupDir != backupDir {
		t.Errorf("BackupDir = %q, want %q", plan.BackupDir, backupDir)
	}
}

func TestDetectBootstrapAction_InitWhenNothingExists(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// Run from the tmpDir so auto-detect doesn't find a git repo
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	cfg := configfile.DefaultConfig()
	plan := detectBootstrapAction(beadsDir, cfg)

	if plan.Action != "init" {
		t.Errorf("action = %q, want %q", plan.Action, "init")
	}
}

func TestNoWorkspaceBootstrapPayload(t *testing.T) {
	payload := noWorkspaceBootstrapPayload()

	if got := payload["action"]; got != "none" {
		t.Fatalf("action = %v, want %q", got, "none")
	}
	if got := payload["reason"]; got != activeWorkspaceNotFoundError() {
		t.Fatalf("reason = %v, want %q", got, activeWorkspaceNotFoundError())
	}
	if got := payload["suggestion"]; got != diagHint() {
		t.Fatalf("suggestion = %v, want %q", got, diagHint())
	}
}

func TestDetectBootstrapAction_ServerModeMissingConfiguredDBDoesNotReturnNone(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	sharedDir := filepath.Join(tmpDir, "shared-dolt")
	if err := os.MkdirAll(filepath.Join(sharedDir, "hq"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedDir, "dolt-server.port"), []byte("3311"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	cfg := configfile.DefaultConfig()
	cfg.DoltMode = configfile.DoltModeServer
	cfg.DoltDatabase = "project_missing"
	cfg.DoltDataDir = sharedDir
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "1")
	t.Setenv("BEADS_DOLT_DATA_DIR", sharedDir)

	origCheck := checkBootstrapServerDB
	checkBootstrapServerDB = func(probeCfg bootstrapServerProbeConfig) bootstrapServerDBCheck {
		if probeCfg.database != "project_missing" {
			t.Fatalf("unexpected dbName: %s", probeCfg.database)
		}
		if probeCfg.port != 3311 {
			t.Fatalf("expected resolved server port 3311, got %d", probeCfg.port)
		}
		return bootstrapServerDBCheck{Exists: false, Reachable: true}
	}
	defer func() { checkBootstrapServerDB = origCheck }()

	plan := detectBootstrapAction(beadsDir, cfg)
	if plan.Action == "none" {
		t.Fatalf("expected bootstrap to continue recovery when configured server DB is missing, got plan %#v", plan)
	}
	if plan.Action != "init" {
		t.Fatalf("expected init fallback when no remote/backup/jsonl exists, got %q", plan.Action)
	}
}

func TestDetectBootstrapAction_ServerModeProbeErrorStopsWithReason(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	sharedDir := filepath.Join(tmpDir, "shared-dolt")
	if err := os.MkdirAll(filepath.Join(sharedDir, "hq"), 0o750); err != nil {
		t.Fatal(err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	cfg := configfile.DefaultConfig()
	cfg.DoltMode = configfile.DoltModeServer
	cfg.DoltDatabase = "project_missing"
	cfg.DoltDataDir = sharedDir
	t.Setenv("BEADS_DOLT_DATA_DIR", sharedDir)

	origCheck := checkBootstrapServerDB
	checkBootstrapServerDB = func(probeCfg bootstrapServerProbeConfig) bootstrapServerDBCheck {
		return bootstrapServerDBCheck{Reachable: true, Err: fmt.Errorf("permission denied")}
	}
	defer func() { checkBootstrapServerDB = origCheck }()

	plan := detectBootstrapAction(beadsDir, cfg)
	if plan.Action != "none" {
		t.Fatalf("expected bootstrap to stop when server probe errors, got %#v", plan)
	}
	if !strings.Contains(plan.Reason, "permission denied") {
		t.Fatalf("expected probe error in plan reason, got %#v", plan)
	}
}

func TestCheckBootstrapServerDB_HonorsTLSFlagInDSN(t *testing.T) {
	probeCfg := bootstrapServerProbeConfig{
		host:     "127.0.0.1",
		port:     1,
		user:     "root",
		database: "beads",
		tls:      true,
	}

	result := checkBootstrapServerDB(probeCfg)
	if result.Reachable {
		t.Fatal("expected unreachable test connection")
	}
	if result.Err == nil {
		t.Fatal("expected connection error for unreachable test host")
	}
}

func TestDetectBootstrapAction_SyncWhenOriginHasDoltRef(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	// Create a bare repo with a refs/dolt/data ref
	bareDir := filepath.Join(t.TempDir(), "bare.git")
	runGitForBootstrapTest(t, "", "init", "--bare", bareDir)

	// Create a source repo, commit, push, then create the dolt ref
	sourceDir := t.TempDir()
	runGitForBootstrapTest(t, sourceDir, "init", "-b", "main")
	runGitForBootstrapTest(t, sourceDir, "config", "user.email", "test@test.com")
	runGitForBootstrapTest(t, sourceDir, "config", "user.name", "Test User")
	runGitForBootstrapTest(t, sourceDir, "commit", "--allow-empty", "-m", "init")
	runGitForBootstrapTest(t, sourceDir, "remote", "add", "origin", bareDir)
	runGitForBootstrapTest(t, sourceDir, "push", "origin", "HEAD:refs/dolt/data")
	// Create refs/dolt/data by pushing HEAD to that ref
	runGitForBootstrapTest(t, sourceDir, "push", "origin", "HEAD:refs/dolt/data")

	// Create a "clone" repo with origin pointing at the bare repo
	cloneDir := t.TempDir()
	runGitForBootstrapTest(t, cloneDir, "init", "-b", "main")
	runGitForBootstrapTest(t, cloneDir, "remote", "add", "origin", bareDir)

	beadsDir := filepath.Join(cloneDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(cloneDir); err != nil {
		t.Fatal(err)
	}

	cfg := configfile.DefaultConfig()
	plan := detectBootstrapAction(beadsDir, cfg)

	if plan.Action != "sync" {
		t.Errorf("action = %q, want %q", plan.Action, "sync")
	}
	if plan.SyncRemote == "" {
		t.Error("SyncRemote is empty, expected git+ prefixed URL")
	}
}

func TestDetectBootstrapAction_ExplicitSyncRemotePreservesRemotesAPIURL(t *testing.T) {
	restore := snapshotBootstrapEnv(t)
	defer restore()
	config.ResetForTesting()
	defer config.ResetForTesting()

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	const syncRemote = "http://myserver:7007/mydb"
	if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"), []byte("sync.remote: "+syncRemote+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BEADS_DIR", beadsDir)
	t.Setenv("BEADS_TEST_IGNORE_REPO_CONFIG", "1")
	if err := config.Initialize(); err != nil {
		t.Fatalf("config.Initialize failed: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	cfg := configfile.DefaultConfig()
	plan := detectBootstrapAction(beadsDir, cfg)

	if plan.Action != "sync" {
		t.Errorf("action = %q, want %q", plan.Action, "sync")
	}
	if plan.SyncRemote != syncRemote {
		t.Errorf("SyncRemote = %q, want unnormalized explicit sync.remote %q", plan.SyncRemote, syncRemote)
	}
}

func TestDetectBootstrapActionRedactsCredentialsFromVisiblePlan(t *testing.T) {
	restore := snapshotBootstrapEnv(t)
	defer restore()
	config.ResetForTesting()
	defer config.ResetForTesting()

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	const secret = "plan-secret"
	const syncRemote = "https://operator:" + secret + "@provider.example/org/beads?token=" + secret
	if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"), []byte("sync.remote: "+syncRemote+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BEADS_DIR", beadsDir)
	t.Setenv("BEADS_TEST_IGNORE_REPO_CONFIG", "1")
	if err := config.Initialize(); err != nil {
		t.Fatalf("config.Initialize: %v", err)
	}

	plan := detectBootstrapAction(beadsDir, configfile.DefaultConfig())
	visible, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(visible, []byte(secret)) || bytes.Contains(visible, []byte("operator@")) {
		t.Fatalf("bootstrap plan exposed remote credentials: %s", visible)
	}
	if !bytes.Contains(visible, []byte("provider.example")) {
		t.Fatalf("bootstrap plan lost useful remote identity: %s", visible)
	}
}

func TestDetectBootstrapAction_InitWhenOriginHasNoDoltRef(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	// Create a bare repo without refs/dolt/data
	bareDir := filepath.Join(t.TempDir(), "bare.git")
	runGitForBootstrapTest(t, "", "init", "--bare", bareDir)

	cloneDir := t.TempDir()
	runGitForBootstrapTest(t, cloneDir, "init", "-b", "main")
	runGitForBootstrapTest(t, cloneDir, "remote", "add", "origin", bareDir)

	beadsDir := filepath.Join(cloneDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(cloneDir); err != nil {
		t.Fatal(err)
	}

	cfg := configfile.DefaultConfig()
	plan := detectBootstrapAction(beadsDir, cfg)

	if plan.Action != "init" {
		t.Errorf("action = %q, want %q (no dolt ref on origin)", plan.Action, "init")
	}
}

// TestBootstrapFreshCloneDetectsRemote verifies that when .beads does NOT
// exist but origin has refs/dolt/data, the bootstrap handler's remote-probe
// logic synthesizes beadsDir and detectBootstrapAction produces a "sync"
// plan instead of the handler exiting with "No .beads directory found".
// This is the core fix for GH#2792.
func TestBootstrapFreshCloneDetectsRemote(t *testing.T) {
	// Create a bare repo and push a fake refs/dolt/data ref to it.
	bareDir := filepath.Join(t.TempDir(), "bare.git")
	runGitForBootstrapTest(t, "", "init", "--bare", bareDir)

	sourceDir := t.TempDir()
	runGitForBootstrapTest(t, sourceDir, "init", "-b", "main")
	runGitForBootstrapTest(t, sourceDir, "config", "user.email", "test@test.com")
	runGitForBootstrapTest(t, sourceDir, "config", "user.name", "Test User")
	runGitForBootstrapTest(t, sourceDir, "commit", "--allow-empty", "-m", "init")
	runGitForBootstrapTest(t, sourceDir, "remote", "add", "origin", bareDir)
	runGitForBootstrapTest(t, sourceDir, "push", "origin", "HEAD:refs/dolt/data")
	runGitForBootstrapTest(t, sourceDir, "push", "origin", "HEAD:refs/dolt/data")

	// Clone into a fresh directory — no .beads exists.
	cloneDir := t.TempDir()
	runGitForBootstrapTest(t, cloneDir, "init", "-b", "main")
	runGitForBootstrapTest(t, cloneDir, "remote", "add", "origin", bareDir)

	// Verify .beads does NOT exist.
	beadsDir := filepath.Join(cloneDir, ".beads")
	if _, err := os.Stat(beadsDir); err == nil {
		t.Fatal(".beads should not exist before bootstrap")
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(cloneDir); err != nil {
		t.Fatal(err)
	}

	// Replicate the Run handler's remote-probe logic: when beadsDir is
	// empty, check origin for refs/dolt/data and synthesize beadsDir.
	// This exercises the same code path the handler uses before calling
	// detectBootstrapAction.
	if !isGitRepo() {
		t.Fatal("expected to be in a git repo")
	}
	originURL, err := gitOriginGetURL()
	if err != nil || originURL == "" {
		t.Fatalf("expected origin URL, got err=%v url=%q", err, originURL)
	}
	if !gitOriginHasDoltDataRef() {
		t.Fatal("expected origin to have refs/dolt/data")
	}

	// Synthesize beadsDir the same way the handler does, then feed it
	// through detectBootstrapAction — the single code path for plan building.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	synthesizedDir := filepath.Join(cwd, ".beads")
	cfg := configfile.DefaultConfig()
	plan := detectBootstrapAction(synthesizedDir, cfg)

	if plan.Action != "sync" {
		t.Errorf("action = %q, want %q", plan.Action, "sync")
	}
	if plan.SyncRemote == "" {
		t.Error("SyncRemote should not be empty")
	}
	if plan.BeadsDir != synthesizedDir {
		t.Errorf("BeadsDir = %q, want %q", plan.BeadsDir, synthesizedDir)
	}
}

// TestBootstrapFreshCloneNoRemoteData verifies that when .beads does NOT exist
// and origin has NO refs/dolt/data, bootstrap correctly reports no data found
// (does not create .beads or crash).
func TestBootstrapFreshCloneNoRemoteData(t *testing.T) {
	// Create a bare repo WITHOUT refs/dolt/data.
	bareDir := filepath.Join(t.TempDir(), "bare.git")
	runGitForBootstrapTest(t, "", "init", "--bare", bareDir)

	cloneDir := t.TempDir()
	runGitForBootstrapTest(t, cloneDir, "init", "-b", "main")
	runGitForBootstrapTest(t, cloneDir, "remote", "add", "origin", bareDir)

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(cloneDir); err != nil {
		t.Fatal(err)
	}

	// When no .beads and no remote data, the remote probe should return false.
	if !isGitRepo() {
		t.Fatal("expected to be in a git repo")
	}
	if gitOriginHasDoltDataRef() {
		t.Fatal("origin should NOT have refs/dolt/data")
	}

	// .beads should still not exist after detection.
	beadsDir := filepath.Join(cloneDir, ".beads")
	if _, err := os.Stat(beadsDir); err == nil {
		t.Fatal(".beads should not be created when remote has no data")
	}
}

// TestBootstrapExistingBeadsDirUnchanged verifies that when .beads already
// exists, the normal bootstrap flow is unaffected by the fresh-clone fix.
// TestDetectBootstrapAction_PlanUsesConfiguredDatabaseName verifies that
// detectBootstrapAction carries the configured dolt_database into the plan,
// rather than silently falling back to the default "beads". This is the
// core regression test for GH#3029.
func TestDetectBootstrapAction_PlanUsesConfiguredDatabaseName(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	cfg := configfile.DefaultConfig()
	cfg.DoltDatabase = "my_project_db"

	plan := detectBootstrapAction(beadsDir, cfg)

	if plan.Database != "my_project_db" {
		t.Errorf("plan.Database = %q, want %q; bootstrap must use the configured database name, not the default",
			plan.Database, "my_project_db")
	}
}

// TestDetectBootstrapAction_PlanDefaultDatabaseWhenNotConfigured verifies
// that the default "beads" is used when no dolt_database is configured.
func TestDetectBootstrapAction_PlanDefaultDatabaseWhenNotConfigured(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	cfg := configfile.DefaultConfig()
	plan := detectBootstrapAction(beadsDir, cfg)

	if plan.Database != configfile.DefaultDoltDatabase {
		t.Errorf("plan.Database = %q, want %q (default)", plan.Database, configfile.DefaultDoltDatabase)
	}
}

// TestDetectBootstrapAction_ServerModePlanUsesConfiguredDatabaseName verifies
// that in server mode, the plan carries the configured database name for
// both the plan.Database field and the server probe. This is the specific
// failure mode reported in GH#3029: when FindBeadsDir resolved to the wrong
// .beads/, the config had no dolt_database, and the plan fell back to "beads".
func TestDetectBootstrapAction_ServerModePlanUsesConfiguredDatabaseName(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// Create a dolt data dir with a subdirectory so the existing-DB check fires.
	// Use BEADS_DOLT_DATA_DIR (not shared server mode) so ResolveDoltDir
	// returns our test directory instead of ~/.beads/shared-server/dolt/.
	doltDataDir := filepath.Join(tmpDir, "dolt-data")
	if err := os.MkdirAll(filepath.Join(doltDataDir, "myrig"), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BEADS_DOLT_DATA_DIR", doltDataDir)

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	cfg := configfile.DefaultConfig()
	cfg.DoltMode = configfile.DoltModeServer
	cfg.DoltDatabase = "myrig"
	cfg.DoltDataDir = doltDataDir

	var probedDBName string
	origCheck := checkBootstrapServerDB
	checkBootstrapServerDB = func(probeCfg bootstrapServerProbeConfig) bootstrapServerDBCheck {
		probedDBName = probeCfg.database
		return bootstrapServerDBCheck{Exists: false, Reachable: true}
	}
	defer func() { checkBootstrapServerDB = origCheck }()

	plan := detectBootstrapAction(beadsDir, cfg)

	if plan.Database != "myrig" {
		t.Errorf("plan.Database = %q, want %q", plan.Database, "myrig")
	}
	if probedDBName != "myrig" {
		t.Errorf("server probe used database %q, want %q; bootstrap must probe the configured database, not the default",
			probedDBName, "myrig")
	}
}

func TestBootstrapExistingBeadsDirUnchanged(t *testing.T) {
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	// With .beads present but empty, detectBootstrapAction should return "init".
	cfg := configfile.DefaultConfig()
	plan := detectBootstrapAction(beadsDir, cfg)
	if plan.Action != "init" {
		t.Errorf("action = %q, want %q for existing empty .beads", plan.Action, "init")
	}
}

// TestDetectBootstrapAction_ServerModeUsesCustomDatabaseName verifies that when
// metadata.json has dolt_database set to a custom name (e.g. "my_rig"),
// detectBootstrapAction uses that name in the plan instead of the default "beads".
// This is the core fix for GH#3029.
func TestDetectBootstrapAction_ServerModeUsesCustomDatabaseName(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// Write metadata.json with a custom dolt_database name
	metadataJSON := `{"dolt_mode": "server", "dolt_database": "my_rig"}`
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(metadataJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	// Load config the same way bootstrap.go does (lines 172-174)
	cfg, err := configfile.Load(beadsDir)
	if err != nil || cfg == nil {
		cfg = configfile.DefaultConfig()
	}

	// Verify config loaded the custom database name
	if got := cfg.GetDoltDatabase(); got != "my_rig" {
		t.Fatalf("GetDoltDatabase() = %q, want %q (metadata.json dolt_database ignored)", got, "my_rig")
	}

	plan := detectBootstrapAction(beadsDir, cfg)

	// The plan should use the custom database name, not "beads"
	if plan.Database != "my_rig" {
		t.Errorf("plan.Database = %q, want %q", plan.Database, "my_rig")
	}
}

// TestDetectBootstrapAction_FreshCloneUsesMetadataDBName verifies that when
// .beads doesn't exist but origin has refs/dolt/data, and metadata.json is
// committed to git with a custom dolt_database, the bootstrap plan uses the
// correct database name after .beads/metadata.json is loaded.
// Part of the fix for GH#3029.
func TestDetectBootstrapAction_FreshCloneUsesMetadataDBName(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")

	// Create a bare repo with refs/dolt/data
	bareDir := filepath.Join(t.TempDir(), "bare.git")
	runGitForBootstrapTest(t, "", "init", "--bare", "--initial-branch=main", bareDir)

	sourceDir := t.TempDir()
	runGitForBootstrapTest(t, sourceDir, "init", "-b", "main")
	runGitForBootstrapTest(t, sourceDir, "config", "user.email", "test@test.com")
	runGitForBootstrapTest(t, sourceDir, "config", "user.name", "Test User")

	// Commit .beads/metadata.json with custom dolt_database to the source repo
	srcBeads := filepath.Join(sourceDir, ".beads")
	if err := os.MkdirAll(srcBeads, 0o750); err != nil {
		t.Fatal(err)
	}
	metadataJSON := `{"dolt_mode": "server", "dolt_database": "my_rig"}`
	if err := os.WriteFile(filepath.Join(srcBeads, "metadata.json"), []byte(metadataJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForBootstrapTest(t, sourceDir, "add", ".beads/metadata.json")
	runGitForBootstrapTest(t, sourceDir, "commit", "-m", "add beads metadata")
	runGitForBootstrapTest(t, sourceDir, "remote", "add", "origin", bareDir)
	runGitForBootstrapTest(t, sourceDir, "push", "origin", "main")
	runGitForBootstrapTest(t, sourceDir, "push", "origin", "HEAD:refs/dolt/data")

	// Clone and verify .beads/metadata.json is checked out.
	// Use a subdirectory of TempDir so git clone creates it (clone fails
	// if the target directory already exists and is non-empty).
	cloneDir := filepath.Join(t.TempDir(), "repo")
	runGitForBootstrapTest(t, "", "clone", bareDir, cloneDir)

	beadsDir := filepath.Join(cloneDir, ".beads")

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(cloneDir); err != nil {
		t.Fatal(err)
	}

	// Load config the same way bootstrap.go does
	cfg, cfgErr := configfile.Load(beadsDir)
	if cfgErr != nil || cfg == nil {
		cfg = configfile.DefaultConfig()
	}

	// After a git clone with committed metadata.json, the config should
	// have the custom database name
	if got := cfg.GetDoltDatabase(); got != "my_rig" {
		t.Fatalf("GetDoltDatabase() = %q, want %q (metadata.json dolt_database not loaded after clone)", got, "my_rig")
	}

	plan := detectBootstrapAction(beadsDir, cfg)

	if plan.Action != "sync" {
		t.Errorf("action = %q, want %q", plan.Action, "sync")
	}
	if plan.Database != "my_rig" {
		t.Errorf("plan.Database = %q, want %q", plan.Database, "my_rig")
	}
}

// TestBootstrapFreshCloneSynthesizedDirUsesDefaultDB verifies that when
// .beads directory doesn't exist (no metadata.json committed to git) and
// beadsDir is synthesized from the remote-probe path, the config falls back
// to DefaultConfig and uses the default "beads" database name.
// This is the expected behavior for repos that never committed metadata.json.
func TestBootstrapFreshCloneSynthesizedDirUsesDefaultDB(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")

	// Create a bare repo with refs/dolt/data but NO .beads/metadata.json
	bareDir := filepath.Join(t.TempDir(), "bare.git")
	runGitForBootstrapTest(t, "", "init", "--bare", bareDir)

	sourceDir := t.TempDir()
	runGitForBootstrapTest(t, sourceDir, "init", "-b", "main")
	runGitForBootstrapTest(t, sourceDir, "config", "user.email", "test@test.com")
	runGitForBootstrapTest(t, sourceDir, "config", "user.name", "Test User")
	runGitForBootstrapTest(t, sourceDir, "commit", "--allow-empty", "-m", "init")
	runGitForBootstrapTest(t, sourceDir, "remote", "add", "origin", bareDir)
	runGitForBootstrapTest(t, sourceDir, "push", "origin", "HEAD:refs/dolt/data")
	runGitForBootstrapTest(t, sourceDir, "push", "origin", "HEAD:refs/dolt/data")

	// Clone — no .beads dir
	cloneDir := t.TempDir()
	runGitForBootstrapTest(t, cloneDir, "init", "-b", "main")
	runGitForBootstrapTest(t, cloneDir, "remote", "add", "origin", bareDir)

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(cloneDir); err != nil {
		t.Fatal(err)
	}

	// Synthesize beadsDir the way the Run handler does
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	synthesizedDir := filepath.Join(cwd, ".beads")

	// Load config the same way bootstrap.go does — synthesized dir doesn't exist
	cfg, cfgErr := configfile.Load(synthesizedDir)
	if cfgErr != nil || cfg == nil {
		cfg = configfile.DefaultConfig()
	}

	// Without metadata.json, default "beads" is expected
	if got := cfg.GetDoltDatabase(); got != "beads" {
		t.Fatalf("GetDoltDatabase() = %q, want %q (should default when no metadata.json)", got, "beads")
	}

	plan := detectBootstrapAction(synthesizedDir, cfg)
	if plan.Action != "sync" {
		t.Errorf("action = %q, want %q", plan.Action, "sync")
	}
	if plan.Database != "beads" {
		t.Errorf("plan.Database = %q, want %q (default when no metadata.json)", plan.Database, "beads")
	}
}

// TestBootstrapRigSubdirUsesParentDBName verifies that when running bootstrap
// from a rig subdirectory (its own git repo) that doesn't have a local .beads,
// but the parent workspace has .beads/metadata.json with dolt_database set,
// the bootstrap plan uses the parent workspace's database name instead of "beads".
// This is the core reproduction for GH#3029.
func TestBootstrapRigSubdirUsesParentDBName(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")

	// Create workspace layout:
	//   workspace/
	//     .beads/metadata.json  (dolt_database: "my_rig")
	//     mayor/rig/            (its own git repo, no .beads)
	workspace := t.TempDir()
	beadsDir := filepath.Join(workspace, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	metadataJSON := `{"dolt_mode": "server", "dolt_database": "my_rig"}`
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(metadataJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a rig subdirectory with its own git repo and remote that has refs/dolt/data
	rigDir := filepath.Join(workspace, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0o750); err != nil {
		t.Fatal(err)
	}

	bareDir := filepath.Join(t.TempDir(), "rig-origin.git")
	runGitForBootstrapTest(t, "", "init", "--bare", "--initial-branch=main", bareDir)
	runGitForBootstrapTest(t, rigDir, "init", "-b", "main")
	runGitForBootstrapTest(t, rigDir, "config", "user.email", "test@test.com")
	runGitForBootstrapTest(t, rigDir, "config", "user.name", "Test User")
	runGitForBootstrapTest(t, rigDir, "commit", "--allow-empty", "-m", "init")
	runGitForBootstrapTest(t, rigDir, "remote", "add", "origin", bareDir)
	runGitForBootstrapTest(t, rigDir, "push", "origin", "HEAD:refs/dolt/data")
	runGitForBootstrapTest(t, rigDir, "push", "origin", "HEAD:refs/dolt/data")

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(rigDir); err != nil {
		t.Fatal(err)
	}

	// Simulate what the bootstrap Run handler does when FindBeadsDir returns "":
	// 1. beadsDir is empty (rig's git root has no .beads)
	// 2. Remote probe finds refs/dolt/data on origin
	// 3. beadsDir is synthesized as <cwd>/.beads
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	synthesizedDir := filepath.Join(cwd, ".beads")

	// configfile.Load on synthesized dir fails — no metadata.json there
	cfg, cfgErr := configfile.Load(synthesizedDir)
	if cfgErr != nil || cfg == nil {
		// This is the fix path: search parent directories for metadata.json
		cfg, cfgErr = findParentConfig(synthesizedDir)
		if cfgErr != nil {
			t.Fatalf("findParentConfig: %v", cfgErr)
		}
	}
	if cfg == nil {
		cfg = configfile.DefaultConfig()
	}

	// The key assertion: should find the workspace's dolt_database, not default "beads"
	if got := cfg.GetDoltDatabase(); got != "my_rig" {
		t.Fatalf("GetDoltDatabase() = %q, want %q (parent workspace metadata.json not found)", got, "my_rig")
	}

	plan := detectBootstrapAction(synthesizedDir, cfg)
	if plan.Action != "sync" {
		t.Errorf("action = %q, want %q", plan.Action, "sync")
	}
	if plan.Database != "my_rig" {
		t.Errorf("plan.Database = %q, want %q", plan.Database, "my_rig")
	}
}

func TestFindParentConfigPreservesLegacyConfig(t *testing.T) {
	workspace := t.TempDir()
	parentBeadsDir := filepath.Join(workspace, ".beads")
	if err := os.Mkdir(parentBeadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(parentBeadsDir, "config.json")
	legacy := []byte(`{"backend":"dolt","dolt_mode":"embedded","dolt_database":"legacy_parent"}`)
	if err := os.WriteFile(legacyPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	synthesizedDir := filepath.Join(workspace, "mayor", "rig", ".beads")
	cfg, err := findParentConfig(synthesizedDir)
	if err != nil {
		t.Fatalf("findParentConfig: %v", err)
	}
	if cfg == nil || cfg.GetDoltDatabase() != "legacy_parent" {
		t.Fatalf("parent config = %#v, want legacy_parent", cfg)
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil || !bytes.Equal(after, legacy) {
		t.Fatalf("parent discovery changed legacy config: equal=%v err=%v", bytes.Equal(after, legacy), err)
	}
	if _, err := os.Lstat(configfile.ConfigPath(parentBeadsDir)); !os.IsNotExist(err) {
		t.Fatalf("parent discovery created metadata.json: %v", err)
	}
}

func TestFindParentConfigStopsAtInvalidNearestAuthority(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, nearerBeadsDir string) string
	}{
		{
			name: "corrupt metadata",
			setup: func(t *testing.T, nearerBeadsDir string) string {
				t.Helper()
				path := configfile.ConfigPath(nearerBeadsDir)
				if err := os.WriteFile(path, []byte(`{"backend":`), 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
		{
			name: "ambiguous legacy config",
			setup: func(t *testing.T, nearerBeadsDir string) string {
				t.Helper()
				path := filepath.Join(nearerBeadsDir, "config.json")
				if err := os.WriteFile(path, []byte(`{"backend":"dolt","Backend":"sqlite"}`), 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
		{
			name: "pending migration",
			setup: func(t *testing.T, nearerBeadsDir string) string {
				t.Helper()
				path := filepath.Join(nearerBeadsDir, configfile.BackendMigrationStateFileName)
				if err := os.WriteFile(path, []byte("pending"), 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			outer := t.TempDir()
			outerBeadsDir := filepath.Join(outer, ".beads")
			nearerBeadsDir := filepath.Join(outer, "nearer", ".beads")
			if err := os.Mkdir(outerBeadsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(nearerBeadsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := (&configfile.Config{Backend: configfile.BackendDolt, DoltDatabase: "must_not_be_used"}).Save(outerBeadsDir); err != nil {
				t.Fatal(err)
			}
			invalidPath := test.setup(t, nearerBeadsDir)
			before, err := os.ReadFile(invalidPath)
			if err != nil {
				t.Fatal(err)
			}

			cfg, err := findParentConfig(filepath.Join(outer, "nearer", "rig", ".beads"))
			if err == nil {
				t.Fatalf("findParentConfig = %#v, nil; want nearest-authority error", cfg)
			}
			if cfg != nil {
				t.Fatalf("findParentConfig returned outer config after nearer error: %#v", cfg)
			}
			after, readErr := os.ReadFile(invalidPath)
			if readErr != nil || !bytes.Equal(after, before) {
				t.Fatalf("failed parent discovery changed nearest authority: equal=%v err=%v", bytes.Equal(after, before), readErr)
			}
		})
	}
}

func TestFindParentConfigRejectsLinkedNearestMetadataWithoutFollowingIt(t *testing.T) {
	outer := t.TempDir()
	outerBeadsDir := filepath.Join(outer, ".beads")
	nearerBeadsDir := filepath.Join(outer, "nearer", ".beads")
	if err := os.Mkdir(outerBeadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nearerBeadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (&configfile.Config{Backend: configfile.BackendDolt, DoltDatabase: "must_not_be_used"}).Save(outerBeadsDir); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(t.TempDir(), "foreign.json")
	foreignBytes := []byte(`{"backend":"dolt","dolt_database":"foreign"}`)
	if err := os.WriteFile(foreign, foreignBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, configfile.ConfigPath(nearerBeadsDir)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	cfg, err := findParentConfig(filepath.Join(outer, "nearer", "rig", ".beads"))
	if err == nil || cfg != nil {
		t.Fatalf("findParentConfig = %#v, %v; want linked nearest-authority error", cfg, err)
	}
	after, readErr := os.ReadFile(foreign)
	if readErr != nil || !bytes.Equal(after, foreignBytes) {
		t.Fatalf("linked parent discovery changed foreign target: equal=%v err=%v", bytes.Equal(after, foreignBytes), readErr)
	}
}

func TestDetectBootstrapActionWithAuthorityRefusesTargetCutoverBeforeRemoteProbe(t *testing.T) {
	repoDir := t.TempDir()
	runGitForBootstrapTest(t, repoDir, "init", "-b", "main")
	runGitForBootstrapTest(t, repoDir, "remote", "add", "origin", "https://provider.invalid/org/board")
	beadsDir := filepath.Join(repoDir, ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (&configfile.Config{
		Backend:      configfile.BackendDolt,
		DoltMode:     configfile.DoltModeEmbedded,
		DoltDatabase: "planned_dolt",
	}).Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	authority, plannedCfg, err := captureBootstrapAuthority(beadsDir)
	if err != nil {
		t.Fatal(err)
	}

	cutover := *plannedCfg
	cutover.Backend = configfile.BackendSQLite
	cutover.SQLitePath = "beads.db"
	if err := cutover.SaveAfterBackendReinitialization(beadsDir); err != nil {
		t.Fatal(err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}
	originalProbe := bootstrapGitOriginHasDoltDataRef
	probeCalls := 0
	bootstrapGitOriginHasDoltDataRef = func() bool {
		probeCalls++
		return true
	}
	defer func() { bootstrapGitOriginHasDoltDataRef = originalProbe }()

	if _, err := detectBootstrapActionWithAuthority(beadsDir, plannedCfg, authority); err == nil {
		t.Fatal("guarded bootstrap detection accepted a Dolt plan after SQLite cutover")
	}
	if probeCalls != 0 {
		t.Fatalf("guarded bootstrap detection contacted the remote provider %d time(s) after cutover", probeCalls)
	}
}

func TestDetectBootstrapActionWithAuthorityRefusesParentCutoverBeforeRemoteProbe(t *testing.T) {
	workspace := t.TempDir()
	parentBeadsDir := filepath.Join(workspace, ".beads")
	if err := os.Mkdir(parentBeadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (&configfile.Config{
		Backend:      configfile.BackendDolt,
		DoltMode:     configfile.DoltModeEmbedded,
		DoltDatabase: "parent_dolt",
	}).Save(parentBeadsDir); err != nil {
		t.Fatal(err)
	}
	rigDir := filepath.Join(workspace, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	runGitForBootstrapTest(t, rigDir, "init", "-b", "main")
	runGitForBootstrapTest(t, rigDir, "remote", "add", "origin", "https://provider.invalid/org/board")
	targetBeadsDir := filepath.Join(rigDir, ".beads")
	authority, plannedCfg, err := captureBootstrapAuthority(targetBeadsDir)
	if err != nil {
		t.Fatal(err)
	}
	if authority.ownerDir != parentBeadsDir || plannedCfg.GetDoltDatabase() != "parent_dolt" {
		t.Fatalf("captured authority = %#v, config = %#v; want parent owner", authority, plannedCfg)
	}

	cutover := *plannedCfg
	cutover.Backend = configfile.BackendSQLite
	cutover.SQLitePath = "beads.db"
	if err := cutover.SaveAfterBackendReinitialization(parentBeadsDir); err != nil {
		t.Fatal(err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(rigDir); err != nil {
		t.Fatal(err)
	}
	originalProbe := bootstrapGitOriginHasDoltDataRef
	probeCalls := 0
	bootstrapGitOriginHasDoltDataRef = func() bool {
		probeCalls++
		return true
	}
	defer func() { bootstrapGitOriginHasDoltDataRef = originalProbe }()

	if _, err := detectBootstrapActionWithAuthority(targetBeadsDir, plannedCfg, authority); err == nil {
		t.Fatal("guarded bootstrap detection accepted parent Dolt authority after parent SQLite cutover")
	}
	if probeCalls != 0 {
		t.Fatalf("guarded parent bootstrap contacted the remote provider %d time(s) after cutover", probeCalls)
	}
	if _, err := os.Lstat(targetBeadsDir); !os.IsNotExist(err) {
		t.Fatalf("refused parent bootstrap created target .beads: %v", err)
	}
}

func TestDetectBootstrapActionWithAuthorityHoldsParentControlDuringRemoteProbe(t *testing.T) {
	workspace := t.TempDir()
	parentBeadsDir := filepath.Join(workspace, ".beads")
	if err := os.Mkdir(parentBeadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (&configfile.Config{
		Backend:      configfile.BackendDolt,
		DoltMode:     configfile.DoltModeEmbedded,
		DoltDatabase: "parent_dolt",
	}).Save(parentBeadsDir); err != nil {
		t.Fatal(err)
	}
	rigDir := filepath.Join(workspace, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	runGitForBootstrapTest(t, rigDir, "init", "-b", "main")
	runGitForBootstrapTest(t, rigDir, "remote", "add", "origin", "https://provider.invalid/org/board")
	targetBeadsDir := filepath.Join(rigDir, ".beads")
	authority, plannedCfg, err := captureBootstrapAuthority(targetBeadsDir)
	if err != nil {
		t.Fatal(err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(rigDir); err != nil {
		t.Fatal(err)
	}
	originalProbe := bootstrapGitOriginHasDoltDataRef
	probeEntered := make(chan struct{})
	releaseProbe := make(chan struct{})
	bootstrapGitOriginHasDoltDataRef = func() bool {
		close(probeEntered)
		<-releaseProbe
		return true
	}
	defer func() { bootstrapGitOriginHasDoltDataRef = originalProbe }()

	type detectionResult struct {
		plan BootstrapPlan
		err  error
	}
	done := make(chan detectionResult, 1)
	go func() {
		plan, err := detectBootstrapActionWithAuthority(targetBeadsDir, plannedCfg, authority)
		done <- detectionResult{plan: plan, err: err}
	}()
	select {
	case <-probeEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("guarded detection did not reach remote probe")
	}
	contending, contentionErr := backendmigrationcontrol.TryAcquire(parentBeadsDir)
	if contending != nil {
		_ = contending.Close()
	}
	if !errors.Is(contentionErr, backendmigrationcontrol.ErrBusy) {
		t.Fatalf("parent migration control during remote probe = %v, want ErrBusy", contentionErr)
	}
	close(releaseProbe)
	select {
	case result := <-done:
		if result.err != nil || result.plan.Action != "sync" {
			t.Fatalf("guarded parent detection = %#v, %v; want sync", result.plan, result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("guarded parent detection did not finish")
	}
	if _, err := os.Lstat(targetBeadsDir); !os.IsNotExist(err) {
		t.Fatalf("planning created target .beads: %v", err)
	}
}

func TestDetectBootstrapDryRunDoesNotPublishDirectMigrationControl(t *testing.T) {
	workspace := t.TempDir()
	beadsDir := filepath.Join(workspace, ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata := []byte(`{"database":"dolt","backend":"dolt","dolt_mode":"embedded","dolt_database":"direct_dry_run"}`)
	if err := os.WriteFile(configfile.ConfigPath(beadsDir), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	controlPath := filepath.Join(beadsDir, backendmigrationcontrol.FileName)
	if _, err := os.Lstat(controlPath); !os.IsNotExist(err) {
		t.Fatalf("precondition: migration control already exists: %v", err)
	}

	authority, cfg, err := captureBootstrapAuthority(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := detectBootstrapDryRunAction(beadsDir, cfg, authority); err != nil {
		t.Fatalf("dry-run detection: %v", err)
	}
	if _, err := os.Lstat(controlPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run published migration control: %v", err)
	}
}

func TestDetectBootstrapDryRunDoesNotPublishParentMigrationControl(t *testing.T) {
	workspace := t.TempDir()
	parentBeadsDir := filepath.Join(workspace, ".beads")
	if err := os.Mkdir(parentBeadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata := []byte(`{"database":"dolt","backend":"dolt","dolt_mode":"embedded","dolt_database":"parent_dry_run"}`)
	if err := os.WriteFile(configfile.ConfigPath(parentBeadsDir), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	targetBeadsDir := filepath.Join(workspace, "mayor", "rig", ".beads")
	if err := os.MkdirAll(filepath.Dir(targetBeadsDir), 0o700); err != nil {
		t.Fatal(err)
	}
	controlPath := filepath.Join(parentBeadsDir, backendmigrationcontrol.FileName)
	if _, err := os.Lstat(controlPath); !os.IsNotExist(err) {
		t.Fatalf("precondition: parent migration control already exists: %v", err)
	}

	authority, cfg, err := captureBootstrapAuthority(targetBeadsDir)
	if err != nil {
		t.Fatal(err)
	}
	if authority.ownerDir != parentBeadsDir {
		t.Fatalf("authority owner = %q, want parent %q", authority.ownerDir, parentBeadsDir)
	}
	if _, err := detectBootstrapDryRunAction(targetBeadsDir, cfg, authority); err != nil {
		t.Fatalf("parent-derived dry-run detection: %v", err)
	}
	if _, err := os.Lstat(controlPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run published parent migration control: %v", err)
	}
	if _, err := os.Lstat(targetBeadsDir); !os.IsNotExist(err) {
		t.Fatalf("dry-run created target .beads: %v", err)
	}
}

func TestResolveBootstrapConfigAndAuthorityPreservesDryRunRepair(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (&configfile.Config{
		Backend:      configfile.BackendDolt,
		DoltMode:     configfile.DoltModeServer,
		DoltDatabase: "stale_on_disk",
	}).Save(beadsDir); err != nil {
		t.Fatal(err)
	}

	originalResolve := resolveBootstrapAuthoritativeMetadata
	resolveBootstrapAuthoritativeMetadata = func(string, bool) (*configfile.Config, string, error) {
		return &configfile.Config{
			Backend:      configfile.BackendDolt,
			DoltMode:     configfile.DoltModeServer,
			DoltDatabase: "hypothetical_repair",
		}, "would repair metadata", nil
	}
	defer func() { resolveBootstrapAuthoritativeMetadata = originalResolve }()

	authority, cfg, msg, err := resolveBootstrapConfigAndAuthority(beadsDir, true)
	if err != nil {
		t.Fatal(err)
	}
	if authority == nil || cfg.GetDoltDatabase() != "hypothetical_repair" || msg != "would repair metadata" {
		t.Fatalf("dry-run resolution = %#v, %#v, %q; want hypothetical repaired config", authority, cfg, msg)
	}
	onDisk, err := configfile.LoadAuthoritativeReadOnly(beadsDir)
	if err != nil || onDisk.GetDoltDatabase() != "stale_on_disk" {
		t.Fatalf("dry-run changed on-disk config: %#v, %v", onDisk, err)
	}
}

func TestExecuteBootstrapLocalActionsRefuseParentCutoverBeforeDoltEffects(t *testing.T) {
	for _, action := range []string{"init", "restore", "jsonl-import"} {
		t.Run(action, func(t *testing.T) {
			workspace := t.TempDir()
			parentBeadsDir := filepath.Join(workspace, ".beads")
			if err := os.Mkdir(parentBeadsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			parentMetadata := []byte(`{"database":"dolt","backend":"dolt","dolt_mode":"embedded","dolt_database":"parent_plan"}`)
			if err := os.WriteFile(configfile.ConfigPath(parentBeadsDir), parentMetadata, 0o600); err != nil {
				t.Fatal(err)
			}

			targetBeadsDir := filepath.Join(workspace, "mayor", action, ".beads")
			if err := os.MkdirAll(filepath.Join(targetBeadsDir, "backup"), 0o700); err != nil {
				t.Fatal(err)
			}
			backupFile := filepath.Join(targetBeadsDir, "backup", "issues.jsonl")
			jsonlFile := filepath.Join(targetBeadsDir, "issues.jsonl")
			if err := os.WriteFile(backupFile, []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(jsonlFile, []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			authority, cfg, err := captureBootstrapAuthority(targetBeadsDir)
			if err != nil {
				t.Fatal(err)
			}
			if authority.ownerDir != parentBeadsDir {
				t.Fatalf("authority owner = %q, want parent %q", authority.ownerDir, parentBeadsDir)
			}
			plan := BootstrapPlan{
				Action:    action,
				BeadsDir:  targetBeadsDir,
				Database:  cfg.GetDoltDatabase(),
				BackupDir: filepath.Join(targetBeadsDir, "backup"),
				JSONLFile: jsonlFile,
				authority: authority,
			}

			cutoverMetadata := []byte(`{"database":"beads.db","backend":"sqlite","sqlite_path":"beads.db"}`)
			if err := os.WriteFile(configfile.ConfigPath(parentBeadsDir), cutoverMetadata, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := executeBootstrapPlan(plan, cfg, true); err == nil {
				t.Fatal("stale parent-derived bootstrap plan was executed after SQLite cutover")
			}
			if _, err := os.Lstat(filepath.Join(targetBeadsDir, "embeddeddolt")); !os.IsNotExist(err) {
				t.Fatalf("stale bootstrap created embedded Dolt state: %v", err)
			}
		})
	}
}

func TestExecuteSyncActionRefusesStaleDoltPlanAfterSQLiteCutover(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sourceDir := filepath.Join(beadsDir, "embeddeddolt")
	if err := os.Mkdir(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sourceSentinel := filepath.Join(sourceDir, "retained-source")
	if err := os.WriteFile(sourceSentinel, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(beadsDir, "config.yaml")
	configBytes := []byte("sync.remote: https://provider.invalid/org/board\n")
	if err := os.WriteFile(configPath, configBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (&configfile.Config{
		Database:     "dolt",
		Backend:      configfile.BackendDolt,
		DoltMode:     configfile.DoltModeEmbedded,
		DoltDatabase: "stale_plan",
	}).Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	authority, plannedCfg, err := captureBootstrapAuthority(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	plan := BootstrapPlan{
		Action:     "sync",
		BeadsDir:   beadsDir,
		Database:   plannedCfg.GetDoltDatabase(),
		SyncRemote: "https://provider.invalid/org/board",
		authority:  authority,
	}

	cutover, err := configfile.LoadAuthoritativeReadOnly(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	cutover.Backend = configfile.BackendSQLite
	cutover.SQLitePath = "beads.db"
	if err := cutover.SaveAfterBackendReinitialization(beadsDir); err != nil {
		t.Fatal(err)
	}
	metadataBefore, err := os.ReadFile(configfile.ConfigPath(beadsDir))
	if err != nil {
		t.Fatal(err)
	}
	configBefore, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	sourceBefore, err := os.ReadFile(sourceSentinel)
	if err != nil {
		t.Fatal(err)
	}

	originalClone := cloneBootstrapRemote
	var cloneCalls int
	cloneBootstrapRemote = func(context.Context, string, string, string, *configfile.Config) error {
		cloneCalls++
		return errors.New("provider must not be called")
	}
	defer func() { cloneBootstrapRemote = originalClone }()

	err = executeSyncAction(t.Context(), plan, plannedCfg)
	if err == nil {
		t.Fatal("executeSyncAction accepted a stale Dolt plan after SQLite cutover")
	}
	if cloneCalls != 0 {
		t.Fatalf("stale bootstrap called provider %d time(s)", cloneCalls)
	}
	for path, before := range map[string][]byte{
		configfile.ConfigPath(beadsDir): metadataBefore,
		configPath:                      configBefore,
		sourceSentinel:                  sourceBefore,
	} {
		after, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(after, before) {
			t.Fatalf("stale bootstrap changed %s: equal=%v err=%v", filepath.Base(path), bytes.Equal(after, before), readErr)
		}
	}
}

func TestExecuteSyncActionHoldsMigrationControlWhileProviderIsBlocked(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (&configfile.Config{
		Database:     "dolt",
		Backend:      configfile.BackendDolt,
		DoltMode:     configfile.DoltModeEmbedded,
		DoltDatabase: "controlled_clone",
	}).Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	authority, cfg, err := captureBootstrapAuthority(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	plan := BootstrapPlan{
		Action:     "sync",
		BeadsDir:   beadsDir,
		Database:   cfg.GetDoltDatabase(),
		SyncRemote: "https://provider.invalid/org/board",
		authority:  authority,
	}

	providerEntered := make(chan struct{})
	releaseProvider := make(chan struct{})
	providerErr := errors.New("provider stopped")
	originalClone := cloneBootstrapRemote
	cloneBootstrapRemote = func(context.Context, string, string, string, *configfile.Config) error {
		close(providerEntered)
		<-releaseProvider
		return providerErr
	}
	defer func() { cloneBootstrapRemote = originalClone }()

	done := make(chan error, 1)
	go func() { done <- executeSyncAction(context.Background(), plan, cfg) }()
	select {
	case <-providerEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider was not invoked")
	}
	contending, contentionErr := backendmigrationcontrol.TryAcquire(beadsDir)
	if contending != nil {
		_ = contending.Close()
	}
	if !errors.Is(contentionErr, backendmigrationcontrol.ErrBusy) {
		t.Fatalf("migration control during provider call = %v, want ErrBusy", contentionErr)
	}
	close(releaseProvider)
	select {
	case err := <-done:
		if !errors.Is(err, providerErr) {
			t.Fatalf("executeSyncAction error = %v, want provider sentinel", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("executeSyncAction did not finish after provider release")
	}
	guard, err := backendmigrationcontrol.TryAcquire(beadsDir)
	if err != nil {
		t.Fatalf("migration control remained held after provider exit: %v", err)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteSyncActionRemoteMigrateErrorDoesNotExposeCredentials(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (&configfile.Config{
		Database:     "dolt",
		Backend:      configfile.BackendDolt,
		DoltMode:     configfile.DoltModeEmbedded,
		DoltDatabase: "credential_error",
	}).Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	authority, cfg, err := captureBootstrapAuthority(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "error-secret"
	const syncRemote = "https://operator:" + secret + "@provider.example/org/beads?token=" + secret
	plan := BootstrapPlan{
		Action:     "sync",
		BeadsDir:   beadsDir,
		Database:   cfg.GetDoltDatabase(),
		SyncRemote: syncRemote,
		authority:  authority,
	}

	originalClone := cloneBootstrapRemote
	originalWarmup := warmupSyncedBootstrap
	cloneBootstrapRemote = func(context.Context, string, string, string, *configfile.Config) error { return nil }
	warmupSyncedBootstrap = func(context.Context, BootstrapPlan, *configfile.Config, string) error {
		return &schema.RemoteMigrateGateError{CurrentVersion: 49, LatestVersion: 50, Pending: 1}
	}
	defer func() {
		cloneBootstrapRemote = originalClone
		warmupSyncedBootstrap = originalWarmup
	}()

	err = executeSyncAction(context.Background(), plan, cfg)
	if err == nil {
		t.Fatal("expected remote-migrate gate error")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "operator@") {
		t.Fatalf("bootstrap error exposed remote credentials: %v", err)
	}
	if !strings.Contains(err.Error(), "configured remote") {
		t.Fatalf("bootstrap error lost useful remote context: %v", err)
	}
}

func TestExecuteSyncActionCloneErrorDoesNotExposeCredentials(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (&configfile.Config{
		Database:     "dolt",
		Backend:      configfile.BackendDolt,
		DoltMode:     configfile.DoltModeEmbedded,
		DoltDatabase: "clone_error",
	}).Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	authority, cfg, err := captureBootstrapAuthority(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "clone-secret"
	const syncRemote = "https://operator:" + secret + "@provider.example/%zz"
	plan := BootstrapPlan{
		Action:     "sync",
		BeadsDir:   beadsDir,
		Database:   cfg.GetDoltDatabase(),
		SyncRemote: syncRemote,
		authority:  authority,
	}

	providerErr := fmt.Errorf("DOLT_CLONE %s failed", syncRemote)
	originalClone := cloneBootstrapRemote
	cloneBootstrapRemote = func(context.Context, string, string, string, *configfile.Config) error {
		return providerErr
	}
	defer func() { cloneBootstrapRemote = originalClone }()

	err = executeSyncAction(context.Background(), plan, cfg)
	if err == nil {
		t.Fatal("expected clone failure")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "%zz") {
		t.Fatalf("bootstrap clone error exposed remote credentials: %v", err)
	}
	if !strings.Contains(err.Error(), "configured remote") {
		t.Fatalf("bootstrap clone error lost actionable context: %v", err)
	}
	if !errors.Is(err, providerErr) {
		t.Fatalf("bootstrap clone error lost its internal cause: %v", err)
	}
}

func TestExecuteSyncActionHoldsControlThroughFinalizeAndWarmup(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (&configfile.Config{
		Database:     "dolt",
		Backend:      configfile.BackendDolt,
		DoltMode:     configfile.DoltModeEmbedded,
		DoltDatabase: "full_span",
	}).Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	authority, cfg, err := captureBootstrapAuthority(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	plan := BootstrapPlan{
		Action:     "sync",
		BeadsDir:   beadsDir,
		Database:   cfg.GetDoltDatabase(),
		SyncRemote: "https://provider.invalid/org/board",
		authority:  authority,
	}

	originalClone := cloneBootstrapRemote
	originalWarmup := warmupSyncedBootstrap
	cloneBootstrapRemote = func(context.Context, string, string, string, *configfile.Config) error { return nil }
	warmupSyncedBootstrap = func(context.Context, BootstrapPlan, *configfile.Config, string) error {
		contending, contentionErr := backendmigrationcontrol.TryAcquire(beadsDir)
		if contending != nil {
			_ = contending.Close()
		}
		if !errors.Is(contentionErr, backendmigrationcontrol.ErrBusy) {
			return fmt.Errorf("migration control during warmup = %v, want ErrBusy", contentionErr)
		}
		return nil
	}
	defer func() {
		cloneBootstrapRemote = originalClone
		warmupSyncedBootstrap = originalWarmup
	}()

	if err := executeSyncAction(context.Background(), plan, cfg); err != nil {
		t.Fatalf("executeSyncAction: %v", err)
	}
	configYAML, err := os.ReadFile(filepath.Join(beadsDir, "config.yaml"))
	if err != nil || !bytes.Contains(configYAML, []byte(plan.SyncRemote)) {
		t.Fatalf("finalization did not persist confirmed sync remote: %q err=%v", configYAML, err)
	}
	guard, err := backendmigrationcontrol.TryAcquire(beadsDir)
	if err != nil {
		t.Fatalf("migration control remained held after warmup: %v", err)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestDetectBootstrapAction_SharedServerEnvUsesSharedPath verifies that when
// BEADS_DOLT_SHARED_SERVER=1 is set but cfg.DoltMode is the default (embedded),
// detectBootstrapAction looks in the shared-server directory — not embeddeddolt/.
// This is the root cause of GH#30.
func TestDetectBootstrapAction_SharedServerEnvUsesSharedPath(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// Override HOME so SharedDoltDir() resolves to our temp directory
	// instead of the real ~/.beads/shared-server/dolt/.
	t.Setenv("HOME", tmpDir)

	// Create a database directory at the shared-server location.
	// SharedDoltDir() returns $HOME/.beads/shared-server/dolt/.
	sharedDoltDir := filepath.Join(tmpDir, ".beads", "shared-server", "dolt")
	if err := os.MkdirAll(filepath.Join(sharedDoltDir, "beads"), 0o750); err != nil {
		t.Fatal(err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	// Shared server enabled, but cfg.DoltMode is default (embedded).
	// Before the fix, this would look in embeddeddolt/ and miss the
	// existing shared-server database.
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "1")

	cfg := configfile.DefaultConfig()
	// Deliberately do NOT set cfg.DoltMode = configfile.DoltModeServer.
	// This reproduces the bug: shared-server via env var with default DoltMode.

	// The server probe stub: report the DB exists so we get action=none.
	origCheck := checkBootstrapServerDB
	checkBootstrapServerDB = func(probeCfg bootstrapServerProbeConfig) bootstrapServerDBCheck {
		return bootstrapServerDBCheck{Exists: true, Reachable: true}
	}
	defer func() { checkBootstrapServerDB = origCheck }()

	plan := detectBootstrapAction(beadsDir, cfg)

	if plan.Action != "none" {
		t.Fatalf("expected action=none (existing shared-server DB detected), got %q: %s", plan.Action, plan.Reason)
	}
	if !plan.HasExisting {
		t.Error("HasExisting = false, want true")
	}
}

// TestDetectBootstrapAction_WorktreeSynthesizedDirPrefersSyncOverDefaultSharedDB
// verifies that when bootstrap is running from a worktree whose fallback
// .beads path lives under a bare/common git directory, remote recovery via
// refs/dolt/data wins over an unrelated default "beads" database already
// present on the shared server.
func TestDetectBootstrapAction_WorktreeSynthesizedDirPrefersSyncOverDefaultSharedDB(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "1")

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	originBare := filepath.Join(t.TempDir(), "origin.git")
	runGitForBootstrapTest(t, "", "init", "--bare", "--initial-branch=main", originBare)

	sourceDir := t.TempDir()
	runGitForBootstrapTest(t, sourceDir, "init", "-b", "main")
	runGitForBootstrapTest(t, sourceDir, "config", "user.email", "test@test.com")
	runGitForBootstrapTest(t, sourceDir, "config", "user.name", "Test User")
	runGitForBootstrapTest(t, sourceDir, "commit", "--allow-empty", "-m", "init")
	runGitForBootstrapTest(t, sourceDir, "remote", "add", "origin", originBare)
	runGitForBootstrapTest(t, sourceDir, "push", "origin", "main")
	runGitForBootstrapTest(t, sourceDir, "push", "origin", "HEAD:refs/dolt/data")

	localBare := filepath.Join(t.TempDir(), "local-bare.git")
	runGitForBootstrapTest(t, "", "clone", "--bare", originBare, localBare)

	worktreeDir := filepath.Join(t.TempDir(), "worktree")
	runGitForBootstrapTest(t, "", "--git-dir="+localBare, "worktree", "add", worktreeDir, "main")

	sharedDoltDir := filepath.Join(homeDir, ".beads", "shared-server", "dolt")
	if err := os.MkdirAll(filepath.Join(sharedDoltDir, "beads"), 0o750); err != nil {
		t.Fatal(err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(worktreeDir); err != nil {
		t.Fatal(err)
	}

	synthesizedDir := filepath.Join(localBare, ".beads")
	cfg, cfgErr := configfile.Load(synthesizedDir)
	if cfgErr != nil || cfg == nil {
		cfg, cfgErr = findParentConfig(synthesizedDir)
		if cfgErr != nil {
			t.Fatalf("findParentConfig: %v", cfgErr)
		}
	}
	if cfg == nil {
		cfg = configfile.DefaultConfig()
	}

	if got := cfg.GetDoltDatabase(); got != "beads" {
		t.Fatalf("GetDoltDatabase() = %q, want %q (default expected without local metadata)", got, "beads")
	}

	origCheck := checkBootstrapServerDB
	checkBootstrapServerDB = func(probeCfg bootstrapServerProbeConfig) bootstrapServerDBCheck {
		if probeCfg.database != "beads" {
			t.Fatalf("probeCfg.database = %q, want %q", probeCfg.database, "beads")
		}
		return bootstrapServerDBCheck{Exists: true, Reachable: true}
	}
	defer func() { checkBootstrapServerDB = origCheck }()

	plan := detectBootstrapAction(synthesizedDir, cfg)

	if plan.Action != "sync" {
		t.Fatalf("expected action=%q, got %q: %s", "sync", plan.Action, plan.Reason)
	}
	if plan.SyncRemote == "" {
		t.Fatal("expected SyncRemote to be populated from origin refs/dolt/data detection")
	}
	if plan.Database != "beads" {
		t.Errorf("plan.Database = %q, want %q (default metadata-free value should still recover via sync)", plan.Database, "beads")
	}
}

func TestDetectBootstrapAction_SynthesizedDirWithoutRecoveryStillUsesExistingSharedDB(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "1")

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	worktreeDir := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(worktreeDir, 0o750); err != nil {
		t.Fatal(err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(worktreeDir); err != nil {
		t.Fatal(err)
	}

	sharedDoltDir := filepath.Join(homeDir, ".beads", "shared-server", "dolt")
	if err := os.MkdirAll(filepath.Join(sharedDoltDir, "project_existing"), 0o750); err != nil {
		t.Fatal(err)
	}

	synthesizedDir := filepath.Join(worktreeDir, ".beads")
	cfg := configfile.DefaultConfig()
	cfg.DoltMode = configfile.DoltModeServer
	cfg.DoltDatabase = "project_existing"

	origCheck := checkBootstrapServerDB
	checkBootstrapServerDB = func(probeCfg bootstrapServerProbeConfig) bootstrapServerDBCheck {
		if probeCfg.database != "project_existing" {
			t.Fatalf("probeCfg.database = %q, want %q", probeCfg.database, "project_existing")
		}
		return bootstrapServerDBCheck{Exists: true, Reachable: true}
	}
	defer func() { checkBootstrapServerDB = origCheck }()

	plan := detectBootstrapAction(synthesizedDir, cfg)

	if plan.Action != "none" {
		t.Fatalf("expected action=%q, got %q: %s", "none", plan.Action, plan.Reason)
	}
	if !plan.HasExisting {
		t.Fatal("expected HasExisting to be true when configured shared-server DB already exists")
	}
}

// TestFinalizeSyncedBootstrapWritesConfigFiles verifies that after a sync
// clone, finalizeSyncedBootstrap writes the metadata.json and config.yaml
// files bd needs to reopen the cloned database. This is the regression
// guard for GH#3201: executeSyncAction previously left the workspace
// without these files, causing "no beads configuration found" and
// "Error 1105: no database selected" on every subsequent bd command.
func TestFinalizeSyncedBootstrapWritesConfigFiles(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	// Simulate a post-clone workspace: cloneFromRemote created the
	// embeddeddolt directory but no metadata.json / config.yaml exists.
	if err := os.MkdirAll(filepath.Join(beadsDir, "embeddeddolt"), 0o750); err != nil {
		t.Fatal(err)
	}

	// Point findProjectConfigYaml at the test workspace instead of walking
	// up from CWD, so the sync.remote write lands in the right file even
	// when the test runs from an unrelated directory.
	t.Setenv("BEADS_DIR", beadsDir)

	const dbName = "beads_hq"
	const syncRemote = "file:///tmp/fake-origin.git"

	cfg := configfile.DefaultConfig()
	if err := finalizeSyncedBootstrap(beadsDir, syncRemote, cfg, dbName); err != nil {
		t.Fatalf("finalizeSyncedBootstrap failed: %v", err)
	}

	// metadata.json must exist and record the database name bd needs to
	// reopen the cloned data. Without this, GetDoltDatabase() falls back to
	// DefaultDoltDatabase ("beads") and the cloned DB is unreachable.
	loaded, err := configfile.Load(beadsDir)
	if err != nil {
		t.Fatalf("configfile.Load after finalize failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("configfile.Load returned nil; metadata.json was not written")
	}
	if loaded.GetDoltDatabase() != dbName {
		t.Errorf("dolt_database = %q, want %q", loaded.GetDoltDatabase(), dbName)
	}
	if loaded.GetDoltMode() != configfile.DoltModeEmbedded {
		t.Errorf("dolt_mode = %q, want %q", loaded.GetDoltMode(), configfile.DoltModeEmbedded)
	}
	if loaded.GetBackend() != configfile.BackendDolt {
		t.Errorf("backend = %q, want %q", loaded.GetBackend(), configfile.BackendDolt)
	}

	// config.yaml must exist so GetYamlConfig / SetYamlConfig and other
	// yaml-backed settings (sync.remote, dolt.shared-server, etc.) work.
	configYamlPath := filepath.Join(beadsDir, "config.yaml")
	yamlBytes, err := os.ReadFile(configYamlPath)
	if err != nil {
		t.Fatalf("config.yaml missing after finalize: %v", err)
	}
	yaml := string(yamlBytes)

	// sync.remote must be persisted so subsequent fresh clones (and
	// bootstrap retries) can rediscover the remote without re-probing
	// origin refs.
	if !strings.Contains(yaml, "sync.remote: ") && !strings.Contains(yaml, "sync-remote: ") {
		t.Errorf("config.yaml does not contain sync.remote entry:\n%s", yaml)
	}
	if !strings.Contains(yaml, syncRemote) {
		t.Errorf("config.yaml does not contain sync remote URL %q:\n%s", syncRemote, yaml)
	}

	gitignoreBytes, err := os.ReadFile(filepath.Join(beadsDir, ".gitignore"))
	if err != nil {
		t.Fatalf(".beads/.gitignore missing after finalize: %v", err)
	}
	gitignore := string(gitignoreBytes)
	for _, pattern := range []string{".local_version", "backup/", "export-state.json", "last-touched"} {
		if !strings.Contains(gitignore, pattern) {
			t.Errorf(".beads/.gitignore missing runtime pattern %q:\n%s", pattern, gitignore)
		}
	}
}

func TestFinalizeSyncedBootstrap_WorktreeStubDoesNotShadowTargetConfig(t *testing.T) {
	restore := snapshotBootstrapEnv(t)
	defer restore()

	config.ResetForTesting()
	defer config.ResetForTesting()

	originBare := filepath.Join(t.TempDir(), "origin.git")
	runGitForBootstrapTest(t, "", "init", "--bare", "--initial-branch=main", originBare)

	sourceDir := t.TempDir()
	runGitForBootstrapTest(t, sourceDir, "init", "-b", "main")
	runGitForBootstrapTest(t, sourceDir, "config", "user.email", "test@test.com")
	runGitForBootstrapTest(t, sourceDir, "config", "user.name", "Test User")
	runGitForBootstrapTest(t, sourceDir, "commit", "--allow-empty", "-m", "init")
	runGitForBootstrapTest(t, sourceDir, "remote", "add", "origin", originBare)
	runGitForBootstrapTest(t, sourceDir, "push", "origin", "main")

	localBare := filepath.Join(t.TempDir(), "local-bare.git")
	runGitForBootstrapTest(t, "", "clone", "--bare", originBare, localBare)

	worktreeDir := filepath.Join(t.TempDir(), "worktree")
	runGitForBootstrapTest(t, "", "--git-dir="+localBare, "worktree", "add", worktreeDir, "main")

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(worktreeDir); err != nil {
		t.Fatal(err)
	}

	// Reproduce the failing shape from fix-worktree-config-yaml-resolution:
	// the worktree has a local .beads stub, but bootstrap is finalizing the
	// shared config under the bare/common-dir parent.
	worktreeStubDir := filepath.Join(worktreeDir, ".beads")
	if err := os.MkdirAll(worktreeStubDir, 0o750); err != nil {
		t.Fatal(err)
	}

	targetBeadsDir := filepath.Join(localBare, ".beads")
	if err := os.MkdirAll(filepath.Join(targetBeadsDir, "embeddeddolt"), 0o750); err != nil {
		t.Fatal(err)
	}

	const remoteURL = "git+ssh://git@github.com/example-org/example-app.git"
	cfg := configfile.DefaultConfig()
	if err := finalizeSyncedBootstrap(targetBeadsDir, remoteURL, cfg, "example-org"); err != nil {
		t.Fatalf("finalizeSyncedBootstrap failed: %v", err)
	}

	targetConfigPath := filepath.Join(targetBeadsDir, "config.yaml")
	targetContent, err := os.ReadFile(targetConfigPath)
	if err != nil {
		t.Fatalf("failed to read target config.yaml: %v", err)
	}
	if !strings.Contains(string(targetContent), remoteURL) {
		t.Fatalf("expected target config.yaml to contain %q, got:\n%s", remoteURL, string(targetContent))
	}

	if _, err := os.Stat(filepath.Join(worktreeStubDir, "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("expected worktree stub config.yaml to remain absent, got err=%v", err)
	}
}

// TestFinalizeSyncedBootstrapIsIdempotent verifies that re-running the
// finalize step over an already-finalized workspace is a no-op — the
// clone retry path relies on this.
func TestFinalizeSyncedBootstrapIsIdempotent(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(filepath.Join(beadsDir, "embeddeddolt"), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BEADS_DIR", beadsDir)

	cfg := configfile.DefaultConfig()
	if err := finalizeSyncedBootstrap(beadsDir, "file:///tmp/a.git", cfg, "beads_hq"); err != nil {
		t.Fatalf("first finalize failed: %v", err)
	}

	firstYaml, err := os.ReadFile(filepath.Join(beadsDir, "config.yaml"))
	if err != nil {
		t.Fatalf("read config.yaml after first finalize: %v", err)
	}

	if err := finalizeSyncedBootstrap(beadsDir, "file:///tmp/a.git", cfg, "beads_hq"); err != nil {
		t.Fatalf("second finalize failed: %v", err)
	}

	secondYaml, err := os.ReadFile(filepath.Join(beadsDir, "config.yaml"))
	if err != nil {
		t.Fatalf("read config.yaml after second finalize: %v", err)
	}

	// createConfigYaml skips existing files, so the template portion must
	// be unchanged. SetYamlConfig rewrites in place but should produce the
	// same output for the same value.
	if string(firstYaml) != string(secondYaml) {
		t.Errorf("config.yaml changed on second finalize.\nfirst:\n%s\nsecond:\n%s", firstYaml, secondYaml)
	}

	// metadata.json must still load cleanly.
	loaded, err := configfile.Load(beadsDir)
	if err != nil || loaded == nil {
		t.Fatalf("metadata.json missing after second finalize: %v", err)
	}
	if loaded.GetDoltDatabase() != "beads_hq" {
		t.Errorf("dolt_database drifted: got %q, want %q", loaded.GetDoltDatabase(), "beads_hq")
	}
}

func TestApplyBootstrapMetadataRepair_UsesResolvedConfig(t *testing.T) {
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	origResolve := resolveBootstrapAuthoritativeMetadata
	resolveBootstrapAuthoritativeMetadata = func(path string, apply bool) (*configfile.Config, string, error) {
		if path != tmpDir {
			t.Fatalf("path = %q, want %q", path, tmpDir)
		}
		if !apply {
			t.Fatal("expected apply=true")
		}
		return &configfile.Config{DoltMode: configfile.DoltModeServer, DoltDatabase: "canonical_db"}, "repaired dolt_database", nil
	}
	defer func() { resolveBootstrapAuthoritativeMetadata = origResolve }()

	resolved, msg, err := applyBootstrapMetadataRepair(beadsDir, configfile.DefaultConfig(), true)
	if err != nil {
		t.Fatalf("applyBootstrapMetadataRepair failed: %v", err)
	}
	if resolved == nil {
		t.Fatal("resolved config is nil")
	}
	if resolved.GetDoltDatabase() != "canonical_db" {
		t.Fatalf("GetDoltDatabase() = %q, want %q", resolved.GetDoltDatabase(), "canonical_db")
	}
	if msg != "repaired dolt_database" {
		t.Fatalf("msg = %q, want %q", msg, "repaired dolt_database")
	}
}

// TestCloneFromRemoteRoutesToServerMode verifies that cloneFromRemote uses
// the SQL server path (not filesystem clone) when ResolveServerMode
// detects external server mode from metadata.json. GH#3343.
func TestCloneFromRemoteRoutesToServerMode(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")

	tmpDir2 := t.TempDir()
	beadsDir2 := filepath.Join(tmpDir2, ".beads")
	if err := os.MkdirAll(beadsDir2, 0o750); err != nil {
		t.Fatal(err)
	}

	// Write metadata.json with server mode and explicit port — this makes
	// ResolveServerMode return ServerModeExternal.
	cfg := &configfile.Config{
		DoltMode:       configfile.DoltModeServer,
		DoltServerHost: "127.0.0.1",
		DoltServerPort: 3308,
		DoltDatabase:   "beads_proj",
	}
	if err := cfg.Save(beadsDir2); err != nil {
		t.Fatalf("save metadata.json: %v", err)
	}

	// cloneFromRemote should attempt a server connection (not a filesystem
	// clone). Since no server is running, we expect a connection error —
	// NOT a "dolt clone failed" error, which would indicate the filesystem
	// path was taken.
	err := cloneFromRemote(t.Context(), beadsDir2, "file:///tmp/nonexistent.git", "beads_proj", cfg)
	if err == nil {
		t.Fatal("expected error (no server running), got nil")
	}
	errMsg := err.Error()

	// The error should indicate a server connection attempt, not a CLI clone.
	if strings.Contains(errMsg, "dolt clone failed") {
		t.Errorf("cloneFromRemote used filesystem clone path in server mode: %v", err)
	}
	if !strings.Contains(errMsg, "server") {
		t.Errorf("expected server-related error, got: %v", err)
	}

	// Verify no local dolt directory was created.
	doltDir := filepath.Join(beadsDir2, "dolt")
	if _, err := os.Stat(doltDir); err == nil {
		t.Errorf("local .beads/dolt/ directory was created — clone should have gone to server, not filesystem")
	}
}

// TestCloneFromRemoteRoutesToServerModeViaEnv verifies that the
// BEADS_DOLT_SERVER_MODE=1 env var triggers the server clone path,
// even when metadata.json is absent.
func TestCloneFromRemoteRoutesToServerModeViaEnv(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "1")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	cfg := &configfile.Config{
		DoltServerHost: "127.0.0.1",
		DoltServerPort: 3309,
		DoltDatabase:   "beads_env",
	}

	err := cloneFromRemote(t.Context(), beadsDir, "file:///tmp/nonexistent.git", "beads_env", cfg)
	if err == nil {
		t.Fatal("expected error (no server running), got nil")
	}
	if strings.Contains(err.Error(), "dolt clone failed") {
		t.Errorf("cloneFromRemote used filesystem clone path despite BEADS_DOLT_SERVER_MODE=1: %v", err)
	}
	if !strings.Contains(err.Error(), "server") {
		t.Errorf("expected server-related error, got: %v", err)
	}
}

// TestCloneFromRemoteExternalNilCfgLoadsDisk verifies that when cfg is nil
// in external server mode, cloneFromRemote falls back to loading config
// from metadata.json on disk.
func TestCloneFromRemoteExternalNilCfgLoadsDisk(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// Write metadata.json to disk with server mode — but pass nil cfg.
	diskCfg := &configfile.Config{
		DoltMode:       configfile.DoltModeServer,
		DoltServerHost: "127.0.0.1",
		DoltServerPort: 3310,
		DoltDatabase:   "beads_disk",
	}
	if err := diskCfg.Save(beadsDir); err != nil {
		t.Fatalf("save metadata.json: %v", err)
	}

	// Pass nil cfg — cloneFromRemote should load from disk and still
	// take the server path.
	err := cloneFromRemote(t.Context(), beadsDir, "file:///tmp/nonexistent.git", "beads_disk", nil)
	if err == nil {
		t.Fatal("expected error (no server running), got nil")
	}
	if strings.Contains(err.Error(), "dolt clone failed") {
		t.Errorf("nil-cfg path used filesystem clone despite server metadata on disk: %v", err)
	}
	if !strings.Contains(err.Error(), "server") {
		t.Errorf("expected server-related error, got: %v", err)
	}
}

// TestCloneFromRemoteOwnedModeUsesCLI verifies that owned-server mode
// (the default when no metadata.json exists) uses the CLI clone path.
func TestCloneFromRemoteOwnedModeUsesCLI(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// No metadata.json → ResolveServerMode returns ServerModeOwned → CLI path.
	// The CLI path calls BootstrapFromRemoteWithDB, which requires dolt CLI.
	// Since dolt may not be installed in CI, we accept either:
	// - "dolt CLI not found" (no dolt binary)
	// - "dolt clone failed" (dolt binary exists but remote is invalid)
	// Both confirm the CLI path was taken, not the server path.
	err := cloneFromRemote(t.Context(), beadsDir, "file:///tmp/nonexistent.git", "beads_owned", nil)
	if err == nil {
		// BootstrapFromRemoteWithDB returns (false, nil) if doltExists — skip.
		return
	}
	errMsg := err.Error()
	if strings.Contains(errMsg, "dolt server unreachable") || strings.Contains(errMsg, "connect to dolt server") {
		t.Errorf("owned-mode clone routed to server path: %v", err)
	}
}

func TestResolveRemoteCloneModeDefaultConfigUsesEmbedded(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")

	beadsDir := filepath.Join(t.TempDir(), ".beads")
	cfg := configfile.DefaultConfig()

	got := resolveRemoteCloneMode(beadsDir, cfg, remoteCloneAuto)
	if got != remoteCloneEmbedded {
		t.Fatalf("resolveRemoteCloneMode(default cfg) = %v, want embedded", got)
	}
}

func TestResolveRemoteCloneModeExplicitExternalOverridesMissingMetadata(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")

	beadsDir := filepath.Join(t.TempDir(), ".beads")
	cfg := &configfile.Config{
		DoltMode:       configfile.DoltModeServer,
		DoltServerHost: "127.0.0.1",
		DoltServerPort: 3312,
		DoltDatabase:   "beads_external",
	}

	got := resolveRemoteCloneMode(beadsDir, cfg, remoteCloneExternalServer)
	if got != remoteCloneExternalServer {
		t.Fatalf("resolveRemoteCloneMode(explicit external) = %v, want external server", got)
	}
}

// TestCloneFromRemoteSharedServerModeUsesServer verifies that
// BEADS_DOLT_SHARED_SERVER=1 triggers the server clone path.
func TestCloneFromRemoteSharedServerModeUsesServer(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "1")

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	cfg := &configfile.Config{
		DoltServerHost: "127.0.0.1",
		DoltServerPort: 3311,
		DoltDatabase:   "beads_shared",
	}

	err := cloneFromRemote(t.Context(), beadsDir, "file:///tmp/nonexistent.git", "beads_shared", cfg)
	if err == nil {
		t.Fatal("expected error (no server running), got nil")
	}
	if strings.Contains(err.Error(), "dolt clone failed") {
		t.Errorf("shared-server mode used filesystem clone: %v", err)
	}
	if !strings.Contains(err.Error(), "server") {
		t.Errorf("expected server-related error, got: %v", err)
	}
}

// TestFinalizeSyncedBootstrapSharedServerSetsServerMode verifies that
// finalizeSyncedBootstrap writes dolt_mode=server when shared-server
// mode is active via env var.
func TestFinalizeSyncedBootstrapSharedServerSetsServerMode(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "1")

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BEADS_DIR", beadsDir)

	cfg := configfile.DefaultConfig()
	if err := finalizeSyncedBootstrap(beadsDir, "file:///tmp/fake.git", cfg, "beads_shared"); err != nil {
		t.Fatalf("finalizeSyncedBootstrap failed: %v", err)
	}

	loaded, err := configfile.Load(beadsDir)
	if err != nil || loaded == nil {
		t.Fatalf("metadata.json missing: %v", err)
	}
	if loaded.GetDoltMode() != configfile.DoltModeServer {
		t.Errorf("dolt_mode = %q, want %q — shared server should set server mode", loaded.GetDoltMode(), configfile.DoltModeServer)
	}
}

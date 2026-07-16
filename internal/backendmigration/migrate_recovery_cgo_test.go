//go:build cgo && unix

package backendmigration

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/atomicfile"
	backendmigrationcontrol "github.com/steveyegge/beads/internal/backendmigration/control"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	beadssqlite "github.com/steveyegge/beads/internal/storage/sqlite"
)

func TestBackendMigrationRecoversAttemptWorkspaceAfterSIGKILL(t *testing.T) {
	if stage := os.Getenv("BEADS_TEST_BACKEND_MIGRATION_KILL_STAGE"); stage != "" {
		beadsDir := os.Getenv("BEADS_TEST_BACKEND_MIGRATION_DIR")
		backendMigrationCheckpoint = func(reached string) {
			if reached == stage {
				_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
				select {}
			}
		}
		if _, err := Apply(context.Background(), beadsDir, "beads.db"); err != nil {
			t.Fatalf("helper Apply before %s checkpoint: %v", stage, err)
		}
		t.Fatalf("helper did not reach %s checkpoint", stage)
	}

	for _, stage := range []string{"created_unstamped", "restore_journal"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			beadsDir := filepath.Join(t.TempDir(), ".beads")
			if err := os.Mkdir(beadsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			cfg := &configfile.Config{
				Database: "beads.db", Backend: configfile.BackendDolt,
				DoltMode: configfile.DoltModeEmbedded, DoltDatabase: "beads",
			}
			if err := cfg.Save(beadsDir); err != nil {
				t.Fatal(err)
			}
			source, err := embeddeddolt.Open(ctx, beadsDir, "beads", "main")
			if err != nil {
				t.Fatal(err)
			}
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command(os.Args[0], "-test.run=^TestBackendMigrationRecoversAttemptWorkspaceAfterSIGKILL$")
			cmd.Env = append(os.Environ(),
				"BEADS_TEST_BACKEND_MIGRATION_KILL_STAGE="+stage,
				"BEADS_TEST_BACKEND_MIGRATION_DIR="+beadsDir,
				"BEADS_DOLT_SERVER_MODE=",
				"BEADS_DOLT_SHARED_SERVER=",
				"BEADS_DOLT_DATA_DIR=",
				"BEADS_DOLT_SERVER_DATABASE=",
			)
			output, runErr := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			if runErr == nil || !errors.As(runErr, &exitErr) || !exitErr.ProcessState.Sys().(syscall.WaitStatus).Signaled() {
				t.Fatalf("helper was not killed at %s: err=%v\n%s", stage, runErr, output)
			}

			state, err := readAttempt(beadsDir)
			if err != nil {
				t.Fatalf("read durable attempt after %s kill: %v", stage, err)
			}
			workspace := attemptWorkspacePath(beadsDir, state.AttemptID)
			switch stage {
			case "created_unstamped":
				if _, err := os.Lstat(filepath.Join(workspace, attemptWorkspaceFile+".creating")); err != nil {
					t.Fatalf("pre-stamp kill did not leave the expected creation artifact: %v", err)
				}
			case "restore_journal":
				journals, err := filepath.Glob(filepath.Join(workspace, "*-journal"))
				if err != nil || len(journals) == 0 {
					t.Fatalf("restore kill did not leave an active SQLite journal: %v err=%v", journals, err)
				}
			}

			result, err := Apply(ctx, beadsDir, "beads.db")
			if err != nil {
				t.Fatalf("recover after %s kill: %v", stage, err)
			}
			if !result.Recovered || !result.Verified || !result.CutoverApplied {
				t.Fatalf("recovery result after %s kill = %#v", stage, result)
			}
			if _, err := os.Stat(filepath.Join(beadsDir, "beads.db")); err != nil {
				t.Fatalf("recovered SQLite target after %s kill: %v", stage, err)
			}
			if _, err := os.Lstat(attemptPath(beadsDir)); !os.IsNotExist(err) {
				t.Fatalf("recovery after %s kill left marker: %v", stage, err)
			}
			if leftovers, err := filepath.Glob(filepath.Join(beadsDir, ".backend-migration-*.work*")); err != nil || len(leftovers) != 0 {
				t.Fatalf("recovery after %s kill left attempt workspace: %v err=%v", stage, leftovers, err)
			}
		})
	}
}

func TestBackendMigrationControlRejectsPausedAndPostCutoverStaleConfigWriter(t *testing.T) {
	ctx := context.Background()
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := &configfile.Config{
		Database: "beads.db", Backend: configfile.BackendDolt,
		DoltMode: configfile.DoltModeEmbedded, DoltDatabase: "beads",
	}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	stale := *cfg
	source, err := embeddeddolt.Open(ctx, beadsDir, "beads", "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}

	reached := make(chan struct{})
	release := make(chan struct{})
	backendMigrationCheckpoint = func(stage string) {
		if stage == "created_unstamped" {
			close(reached)
			<-release
		}
	}
	defer func() { backendMigrationCheckpoint = nil }()

	type applyResult struct {
		result Result
		err    error
	}
	applied := make(chan applyResult, 1)
	go func() {
		result, err := Apply(ctx, beadsDir, "beads.db")
		applied <- applyResult{result: result, err: err}
	}()
	select {
	case <-reached:
	case <-time.After(30 * time.Second):
		t.Fatal("migration did not reach the controlled checkpoint")
	}

	stale.DoltDatabase = "stale_writer"
	staleWriteErr := stale.Save(beadsDir)
	current, currentCfg, err := configfile.ReadForBackendMigration(beadsDir)
	close(release)
	var got applyResult
	select {
	case got = <-applied:
	case <-time.After(30 * time.Second):
		t.Fatal("migration did not finish after releasing checkpoint")
	}
	if !errors.Is(staleWriteErr, configfile.ErrBackendMigrationPending) && !errors.Is(staleWriteErr, backendmigrationcontrol.ErrBusy) {
		t.Fatalf("paused stale writer error = %v, want pending or workspace-control busy", staleWriteErr)
	}
	if err != nil || currentCfg.GetBackend() != configfile.BackendDolt {
		t.Fatalf("paused writer changed pre-cutover authority: backend=%v err=%v metadata=%q", currentCfg, err, current)
	}
	if got.err != nil || !got.result.Verified || !got.result.CutoverApplied {
		t.Fatalf("Apply result = %#v, %v", got.result, got.err)
	}
	authoritative, err := os.ReadFile(configfile.ConfigPath(beadsDir))
	if err != nil {
		t.Fatal(err)
	}
	if err := stale.Save(beadsDir); !errors.Is(err, configfile.ErrConfigChanged) {
		t.Fatalf("post-cutover stale writer error = %v, want stale witness refusal", err)
	}
	after, err := os.ReadFile(configfile.ConfigPath(beadsDir))
	if err != nil || !bytes.Equal(after, authoritative) {
		t.Fatalf("post-cutover stale writer changed SQLite authority: equal=%v err=%v", bytes.Equal(after, authoritative), err)
	}
}

func TestPreviewUsesOnlyMetadataDoltDatabaseIdentity(t *testing.T) {
	ctx := context.Background()
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := &configfile.Config{
		Database: "beads.db", Backend: configfile.BackendDolt,
		DoltMode: configfile.DoltModeEmbedded, DoltDatabase: "metadata_database",
	}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	store, err := embeddeddolt.Open(ctx, beadsDir, "metadata_database", "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "wrong_database")

	result, err := Preview(ctx, beadsDir, "beads.db")
	if err != nil {
		t.Fatalf("metadata-authorized preview was redirected by the environment: %v", err)
	}
	if result.Status != "planned" || result.SourceBackend != configfile.BackendDolt {
		t.Fatalf("metadata-authorized preview result = %#v", result)
	}
}

func TestApplyRetainsOriginalAuthorityRecoveryWhenOwnedTargetBecomesHardlinked(t *testing.T) {
	ctx := context.Background()
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	originalConfig := &configfile.Config{
		Database: "beads.db", Backend: configfile.BackendDolt,
		DoltMode: configfile.DoltModeEmbedded, DoltDatabase: "beads",
	}
	originalMetadata, err := configfile.MarshalForBackendMigration(originalConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.WriteFileDurable(configfile.ConfigPath(beadsDir), originalMetadata, 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := embeddeddolt.Open(ctx, beadsDir, "beads", "main")
	if err != nil {
		t.Fatalf("initialize embedded Dolt recovery source: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	binding, err := embeddeddolt.BindMigrationSource(beadsDir, "beads")
	if err != nil {
		t.Fatal(err)
	}
	sourceIdentity, err := binding.Witness()
	if closeErr := binding.Close(); err != nil || closeErr != nil {
		t.Fatal(errors.Join(err, closeErr))
	}

	targetConfig := *originalConfig
	targetConfig.Backend = configfile.BackendSQLite
	targetConfig.SQLitePath = "beads.db"
	targetMetadata, err := configfile.MarshalForBackendMigration(&targetConfig)
	if err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(beadsDir, "beads.db")
	target, err := beadssqlite.CreateFreshForMigration(ctx, targetPath, recoveryTestAttempt)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	state := &attemptState{
		Version: attemptSchemaVersion, AttemptID: recoveryTestAttempt, Phase: phaseTargetReady,
		SQLitePath: "beads.db", SourceIdentity: sourceIdentity, WorkspaceIdentity: recoveryTestWorkspaceIdentity,
		SnapshotDigest: strings.Repeat("a", 64),
		RowCounts:      map[string]int{"issues": 0}, OriginalMetadata: originalMetadata, TargetMetadata: targetMetadata,
	}
	if err := createAttempt(beadsDir, state); err != nil {
		t.Fatal(err)
	}
	markerBefore, err := os.ReadFile(attemptPath(beadsDir))
	if err != nil {
		t.Fatal(err)
	}
	hardlinkPath := filepath.Join(beadsDir, "hardlink-witness.db")
	if err := os.Link(targetPath, hardlinkPath); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := Apply(ctx, beadsDir, state.SQLitePath); err == nil {
			t.Fatalf("recovery attempt %d unexpectedly removed a hardlinked target", attempt)
		}
		markerAfter, err := os.ReadFile(attemptPath(beadsDir))
		if err != nil || !bytes.Equal(markerAfter, markerBefore) {
			t.Fatalf("recovery attempt %d changed marker: equal=%v err=%v", attempt, bytes.Equal(markerAfter, markerBefore), err)
		}
		if _, err := configfile.Load(beadsDir); !errors.Is(err, configfile.ErrBackendMigrationPending) {
			t.Fatalf("recovery attempt %d released ordinary config gate: %v", attempt, err)
		}
		for _, path := range []string{targetPath, hardlinkPath, filepath.Join(beadsDir, "embeddeddolt")} {
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("recovery attempt %d did not preserve %s: %v", attempt, path, err)
			}
		}
	}
}

package backendmigration

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/atomicfile"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	beadssqlite "github.com/steveyegge/beads/internal/storage/sqlite"
)

const (
	recoveryTestAttempt           = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	recoveryTestWorkspaceIdentity = "1:1"
)

func TestValidateSQLiteBasename(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "beads.db", "beads-migrated.db", "migration_2026.07.db"} {
		if _, err := ValidateSQLiteBasename(value); err != nil {
			t.Errorf("ValidateSQLiteBasename(%q): %v", value, err)
		}
	}
	for _, value := range []string{
		".hidden.db", "Archive.db", "name.DB", "../beads.db", "nested/beads.db", "/tmp/beads.db",
		`C:\\tmp\\beads.db`, "beads.db?mode=memory", "beads#copy.db", "beads%2fcopy.db",
		"beads\ncopy.db", "beads.sqlite", strings.Repeat("a", 128) + ".db",
	} {
		if _, err := ValidateSQLiteBasename(value); err == nil {
			t.Errorf("ValidateSQLiteBasename(%q) unexpectedly succeeded", value)
		}
	}
}

func TestCleanupMarkerDiscoverySupportsGlobMetacharactersInWorkspacePath(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), "workspace[", ".beads")
	if err := os.MkdirAll(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerPath := attemptCleanupPath(beadsDir, recoveryTestAttempt)
	if err := os.WriteFile(markerPath, []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}

	pending, err := pendingAttempt(beadsDir)
	if err != nil || !pending {
		t.Fatalf("pendingAttempt() = %v, %v; want true, nil", pending, err)
	}
	got, err := attemptStatePath(beadsDir)
	if err != nil || got != markerPath {
		t.Fatalf("attemptStatePath() = %q, %v; want %q, nil", got, err, markerPath)
	}
}

func TestAttemptValidationAcceptsCaseInsensitiveEmbeddedMode(t *testing.T) {
	original := &configfile.Config{
		Database: "beads.db", Backend: configfile.BackendDolt,
		DoltMode: "EMBEDDED", DoltDatabase: "beads",
	}
	target := *original
	target.Backend = configfile.BackendSQLite
	target.SQLitePath = "beads.db"
	originalMetadata, err := configfile.MarshalForBackendMigration(original)
	if err != nil {
		t.Fatal(err)
	}
	targetMetadata, err := configfile.MarshalForBackendMigration(&target)
	if err != nil {
		t.Fatal(err)
	}
	state := &attemptState{
		Version: attemptSchemaVersion, AttemptID: recoveryTestAttempt, Phase: phasePrepared,
		SQLitePath: "beads.db", SourceIdentity: "v2:1:1/2:2/3:3", WorkspaceIdentity: "1:1",
		SnapshotDigest: strings.Repeat("a", 64), RowCounts: map[string]int{"issues": 0},
		OriginalMetadata: originalMetadata, TargetMetadata: targetMetadata,
	}
	if err := validateAttempt(state); err != nil {
		t.Fatalf("mixed-case embedded mode should remain recoverable: %v", err)
	}
}

func TestAttemptWitnessesAreBoundAndTransitionsCompareCompleteState(t *testing.T) {
	ctx := context.Background()
	beadsDir, state := prepareTargetAuthorityRecovery(t, ctx, false)

	forgedConfig, err := configfile.ParseForBackendMigration(state.TargetMetadata)
	if err != nil {
		t.Fatal(err)
	}
	forgedConfig.ProjectID = "forged-project"
	forged := *state
	forged.TargetMetadata, err = configfile.MarshalForBackendMigration(forgedConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAttempt(&forged); err == nil {
		t.Fatal("target metadata with unrelated changes unexpectedly matched source witness")
	}

	changed := *state
	changed.RowCounts = map[string]int{"issues": 1}
	if err := removeAttempt(beadsDir, &changed); err == nil {
		t.Fatal("marker removal accepted a matching attempt ID with different durable state")
	}
	current, err := readAttempt(beadsDir)
	if err != nil {
		t.Fatalf("marker was not preserved after state mismatch: %v", err)
	}
	if !reflect.DeepEqual(current, state) {
		t.Fatalf("preserved marker changed: got %#v want %#v", current, state)
	}
	t.Setenv("BEADS_DOLT_DATA_DIR", filepath.Join(t.TempDir(), "ambient-override"))
	if _, err := readAttempt(beadsDir); err != nil {
		t.Fatalf("ambient Dolt data-dir override changed durable marker parsing: %v", err)
	}
}

func TestAttemptRemovalNeverDeletesConcurrentReplacementAndRecoversQuarantine(t *testing.T) {
	ctx := context.Background()
	beadsDir, state := prepareTargetAuthorityRecovery(t, ctx, true)
	foreign := *state
	foreign.AttemptID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	foreignBytes, err := marshalAttempt(&foreign)
	if err != nil {
		t.Fatal(err)
	}
	preservedOriginal := filepath.Join(beadsDir, "preserved-original-marker.lock")
	var hookErr error
	err = quarantineAndRemoveAttempt(beadsDir, state, func() {
		if renameErr := os.Rename(attemptPath(beadsDir), preservedOriginal); renameErr != nil {
			hookErr = renameErr
			return
		}
		hookErr = os.WriteFile(attemptPath(beadsDir), foreignBytes, 0o600)
	})
	if hookErr != nil {
		t.Fatalf("replace marker during removal: %v", hookErr)
	}
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("concurrent marker replacement removal error = %v", err)
	}
	canonical, err := os.ReadFile(attemptPath(beadsDir))
	if err != nil || !bytes.Equal(canonical, foreignBytes) {
		t.Fatalf("concurrent replacement was not restored exactly: equal=%v err=%v", bytes.Equal(canonical, foreignBytes), err)
	}
	if _, err := os.Stat(preservedOriginal); err != nil {
		t.Fatalf("original marker fixture was lost: %v", err)
	}
	if _, err := os.Lstat(attemptCleanupPath(beadsDir, state.AttemptID)); !os.IsNotExist(err) {
		t.Fatalf("changed marker remained under the wrong quarantine name: %v", err)
	}
	if _, err := configfile.Load(beadsDir); !errors.Is(err, configfile.ErrBackendMigrationPending) {
		t.Fatalf("concurrent replacement released config gate: %v", err)
	}

	if err := os.Remove(attemptPath(beadsDir)); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(preservedOriginal, attemptCleanupPath(beadsDir, state.AttemptID)); err != nil {
		t.Fatal(err)
	}
	if pending, err := pendingAttempt(beadsDir); err != nil || !pending {
		t.Fatalf("quarantined marker pending = %v, err=%v", pending, err)
	}
	recovered, err := readAttempt(beadsDir)
	if err != nil || !attemptStatesEqual(recovered, state) {
		t.Fatalf("read quarantined marker = %#v, err=%v", recovered, err)
	}
	if err := removeAttempt(beadsDir, state); err != nil {
		t.Fatalf("finish exact quarantined marker removal: %v", err)
	}
	if pending, err := pendingAttempt(beadsDir); err != nil || pending {
		t.Fatalf("completed quarantine removal pending = %v, err=%v", pending, err)
	}
}

func TestAttemptWorkspaceCleanupRecoversCrashArtifactsAndQuarantine(t *testing.T) {
	for _, quarantined := range []bool{false, true} {
		name := "canonical"
		if quarantined {
			name = "quarantined"
		}
		t.Run(name, func(t *testing.T) {
			beadsDir := filepath.Join(t.TempDir(), ".beads")
			if err := os.Mkdir(beadsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			identity, err := createAttemptWorkspace(beadsDir, recoveryTestAttempt)
			if err != nil {
				t.Fatal(err)
			}
			workspace := attemptWorkspacePath(beadsDir, recoveryTestAttempt)
			for _, name := range []string{
				attemptWorkspaceFile + ".creating",
				attemptWorkspaceFile + ".creating-journal",
			} {
				if err := os.WriteFile(filepath.Join(workspace, name), []byte("crash artifact"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if quarantined {
				if err := os.Rename(workspace, attemptWorkspaceCleanupPath(beadsDir, recoveryTestAttempt)); err != nil {
					t.Fatal(err)
				}
			}
			if err := removeAttemptWorkspace(beadsDir, recoveryTestAttempt, identity); err != nil {
				t.Fatalf("recover attempt workspace cleanup: %v", err)
			}
			for _, path := range []string{workspace, attemptWorkspaceCleanupPath(beadsDir, recoveryTestAttempt)} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("attempt workspace residue %s: %v", path, err)
				}
			}
		})
	}
}

func TestAttemptWorkspaceCleanupRejectsReplacementAndUnexpectedFiles(t *testing.T) {
	t.Run("replacement", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		if err := os.Mkdir(beadsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		identity, err := createAttemptWorkspace(beadsDir, recoveryTestAttempt)
		if err != nil {
			t.Fatal(err)
		}
		workspace := attemptWorkspacePath(beadsDir, recoveryTestAttempt)
		preserved := workspace + ".preserved"
		if err := os.Rename(workspace, preserved); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := removeAttemptWorkspace(beadsDir, recoveryTestAttempt, identity); err == nil {
			t.Fatal("replacement attempt workspace was removed")
		}
		for _, path := range []string{workspace, preserved} {
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("replacement check did not preserve %s: %v", path, err)
			}
		}
	})

	t.Run("unexpected entry", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		if err := os.Mkdir(beadsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		identity, err := createAttemptWorkspace(beadsDir, recoveryTestAttempt)
		if err != nil {
			t.Fatal(err)
		}
		workspace := attemptWorkspacePath(beadsDir, recoveryTestAttempt)
		foreign := filepath.Join(workspace, "foreign.txt")
		if err := os.WriteFile(foreign, []byte("preserve"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := removeAttemptWorkspace(beadsDir, recoveryTestAttempt, identity); err == nil {
			t.Fatal("unexpected workspace entry was removed")
		}
		if data, err := os.ReadFile(filepath.Join(attemptWorkspaceCleanupPath(beadsDir, recoveryTestAttempt), "foreign.txt")); err != nil || string(data) != "preserve" {
			t.Fatalf("unexpected workspace entry was not preserved: data=%q err=%v", data, err)
		}
	})
}

func TestPreviewRefusesExistingTargetWithoutOtherEffects(t *testing.T) {
	ctx := context.Background()
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.MkdirAll(filepath.Join(beadsDir, "embeddeddolt", "beads"), 0o700); err != nil {
		t.Fatal(err)
	}
	metadata, err := configfile.MarshalForBackendMigration(&configfile.Config{
		Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded, DoltDatabase: "beads",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configfile.ConfigPath(beadsDir), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(beadsDir, "beads.db")
	before := []byte("legacy SQLite must survive")
	if err := os.WriteFile(targetPath, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Preview(ctx, beadsDir, "beads.db"); err == nil || !strings.Contains(err.Error(), "--sqlite-path") {
		t.Fatalf("preview collision error = %v", err)
	}
	after, err := os.ReadFile(targetPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("preview changed collision: after=%q err=%v", after, err)
	}
	if _, err := os.Lstat(attemptPath(beadsDir)); !os.IsNotExist(err) {
		t.Fatalf("preview created recovery marker: %v", err)
	}
}

func TestPreviewReportsSourceAdmissionBeforeTargetCollision(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata, err := configfile.MarshalForBackendMigration(&configfile.Config{
		Backend:  configfile.BackendDolt,
		DoltMode: configfile.DoltModeServer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configfile.ConfigPath(beadsDir), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(beadsDir, "beads.db")
	target := []byte("unrelated target must survive")
	if err := os.WriteFile(targetPath, target, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = Preview(context.Background(), beadsDir, "beads.db")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "server") || strings.Contains(err.Error(), "--sqlite-path") {
		t.Fatalf("source admission was masked by target collision: %v", err)
	}
	after, readErr := os.ReadFile(targetPath)
	if readErr != nil || !bytes.Equal(after, target) {
		t.Fatalf("source-admission refusal changed target: equal=%v err=%v", bytes.Equal(after, target), readErr)
	}
}

func TestPreviewLegacyOnlyMetadataReturnsActionablePrerequisiteWithoutEffects(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(beadsDir, "config.json")
	legacy := []byte(`{"backend":"dolt","dolt_mode":"embedded"}`)
	if err := os.WriteFile(legacyPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Preview(context.Background(), beadsDir, "beads.db")
	var safe *SafeError
	if !errors.As(err, &safe) {
		t.Fatalf("legacy-only preview error = %T %v, want SafeError", err, err)
	}
	if safe.Code() != string(ErrorCodeUnsupported) || !strings.Contains(safe.Error(), "bd doctor") {
		t.Fatalf("legacy-only preview error = %#v, want actionable doctor prerequisite", safe)
	}
	if safe.RetryCommand() != "" {
		t.Fatalf("legacy-only preview retry = %q, want none", safe.RetryCommand())
	}
	after, readErr := os.ReadFile(legacyPath)
	if readErr != nil || !bytes.Equal(after, legacy) {
		t.Fatalf("legacy-only preview changed config.json: equal=%v err=%v", bytes.Equal(after, legacy), readErr)
	}
	if _, statErr := os.Lstat(configfile.ConfigPath(beadsDir)); !os.IsNotExist(statErr) {
		t.Fatalf("legacy-only preview created metadata.json: %v", statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(beadsDir, "beads.db")); !os.IsNotExist(statErr) {
		t.Fatalf("legacy-only preview created SQLite target: %v", statErr)
	}
}

func TestSafeFailureDoesNotSuggestRetryForUnclassifiedAdmissionFailure(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	safe := SafeFailure(beadsDir, errors.New("unclassified admission failure"), "archive.db")
	if safe.RetryCommand() != "" {
		t.Fatalf("generic safety failure retry = %q, want none", safe.RetryCommand())
	}
}

func TestInspectRefusesGroupOrWorldWritableWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACLs do not expose POSIX group/world write bits")
	}
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(beadsDir, 0o777); err != nil {
		t.Skipf("workspace permissions cannot be changed on this platform: %v", err)
	}
	info, err := os.Stat(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o022 == 0 {
		t.Skip("platform does not expose group/world writable directory bits")
	}
	if _, err := Inspect(beadsDir, "beads.db"); err == nil || !strings.Contains(err.Error(), "writable") {
		t.Fatalf("insecure workspace admission error = %v", err)
	}
}

func TestApplyRecoversAdoptedSQLiteAuthorityWithoutDigestRollback(t *testing.T) {
	ctx := context.Background()
	beadsDir, state := prepareTargetAuthorityRecovery(t, ctx, true)
	result, err := Apply(ctx, beadsDir, state.SQLitePath)
	if err != nil {
		t.Fatalf("recover adopted SQLite authority: %v", err)
	}
	if !result.Recovered || !result.Verified || !result.CutoverApplied {
		t.Fatalf("recovery result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(beadsDir, state.SQLitePath)); err != nil {
		t.Fatalf("recovery removed authoritative SQLite target: %v", err)
	}
	if _, err := os.Lstat(attemptPath(beadsDir)); !os.IsNotExist(err) {
		t.Fatalf("recovery left marker: %v", err)
	}
}

func TestApplyPreservesBothStoresWhenMetadataAuthorityDiverges(t *testing.T) {
	ctx := context.Background()
	beadsDir, state := prepareTargetAuthorityRecovery(t, ctx, false)
	diverged := append([]byte(nil), state.TargetMetadata...)
	diverged = append(diverged, '\n')
	if err := atomicfile.WriteFileDurable(configfile.ConfigPath(beadsDir), diverged, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, beadsDir, state.SQLitePath); err == nil || !strings.Contains(err.Error(), "diverged") {
		t.Fatalf("diverged authority error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(beadsDir, state.SQLitePath)); err != nil {
		t.Fatalf("diverged recovery removed target: %v", err)
	}
	if _, err := os.Stat(attemptPath(beadsDir)); err != nil {
		t.Fatalf("diverged recovery removed marker: %v", err)
	}
}

func TestApplyPreservesTargetAuthorityRecoveryWhenSourceIdentityChanged(t *testing.T) {
	ctx := context.Background()
	beadsDir, state := prepareTargetAuthorityRecovery(t, ctx, true)
	markerBefore, err := os.ReadFile(attemptPath(beadsDir))
	if err != nil {
		t.Fatal(err)
	}

	sourcePath := filepath.Join(beadsDir, "embeddeddolt")
	preservedSourcePath := filepath.Join(beadsDir, "embeddeddolt-preserved")
	if err := os.Rename(sourcePath, preservedSourcePath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sourcePath, "beads"), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := Apply(ctx, beadsDir, state.SQLitePath); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("replaced source recovery error = %v", err)
	}
	markerAfter, err := os.ReadFile(attemptPath(beadsDir))
	if err != nil || !bytes.Equal(markerAfter, markerBefore) {
		t.Fatalf("source replacement changed recovery marker: equal=%v err=%v", bytes.Equal(markerAfter, markerBefore), err)
	}
	for _, path := range []string{
		filepath.Join(beadsDir, state.SQLitePath), sourcePath, preservedSourcePath,
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("source replacement recovery did not preserve %s: %v", path, err)
		}
	}
}

func TestApplyRetainsRecoveryGateWhenTargetAuthorityCleanupFails(t *testing.T) {
	for name, relativeArtifact := range map[string]string{
		"published staging":       "staging",
		"prepublication creation": "creating",
		"orphan creation sidecar": "creating-journal",
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			beadsDir, state := prepareTargetAuthorityRecovery(t, ctx, true)
			markerBefore, err := os.ReadFile(attemptPath(beadsDir))
			if err != nil {
				t.Fatal(err)
			}
			stagingPath := filepath.Join(beadsDir, stagingBasename(state.AttemptID))
			artifactPath := stagingPath
			switch relativeArtifact {
			case "creating":
				artifactPath += ".creating"
			case "creating-journal":
				artifactPath += ".creating-journal"
			}
			foreign := []byte("foreign staging collision must survive")
			if err := os.WriteFile(artifactPath, foreign, 0o600); err != nil {
				t.Fatal(err)
			}

			for attempt := 1; attempt <= 2; attempt++ {
				if _, err := Apply(ctx, beadsDir, state.SQLitePath); err == nil {
					t.Fatalf("recovery attempt %d unexpectedly ignored foreign staging artifact", attempt)
				}
				markerAfter, err := os.ReadFile(attemptPath(beadsDir))
				if err != nil || !bytes.Equal(markerAfter, markerBefore) {
					t.Fatalf("recovery attempt %d changed marker: equal=%v err=%v", attempt, bytes.Equal(markerAfter, markerBefore), err)
				}
				if _, err := configfile.Load(beadsDir); !errors.Is(err, configfile.ErrBackendMigrationPending) {
					t.Fatalf("recovery attempt %d released ordinary config gate: %v", attempt, err)
				}
				data, err := os.ReadFile(artifactPath)
				if err != nil || !bytes.Equal(data, foreign) {
					t.Fatalf("recovery attempt %d changed foreign staging artifact: data=%q err=%v", attempt, data, err)
				}
				if _, err := os.Stat(filepath.Join(beadsDir, state.SQLitePath)); err != nil {
					t.Fatalf("recovery attempt %d removed authoritative SQLite target: %v", attempt, err)
				}
			}
		})
	}
}

func TestReadAttemptRejectsSymlinkAndDuplicateKeys(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(t.TempDir(), "foreign.json")
	if err := os.WriteFile(foreign, []byte(`{"foreign":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, attemptPath(beadsDir)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := readAttempt(beadsDir); err == nil {
		t.Fatal("symlinked attempt marker unexpectedly read")
	}
	if err := os.Remove(attemptPath(beadsDir)); err != nil {
		t.Fatal(err)
	}
	duplicate := `{"version":1,"version":1,"attempt_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","phase":"prepared","sqlite_path":"beads.db","snapshot_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","row_counts":{"issues":0},"original_metadata":"eA==","target_metadata":"eQ=="}`
	if err := os.WriteFile(attemptPath(beadsDir), []byte(duplicate), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAttempt(beadsDir); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate marker error = %v", err)
	}
}

func prepareTargetAuthorityRecovery(t *testing.T, ctx context.Context, adopted bool) (string, *attemptState) {
	t.Helper()
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(beadsDir, "embeddeddolt", "beads"), 0o700); err != nil {
		t.Fatal(err)
	}
	sourceBinding, err := embeddeddolt.BindMigrationSource(beadsDir, "beads")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sourceBinding.Close() })
	sourceIdentity, err := sourceBinding.Witness()
	if err != nil {
		t.Fatal(err)
	}
	originalConfig := &configfile.Config{Database: "beads.db", Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded}
	targetConfig := *originalConfig
	targetConfig.Backend = configfile.BackendSQLite
	targetConfig.SQLitePath = "beads.db"
	originalMetadata, err := configfile.MarshalForBackendMigration(originalConfig)
	if err != nil {
		t.Fatal(err)
	}
	targetMetadata, err := configfile.MarshalForBackendMigration(&targetConfig)
	if err != nil {
		t.Fatal(err)
	}
	target, err := beadssqlite.CreateFreshForMigration(ctx, filepath.Join(beadsDir, "beads.db"), recoveryTestAttempt)
	if err != nil {
		t.Fatal(err)
	}
	if adopted {
		if err := target.MarkAdopted(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	state := &attemptState{
		Version: attemptSchemaVersion, AttemptID: recoveryTestAttempt, Phase: phaseCutover,
		SQLitePath: "beads.db", SourceIdentity: sourceIdentity, WorkspaceIdentity: recoveryTestWorkspaceIdentity,
		SnapshotDigest: strings.Repeat("a", 64),
		RowCounts:      map[string]int{"issues": 0}, OriginalMetadata: originalMetadata, TargetMetadata: targetMetadata,
	}
	if err := createAttempt(beadsDir, state); err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.WriteFileDurable(configfile.ConfigPath(beadsDir), targetMetadata, 0o600); err != nil {
		t.Fatal(err)
	}
	return beadsDir, state
}

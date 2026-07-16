package configfile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backendmigrationcontrol "github.com/steveyegge/beads/internal/backendmigration/control"
)

func TestLoadRejectsPendingBackendMigrationWhileRecoveryReadRemainsAvailable(t *testing.T) {
	beadsDir := t.TempDir()
	cfg := &Config{Database: "beads.db", Backend: BackendDolt, DoltMode: DoltModeEmbedded}
	metadata, err := MarshalForBackendMigration(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(beadsDir), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, BackendMigrationStateFileName), []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(beadsDir); !errors.Is(err, ErrBackendMigrationPending) {
		t.Fatalf("Load pending migration error = %v", err)
	}
	exact, recovered, err := ReadForBackendMigration(beadsDir)
	if err != nil {
		t.Fatalf("ReadForBackendMigration during recovery: %v", err)
	}
	if !bytes.Equal(exact, metadata) || recovered.GetBackend() != BackendDolt {
		t.Fatalf("recovery witness/config = %q, %#v", exact, recovered)
	}
}

func TestSaveRejectsPendingBackendMigrationWithoutChangingMetadata(t *testing.T) {
	for _, markerName := range []string{
		BackendMigrationStateFileName,
		".backend-migration-test.cleanup.lock",
	} {
		t.Run(markerName, func(t *testing.T) {
			beadsDir := t.TempDir()
			original := &Config{Database: "beads.db", Backend: BackendDolt, DoltMode: DoltModeEmbedded}
			if err := original.Save(beadsDir); err != nil {
				t.Fatalf("save original metadata: %v", err)
			}
			before, err := os.ReadFile(ConfigPath(beadsDir))
			if err != nil {
				t.Fatalf("read original metadata: %v", err)
			}
			if err := os.WriteFile(filepath.Join(beadsDir, markerName), []byte("pending"), 0o600); err != nil {
				t.Fatalf("write migration marker: %v", err)
			}

			updated := *original
			updated.Backend = BackendSQLite
			updated.SQLitePath = "beads.db"
			if err := updated.Save(beadsDir); !errors.Is(err, ErrBackendMigrationPending) {
				t.Fatalf("Save pending migration error = %v", err)
			}
			after, err := os.ReadFile(ConfigPath(beadsDir))
			if err != nil {
				t.Fatalf("read metadata after refused save: %v", err)
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("refused Save changed metadata:\nbefore: %s\nafter: %s", before, after)
			}
		})
	}
}

func TestRejectPendingBackendMigrationFindsCleanupMarkerInGlobMetacharacterPath(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), "workspace[", ".beads")
	if err := os.MkdirAll(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, ".backend-migration-test.cleanup.lock"), []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RejectPendingBackendMigration(beadsDir); !errors.Is(err, ErrBackendMigrationPending) {
		t.Fatalf("cleanup marker in metacharacter path error = %v", err)
	}
}

func TestLoadReadOnlyPreservesLegacyConfig(t *testing.T) {
	beadsDir := t.TempDir()
	legacyPath := filepath.Join(beadsDir, "config.json")
	legacy := []byte(`{"backend":"dolt","dolt_mode":"embedded"}`)
	if err := os.WriteFile(legacyPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadReadOnly(beadsDir)
	if err != nil || cfg == nil || cfg.GetBackend() != BackendDolt {
		t.Fatalf("LoadReadOnly() = %#v, %v", cfg, err)
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil || !bytes.Equal(after, legacy) {
		t.Fatalf("LoadReadOnly changed legacy config: equal=%v err=%v", bytes.Equal(after, legacy), err)
	}
	if _, err := os.Lstat(ConfigPath(beadsDir)); !os.IsNotExist(err) {
		t.Fatalf("LoadReadOnly created metadata.json: %v", err)
	}
}

func TestLoadAuthoritativeReadOnlyPreservesLegacyConfig(t *testing.T) {
	beadsDir := t.TempDir()
	legacyPath := filepath.Join(beadsDir, "config.json")
	legacy := []byte(`{"backend":"dolt","dolt_mode":"embedded","dolt_database":"legacy_board"}`)
	if err := os.WriteFile(legacyPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadAuthoritativeReadOnly(beadsDir)
	if err != nil || cfg == nil || cfg.GetBackend() != BackendDolt || cfg.GetDoltDatabase() != "legacy_board" {
		t.Fatalf("LoadAuthoritativeReadOnly() = %#v, %v", cfg, err)
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil || !bytes.Equal(after, legacy) {
		t.Fatalf("authoritative read changed legacy config: equal=%v err=%v", bytes.Equal(after, legacy), err)
	}
	if _, err := os.Lstat(ConfigPath(beadsDir)); !os.IsNotExist(err) {
		t.Fatalf("authoritative read created metadata.json: %v", err)
	}
}

func TestLoadAuthoritativeReadOnlyRejectsAmbiguousLegacyConfig(t *testing.T) {
	beadsDir := t.TempDir()
	legacyPath := filepath.Join(beadsDir, "config.json")
	legacy := []byte(`{"backend":"dolt","Backend":"sqlite"}`)
	if err := os.WriteFile(legacyPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadAuthoritativeReadOnly(beadsDir); err == nil {
		t.Fatal("LoadAuthoritativeReadOnly accepted ambiguous legacy metadata")
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil || !bytes.Equal(after, legacy) {
		t.Fatalf("refused authoritative read changed legacy config: equal=%v err=%v", bytes.Equal(after, legacy), err)
	}
	if _, err := os.Lstat(ConfigPath(beadsDir)); !os.IsNotExist(err) {
		t.Fatalf("refused authoritative read created metadata.json: %v", err)
	}
}

func TestLoadReadOnlyRejectsLinkedMetadata(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "foreign.json")
	if err := os.WriteFile(target, []byte(`{"backend":"dolt"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, ConfigPath(beadsDir)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := LoadReadOnly(beadsDir); err == nil {
		t.Fatal("LoadReadOnly accepted linked metadata")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != `{"backend":"dolt"}` {
		t.Fatalf("LoadReadOnly changed linked target: data=%q err=%v", data, err)
	}
}

func TestSaveRefusesWhileBackendMigrationControlsWorkspace(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	original := &Config{Database: "beads.db", Backend: BackendDolt}
	if err := original.Save(beadsDir); err != nil {
		t.Fatal(err)
	}

	guard, err := backendmigrationcontrol.TryAcquire(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close() //nolint:errcheck // test cleanup

	updated := *original
	updated.DoltDatabase = "stale_writer"
	if err := updated.Save(beadsDir); !errors.Is(err, backendmigrationcontrol.ErrBusy) {
		t.Fatalf("Save during controlled migration error = %v, want ErrBusy", err)
	}
}

func TestSaveHoldsWorkspaceControlThroughMetadataPublication(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	original := &Config{Database: "beads.db", Backend: BackendDolt}
	if err := original.Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	updated := *original
	updated.DoltDatabase = "updated"

	reached := make(chan struct{})
	release := make(chan struct{})
	configSaveCheckpoint = func(stage string) {
		if stage == "guarded" {
			close(reached)
			<-release
		}
	}
	defer func() { configSaveCheckpoint = nil }()
	saved := make(chan error, 1)
	go func() { saved <- updated.Save(beadsDir) }()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("Save did not reach guarded metadata publication")
	}

	contending, contentionErr := backendmigrationcontrol.TryAcquire(beadsDir)
	if contending != nil {
		_ = contending.Close()
	}
	close(release)
	var saveErr error
	select {
	case saveErr = <-saved:
	case <-time.After(5 * time.Second):
		t.Fatal("Save did not finish after checkpoint release")
	}
	if !errors.Is(contentionErr, backendmigrationcontrol.ErrBusy) {
		t.Fatalf("workspace control during Save = %v, want ErrBusy", contentionErr)
	}
	if saveErr != nil {
		t.Fatalf("guarded Save: %v", saveErr)
	}
}

func TestSaveRefusesStaleMetadataWitness(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	original := &Config{Database: "beads.db", Backend: BackendDolt}
	if err := original.Save(beadsDir); err != nil {
		t.Fatal(err)
	}

	stale := *original
	staleWitness := bytes.Clone(stale.sourceBytes)
	fresh := *original
	fresh.DoltDatabase = "newer_writer"
	if err := fresh.Save(beadsDir); err != nil {
		t.Fatalf("fresh Save: %v", err)
	}
	if !bytes.Equal(stale.sourceBytes, staleWitness) {
		t.Fatal("fresh Save mutated the stale copy's metadata witness")
	}
	stale.Backend = BackendSQLite
	stale.SQLitePath = "beads.db"
	if err := stale.Save(beadsDir); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("stale Save error = %v, want ErrConfigChanged", err)
	}
	cfg, err := LoadReadOnly(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GetBackend() != BackendDolt || cfg.DoltDatabase != "newer_writer" {
		t.Fatalf("stale Save changed current config: %#v", cfg)
	}
}

func TestSaveRejectsBackendTransitionWithoutExplicitReinitialization(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	original := &Config{Database: "dolt", Backend: BackendDolt, DoltMode: DoltModeEmbedded}
	if err := original.Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(ConfigPath(beadsDir))
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadReadOnly(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Backend = BackendSQLite
	loaded.SQLitePath = "beads.db"

	for name, candidate := range map[string]*Config{
		"loaded config":      loaded,
		"source-less config": {Backend: BackendSQLite, SQLitePath: "beads.db"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := candidate.Save(beadsDir); !errors.Is(err, ErrBackendTransitionRequiresReinit) {
				t.Fatalf("Save backend transition error = %v, want ErrBackendTransitionRequiresReinit", err)
			}
			after, err := os.ReadFile(ConfigPath(beadsDir))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("refused backend transition changed metadata:\nbefore: %s\nafter: %s", before, after)
			}
		})
	}
}

func TestSaveAfterBackendReinitializationAuthorizesTransition(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	original := &Config{Database: "dolt", Backend: BackendDolt, DoltMode: DoltModeEmbedded}
	if err := original.Save(beadsDir); err != nil {
		t.Fatal(err)
	}

	updated, err := LoadReadOnly(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	updated.Backend = BackendSQLite
	updated.SQLitePath = "beads.db"
	if err := updated.SaveAfterBackendReinitialization(beadsDir); err != nil {
		t.Fatalf("SaveAfterBackendReinitialization: %v", err)
	}

	got, err := LoadReadOnly(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.GetBackend() != BackendSQLite || got.GetSQLitePath() != "beads.db" {
		t.Fatalf("authorized backend config = %#v, want SQLite beads.db", got)
	}
}

func TestSaveMarkerOnlyRefusalDoesNotCreateControlFile(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(beadsDir, BackendMigrationStateFileName)
	if err := os.WriteFile(markerPath, []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (&Config{Backend: BackendDolt}).Save(beadsDir); !errors.Is(err, ErrBackendMigrationPending) {
		t.Fatalf("marker-only Save error = %v, want pending", err)
	}
	entries, err := os.ReadDir(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != BackendMigrationStateFileName {
		t.Fatalf("marker-only refusal changed workspace entries: %v", entries)
	}
}

func TestReadForBackendMigrationIsStrictAndEffectFree(t *testing.T) {
	beadsDir := t.TempDir()
	legacyPath := filepath.Join(beadsDir, "config.json")
	if err := os.WriteFile(legacyPath, []byte(`{"backend":"dolt"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadForBackendMigration(beadsDir); err == nil {
		t.Fatal("strict migration read unexpectedly fell back to legacy config.json")
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("strict migration read removed legacy config: %v", err)
	}
	if _, err := os.Lstat(ConfigPath(beadsDir)); !os.IsNotExist(err) {
		t.Fatalf("strict migration read created metadata.json: %v", err)
	}
}

func TestReadForBackendMigrationRejectsAmbiguousMetadata(t *testing.T) {
	for name, payload := range map[string]string{
		"unknown field":     `{"backend":"dolt","surprise":true}`,
		"duplicate field":   `{"backend":"dolt","backend":"sqlite"}`,
		"case variant":      `{"backend":"dolt","Backend":"sqlite"}`,
		"null field":        `{"backend":null}`,
		"trailing document": `{"backend":"dolt"} {}`,
		"unsupported mode":  `{"backend":"dolt","dolt_mode":"shared-magic"}`,
	} {
		t.Run(name, func(t *testing.T) {
			beadsDir := t.TempDir()
			if err := os.WriteFile(ConfigPath(beadsDir), []byte(payload), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := ReadForBackendMigration(beadsDir); err == nil {
				t.Fatalf("ambiguous metadata unexpectedly accepted: %s", payload)
			}
		})
	}

	beadsDir := t.TempDir()
	oversized := strings.Repeat(" ", maxBackendMigrationMetadata+1)
	if err := os.WriteFile(ConfigPath(beadsDir), []byte(oversized), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadForBackendMigration(beadsDir); err == nil {
		t.Fatal("oversized migration metadata unexpectedly accepted")
	}
}

package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	backendmigrationcontrol "github.com/steveyegge/beads/internal/backendmigration/control"
)

func TestBackendSelectionConfigWritesRespectMigrationControl(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(beadsDir, "config.yaml")
	original := []byte("dolt:\n  mode: embedded\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatal(err)
	}

	guard, err := backendmigrationcontrol.TryAcquire(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close() //nolint:errcheck // test cleanup

	if err := SetYamlConfigInDir(beadsDir, "dolt.shared-server", "true"); !errors.Is(err, backendmigrationcontrol.ErrBusy) {
		t.Fatalf("backend-selection write error = %v, want ErrBusy", err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil || string(after) != string(original) {
		t.Fatalf("blocked backend-selection write changed config: got=%q err=%v", after, err)
	}
}

func TestBackendSelectionConfigWritesRejectDurableMigrationRecovery(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(beadsDir, "config.yaml")
	original := []byte("dolt:\n  mode: embedded\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "backend-migration.lock"), []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetYamlConfigInDir(beadsDir, "dolt.shared-server", "true"); err == nil {
		t.Fatal("backend-selection write bypassed durable migration recovery")
	}
	after, err := os.ReadFile(configPath)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatalf("blocked backend-selection write changed config: equal=%v err=%v", bytes.Equal(after, original), err)
	}
	if _, err := os.Lstat(filepath.Join(beadsDir, backendmigrationcontrol.FileName)); !os.IsNotExist(err) {
		t.Fatalf("marker-only refusal created workspace control: %v", err)
	}
}

func TestReadWorkspaceDoltSelectionMergesLocalOverride(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"), []byte("dolt:\n  mode: embedded\n  shared-server: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "config.local.yaml"), []byte("dolt:\n  shared-server: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	mode, shared, err := ReadWorkspaceDoltSelection(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "embedded" || !shared {
		t.Fatalf("workspace selection = mode %q shared %v, want embedded/true", mode, shared)
	}
}

func TestReadWorkspaceDoltSelectionRejectsMalformedConfig(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"), []byte("dolt: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadWorkspaceDoltSelection(beadsDir); err == nil {
		t.Fatal("malformed workspace config unexpectedly accepted")
	}
}

func TestReadWorkspaceDoltSelectionRejectsUnsafeLocalOverride(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{
			name: "malformed",
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("dolt: ["), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink",
			setup: func(t *testing.T, path string) {
				t.Helper()
				target := filepath.Join(filepath.Dir(path), "foreign.yaml")
				if err := os.WriteFile(target, []byte("dolt:\n  shared-server: false\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			beadsDir := filepath.Join(t.TempDir(), ".beads")
			if err := os.Mkdir(beadsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"), []byte("dolt:\n  mode: embedded\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			test.setup(t, filepath.Join(beadsDir, "config.local.yaml"))
			if _, _, err := ReadWorkspaceDoltSelection(beadsDir); err == nil {
				t.Fatal("unsafe local override unexpectedly accepted")
			}
		})
	}
}

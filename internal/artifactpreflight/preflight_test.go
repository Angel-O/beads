package artifactpreflight

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
)

func TestPreflightRejectsEveryMigrationMarkerBeforeConfigInspection(t *testing.T) {
	for _, marker := range []string{
		configfile.BackendMigrationStateFileName,
		".backend-migration-test.cleanup.lock",
	} {
		t.Run(marker, func(t *testing.T) {
			root := t.TempDir()
			legacyDir := filepath.Join(root, "a", ".beads")
			markedDir := filepath.Join(root, "z", "deep", ".beads")
			for _, dir := range []string{legacyDir, markedDir} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			legacy := []byte(`{"backend":"dolt","dolt_mode":"embedded"}`)
			if err := os.WriteFile(filepath.Join(legacyDir, "config.json"), legacy, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(markedDir, marker), []byte("pending"), 0o600); err != nil {
				t.Fatal(err)
			}

			if _, err := Preflight(root); !errors.Is(err, configfile.ErrBackendMigrationPending) {
				t.Fatalf("Preflight error = %v, want pending migration", err)
			}
			after, err := os.ReadFile(filepath.Join(legacyDir, "config.json"))
			if err != nil || !bytes.Equal(after, legacy) {
				t.Fatalf("preflight changed legacy config: equal=%v err=%v", bytes.Equal(after, legacy), err)
			}
			if _, err := os.Lstat(configfile.ConfigPath(legacyDir)); !os.IsNotExist(err) {
				t.Fatalf("preflight created metadata.json: %v", err)
			}
		})
	}
}

func TestPreflightRejectsLinkedBeadsEntryBeforeInspectingOtherWorkspaces(t *testing.T) {
	root := t.TempDir()
	regularDir := filepath.Join(root, "a", ".beads")
	if err := os.MkdirAll(regularDir, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata := []byte(`{"backend":"dolt"}`)
	if err := os.WriteFile(configfile.ConfigPath(regularDir), metadata, 0o600); err != nil {
		t.Fatal(err)
	}

	linkedTarget := filepath.Join(root, "linked-target")
	if err := os.Mkdir(linkedTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	linkedEntry := filepath.Join(root, "z", ".beads")
	if err := os.MkdirAll(filepath.Dir(linkedEntry), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(linkedTarget, linkedEntry); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := Preflight(root); err == nil {
		t.Fatal("preflight ignored a linked .beads entry")
	}
	after, err := os.ReadFile(configfile.ConfigPath(regularDir))
	if err != nil || !bytes.Equal(after, metadata) {
		t.Fatalf("failed preflight changed earlier metadata: equal=%v err=%v", bytes.Equal(after, metadata), err)
	}
}

func TestPreflightClassifiesBackendsAndRedirectCruft(t *testing.T) {
	root := t.TempDir()
	doltDir := filepath.Join(root, "a", ".beads")
	sqliteDir := filepath.Join(root, "b", ".beads")
	cruftDir := filepath.Join(root, "polecats", "worker", ".beads")
	for _, dir := range []string{doltDir, sqliteDir, cruftDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(configfile.ConfigPath(doltDir), []byte(`{"backend":"dolt"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configfile.ConfigPath(sqliteDir), []byte(`{"backend":"sqlite","sqlite_path":"beads.db"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configfile.ConfigPath(cruftDir), []byte("invalid disposable cruft"), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, err := Preflight(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Workspaces) != 3 {
		t.Fatalf("workspace count = %d, want 3: %#v", len(snapshot.Workspaces), snapshot.Workspaces)
	}
	byPath := make(map[string]Workspace, len(snapshot.Workspaces))
	for _, workspace := range snapshot.Workspaces {
		byPath[workspace.BeadsDir] = workspace
	}
	if got := byPath[doltDir]; got.Backend != configfile.BackendDolt || got.CruftOnly {
		t.Fatalf("Dolt classification = %#v", got)
	}
	if got := byPath[sqliteDir]; got.Backend != configfile.BackendSQLite || got.CruftOnly {
		t.Fatalf("SQLite classification = %#v", got)
	}
	if got := byPath[cruftDir]; !got.CruftOnly {
		t.Fatalf("redirect cruft classification = %#v", got)
	}
}

func TestPreflightRejectsUnknownBackendWithoutTouchingArtifacts(t *testing.T) {
	root := t.TempDir()
	beadsDir := filepath.Join(root, ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configfile.ConfigPath(beadsDir), []byte(`{"backend":"future-store"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	walPath := filepath.Join(beadsDir, "beads.db-wal")
	wal := []byte("authoritative recovery state")
	if err := os.WriteFile(walPath, wal, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Preflight(root); err == nil {
		t.Fatal("Preflight accepted an unknown backend")
	}
	got, err := os.ReadFile(walPath)
	if err != nil || !bytes.Equal(got, wal) {
		t.Fatalf("failed preflight changed WAL: equal=%v err=%v", bytes.Equal(got, wal), err)
	}
}

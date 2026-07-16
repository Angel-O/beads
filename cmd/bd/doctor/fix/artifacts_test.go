package fix

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	backendmigrationcontrol "github.com/steveyegge/beads/internal/backendmigration/control"
	"github.com/steveyegge/beads/internal/configfile"
)

func TestClassicArtifacts_NoArtifacts(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatal(err)
	}

	err := ClassicArtifacts(dir)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestClassicArtifacts_RemovesSQLiteWAL(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create WAL/SHM files
	for _, name := range []string{"beads.db-shm", "beads.db-wal"} {
		if err := os.WriteFile(filepath.Join(beadsDir, name), []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	err := ClassicArtifacts(dir)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}

	// Verify WAL/SHM files were removed
	for _, name := range []string{"beads.db-shm", "beads.db-wal"} {
		if _, err := os.Stat(filepath.Join(beadsDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s should have been removed", name)
		}
	}
}

func TestClassicArtifactsRejectsPendingBackendMigrationWithoutRemovingFiles(t *testing.T) {
	for _, markerName := range []string{
		configfile.BackendMigrationStateFileName,
		".backend-migration-test.cleanup.lock",
	} {
		t.Run(markerName, func(t *testing.T) {
			dir := t.TempDir()
			beadsDir := filepath.Join(dir, ".beads")
			if err := os.MkdirAll(beadsDir, 0o755); err != nil {
				t.Fatal(err)
			}
			markerPath := filepath.Join(beadsDir, markerName)
			marker := []byte("pending")
			if err := os.WriteFile(markerPath, marker, 0o600); err != nil {
				t.Fatal(err)
			}
			walPath := filepath.Join(beadsDir, "beads.db-wal")
			wal := []byte("recovery state")
			if err := os.WriteFile(walPath, wal, 0o600); err != nil {
				t.Fatal(err)
			}

			if err := ClassicArtifacts(dir); !errors.Is(err, configfile.ErrBackendMigrationPending) {
				t.Fatalf("ClassicArtifacts pending migration error = %v", err)
			}
			for path, want := range map[string][]byte{markerPath: marker, walPath: wal} {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != string(want) {
					t.Fatalf("refused cleanup changed %s: got=%q err=%v", filepath.Base(path), got, err)
				}
			}
		})
	}
}

func TestClassicArtifactsPreflightsAllWorkspacesBeforeRemovingAnything(t *testing.T) {
	root := t.TempDir()
	beforeBeadsDir := filepath.Join(root, "a", ".beads")
	markedBeadsDir := filepath.Join(root, "z", ".beads")
	for _, beadsDir := range []string{beforeBeadsDir, markedBeadsDir} {
		if err := os.MkdirAll(beadsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(beadsDir, "beads.db-wal"), []byte("recovery state"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(markedBeadsDir, configfile.BackendMigrationStateFileName), []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ClassicArtifacts(root); !errors.Is(err, configfile.ErrBackendMigrationPending) {
		t.Fatalf("ClassicArtifacts pending migration error = %v", err)
	}
	for _, beadsDir := range []string{beforeBeadsDir, markedBeadsDir} {
		walPath := filepath.Join(beadsDir, "beads.db-wal")
		if got, err := os.ReadFile(walPath); err != nil || string(got) != "recovery state" {
			t.Fatalf("preflight failure changed %s: got=%q err=%v", walPath, got, err)
		}
	}
}

func TestClassicArtifactsPreflightPreservesEarlierLegacyWorkspace(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, "a", ".beads")
	markedDir := filepath.Join(root, "z", "deep", ".beads")
	for _, dir := range []string{legacyDir, markedDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	legacy := []byte(`{"backend":"dolt","dolt_mode":"embedded"}`)
	legacyPath := filepath.Join(legacyDir, "config.json")
	wal := []byte("recovery state")
	walPath := filepath.Join(legacyDir, "beads.db-wal")
	if err := os.WriteFile(legacyPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(walPath, wal, 0o600); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(markedDir, configfile.BackendMigrationStateFileName)
	marker := []byte("pending")
	if err := os.WriteFile(markerPath, marker, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ClassicArtifacts(root); !errors.Is(err, configfile.ErrBackendMigrationPending) {
		t.Fatalf("ClassicArtifacts error = %v, want pending migration", err)
	}
	for path, want := range map[string][]byte{legacyPath: legacy, walPath: wal, markerPath: marker} {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("preflight changed %s: equal=%v err=%v", path, bytes.Equal(got, want), err)
		}
	}
	if _, err := os.Lstat(configfile.ConfigPath(legacyDir)); !os.IsNotExist(err) {
		t.Fatalf("preflight created metadata.json: %v", err)
	}
}

func TestClassicArtifactsAcquiresEveryWorkspaceControlBeforeDeleting(t *testing.T) {
	root := t.TempDir()
	firstDir := filepath.Join(root, "a", ".beads")
	laterDir := filepath.Join(root, "z", ".beads")
	for _, dir := range []string{firstDir, laterDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := (&configfile.Config{Backend: configfile.BackendDolt}).Save(dir); err != nil {
			t.Fatal(err)
		}
	}
	walPath := filepath.Join(firstDir, "beads.db-wal")
	wal := []byte("must remain")
	if err := os.WriteFile(walPath, wal, 0o600); err != nil {
		t.Fatal(err)
	}
	guard, err := backendmigrationcontrol.TryAcquire(laterDir)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close() //nolint:errcheck // test cleanup

	if err := ClassicArtifacts(root); !errors.Is(err, backendmigrationcontrol.ErrBusy) {
		t.Fatalf("ClassicArtifacts contention error = %v, want ErrBusy", err)
	}
	got, err := os.ReadFile(walPath)
	if err != nil || !bytes.Equal(got, wal) {
		t.Fatalf("partial cleanup changed earlier WAL: equal=%v err=%v", bytes.Equal(got, wal), err)
	}
}

func TestClassicArtifactsPreservesActiveSQLiteWorkspaceDuringRecursiveCleanup(t *testing.T) {
	root := t.TempDir()
	sqliteBeadsDir := filepath.Join(root, "a-sqlite", ".beads")
	if err := os.MkdirAll(sqliteBeadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := (&configfile.Config{Backend: configfile.BackendSQLite, SQLitePath: "beads.db"}).Save(sqliteBeadsDir); err != nil {
		t.Fatal(err)
	}
	activeFiles := map[string][]byte{
		"beads.db":     []byte("authoritative sqlite database"),
		"beads.db-wal": []byte("authoritative sqlite wal"),
		"beads.db-shm": []byte("authoritative sqlite shm"),
	}
	for name, data := range activeFiles {
		if err := os.WriteFile(filepath.Join(sqliteBeadsDir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	doltBeadsDir := filepath.Join(root, "z-dolt", ".beads")
	if err := os.MkdirAll(doltBeadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(doltBeadsDir, "beads.backup-20260204.db")
	if err := os.WriteFile(backupPath, []byte("classic artifact"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ClassicArtifacts(root); err != nil {
		t.Fatalf("ClassicArtifacts: %v", err)
	}
	for name, want := range activeFiles {
		path := filepath.Join(sqliteBeadsDir, name)
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(want) {
			t.Fatalf("recursive cleanup changed active SQLite %s: got=%q err=%v", name, got, err)
		}
	}
	if _, err := os.Lstat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("unrelated Dolt artifact was not cleaned: %v", err)
	}
}

func TestClassicArtifacts_SkipsBeadsDB(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create beads.db (should be skipped)
	if err := os.WriteFile(filepath.Join(beadsDir, "beads.db"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	err := ClassicArtifacts(dir)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}

	// beads.db should still exist (not safe to delete automatically)
	if _, err := os.Stat(filepath.Join(beadsDir, "beads.db")); os.IsNotExist(err) {
		t.Error("beads.db should NOT have been removed")
	}
}

func TestClassicArtifacts_RemovesBackupDBs(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatal(err)
	}

	backupName := "beads.backup-20260204.db"
	if err := os.WriteFile(filepath.Join(beadsDir, backupName), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	err := ClassicArtifacts(dir)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}

	if _, err := os.Stat(filepath.Join(beadsDir, backupName)); !os.IsNotExist(err) {
		t.Error("backup db should have been removed")
	}
}

func TestClassicArtifacts_CleansJSONLInDoltDir(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	doltDir := filepath.Join(beadsDir, "dolt")
	if err := os.MkdirAll(doltDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create safe-to-delete JSONL artifacts
	for _, name := range []string{"issues.jsonl.new"} {
		if err := os.WriteFile(filepath.Join(beadsDir, name), []byte(`{"id":"test"}`), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Create empty interactions.jsonl
	if err := os.WriteFile(filepath.Join(beadsDir, "interactions.jsonl"), []byte{}, 0644); err != nil {
		t.Fatal(err)
	}

	// Create issues.jsonl (should be skipped)
	if err := os.WriteFile(filepath.Join(beadsDir, "issues.jsonl"), []byte(`{"id":"real"}`), 0644); err != nil {
		t.Fatal(err)
	}

	err := ClassicArtifacts(dir)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}

	// Safe files should be removed
	for _, name := range []string{"issues.jsonl.new", "interactions.jsonl"} {
		if _, err := os.Stat(filepath.Join(beadsDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s should have been removed", name)
		}
	}

	// issues.jsonl should be kept
	if _, err := os.Stat(filepath.Join(beadsDir, "issues.jsonl")); os.IsNotExist(err) {
		t.Error("issues.jsonl should NOT have been removed")
	}
}

func TestClassicArtifacts_CleansCruftBeadsDir(t *testing.T) {
	dir := t.TempDir()
	polecatsDir := filepath.Join(dir, "polecats", "test")
	beadsDir := filepath.Join(polecatsDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Add redirect (expected)
	if err := os.WriteFile(filepath.Join(beadsDir, "redirect"), []byte("../../mayor/rig/.beads"), 0644); err != nil {
		t.Fatal(err)
	}

	// Add .gitkeep (should be preserved)
	if err := os.WriteFile(filepath.Join(beadsDir, ".gitkeep"), []byte{}, 0644); err != nil {
		t.Fatal(err)
	}

	// Add cruft
	if err := os.WriteFile(filepath.Join(beadsDir, "extra.txt"), []byte("cruft"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(beadsDir, "cruft-subdir"), 0755); err != nil {
		t.Fatal(err)
	}

	err := ClassicArtifacts(dir)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}

	// redirect should still exist
	if _, err := os.Stat(filepath.Join(beadsDir, "redirect")); os.IsNotExist(err) {
		t.Error("redirect should NOT have been removed")
	}

	// .gitkeep should still exist
	if _, err := os.Stat(filepath.Join(beadsDir, ".gitkeep")); os.IsNotExist(err) {
		t.Error(".gitkeep should NOT have been removed")
	}

	// cruft should be removed
	if _, err := os.Stat(filepath.Join(beadsDir, "extra.txt")); !os.IsNotExist(err) {
		t.Error("extra.txt should have been removed")
	}
	if _, err := os.Stat(filepath.Join(beadsDir, "cruft-subdir")); !os.IsNotExist(err) {
		t.Error("cruft-subdir should have been removed")
	}
}

func TestClassicArtifacts_CleansCruftWithoutRedirectFile(t *testing.T) {
	// Regression test: when a redirect-expected location has cruft files but
	// NO redirect file, the fix should still clean up the cruft.
	// Previously, the fix required both isRedirectExpected AND hasRedirect,
	// leaving stale files (config.yaml, metadata.json, README.md, issues.jsonl)
	// in .git/beads-worktrees/*/.beads/ directories.
	dir := t.TempDir()

	// Simulate .git/beads-worktrees/master/.beads/ (redirect-expected location)
	worktreeBeads := filepath.Join(dir, ".git", "beads-worktrees", "master", ".beads")
	if err := os.MkdirAll(worktreeBeads, 0755); err != nil {
		t.Fatal(err)
	}

	// Add typical stale files (NO redirect file present)
	cruftFiles := []string{"config.yaml", "metadata.json", "README.md", "issues.jsonl", ".gitignore", ".local_version"}
	for _, name := range cruftFiles {
		if err := os.WriteFile(filepath.Join(worktreeBeads, name), []byte("stale"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	err := ClassicArtifacts(dir)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}

	// All cruft files should be removed
	for _, name := range cruftFiles {
		if _, err := os.Stat(filepath.Join(worktreeBeads, name)); !os.IsNotExist(err) {
			t.Errorf("%s should have been removed (cruft in redirect-expected dir without redirect file)", name)
		}
	}
}

func TestIsRedirectExpectedLocation(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{"polecat worktree", "/foo/polecats/obsidian/.beads", true},
		{"crew workspace", "/foo/crew/mel/.beads", true},
		{"refinery rig", "/foo/refinery/rig/.beads", true},
		{"beads-worktrees", "/foo/.git/beads-worktrees/abc/.beads", true},
		{"regular beads dir", "/foo/.beads", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRedirectExpectedLocation(tt.path)
			if got != tt.expected {
				t.Errorf("isRedirectExpectedLocation(%q) = %v, want %v", tt.path, got, tt.expected)
			}
		})
	}
}

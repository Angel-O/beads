package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveChangeDirBeadsDirDoesNotChangeCWD(t *testing.T) {
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(origWD)
	})

	startDir := t.TempDir()
	t.Chdir(startDir)

	projectDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(projectDir); err == nil {
		projectDir = resolved
	}
	beadsDir := filepath.Join(projectDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(`{"backend":"dolt"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := resolveChangeDirBeadsDir(projectDir)
	if err != nil {
		t.Fatalf("resolveChangeDirBeadsDir: %v", err)
	}
	if got != beadsDir {
		t.Fatalf("resolveChangeDirBeadsDir() = %q, want %q", got, beadsDir)
	}

	afterWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd after resolve: %v", err)
	}
	if afterWD != startDir {
		t.Fatalf("working directory changed to %q, want %q", afterWD, startDir)
	}
}

func TestResolveChangeDirBeadsDirRejectsFile(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := resolveChangeDirBeadsDir(filePath); err == nil {
		t.Fatal("expected non-directory -C target to fail")
	}
}

func TestResolveChangeDirBeadsDirRejectsDirectoryWithoutProject(t *testing.T) {
	if _, err := resolveChangeDirBeadsDir(t.TempDir()); err == nil {
		t.Fatal("expected -C target without a beads project to fail")
	}
}

func TestApplyChangeDirSelectionOverridesAmbientDatabaseSelectors(t *testing.T) {
	projectDir := t.TempDir()
	beadsDir := filepath.Join(projectDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(`{"backend":"dolt"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	originalChangeDir := changeDir
	originalSnapshot := changeDirEnvSnapshot
	changeDir = projectDir
	changeDirEnvSnapshot = nil
	t.Cleanup(func() {
		if changeDirEnvSnapshot != nil {
			restoreChangeDirSelection()
		}
		changeDir = originalChangeDir
		changeDirEnvSnapshot = originalSnapshot
	})
	t.Setenv("BEADS_DIR", "/ambient/beads")
	t.Setenv("BEADS_DB", "/ambient/beads.db")
	t.Setenv("BD_DB", "/ambient/legacy.db")

	if err := applyChangeDirSelection(); err != nil {
		t.Fatalf("applyChangeDirSelection: %v", err)
	}
	if got := os.Getenv("BEADS_DIR"); got != beadsDir {
		t.Fatalf("BEADS_DIR = %q, want %q", got, beadsDir)
	}
	for _, key := range []string{"BEADS_DB", "BD_DB"} {
		if value, present := os.LookupEnv(key); present {
			t.Fatalf("%s remained set to %q after -C selection", key, value)
		}
	}

	restoreChangeDirSelection()
	for key, want := range map[string]string{
		"BEADS_DIR": "/ambient/beads",
		"BEADS_DB":  "/ambient/beads.db",
		"BD_DB":     "/ambient/legacy.db",
	} {
		if got := os.Getenv(key); got != want {
			t.Fatalf("restored %s = %q, want %q", key, got, want)
		}
	}
}

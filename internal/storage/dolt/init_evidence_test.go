package dolt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHasLocalInitializationEvidence(t *testing.T) {
	t.Run("empty workspace", func(t *testing.T) {
		exists, err := HasLocalInitializationEvidence(t.TempDir())
		if err != nil || exists {
			t.Fatalf("got exists=%v err=%v, want verified absence", exists, err)
		}
	})

	t.Run("legacy dolt repository", func(t *testing.T) {
		beadsDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(beadsDir, "dolt", ".dolt"), 0700); err != nil {
			t.Fatal(err)
		}
		exists, err := HasLocalInitializationEvidence(beadsDir)
		if err != nil || !exists {
			t.Fatalf("got exists=%v err=%v, want initialized", exists, err)
		}
	})

	t.Run("server dolt repository", func(t *testing.T) {
		beadsDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(beadsDir, "dolt", "source", ".dolt"), 0700); err != nil {
			t.Fatal(err)
		}
		exists, err := HasLocalInitializationEvidence(beadsDir)
		if err != nil || !exists {
			t.Fatalf("got exists=%v err=%v, want initialized", exists, err)
		}
	})

	t.Run("server dolt repository with configuration directory", func(t *testing.T) {
		beadsDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(beadsDir, "dolt", "source", ".dolt"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(beadsDir, "dolt", ".doltcfg"), 0700); err != nil {
			t.Fatal(err)
		}
		exists, err := HasLocalInitializationEvidence(beadsDir)
		if err != nil || !exists {
			t.Fatalf("got exists=%v err=%v, want initialized", exists, err)
		}
	})

	t.Run("valid server repository plus malformed sibling fails closed", func(t *testing.T) {
		beadsDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(beadsDir, "dolt", "source", ".dolt"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(beadsDir, "dolt", "malformed"), 0700); err != nil {
			t.Fatal(err)
		}
		if exists, err := HasLocalInitializationEvidence(beadsDir); err == nil || exists {
			t.Fatalf("got exists=%v err=%v, want operational error", exists, err)
		}
	})

	t.Run("embedded dolt repository", func(t *testing.T) {
		beadsDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(beadsDir, "embeddeddolt", "source", ".dolt"), 0700); err != nil {
			t.Fatal(err)
		}
		exists, err := HasLocalInitializationEvidence(beadsDir)
		if err != nil || !exists {
			t.Fatalf("got exists=%v err=%v, want initialized", exists, err)
		}
	})

	t.Run("embedded repository with persistent lock artifact", func(t *testing.T) {
		beadsDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(beadsDir, "embeddeddolt", "source", ".dolt"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(beadsDir, "embeddeddolt", ".lock"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		exists, err := HasLocalInitializationEvidence(beadsDir)
		if err != nil || !exists {
			t.Fatalf("got exists=%v err=%v, want initialized", exists, err)
		}
	})

	t.Run("valid embedded repository plus malformed sibling fails closed", func(t *testing.T) {
		beadsDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(beadsDir, "embeddeddolt", "source", ".dolt"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(beadsDir, "embeddeddolt", "malformed"), 0700); err != nil {
			t.Fatal(err)
		}
		if exists, err := HasLocalInitializationEvidence(beadsDir); err == nil || exists {
			t.Fatalf("got exists=%v err=%v, want operational error", exists, err)
		}
	})

	t.Run("invalid lock artifact has a clean error", func(t *testing.T) {
		beadsDir := t.TempDir()
		lockPath := filepath.Join(beadsDir, "dolt", ".lock")
		if err := os.MkdirAll(lockPath, 0700); err != nil {
			t.Fatal(err)
		}
		exists, err := HasLocalInitializationEvidence(beadsDir)
		if err == nil || exists {
			t.Fatalf("got exists=%v err=%v, want operational error", exists, err)
		}
		if strings.Contains(err.Error(), "%!w") {
			t.Fatalf("error contains a nil %%w formatting artifact: %q", err)
		}
	})

	t.Run("dangling embedded root symlink fails closed", func(t *testing.T) {
		beadsDir := t.TempDir()
		path := filepath.Join(beadsDir, "embeddeddolt")
		if err := os.Symlink(filepath.Join(beadsDir, "missing"), path); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if exists, err := HasLocalInitializationEvidence(beadsDir); err == nil || exists {
			t.Fatalf("got exists=%v err=%v, want operational error", exists, err)
		}
	})

	t.Run("partial legacy layout fails closed", func(t *testing.T) {
		beadsDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(beadsDir, "dolt"), 0700); err != nil {
			t.Fatal(err)
		}
		if exists, err := HasLocalInitializationEvidence(beadsDir); err == nil || exists {
			t.Fatalf("got exists=%v err=%v, want operational error", exists, err)
		}
	})

	t.Run("unreadable shape fails closed", func(t *testing.T) {
		beadsDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(beadsDir, "embeddeddolt"), []byte("not a directory"), 0600); err != nil {
			t.Fatal(err)
		}
		if exists, err := HasLocalInitializationEvidence(beadsDir); err == nil || exists {
			t.Fatalf("got exists=%v err=%v, want operational error", exists, err)
		}
	})

	t.Run("empty embedded root fails closed", func(t *testing.T) {
		beadsDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(beadsDir, "embeddeddolt"), 0700); err != nil {
			t.Fatal(err)
		}
		if exists, err := HasLocalInitializationEvidence(beadsDir); err == nil || exists {
			t.Fatalf("got exists=%v err=%v, want operational error", exists, err)
		}
	})

	t.Run("entry limit fails closed", func(t *testing.T) {
		beadsDir := t.TempDir()
		root := filepath.Join(beadsDir, "dolt")
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		for i := 0; i <= maxInitializationEvidenceEntries; i++ {
			if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("entry-%04d", i)), 0700); err != nil {
				t.Fatal(err)
			}
		}
		if exists, err := HasLocalInitializationEvidence(beadsDir); err == nil || exists {
			t.Fatalf("got exists=%v err=%v, want evidence-limit error", exists, err)
		}
	})

	t.Run("entry limit boundary is allowed", func(t *testing.T) {
		root := t.TempDir()
		for i := 0; i < maxInitializationEvidenceEntries; i++ {
			if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("entry-%04d", i)), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		entries, err := boundedReadDir(root)
		if err != nil || len(entries) != maxInitializationEvidenceEntries {
			t.Fatalf("got entries=%d err=%v, want exactly %d entries", len(entries), err, maxInitializationEvidenceEntries)
		}
	})

	t.Run("dangling workspace root fails closed", func(t *testing.T) {
		parent := t.TempDir()
		root := filepath.Join(parent, "workspace")
		if err := os.Symlink(filepath.Join(parent, "missing"), root); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if exists, err := HasLocalInitializationEvidence(root); err == nil || exists {
			t.Fatalf("got exists=%v err=%v, want operational error", exists, err)
		}
	})

	t.Run("empty workspace path is invalid", func(t *testing.T) {
		if exists, err := HasLocalInitializationEvidence(""); err == nil || exists {
			t.Fatalf("got exists=%v err=%v, want invalid-input error", exists, err)
		}
	})

	t.Run("path errors quote control characters", func(t *testing.T) {
		cause := errors.New("permission denied")
		err := safePathError("inspect Dolt path", "unsafe\n\x1b[31m", &os.PathError{
			Op:   "lstat",
			Path: "unsafe\n\x1b[31m",
			Err:  cause,
		})
		if !errors.Is(err, cause) {
			t.Fatalf("path error lost its cause: %v", err)
		}
		if strings.ContainsAny(err.Error(), "\n\x1b") {
			t.Fatalf("path error contains raw terminal-control characters: %q", err)
		}
	})
}

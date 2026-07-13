//go:build (darwin && !ios) || (linux && !android)

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDatabaseSelectorRejectsSpecialFiles(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		name := "FIFO"
		if symlink {
			name = "symlink to FIFO"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "fifo")
			if err := unix.Mkfifo(target, 0o600); err != nil {
				t.Fatal(err)
			}
			selector := target
			if symlink {
				selector = filepath.Join(root, "link")
				if err := os.Symlink(target, selector); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			if err := validateDatabaseSelectorPath(selector); err == nil {
				t.Fatal("special-file database selector was accepted")
			}
		})
	}
}

func TestAbsoluteCleanDatabasePathPropagatesGetwdFailure(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	doomed := filepath.Join(t.TempDir(), "removed-cwd")
	if err := os.Mkdir(doomed, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(doomed); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(original); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	}()
	if err := os.Remove(doomed); err != nil {
		t.Fatal(err)
	}

	got, err := absoluteCleanDatabasePath("relative.db")
	if err == nil || got != "" {
		t.Fatalf("absolute path from removed CWD = %q, err=%v, want propagated error", got, err)
	}
	if !strings.Contains(err.Error(), "absolute database path") {
		t.Fatalf("absolute-path error = %v, want operation context", err)
	}
}

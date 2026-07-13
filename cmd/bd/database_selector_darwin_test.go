//go:build darwin || ios

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDatabasePathEqualOrDescendantDarwinUsesStoredCase(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "DatabaseRoot")
	child := filepath.Join(root, "Child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	alternate := filepath.Join(parent, "databaseroot", "child")
	caseInsensitive := true
	if _, err := os.Stat(alternate); err != nil {
		caseInsensitive = false
	} else {
		got, err := databasePathEqualOrDescendant(alternate, root)
		if err != nil || !got {
			t.Fatalf("mixed-case Darwin containment = %v, err=%v, want true", got, err)
		}
	}
	got, err := databasePathEqualOrDescendant(
		filepath.Join(parent, "Future.db"),
		filepath.Join(parent, "future.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != caseInsensitive {
		t.Fatalf("missing-leaf equality = %v, want case-insensitive=%v", got, caseInsensitive)
	}
}

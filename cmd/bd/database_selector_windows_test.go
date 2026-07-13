//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatabaseSelectorResolvesWindowsJunction(t *testing.T) {
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "beads.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(t.TempDir(), "database-junction")
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", junction, target).CombinedOutput(); err != nil {
		t.Skipf("junction unavailable: %v (%s)", err, output)
	}

	selector := filepath.Join(junction, "beads.db")
	got, err := validatedDatabaseSelectorPath(selector)
	if err != nil {
		t.Fatalf("junction selector: %v", err)
	}
	want := filepath.Join(resolvedTestPath(t, target), "beads.db")
	if got != want {
		t.Fatalf("junction selector = %q, want resolved path %q", got, want)
	}
	contained, err := databasePathEqualOrDescendant(selector, target)
	if err != nil || !contained {
		t.Fatalf("junction containment = %v, err=%v, want true", contained, err)
	}

	missingSelector := filepath.Join(junction, "future.db")
	got, err = validatedDatabaseSelectorPath(missingSelector)
	if err != nil {
		t.Fatalf("missing selector under junction: %v", err)
	}
	want = filepath.Join(resolvedTestPath(t, target), "future.db")
	if got != want {
		t.Fatalf("missing selector under junction = %q, want resolved path %q", got, want)
	}
}

func TestCanonicalWindowsContainmentPreservesComponentCase(t *testing.T) {
	if canonicalDatabasePathEqualOrDescendant(`C:\DatabaseRoot\child`, `C:\databaseroot`) {
		t.Fatal("case-distinct canonical Windows path matched root")
	}
}

func TestDatabasePathEqualOrDescendantWindowsCaseSemantics(t *testing.T) {
	t.Run("case-insensitive directory normalizes stored case", func(t *testing.T) {
		parent := t.TempDir()
		root := filepath.Join(parent, "DatabaseRoot")
		child := filepath.Join(root, "Child")
		if err := os.MkdirAll(child, 0o700); err != nil {
			t.Fatal(err)
		}
		alternate := filepath.Join(parent, "databaseroot", "child")
		if _, err := os.Stat(alternate); err != nil {
			t.Skipf("test directory is not case-insensitive: %v", err)
		}
		got, err := databasePathEqualOrDescendant(alternate, root)
		if err != nil || !got {
			t.Fatalf("mixed-case containment = %v, err=%v, want true", got, err)
		}
		got, err = databasePathEqualOrDescendant(
			filepath.Join(parent, "Future.db"),
			filepath.Join(parent, "future.db"),
		)
		if err != nil || !got {
			t.Fatalf("case-insensitive missing-leaf equality = %v, err=%v, want true", got, err)
		}
	})

	t.Run("case-sensitive directory keeps distinct roots", func(t *testing.T) {
		parent := t.TempDir()
		if output, err := exec.Command("fsutil", "file", "setCaseSensitiveInfo", parent, "enable").CombinedOutput(); err != nil {
			t.Skipf("per-directory case sensitivity unavailable: %v (%s)", err, strings.TrimSpace(string(output)))
		}
		upper := filepath.Join(parent, "DatabaseRoot")
		lower := filepath.Join(parent, "databaseroot")
		if err := os.Mkdir(upper, 0o700); err != nil {
			t.Fatal(err)
		}
		child := filepath.Join(lower, "child")
		if err := os.MkdirAll(child, 0o700); err != nil {
			t.Fatal(err)
		}
		got, err := databasePathEqualOrDescendant(child, upper)
		if err != nil {
			t.Fatal(err)
		}
		if got {
			t.Fatal("case-distinct directory matched root")
		}
		got, err = databasePathEqualOrDescendant(
			filepath.Join(parent, "Future.db"),
			filepath.Join(parent, "future.db"),
		)
		if err != nil {
			t.Fatal(err)
		}
		if got {
			t.Fatal("case-sensitive missing leaves matched")
		}
	})
}

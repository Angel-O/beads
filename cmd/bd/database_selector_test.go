//go:build android || darwin || ios || linux || windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/safefile"
)

func TestDatabaseSelectorValidation(t *testing.T) {
	t.Run("regular file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "beads.db")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := validatedDatabaseSelectorPath(path)
		if err != nil {
			t.Fatalf("regular selector: %v", err)
		}
		want := resolvedTestPath(t, path)
		if got != want {
			t.Fatalf("regular selector = %q, want canonical path %q", got, want)
		}
	})

	for _, targetKind := range []string{"file", "directory"} {
		t.Run("symlink to "+targetKind, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "target")
			if targetKind == "file" {
				if err := os.WriteFile(target, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
			selector := filepath.Join(root, "selector")
			if err := os.Symlink(target, selector); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			got, err := validatedDatabaseSelectorPath(selector)
			if runtime.GOOS == "windows" {
				if err == nil {
					t.Fatal("Windows final selector reparse point was accepted")
				}
				return
			}
			if err != nil {
				t.Fatalf("valid selector symlink: %v", err)
			}
			want := resolvedTestPath(t, target)
			if got != want {
				t.Fatalf("selector symlink = %q, want resolved target %q", got, want)
			}
		})
	}

	t.Run("dangling selector symlink", func(t *testing.T) {
		root := t.TempDir()
		selector := filepath.Join(root, "selector")
		if err := os.Symlink(filepath.Join(root, "missing"), selector); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if err := validateDatabaseSelectorPath(selector); err == nil {
			t.Fatal("dangling selector symlink was accepted")
		}
	})

	t.Run("missing final component", func(t *testing.T) {
		selector := filepath.Join(t.TempDir(), "future.db")
		got, err := validatedDatabaseSelectorPath(selector)
		if err != nil {
			t.Fatalf("missing database leaf: %v", err)
		}
		want := filepath.Join(resolvedTestPath(t, filepath.Dir(selector)), filepath.Base(selector))
		if got != want {
			t.Fatalf("missing selector = %q, want canonical path %q", got, want)
		}
	})

	t.Run("missing leaf under outward symlinked parent", func(t *testing.T) {
		workspace := t.TempDir()
		outside := t.TempDir()
		alias := filepath.Join(workspace, "database-parent")
		if err := os.Symlink(outside, alias); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		got, err := validatedDatabaseSelectorPath(filepath.Join(alias, "future.db"))
		if err != nil {
			t.Fatalf("outward parent alias: %v", err)
		}
		want := filepath.Join(resolvedTestPath(t, outside), "future.db")
		if got != want {
			t.Fatalf("outward parent alias = %q, want %q", got, want)
		}
	})

	t.Run("missing leaf under inward symlinked parent", func(t *testing.T) {
		workspace := t.TempDir()
		inside := filepath.Join(workspace, "database-parent")
		if err := os.Mkdir(inside, 0o700); err != nil {
			t.Fatal(err)
		}
		alias := filepath.Join(t.TempDir(), "inside-alias")
		if err := os.Symlink(inside, alias); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		got, err := validatedDatabaseSelectorPath(filepath.Join(alias, "future.db"))
		if err != nil {
			t.Fatalf("inward parent alias: %v", err)
		}
		want := filepath.Join(resolvedTestPath(t, inside), "future.db")
		if got != want {
			t.Fatalf("inward parent alias = %q, want %q", got, want)
		}
	})

	t.Run("dangling parent", func(t *testing.T) {
		root := t.TempDir()
		parent := filepath.Join(root, "database-parent")
		if err := os.Symlink(filepath.Join(root, "missing"), parent); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		err := validateDatabaseSelectorPath(filepath.Join(parent, "dolt"))
		if err == nil || !strings.Contains(err.Error(), "database-parent") {
			t.Fatalf("dangling-parent error = %v", err)
		}
	})

	t.Run("control characters are escaped", func(t *testing.T) {
		root := t.TempDir()
		parent := filepath.Join(root, "unsafe\n\x1b[31m")
		if err := os.WriteFile(parent, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		err := validateDatabaseSelectorPath(filepath.Join(parent, "db"))
		if err == nil || strings.ContainsAny(err.Error(), "\n\x1b") {
			t.Fatalf("control-unsafe error = %q", err)
		}
	})

	t.Run("canonicalization failure", func(t *testing.T) {
		root := t.TempDir()
		first := filepath.Join(root, "first")
		second := filepath.Join(root, "second")
		if err := os.Symlink(second, first); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if err := os.Symlink(first, second); err != nil {
			t.Fatal(err)
		}
		if _, err := validatedDatabaseSelectorPath(first); err == nil {
			t.Fatal("selector symlink loop was accepted")
		}
	})
}

func TestDatabaseSelectorRejectsHardLinkedRegularFiles(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "beads.db")
	if err := os.WriteFile(databasePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	aliasPath := filepath.Join(root, "beads-alias.db")
	if err := os.Link(databasePath, aliasPath); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}

	for _, path := range []string{databasePath, aliasPath} {
		got, err := validatedDatabaseSelectorPath(path)
		if got != "" || err == nil || !strings.Contains(err.Error(), "hard links") {
			t.Fatalf("hard-linked selector %q = %q, err=%v, want rejection", path, got, err)
		}
	}

	if err := os.Remove(aliasPath); err != nil {
		t.Fatal(err)
	}
	if got, err := validatedDatabaseSelectorPath(databasePath); err != nil || got != resolvedTestPath(t, databasePath) {
		t.Fatalf("single-link selector after alias removal = %q, err=%v", got, err)
	}
}

func TestDatabaseSelectorRejectsUnsafeRegularFileLinkCountEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "beads.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	canonicalPath := resolvedTestPath(t, path)

	for _, test := range []struct {
		name      string
		count     uint64
		known     bool
		wantError string
	}{
		{name: "unknown", wantError: "unavailable"},
		{name: "zero", count: 0, known: true, wantError: "exactly one"},
		{name: "multiple", count: 2, known: true, wantError: "exactly one"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := validatedDatabaseSelectorPathWithObserver(path, func(string) (*safefile.MetadataObservation, error) {
				return &safefile.MetadataObservation{
					CanonicalPath:  canonicalPath,
					Info:           info,
					LinkCount:      test.count,
					LinkCountKnown: test.known,
				}, nil
			})
			if got != "" || err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("selector with %s link evidence = %q, err=%v, want %q rejection", test.name, got, err, test.wantError)
			}
		})
	}
}

func resolvedTestPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve expected path %q: %v", path, err)
	}
	return filepath.Clean(resolved)
}

func TestDatabaseSelectorStableValidation(t *testing.T) {
	t.Run("existing selector replaced by symlink", func(t *testing.T) {
		root := t.TempDir()
		selector := filepath.Join(root, "beads.db")
		if err := os.WriteFile(selector, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(root, "replacement.db")
		if err := os.WriteFile(target, nil, 0o600); err != nil {
			t.Fatal(err)
		}

		_, err := validatedDatabaseSelectorPathWithObserver(selector, func(path string) (*safefile.MetadataObservation, error) {
			if err := os.Remove(path); err != nil {
				return nil, err
			}
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			return safefile.ObserveMetadataNoFollow(path)
		})
		if err == nil {
			t.Fatal("replacement selector symlink was accepted")
		}
	})

	t.Run("missing selector parent replaced by file", func(t *testing.T) {
		root := t.TempDir()
		parent := filepath.Join(root, "database-parent")
		if err := os.Mkdir(parent, 0o700); err != nil {
			t.Fatal(err)
		}
		selector := filepath.Join(parent, "future.db")

		_, err := validatedDatabaseSelectorPathWithObserver(selector, func(path string) (*safefile.MetadataObservation, error) {
			if path != parent {
				return safefile.ObserveMetadataNoFollow(path)
			}
			if err := os.Remove(path); err != nil {
				return nil, err
			}
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				return nil, err
			}
			return safefile.ObserveMetadataNoFollow(path)
		})
		if err == nil {
			t.Fatal("replacement selector parent file was accepted")
		}
	})

	for _, materializedKind := range []string{"regular file", "symlink"} {
		t.Run("missing leaf materializes as "+materializedKind, func(t *testing.T) {
			parent := t.TempDir()
			selector := filepath.Join(parent, "future.db")
			target := filepath.Join(parent, "target.db")
			if err := os.WriteFile(target, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			materialized := false
			got, err := validatedDatabaseSelectorPathWithObserver(selector, func(path string) (*safefile.MetadataObservation, error) {
				observation, observeErr := safefile.ObserveMetadataNoFollow(path)
				if path != parent || materialized || observeErr != nil {
					return observation, observeErr
				}
				materialized = true
				if materializedKind == "regular file" {
					if err := os.WriteFile(selector, nil, 0o600); err != nil {
						return nil, err
					}
				} else if err := os.Symlink(target, selector); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				return observation, nil
			})
			if materializedKind == "regular file" {
				if err != nil || got != resolvedTestPath(t, selector) {
					t.Fatalf("materialized regular selector = %q, err=%v", got, err)
				}
			} else if err == nil || got != "" {
				t.Fatalf("materialized symlink selector = %q, err=%v, want rejection", got, err)
			}
		})
	}

	t.Run("observer path errors escape control characters", func(t *testing.T) {
		selector := filepath.Join(t.TempDir(), "unsafe\n\x1b[31m.db")
		if err := os.WriteFile(selector, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := validatedDatabaseSelectorPathWithObserver(selector, func(path string) (*safefile.MetadataObservation, error) {
			return nil, &os.PathError{Op: "open", Path: path, Err: errors.New("permission denied")}
		})
		if err == nil || strings.ContainsAny(err.Error(), "\n\x1b") {
			t.Fatalf("control-unsafe observer error = %q", err)
		}
	})

}

func TestDatabaseWorkspaceDirectoryValidation(t *testing.T) {
	root := t.TempDir()
	if err := validateDatabaseWorkspaceDirectory(root); err != nil {
		t.Fatalf("valid workspace: %v", err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, filepath.Join(root, "missing")} {
		if err := validateDatabaseWorkspaceDirectory(path); err == nil {
			t.Fatalf("invalid workspace %q was accepted", path)
		}
	}
}

func TestDatabasePathEqualOrDescendant(t *testing.T) {
	root := t.TempDir()
	if got, err := databasePathEqualOrDescendant(root, root); err != nil || !got {
		t.Fatal("root did not match itself")
	}
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if got, err := databasePathEqualOrDescendant(child, root); err != nil || !got {
		t.Fatal("child did not match root")
	}
	sibling := filepath.Join(filepath.Dir(root), "sibling")
	if err := os.Mkdir(sibling, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
	if got, err := databasePathEqualOrDescendant(sibling, root); err != nil || got {
		t.Fatal("sibling matched root")
	}

	t.Run("canonical aliases", func(t *testing.T) {
		target := t.TempDir()
		child := filepath.Join(target, "child")
		if err := os.Mkdir(child, 0o700); err != nil {
			t.Fatal(err)
		}
		alias := filepath.Join(t.TempDir(), "target-alias")
		if err := os.Symlink(target, alias); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		got, err := databasePathEqualOrDescendant(filepath.Join(alias, "child"), target)
		if err != nil || !got {
			t.Fatalf("aliased child containment = %v, err=%v, want true", got, err)
		}
	})

	t.Run("outward parent alias", func(t *testing.T) {
		workspace := t.TempDir()
		outside := t.TempDir()
		alias := filepath.Join(workspace, "outside-alias")
		if err := os.Symlink(outside, alias); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		got, err := databasePathEqualOrDescendant(filepath.Join(alias, "future.db"), workspace)
		if err != nil {
			t.Fatalf("outward alias containment: %v", err)
		}
		if got {
			t.Fatal("outward parent alias matched workspace")
		}
	})

	t.Run("case-distinct sibling on case-sensitive filesystem", func(t *testing.T) {
		parent := t.TempDir()
		upper := filepath.Join(parent, "DatabaseRoot")
		lower := filepath.Join(parent, "databaseroot")
		if err := os.Mkdir(upper, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(lower); err == nil {
			t.Skip("filesystem is case-insensitive")
		}
		if err := os.Mkdir(lower, 0o700); err != nil {
			t.Fatal(err)
		}
		child := filepath.Join(lower, "child")
		if err := os.Mkdir(child, 0o700); err != nil {
			t.Fatal(err)
		}
		got, err := databasePathEqualOrDescendant(child, upper)
		if err != nil {
			t.Fatalf("case-distinct containment: %v", err)
		}
		if got {
			t.Fatal("case-distinct sibling matched root")
		}
	})
}

func TestDatabasePathEqual(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.Mkdir(first, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(second, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		left  string
		right string
		want  bool
	}{
		{name: "same existing path", left: first, right: first, want: true},
		{name: "distinct existing paths", left: first, right: second},
		{name: "same missing path", left: filepath.Join(root, "future.db"), right: filepath.Join(root, "future.db"), want: true},
		{name: "distinct missing paths", left: filepath.Join(root, "future.db"), right: filepath.Join(root, "other.db")},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := databasePathEqual(test.left, test.right)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("databasePathEqual(%q, %q) = %v, want %v", test.left, test.right, got, test.want)
			}
		})
	}

	t.Run("empty paths", func(t *testing.T) {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct {
			name  string
			left  string
			right string
		}{
			{name: "left", right: cwd},
			{name: "right", left: cwd},
			{name: "both"},
		} {
			t.Run(test.name, func(t *testing.T) {
				got, err := databasePathEqual(test.left, test.right)
				if err == nil || got {
					t.Fatalf("databasePathEqual(%q, %q) = %v, err=%v, want false with error", test.left, test.right, got, err)
				}
			})
		}
	})
}

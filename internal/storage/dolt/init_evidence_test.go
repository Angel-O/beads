package dolt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/safefile"
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

	t.Run("opened directory must remain bound to its name", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "entry"), 0o700); err != nil {
			t.Fatal(err)
		}
		moved := root + ".moved"
		t.Cleanup(func() { _ = os.RemoveAll(moved) })
		var replacementErr error
		opener := func(candidate string) (*os.File, error) {
			file, err := safefile.OpenReadOnlyNoFollow(candidate)
			if err != nil {
				return nil, err
			}
			if err := os.Rename(candidate, moved); err != nil {
				_ = file.Close()
				return nil, err
			}
			if err := os.Symlink(moved, candidate); err != nil {
				replacementErr = err
				_ = file.Close()
				return nil, err
			}
			return file, nil
		}

		entries, err := boundedReadDirWithOpener(root, opener)
		if replacementErr != nil {
			t.Skipf("symlink replacement unavailable: %v", replacementErr)
		}
		if err == nil || entries != nil {
			t.Fatalf("got entries=%v err=%v, want directory replacement rejection", entries, err)
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

func TestDoltEvidenceKeepsProviderRootBoundThroughMarkerInspection(t *testing.T) {
	probeTarget := t.TempDir()
	probeLink := filepath.Join(t.TempDir(), "symlink-probe")
	if err := os.Symlink(probeTarget, probeLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.Remove(probeLink); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name          string
		providerName  string
		markerParent  string
		providerSetup string
	}{
		{
			name:          "legacy repository",
			providerName:  "dolt",
			providerSetup: ".dolt",
		},
		{
			name:          "server repository",
			providerName:  "dolt",
			markerParent:  "source",
			providerSetup: filepath.Join("source", ".dolt"),
		},
		{
			name:          "embedded repository",
			providerName:  "embeddeddolt",
			markerParent:  "source",
			providerSetup: filepath.Join("source", ".dolt"),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			beadsDir := t.TempDir()
			providerRoot := filepath.Join(beadsDir, tt.providerName)
			if err := os.MkdirAll(filepath.Join(providerRoot, tt.providerSetup), 0o700); err != nil {
				t.Fatal(err)
			}
			replacementProvider := filepath.Join(t.TempDir(), tt.providerName)
			if err := os.MkdirAll(filepath.Join(replacementProvider, tt.providerSetup), 0o700); err != nil {
				t.Fatal(err)
			}
			trigger := filepath.Join(providerRoot, tt.markerParent)
			moved := providerRoot + ".moved"
			replaced := false
			access := doltEvidenceAccess{
				openDirectory: safefile.OpenReadOnlyNoFollow,
				beforeRepositoryMarker: func(parent string) error {
					if replaced || filepath.Clean(parent) != filepath.Clean(trigger) {
						return nil
					}
					if err := os.Rename(providerRoot, moved); err != nil {
						return err
					}
					if err := os.Symlink(replacementProvider, providerRoot); err != nil {
						return err
					}
					replaced = true
					return nil
				},
			}

			exists, err := hasLocalInitializationEvidenceWithAccess(beadsDir, access)
			if !replaced {
				t.Fatal("test did not replace the provider root before marker inspection")
			}
			if err == nil || exists {
				t.Fatalf("got exists=%v err=%v, want provider-root replacement rejection", exists, err)
			}
		})
	}
}

func TestDoltEvidenceClassifiesMissingWorkspaceAncestors(t *testing.T) {
	t.Run("genuine absence is allowed", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), "missing", "workspace")
		if exists, err := HasLocalInitializationEvidence(beadsDir); err != nil || exists {
			t.Fatalf("got exists=%v err=%v, want verified absence", exists, err)
		}
	})

	t.Run("dangling symlink ancestor fails closed", func(t *testing.T) {
		root := t.TempDir()
		ancestor := filepath.Join(root, "dangling")
		if err := os.Symlink(filepath.Join(root, "missing-target"), ancestor); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		beadsDir := filepath.Join(ancestor, "workspace")
		exists, err := HasLocalInitializationEvidence(beadsDir)
		if err == nil || exists {
			t.Fatalf("got exists=%v err=%v, want dangling-ancestor rejection", exists, err)
		}
		if errors.Is(err, os.ErrNotExist) {
			t.Fatalf("dangling-ancestor error was misclassified as absence: %v", err)
		}
	})

	t.Run("non-directory ancestor fails closed", func(t *testing.T) {
		ancestor := filepath.Join(t.TempDir(), "regular-file")
		if err := os.WriteFile(ancestor, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		beadsDir := filepath.Join(ancestor, "workspace")
		if exists, err := HasLocalInitializationEvidence(beadsDir); err == nil || exists {
			t.Fatalf("got exists=%v err=%v, want non-directory-ancestor rejection", exists, err)
		}
	})

	t.Run("dangling final symlink syntax fails closed", func(t *testing.T) {
		root := t.TempDir()
		link := filepath.Join(root, "dangling")
		if err := os.Symlink(filepath.Join(root, "missing-target"), link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		for _, tt := range []struct {
			name string
			path string
		}{
			{name: "trailing separator", path: link + string(os.PathSeparator)},
			{name: "dot suffix", path: link + string(os.PathSeparator) + "."},
		} {
			t.Run(tt.name, func(t *testing.T) {
				exists, err := HasLocalInitializationEvidence(tt.path)
				if err == nil || exists {
					t.Fatalf("got exists=%v err=%v, want dangling-final-symlink rejection", exists, err)
				}
				if errors.Is(err, os.ErrNotExist) {
					t.Fatalf("dangling-final-symlink error was misclassified as absence: %v", err)
				}
			})
		}
	})

	t.Run("valid symlink ancestor is allowed", func(t *testing.T) {
		target := t.TempDir()
		ancestor := filepath.Join(t.TempDir(), "ancestor")
		if err := os.Symlink(target, ancestor); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		beadsDir := filepath.Join(ancestor, "workspace")
		if exists, err := HasLocalInitializationEvidence(beadsDir); err != nil || exists {
			t.Fatalf("got exists=%v err=%v, want verified absence through valid ancestor", exists, err)
		}
	})
}

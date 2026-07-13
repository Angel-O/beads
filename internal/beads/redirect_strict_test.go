package beads

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/safefile"
)

func TestFollowRedirectStrict(t *testing.T) {
	t.Run("no redirect", func(t *testing.T) {
		beadsDir := t.TempDir()
		got, err := FollowRedirectStrict(beadsDir)
		if err != nil || got != beadsDir {
			t.Fatalf("got path=%q err=%v, want source", got, err)
		}
	})

	t.Run("valid target", func(t *testing.T) {
		source := t.TempDir()
		target := t.TempDir()
		if err := os.WriteFile(filepath.Join(source, RedirectFileName), []byte(target+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := FollowRedirectStrict(source)
		if err != nil || got != testCanonicalPath(t, target) {
			t.Fatalf("got path=%q err=%v, want %q", got, err, target)
		}
	})

	t.Run("relative target", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "project", ".beads")
		target := filepath.Join(root, "shared", ".beads")
		if err := os.MkdirAll(source, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(target, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, RedirectFileName), []byte("../shared/.beads\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := FollowRedirectStrict(source)
		if err != nil || got != testCanonicalPath(t, target) {
			t.Fatalf("got path=%q err=%v, want %q", got, err, target)
		}
	})

	t.Run("comments only", func(t *testing.T) {
		source := t.TempDir()
		if err := os.WriteFile(filepath.Join(source, RedirectFileName), []byte("# no target\n\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := FollowRedirectStrict(source); err == nil {
			t.Fatal("targetless redirect was accepted")
		}
	})

	t.Run("multiple targets", func(t *testing.T) {
		source := t.TempDir()
		first := t.TempDir()
		second := t.TempDir()
		data := []byte(first + "\n" + second + "\n")
		if err := os.WriteFile(filepath.Join(source, RedirectFileName), data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := FollowRedirectStrict(source); err == nil {
			t.Fatal("ambiguous multi-target redirect was accepted")
		}
	})

	t.Run("missing target", func(t *testing.T) {
		source := t.TempDir()
		if err := os.WriteFile(filepath.Join(source, RedirectFileName), []byte(filepath.Join(source, "missing")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := FollowRedirectStrict(source); err == nil {
			t.Fatal("missing redirect target was accepted")
		}
	})

	t.Run("symlinked redirect", func(t *testing.T) {
		source := t.TempDir()
		target := t.TempDir()
		contents := filepath.Join(source, "redirect-contents")
		if err := os.WriteFile(contents, []byte(target+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(contents, filepath.Join(source, RedirectFileName)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := FollowRedirectStrict(source); err == nil {
			t.Fatal("symlinked redirect was accepted")
		}
	})

	t.Run("symlinked target", func(t *testing.T) {
		source := t.TempDir()
		target := t.TempDir()
		link := filepath.Join(t.TempDir(), "target-link")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if err := os.WriteFile(filepath.Join(source, RedirectFileName), []byte(link+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := FollowRedirectStrict(source); err == nil {
			t.Fatal("symlinked redirect target was accepted")
		}
	})

	t.Run("redirect chain", func(t *testing.T) {
		source := t.TempDir()
		target := t.TempDir()
		final := t.TempDir()
		if err := os.WriteFile(filepath.Join(source, RedirectFileName), []byte(target+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, RedirectFileName), []byte(final+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := FollowRedirectStrict(source); err == nil {
			t.Fatal("redirect chain was accepted")
		}
	})

	t.Run("redirect chain marker types", func(t *testing.T) {
		for _, marker := range []struct {
			name string
			make func(string) error
		}{
			{name: "directory", make: func(path string) error { return os.Mkdir(path, 0o700) }},
			{name: "dangling symlink", make: func(path string) error { return os.Symlink(path+"-missing", path) }},
		} {
			t.Run(marker.name, func(t *testing.T) {
				source := t.TempDir()
				target := t.TempDir()
				if err := os.WriteFile(filepath.Join(source, RedirectFileName), []byte(target+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := marker.make(filepath.Join(target, RedirectFileName)); err != nil {
					t.Skipf("chain marker unavailable: %v", err)
				}
				if _, err := FollowRedirectStrict(source); err == nil {
					t.Fatal("non-regular redirect chain marker was accepted")
				}
			})
		}
	})

	t.Run("non-directory target", func(t *testing.T) {
		source := t.TempDir()
		target := filepath.Join(t.TempDir(), "target")
		if err := os.WriteFile(target, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, RedirectFileName), []byte(target+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := FollowRedirectStrict(source); err == nil {
			t.Fatal("non-directory redirect target was accepted")
		}
	})

	t.Run("oversized redirect", func(t *testing.T) {
		source := t.TempDir()
		if err := os.WriteFile(filepath.Join(source, RedirectFileName), bytes.Repeat([]byte("x"), maxRedirectFileBytes+1), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := FollowRedirectStrict(source)
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("got err=%v, want bounded-read error", err)
		}
	})

	t.Run("exact size limit", func(t *testing.T) {
		source := t.TempDir()
		target := t.TempDir()
		prefix := []byte(target + "\n")
		data := append(prefix, bytes.Repeat([]byte(" "), maxRedirectFileBytes-len(prefix))...)
		if err := os.WriteFile(filepath.Join(source, RedirectFileName), data, 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := FollowRedirectStrict(source)
		if err != nil || got != testCanonicalPath(t, target) {
			t.Fatalf("exact-limit redirect got path=%q err=%v, want %q", got, err, target)
		}
	})

	t.Run("control characters in path are quoted", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "unsafe\n\x1b[31m", ".beads")
		if err := os.MkdirAll(source, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, RedirectFileName), []byte("missing\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := FollowRedirectStrict(source)
		if err == nil {
			t.Fatal("missing redirect target was accepted")
		}
		if strings.ContainsAny(err.Error(), "\n\x1b") {
			t.Fatalf("error contains raw terminal-control characters: %q", err)
		}
	})

	t.Run("lenient discovery stays silent", func(t *testing.T) {
		t.Setenv("BD_DEBUG_ROUTING", "")
		capture := func(source string) []byte {
			return captureRedirectStderr(t, func() { _ = FollowRedirect(source) })
		}

		missingSource := t.TempDir()
		if err := os.WriteFile(filepath.Join(missingSource, RedirectFileName), []byte("missing\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := capture(missingSource); len(got) != 0 {
			t.Fatalf("invalid lenient redirect wrote stderr: %q", got)
		}

		chainSource := t.TempDir()
		chainTarget := t.TempDir()
		if err := os.WriteFile(filepath.Join(chainSource, RedirectFileName), []byte(chainTarget+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(chainTarget, RedirectFileName), []byte("missing\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := capture(chainSource); len(got) != 0 {
			t.Fatalf("lenient redirect chain wrote stderr: %q", got)
		}
	})
}

func TestFollowRedirectCompatibility(t *testing.T) {
	t.Setenv("BD_DEBUG_ROUTING", "")

	t.Run("stable redirect symlink", func(t *testing.T) {
		source := t.TempDir()
		target := t.TempDir()
		contents := filepath.Join(source, "redirect-contents")
		if err := os.WriteFile(contents, []byte(target+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(contents, filepath.Join(source, RedirectFileName)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if got := FollowRedirect(source); got != testCanonicalPath(t, target) {
			t.Fatalf("FollowRedirect() = %q, want symlink contents target %q", got, target)
		}
	})

	t.Run("first target remains authoritative", func(t *testing.T) {
		source := t.TempDir()
		first := t.TempDir()
		second := t.TempDir()
		data := []byte("# comment\r\n\r\n" + first + "\r\n" + second + "\r\n")
		if err := os.WriteFile(filepath.Join(source, RedirectFileName), data, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := FollowRedirect(source); got != testCanonicalPath(t, first) {
			t.Fatalf("FollowRedirect() = %q, want first target %q", got, first)
		}
	})

	t.Run("invalid files silently fall back", func(t *testing.T) {
		for _, setup := range []struct {
			name string
			make func(string) error
		}{
			{
				name: "oversized",
				make: func(path string) error {
					return os.WriteFile(path, bytes.Repeat([]byte("x"), maxRedirectFileBytes+1), 0o600)
				},
			},
			{
				name: "directory",
				make: func(path string) error { return os.Mkdir(path, 0o700) },
			},
		} {
			t.Run(setup.name, func(t *testing.T) {
				source := t.TempDir()
				if err := setup.make(filepath.Join(source, RedirectFileName)); err != nil {
					t.Fatal(err)
				}
				var got string
				stderr := captureRedirectStderr(t, func() { got = FollowRedirect(source) })
				if got != source || len(stderr) != 0 {
					t.Fatalf("invalid redirect got path=%q stderr=%q, want silent source %q", got, stderr, source)
				}
			})
		}
	})

	t.Run("debug routing remains opt in", func(t *testing.T) {
		t.Setenv("BD_DEBUG_ROUTING", "1")
		source := filepath.Join(t.TempDir(), "source\n\x1b[31m")
		target := filepath.Join(t.TempDir(), "target\x1b[32m")
		if err := os.Mkdir(source, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, RedirectFileName), []byte(target+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		stderr := captureRedirectStderr(t, func() { _ = FollowRedirect(source) })
		if !strings.Contains(string(stderr), "[routing] Followed redirect") {
			t.Fatalf("debug routing stderr = %q, want routing trace", stderr)
		}
		if bytes.Contains(stderr, []byte{'\x1b'}) || bytes.Count(stderr, []byte{'\n'}) != 1 {
			t.Fatalf("debug routing stderr contains raw path controls: %q", stderr)
		}
	})
}

func TestFollowRedirectStrictDetectsRedirectReplacement(t *testing.T) {
	newSource := func(t *testing.T) (string, string, string) {
		t.Helper()
		source := t.TempDir()
		target := t.TempDir()
		path := filepath.Join(source, RedirectFileName)
		if err := os.WriteFile(path, []byte(target+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return source, target, path
	}

	t.Run("different inode during open", func(t *testing.T) {
		source, target, path := newSource(t)
		replacement := filepath.Join(source, "replacement")
		if err := os.WriteFile(replacement, []byte(target+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := followRedirectStrictWithOpener(source, func(string) (*os.File, error) {
			if err := os.Remove(path); err != nil {
				return nil, err
			}
			if err := os.Rename(replacement, path); err != nil {
				return nil, err
			}
			return safefile.OpenReadOnlyNoFollow(path)
		})
		if !errors.Is(err, errRedirectChanged) {
			t.Fatalf("replacement error = %v, want errRedirectChanged", err)
		}
	})

	t.Run("same inode changes during open", func(t *testing.T) {
		source, _, path := newSource(t)
		_, err := followRedirectStrictWithOpener(source, func(string) (*os.File, error) {
			file, err := safefile.OpenReadOnlyNoFollow(path)
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(path, []byte("changed-to-a-different-size\n"), 0o600); err != nil {
				_ = file.Close()
				return nil, err
			}
			return file, nil
		})
		if !errors.Is(err, errRedirectChanged) {
			t.Fatalf("same-inode change error = %v, want errRedirectChanged", err)
		}
	})

	t.Run("disappears during open", func(t *testing.T) {
		source, _, path := newSource(t)
		got, err := followRedirectStrictWithOpener(source, func(string) (*os.File, error) {
			if err := os.Remove(path); err != nil {
				return nil, err
			}
			return safefile.OpenReadOnlyNoFollow(path)
		})
		if got != "" || !errors.Is(err, errRedirectChanged) || errors.Is(err, os.ErrNotExist) {
			t.Fatalf("disappearance got path=%q err=%v, want only errRedirectChanged", got, err)
		}
	})

	t.Run("disappears after open", func(t *testing.T) {
		source, _, path := newSource(t)
		got, err := followRedirectStrictWithOpener(source, func(string) (*os.File, error) {
			file, err := safefile.OpenReadOnlyNoFollow(path)
			if err != nil {
				return nil, err
			}
			if err := os.Remove(path); err != nil {
				_ = file.Close()
				return nil, err
			}
			return file, nil
		})
		if got != "" || !errors.Is(err, errRedirectChanged) || errors.Is(err, os.ErrNotExist) {
			t.Fatalf("post-open disappearance got path=%q err=%v, want only errRedirectChanged", got, err)
		}
	})
}

func TestStrictRedirectTargetDetectsDirectoryReplacement(t *testing.T) {
	t.Run("different directory during open", func(t *testing.T) {
		parent := t.TempDir()
		target := filepath.Join(parent, "target")
		replacement := filepath.Join(parent, "replacement")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(replacement, 0o700); err != nil {
			t.Fatal(err)
		}
		err := validateStrictRedirectDirectoryWithOpener(target, func(string) (*os.File, error) {
			if err := os.Remove(target); err != nil {
				return nil, err
			}
			if err := os.Rename(replacement, target); err != nil {
				return nil, err
			}
			return safefile.OpenReadOnlyNoFollow(target)
		})
		if !errors.Is(err, errRedirectChanged) {
			t.Fatalf("directory replacement error = %v, want errRedirectChanged", err)
		}
	})

	t.Run("directory disappears during open", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "target")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		err := validateStrictRedirectDirectoryWithOpener(target, func(string) (*os.File, error) {
			if err := os.Remove(target); err != nil {
				return nil, err
			}
			return safefile.OpenReadOnlyNoFollow(target)
		})
		if !errors.Is(err, errRedirectChanged) || errors.Is(err, os.ErrNotExist) {
			t.Fatalf("directory disappearance error = %v, want only errRedirectChanged", err)
		}
	})

	t.Run("directory disappears after open", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "target")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		err := validateStrictRedirectDirectoryWithOpener(target, func(string) (*os.File, error) {
			file, err := safefile.OpenReadOnlyNoFollow(target)
			if err != nil {
				return nil, err
			}
			if err := os.Remove(target); err != nil {
				_ = file.Close()
				return nil, err
			}
			return file, nil
		})
		if !errors.Is(err, errRedirectChanged) || errors.Is(err, os.ErrNotExist) {
			t.Fatalf("post-open directory disappearance error = %v, want only errRedirectChanged", err)
		}
	})
}

func captureRedirectStderr(t *testing.T, fn func()) []byte {
	t.Helper()
	stderrPath := filepath.Join(t.TempDir(), "stderr")
	stderr, err := os.Create(stderrPath) //nolint:gosec // test-owned path
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = stderr
	defer func() { os.Stderr = old }()
	fn()
	if err := stderr.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func testCanonicalPath(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

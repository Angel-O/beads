//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris || zos

package configfile

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestReadStableConfigFileDoesNotFollowReplacementSymlinkToFIFO(t *testing.T) {
	beadsDir := t.TempDir()
	path := ConfigPath(beadsDir)
	if err := os.WriteFile(path, []byte(`{"database":"dolt","backend":"dolt"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target-fifo")
	if err := unix.Mkfifo(target, 0o600); err != nil {
		t.Fatal(err)
	}

	assertStableReadReturnsPromptly(t, path, func(path string) (*os.File, error) {
		if err := os.Remove(path); err != nil {
			return nil, err
		}
		if err := os.Symlink(target, path); err != nil {
			return nil, err
		}
		return openReadOnlyConfigFile(path)
	})
}

func TestReadStableConfigFileDoesNotBlockOnReplacementFIFO(t *testing.T) {
	beadsDir := t.TempDir()
	path := ConfigPath(beadsDir)
	if err := os.WriteFile(path, []byte(`{"database":"dolt","backend":"dolt"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	assertStableReadReturnsPromptly(t, path, func(path string) (*os.File, error) {
		if err := os.Remove(path); err != nil {
			return nil, err
		}
		if err := unix.Mkfifo(path, 0o600); err != nil {
			return nil, err
		}
		return openReadOnlyConfigFile(path)
	})
}

func assertStableReadReturnsPromptly(t *testing.T, path string, opener func(string) (*os.File, error)) {
	t.Helper()
	wantFlags := unix.O_NOFOLLOW | unix.O_NONBLOCK
	if openReadOnlyConfigFlags&wantFlags != wantFlags {
		t.Fatalf("safe-open flags = %#x, want at least %#x", openReadOnlyConfigFlags, wantFlags)
	}
	result := make(chan error, 1)
	go func() {
		_, err := readStableConfigFileWithOpener(path, opener)
		result <- err
	}()

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("replacement special file was accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stable config read blocked on a replacement special file")
	}
}

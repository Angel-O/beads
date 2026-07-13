//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris || zos

package safefile

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestOpenReadOnlyNoFollowRejectsFinalSymlink(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("target data"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	file, err := OpenReadOnlyNoFollow(link)
	if err == nil || file != nil {
		if file != nil {
			_ = file.Close()
		}
		t.Fatalf("no-follow open got file=%v err=%v, want rejection", file, err)
	}
}

func TestOpenReadOnlySpecialFileReturnsPromptly(t *testing.T) {
	wantFlags := unix.O_CLOEXEC | unix.O_NONBLOCK | unix.O_NOFOLLOW
	if readOnlyNoFollowFlags&wantFlags != wantFlags {
		t.Fatalf("safe-open flags are incomplete: got %#x want at least %#x", readOnlyNoFollowFlags, wantFlags)
	}
	path := filepath.Join(t.TempDir(), "fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	result := make(chan struct {
		file *os.File
		err  error
	}, 1)
	go func() {
		file, err := OpenReadOnlyNoFollow(path)
		result <- struct {
			file *os.File
			err  error
		}{file: file, err: err}
	}()
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("open FIFO: %v", got.err)
		}
		defer got.file.Close() //nolint:errcheck // test descriptor
		info, err := got.file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().IsRegular() {
			t.Fatal("FIFO descriptor reported as regular")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read-only open blocked on FIFO")
	}
}

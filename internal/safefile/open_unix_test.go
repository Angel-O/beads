//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris || zos

package safefile

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestOpenReadOnlyFollowsFinalSymlink(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("target data"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	file, err := OpenReadOnly(link)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer file.Close() //nolint:errcheck // test descriptor
	data, err := io.ReadAll(file)
	if err != nil || string(data) != "target data" {
		t.Fatalf("read followed target got %q err=%v", data, err)
	}
}

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
	wantBaseFlags := unix.O_CLOEXEC | unix.O_NONBLOCK
	if readOnlyOpenFlags&wantBaseFlags != wantBaseFlags || noFollowOpenFlag != unix.O_NOFOLLOW {
		t.Fatalf("safe-open flags are incomplete: base=%#x nofollow=%#x", readOnlyOpenFlags, noFollowOpenFlag)
	}
	for _, open := range []struct {
		name string
		fn   func(string) (*os.File, error)
	}{
		{name: "follow", fn: OpenReadOnly},
		{name: "no-follow", fn: OpenReadOnlyNoFollow},
	} {
		t.Run(open.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fifo")
			if err := unix.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
			result := make(chan struct {
				file *os.File
				err  error
			}, 1)
			go func() {
				file, err := open.fn(path)
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
		})
	}
}

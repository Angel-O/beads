//go:build darwin || ios

package safefile

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestOpenMetadataNoFollowUsesNonblockingEventDescriptor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(path, []byte("must not be readable"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := OpenMetadataNoFollow(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close() //nolint:errcheck // test descriptor
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := unix.O_EVTONLY | unix.O_NONBLOCK
	if flags&want != want {
		t.Fatalf("metadata descriptor flags = %#x, want %#x", flags, want)
	}
	if _, err := file.Read(make([]byte, 1)); err == nil {
		t.Fatal("event-only metadata descriptor read file data")
	}
}

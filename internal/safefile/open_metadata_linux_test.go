//go:build linux

package safefile

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestOpenMetadataNoFollowUsesPathOnlyDescriptor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
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
	if flags&unix.O_PATH == 0 {
		t.Fatalf("metadata descriptor flags = %#x, want O_PATH", flags)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("metadata descriptor mode = %v, want FIFO", info.Mode())
	}
}

func TestOpenMetadataNoFollowDescriptorCannotReadData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(path, []byte("must not be readable"), 0); err != nil {
		t.Fatal(err)
	}
	file, err := OpenMetadataNoFollow(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close() //nolint:errcheck // test descriptor
	if _, err := file.Read(make([]byte, 1)); err == nil {
		t.Fatal("O_PATH metadata descriptor read file data")
	}
}

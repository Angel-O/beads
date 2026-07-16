//go:build unix

package safefile

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestReadRegularFileRejectsHardlink(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "original.json")
	linked := filepath.Join(dir, "control.json")
	if err := os.WriteFile(original, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(original, linked); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if _, err := ReadRegularFile(linked, 1024); err == nil {
		t.Fatal("hardlinked control file unexpectedly read")
	}
}

func TestReadRegularFileRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.pipe")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Skipf("FIFOs unavailable: %v", err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := ReadRegularFile(path, 1024)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("FIFO unexpectedly read as a regular control file")
		}
	case <-time.After(time.Second):
		t.Fatal("reading a FIFO blocked instead of failing closed")
	}
}

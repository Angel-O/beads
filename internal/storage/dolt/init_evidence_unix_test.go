//go:build (darwin && !ios) || (linux && !android)

package dolt

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/safefile"
	"golang.org/x/sys/unix"
)

func TestDoltEvidenceRejectsFIFOReplacementPromptly(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "entry"), 0o700); err != nil {
		t.Fatal(err)
	}
	moved := root + ".moved"
	t.Cleanup(func() { _ = os.RemoveAll(moved) })
	opener := func(candidate string) (*os.File, error) {
		file, err := safefile.OpenReadOnlyNoFollow(candidate)
		if err != nil {
			return nil, err
		}
		if err := os.Rename(candidate, moved); err != nil {
			_ = file.Close()
			return nil, err
		}
		if err := unix.Mkfifo(candidate, 0o600); err != nil {
			_ = file.Close()
			return nil, err
		}
		return file, nil
	}

	result := make(chan error, 1)
	go func() {
		_, err := boundedReadDirWithOpener(root, opener)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("Dolt FIFO replacement was accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Dolt evidence blocked on a FIFO replacement")
	}
}

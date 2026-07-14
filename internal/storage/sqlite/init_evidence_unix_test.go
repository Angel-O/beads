//go:build (darwin && !ios) || (linux && !android)

package sqlite

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/safefile"
	"golang.org/x/sys/unix"
)

func TestSQLiteEvidenceRejectsFIFOReplacementPromptly(t *testing.T) {
	beadsDir := t.TempDir()
	path := filepath.Join(beadsDir, defaultSQLitePath)
	store, err := Provision(t.Context(), path)
	if err != nil {
		t.Fatalf("provision SQLite evidence: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close SQLite evidence: %v", err)
	}

	moved := path + ".moved"
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
		_, _, err := sqliteDatabaseAtWithOpener(path, opener)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("SQLite FIFO replacement was accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SQLite evidence blocked on a FIFO replacement")
	}
}

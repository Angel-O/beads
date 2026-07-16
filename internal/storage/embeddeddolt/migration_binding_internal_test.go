//go:build cgo && unix

package embeddeddolt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage"
)

type closeRecordingMigrationStore struct {
	storage.DoltStorage
	closed bool
}

func (s *closeRecordingMigrationStore) Close() error {
	s.closed = true
	return s.DoltStorage.Close()
}

func TestMigrationSourceBindingRejectsAndClosesSourceReplacedDuringDriverOpen(t *testing.T) {
	ctx := context.Background()
	originalBeadsDir := filepath.Join(t.TempDir(), ".beads")
	replacementBeadsDir := filepath.Join(t.TempDir(), ".beads")
	initializeMigrationBindingStore(t, ctx, originalBeadsDir)
	initializeMigrationBindingStore(t, ctx, replacementBeadsDir)

	binding, err := BindMigrationSource(originalBeadsDir, "beads")
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close() //nolint:errcheck // test cleanup
	originalDataPath := filepath.Join(originalBeadsDir, "embeddeddolt")
	originalDatabasePath := filepath.Join(originalDataPath, "beads")
	preservedDatabasePath := filepath.Join(originalDataPath, "beads-preserved")
	replacementDatabasePath := filepath.Join(replacementBeadsDir, "embeddeddolt", "beads")
	var opened *closeRecordingMigrationStore

	source, err := binding.openReadOnlyUsing(ctx, "beads", "main", func(ctx context.Context, beadsDir, database, branch string) (storage.DoltStorage, error) {
		if err := os.Rename(originalDatabasePath, preservedDatabasePath); err != nil {
			return nil, err
		}
		if err := os.Rename(replacementDatabasePath, originalDatabasePath); err != nil {
			return nil, err
		}
		replacement, err := openReadOnly(ctx, beadsDir, database, branch, true)
		if err != nil {
			return nil, err
		}
		opened = &closeRecordingMigrationStore{DoltStorage: replacement}
		return opened, nil
	})
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("mid-open source replacement error = %v", err)
	}
	if source != nil {
		t.Fatal("mid-open source replacement returned a usable source")
	}
	if opened == nil || !opened.closed {
		t.Fatal("mid-open replacement source was not closed after the post-open identity check")
	}
	for _, path := range []string{originalDataPath, originalDatabasePath, preservedDatabasePath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("mid-open replacement did not preserve %s: %v", path, err)
		}
	}
}

func initializeMigrationBindingStore(t *testing.T, ctx context.Context, beadsDir string) {
	t.Helper()
	if err := os.MkdirAll(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := &configfile.Config{
		Database: "beads.db", Backend: configfile.BackendDolt,
		DoltMode: configfile.DoltModeEmbedded, DoltDatabase: "beads",
	}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, beadsDir, "beads", "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

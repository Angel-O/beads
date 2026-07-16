//go:build cgo

package embeddeddolt_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
)

func TestRetainedMigrationSourceExcludesWriterAndStaleStoreRefusesAfterCutover(t *testing.T) {
	ctx := context.Background()
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	staleStore, err := embeddeddolt.Open(ctx, beadsDir, "testdb", "main")
	if err != nil {
		t.Fatalf("open stale store: %v", err)
	}
	t.Cleanup(func() { _ = staleStore.Close() })

	sourceConfig := &configfile.Config{
		Database:     "beads.db",
		Backend:      configfile.BackendDolt,
		DoltMode:     configfile.DoltModeEmbedded,
		DoltDatabase: "testdb",
	}
	if err := sourceConfig.Save(beadsDir); err != nil {
		t.Fatalf("save source metadata: %v", err)
	}
	binding, err := embeddeddolt.BindMigrationSource(beadsDir, "testdb")
	if err != nil {
		t.Fatalf("bind migration source: %v", err)
	}
	t.Cleanup(func() { _ = binding.Close() })
	source, err := binding.OpenReadOnly(ctx, "testdb", "main")
	if err != nil {
		t.Fatalf("retain migration source: %v", err)
	}
	sourceClosed := false
	t.Cleanup(func() {
		if !sourceClosed {
			_ = source.Close()
		}
	})

	writerResult := make(chan error, 1)
	go func() {
		writerResult <- staleStore.SetConfig(context.Background(), "migration.concurrent-write", "must-not-land")
	}()
	select {
	case err := <-writerResult:
		t.Fatalf("writer was not excluded by retained source lock: %v", err)
	case <-time.After(250 * time.Millisecond):
	}
	targetConfig := *sourceConfig
	targetConfig.Backend = configfile.BackendSQLite
	targetConfig.SQLitePath = "beads.db"
	if err := targetConfig.SaveAfterBackendReinitialization(beadsDir); err != nil {
		t.Fatalf("switch metadata authority: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("release retained source: %v", err)
	}
	sourceClosed = true

	select {
	case err := <-writerResult:
		if !errors.Is(err, embeddeddolt.ErrBackendProviderChanged) {
			t.Fatalf("stale writer error = %v, want ErrBackendProviderChanged", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stale writer did not resume after retained source closed")
	}
	if err := staleStore.Commit(ctx, "must not reach preserved Dolt history"); !errors.Is(err, embeddeddolt.ErrBackendProviderChanged) {
		t.Fatalf("stale version-control mutation error = %v, want ErrBackendProviderChanged", err)
	}

	raw, cleanup, err := embeddeddolt.OpenSQL(ctx, filepath.Join(beadsDir, "embeddeddolt"), "testdb", "main")
	if err != nil {
		t.Fatalf("inspect preserved source: %v", err)
	}
	defer cleanup() //nolint:errcheck // test cleanup
	var writes int
	if err := raw.QueryRowContext(ctx, "SELECT COUNT(*) FROM config WHERE `key` = ?", "migration.concurrent-write").Scan(&writes); err != nil {
		t.Fatalf("inspect preserved source write: %v", err)
	}
	if writes != 0 {
		t.Fatalf("stale writer mutated preserved Dolt source: %d rows", writes)
	}
}

func TestMigrationOpenReturnsBusyWhileAnotherDriverOwnsWorkspace(t *testing.T) {
	ctx := context.Background()
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	store, err := embeddeddolt.Open(ctx, beadsDir, "busydb", "main")
	if err != nil {
		t.Fatalf("initialize busy source: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := &configfile.Config{
		Database: "beads.db", Backend: configfile.BackendDolt,
		DoltMode: configfile.DoltModeEmbedded, DoltDatabase: "busydb",
	}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatal(err)
	}

	active, closeActive, err := embeddeddolt.OpenSQL(ctx, filepath.Join(beadsDir, "embeddeddolt"), "busydb", "main")
	if err != nil {
		t.Fatalf("open active source: %v", err)
	}
	defer closeActive() //nolint:errcheck // test cleanup
	if err := active.PingContext(ctx); err != nil {
		t.Fatalf("ping active source: %v", err)
	}

	binding, err := embeddeddolt.BindMigrationSource(beadsDir, "busydb")
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close() //nolint:errcheck // test cleanup
	opened, err := binding.OpenReadOnly(ctx, "busydb", "main")
	if opened != nil {
		_ = opened.Close()
		t.Fatal("migration returned a source while another driver owned the workspace")
	}
	if !errors.Is(err, embeddeddolt.ErrBackendMigrationBusy) {
		t.Fatalf("active migration-open error = %v, want ErrBackendMigrationBusy", err)
	}
}

func TestMigrationSourceBindingRejectsSymlinkAndDirectoryReplacement(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		if err := os.MkdirAll(beadsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		foreign := t.TempDir()
		if err := os.Symlink(foreign, filepath.Join(beadsDir, "embeddeddolt")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := embeddeddolt.BindMigrationSource(beadsDir, "testdb"); err == nil {
			t.Fatal("symlinked migration source unexpectedly bound")
		}
	})

	t.Run("replacement", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		dataDir := filepath.Join(beadsDir, "embeddeddolt")
		if err := os.MkdirAll(filepath.Join(dataDir, "testdb"), 0o700); err != nil {
			t.Fatal(err)
		}
		binding, err := embeddeddolt.BindMigrationSource(beadsDir, "testdb")
		if err != nil {
			t.Fatal(err)
		}
		defer binding.Close() //nolint:errcheck // test cleanup
		admitted := dataDir + ".admitted"
		if err := os.Rename(dataDir, admitted); err != nil {
			t.Skipf("directory replacement unavailable: %v", err)
		}
		if err := os.Mkdir(dataDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := binding.Verify(); err == nil {
			t.Fatal("replaced migration source unexpectedly retained authority")
		}
	})
}

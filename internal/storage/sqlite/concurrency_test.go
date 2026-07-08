package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestDSNEnablesWALAndBusyTimeout pins the multi-process concurrency pragmas in the
// DSN. Without WAL + busy_timeout, concurrent bd processes hit "database is locked"
// under the fleet's normal reader/writer interleaving (see dsn.go).
func TestDSNEnablesWALAndBusyTimeout(t *testing.T) {
	got := dsn("beads.db")
	for _, want := range []string{
		"_pragma=foreign_keys(1)",
		"_pragma=journal_mode(WAL)",
		"_pragma=busy_timeout(5000)",
		"_txlock=immediate",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dsn missing %q; got %q", want, got)
		}
	}
	// A caller supplying a full file: DSN opts out verbatim.
	passthrough := "file:custom.db?_pragma=journal_mode(DELETE)"
	if dsn(passthrough) != passthrough {
		t.Errorf("dsn must pass a file: DSN through unchanged; got %q", dsn(passthrough))
	}
}

// TestProvisionSwitchesFileToWAL proves the DSN actually put the database file into
// WAL mode (persistent on the file) — a fresh, pragma-less connection reads it back.
func TestProvisionSwitchesFileToWAL(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "wal.db")
	st, err := Provision(ctx, path)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	defer st.Close()

	raw, err := sql.Open("sqlite", "file:"+path) // no pragmas: read the file's persistent mode
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	defer raw.Close()
	var mode string
	if err := raw.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if strings.ToLower(mode) != "wal" {
		t.Fatalf("journal_mode = %q, want wal (the file was not switched to WAL)", mode)
	}
}

// TestDSNBusyTimeoutHonoredByDriver guards against a DSN-syntax typo silently leaving
// busy_timeout at 0 (instant SQLITE_BUSY). It asserts modernc actually applies the
// _pragma=busy_timeout(5000) from the DSN to every connection.
func TestDSNBusyTimeoutHonoredByDriver(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "bt.db")
	raw, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer raw.Close()
	var ms int
	if err := raw.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&ms); err != nil {
		t.Fatalf("PRAGMA busy_timeout: %v", err)
	}
	if ms != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000 (DSN pragma not honored — check syntax)", ms)
	}
}

// TestConcurrentWritersNoDatabaseLocked drives many concurrent in-process writers
// through the real store. With _txlock=immediate + the default (unbounded) pool this
// would self-collide on the write lock; SetMaxOpenConns(1) (dialect.go) serializes them
// so every write succeeds. Zero "database is locked" is the contract.
func TestConcurrentWritersNoDatabaseLocked(t *testing.T) {
	ctx := context.Background()
	st, err := Provision(ctx, filepath.Join(t.TempDir(), "conc.db"))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	defer st.Close()

	const writers, perWriter = 16, 25
	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if err := st.SetConfig(ctx, fmt.Sprintf("k.%d.%d", w, i), "v"); err != nil {
					errs <- fmt.Errorf("writer %d op %d: %w", w, i, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent write failed: %v", err)
	}
}

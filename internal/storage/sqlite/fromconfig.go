package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage"
)

// NewFromConfig opens the SQLite backend for a workspace, reading the database file
// path from .beads/metadata.json (default beads.db, relative to the beads dir). SQLite
// is file-based, so there is no DSN password to manage.
func NewFromConfig(ctx context.Context, beadsDir string) (storage.DoltStorage, error) {
	cfg, err := configfile.Load(beadsDir)
	if err != nil {
		return nil, fmt.Errorf("sqlite: load config: %w", err)
	}
	path := cfg.GetSQLitePath()
	if path == "" {
		path = "beads.db"
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(beadsDir, path)
	}
	return OpenExisting(ctx, path)
}

// OpenExisting opens an already-provisioned SQLite workspace without creating
// or mutating it. A missing configured database is a hard error after cutover.
func OpenExisting(ctx context.Context, dbPath string) (storage.DoltStorage, error) {
	if dbPath == "" {
		return nil, errors.New("sqlite: empty database path")
	}
	before, err := os.Lstat(dbPath)
	if err != nil {
		return nil, fmt.Errorf("sqlite: configured database is unavailable: %w", err)
	}
	if !regularSingleLink(before) {
		return nil, errors.New("sqlite: configured database is not a regular file")
	}
	d := filesystemDSN(dbPath, "rw", "WAL")
	store, err := New(ctx, Config{DSN: d})
	if err != nil {
		return nil, fmt.Errorf("sqlite: open existing: %w", err)
	}
	if err := store.DB().PingContext(ctx); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("sqlite: open configured database without create: %w", err)
	}
	if err := verifySchemaVersion(ctx, store.DB()); err != nil {
		_ = store.Close()
		return nil, err
	}
	after, err := os.Lstat(dbPath)
	if err != nil || !regularSingleLink(after) || !os.SameFile(before, after) {
		_ = store.Close()
		return nil, errors.New("sqlite: configured database changed while opening")
	}
	return store, nil
}

// Provision opens the SQLite database file, applies the schema (idempotent; config
// seeds on first provision), and returns the store. bd init calls this directly.
func Provision(ctx context.Context, dbPath string) (storage.DoltStorage, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("sqlite: empty database path")
	}
	d := filesystemDSN(dbPath, "rwc", "WAL")
	// DDL and seeds are native SQLite; a raw modernc connection (no translation) runs
	// them. The store's own connection goes through the translating dialect.
	raw, err := sql.Open("sqlite", d)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open (raw): %w", err)
	}
	if err := InitSchema(ctx, raw); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("sqlite: init schema: %w", err)
	}
	_ = raw.Close()
	return New(ctx, Config{DSN: d})
}

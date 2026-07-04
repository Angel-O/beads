package postgres

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/pgdialect"
)

// NewFromConfig opens the Postgres backend for a workspace. It reads the DSN and
// per-workspace schema from .beads/metadata.json (password merged from
// BEADS_PG_PASSWORD, never persisted), applies the schema over a raw
// non-translating connection, and returns the store. This is the factory arm
// cmd/bd dispatches to when metadata.json has backend="postgres".
func NewFromConfig(ctx context.Context, beadsDir string) (storage.DoltStorage, error) {
	cfg, err := configfile.Load(beadsDir)
	if err != nil {
		return nil, fmt.Errorf("postgres: load config: %w", err)
	}
	dsn := cfg.GetPostgresDSN()
	if dsn == "" {
		return nil, fmt.Errorf("postgres: no DSN (set postgres_dsn in metadata.json, or BEADS_POSTGRES_URL)")
	}
	schema := cfg.GetPostgresSchema()
	if schema == "" {
		return nil, fmt.Errorf("postgres: no schema (set postgres_schema in metadata.json)")
	}

	// DDL runs over a raw connection: the translating driver would mangle the
	// $$-quoted function bodies and treat DDL as workload SQL.
	raw, err := pgdialect.OpenRaw(dsn, schema)
	if err != nil {
		return nil, fmt.Errorf("postgres: open (raw): %w", err)
	}
	if err := InitSchema(ctx, raw, schema); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("postgres: init schema: %w", err)
	}
	_ = raw.Close()

	st, err := New(ctx, Config{DSN: dsn, Schema: schema})
	if err != nil {
		return nil, err
	}
	return st, nil
}

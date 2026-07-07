package beads

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/storage/postgres"
)

// PostgresServerConfig configures OpenServerPostgres. Unlike ServerConfig
// (discrete MySQL-shaped fields), Postgres connectivity is expressed as a
// complete pgx DSN because sslmode/sslrootcert/channel_binding are DSN-native
// and pgx owns their semantics.
type PostgresServerConfig struct {
	// DSN is a complete pgx v5 connection string, e.g.
	//   postgres://user:pass@host:5432/db?sslmode=verify-full&sslrootcert=/etc/ca/zone.pem
	// The password must already be resolved: none of the CLI credential ladder
	// (BEADS_PG_PASSWORD, BEADS_PG_PASSWORD_COMMAND, credentials file) runs here.
	// Hosted embedders inject the credential from their provider.
	DSN string

	// Schema is the per-project schema. It is provisioned if missing
	// (CREATE SCHEMA IF NOT EXISTS + the engine DDL, idempotent) and pinned as
	// search_path on every connection. In hosted deployments routing decides
	// this value; clients never do.
	Schema string
}

// OpenServerPostgres opens the beads engine against an external Postgres
// server, the Postgres sibling of OpenServer. No .beads directory,
// metadata.json, or credentials file is involved. Provisioning is implicit and
// idempotent (the CreateIfMissing analog is always on): first open creates the
// schema, applies the engine DDL, and stamps the schema version; later opens
// verify the stamp and refuse a version mismatch (the embedding server owns
// schema version).
func OpenServerPostgres(ctx context.Context, cfg PostgresServerConfig) (Storage, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("beads: OpenServerPostgres requires a DSN")
	}
	if cfg.Schema == "" {
		return nil, fmt.Errorf("beads: OpenServerPostgres requires a Schema")
	}
	return postgres.Provision(ctx, cfg.DSN, cfg.Schema)
}

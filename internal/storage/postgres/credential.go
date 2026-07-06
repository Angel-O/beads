package postgres

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/creds"
	"github.com/steveyegge/beads/internal/storage/pgdialect"
)

// resolveDSNCredential returns baseDSN with a password resolved and placed, ready to
// connect. The precedence, high to low:
//
//   - the DSN already carries a password (a full BEADS_POSTGRES_URL, or an explicit
//     --pg-url) — it wins outright, no ladder is applied;
//   - BEADS_PG_PASSWORD_COMMAND — a credential command (Vault / RDS-IAM / GCP-IAM /
//     `gasworks getToken`); run at open time, cached until near expiry;
//   - BEADS_PG_PASSWORD — a static password;
//   - nothing configured — baseDSN is returned untouched so pgx's own libpq
//     fallbacks (PGPASSWORD, ~/.pgpass, PGPASSFILE) still apply.
//
// It fails closed: if a configured credential command errors, the open aborts — it
// never silently downgrades to the static password or a passwordless connection.
func resolveDSNCredential(ctx context.Context, cfg *configfile.Config, baseDSN string) (string, error) {
	return resolveDSNWithSources(ctx, baseDSN,
		creds.CommandSource{Command: cfg.GetPostgresPasswordCommand(), Kind: creds.KindSecret, Label: "BEADS_PG_PASSWORD_COMMAND"},
		creds.EnvSource{Var: "BEADS_PG_PASSWORD"},
	)
}

// resolveDSNWithSources is the testable core of resolveDSNCredential: it applies the
// given credential ladder to baseDSN. A DSN that already carries a password wins
// outright; otherwise the first configured source supplies the password, and
// nothing-configured leaves the DSN untouched for pgx's libpq fallbacks.
func resolveDSNWithSources(ctx context.Context, baseDSN string, sources ...creds.Source) (string, error) {
	if pgdialect.HasPassword(baseDSN) {
		return baseDSN, nil
	}
	cred, ok, err := creds.ResolveLadder(ctx, sources...)
	if err != nil {
		return "", err
	}
	if !ok {
		return baseDSN, nil
	}
	// The SQL backends take a secret in the password slot; an identity credential
	// (presented as a username to a gateway) has no home on a direct connection, so
	// refuse it loudly rather than land a token where a password belongs. This guards
	// the future BEADS_PG_CREDENTIAL_COMMAND (identity) rung, which is reserved today.
	if cred.Kind != creds.KindSecret {
		return "", fmt.Errorf("postgres: credential from %s is an identity, not a password; the postgres backend connects directly and has no gateway to present it to", cred.Source)
	}
	return pgdialect.WithPassword(baseDSN, cred.Value)
}

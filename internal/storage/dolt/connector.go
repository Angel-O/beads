package dolt

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"

	mysql "github.com/go-sql-driver/mysql"

	"github.com/steveyegge/beads/internal/creds"
)

// openSQLDB opens a *sql.DB for a dolt sql-server DSN.
//
// When src is nil (the static-user, embedded, and local paths) it is exactly
// sql.Open("mysql", dsn) — byte-for-byte today's behavior.
//
// When src is set (the authenticating-gateway credential path) it returns a
// connector-backed *sql.DB whose BeforeConnect hook re-resolves a fresh cached
// credential token at EACH new physical connection dial. The gateway reads that token
// as the MySQL username, so:
//   - every new pooled connection authenticates with a live token, and
//   - EXISTING pooled connections are never re-authenticated by the server, so they
//     survive token rotation (the warm-connection property).
//
// go-sql-driver/mysql clones the Config before invoking BeforeConnect
// (connector.go, per-Connect Clone), so the per-dial User mutation is isolated to that
// dial. sql.Open("mysql", dsn) and sql.OpenDB(NewConnector(ParseDSN(dsn))) are
// equivalent — NewConnector normalizes the config, and ReadTimeout/WriteTimeout/
// TLSConfig ride mysql.Config fields through both paths.
func openSQLDB(dsn string, src creds.Source) (*sql.DB, error) {
	if src == nil {
		return sql.Open("mysql", dsn)
	}

	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing DSN for credential connector: %w", err)
	}
	if err := cfg.Apply(mysql.BeforeConnect(credentialBeforeConnect(src))); err != nil {
		return nil, fmt.Errorf("applying credential connector hook: %w", err)
	}
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating credential connector: %w", err)
	}
	return sql.OpenDB(connector), nil
}

// credentialBeforeConnect returns a mysql BeforeConnect hook that mints a fresh
// per-dial token for the literal destination and stamps it onto the connection's
// username. The dial host/port/database are read from the driver's Config — the exact
// target of this dial — so the destination reported to the helper via BEADS_EXEC_INFO
// is structurally the destination bd dials (no parallel captured field to drift). It
// fails closed: a resolution error, a non-identity credential, or a token that cannot
// be a DSN username aborts the dial rather than falling back to a stale credential.
//
// resolveCredentialToken (internal/creds) is process-cached with a pre-expiry skew, so
// the common case is a mutex-guarded map hit; only near the token expiry boundary does
// it shell out to the helper. It is a named function so tests can invoke it without a
// live server.
func credentialBeforeConnect(src creds.Source) func(context.Context, *mysql.Config) error {
	return func(ctx context.Context, c *mysql.Config) error {
		host, port := dialTarget(c)
		cred, err := creds.ResolveForDial(ctx, src, host, port, c.DBName)
		if err != nil {
			return fmt.Errorf("resolving dolt credential command: %w", err)
		}
		// Per-dial defense in depth mirroring ApplyGatewayCredential: the token is
		// presented AS the username, so a non-identity credential must never reach the
		// slot, and a ':' '@' or '/' would mis-split the DSN user field.
		if cred.Kind != creds.KindIdentity {
			return fmt.Errorf("dolt: credential from %s is not an identity; refusing to present it as the connection username", cred.Source)
		}
		if strings.ContainsAny(cred.Value, ":@/") {
			return fmt.Errorf("dolt: credential from %s contains a character (:, @, or /) that cannot be placed in the connection username", cred.Source)
		}
		c.User = cred.Value
		return nil
	}
}

// dialTarget extracts the host and port bd is about to dial from the driver Config.
// For a TCP address (the gateway path) Addr is "host:port". A unix socket has no
// network destination to gate, so it returns an empty host (and port 0): the resolve
// then injects no BEADS_EXEC_INFO and adds no host cache dimension (dialContext
// short-circuits on an empty DialHost, so the now-strict CanonicalHost is never asked
// to canonicalize a socket path), while the token is still stamped and the socket
// still dialed.
func dialTarget(c *mysql.Config) (host string, port int) {
	if c.Net == "unix" {
		return "", 0
	}
	h, p, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return c.Addr, 0
	}
	port, _ = strconv.Atoi(p)
	return h, port
}

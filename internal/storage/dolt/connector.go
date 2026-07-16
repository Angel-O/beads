package dolt

import (
	"context"
	"database/sql"
	"database/sql/driver"
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
//
// The credential path additionally wraps the connector in a redactingConnector so every
// physical dial's error is scrubbed of token material at its single source — see
// newRedactingCredentialConnector. openSQLDB is the only credential-path dial site, so
// wrapping here covers all callers (the pooled store, execWithLongTimeout*,
// openMigrationDB, the openServerConnection ping, the ignored-tx pool) at once.
func openSQLDB(dsn string, src creds.Source) (*sql.DB, error) {
	if src == nil {
		return sql.Open("mysql", dsn)
	}
	connector, err := newRedactingCredentialConnector(dsn, src)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(connector), nil
}

// newRedactingCredentialConnector builds the credential-path connector: a mysql
// connector whose BeforeConnect hook mints a fresh per-dial token, wrapped in a
// redactingConnector so a token-echoing 1045 is scrubbed at the dial before it can
// surface. Returned as a driver.Connector so a test can assert the credential path is
// wrapped for redaction.
func newRedactingCredentialConnector(dsn string, src creds.Source) (driver.Connector, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing DSN for credential connector: %w", err)
	}
	if err := cfg.Apply(mysql.BeforeConnect(credentialBeforeConnect(src))); err != nil {
		return nil, fmt.Errorf("applying credential connector hook: %w", err)
	}
	inner, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating credential connector: %w", err)
	}
	return &redactingConnector{inner: inner}, nil
}

// tokenHolder carries the exact credential token stamped on a single physical dial from
// the BeforeConnect hook back to redactingConnector.Connect. The scrub therefore keys on
// the token THIS dial presented, never a shared cache snapshot — so a concurrent
// creds.Invalidate cannot evict the needle between the failing dial and the redaction.
type tokenHolder struct{ token string }

// holderKey is the unexported context key under which redactingConnector threads a
// per-dial tokenHolder to credentialBeforeConnect.
type holderKey struct{}

// redactingConnector wraps the credential-path mysql connector so every physical dial's
// error is scrubbed of token material at its single source. A minted identity token is
// presented as the wire username, so an auth rejection (MySQL 1045) — which can only
// occur at connect time, inside the driver's Connect, never on a query over an
// already-authenticated connection — echoes the token verbatim. Scrubbing in Connect
// covers every dial site at once (the fix's coverage guarantee) with no per-call-site
// wiring, and is race-free (the token comes from the per-dial holder, not the cache).
type redactingConnector struct {
	inner driver.Connector
}

// Connect threads a fresh per-dial tokenHolder through ctx to credentialBeforeConnect,
// which records the token it stamps onto the connection's username. go-sql-driver/mysql
// invokes BeforeConnect with the exact ctx passed to Connect (verified against the
// vendored driver: connector.go calls c.cfg.beforeConnect(ctx, cfg) with the Connect
// ctx), so the hook observes this holder. On a dial error carrying the stamped token,
// Connect scrubs it; a successful dial or an error without the token passes through
// untouched, preserving error identity and errors.As classification.
func (c *redactingConnector) Connect(ctx context.Context) (driver.Conn, error) {
	holder := &tokenHolder{}
	conn, err := c.inner.Connect(context.WithValue(ctx, holderKey{}, holder))
	if err != nil {
		return nil, redactErrorToken(err, holder.token)
	}
	return conn, nil
}

// Driver delegates to the wrapped connector's driver.
func (c *redactingConnector) Driver() driver.Driver { return c.inner.Driver() }

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
		// Record the exact token stamped on THIS dial so redactingConnector can scrub it
		// out of a token-echoing 1045 without consulting (and racing) the shared
		// credential cache. Absent when the hook is invoked directly in a test.
		if h, ok := ctx.Value(holderKey{}).(*tokenHolder); ok {
			h.token = cred.Value
		}
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

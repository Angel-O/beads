package dolt

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"

	mysql "github.com/go-sql-driver/mysql"

	"github.com/steveyegge/beads/internal/creds"
)

// warmToken caches token for (command, gw.example) in the process-global credential
// cache via a real one-line shell helper, then returns a CommandSource pinned to that
// destination. The gateway would echo this exact token as the wire username in a 1045.
func warmToken(t *testing.T, token string) creds.CommandSource {
	t.Helper()
	src := creds.CommandSource{
		Command:  "printf %s '" + token + "'",
		Kind:     creds.KindIdentity,
		DialHost: "gw.example",
		DialPort: 3306,
		Database: "db",
	}
	if _, err := creds.ResolveForDial(context.Background(), src, "gw.example", 3306, "db"); err != nil {
		t.Fatalf("warming the credential cache: %v", err)
	}
	return src
}

// redactCredentialError scrubs the token out of the message while preserving the error
// chain: the fmt-wrapped result reads the sentinel, contains no token, and errors.As
// still reaches the inner 1045 so isAuthError keeps classifying it.
func TestRedactCredentialErrorPreservesClassification(t *testing.T) {
	const token = "eyJhbGciOiJSUzI1NiJ9.LIVE-token-classification-xyz.sig"
	_ = warmToken(t, token)

	inner := &mysql.MySQLError{Number: 1045, Message: "Access denied for user '" + token + "'@'10.0.0.1' (using password: YES)"}
	// Mirror openServerConnection's gateway-open wrap: redact, then fmt.Errorf(%w).
	wrapped := fmt.Errorf("failed to connect to gateway server gw.example:3306 (database %q): %w", "db", redactCredentialError(inner))

	if strings.Contains(wrapped.Error(), token) {
		t.Fatalf("wrapped error leaked the token: %q", wrapped.Error())
	}
	if !strings.Contains(wrapped.Error(), creds.RedactedTokenSentinel) {
		t.Fatalf("wrapped error missing the sentinel: %q", wrapped.Error())
	}
	var myErr *mysql.MySQLError
	if !errors.As(wrapped, &myErr) || myErr.Number != 1045 {
		t.Fatalf("errors.As must reach the 1045 MySQLError through redaction + fmt wrap, got %v", wrapped)
	}
	if !isAuthError(wrapped) {
		t.Fatal("isAuthError must still classify the scrubbed, wrapped error")
	}

	if redactCredentialError(nil) != nil {
		t.Fatal("a nil error must stay nil")
	}
	plain := errors.New("connection refused")
	if got := redactCredentialError(plain); got != plain {
		t.Fatal("an error with no cached token must be returned unchanged (identity preserved)")
	}
}

// withRetry scrubs the token before the 1045 surfaces. The op models the connector's
// per-dial BeforeConnect: it re-mints (repopulating the cache the retry invalidated)
// then returns the gateway's token-echoing 1045. The surfaced error carries the
// sentinel, never the token, and still classifies as an auth error.
func TestWithRetry1045RedactsToken(t *testing.T) {
	ctx := context.Background()
	const token = "eyJhbGciOiJSUzI1NiJ9.LIVE-EIA-do-not-leak-abc.sig"
	src := warmToken(t, token)

	s := &DoltStore{credentialSource: src}
	authErr := &mysql.MySQLError{Number: 1045, Message: "Access denied for user '" + token + "'@'10.0.0.1' (using password: YES)"}
	op := func() error {
		// The retry's fresh dial re-mints the (constant) token back into the cache.
		if _, err := creds.ResolveForDial(ctx, src, "gw.example", 3306, "db"); err != nil {
			return err
		}
		return authErr
	}

	surfaced := s.withRetry(ctx, op)
	assertRedacted(t, surfaced, token)
}

// withRetryTx scrubs the token on the write path. A driver whose BeginTx re-mints the
// token (as the connector would on a fresh dial) and returns a token-echoing 1045
// proves the surfaced write-path error is scrubbed too.
func TestWithRetryTx1045RedactsToken(t *testing.T) {
	ctx := context.Background()
	const token = "eyJhbGciOiJSUzI1NiJ9.LIVE-EIA-write-leak-def.sig"
	src := warmToken(t, token)

	drv := &authTokenDriver{src: src, token: token}
	name := fmt.Sprintf("authtoken-%s-%p", t.Name(), drv)
	sql.Register(name, drv)
	db, err := sql.Open(name, "ignored")
	if err != nil {
		t.Fatalf("sql.Open(authtoken): %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := &DoltStore{db: db, serverMode: true, credentialSource: src}

	surfaced := s.withRetryTx(ctx, func(*sql.Tx) error { return nil })
	assertRedacted(t, surfaced, token)
}

// assertRedacted checks the surfaced error scrubbed the token, kept the sentinel, and
// still classifies as a 1045 auth error through the wrapper.
func assertRedacted(t *testing.T, surfaced error, token string) {
	t.Helper()
	if surfaced == nil {
		t.Fatal("expected the 1045 to surface after the bounded retry")
	}
	if strings.Contains(surfaced.Error(), token) {
		t.Fatalf("surfaced error leaked the token: %q", surfaced.Error())
	}
	if !strings.Contains(surfaced.Error(), creds.RedactedTokenSentinel) {
		t.Fatalf("surfaced error missing the redaction sentinel: %q", surfaced.Error())
	}
	var myErr *mysql.MySQLError
	if !errors.As(surfaced, &myErr) || myErr.Number != 1045 {
		t.Fatalf("errors.As must still reach the 1045 after redaction, got %v", surfaced)
	}
	if !isAuthError(surfaced) {
		t.Fatal("isAuthError must still classify the surfaced, scrubbed error")
	}
}

// authTokenDriver fails BeginTx with a token-echoing 1045. Each BeginTx first re-mints
// the token into the credential cache (mirroring the connector's per-dial resolve on a
// fresh dial) so redaction at the surface point has the token available.
type authTokenDriver struct {
	src   creds.CommandSource
	token string
}

func (d *authTokenDriver) Open(string) (driver.Conn, error) { return &authTokenConn{drv: d}, nil }

type authTokenConn struct{ drv *authTokenDriver }

func (c *authTokenConn) Prepare(string) (driver.Stmt, error) {
	return nil, c.drv.err()
}
func (c *authTokenConn) Close() error { return nil }
func (c *authTokenConn) Begin() (driver.Tx, error) {
	// Re-mint the constant token back into the cache, as a real fresh dial would.
	_, _ = creds.ResolveForDial(context.Background(), c.drv.src, "gw.example", 3306, "db")
	return nil, c.drv.err()
}
func (d *authTokenDriver) err() error {
	return &mysql.MySQLError{Number: 1045, Message: "Access denied for user '" + d.token + "'@'10.0.0.1'"}
}

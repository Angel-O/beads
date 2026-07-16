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

// redactErrorToken scrubs the exact per-dial token out of the message while preserving
// the error chain: the fmt-wrapped result reads the sentinel, contains no token, and
// errors.As still reaches the inner 1045 so isAuthError keeps classifying it. A nil error,
// an empty token, and a token absent from the message all preserve identity.
func TestRedactErrorTokenPreservesClassification(t *testing.T) {
	const token = "eyJhbGciOiJSUzI1NiJ9.LIVE-token-classification-xyz.sig"

	inner := &mysql.MySQLError{Number: 1045, Message: "Access denied for user '" + token + "'@'10.0.0.1' (using password: YES)"}
	// Mirror openServerConnection's gateway-open wrap: the connector-scrubbed error is
	// then fmt.Errorf(%w)-wrapped by the caller.
	wrapped := fmt.Errorf("failed to connect to gateway server gw.example:3306 (database %q): %w", "db", redactErrorToken(inner, token))

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

	if redactErrorToken(nil, token) != nil {
		t.Fatal("a nil error must stay nil")
	}
	plain := errors.New("connection refused")
	if got := redactErrorToken(plain, token); got != plain {
		t.Fatal("an error without the token must be returned unchanged (identity preserved)")
	}
	if got := redactErrorToken(inner, ""); got != inner {
		t.Fatal("an empty token must return the error unchanged (no needle)")
	}
}

// withRetry surfaces the connector-scrubbed 1045 without reintroducing token material and
// while preserving classification through its bounded one-shot invalidate+retry. The op
// returns what the redacting connector yields — an already-scrubbed 1045 — and the
// surfaced error still reads the sentinel, never the token, and still classifies.
func TestWithRetryPreservesConnectorRedaction(t *testing.T) {
	ctx := context.Background()
	const token = "eyJhbGciOiJSUzI1NiJ9.LIVE-EIA-do-not-leak-abc.sig"
	src := creds.CommandSource{Command: "printf tok", Kind: creds.KindIdentity}
	s := &DoltStore{credentialSource: src}

	scrubbed := redactErrorToken(&mysql.MySQLError{Number: 1045, Message: "Access denied for user '" + token + "'@'10.0.0.1' (using password: YES)"}, token)
	surfaced := s.withRetry(ctx, func() error { return scrubbed })
	assertRedacted(t, surfaced, token)
}

// withRetryTx surfaces the connector-scrubbed 1045 on the write path. A driver whose
// BeginTx returns the already-scrubbed error (as the redacting connector would on a fresh
// dial) proves the write-path retry keeps the scrub and the 1045 classification.
func TestWithRetryTxPreservesConnectorRedaction(t *testing.T) {
	ctx := context.Background()
	const token = "eyJhbGciOiJSUzI1NiJ9.LIVE-EIA-write-leak-def.sig"

	drv := &authTokenDriver{token: token}
	name := fmt.Sprintf("authtoken-%s-%p", t.Name(), drv)
	sql.Register(name, drv)
	db, err := sql.Open(name, "ignored")
	if err != nil {
		t.Fatalf("sql.Open(authtoken): %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	src := creds.CommandSource{Command: "printf tok", Kind: creds.KindIdentity}
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

// authTokenDriver fails BeginTx with the connector-scrubbed token-echoing 1045 — the
// exact error shape the redacting connector produces on a fresh write-path dial.
type authTokenDriver struct{ token string }

func (d *authTokenDriver) Open(string) (driver.Conn, error) { return &authTokenConn{drv: d}, nil }

type authTokenConn struct{ drv *authTokenDriver }

func (c *authTokenConn) Prepare(string) (driver.Stmt, error) { return nil, c.drv.err() }
func (c *authTokenConn) Close() error                        { return nil }
func (c *authTokenConn) Begin() (driver.Tx, error)           { return nil, c.drv.err() }
func (d *authTokenDriver) err() error {
	raw := &mysql.MySQLError{Number: 1045, Message: "Access denied for user '" + d.token + "'@'10.0.0.1'"}
	return redactErrorToken(raw, d.token)
}

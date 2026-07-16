package dolt

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	mysql "github.com/go-sql-driver/mysql"

	"github.com/steveyegge/beads/internal/creds"
)

func mysql1045() error {
	return &mysql.MySQLError{Number: 1045, Message: "Access denied for user 'x'"}
}

func TestIsAuthError(t *testing.T) {
	if !isAuthError(mysql1045()) {
		t.Fatal("a 1045 MySQLError must be an auth error")
	}
	if !isAuthError(fmt.Errorf("wrapped: %w", mysql1045())) {
		t.Fatal("a wrapped 1045 must be an auth error")
	}
	if !isAuthError(fmt.Errorf("Error 1045: Access denied for user")) {
		t.Fatal("the access-denied substring fallback must match")
	}
	if isAuthError(fmt.Errorf("connection refused")) {
		t.Fatal("a connection error is not an auth error")
	}
	// A 1045 must never be classified as a connection error (it must not trip the
	// circuit breaker).
	if isConnectionError(mysql1045()) {
		t.Fatal("a 1045 must not be a connection error")
	}
}

// withRetry: a 1045 is gated on a credential source and bounded to exactly one
// invalidate+retry; a static store fails fast.
func TestWithRetry1045BoundedAndGated(t *testing.T) {
	ctx := context.Background()

	withSrc := &DoltStore{credentialSource: creds.CommandSource{
		Command: "gettoken", Kind: creds.KindIdentity, DialHost: "gw.example",
	}}
	calls := 0
	if err := withSrc.withRetry(ctx, func() error { calls++; return mysql1045() }); err == nil {
		t.Fatal("expected the 1045 to fail after the bounded retry")
	}
	if calls != 2 {
		t.Fatalf("credential path: op called %d times, want 2 (one invalidate+retry)", calls)
	}

	static := &DoltStore{}
	calls = 0
	if err := static.withRetry(ctx, func() error { calls++; return mysql1045() }); err == nil {
		t.Fatal("expected the 1045 to fail")
	}
	if calls != 1 {
		t.Fatalf("static path: op called %d times, want 1 (fail-fast, no invalidate)", calls)
	}
}

// withRetry actually drops the cached token for the store's destination on a 1045, so
// the next dial re-mints.
func TestWithRetry1045InvalidatesCache(t *testing.T) {
	ctx := context.Background()
	counter := filepath.Join(t.TempDir(), "runs")
	cmd := "printf x >> " + counter + "; printf tok"
	src := creds.CommandSource{Command: cmd, Kind: creds.KindIdentity, DialHost: "gw.example", DialPort: 3306, Database: "db"}
	runCount := func() int { b, _ := os.ReadFile(counter); return len(b) }

	if _, err := creds.ResolveForDial(ctx, src, "gw.example", 3306, "db"); err != nil {
		t.Fatalf("warm 1: %v", err)
	}
	if _, err := creds.ResolveForDial(ctx, src, "gw.example", 3306, "db"); err != nil {
		t.Fatalf("warm 2: %v", err)
	}
	if runCount() != 1 {
		t.Fatalf("expected the token cached after warm-up, runs=%d", runCount())
	}

	s := &DoltStore{credentialSource: src}
	_ = s.withRetry(ctx, func() error { return mysql1045() })

	if _, err := creds.ResolveForDial(ctx, src, "gw.example", 3306, "db"); err != nil {
		t.Fatalf("post-invalidate resolve: %v", err)
	}
	if runCount() != 2 {
		t.Fatalf("withRetry did not invalidate the cached token, runs=%d (want 2)", runCount())
	}
}

// --- write path: a driver that fails BeginTx with 1045 ---

type auth1045Driver struct{ begins atomic.Int64 }

func (d *auth1045Driver) Open(string) (driver.Conn, error) { return &auth1045Conn{drv: d}, nil }

type auth1045Conn struct{ drv *auth1045Driver }

func (c *auth1045Conn) Prepare(string) (driver.Stmt, error) { return nil, mysql1045() }
func (c *auth1045Conn) Close() error                        { return nil }
func (c *auth1045Conn) Begin() (driver.Tx, error) {
	c.drv.begins.Add(1)
	return nil, mysql1045()
}

func openAuth1045Store(t *testing.T, src creds.Source) (*DoltStore, *auth1045Driver) {
	t.Helper()
	drv := &auth1045Driver{}
	name := fmt.Sprintf("auth1045-%s-%p", t.Name(), drv)
	sql.Register(name, drv)
	db, err := sql.Open(name, "ignored")
	if err != nil {
		t.Fatalf("sql.Open(auth1045): %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &DoltStore{db: db, serverMode: true, credentialSource: src}, drv
}

// withRetryTx: the same bounded one-shot invalidate+retry on the write path, gated on a
// credential source.
func TestWithRetryTx1045BoundedAndGated(t *testing.T) {
	ctx := context.Background()

	s, drv := openAuth1045Store(t, creds.CommandSource{
		Command: "gettoken", Kind: creds.KindIdentity, DialHost: "gw.example",
	})
	if err := s.withRetryTx(ctx, func(*sql.Tx) error { return nil }); err == nil {
		t.Fatal("expected the 1045 to surface after the bounded retry")
	}
	if got := drv.begins.Load(); got != 2 {
		t.Fatalf("credential write path: BeginTx attempts = %d, want 2", got)
	}

	sStatic, drvStatic := openAuth1045Store(t, nil)
	if err := sStatic.withRetryTx(ctx, func(*sql.Tx) error { return nil }); err == nil {
		t.Fatal("expected the 1045 to fail")
	}
	if got := drvStatic.begins.Load(); got != 1 {
		t.Fatalf("static write path: BeginTx attempts = %d, want 1 (fail-fast)", got)
	}
}

package dolt

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"
	"testing"

	mysql "github.com/go-sql-driver/mysql"

	"github.com/steveyegge/beads/internal/creds"
)

// identitySource returns a KindIdentity command source whose helper prints the given
// bare token. The command string embeds the token so each token gets its own cache key
// (no collision with another test's process-global credential cache).
func identitySource(token string) creds.CommandSource {
	return creds.CommandSource{Command: "printf %s '" + token + "'", Kind: creds.KindIdentity}
}

// fakeConnector is a driver.Connector whose Connect delegates to onConnect. It models the
// real mysql connector for redactingConnector tests without a live server.
type fakeConnector struct {
	onConnect func(ctx context.Context) (driver.Conn, error)
	drv       driver.Driver
}

func (f *fakeConnector) Connect(ctx context.Context) (driver.Conn, error) { return f.onConnect(ctx) }
func (f *fakeConnector) Driver() driver.Driver                            { return f.drv }

// tokenEchoConnector returns a fakeConnector that runs the REAL credentialBeforeConnect
// hook for src against a gateway dial — so the per-dial token is minted and stamped into
// the ctx holder exactly as the driver does — optionally runs mid between the stamp and
// the error (to model a concurrent cache eviction), then returns the token-echoing 1045
// the gateway sends when it rejects the presented username.
func tokenEchoConnector(src creds.Source, mid func()) *fakeConnector {
	return &fakeConnector{onConnect: func(ctx context.Context) (driver.Conn, error) {
		cfg := &mysql.Config{Net: "tcp", Addr: "gw.example:3306", DBName: "db"}
		if err := credentialBeforeConnect(src)(ctx, cfg); err != nil {
			return nil, err
		}
		if mid != nil {
			mid()
		}
		// The gateway reads the stamped username (the minted token) and, on rejection,
		// echoes it verbatim in the 1045 — the exact leak this connector must scrub.
		return nil, &mysql.MySQLError{
			Number:  1045,
			Message: "Access denied for user '" + cfg.User + "'@'10.0.0.1' (using password: YES)",
		}
	}}
}

// assertConnectorRedacted checks the surfaced dial error scrubbed the token, kept the
// sentinel, and still classifies as a 1045 auth error through the wrapper.
func assertConnectorRedacted(t *testing.T, err error, token string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected the gateway 1045 to surface")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("connector leaked the per-dial token: %q", err.Error())
	}
	if !strings.Contains(err.Error(), creds.RedactedTokenSentinel) {
		t.Fatalf("connector did not insert the sentinel: %q", err.Error())
	}
	var myErr *mysql.MySQLError
	if !errors.As(err, &myErr) || myErr.Number != 1045 {
		t.Fatalf("errors.As must still reach the 1045 through redaction: %v", err)
	}
	if !isAuthError(err) {
		t.Fatal("isAuthError must still classify the scrubbed dial error")
	}
}

// The wrapping connector scrubs the token-echoing 1045 at the dial SOURCE — with no
// DoltStore, no withRetry, and none of the former hand-wired store.go sites in the path.
// This is the coverage guarantee: because every credential-path dial funnels through this
// connector, a 1045 from any caller (GetStateHash, execWithLongTimeout, openMigrationDB,
// the pooled store) is scrubbed here, not at three specific call sites.
func TestRedactingConnectorScrubsPerDialTokenAtSource(t *testing.T) {
	const token = "eyJhbGciOiJSUzI1NiJ9.LIVE-connector-source-scrub-xyz.sig"
	rc := &redactingConnector{inner: tokenEchoConnector(identitySource(token), nil)}

	_, err := rc.Connect(context.Background())
	assertConnectorRedacted(t, err, token)
}

// The exact-per-dial-token approach is immune to the cache-eviction race that broke the
// cache-scan redactor: here the cache entry is dropped AFTER the hook stamps the token
// but BEFORE the 1045 is redacted. RedactKnownTokens would find nothing and leak; the
// per-dial holder still carries the exact token, so the scrub succeeds.
func TestRedactingConnectorImmuneToCacheEviction(t *testing.T) {
	const token = "eyJhbGciOiJSUzI1NiJ9.LIVE-evicted-before-redact-abc.sig"
	src := identitySource(token)
	// Invalidate keys on (command, canonical DialHost); align DialHost with the dial so
	// the eviction actually drops the entry the hook just warmed.
	evict := creds.CommandSource{Command: src.Command, Kind: creds.KindIdentity, DialHost: "gw.example"}
	rc := &redactingConnector{inner: tokenEchoConnector(src, func() { creds.Invalidate(evict) })}

	_, err := rc.Connect(context.Background())
	assertConnectorRedacted(t, err, token)
}

// Two concurrent dials, each evicting the shared cache mid-dial while the other's 1045
// surfaces. Each dial's holder carries its own token, so both surfaced errors are scrubbed
// with no dependence on cache state and no data race. Run under -race.
func TestRedactingConnectorConcurrentDialsRaceSafe(t *testing.T) {
	tokens := []string{
		"eyJhbGciOiJSUzI1NiJ9.LIVE-race-token-A-111.sig",
		"eyJhbGciOiJSUzI1NiJ9.LIVE-race-token-B-222.sig",
	}
	var wg sync.WaitGroup
	for _, token := range tokens {
		wg.Add(1)
		go func(token string) {
			defer wg.Done()
			src := identitySource(token)
			evict := creds.CommandSource{Command: src.Command, Kind: creds.KindIdentity, DialHost: "gw.example"}
			rc := &redactingConnector{inner: tokenEchoConnector(src, func() { creds.Invalidate(evict) })}
			_, err := rc.Connect(context.Background())
			if err == nil || strings.Contains(err.Error(), token) {
				t.Errorf("token leaked under concurrency: %v", err)
				return
			}
			if !strings.Contains(err.Error(), creds.RedactedTokenSentinel) {
				t.Errorf("missing sentinel under concurrency: %q", err.Error())
			}
		}(token)
	}
	wg.Wait()
}

// A successful dial and a dial error that carries no token pass through untouched: error
// identity is preserved so nothing but token material is ever rewritten.
func TestRedactingConnectorPassthrough(t *testing.T) {
	// No holder token recorded (hook never ran) → the error is returned unchanged.
	sentinelErr := errors.New("connection refused")
	rc := &redactingConnector{inner: &fakeConnector{onConnect: func(context.Context) (driver.Conn, error) {
		return nil, sentinelErr
	}}}
	if _, err := rc.Connect(context.Background()); err != sentinelErr {
		t.Fatalf("a non-token dial error must pass through unchanged, got %v", err)
	}

	okConn := &fakeConnector{onConnect: func(context.Context) (driver.Conn, error) { return nil, nil }}
	rc = &redactingConnector{inner: okConn}
	if _, err := rc.Connect(context.Background()); err != nil {
		t.Fatalf("a successful dial must not error, got %v", err)
	}
}

// Driver() delegates to the wrapped connector's driver.
func TestRedactingConnectorDriverDelegates(t *testing.T) {
	want := mysql.MySQLDriver{}
	rc := &redactingConnector{inner: &fakeConnector{drv: want}}
	if got := rc.Driver(); got != want {
		t.Fatalf("Driver() must delegate to the inner connector, got %T", got)
	}
}

// The credential path (src != nil) is wrapped in a redactingConnector, so every dial
// openSQLDB opens on that path is scrubbed at the source. openSQLDB is the only
// credential-path dial site, so this is the trace proof that all callers are covered.
func TestOpenSQLDBWrapsCredentialConnectorForRedaction(t *testing.T) {
	src := creds.CommandSource{Command: "printf tok", Kind: creds.KindIdentity}
	c, err := newRedactingCredentialConnector("token-per-dial@tcp(gw.example:3306)/db?tls=false", src)
	if err != nil {
		t.Fatalf("build credential connector: %v", err)
	}
	if _, ok := c.(*redactingConnector); !ok {
		t.Fatalf("credential path is not wrapped for redaction: %T", c)
	}
}

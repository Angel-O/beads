package mysql

import (
	"context"
	"strings"
	"testing"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/creds"
)

const baseDSN = "bts@tcp(127.0.0.1:55441)/"

// password parses out and returns the password go-sql-driver sees.
func password(t *testing.T, dsn string) string {
	t.Helper()
	cfg, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("gomysql.ParseDSN(%q): %v", dsn, err)
	}
	return cfg.Passwd
}

func TestResolveDSNCredentialCommand(t *testing.T) {
	t.Setenv("BEADS_MYSQL_PASSWORD_COMMAND", "printf cmd-pw")
	t.Setenv("BEADS_MYSQL_PASSWORD", "")
	got, err := resolveDSNCredential(context.Background(), &configfile.Config{}, baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	if pw := password(t, got); pw != "cmd-pw" {
		t.Fatalf("password = %q, want cmd-pw", pw)
	}
}

func TestResolveDSNCredentialEnv(t *testing.T) {
	t.Setenv("BEADS_MYSQL_PASSWORD_COMMAND", "")
	t.Setenv("BEADS_MYSQL_PASSWORD", "env-pw")
	got, err := resolveDSNCredential(context.Background(), &configfile.Config{}, baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	if pw := password(t, got); pw != "env-pw" {
		t.Fatalf("password = %q, want env-pw", pw)
	}
}

// The command out-ranks the static env password.
func TestResolveDSNCredentialCommandBeatsEnv(t *testing.T) {
	t.Setenv("BEADS_MYSQL_PASSWORD_COMMAND", "printf cmd-wins")
	t.Setenv("BEADS_MYSQL_PASSWORD", "env-loses")
	got, err := resolveDSNCredential(context.Background(), &configfile.Config{}, baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	if pw := password(t, got); pw != "cmd-wins" {
		t.Fatalf("password = %q, want cmd-wins", pw)
	}
}

// A configured-but-erroring command aborts the open and does NOT fall through to the
// env password. Here a bare token with whitespace is rejected by parseCredential — a
// configured error, not a command exit failure.
func TestResolveDSNCredentialFailsClosed(t *testing.T) {
	t.Setenv("BEADS_MYSQL_PASSWORD_COMMAND", `printf 'access denied'`)
	t.Setenv("BEADS_MYSQL_PASSWORD", "env-would-be-wrong")
	_, err := resolveDSNCredential(context.Background(), &configfile.Config{}, baseDSN)
	if err == nil {
		t.Fatal("expected an error (fail-closed); a broken command must not fall through to BEADS_MYSQL_PASSWORD")
	}
}

// A command that exits non-zero also aborts (the other configured-error shape).
func TestResolveDSNCredentialFailsClosedOnExit(t *testing.T) {
	t.Setenv("BEADS_MYSQL_PASSWORD_COMMAND", "false")
	t.Setenv("BEADS_MYSQL_PASSWORD", "env-would-be-wrong")
	_, err := resolveDSNCredential(context.Background(), &configfile.Config{}, baseDSN)
	if err == nil {
		t.Fatal("expected an error when the command exits non-zero")
	}
}

// A getToken/ExecCredential JSON envelope resolves the password end-to-end.
func TestResolveDSNCredentialJSONEnvelope(t *testing.T) {
	t.Setenv("BEADS_MYSQL_PASSWORD_COMMAND", `printf '{"access_token":"tok-pw","expires_in":90}'`)
	t.Setenv("BEADS_MYSQL_PASSWORD", "")
	got, err := resolveDSNCredential(context.Background(), &configfile.Config{}, baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	if pw := password(t, got); pw != "tok-pw" {
		t.Fatalf("password = %q, want tok-pw", pw)
	}
}

// End-to-end: resolving a password into a userless base DSN must fail loudly (the
// grammar would silently drop it), not connect passwordless.
func TestResolveDSNCredentialUserlessDSNFailsLoudly(t *testing.T) {
	t.Setenv("BEADS_MYSQL_PASSWORD_COMMAND", "")
	t.Setenv("BEADS_MYSQL_PASSWORD", "some-pw")
	_, err := resolveDSNCredential(context.Background(), &configfile.Config{}, "tcp(127.0.0.1:55441)/")
	if err == nil {
		t.Fatal("expected an error placing a password into a userless DSN")
	}
}

// withDatabase refuses a userless password-bearing DSN (the path that skips the
// ladder via an inline ":secret@tcp(host)/" URL) rather than silently dropping it.
func TestWithDatabaseRefusesUserlessPassword(t *testing.T) {
	if _, err := withDatabase(":secret@tcp(127.0.0.1:55441)/", "db"); err == nil {
		t.Fatal("expected withDatabase to refuse a userless password-bearing DSN")
	}
	// A normal user:pass DSN still works.
	if _, err := withDatabase("bts:bts@tcp(127.0.0.1:55441)/", "db"); err != nil {
		t.Fatalf("withDatabase rejected a valid DSN: %v", err)
	}
}

func TestResolveDSNCredentialNothingConfigured(t *testing.T) {
	t.Setenv("BEADS_MYSQL_PASSWORD_COMMAND", "")
	t.Setenv("BEADS_MYSQL_PASSWORD", "")
	got, err := resolveDSNCredential(context.Background(), &configfile.Config{}, baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	if got != baseDSN {
		t.Fatalf("dsn = %q, want it unchanged (%q)", got, baseDSN)
	}
}

func TestResolveDSNCredentialExistingPasswordWins(t *testing.T) {
	t.Setenv("BEADS_MYSQL_PASSWORD_COMMAND", "printf should-not-run")
	t.Setenv("BEADS_MYSQL_PASSWORD", "should-not-run")
	withPw := "bts:already-here@tcp(127.0.0.1:55441)/"
	got, err := resolveDSNCredential(context.Background(), &configfile.Config{}, withPw)
	if err != nil {
		t.Fatal(err)
	}
	if pw := password(t, got); pw != "already-here" {
		t.Fatalf("password = %q, want already-here (existing password must win)", pw)
	}
}

// staticSource is a test Source with a fixed credential, used to exercise the
// identity-refusal guard (no env-driven rung yields KindIdentity today).
type staticSource struct{ cred creds.Credential }

func (s staticSource) Name() string { return "test" }
func (s staticSource) Resolve(context.Context) (creds.Credential, bool, error) {
	return s.cred, true, nil
}

func TestResolveDSNCredentialRefusesIdentity(t *testing.T) {
	src := staticSource{cred: creds.Credential{Value: "an-eia-token", Kind: creds.KindIdentity, Source: "test"}}
	_, err := resolveDSNWithSources(context.Background(), baseDSN, src)
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("expected an identity-refusal error, got %v", err)
	}
}

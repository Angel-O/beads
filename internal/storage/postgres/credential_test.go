package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/creds"
)

const baseDSN = "postgres://bts@127.0.0.1:5432/db"

// password parses out and returns the password pgx sees, failing the test on a parse error.
func password(t *testing.T, dsn string) string {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("pgx.ParseConfig(%q): %v", dsn, err)
	}
	return cfg.Password
}

func TestResolveDSNCredentialCommand(t *testing.T) {
	t.Setenv("BEADS_PG_PASSWORD_COMMAND", "printf cmd-pw")
	t.Setenv("BEADS_PG_PASSWORD", "")
	got, err := resolveDSNCredential(context.Background(), &configfile.Config{}, baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	if pw := password(t, got); pw != "cmd-pw" {
		t.Fatalf("password = %q, want cmd-pw", pw)
	}
}

func TestResolveDSNCredentialEnv(t *testing.T) {
	t.Setenv("BEADS_PG_PASSWORD_COMMAND", "")
	t.Setenv("BEADS_PG_PASSWORD", "env-pw")
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
	t.Setenv("BEADS_PG_PASSWORD_COMMAND", "printf cmd-wins")
	t.Setenv("BEADS_PG_PASSWORD", "env-loses")
	got, err := resolveDSNCredential(context.Background(), &configfile.Config{}, baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	if pw := password(t, got); pw != "cmd-wins" {
		t.Fatalf("password = %q, want cmd-wins", pw)
	}
}

// A failing command aborts the open and does NOT fall through to the env password.
func TestResolveDSNCredentialFailsClosed(t *testing.T) {
	// A bare token with whitespace is rejected by parseCredential — a configured error.
	t.Setenv("BEADS_PG_PASSWORD_COMMAND", `printf 'access denied'`)
	t.Setenv("BEADS_PG_PASSWORD", "env-would-be-wrong")
	_, err := resolveDSNCredential(context.Background(), &configfile.Config{}, baseDSN)
	if err == nil {
		t.Fatal("expected an error (fail-closed); a broken command must not fall through to BEADS_PG_PASSWORD")
	}
}

// Nothing configured: the base DSN comes back untouched (pgx's own PGPASSWORD/.pgpass
// fallbacks then apply at connect).
func TestResolveDSNCredentialNothingConfigured(t *testing.T) {
	t.Setenv("BEADS_PG_PASSWORD_COMMAND", "")
	t.Setenv("BEADS_PG_PASSWORD", "")
	got, err := resolveDSNCredential(context.Background(), &configfile.Config{}, baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	if got != baseDSN {
		t.Fatalf("dsn = %q, want it unchanged (%q)", got, baseDSN)
	}
}

// A DSN that already carries a password wins outright — the ladder is not applied.
func TestResolveDSNCredentialExistingPasswordWins(t *testing.T) {
	t.Setenv("BEADS_PG_PASSWORD_COMMAND", "printf should-not-run")
	t.Setenv("BEADS_PG_PASSWORD", "should-not-run")
	withPw := "postgres://bts:already-here@127.0.0.1:5432/db"
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

// An identity credential (KindIdentity) has no home on a direct SQL connection and
// must be refused, never landed in the password slot.
func TestResolveDSNCredentialRefusesIdentity(t *testing.T) {
	src := staticSource{cred: creds.Credential{Value: "an-eia-token", Kind: creds.KindIdentity, Source: "test"}}
	_, err := resolveDSNWithSources(context.Background(), baseDSN, src)
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("expected an identity-refusal error, got %v", err)
	}
}

package configfile

import "testing"

// GetPostgresDSN merges the password from BEADS_PG_PASSWORD into a persisted,
// password-free DSN at open time — the counterpart to pgdialect.RedactPassword, which
// strips it for persistence. Persisting only the stripped form loses nothing: this
// reconstructs the full connection string operationally.
func TestGetPostgresDSNMergesPassword(t *testing.T) {
	t.Setenv("BEADS_PG_PASSWORD", "secret")
	c := &Config{PostgresDSN: "postgres://bts@127.0.0.1:5432/db"}
	if got, want := c.GetPostgresDSN(), "postgres://bts:secret@127.0.0.1:5432/db"; got != want {
		t.Fatalf("GetPostgresDSN() = %q, want %q", got, want)
	}
}

// BEADS_POSTGRES_URL (full URL) takes precedence over the metadata DSN + password merge.
func TestGetPostgresDSNEnvURLPrecedence(t *testing.T) {
	t.Setenv("BEADS_POSTGRES_URL", "postgres://a:b@host:5432/db")
	t.Setenv("BEADS_PG_PASSWORD", "ignored")
	c := &Config{PostgresDSN: "postgres://other@127.0.0.1:5432/db"}
	if got, want := c.GetPostgresDSN(), "postgres://a:b@host:5432/db"; got != want {
		t.Fatalf("GetPostgresDSN() = %q, want %q", got, want)
	}
}

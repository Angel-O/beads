package pgdialect

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
)

// HasPassword reports whether the connection string already carries a password, in
// either the URL or the libpq keyword/value form. A DSN that already has a password
// (a full BEADS_POSTGRES_URL, or an explicit --pg-url) wins outright: the credential
// ladder is not applied on top of it.
func HasPassword(dsn string) bool {
	cfg, err := pgx.ParseConfig(dsn)
	return err == nil && cfg.Password != ""
}

// WithPassword returns dsn with password placed in its password slot, handling both
// connection-string grammars pgx accepts — URL userinfo for scheme:// DSNs, a
// single-quoted keyword/value token otherwise — and then VERIFIES with the same
// parser pgx itself uses that the password round-tripped. It fails loudly rather
// than emit a DSN that silently drops or mangles the secret (the bug the old inline
// net/url merge had on keyword-form DSNs, which url.Parse cannot represent).
//
// This is the counterpart to RedactPassword: RedactPassword strips the password for
// persistence, WithPassword re-injects the resolved password at open time.
func WithPassword(dsn, password string) (string, error) {
	var out string
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		// URL form: set/replace the userinfo password, preserving any username.
		// url.String() percent-encodes special characters (an RDS-IAM token's
		// '&'/'='/'/'), which pgx.ParseConfig decodes back — the verify below proves it.
		user := ""
		if u.User != nil {
			user = u.User.Username()
		}
		u.User = url.UserPassword(user, password)
		out = u.String()
	} else {
		// libpq keyword/value form: drop any existing password token, then append a
		// single-quoted password (backslash-escaping backslash and quote).
		stripped := strings.TrimSpace(strings.Join(strings.Fields(kvPasswordRe.ReplaceAllString(dsn, "$1")), " "))
		out = stripped + " password='" + escapeKeywordValue(password) + "'"
	}

	cfg, err := pgx.ParseConfig(out)
	if err != nil {
		return "", fmt.Errorf("cannot place password into the connection string (unparseable result): %w", err)
	}
	if cfg.Password != password {
		return "", fmt.Errorf("password did not round-trip into the connection string; refusing to connect with a wrong or empty password")
	}
	return out, nil
}

// escapeKeywordValue escapes a value for a single-quoted libpq keyword/value token.
func escapeKeywordValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return s
}

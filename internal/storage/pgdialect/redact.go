package pgdialect

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// kvPasswordRe matches a libpq keyword/value password token (password= or
// sslpassword=), whose value is either single-quoted or a run of non-space chars.
var kvPasswordRe = regexp.MustCompile(`(?i)(^|\s)(?:password|sslpassword)\s*=\s*(?:'(?:[^'\\]|\\.)*'|\S*)`)

// RedactPassword returns dsn with the password removed, or an error if a password
// cannot be safely removed. It strips every known password location — URL userinfo,
// URL query params (password, sslpassword), and libpq keyword/value tokens — then
// VERIFIES with the same parser pgx itself uses that no password survives, failing
// closed rather than persisting a secret in a DSN shape we did not anticipate.
//
// Callers persist the returned string (e.g. to metadata.json) and re-supply the
// password at open time from BEADS_PG_PASSWORD (see configfile.GetPostgresDSN), so
// nothing operational is lost by never writing the password to disk. The hard
// invariant — a password must never be persisted — is enforced here, not assumed.
func RedactPassword(dsn string) (string, error) {
	stripped := stripPasswordBestEffort(dsn)
	cfg, err := pgx.ParseConfig(stripped)
	if err != nil {
		// If it will not parse, we cannot prove it is password-free. Refuse.
		return "", fmt.Errorf("cannot verify the connection string is free of a password (unparseable after redaction): %w", err)
	}
	if cfg.Password != "" {
		return "", fmt.Errorf("refusing to persist a connection string that still carries a password; supply it via BEADS_PG_PASSWORD instead of embedding it in --pg-url")
	}
	return stripped, nil
}

// stripPasswordBestEffort removes passwords from the two connection-string shapes
// pgx accepts. The authoritative guarantee is RedactPassword's post-parse check; this
// only has to handle the common forms well enough not to trip that check falsely.
func stripPasswordBestEffort(dsn string) string {
	// URL form (scheme://...): strip userinfo password and query password/sslpassword.
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		if u.User != nil {
			if name := u.User.Username(); name == "" {
				u.User = nil
			} else {
				u.User = url.User(name)
			}
		}
		q := u.Query()
		q.Del("password")
		q.Del("sslpassword")
		u.RawQuery = q.Encode()
		return u.String()
	}
	// libpq keyword/value form (host=... password=... ...): drop the password tokens
	// and collapse surrounding whitespace so the remainder still parses.
	out := kvPasswordRe.ReplaceAllString(dsn, "$1")
	return strings.TrimSpace(strings.Join(strings.Fields(out), " "))
}

package mysqldialect

import (
	"fmt"

	"github.com/go-sql-driver/mysql"
)

// HasPassword reports whether the DSN already carries a password. A DSN that already
// has one (a full BEADS_MYSQL_URL, or an explicit --mysql-url) wins outright: the
// credential ladder is not applied on top of it.
func HasPassword(dsn string) bool {
	cfg, err := mysql.ParseDSN(dsn)
	return err == nil && cfg.Passwd != ""
}

// WithPassword returns dsn with password placed in its password slot, using
// go-sql-driver's own parser (never string surgery) and VERIFYING with a re-parse
// that the password round-tripped. It fails loudly rather than emit a DSN that
// silently drops the secret.
//
// Unlike pgdialect there is a single DSN grammar (user[:password]@net(addr)/db), so
// there is no URL-vs-keyword branch and no escaping — FormatDSN writes the password
// verbatim and ParseDSN recovers it structurally. But the grammar has one sharp edge:
// it cannot carry a password without a username (FormatDSN emits the user:pass@ block
// only when a user is present), so a userless DSN would silently drop the password.
// We reject that up front. This is the MySQL twin of the keyword-form drop pgdialect
// guards against. It is the counterpart to mysql.RedactPassword, which strips the
// password for persistence.
func WithPassword(dsn, password string) (string, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return "", fmt.Errorf("cannot place password into the connection string (unparseable DSN): %w", err)
	}
	if cfg.User == "" {
		return "", fmt.Errorf("cannot place a password: the DSN has no username, and the MySQL DSN grammar cannot carry a password without one; add a user to the DSN")
	}
	cfg.Passwd = password
	out := cfg.FormatDSN()

	again, err := mysql.ParseDSN(out)
	if err != nil {
		return "", fmt.Errorf("cannot place password into the connection string (unparseable result): %w", err)
	}
	if again.Passwd != password {
		return "", fmt.Errorf("password did not round-trip into the connection string; refusing to connect with a wrong or empty password")
	}
	return out, nil
}

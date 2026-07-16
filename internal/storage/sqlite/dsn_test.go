package sqlite

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// TestDSNBusyTimeoutPrecedesJournalMode guards the pragma ORDER inside the DSN.
//
// Setting journal_mode(WAL) before busy_timeout leaves a window on first open
// where a WAL-mode switch can collide with another process and surface a raw
// SQLITE_BUSY: the journal-mode change needs the database lock, and without a
// busy_timeout already installed the connection gives up immediately. Today
// modernc.org/sqlite rescues that ordering by sorting busy_timeout ahead of
// every other _pragma at connect time (sqlite.go, applyPragmas) — but that is
// a driver implementation detail, not a contract. The DSN itself must encode
// the safe order so a driver that applies pragmas verbatim (or a future
// modernc version that drops the sort) cannot reintroduce the race.
func TestDSNBusyTimeoutPrecedesJournalMode(t *testing.T) {
	d := dsn("/tmp/ws/beads.db")
	bt := strings.Index(d, "busy_timeout")
	jm := strings.Index(d, "journal_mode")
	if bt < 0 || jm < 0 {
		t.Fatalf("dsn missing busy_timeout and/or journal_mode pragma: %q", d)
	}
	if bt > jm {
		t.Errorf("busy_timeout must precede journal_mode in the DSN (defense-in-depth against in-order pragma application), got %q", d)
	}
}

// TestDSNCarriesMultiprocessSafetyPragmas pins the full parameter set the
// multiprocess-safety audit proved out (WAL + busy_timeout(5000) +
// _txlock=immediate + single-conn pool). Dropping any one of these silently
// reopens a proven corruption/contention class, so removal must be a loud,
// deliberate act.
func TestDSNCarriesMultiprocessSafetyPragmas(t *testing.T) {
	d := dsn("/tmp/ws/beads.db")
	for _, want := range []string{
		"_pragma=foreign_keys(1)",
		"_pragma=journal_mode(WAL)",
		"_pragma=busy_timeout(5000)",
		"_txlock=immediate",
		"_time_format=datetime",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("dsn lost load-bearing parameter %q: %q", want, d)
		}
	}
}

// TestDSNFileURIPassthrough documents that a caller-supplied file: URI is used
// verbatim — pragmas are the caller's responsibility on that path.
func TestDSNFileURIPassthrough(t *testing.T) {
	in := "file:/tmp/ws/beads.db?_pragma=busy_timeout(100)"
	if got := dsn(in); got != in {
		t.Errorf("dsn(%q) = %q, want passthrough", in, got)
	}
}

// TestDSNCaseSensitiveLike pins the mechanism behind bd-oyvc2.10: dsn() must carry
// _pragma=case_sensitive_like(1), and modernc.org/sqlite must apply it on EVERY new
// connection (DSN _pragma params are per-connection), so raw-cased LIKE matches the
// other backends' case-sensitive collations. The behavioral contract is covered by
// the conformance corpus (Audit/*CaseSensitive); this guards the DSN wiring itself.
func TestDSNCaseSensitiveLike(t *testing.T) {
	db, err := sql.Open("sqlite", dsn(filepath.Join(t.TempDir(), "like.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Multiple pooled connections, each of which must have the pragma applied.
	db.SetMaxOpenConns(4)
	for i := 0; i < 4; i++ {
		var v int
		if err := db.QueryRow("SELECT 'a' LIKE 'A'").Scan(&v); err != nil {
			t.Fatal(err)
		}
		if v != 0 {
			t.Fatalf("'a' LIKE 'A' = %d, want 0 (case-sensitive; SQLite default is ASCII-case-insensitive)", v)
		}
	}
}

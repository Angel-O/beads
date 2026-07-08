package sqlite

import "strings"

// dsn builds a modernc.org/sqlite DSN for a database file path. The pragmas make the
// backend safe under multi-PROCESS access (bd is a separate binary many callers shell
// out to concurrently, alongside a long-lived reader):
//
//   - foreign_keys(1)     — FK enforcement (off by default in SQLite).
//   - journal_mode(WAL)   — write-ahead logging: readers no longer block the writer and
//     vice versa (the default rollback-journal mode mutually excludes them). Persistent
//     on the file; asserting it per-connection is an idempotent no-op after the first.
//   - busy_timeout(5000)  — a connection that can't acquire the lock WAITS up to 5s
//     rather than returning SQLITE_BUSY instantly (the default is 0 = fail immediately).
//     busy_timeout is per-connection, so it must live in the DSN, not be set once.
//   - _txlock=immediate   — every transaction takes the write lock up front (BEGIN
//     IMMEDIATE), so a read-then-write never fails the upgrade with BUSY_SNAPSHOT and
//     writers serialize correctly.
//
// synchronous stays at the SQLite default (FULL) — durable per commit under WAL. Drop to
// NORMAL (fsync only at checkpoint) as a perf lever if the "lose the last commits on an
// OS/power crash" trade is acceptable; that is a deliberate durability choice, left off here.
//
// A caller supplying a full "file:...?..." DSN opts out of these defaults verbatim.
func dsn(path string) string {
	if strings.HasPrefix(path, "file:") {
		return path
	}
	return "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"
}

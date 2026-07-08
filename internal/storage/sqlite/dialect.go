package sqlite

import (
	"context"
	"database/sql"

	"github.com/steveyegge/beads/internal/storage/sqlitedialect"
)

// sqliteDialect is the SQLite SQL flavor: it opens a *sql.DB over modernc.org/sqlite
// whose driver translates bd's canonical MySQL SQL to SQLite (small: mainly
// INSERT IGNORE → INSERT OR IGNORE). The dsn already carries the file path + pragmas.
type sqliteDialect struct{ dsn string }

func (d sqliteDialect) Name() string { return "sqlite" }

func (d sqliteDialect) Open(_ context.Context) (*sql.DB, error) {
	db, err := sqlitedialect.Open(d.dsn)
	if err != nil {
		return nil, err
	}
	// Serialize this process's access to a single connection. With _txlock=immediate,
	// database/sql's default (unbounded) pool would let two goroutines each open a
	// connection and instantly collide on the write lock. One connection makes
	// in-process access race-free; cross-PROCESS concurrency is handled by WAL +
	// busy_timeout in the DSN. At bd's write rate the lost in-process read parallelism
	// is irrelevant, and correctness is trivial.
	db.SetMaxOpenConns(1)
	return db, nil
}

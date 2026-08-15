package dolt

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/steveyegge/beads/internal/storage/doltutil"
	"github.com/steveyegge/beads/internal/storage/schema"
)

// freshDBWithBootstrapDebris creates a never-migrated database on the test
// server and plants the debris an aborted first migration pass leaves behind:
// a table that migration 0001 creates, present but uncommitted in the Dolt
// working set. It returns a pool on that database.
func freshDBWithBootstrapDebris(t *testing.T, ctx context.Context) *sql.DB {
	t.Helper()
	dbName := uniqueTestDBName(t)
	initDSN := doltutil.ServerDSN{Host: "127.0.0.1", Port: testServerPort, User: "root"}.String()
	initDB, err := sql.Open("mysql", initDSN)
	if err != nil {
		t.Fatalf("open init connection: %v", err)
	}
	defer initDB.Close()
	if _, err := initDB.ExecContext(ctx, "CREATE DATABASE `"+dbName+"`"); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cleanupDB, err := sql.Open("mysql", initDSN)
		if err == nil {
			_, _ = cleanupDB.ExecContext(cctx, "DROP DATABASE IF EXISTS `"+dbName+"`")
			cleanupDB.Close()
		}
	})

	dsn := doltutil.ServerDSN{Host: "127.0.0.1", Port: testServerPort, User: "root", Database: dbName}.String()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(2)

	// The debris: migration 0001's table exists but was never committed —
	// exactly what an "i/o timeout" mid-pass leaves in the working set.
	if _, err := db.ExecContext(ctx, "CREATE TABLE issues (id VARCHAR(255) PRIMARY KEY)"); err != nil {
		t.Fatalf("plant debris: %v", err)
	}
	return db
}

// TestFreshBootstrapHealConvergesOnOwnDebris: the process that created the
// database retries into its own aborted pass's debris; the #4566 guard fires,
// the heal discards the working set, and the retry converges to the latest
// schema (gastownhall/beads#5012 — the Homebrew gastown `bd init` failure).
func TestFreshBootstrapHealConvergesOnOwnDebris(t *testing.T) {
	skipIfNoDolt(t)
	acquireTestSlot()
	t.Cleanup(releaseTestSlot)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	db := freshDBWithBootstrapDebris(t, ctx)

	if _, err := initSchemaOnDBWithRetryAndGateOwnership(ctx, db, nil, true); err != nil {
		t.Fatalf("expected the fresh-bootstrap heal to converge, got: %v", err)
	}
	got, err := schema.CurrentVersion(ctx, db)
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if want := schema.LatestVersion(); got != want {
		t.Fatalf("schema version after heal = %d, want %d", got, want)
	}
	// The debris table was replaced by the real 0001 schema (more than one column).
	var cols int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'issues'").Scan(&cols); err != nil {
		t.Fatalf("count issues columns: %v", err)
	}
	if cols < 2 {
		t.Fatalf("issues table still has the debris shape (%d column)", cols)
	}
}

// TestFreshBootstrapHealNotArmedForPreexistingDatabase: the same debris on a
// database this process did NOT create must still be refused by the #4566
// guard — dirty tables there may be user data, and the heal is never armed.
func TestFreshBootstrapHealNotArmedForPreexistingDatabase(t *testing.T) {
	skipIfNoDolt(t)
	acquireTestSlot()
	t.Cleanup(releaseTestSlot)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	db := freshDBWithBootstrapDebris(t, ctx)

	_, err := initSchemaOnDBWithRetryAndGateOwnership(ctx, db, nil, false)
	var dirty *schema.DirtyTablesError
	if !errors.As(err, &dirty) {
		t.Fatalf("expected *schema.DirtyTablesError for a pre-existing database, got: %v", err)
	}
	// And the debris is untouched: still exactly the one planted column.
	var cols int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'issues'").Scan(&cols); err != nil {
		t.Fatalf("count issues columns: %v", err)
	}
	if cols != 1 {
		t.Fatalf("pre-existing database was modified: issues has %d columns, want 1", cols)
	}
}

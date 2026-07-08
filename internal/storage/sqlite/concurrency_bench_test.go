package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSQLiteMultiProcessContention is the standalone before/after measurement for the
// multi-process concurrency fix. It simulates N separate bd PROCESSES with N independent
// *sql.DB handles (each its own pool) to one file, plus a long-lived reader holding an
// open snapshot — gascity's real shape (a persistent controller reader + short-lived
// agent writers). It runs the OLD DSN (rollback-journal, busy_timeout=0) and the NEW DSN
// (WAL + busy_timeout=5000), logs "database is locked" rate + write-latency percentiles
// for each, and ASSERTS the new config is clean (0 locked). The OLD arm is logged, not
// asserted (its failure rate is inherently racy — the point is the contrast).
//
// Run just this: go test ./internal/storage/sqlite -run TestSQLiteMultiProcessContention -v
func TestSQLiteMultiProcessContention(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-process contention benchmark; skipped in -short")
	}
	const (
		procs    = 8  // independent *sql.DB handles ≈ separate processes
		perProc  = 30 // point writes each
		attempts = procs * perProc
	)

	old := runContentionArm(t, "OLD (rollback-journal, busy_timeout=0)", false, 0, procs, perProc)
	neu := runContentionArm(t, "NEW (WAL, busy_timeout=5000)", true, 5000, procs, perProc)

	t.Logf("\n  ARM                                   attempts  locked  p50        p99        wall")
	for _, r := range []armResult{old, neu} {
		t.Logf("  %-36s  %8d  %6d  %-9s  %-9s  %s", r.name, r.attempts, r.locked,
			r.p50.Round(time.Microsecond), r.p99.Round(time.Microsecond), r.wall.Round(time.Millisecond))
	}

	if neu.locked != 0 {
		t.Fatalf("NEW config still produced %d/%d 'database is locked' errors — WAL+busy_timeout not effective", neu.locked, attempts)
	}
}

type armResult struct {
	name             string
	attempts, locked int
	p50, p99, wall   time.Duration
}

// benchDSN builds a DSN for the arm. wal toggles journal_mode(WAL); busyMs>0 adds
// busy_timeout; immediate adds _txlock=immediate (writers use it, the reader does not).
func benchDSN(path string, wal bool, busyMs int, immediate bool) string {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)"
	if wal {
		dsn += "&_pragma=journal_mode(WAL)"
	}
	if busyMs > 0 {
		dsn += fmt.Sprintf("&_pragma=busy_timeout(%d)", busyMs)
	}
	if immediate {
		dsn += "&_txlock=immediate"
	}
	return dsn
}

func runContentionArm(t *testing.T, name string, wal bool, busyMs, procs, perProc int) armResult {
	t.Helper()
	ctx := context.Background()
	path := fmt.Sprintf("%s/bench-%v-%d.db", t.TempDir(), wal, busyMs)

	// Provision: create the table and set the file's journal mode (persistent).
	prov, err := sql.Open("sqlite", benchDSN(path, wal, busyMs, true))
	if err != nil {
		t.Fatalf("%s: open provisioner: %v", name, err)
	}
	if _, err := prov.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS bench(id INTEGER PRIMARY KEY AUTOINCREMENT, n INTEGER)"); err != nil {
		t.Fatalf("%s: create table: %v", name, err)
	}
	_ = prov.Close()

	// A long-lived reader holding an open snapshot (no _txlock=immediate — a real reader).
	reader, err := sql.Open("sqlite", benchDSN(path, wal, busyMs, false))
	if err != nil {
		t.Fatalf("%s: open reader: %v", name, err)
	}
	reader.SetMaxOpenConns(1)
	rtx, err := reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("%s: reader begin: %v", name, err)
	}
	var cnt int
	_ = rtx.QueryRowContext(ctx, "SELECT count(*) FROM bench").Scan(&cnt) // acquire the read lock/snapshot

	// N independent writer "processes", each its own pool pinned to one connection.
	type res struct {
		lat    []time.Duration
		locked int
	}
	results := make([]res, procs)
	var wg sync.WaitGroup
	start := time.Now()
	for p := 0; p < procs; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			db, err := sql.Open("sqlite", benchDSN(path, wal, busyMs, true))
			if err != nil {
				results[p].locked = perProc
				return
			}
			db.SetMaxOpenConns(1)
			defer db.Close()
			for i := 0; i < perProc; i++ {
				t0 := time.Now()
				_, err := db.ExecContext(ctx, "INSERT INTO bench(n) VALUES(?)", p*1000+i)
				results[p].lat = append(results[p].lat, time.Since(t0))
				if err != nil {
					e := strings.ToLower(err.Error())
					if strings.Contains(e, "locked") || strings.Contains(e, "busy") {
						results[p].locked++
					}
				}
			}
		}(p)
	}
	wg.Wait()
	wall := time.Since(start)

	_ = rtx.Rollback()
	_ = reader.Close()

	var all []time.Duration
	locked := 0
	for _, r := range results {
		all = append(all, r.lat...)
		locked += r.locked
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	pct := func(p float64) time.Duration {
		if len(all) == 0 {
			return 0
		}
		idx := int(p * float64(len(all)-1))
		return all[idx]
	}
	return armResult{name: name, attempts: procs * perProc, locked: locked, p50: pct(0.50), p99: pct(0.99), wall: wall}
}

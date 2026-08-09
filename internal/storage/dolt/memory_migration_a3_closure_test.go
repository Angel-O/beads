package dolt

// PROTOTYPE ONLY: executable closure evidence for Memory Beads spike A3.
// The production provider is intentionally not wired to these throwaway
// tables or gates. These tests exercise the real direct/server Dolt path.

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage/doltmemorymigration"
	"github.com/steveyegge/beads/internal/storage/schema"
)

const (
	a3ProjectID = "018f6df0-7b4b-7a20-9d31-4f517f2860c1"
	a3Author    = "Ada Example <ada@example.com>"
)

var (
	errA3BeforePublish = errors.New("a3 fixture: stop before branch publication")
	errA3LostAck       = errors.New("a3 fixture: lose branch publication acknowledgement")
)

func TestMemoryMigrationA3Closure_PreflightSerializesWithOrdinaryConfigWriter(t *testing.T) {
	t.Run("writer_first_rechecks_freshness", func(t *testing.T) {
		store, cleanup := setupConcurrentTestStore(t)
		defer cleanup()
		ctx, cancel := testContext(t)
		defer cancel()
		a3InstallFixture(t, ctx, store.db)

		writer, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Rollback()
		if err := doltmemorymigration.AdmitConfigMutation(ctx, writer, doltmemorymigration.LegacyPrefix()+"writer-first"); err != nil {
			t.Fatalf("admit ordinary writer: %v", err)
		}
		if _, err := writer.ExecContext(ctx,
			"REPLACE INTO config (`key`, value) VALUES (?, ?)",
			doltmemorymigration.LegacyPrefix()+"writer-first", "written before marker"); err != nil {
			t.Fatal(err)
		}

		firstPreflight := make(chan struct{})
		releasePreflight := make(chan struct{})
		var preflights atomic.Int32
		coordinator, err := doltmemorymigration.New(store.db, doltmemorymigration.Options{
			ProjectID: a3ProjectID,
			Author:    a3Author,
			OnEvent: func(ctx context.Context, event doltmemorymigration.Event) error {
				if event.Point != doltmemorymigration.EventPreflightRead {
					return nil
				}
				if preflights.Add(1) == 1 {
					close(firstPreflight)
					select {
					case <-releasePreflight:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		done := a3RunAsync(ctx, coordinator)
		a3WaitSignal(t, firstPreflight, "migration preflight")
		if err := writer.Commit(); err != nil {
			t.Fatalf("commit writer that won ordering: %v", err)
		}
		close(releasePreflight)
		out := a3WaitRun(t, done)
		if out.err != nil {
			t.Fatalf("migration after writer-first ordering: %v", out.err)
		}
		if preflights.Load() < 2 {
			t.Fatalf("preflight count = %d, want a fresh preflight after source changed", preflights.Load())
		}
		var body string
		if err := store.db.QueryRowContext(ctx,
			"SELECT body FROM "+doltmemorymigration.PrototypeBeadsTable()+" WHERE legacy_key = 'writer-first'").Scan(&body); err != nil {
			t.Fatalf("read converted writer-first state: %v", err)
		}
		if body != "written before marker" {
			t.Fatalf("converted body = %q", body)
		}
	})

	t.Run("marker_first_retries_to_typed_refusal", func(t *testing.T) {
		store, cleanup := setupConcurrentTestStore(t)
		defer cleanup()
		ctx, cancel := testContext(t)
		defer cancel()
		a3InstallFixture(t, ctx, store.db)
		a3SeedLegacy(t, ctx, store.db, "marker-first", "before marker", "a3 fixture: marker-first")

		prepared := make(chan struct{})
		allowMarkerCommit := make(chan struct{})
		committed := make(chan struct{})
		allowMigration := make(chan struct{})
		coordinator, err := doltmemorymigration.New(store.db, doltmemorymigration.Options{
			ProjectID: a3ProjectID,
			Author:    a3Author,
			OnEvent: func(ctx context.Context, event doltmemorymigration.Event) error {
				switch event.Point {
				case doltmemorymigration.EventControlPrepared:
					close(prepared)
					select {
					case <-allowMarkerCommit:
					case <-ctx.Done():
						return ctx.Err()
					}
				case doltmemorymigration.EventControlCommitted:
					close(committed)
					select {
					case <-allowMigration:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		migrationDone := a3RunAsync(ctx, coordinator)
		a3WaitSignal(t, prepared, "prepared marker")

		writerAttempted := make(chan struct{})
		var attemptOnce sync.Once
		writerDone := make(chan error, 1)
		go func() {
			writerDone <- store.withRetryTx(ctx, func(tx *sql.Tx) error {
				attemptOnce.Do(func() { close(writerAttempted) })
				if err := doltmemorymigration.AdmitConfigMutation(ctx, tx, doltmemorymigration.LegacyPrefix()+"marker-first"); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx,
					"REPLACE INTO config (`key`, value) VALUES (?, 'must not land')",
					doltmemorymigration.LegacyPrefix()+"marker-first")
				return err
			})
		}()
		a3WaitSignal(t, writerAttempted, "overlapping config writer")
		close(allowMarkerCommit)
		a3WaitSignal(t, committed, "committed marker")

		var writerErr error
		select {
		case writerErr = <-writerDone:
		case <-time.After(10 * time.Second):
			t.Fatal("ordinary writer did not serialize and retry after marker commit")
		}
		var inProgress *doltmemorymigration.MigrationInProgressError
		if !errors.As(writerErr, &inProgress) {
			t.Fatalf("writer error = %v, want typed migration_in_progress", writerErr)
		}
		var body string
		if err := store.db.QueryRowContext(ctx,
			"SELECT value FROM config WHERE `key` = ?", doltmemorymigration.LegacyPrefix()+"marker-first").Scan(&body); err != nil {
			t.Fatal(err)
		}
		if body != "before marker" {
			t.Fatalf("marker-first writer mutated source to %q", body)
		}
		close(allowMigration)
		if out := a3WaitRun(t, migrationDone); out.err != nil {
			t.Fatalf("finish marker-first migration: %v", out.err)
		}
	})
}

func TestMemoryMigrationA3Closure_FaultsRestartAndNoMixedServing(t *testing.T) {
	store, cleanup := setupConcurrentTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()
	mainBranch, peerBranch := a3PrepareDivergentFixture(t, ctx, store.db)

	var publications atomic.Int32
	first, err := doltmemorymigration.New(store.db, doltmemorymigration.Options{
		ProjectID: a3ProjectID,
		Author:    a3Author,
		OnEvent: func(_ context.Context, event doltmemorymigration.Event) error {
			if event.Point == doltmemorymigration.EventBranchPublished && publications.Add(1) == 1 {
				return &doltmemorymigration.PublicationIndeterminateError{Branch: event.Branch, Cause: errA3LostAck}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstResult, err := first.Run(ctx)
	if !errors.Is(err, errA3LostAck) {
		t.Fatalf("first run error = %v, want lost acknowledgement", err)
	}
	var indeterminate *doltmemorymigration.PublicationIndeterminateError
	if !errors.As(err, &indeterminate) {
		t.Fatalf("first run error = %v, want typed indeterminate publication", err)
	}
	_ = firstResult // the acknowledgement was deliberately lost
	snapshot, found, err := doltmemorymigration.InspectControl(ctx, store.db)
	if err != nil || !found {
		t.Fatalf("inspect in-progress control: found=%v err=%v", found, err)
	}
	if snapshot.Phase != doltmemorymigration.PhaseInProgress {
		t.Fatalf("phase = %q, want migration_in_progress", snapshot.Phase)
	}

	var canonicalViews, legacyViews int
	for _, branch := range []string{mainBranch, peerBranch} {
		a3OnBranch(t, ctx, store.db, branch, func(conn *sql.Conn) {
			if a3Count(t, ctx, conn,
				"SELECT COUNT(*) FROM "+doltmemorymigration.PrototypeLedgerTable()+" WHERE migration_id = ?", snapshot.MigrationID) == 1 {
				canonicalViews++
			}
			if a3Count(t, ctx, conn,
				"SELECT COUNT(*) FROM config WHERE `key` LIKE CONCAT(?, '%')", doltmemorymigration.LegacyPrefix()) != 0 {
				legacyViews++
			}
			for _, surface := range []string{
				"canonical read", "canonical write", "deprecated read", "deprecated write",
			} {
				mutated := false
				err := a3GuardedMemoryOperation(ctx, conn, func() { mutated = true })
				var inProgress *doltmemorymigration.MigrationInProgressError
				if !errors.As(err, &inProgress) {
					t.Errorf("%s on %s error = %v, want typed migration_in_progress", surface, branch, err)
				}
				if mutated {
					t.Errorf("%s on %s mutated while migration was in progress", surface, branch)
				}
			}
		})
	}
	if canonicalViews != 1 || legacyViews != 1 {
		t.Fatalf("physical one-branch publication = canonical views %d, legacy views %d; want 1 and 1", canonicalViews, legacyViews)
	}

	// A new coordinator and pinned SQL session model process restart/reopen.
	second, err := doltmemorymigration.New(store.db, doltmemorymigration.Options{ProjectID: a3ProjectID, Author: a3Author})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := second.Run(ctx)
	if err != nil {
		t.Fatalf("resume after lost acknowledgement: %v", err)
	}
	if resumed.MigrationID != snapshot.MigrationID {
		t.Fatalf("resume migration ID = %q, want %q", resumed.MigrationID, snapshot.MigrationID)
	}

	var beadIDs []string
	var revisionIDs []string
	for _, branch := range []string{mainBranch, peerBranch} {
		a3OnBranch(t, ctx, store.db, branch, func(conn *sql.Conn) {
			if err := doltmemorymigration.CheckMemoryAccess(ctx, conn); err != nil {
				t.Errorf("completed gate on %s: %v", branch, err)
			}
			if got := a3Count(t, ctx, conn,
				"SELECT COUNT(*) FROM config WHERE `key` LIKE CONCAT(?, '%')", doltmemorymigration.LegacyPrefix()); got != 0 {
				t.Errorf("legacy rows on %s = %d", branch, got)
			}
			beadIDs = append(beadIDs, a3String(t, ctx, conn,
				"SELECT id FROM "+doltmemorymigration.PrototypeBeadsTable()+" WHERE legacy_key = 'shared'"))
			revisionIDs = append(revisionIDs, a3String(t, ctx, conn,
				"SELECT id FROM "+doltmemorymigration.PrototypeRevisionsTable()+" WHERE bead_id = ?", beadIDs[len(beadIDs)-1]))
			if got := a3Count(t, ctx, conn,
				"SELECT COUNT(*) FROM "+doltmemorymigration.PrototypeLedgerTable()+" WHERE migration_id = ?", snapshot.MigrationID); got != 1 {
				t.Errorf("ledger rows on %s = %d, want 1", branch, got)
			}
		})
	}
	if beadIDs[0] != beadIDs[1] {
		t.Fatalf("semantic bead identity diverged across views: %q != %q", beadIDs[0], beadIDs[1])
	}
	if revisionIDs[0] == revisionIDs[1] {
		t.Fatalf("divergent branch bodies collapsed to revision %q", revisionIDs[0])
	}

	before := a3Count(t, ctx, store.db, "SELECT COUNT(*) FROM dolt_log")
	third, _ := doltmemorymigration.New(store.db, doltmemorymigration.Options{ProjectID: a3ProjectID, Author: a3Author})
	thirdResult, err := third.Run(ctx)
	if err != nil {
		t.Fatalf("idempotent completed retry: %v", err)
	}
	if !thirdResult.AlreadyComplete {
		t.Fatal("idempotent retry did not report already complete")
	}
	after := a3Count(t, ctx, store.db, "SELECT COUNT(*) FROM dolt_log")
	if after != before {
		t.Fatalf("idempotent retry added commits: before=%d after=%d", before, after)
	}
	if err := doltmemorymigration.ResetPrototypeControl(ctx, store.db); err != nil {
		t.Fatalf("simulate lost clone-local phase record: %v", err)
	}
	var missingPhase *doltmemorymigration.MigrationInProgressError
	if err := doltmemorymigration.CheckMemoryAccess(ctx, store.db); !errors.As(err, &missingPhase) {
		t.Fatalf("gate after lost phase record = %v, want typed unavailable from versioned ledger evidence", err)
	}
	fourth, _ := doltmemorymigration.New(store.db, doltmemorymigration.Options{ProjectID: a3ProjectID, Author: a3Author})
	if _, err := fourth.Run(ctx); err == nil {
		t.Fatal("coordinator recreated a new migration after the phase record was lost")
	} else {
		var preflight *doltmemorymigration.PreflightError
		if !errors.As(err, &preflight) {
			t.Fatalf("lost-phase coordinator error = %v, want recovery preflight", err)
		}
	}
}

func TestMemoryMigrationA3Closure_HistoricalPreSchemaViewPublishesSchemaAtomically(t *testing.T) {
	store, cleanup := setupConcurrentTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	// This branch is real history from before the throwaway canonical tables
	// existed. Keeping its name after the active branch makes the first run
	// publish the active view, then fault while preparing the historical one.
	a3SeedLegacy(t, ctx, store.db, "pre-schema", "historical body", "a3 fixture: pre-schema history")
	const historicalBranch = "zz-a3-pre-schema-history"
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.DrainCall(ctx, conn, "CALL DOLT_BRANCH(?, 'HEAD')", historicalBranch); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	if err := doltmemorymigration.InstallPrototypeSchema(ctx, conn); err != nil {
		_ = conn.Close()
		t.Fatalf("install A3 fixture after historical branch: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	first, _ := doltmemorymigration.New(store.db, doltmemorymigration.Options{
		ProjectID: a3ProjectID,
		Author:    a3Author,
		OnEvent: func(_ context.Context, event doltmemorymigration.Event) error {
			if event.Point == doltmemorymigration.EventBranchPrepared && event.Branch == historicalBranch {
				return errA3BeforePublish
			}
			return nil
		},
	})
	if _, err := first.Run(ctx); !errors.Is(err, errA3BeforePublish) {
		t.Fatalf("historical pre-schema fault = %v, want before-publication fault", err)
	}

	var mainBranch string
	if err := store.db.QueryRowContext(ctx, "SELECT active_branch()").Scan(&mainBranch); err != nil {
		t.Fatal(err)
	}
	a3OnBranch(t, ctx, store.db, mainBranch, func(conn *sql.Conn) {
		if got := a3Count(t, ctx, conn,
			"SELECT COUNT(*) FROM "+doltmemorymigration.PrototypeLedgerTable()); got != 1 {
			t.Fatalf("published active-view ledgers = %d, want 1", got)
		}
		var inProgress *doltmemorymigration.MigrationInProgressError
		if err := doltmemorymigration.CheckMemoryAccess(ctx, conn); !errors.As(err, &inProgress) {
			t.Fatalf("published active-view gate = %v, want global typed unavailable", err)
		}
	})
	a3OnBranch(t, ctx, store.db, historicalBranch, func(conn *sql.Conn) {
		if got := a3Count(t, ctx, conn,
			"SELECT COUNT(*) FROM config WHERE `key` = ?", doltmemorymigration.LegacyPrefix()+"pre-schema"); got != 1 {
			t.Fatalf("historical legacy rows after rollback = %d, want 1", got)
		}
		if got := a3PrototypeTableCount(t, ctx, conn); got != 0 {
			t.Fatalf("historical prototype tables after rollback = %d, want 0", got)
		}
		if got := a3Count(t, ctx, conn, "SELECT COUNT(*) FROM dolt_status WHERE staged = 1"); got != 0 {
			t.Fatalf("historical staged changes after rollback = %d, want 0", got)
		}
		var inProgress *doltmemorymigration.MigrationInProgressError
		if err := doltmemorymigration.CheckMemoryAccess(ctx, conn); !errors.As(err, &inProgress) {
			t.Fatalf("historical pre-schema gate = %v, want typed unavailable", err)
		}
	})

	second, _ := doltmemorymigration.New(store.db, doltmemorymigration.Options{ProjectID: a3ProjectID, Author: a3Author})
	if _, err := second.Run(ctx); err != nil {
		t.Fatalf("resume historical pre-schema view: %v", err)
	}
	a3OnBranch(t, ctx, store.db, historicalBranch, func(conn *sql.Conn) {
		if got := a3PrototypeTableCount(t, ctx, conn); got != 3 {
			t.Fatalf("historical prototype tables after publication = %d, want 3", got)
		}
		if got := a3Count(t, ctx, conn,
			"SELECT COUNT(*) FROM config WHERE `key` LIKE CONCAT(?, '%')", doltmemorymigration.LegacyPrefix()); got != 0 {
			t.Fatalf("historical legacy rows after publication = %d, want 0", got)
		}
		if got := a3Count(t, ctx, conn,
			"SELECT COUNT(*) FROM "+doltmemorymigration.PrototypeBeadsTable()+" WHERE legacy_key = 'pre-schema'"); got != 1 {
			t.Fatalf("historical canonical rows after publication = %d, want 1", got)
		}
		if err := doltmemorymigration.CheckMemoryAccess(ctx, conn); err != nil {
			t.Fatalf("historical gate after convergence: %v", err)
		}
	})
}

func TestMemoryMigrationA3Closure_ConcurrentCoordinatorsConvergeOnce(t *testing.T) {
	store, cleanup := setupConcurrentTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()
	a3InstallFixture(t, ctx, store.db)
	a3SeedLegacy(t, ctx, store.db, "concurrent", "one body", "a3 fixture: concurrent migration")

	coordinators := make([]*doltmemorymigration.Coordinator, 2)
	for i := range coordinators {
		var err error
		coordinators[i], err = doltmemorymigration.New(store.db, doltmemorymigration.Options{
			ProjectID: a3ProjectID,
			Author:    a3Author,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	results := make([]doltmemorymigration.Result, len(coordinators))
	errs := make([]error, len(coordinators))
	var wg sync.WaitGroup
	for i := range coordinators {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = coordinators[i].Run(ctx)
		}(i)
	}
	wg.Wait()

	migrated, already := 0, 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("coordinator %d: %v", i, errs[i])
		}
		if len(results[i].MigratedViews) != 0 {
			migrated++
		}
		if results[i].AlreadyComplete {
			already++
		}
	}
	if migrated != 1 || already != 1 {
		t.Fatalf("concurrent results = %+v, want one migration and one completed observation", results)
	}
	if got := a3Count(t, ctx, store.db,
		"SELECT COUNT(*) FROM "+doltmemorymigration.PrototypeRevisionsTable()+" WHERE origin = 'legacy_migration'"); got != 1 {
		t.Fatalf("concurrent migration revisions = %d, want 1", got)
	}
}

func TestMemoryMigrationA3Closure_PreservesRelatedBranchWorkingState(t *testing.T) {
	store, cleanup := setupConcurrentTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()
	a3InstallFixture(t, ctx, store.db)
	a3SeedLegacy(t, ctx, store.db, "working-view", "committed main body", "a3 fixture: working-view ancestor")

	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var mainBranch string
	if err := conn.QueryRowContext(ctx, "SELECT active_branch()").Scan(&mainBranch); err != nil {
		t.Fatal(err)
	}
	const peerBranch = "a3-working-peer"
	if err := schema.DrainCall(ctx, conn, "CALL DOLT_BRANCH(?, 'HEAD')", peerBranch); err != nil {
		t.Fatal(err)
	}
	if err := schema.DrainCall(ctx, conn, "CALL DOLT_CHECKOUT(?)", peerBranch); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx,
		"UPDATE config SET value = 'unpublished peer body' WHERE `key` = ?",
		doltmemorymigration.LegacyPrefix()+"working-view"); err != nil {
		t.Fatal(err)
	}
	if err := schema.DrainCall(ctx, conn, "CALL DOLT_CHECKOUT(?)", mainBranch); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	coordinator, _ := doltmemorymigration.New(store.db, doltmemorymigration.Options{ProjectID: a3ProjectID, Author: a3Author})
	if _, err := coordinator.Run(ctx); err != nil {
		t.Fatalf("migrate related working view: %v", err)
	}
	for branch, wantBody := range map[string]string{
		mainBranch: "committed main body",
		peerBranch: "unpublished peer body",
	} {
		a3OnBranch(t, ctx, store.db, branch, func(conn *sql.Conn) {
			got := a3String(t, ctx, conn,
				"SELECT body FROM "+doltmemorymigration.PrototypeBeadsTable()+" WHERE legacy_key = 'working-view'")
			if got != wantBody {
				t.Errorf("canonical body on %s = %q, want %q", branch, got, wantBody)
			}
		})
	}
}

func TestMemoryMigrationA3Closure_BeforePublicationRollsBackAndResumes(t *testing.T) {
	store, cleanup := setupConcurrentTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()
	a3InstallFixture(t, ctx, store.db)
	a3SeedLegacy(t, ctx, store.db, "rollback", "preserve me", "a3 fixture: rollback")

	first, _ := doltmemorymigration.New(store.db, doltmemorymigration.Options{
		ProjectID: a3ProjectID,
		Author:    a3Author,
		OnEvent: func(_ context.Context, event doltmemorymigration.Event) error {
			if event.Point == doltmemorymigration.EventBranchPrepared {
				return errA3BeforePublish
			}
			return nil
		},
	})
	if _, err := first.Run(ctx); !errors.Is(err, errA3BeforePublish) {
		t.Fatalf("before-publication run error = %v", err)
	}
	if got := a3Count(t, ctx, store.db,
		"SELECT COUNT(*) FROM config WHERE `key` = ?", doltmemorymigration.LegacyPrefix()+"rollback"); got != 1 {
		t.Fatalf("legacy rows after rollback = %d, want 1", got)
	}
	if got := a3Count(t, ctx, store.db,
		"SELECT COUNT(*) FROM "+doltmemorymigration.PrototypeBeadsTable()); got != 0 {
		t.Fatalf("canonical rows after rollback = %d, want 0", got)
	}
	if got := a3Count(t, ctx, store.db,
		"SELECT COUNT(*) FROM dolt_status WHERE staged = 1"); got != 0 {
		t.Fatalf("staged rows after rollback = %d, want 0", got)
	}

	second, _ := doltmemorymigration.New(store.db, doltmemorymigration.Options{ProjectID: a3ProjectID, Author: a3Author})
	if _, err := second.Run(ctx); err != nil {
		t.Fatalf("resume before-publication fault: %v", err)
	}
	if got := a3Count(t, ctx, store.db,
		"SELECT COUNT(*) FROM "+doltmemorymigration.PrototypeBeadsTable()+" WHERE legacy_key = 'rollback'"); got != 1 {
		t.Fatalf("canonical rows after resume = %d, want 1", got)
	}
}

func TestMemoryMigrationA3Closure_UnrelatedDirtyConfigStopsBeforeMarker(t *testing.T) {
	store, cleanup := setupConcurrentTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()
	a3InstallFixture(t, ctx, store.db)
	a3SeedLegacy(t, ctx, store.db, "dirty-guard", "private memory body", "a3 fixture: dirty guard")
	if _, err := store.db.ExecContext(ctx,
		"UPDATE config SET value = 'unpublished ordinary value' WHERE `key` = 'issue_prefix'"); err != nil {
		t.Fatal(err)
	}

	coordinator, _ := doltmemorymigration.New(store.db, doltmemorymigration.Options{ProjectID: a3ProjectID, Author: a3Author})
	_, err := coordinator.Run(ctx)
	var preflight *doltmemorymigration.PreflightError
	if !errors.As(err, &preflight) {
		t.Fatalf("dirty-config error = %v, want typed preflight refusal", err)
	}
	if got := err.Error(); got == "" || strings.Contains(got, "private memory body") {
		t.Fatalf("dirty-config diagnostic leaked memory body: %q", got)
	}
	if _, found, inspectErr := doltmemorymigration.InspectControl(ctx, store.db); inspectErr != nil || found {
		t.Fatalf("dirty preflight installed marker: found=%v err=%v", found, inspectErr)
	}
	if got := a3Count(t, ctx, store.db,
		"SELECT COUNT(*) FROM config WHERE `key` = ?", doltmemorymigration.LegacyPrefix()+"dirty-guard"); got != 1 {
		t.Fatalf("legacy state after dirty preflight = %d row(s), want 1", got)
	}
	if got := a3Count(t, ctx, store.db,
		"SELECT COUNT(*) FROM "+doltmemorymigration.PrototypeBeadsTable()); got != 0 {
		t.Fatalf("canonical state after dirty preflight = %d row(s), want 0", got)
	}
}

func TestMemoryMigrationA3Closure_RefMutationIsOrderedAndLateLegacyViewFailsClosed(t *testing.T) {
	store, cleanup := setupConcurrentTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()
	a3InstallFixture(t, ctx, store.db)
	a3SeedLegacy(t, ctx, store.db, "late-ref", "old history", "a3 fixture: late ref source")
	oldHead := a3String(t, ctx, store.db, "SELECT DOLT_HASHOF('HEAD')")

	beforeFinalize := make(chan struct{})
	allowFinalize := make(chan struct{})
	coordinator, _ := doltmemorymigration.New(store.db, doltmemorymigration.Options{
		ProjectID: a3ProjectID,
		Author:    a3Author,
		OnEvent: func(ctx context.Context, event doltmemorymigration.Event) error {
			if event.Point == doltmemorymigration.EventBeforeFinalize {
				close(beforeFinalize)
				select {
				case <-allowFinalize:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		},
	})
	migrationDone := a3RunAsync(ctx, coordinator)
	a3WaitSignal(t, beforeFinalize, "migration before-finalize point")

	refStarted := make(chan struct{})
	refDone := make(chan error, 1)
	go func() {
		close(refStarted)
		refDone <- doltmemorymigration.RunRefMutation(ctx, store.db, func(ctx context.Context, conn *sql.Conn) error {
			return schema.DrainCall(ctx, conn, "CALL DOLT_BRANCH(?, ?)", "a3-late-legacy", oldHead)
		})
	}()
	a3WaitSignal(t, refStarted, "ref mutation attempt")
	select {
	case err := <-refDone:
		t.Fatalf("ref mutation escaped shared migration lock: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(allowFinalize)
	if out := a3WaitRun(t, migrationDone); out.err != nil {
		t.Fatalf("initial migration: %v", out.err)
	}
	select {
	case err := <-refDone:
		if err != nil {
			t.Fatalf("ordered late ref mutation: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ordered ref mutation did not run after migration released its lock")
	}
	var globalInProgress *doltmemorymigration.MigrationInProgressError
	if err := doltmemorymigration.CheckMemoryAccess(ctx, store.db); !errors.As(err, &globalInProgress) {
		t.Fatalf("canonical view gate after late legacy ref = %v, want global typed unavailable", err)
	}

	a3OnBranch(t, ctx, store.db, "a3-late-legacy", func(conn *sql.Conn) {
		err := doltmemorymigration.CheckMemoryAccess(ctx, conn)
		var inProgress *doltmemorymigration.MigrationInProgressError
		if !errors.As(err, &inProgress) {
			t.Fatalf("late legacy view gate = %v, want typed migration_in_progress", err)
		}
	})

	resume, _ := doltmemorymigration.New(store.db, doltmemorymigration.Options{ProjectID: a3ProjectID, Author: a3Author})
	if _, err := resume.Run(ctx); err != nil {
		t.Fatalf("converge late legacy view: %v", err)
	}
	a3OnBranch(t, ctx, store.db, "a3-late-legacy", func(conn *sql.Conn) {
		if err := doltmemorymigration.CheckMemoryAccess(ctx, conn); err != nil {
			t.Fatalf("late view after convergence: %v", err)
		}
		if got := a3Count(t, ctx, conn,
			"SELECT COUNT(*) FROM config WHERE `key` LIKE CONCAT(?, '%')", doltmemorymigration.LegacyPrefix()); got != 0 {
			t.Fatalf("late view legacy rows after convergence = %d", got)
		}
	})
}

func TestMemoryMigrationA3Closure_FinalAuditAbsorbsUncoordinatedRefRace(t *testing.T) {
	store, cleanup := setupConcurrentTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()
	a3InstallFixture(t, ctx, store.db)
	a3SeedLegacy(t, ctx, store.db, "final-race", "old ref body", "a3 fixture: final audit source")
	oldHead := a3String(t, ctx, store.db, "SELECT DOLT_HASHOF('HEAD')")

	var injectOnce sync.Once
	var injectErr error
	var completeMarkers atomic.Int32
	var gateDuringFirstComplete error
	coordinator, _ := doltmemorymigration.New(store.db, doltmemorymigration.Options{
		ProjectID: a3ProjectID,
		Author:    a3Author,
		OnEvent: func(ctx context.Context, event doltmemorymigration.Event) error {
			if event.Point == doltmemorymigration.EventCompleteCommitted && completeMarkers.Add(1) == 1 {
				gateDuringFirstComplete = doltmemorymigration.CheckMemoryAccess(ctx, store.db)
				return nil
			}
			if event.Point == doltmemorymigration.EventCompletePrepared {
				injectOnce.Do(func() {
					conn, err := store.db.Conn(ctx)
					if err != nil {
						injectErr = err
						return
					}
					defer conn.Close()
					injectErr = schema.DrainCall(ctx, conn,
						"CALL DOLT_BRANCH(?, ?)", "a3-final-audit-race", oldHead)
				})
			}
			return injectErr
		},
	})
	if _, err := coordinator.Run(ctx); err != nil {
		t.Fatalf("migration with final ref race: %v", err)
	}
	var inProgress *doltmemorymigration.MigrationInProgressError
	if !errors.As(gateDuringFirstComplete, &inProgress) {
		t.Fatalf("gate between first complete marker and audit = %v, want typed unavailable", gateDuringFirstComplete)
	}
	snapshot, found, err := doltmemorymigration.InspectControl(ctx, store.db)
	if err != nil || !found {
		t.Fatalf("inspect final control: found=%v err=%v", found, err)
	}
	if snapshot.Phase != doltmemorymigration.PhaseComplete || snapshot.Views != 2 || len(snapshot.Remaining) != 0 {
		t.Fatalf("final snapshot = %+v, want two complete views", snapshot)
	}
	a3OnBranch(t, ctx, store.db, "a3-final-audit-race", func(conn *sql.Conn) {
		if err := doltmemorymigration.CheckMemoryAccess(ctx, conn); err != nil {
			t.Fatalf("raced view was returned before convergence: %v", err)
		}
		if got := a3Count(t, ctx, conn,
			"SELECT COUNT(*) FROM config WHERE `key` LIKE CONCAT(?, '%')", doltmemorymigration.LegacyPrefix()); got != 0 {
			t.Fatalf("raced view legacy rows = %d, want 0", got)
		}
	})
}

func TestMemoryMigrationA3Closure_TeamServerRefusesWithAction(t *testing.T) {
	store, cleanup := setupConcurrentTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()
	a3InstallFixture(t, ctx, store.db)
	coordinator, _ := doltmemorymigration.New(store.db, doltmemorymigration.Options{
		ProjectID:     a3ProjectID,
		Author:        a3Author,
		ExternalOwner: "beads-team-server",
	})
	_, err := coordinator.Run(ctx)
	var ownerErr *doltmemorymigration.ExternalOwnerError
	if !errors.As(err, &ownerErr) {
		t.Fatalf("team-server run error = %v, want typed external owner refusal", err)
	}
	if _, found, inspectErr := doltmemorymigration.InspectControl(ctx, store.db); inspectErr != nil || found {
		t.Fatalf("team-server refusal mutated control: found=%v err=%v", found, inspectErr)
	}
}

type a3RunResult struct {
	result doltmemorymigration.Result
	err    error
}

func a3RunAsync(ctx context.Context, coordinator *doltmemorymigration.Coordinator) <-chan a3RunResult {
	done := make(chan a3RunResult, 1)
	go func() {
		result, err := coordinator.Run(ctx)
		done <- a3RunResult{result: result, err: err}
	}()
	return done
}

func a3WaitRun(t *testing.T, done <-chan a3RunResult) a3RunResult {
	t.Helper()
	select {
	case out := <-done:
		return out
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for A3 migration")
		return a3RunResult{}
	}
}

func a3WaitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func a3InstallFixture(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := doltmemorymigration.InstallPrototypeSchema(ctx, conn); err != nil {
		t.Fatalf("install A3 fixture: %v", err)
	}
	// setupConcurrentTestStore seeds issue_prefix as working state. Publish that
	// fixture-owned baseline so the migration tests do not accidentally ask the
	// conversion commit to sweep an unrelated dirty config row.
	if a3Count(t, ctx, conn,
		"SELECT COUNT(*) FROM dolt_diff('HEAD', 'WORKING', 'config')") != 0 {
		a3CommitOnConn(t, ctx, conn, "a3 fixture: publish config baseline")
	}
}

func a3SeedLegacy(t *testing.T, ctx context.Context, db *sql.DB, key, body, message string) {
	t.Helper()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx,
		"REPLACE INTO config (`key`, value) VALUES (?, ?)", doltmemorymigration.LegacyPrefix()+key, body); err != nil {
		t.Fatal(err)
	}
	if err := schema.DrainCall(ctx, conn, "CALL DOLT_ADD('config')"); err != nil {
		t.Fatal(err)
	}
	if err := schema.DrainCall(ctx, conn, "CALL DOLT_COMMIT('-m', ?, '--author', ?)", message, a3Author); err != nil {
		t.Fatal(err)
	}
}

func a3PrepareDivergentFixture(t *testing.T, ctx context.Context, db *sql.DB) (string, string) {
	t.Helper()
	a3InstallFixture(t, ctx, db)
	a3SeedLegacy(t, ctx, db, "shared", "ancestor", "a3 fixture: shared ancestor")
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var mainBranch string
	if err := conn.QueryRowContext(ctx, "SELECT active_branch()").Scan(&mainBranch); err != nil {
		t.Fatal(err)
	}
	peerBranch := "a3-peer"
	if err := schema.DrainCall(ctx, conn, "CALL DOLT_BRANCH(?, 'HEAD')", peerBranch); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx,
		"UPDATE config SET value = 'main body' WHERE `key` = ?", doltmemorymigration.LegacyPrefix()+"shared"); err != nil {
		t.Fatal(err)
	}
	a3CommitOnConn(t, ctx, conn, "a3 fixture: main divergence")
	if err := schema.DrainCall(ctx, conn, "CALL DOLT_CHECKOUT(?)", peerBranch); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx,
		"UPDATE config SET value = 'peer body' WHERE `key` = ?", doltmemorymigration.LegacyPrefix()+"shared"); err != nil {
		t.Fatal(err)
	}
	a3CommitOnConn(t, ctx, conn, "a3 fixture: peer divergence")
	if err := schema.DrainCall(ctx, conn, "CALL DOLT_CHECKOUT(?)", mainBranch); err != nil {
		t.Fatal(err)
	}
	return mainBranch, peerBranch
}

func a3CommitOnConn(t *testing.T, ctx context.Context, conn *sql.Conn, message string) {
	t.Helper()
	if err := schema.DrainCall(ctx, conn, "CALL DOLT_ADD('config')"); err != nil {
		t.Fatal(err)
	}
	if err := schema.DrainCall(ctx, conn, "CALL DOLT_COMMIT('-m', ?, '--author', ?)", message, a3Author); err != nil {
		t.Fatal(err)
	}
}

func a3OnBranch(t *testing.T, ctx context.Context, db *sql.DB, branch string, fn func(*sql.Conn)) {
	t.Helper()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var original string
	if err := conn.QueryRowContext(ctx, "SELECT active_branch()").Scan(&original); err != nil {
		t.Fatal(err)
	}
	if err := schema.DrainCall(ctx, conn, "CALL DOLT_CHECKOUT(?)", branch); err != nil {
		t.Fatalf("checkout %s: %v", branch, err)
	}
	defer func() {
		if err := schema.DrainCall(context.Background(), conn, "CALL DOLT_CHECKOUT(?)", original); err != nil {
			t.Errorf("restore branch %s: %v", original, err)
		}
	}()
	fn(conn)
}

func a3PrototypeTableCount(t *testing.T, ctx context.Context, conn *sql.Conn) int {
	t.Helper()
	return a3Count(t, ctx, conn, `
		SELECT COUNT(*)
		FROM information_schema.tables
		WHERE table_schema = DATABASE()
		  AND table_name IN (?, ?, ?)`,
		doltmemorymigration.PrototypeBeadsTable(),
		doltmemorymigration.PrototypeRevisionsTable(),
		doltmemorymigration.PrototypeLedgerTable())
}

func a3GuardedMemoryOperation(ctx context.Context, db doltmemorymigration.DBConn, mutate func()) error {
	if err := doltmemorymigration.CheckMemoryAccess(ctx, db); err != nil {
		return err
	}
	mutate()
	return nil
}

func a3Count(t *testing.T, ctx context.Context, db doltmemorymigration.DBConn, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return count
}

func a3String(t *testing.T, ctx context.Context, db doltmemorymigration.DBConn, query string, args ...any) string {
	t.Helper()
	var value string
	if err := db.QueryRowContext(ctx, query, args...).Scan(&value); err != nil {
		t.Fatalf("string query %q: %v", query, err)
	}
	return value
}

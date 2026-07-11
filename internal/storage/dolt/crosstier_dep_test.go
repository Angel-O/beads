package dolt

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// These tests pin the two-session transaction seam: DoltStore.RunInTransaction
// runs versioned tables on one SQL session (regularTx) and dolt-ignored wisp
// tables on another (ignoredTx). A dependency whose write table lives on one
// session and whose target issue lives on the other must still resolve targets
// created earlier in the same logical transaction — the shape `bd create
// --deps blocks:<other-tier-id>` produces since create+deps became one
// transaction. Regression coverage for the red-team finding where such creates
// hard-failed with "issue not found" on the Dolt server backend.

func crossTierRegularIssue(id, title string) *types.Issue {
	return &types.Issue{
		ID:        id,
		Title:     title,
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
	}
}

func crossTierWispIssue(id, title string) *types.Issue {
	iss := crossTierRegularIssue(id, title)
	iss.Ephemeral = true
	return iss
}

func assertCrossTierIsBlocked(ctx context.Context, t *testing.T, db *sql.DB, table, id string, want bool) {
	t.Helper()
	var got bool
	if err := db.QueryRowContext(ctx, "SELECT is_blocked FROM "+table+" WHERE id = ?", id).Scan(&got); err != nil {
		t.Fatalf("query %s.is_blocked for %s: %v", table, id, err)
	}
	if got != want {
		t.Fatalf("%s.is_blocked for %s = %v, want %v", table, id, got, want)
	}
}

func assertDepEdge(ctx context.Context, t *testing.T, store *DoltStore, sourceID, targetID string) {
	t.Helper()
	deps, err := store.GetDependencyRecords(ctx, sourceID)
	if err != nil {
		t.Fatalf("GetDependencyRecords(%s): %v", sourceID, err)
	}
	for _, d := range deps {
		if d.DependsOnID == targetID {
			return
		}
	}
	t.Fatalf("no dependency edge %s -> %s; records: %+v", sourceID, targetID, deps)
}

// New wisp created in the transaction blocks an existing regular issue
// (`bd create "blocker" --ephemeral --deps blocks:<regular-id>`): the edge
// writes to the regular tier while the target wisp is uncommitted on the
// ignored session.
func TestRunInTransactionNewWispBlocksExistingRegular(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	regular := crossTierRegularIssue("test-xtier-blocked-regular", "regular issue blocked by new wisp")
	if err := store.CreateIssue(ctx, regular, "tester"); err != nil {
		t.Fatalf("CreateIssue regular: %v", err)
	}

	wisp := crossTierWispIssue("test-xtier-new-wisp-blocker", "new wisp blocking a regular issue")
	if err := store.RunInTransaction(ctx, "test: create wisp blocking regular", func(tx storage.Transaction) error {
		if err := tx.CreateIssue(ctx, wisp, "tester"); err != nil {
			return err
		}
		return tx.AddDependency(ctx, &types.Dependency{
			IssueID:     regular.ID,
			DependsOnID: wisp.ID,
			Type:        types.DepBlocks,
		}, "tester")
	}); err != nil {
		t.Fatalf("RunInTransaction create wisp + blocks edge: %v", err)
	}

	assertWispCount(ctx, t, store.db, wisp.ID, 1)
	assertDepEdge(ctx, t, store, regular.ID, wisp.ID)
	assertCrossTierIsBlocked(ctx, t, store.db, "issues", regular.ID, true)
}

// New regular issue created in the transaction blocks an existing wisp
// (`bd create "blocker" --deps blocks:<wisp-id>`): the edge writes to the
// ignored tier while the target regular issue is uncommitted on the regular
// session.
func TestRunInTransactionNewRegularBlocksExistingWisp(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	wisp := crossTierWispIssue("test-xtier-blocked-wisp", "wisp blocked by new regular issue")
	if err := store.CreateIssue(ctx, wisp, "tester"); err != nil {
		t.Fatalf("CreateIssue wisp: %v", err)
	}

	regular := crossTierRegularIssue("test-xtier-new-regular-blocker", "new regular issue blocking a wisp")
	if err := store.RunInTransaction(ctx, "test: create regular blocking wisp", func(tx storage.Transaction) error {
		if err := tx.CreateIssue(ctx, regular, "tester"); err != nil {
			return err
		}
		return tx.AddDependency(ctx, &types.Dependency{
			IssueID:     wisp.ID,
			DependsOnID: regular.ID,
			Type:        types.DepBlocks,
		}, "tester")
	}); err != nil {
		t.Fatalf("RunInTransaction create regular + blocks edge: %v", err)
	}

	assertIssueCount(ctx, t, store.db, regular.ID, 1)
	assertDepEdge(ctx, t, store, wisp.ID, regular.ID)
	assertCrossTierIsBlocked(ctx, t, store.db, "wisps", wisp.ID, true)
}

// A closed cross-tier blocker must not mark the source blocked: the openness
// gate has to consult the target's own session, not just existence.
func TestRunInTransactionClosedCrossTierBlockerDoesNotBlock(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	wisp := crossTierWispIssue("test-xtier-closed-blocked-wisp", "wisp with already-closed regular blocker")
	if err := store.CreateIssue(ctx, wisp, "tester"); err != nil {
		t.Fatalf("CreateIssue wisp: %v", err)
	}

	regular := crossTierRegularIssue("test-xtier-closed-regular-blocker", "closed regular issue blocking a wisp")
	regular.Status = types.StatusClosed
	if err := store.RunInTransaction(ctx, "test: create closed regular blocking wisp", func(tx storage.Transaction) error {
		if err := tx.CreateIssue(ctx, regular, "tester"); err != nil {
			return err
		}
		return tx.AddDependency(ctx, &types.Dependency{
			IssueID:     wisp.ID,
			DependsOnID: regular.ID,
			Type:        types.DepBlocks,
		}, "tester")
	}); err != nil {
		t.Fatalf("RunInTransaction create closed regular + blocks edge: %v", err)
	}

	assertDepEdge(ctx, t, store, wisp.ID, regular.ID)
	assertCrossTierIsBlocked(ctx, t, store.db, "wisps", wisp.ID, false)
}

// A cross-tier dependency on a target that exists in neither tier must still
// fail with the standard not-found error and roll back the whole transaction.
func TestRunInTransactionCrossTierDepMissingTargetRollsBack(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	wisp := crossTierWispIssue("test-xtier-missing-target-wisp", "wisp whose dep target is missing")
	err := store.RunInTransaction(ctx, "test: wisp dep on missing target", func(tx storage.Transaction) error {
		if err := tx.CreateIssue(ctx, wisp, "tester"); err != nil {
			return err
		}
		return tx.AddDependency(ctx, &types.Dependency{
			IssueID:     wisp.ID,
			DependsOnID: "test-xtier-does-not-exist",
			Type:        types.DepBlocks,
		}, "tester")
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("RunInTransaction error = %v, want target not-found", err)
	}
	assertWispCount(ctx, t, store.db, wisp.ID, 0)
}

// A blocking cycle closed across the two tiers inside one transaction must be
// rejected even though each session sees only its own uncommitted edges.
func TestRunInTransactionCrossTierCycleRejected(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	regular := crossTierRegularIssue("test-xtier-cycle-regular", "regular issue in cross-tier cycle")
	if err := store.CreateIssue(ctx, regular, "tester"); err != nil {
		t.Fatalf("CreateIssue regular: %v", err)
	}

	wisp := crossTierWispIssue("test-xtier-cycle-wisp", "wisp in cross-tier cycle")
	err := store.RunInTransaction(ctx, "test: cross-tier blocking cycle", func(tx storage.Transaction) error {
		if err := tx.CreateIssue(ctx, wisp, "tester"); err != nil {
			return err
		}
		if err := tx.AddDependency(ctx, &types.Dependency{
			IssueID:     wisp.ID,
			DependsOnID: regular.ID,
			Type:        types.DepBlocks,
		}, "tester"); err != nil {
			return err
		}
		return tx.AddDependency(ctx, &types.Dependency{
			IssueID:     regular.ID,
			DependsOnID: wisp.ID,
			Type:        types.DepBlocks,
		}, "tester")
	})
	if err == nil || !strings.Contains(err.Error(), "would create a cycle") {
		t.Fatalf("RunInTransaction error = %v, want cycle rejection", err)
	}
	assertWispCount(ctx, t, store.db, wisp.ID, 0)
	if deps, derr := store.GetDependencyRecords(ctx, regular.ID); derr != nil || len(deps) != 0 {
		t.Fatalf("regular issue dependency records after rollback = %v (err %v), want none", deps, derr)
	}
}

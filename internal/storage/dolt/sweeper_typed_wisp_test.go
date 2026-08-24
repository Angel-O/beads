package dolt

import (
	"context"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/issueops"
)

// typedWispClosedAt / typedWispCutoff mirror the conformance kit's fixed
// stamps: pinned rather than offsets from time.Now so the cutoff never races
// the test's own clock.
var (
	typedWispClosedAt = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	typedWispCutoff   = time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
)

// TestSweepTreatsALegacyTypedWispAsEphemeralTier pins the tier boundary on the
// row shape that motivated it: a closed row in the ISSUES plane carrying a
// wisp_type but not the ephemeral flag — the shape older creators minted
// (wisp_type set, ephemeral left 0), 858 of which survived every purge in one
// production DB because the candidate filter matched the flag alone.
//
// `bd purge` must take it; `bd prune` must not — a typed wisp is
// ephemeral-tier however it was minted. The durable sibling proves the tier
// boundary moved rather than disappeared.
//
// The legacy shape is manufactured with a direct UPDATE because every current
// create path infers ephemeral from wisp_type (that inference is the other
// half of this fix, TestCreateIssueInfersEphemeralFromWispType below); the
// rows this test guards were minted before the inference existed.
func TestSweepTreatsALegacyTypedWispAsEphemeralTier(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	legacy := typedWispSeedClosedIssue(t, ctx, store, "twp-legacy-1")
	durable := typedWispSeedClosedIssue(t, ctx, store, "twp-durable-1")
	if _, err := store.db.ExecContext(ctx,
		"UPDATE issues SET wisp_type = 'patrol' WHERE id = ?", legacy); err != nil {
		t.Fatalf("manufacturing the legacy typed-wisp shape: %v", err)
	}

	sweeper, err := store.Sweeper()
	if err != nil {
		t.Fatalf("Sweeper(): %v", err)
	}
	cutoff := typedWispCutoff

	// The ephemeral tier owns the typed wisp, flag or no flag.
	result, err := sweeper.Sweep(ctx, issueops.SweepRequest{
		Tier:         issueops.SweepEphemeral,
		IDPattern:    "twp-*",
		ClosedBefore: &cutoff,
	})
	if err != nil {
		t.Fatalf("ephemeral sweep: %v", err)
	}
	if result.Swept != 1 {
		t.Fatalf("ephemeral sweep Swept = %d, want 1 (the legacy typed wisp)", result.Swept)
	}
	typedWispAssertIssueRows(t, ctx, store, 0, legacy)
	typedWispAssertIssueRows(t, ctx, store, 1, durable)

	// The durable tier still owns the plain row — the boundary moved, it did
	// not collapse into "purge takes everything".
	result, err = sweeper.Sweep(ctx, issueops.SweepRequest{
		Tier:         issueops.SweepDurable,
		IDPattern:    "twp-*",
		ClosedBefore: &cutoff,
	})
	if err != nil {
		t.Fatalf("durable sweep: %v", err)
	}
	if result.Swept != 1 {
		t.Fatalf("durable sweep Swept = %d, want 1 (the plain closed issue)", result.Swept)
	}
	typedWispAssertIssueRows(t, ctx, store, 0, durable)
}

// TestSweepLeavesNoHistoryBeadsToTheDurableTier pins the other edge of the
// same boundary: a NoHistory bead lives in the WISPS plane with ephemeral=0
// and no wisp_type, and it is durable-tier (GH#3649) — widening purge to
// typed wisps must not sweep it up.
func TestSweepLeavesNoHistoryBeadsToTheDurableTier(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	closedAt := typedWispClosedAt
	noHistory := &types.Issue{
		ID:        "twp-nohist-1",
		Title:     "no-history bead",
		Status:    types.StatusClosed,
		Priority:  2,
		IssueType: types.TypeTask,
		NoHistory: true,
		ClosedAt:  &closedAt,
	}
	if err := store.CreateIssue(ctx, noHistory, "typed-wisp-seed"); err != nil {
		t.Fatalf("seeding no-history bead: %v", err)
	}

	sweeper, err := store.Sweeper()
	if err != nil {
		t.Fatalf("Sweeper(): %v", err)
	}
	cutoff := typedWispCutoff

	result, err := sweeper.Sweep(ctx, issueops.SweepRequest{
		Tier:         issueops.SweepEphemeral,
		IDPattern:    "twp-*",
		ClosedBefore: &cutoff,
	})
	if err != nil {
		t.Fatalf("ephemeral sweep: %v", err)
	}
	if result.Swept != 0 {
		t.Fatalf("ephemeral sweep Swept = %d, want 0 — a NoHistory bead is durable-tier", result.Swept)
	}
	typedWispAssertWispRows(t, ctx, store, 1, noHistory.ID)
}

// TestCreateIssueInfersEphemeralFromWispType pins the mint-side half of the
// fix: a wisp_type is a claim of ephemerality, so a create that carries one
// without the flag still lands in the wisps plane marked ephemeral, instead
// of minting the very shape the legacy test above has to manufacture by hand.
func TestCreateIssueInfersEphemeralFromWispType(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	issue := &types.Issue{
		ID:        "twp-minted-1",
		Title:     "typed wisp minted without the flag",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		WispType:  types.WispTypePatrol,
	}
	if err := store.CreateIssue(ctx, issue, "typed-wisp-seed"); err != nil {
		t.Fatalf("creating typed wisp: %v", err)
	}
	if !issue.Ephemeral {
		t.Errorf("issue.Ephemeral = false after create, want true — wisp_type implies the ephemeral tier")
	}
	typedWispAssertWispRows(t, ctx, store, 1, issue.ID)
	typedWispAssertIssueRows(t, ctx, store, 0, issue.ID)

	var ephemeral int
	if err := store.db.QueryRowContext(ctx,
		"SELECT ephemeral FROM wisps WHERE id = ?", issue.ID).Scan(&ephemeral); err != nil {
		t.Fatalf("reading minted wisp row: %v", err)
	}
	if ephemeral != 1 {
		t.Errorf("wisps.ephemeral = %d, want 1", ephemeral)
	}
}

func typedWispSeedClosedIssue(t *testing.T, ctx context.Context, store *DoltStore, id string) string {
	t.Helper()
	closedAt := typedWispClosedAt
	issue := &types.Issue{
		ID:        id,
		Title:     "typed-wisp case " + id,
		Status:    types.StatusClosed,
		Priority:  2,
		IssueType: types.TypeTask,
		ClosedAt:  &closedAt,
	}
	if err := store.CreateIssue(ctx, issue, "typed-wisp-seed"); err != nil {
		t.Fatalf("seeding %s: %v", id, err)
	}
	return issue.ID
}

func typedWispAssertIssueRows(t *testing.T, ctx context.Context, store *DoltStore, want int, id string) {
	t.Helper()
	typedWispAssertRows(t, ctx, store, "issues", want, id)
}

func typedWispAssertWispRows(t *testing.T, ctx context.Context, store *DoltStore, want int, id string) {
	t.Helper()
	typedWispAssertRows(t, ctx, store, "wisps", want, id)
}

func typedWispAssertRows(t *testing.T, ctx context.Context, store *DoltStore, table string, want int, id string) {
	t.Helper()
	var got int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+table+" WHERE id = ?", id).Scan(&got); err != nil {
		t.Fatalf("counting %s rows for %s: %v", table, id, err)
	}
	if got != want {
		t.Errorf("%s rows for %s = %d, want %d", table, id, got, want)
	}
}

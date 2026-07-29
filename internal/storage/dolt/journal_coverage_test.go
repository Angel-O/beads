package dolt

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

var (
	journalRefMu sync.Mutex
	journalRefs  int
)

// enableJournalForTest turns the process-global events journal on for a test
// and keeps it on until the last concurrent journal test finishes. The dolt
// suite runs tests in parallel (setupTestStore calls t.Parallel), so a plain
// enable/disable pair would let one journal test's cleanup flip the flag off
// underneath a still-running sibling. Reference counting keeps the flag stable
// across the overlapping journal tests; non-journal tests are unaffected because
// the journal table is dolt_ignored and separate from versioned data.
func enableJournalForTest(t *testing.T) {
	t.Helper()
	journalRefMu.Lock()
	journalRefs++
	issueops.SetJournalEnabled(true)
	journalRefMu.Unlock()
	t.Cleanup(func() {
		journalRefMu.Lock()
		journalRefs--
		if journalRefs == 0 {
			issueops.SetJournalEnabled(false)
		}
		journalRefMu.Unlock()
	})
}

// jrow is one journal row read back in seq order.
type jrow struct {
	seq int64
	op  string
	id  string
}

func readJournalRows(t *testing.T, store *DoltStore) []jrow {
	t.Helper()
	rows, err := store.db.QueryContext(context.Background(),
		`SELECT seq, op, issue_id FROM bd_events_journal ORDER BY seq ASC`)
	if err != nil {
		t.Fatalf("query journal: %v", err)
	}
	defer rows.Close()
	var out []jrow
	for rows.Next() {
		var r jrow
		if err := rows.Scan(&r.seq, &r.op, &r.id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func clearJournal(t *testing.T, store *DoltStore) {
	t.Helper()
	if _, err := store.db.ExecContext(context.Background(), "DELETE FROM bd_events_journal"); err != nil {
		t.Fatalf("clear journal: %v", err)
	}
}

// hasOpFor reports whether the journal holds a row with the given op for id.
func hasOpFor(rows []jrow, op, id string) bool {
	for _, r := range rows {
		if r.op == op && r.id == id {
			return true
		}
	}
	return false
}

// TestEventsJournal_SeamEntryPoints drives the mutation entry points the
// earlier op-by-op tests do not — rename, wisps, reopen, ready-claim, lease
// reclaim, by-source-repo bulk delete, and creation-time dependencies — through
// the real DoltStore and asserts each lands in the journal at the issueops seam.
func TestEventsJournal_SeamEntryPoints(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	enableJournalForTest(t)

	mk := func(id string) *types.Issue {
		return &types.Issue{ID: id, Title: "t-" + id, IssueType: types.TypeTask, Status: types.StatusOpen}
	}

	t.Run("rename", func(t *testing.T) {
		if err := store.CreateIssue(ctx, mk("bd-rn-1"), "actor"); err != nil {
			t.Fatalf("create: %v", err)
		}
		iss, err := store.GetIssue(ctx, "bd-rn-1")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		clearJournal(t, store)
		if err := store.UpdateIssueID(ctx, "bd-rn-1", "bd-rn-2", iss, "actor"); err != nil {
			t.Fatalf("rename: %v", err)
		}
		if rows := readJournalRows(t, store); !hasOpFor(rows, "update", "bd-rn-2") {
			t.Fatalf("rename must journal an update for the new id; got %+v", rows)
		}
	})

	t.Run("wisp create/update/close", func(t *testing.T) {
		clearJournal(t, store)
		w := &types.Issue{Title: "wisp work", IssueType: types.TypeTask, Status: types.StatusOpen, Ephemeral: true}
		if err := store.CreateIssue(ctx, w, "actor"); err != nil {
			t.Fatalf("create wisp: %v", err)
		}
		if err := store.UpdateIssue(ctx, w.ID, map[string]interface{}{"title": "wisp renamed"}, "actor"); err != nil {
			t.Fatalf("update wisp: %v", err)
		}
		if err := store.CloseIssue(ctx, w.ID, "done", "actor", ""); err != nil {
			t.Fatalf("close wisp: %v", err)
		}
		rows := readJournalRows(t, store)
		for _, op := range []string{"create", "update", "close"} {
			if !hasOpFor(rows, op, w.ID) {
				t.Fatalf("wisp must journal %q for %s; got %+v", op, w.ID, rows)
			}
		}
	})

	t.Run("reopen", func(t *testing.T) {
		if err := store.CreateIssue(ctx, mk("bd-ro-1"), "actor"); err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := store.CloseIssue(ctx, "bd-ro-1", "done", "actor", ""); err != nil {
			t.Fatalf("close: %v", err)
		}
		clearJournal(t, store)
		if err := store.ReopenIssue(ctx, "bd-ro-1", "back", "actor"); err != nil {
			t.Fatalf("reopen: %v", err)
		}
		if rows := readJournalRows(t, store); !hasOpFor(rows, "update", "bd-ro-1") {
			t.Fatalf("reopen must journal an update; got %+v", rows)
		}
	})

	t.Run("ready-claim", func(t *testing.T) {
		if err := store.CreateIssue(ctx, mk("bd-rc-1"), "actor"); err != nil {
			t.Fatalf("create: %v", err)
		}
		clearJournal(t, store)
		claimed, err := store.ClaimReadyIssue(ctx, types.WorkFilter{}, "worker")
		if err != nil {
			t.Fatalf("claim-ready: %v", err)
		}
		if claimed == nil {
			t.Fatalf("claim-ready returned no issue")
		}
		if rows := readJournalRows(t, store); !hasOpFor(rows, "update", claimed.ID) {
			t.Fatalf("ready-claim must journal an update for %s; got %+v", claimed.ID, rows)
		}
	})

	t.Run("lease reclaim", func(t *testing.T) {
		if err := store.CreateIssue(ctx, mk("bd-lease-1"), "actor"); err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := store.ClaimIssue(ctx, "bd-lease-1", "worker"); err != nil {
			t.Fatalf("claim: %v", err)
		}
		clearJournal(t, store)
		// Negative olderThan pushes the cutoff into the future so the fresh lease
		// counts as expired and is reclaimed deterministically.
		reclaimed, err := store.ReclaimExpiredLeases(ctx, -24*time.Hour, "reaper")
		if err != nil {
			t.Fatalf("reclaim: %v", err)
		}
		if len(reclaimed) == 0 {
			t.Fatalf("expected at least one reclaimed lease")
		}
		if rows := readJournalRows(t, store); !hasOpFor(rows, "update", reclaimed[0].ID) {
			t.Fatalf("lease reclaim must journal an update for %s; got %+v", reclaimed[0].ID, rows)
		}
	})

	t.Run("delete by source repo", func(t *testing.T) {
		if err := store.CreateIssue(ctx, mk("bd-sr-1"), "actor"); err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := store.CreateIssue(ctx, mk("bd-sr-2"), "actor"); err != nil {
			t.Fatalf("create: %v", err)
		}
		// source_repo is internal routing state not set on the create path here;
		// stamp it directly so the bulk delete has a repo to match.
		if _, err := store.db.ExecContext(ctx,
			"UPDATE issues SET source_repo = 'repo-z' WHERE id IN ('bd-sr-1','bd-sr-2')"); err != nil {
			t.Fatalf("stamp source_repo: %v", err)
		}
		clearJournal(t, store)
		n, err := store.DeleteIssuesBySourceRepo(ctx, "repo-z")
		if err != nil {
			t.Fatalf("delete by source repo: %v", err)
		}
		if n != 2 {
			t.Fatalf("expected 2 deleted, got %d", n)
		}
		rows := readJournalRows(t, store)
		if !hasOpFor(rows, "delete", "bd-sr-1") || !hasOpFor(rows, "delete", "bd-sr-2") {
			t.Fatalf("by-source-repo bulk delete must journal each row; got %+v", rows)
		}
	})

	t.Run("creation-time dependency", func(t *testing.T) {
		if err := store.CreateIssue(ctx, mk("test-ct-target"), "actor"); err != nil {
			t.Fatalf("create target: %v", err)
		}
		clearJournal(t, store)
		dep := mk("test-ct-dep")
		dep.Dependencies = []*types.Dependency{{
			IssueID: "test-ct-dep", DependsOnID: "test-ct-target", Type: types.DepBlocks,
		}}
		if err := store.CreateIssues(ctx, []*types.Issue{dep}, "actor"); err != nil {
			t.Fatalf("create with dep: %v", err)
		}
		rows := readJournalRows(t, store)
		if !hasOpFor(rows, "create", "test-ct-dep") {
			t.Fatalf("create must journal; got %+v", rows)
		}
		if !hasOpFor(rows, "dep_add", "test-ct-dep") {
			t.Fatalf("creation-time dependency must journal a dep_add for the source; got %+v", rows)
		}
	})
}

// TestEventsJournalAccessorServerStore guards the server-mode DoltStore's
// EventsJournalAccessor (the read/prune capability the `bd events` CLI uses
// against a Dolt SQL server), mirroring the embedded-store guard in embeddeddolt.
func TestEventsJournalAccessorServerStore(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	enableJournalForTest(t)
	clearJournal(t, store)

	mk := func(id string) *types.Issue {
		return &types.Issue{ID: id, Title: "t-" + id, IssueType: types.TypeTask, Status: types.StatusOpen}
	}
	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	must(store.CreateIssue(ctx, mk("jrn-a1"), "actor"), "create 1")
	must(store.CreateIssue(ctx, mk("jrn-a2"), "actor"), "create 2")
	must(store.UpdateIssue(ctx, "jrn-a1", map[string]interface{}{"title": "renamed"}, "actor"), "update")
	must(store.CloseIssue(ctx, "jrn-a1", "done", "actor", ""), "close")
	must(store.DeleteIssue(ctx, "jrn-a2"), "delete")

	rows, err := store.ReadEventsJournal(ctx, 0, 0)
	must(err, "read all")
	wantOps := []string{"create", "create", "update", "close", "delete"}
	if len(rows) != len(wantOps) {
		t.Fatalf("read %d rows, want %d: %+v", len(rows), len(wantOps), rows)
	}
	for i, w := range wantOps {
		if rows[i].Op != w {
			t.Errorf("row %d op = %q, want %q", i, rows[i].Op, w)
		}
	}
	// retain-rows floor keeps the newest two rows despite a wide --before.
	n, err := store.PruneEventsJournal(ctx, 1_000_000, 0, 2)
	must(err, "prune retain-rows")
	if n != 3 {
		t.Fatalf("prune retain-rows=2 deleted %d, want 3", n)
	}
	after, err := store.ReadEventsJournal(ctx, 0, 0)
	must(err, "read after")
	if len(after) != 2 {
		t.Fatalf("after prune %d rows, want 2", len(after))
	}
}

// TestEventsJournal_ReplayFromZero proves the journal is a complete, ordered,
// replayable record: applying every row in seq order (set snapshot on
// create/update/close, drop on delete) reconstructs exactly the store's final
// live set.
func TestEventsJournal_ReplayFromZero(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	enableJournalForTest(t)
	clearJournal(t, store)

	mk := func(id string) *types.Issue {
		return &types.Issue{ID: id, Title: "t-" + id, IssueType: types.TypeTask, Status: types.StatusOpen}
	}
	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	must(store.CreateIssue(ctx, mk("bd-rp-1"), "a"), "create 1")
	must(store.CreateIssue(ctx, mk("bd-rp-2"), "a"), "create 2")
	must(store.CreateIssue(ctx, mk("bd-rp-3"), "a"), "create 3")
	must(store.UpdateIssue(ctx, "bd-rp-1", map[string]interface{}{"title": "renamed"}, "a"), "update")
	must(store.CloseIssue(ctx, "bd-rp-2", "done", "a", ""), "close")
	must(store.DeleteIssue(ctx, "bd-rp-3"), "delete")

	// Replay: reconstruct the live id set from the journal snapshots.
	rows, err := store.db.QueryContext(ctx,
		`SELECT op, issue_id FROM bd_events_journal ORDER BY seq ASC`)
	must(err, "read journal")
	defer rows.Close()
	live := map[string]bool{}
	for rows.Next() {
		var op, id string
		must(rows.Scan(&op, &id), "scan")
		switch op {
		case "delete":
			delete(live, id)
		case "create", "update", "close":
			live[id] = true
		}
	}
	must(rows.Err(), "rows err")

	got := make([]string, 0, len(live))
	for id := range live {
		got = append(got, id)
	}
	sort.Strings(got)
	// The store's actual surviving set: rp-1 (updated) and rp-2 (closed but still
	// a row); rp-3 was deleted.
	want := []string{"bd-rp-1", "bd-rp-2"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("replayed live set %v, want %v", got, want)
	}
	for _, id := range want {
		if _, err := store.GetIssue(ctx, id); err != nil {
			t.Fatalf("replayed id %s should still exist in the store: %v", id, err)
		}
	}
	if _, err := store.GetIssue(ctx, "bd-rp-3"); err == nil {
		t.Fatalf("bd-rp-3 was deleted; replay must not resurrect it")
	}
}

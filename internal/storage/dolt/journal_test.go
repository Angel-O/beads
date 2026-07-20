package dolt

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestMutationsJournal_EmbeddedPlumbing drives mutations through the DoltStore
// (the embedded/DoltStorage write plumbing, which bottoms out in the issueops
// *InTx functions) against real Dolt and asserts the journal at the issueops
// seam records each op with an engine-assigned monotonic seq. This is the second
// of the two plumbings; the first is exercised in domain/db.
func TestMutationsJournal_EmbeddedPlumbing(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	enableJournalForTest(t)
	if _, err := store.db.ExecContext(ctx, "DELETE FROM bd_mutations_journal"); err != nil {
		t.Fatalf("clear journal: %v", err)
	}

	mk := func(id string) *types.Issue {
		return &types.Issue{ID: id, Title: "t-" + id, IssueType: types.TypeTask, Status: types.StatusOpen}
	}
	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}

	must(store.CreateIssue(ctx, mk("bd-emb-1"), "actor"), "create 1")
	must(store.CreateIssue(ctx, mk("bd-emb-2"), "actor"), "create 2")
	must(store.UpdateIssue(ctx, "bd-emb-1", map[string]interface{}{"title": "renamed"}, "actor"), "update")
	must(store.AddLabel(ctx, "bd-emb-1", "urgent", "actor"), "add label")
	must(store.ClaimIssue(ctx, "bd-emb-1", "worker"), "claim")
	must(store.AddDependency(ctx, &types.Dependency{IssueID: "bd-emb-1", DependsOnID: "bd-emb-2", Type: types.DepBlocks}, "actor"), "add dep")
	must(store.RemoveDependency(ctx, "bd-emb-1", "bd-emb-2", "actor"), "remove dep")
	must(store.CloseIssue(ctx, "bd-emb-1", "done", "actor", ""), "close")
	must(store.DeleteIssue(ctx, "bd-emb-2"), "delete")

	type row struct {
		seq int64
		op  string
		id  string
	}
	rows, err := store.db.QueryContext(ctx,
		`SELECT seq, op, issue_id FROM bd_mutations_journal ORDER BY seq ASC`)
	must(err, "query journal")
	defer rows.Close()

	var got []row
	for rows.Next() {
		var r row
		must(rows.Scan(&r.seq, &r.op, &r.id), "scan")
		got = append(got, r)
	}
	must(rows.Err(), "rows err")

	wantOps := []string{"create", "create", "update", "update", "update", "dep_add", "dep_remove", "close", "delete"}
	if len(got) != len(wantOps) {
		t.Fatalf("expected %d journal rows, got %d: %+v", len(wantOps), len(got), got)
	}
	var prev int64
	for i, want := range wantOps {
		if got[i].op != want {
			t.Fatalf("row %d: op %q, want %q (%+v)", i, got[i].op, want, got)
		}
		if got[i].seq <= prev {
			t.Fatalf("row %d: seq %d not strictly greater than prev %d", i, got[i].seq, prev)
		}
		prev = got[i].seq
	}
}

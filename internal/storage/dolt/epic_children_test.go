package dolt

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

func TestGetEpicChildrenMatchesCloseGateRows(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const parent = "ecc-parent"
	createPerm(t, ctx, store, parent)

	for _, id := range []string{"ecc-durable-open", "ecc-durable-closed"} {
		createPerm(t, ctx, store, id)
		if err := store.AddDependency(ctx, &types.Dependency{
			IssueID: id, DependsOnID: parent, Type: types.DepParentChild,
		}, "tester"); err != nil {
			t.Fatalf("add durable child %s: %v", id, err)
		}
	}
	for _, id := range []string{"ecc-wisp-open", "ecc-wisp-closed"} {
		createWisp(t, ctx, store, id)
		if err := store.AddDependency(ctx, &types.Dependency{
			IssueID: id, DependsOnID: parent, Type: types.DepParentChild,
		}, "tester"); err != nil {
			t.Fatalf("add wisp child %s: %v", id, err)
		}
	}
	for _, id := range []string{"ecc-durable-closed", "ecc-wisp-closed"} {
		if err := store.CloseIssue(ctx, id, "done", "tester", ""); err != nil {
			t.Fatalf("close child %s: %v", id, err)
		}
	}

	// Give a wisp edge the same row ID as a durable edge. The close gate counts
	// the durable row and excludes this wisp copy, even though its child differs.
	var durableDependencyID string
	if err := store.db.QueryRowContext(ctx,
		"SELECT id FROM dependencies WHERE issue_id = ?", "ecc-durable-open").Scan(&durableDependencyID); err != nil {
		t.Fatalf("read durable dependency id: %v", err)
	}
	createWisp(t, ctx, store, "ecc-duplicate-wisp")
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID: "ecc-duplicate-wisp", DependsOnID: parent, Type: types.DepParentChild,
	}, "tester"); err != nil {
		t.Fatalf("add duplicate wisp child: %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		"UPDATE wisp_dependencies SET id = ? WHERE issue_id = ?", durableDependencyID, "ecc-duplicate-wisp"); err != nil {
		t.Fatalf("duplicate wisp dependency id: %v", err)
	}

	// A durable parent wins target routing when an anomalous wisp twin exists.
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin wisp twin transaction: %v", err)
	}
	if err := issueops.InsertIssueStrictInTx(ctx, tx, "wisps", &types.Issue{
		ID: parent, Title: "wisp twin", Status: types.StatusOpen, Priority: 2,
		IssueType: types.TypeEpic, Ephemeral: true,
	}); err != nil {
		_ = tx.Rollback()
		t.Fatalf("insert wisp parent twin: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit wisp parent twin: %v", err)
	}

	got, err := store.GetEpicChildren(ctx, parent)
	if err != nil {
		t.Fatalf("GetEpicChildren: %v", err)
	}
	want := []struct {
		id, status, issueType, plane string
	}{
		{"ecc-durable-closed", "closed", "task", "durable"},
		{"ecc-durable-open", "open", "task", "durable"},
		{"ecc-wisp-closed", "closed", "task", "wisp"},
		{"ecc-wisp-open", "open", "task", "wisp"},
	}
	if len(got) != len(want) {
		t.Fatalf("GetEpicChildren returned %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i, want := range want {
		if got[i].ID != want.id || string(got[i].Status) != want.status || string(got[i].IssueType) != want.issueType || got[i].StoragePlane != want.plane {
			t.Errorf("child[%d] = %+v, want id=%q status=%q type=%q plane=%q", i, got[i], want.id, want.status, want.issueType, want.plane)
		}
	}
}

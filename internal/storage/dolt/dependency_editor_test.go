package dolt

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	storageissueops "github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

func TestDoltDependencyEditorReconcilesEpicContextsAtomically(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	epic := &types.Issue{ID: "test-context-epic", Title: "epic", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic}
	childA := &types.Issue{ID: "test-context-child-a", Title: "child a", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	childB := &types.Issue{ID: "test-context-child-b", Title: "child b", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	childDuplicate := &types.Issue{ID: "test-context-child-duplicate", Title: "duplicate context child", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	grandchild := &types.Issue{ID: "test-context-grandchild", Title: "grandchild", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	invalid := &types.Issue{ID: "test-context-invalid", Title: "invalid", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic}
	if err := store.CreateIssues(ctx, []*types.Issue{epic, childA, childB, childDuplicate, grandchild, invalid}, "seed"); err != nil {
		t.Fatalf("CreateIssues: %v", err)
	}
	for id, label := range map[string]string{
		epic.ID:           "ordinary",
		childA.ID:         "ctx:z",
		childB.ID:         "ctx:a",
		childDuplicate.ID: "ctx:a",
		grandchild.ID:     "ctx:grandchild",
		invalid.ID:        "ctx:invalid",
	} {
		if err := store.AddLabel(ctx, id, label, "seed"); err != nil {
			t.Fatalf("AddLabel(%s, %s): %v", id, label, err)
		}
	}
	editor, err := store.DependencyEditor()
	if err != nil {
		t.Fatalf("DependencyEditor: %v", err)
	}
	add := func(edges ...publicops.DependencyEdge) {
		t.Helper()
		if _, err := editor.AddDependencies(ctx, publicops.AddDependenciesRequest{
			Actor:                 "writer",
			Edges:                 edges,
			ReconcileEpicContexts: true,
		}); err != nil {
			t.Fatalf("AddDependencies: %v", err)
		}
	}
	addWithoutReconcile := func(edges ...publicops.DependencyEdge) {
		t.Helper()
		if _, err := editor.AddDependencies(ctx, publicops.AddDependenciesRequest{Actor: "writer", Edges: edges}); err != nil {
			t.Fatalf("AddDependencies without reconciliation: %v", err)
		}
	}
	addWithoutReconcile(publicops.DependencyEdge{IssueID: grandchild.ID, DependsOnID: childA.ID, Type: publicops.DepParentChild})
	add(
		publicops.DependencyEdge{IssueID: childA.ID, DependsOnID: epic.ID, Type: publicops.DepParentChild},
		publicops.DependencyEdge{IssueID: childB.ID, DependsOnID: epic.ID, Type: publicops.DepParentChild},
	)

	assertLabels := func(id string, want []string) {
		t.Helper()
		got, err := store.GetLabels(ctx, id)
		if err != nil {
			t.Fatalf("GetLabels(%s): %v", id, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("labels(%s) = %v, want %v", id, got, want)
		}
	}
	assertLabels(epic.ID, []string{"ctx:a", "ctx:z", "ordinary"})

	// Re-adding is idempotent and re-derives the same direct-child union. A
	// duplicate direct-child context is materialized once, and the grandchild's
	// context never reaches the epic.
	add(publicops.DependencyEdge{IssueID: childA.ID, DependsOnID: epic.ID, Type: publicops.DepParentChild})
	assertLabels(epic.ID, []string{"ctx:a", "ctx:z", "ordinary"})
	add(publicops.DependencyEdge{IssueID: childDuplicate.ID, DependsOnID: epic.ID, Type: publicops.DepParentChild})
	assertLabels(epic.ID, []string{"ctx:a", "ctx:z", "ordinary"})

	if _, err := editor.RemoveDependency(ctx, publicops.RemoveDependencyRequest{
		Actor: "writer", IssueID: childA.ID, DependsOnID: epic.ID, ReconcileEpicContexts: true,
	}); err != nil {
		t.Fatalf("RemoveDependency child A: %v", err)
	}
	assertLabels(epic.ID, []string{"ctx:a", "ordinary"})
	if _, err := editor.RemoveDependency(ctx, publicops.RemoveDependencyRequest{
		Actor: "writer", IssueID: childB.ID, DependsOnID: epic.ID, ReconcileEpicContexts: true,
	}); err != nil {
		t.Fatalf("RemoveDependency first duplicate-context child: %v", err)
	}
	assertLabels(epic.ID, []string{"ctx:a", "ordinary"})
	if _, err := editor.RemoveDependency(ctx, publicops.RemoveDependencyRequest{
		Actor: "writer", IssueID: childB.ID, DependsOnID: epic.ID, ReconcileEpicContexts: true,
	}); err != nil {
		t.Fatalf("idempotent RemoveDependency first duplicate-context child: %v", err)
	}
	assertLabels(epic.ID, []string{"ctx:a", "ordinary"})
	if _, err := editor.RemoveDependency(ctx, publicops.RemoveDependencyRequest{
		Actor: "writer", IssueID: childDuplicate.ID, DependsOnID: epic.ID, ReconcileEpicContexts: true,
	}); err != nil {
		t.Fatalf("RemoveDependency final duplicate-context child: %v", err)
	}
	assertLabels(epic.ID, []string{"ordinary"})

	before, err := store.GetDependencyRecords(ctx, invalid.ID)
	if err != nil {
		t.Fatalf("GetDependencyRecords before rollback: %v", err)
	}
	_, err = editor.AddDependencies(ctx, publicops.AddDependenciesRequest{
		Actor: "writer", Edges: []publicops.DependencyEdge{{IssueID: invalid.ID, DependsOnID: epic.ID, Type: publicops.DepParentChild}}, ReconcileEpicContexts: true,
	})
	if !errors.Is(err, storage.ErrValidation) {
		t.Fatalf("invalid child add error = %v, want validation error", err)
	}
	after, err := store.GetDependencyRecords(ctx, invalid.ID)
	if err != nil {
		t.Fatalf("GetDependencyRecords after rollback: %v", err)
	}
	if len(before) != len(after) {
		t.Fatalf("invalid child edge count = %d, want rollback to %d", len(after), len(before))
	}
	assertLabels(epic.ID, []string{"ordinary"})
}

type failEpicContextLabelInsertTx struct {
	*sql.Tx
}

func (tx *failEpicContextLabelInsertTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if strings.Contains(query, "INSERT IGNORE INTO labels") {
		return nil, errors.New("injected epic context label write failure")
	}
	return tx.Tx.ExecContext(ctx, query, args...)
}

func TestDoltDependencyEditorReconciliationRollsBackOnLabelWriteFailure(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	epic := &types.Issue{ID: "test-context-failure-epic", Title: "epic", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic}
	child := &types.Issue{ID: "test-context-failure-child", Title: "child", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	if err := store.CreateIssues(ctx, []*types.Issue{epic, child}, "seed"); err != nil {
		t.Fatalf("CreateIssues: %v", err)
	}
	for id, label := range map[string]string{epic.ID: "ctx:stale", child.ID: "ctx:one"} {
		if err := store.AddLabel(ctx, id, label, "seed"); err != nil {
			t.Fatalf("AddLabel(%s): %v", id, err)
		}
	}
	if err := store.AddLabel(ctx, epic.ID, "ordinary", "seed"); err != nil {
		t.Fatalf("AddLabel(%s): %v", epic.ID, err)
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO dependencies (id, issue_id, depends_on_issue_id, type, created_at, created_by)
		VALUES (UUID(), ?, ?, ?, NOW(), ?)`, child.ID, epic.ID, types.DepParentChild, "writer")
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("seed edge in transaction: %v", err)
	}
	_, err = storageissueops.ReconcileEpicContextsInTx(ctx, &failEpicContextLabelInsertTx{Tx: tx}, epic.ID, "writer")
	if err == nil || !strings.Contains(err.Error(), "injected epic context label write failure") {
		_ = tx.Rollback()
		t.Fatalf("reconcile error = %v, want injected label write failure", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	deps, err := store.GetDependencyRecords(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetDependencyRecords after rollback: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("dependency count after rollback = %d, want 0", len(deps))
	}
	labels, err := store.GetLabels(ctx, epic.ID)
	if err != nil {
		t.Fatalf("GetLabels after rollback: %v", err)
	}
	if want := []string{"ctx:stale", "ordinary"}; !reflect.DeepEqual(labels, want) {
		t.Fatalf("epic labels after rollback = %v, want %v", labels, want)
	}
}

// What is left here is the DependencyEditor's PERSISTENCE tier: how a history
// entry lands and what it says. The role's semantics — result shape,
// idempotency verdicts, the typed refusals, all-or-nothing, and the number of
// history entries a call records — moved to
// conformance.RunDependencyEditor*, which runs on all three backends.
//
// These stay dolt-only on purpose. The commit-message spellings are
// single-sourced in internal/storage/issueops, so one backend pinning a
// spelling is enough and re-pinning it three times is duplication; the staging
// assertions read dolt_status and plant a dirty working set, and the working
// set is not a caller-visible concept on the unit-of-work route.

// TestDoltStoreDependencyEditorBatchNamesItsHistoryEntry pins the count
// spelling and the staging half: N edges commit as one entry that names the
// count, and the table it staged is clean afterwards.
func TestDoltStoreDependencyEditorBatchNamesItsHistoryEntry(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()
	seedIssues(ctx, t, store, "test-dep-add-a", "test-dep-add-b", "test-dep-add-c")

	editor, err := store.DependencyEditor()
	if err != nil {
		t.Fatalf("DependencyEditor(): %v", err)
	}
	if _, err := editor.AddDependencies(ctx, publicops.AddDependenciesRequest{
		Actor: "writer",
		Edges: []publicops.DependencyEdge{
			{IssueID: "test-dep-add-a", DependsOnID: "test-dep-add-b", Type: publicops.DepBlocks},
			{IssueID: "test-dep-add-a", DependsOnID: "test-dep-add-c", Type: publicops.DepRelated},
		},
	}); err != nil {
		t.Fatalf("AddDependencies: %v", err)
	}

	if !doltHasCommitMessage(ctx, t, store, "dependency: add 2 edges") {
		t.Fatal("no commit named the edge count")
	}
	requireCleanTables(ctx, t, store, "dependencies")
}

// TestDoltStoreDependencyEditorSingleEdgeNamesTheHistoryEntry pins the other
// spelling: one edge names both endpoints, however the caller spelled the
// request.
func TestDoltStoreDependencyEditorSingleEdgeNamesTheHistoryEntry(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()
	seedIssues(ctx, t, store, "test-dep-msg-a", "test-dep-msg-b")

	editor, err := store.DependencyEditor()
	if err != nil {
		t.Fatalf("DependencyEditor(): %v", err)
	}
	if _, err := editor.AddDependencies(ctx, publicops.AddDependenciesRequest{
		Actor: "writer",
		Edges: []publicops.DependencyEdge{{IssueID: "test-dep-msg-a", DependsOnID: "test-dep-msg-b", Type: publicops.DepBlocks}},
	}); err != nil {
		t.Fatalf("AddDependencies single: %v", err)
	}
	if !doltHasCommitMessage(ctx, t, store, "bd: dep add test-dep-msg-a test-dep-msg-b") {
		t.Fatal("a single edge did not name both endpoints in history")
	}
}

// TestDoltStoreDependencyEditorRemoveNamesTheHistoryEntry pins the removal's
// spelling and its staging.
func TestDoltStoreDependencyEditorRemoveNamesTheHistoryEntry(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()
	seedIssues(ctx, t, store, "test-dep-rm-a", "test-dep-rm-b")

	editor, err := store.DependencyEditor()
	if err != nil {
		t.Fatalf("DependencyEditor(): %v", err)
	}
	if _, err := editor.AddDependencies(ctx, publicops.AddDependenciesRequest{
		Actor: "writer",
		Edges: []publicops.DependencyEdge{{IssueID: "test-dep-rm-a", DependsOnID: "test-dep-rm-b", Type: publicops.DepBlocks}},
	}); err != nil {
		t.Fatalf("AddDependencies: %v", err)
	}
	if _, err := editor.RemoveDependency(ctx, publicops.RemoveDependencyRequest{
		Actor: "writer", IssueID: "test-dep-rm-a", DependsOnID: "test-dep-rm-b",
	}); err != nil {
		t.Fatalf("RemoveDependency: %v", err)
	}

	if !doltHasCommitMessage(ctx, t, store, "bd: dep remove test-dep-rm-a test-dep-rm-b") {
		t.Fatal("the removal did not name the edge in history")
	}
	requireCleanTables(ctx, t, store, "dependencies")
}

// TestDoltStoreDependencyEditorNoOpRemoveLeavesPendingRowsUncommitted is the
// GH#2455 pin for this role: an operation that wrote nothing must stage
// nothing, because DOLT_ADD stages a whole TABLE and a commit made for a no-op
// carries away whatever else was pending in it.
//
// The commit COUNT cannot see this on its own — Dolt swallows a commit with an
// empty diff, so a leaky no-op only shows up when there is something in the
// table for it to sweep. That is why this case survives the conformance
// contract's own "a removal that found nothing records no history" assertion:
// they fail on different bugs.
func TestDoltStoreDependencyEditorNoOpRemoveLeavesPendingRowsUncommitted(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()
	seedIssues(ctx, t, store, "test-dep-sweep-a", "test-dep-sweep-b", "test-dep-sweep-c")

	editor, err := store.DependencyEditor()
	if err != nil {
		t.Fatalf("DependencyEditor(): %v", err)
	}
	// A pending edge in the working set that belongs to nobody's transaction.
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO dependencies (id, issue_id, depends_on_issue_id, type, created_at, created_by)
		VALUES (?, ?, ?, ?, NOW(), ?)`,
		"00000000-0000-4000-8000-00000000d09e", "test-dep-sweep-b", "test-dep-sweep-c", types.DepBlocks, "someone-else",
	); err != nil {
		t.Fatalf("stage a pending dependency row: %v", err)
	}

	before := doltCommitCount(ctx, t, store)
	removed, err := editor.RemoveDependency(ctx, publicops.RemoveDependencyRequest{
		Actor: "writer", IssueID: "test-dep-sweep-a", DependsOnID: "test-dep-sweep-b",
	})
	if err != nil {
		t.Fatalf("RemoveDependency on a missing edge: %v", err)
	}
	if removed.Removed {
		t.Fatal("Removed = true, want false")
	}
	if after := doltCommitCount(ctx, t, store); after != before {
		t.Fatalf("commit count = %d, want %d: the no-op swept the pending row into a commit", after, before)
	}
	var dirty int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM dolt_status WHERE table_name = 'dependencies'").Scan(&dirty); err != nil {
		t.Fatalf("query dolt_status: %v", err)
	}
	if dirty == 0 {
		t.Fatal("dependencies is clean: the no-op removal committed a row it never wrote")
	}
}

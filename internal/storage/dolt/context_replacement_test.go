package dolt

import (
	"errors"
	"reflect"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

func TestDoltUpdateContextReconcilesDirectEpicParent(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	epics := []*types.Issue{
		{ID: "context-update-epic", Title: "epic", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic},
		{ID: "context-update-child", Title: "child", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
		{ID: "context-update-parent", Title: "ordinary parent", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Labels: []string{"ordinary"}},
	}
	if err := store.CreateIssues(ctx, epics, "seed"); err != nil {
		t.Fatalf("CreateIssues: %v", err)
	}
	for id, label := range map[string]string{
		"context-update-epic":  "ctx:old",
		"context-update-child": "ctx:one",
	} {
		if err := store.AddLabel(ctx, id, label, "seed"); err != nil {
			t.Fatalf("AddLabel(%s): %v", id, err)
		}
	}
	if err := store.AddLabel(ctx, "context-update-epic", "ordinary", "seed"); err != nil {
		t.Fatalf("AddLabel ordinary: %v", err)
	}
	if err := store.AddDependency(ctx, &types.Dependency{IssueID: "context-update-child", DependsOnID: "context-update-epic", Type: types.DepParentChild}, "seed"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	if err := store.AddDependency(ctx, &types.Dependency{IssueID: "context-update-child", DependsOnID: "context-update-parent", Type: types.DepParentChild}, "seed"); err != nil {
		t.Fatalf("AddDependency ordinary parent: %v", err)
	}

	lifecycle, err := store.IssueLifecycle()
	if err != nil {
		t.Fatalf("IssueLifecycle: %v", err)
	}
	if _, err := lifecycle.Update(ctx, publicops.UpdateRequest{
		Actor:   "writer",
		IssueID: "context-update-child",
		Patch: publicops.IssuePatch{Contexts: publicops.Field[[]string]{
			Set: true, Value: []string{"two"},
		}},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

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
	assertLabels("context-update-child", []string{"ctx:two"})
	assertLabels("context-update-epic", []string{"ctx:two", "ordinary"})
	assertLabels("context-update-parent", []string{"ordinary"})
}

func TestDoltUpdateContextRejectsEpicWithoutMutation(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	ep := &types.Issue{ID: "context-reject-epic", Title: "epic", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic}
	if err := store.CreateIssue(ctx, ep, "seed"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.AddLabel(ctx, ep.ID, "ctx:old", "seed"); err != nil {
		t.Fatalf("AddLabel: %v", err)
	}
	lifecycle, err := store.IssueLifecycle()
	if err != nil {
		t.Fatalf("IssueLifecycle: %v", err)
	}
	_, err = lifecycle.Update(ctx, publicops.UpdateRequest{
		Actor:   "writer",
		IssueID: ep.ID,
		Patch: publicops.IssuePatch{
			IssueType: publicops.Field[types.IssueType]{Set: true, Value: types.TypeTask},
			Contexts:  publicops.Field[[]string]{Set: true, Value: []string{"new"}},
		},
	})
	if !errors.Is(err, storage.ErrValidation) {
		t.Fatalf("Update error = %v, want ErrValidation", err)
	}
	labels, err := store.GetLabels(ctx, ep.ID)
	if err != nil {
		t.Fatalf("GetLabels: %v", err)
	}
	if want := []string{"ctx:old"}; !reflect.DeepEqual(labels, want) {
		t.Fatalf("epic labels = %v, want %v", labels, want)
	}
	stored, err := store.GetIssue(ctx, ep.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if stored.IssueType != types.TypeEpic {
		t.Fatalf("epic type = %q, want %q", stored.IssueType, types.TypeEpic)
	}
}

func TestDoltUpdateContextRejectsParentPatchWithoutMutation(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	oldParent := &types.Issue{ID: "context-parent-old", Title: "old parent", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic}
	newParent := &types.Issue{ID: "context-parent-new", Title: "new parent", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic}
	child := &types.Issue{ID: "context-parent-child", Title: "child", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	if err := store.CreateIssues(ctx, []*types.Issue{oldParent, newParent, child}, "seed"); err != nil {
		t.Fatalf("CreateIssues: %v", err)
	}
	if err := store.AddLabel(ctx, child.ID, "ctx:old", "seed"); err != nil {
		t.Fatalf("AddLabel: %v", err)
	}
	if err := store.AddDependency(ctx, &types.Dependency{IssueID: child.ID, DependsOnID: oldParent.ID, Type: types.DepParentChild}, "seed"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	lifecycle, err := store.IssueLifecycle()
	if err != nil {
		t.Fatalf("IssueLifecycle: %v", err)
	}
	_, err = lifecycle.Update(ctx, publicops.UpdateRequest{
		Actor:   "writer",
		IssueID: child.ID,
		Patch: publicops.IssuePatch{
			Contexts: publicops.Field[[]string]{Set: true, Value: []string{"new"}},
			ParentID: publicops.Field[string]{Set: true, Value: newParent.ID},
		},
	})
	if !errors.Is(err, storage.ErrValidation) {
		t.Fatalf("Update error = %v, want ErrValidation", err)
	}
	labels, err := store.GetLabels(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetLabels: %v", err)
	}
	if want := []string{"ctx:old"}; !reflect.DeepEqual(labels, want) {
		t.Fatalf("child labels = %v, want %v", labels, want)
	}
	deps, err := store.GetDependencyRecords(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetDependencyRecords: %v", err)
	}
	if len(deps) != 1 || deps[0].DependsOnID != oldParent.ID {
		t.Fatalf("child parents = %v, want only %q", deps, oldParent.ID)
	}
}

func TestDoltUpdateContextRollsBackWhenParentReconciliationFails(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	issues := []*types.Issue{
		{ID: "context-rollback-epic", Title: "epic", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic},
		{ID: "context-rollback-child-a", Title: "valid child", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
		// This pre-existing malformed child makes the derived parent state
		// impossible to validate after child A's write.
		{ID: "context-rollback-child-z", Title: "invalid child", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
	}
	if err := store.CreateIssues(ctx, issues, "seed"); err != nil {
		t.Fatalf("CreateIssues: %v", err)
	}
	for id, label := range map[string]string{
		"context-rollback-epic":    "ctx:old",
		"context-rollback-child-a": "ctx:one",
	} {
		if err := store.AddLabel(ctx, id, label, "seed"); err != nil {
			t.Fatalf("AddLabel(%s): %v", id, err)
		}
	}
	for _, child := range []string{"context-rollback-child-a", "context-rollback-child-z"} {
		if err := store.AddDependency(ctx, &types.Dependency{IssueID: child, DependsOnID: "context-rollback-epic", Type: types.DepParentChild}, "seed"); err != nil {
			t.Fatalf("AddDependency(%s): %v", child, err)
		}
	}

	lifecycle, err := store.IssueLifecycle()
	if err != nil {
		t.Fatalf("IssueLifecycle: %v", err)
	}
	_, err = lifecycle.Update(ctx, publicops.UpdateRequest{
		Actor:   "writer",
		IssueID: "context-rollback-child-a",
		Patch:   publicops.IssuePatch{Contexts: publicops.Field[[]string]{Set: true, Value: []string{"two"}}},
	})
	if !errors.Is(err, storage.ErrValidation) {
		t.Fatalf("Update error = %v, want ErrValidation", err)
	}
	for id, want := range map[string][]string{
		"context-rollback-child-a": {"ctx:one"},
		"context-rollback-epic":    {"ctx:old"},
	} {
		labels, readErr := store.GetLabels(ctx, id)
		if readErr != nil {
			t.Fatalf("GetLabels(%s): %v", id, readErr)
		}
		if !reflect.DeepEqual(labels, want) {
			t.Fatalf("labels(%s) = %v, want %v", id, labels, want)
		}
	}
}

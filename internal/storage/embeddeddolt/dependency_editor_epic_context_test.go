//go:build cgo

package embeddeddolt_test

import (
	"reflect"
	"testing"

	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

func TestEmbeddedDependencyEditorReconcilesEpicContexts(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	te := newTestEnv(t, "epic-context")
	ctx := t.Context()
	epic := &types.Issue{ID: "embedded-context-epic", Title: "epic", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic}
	childA := &types.Issue{ID: "embedded-context-a", Title: "a", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	childB := &types.Issue{ID: "embedded-context-b", Title: "b", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	if err := te.store.CreateIssues(ctx, []*types.Issue{epic, childA, childB}, "seed"); err != nil {
		t.Fatalf("CreateIssues: %v", err)
	}
	if err := te.store.AddLabel(ctx, epic.ID, "ordinary", "seed"); err != nil {
		t.Fatalf("AddLabel epic: %v", err)
	}
	for id, label := range map[string]string{childA.ID: "ctx:z", childB.ID: "ctx:a"} {
		if err := te.store.AddLabel(ctx, id, label, "seed"); err != nil {
			t.Fatalf("AddLabel %s: %v", id, err)
		}
	}
	editor, err := te.store.DependencyEditor()
	if err != nil {
		t.Fatalf("DependencyEditor: %v", err)
	}
	if _, err := editor.AddDependencies(ctx, publicops.AddDependenciesRequest{
		Actor: "writer", ReconcileEpicContexts: true,
		Edges: []publicops.DependencyEdge{
			{IssueID: childA.ID, DependsOnID: epic.ID, Type: publicops.DepParentChild},
			{IssueID: childB.ID, DependsOnID: epic.ID, Type: publicops.DepParentChild},
		},
	}); err != nil {
		t.Fatalf("AddDependencies: %v", err)
	}
	labels, err := te.store.GetLabels(ctx, epic.ID)
	if err != nil {
		t.Fatalf("GetLabels after add: %v", err)
	}
	if want := []string{"ctx:a", "ctx:z", "ordinary"}; !reflect.DeepEqual(labels, want) {
		t.Fatalf("epic labels = %v, want %v", labels, want)
	}
	if _, err := editor.RemoveDependency(ctx, publicops.RemoveDependencyRequest{
		Actor: "writer", IssueID: childA.ID, DependsOnID: epic.ID, ReconcileEpicContexts: true,
	}); err != nil {
		t.Fatalf("RemoveDependency: %v", err)
	}
	labels, err = te.store.GetLabels(ctx, epic.ID)
	if err != nil {
		t.Fatalf("GetLabels after remove: %v", err)
	}
	if want := []string{"ctx:a", "ordinary"}; !reflect.DeepEqual(labels, want) {
		t.Fatalf("epic labels after remove = %v, want %v", labels, want)
	}
}

func TestEmbeddedIssueLifecycleReplacesContextAndReconcilesParent(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	te := newTestEnv(t, "context-replacement")
	ctx := t.Context()
	ep := &types.Issue{ID: "embedded-replace-epic", Title: "epic", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic, Labels: []string{"ctx:old", "ordinary"}}
	child := &types.Issue{ID: "embedded-replace-child", Title: "child", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Labels: []string{"ctx:one"}}
	if err := te.store.CreateIssues(ctx, []*types.Issue{ep, child}, "seed"); err != nil {
		t.Fatalf("CreateIssues: %v", err)
	}
	if err := te.store.AddDependency(ctx, &types.Dependency{IssueID: child.ID, DependsOnID: ep.ID, Type: types.DepParentChild}, "seed"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	lifecycle, err := te.store.IssueLifecycle()
	if err != nil {
		t.Fatalf("IssueLifecycle: %v", err)
	}
	if _, err := lifecycle.Update(ctx, publicops.UpdateRequest{
		Actor: "writer", IssueID: child.ID,
		Patch: publicops.IssuePatch{Contexts: publicops.Field[[]string]{Set: true, Value: []string{"two"}}},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	for id, want := range map[string][]string{
		child.ID: {"ctx:two"},
		ep.ID:    {"ctx:two", "ordinary"},
	} {
		labels, err := te.store.GetLabels(ctx, id)
		if err != nil {
			t.Fatalf("GetLabels(%s): %v", id, err)
		}
		if !reflect.DeepEqual(labels, want) {
			t.Fatalf("labels(%s) = %v, want %v", id, labels, want)
		}
	}
}

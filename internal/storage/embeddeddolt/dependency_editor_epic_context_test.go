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

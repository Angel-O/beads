package uow

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

func TestUOWDependencyEditorReconcilesEpicContexts(t *testing.T) {
	ctx := t.Context()
	provider := newUOWRoleFixtureProvider(t, ctx, "ctx")
	kit := newUOWRoleFixtureKit(provider, "ctx")
	epic := &types.Issue{ID: "ctx-epic", Title: "epic", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic, Labels: []string{"ordinary"}}
	child := &types.Issue{ID: "ctx-child", Title: "child", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Labels: []string{"ctx:one"}}
	if err := kit.CreateIssue(ctx, epic, "seed"); err != nil {
		t.Fatalf("create epic: %v", err)
	}
	if err := kit.CreateIssue(ctx, child, "seed"); err != nil {
		t.Fatalf("create child: %v", err)
	}
	editor, err := NewDependencyEditor(provider)
	if err != nil {
		t.Fatalf("NewDependencyEditor: %v", err)
	}
	request := publicops.AddDependenciesRequest{
		Actor:                 "writer",
		Edges:                 []publicops.DependencyEdge{{IssueID: child.ID, DependsOnID: epic.ID, Type: publicops.DepParentChild}},
		ReconcileEpicContexts: true,
	}
	if _, err := editor.AddDependencies(ctx, request); err != nil {
		t.Fatalf("AddDependencies: %v", err)
	}
	assertUOWLabels(t, ctx, provider, epic.ID, []string{"ctx:one", "ordinary"})

	if _, err := editor.RemoveDependency(ctx, publicops.RemoveDependencyRequest{
		Actor: "writer", IssueID: child.ID, DependsOnID: epic.ID, ReconcileEpicContexts: true,
	}); err != nil {
		t.Fatalf("RemoveDependency: %v", err)
	}
	assertUOWLabels(t, ctx, provider, epic.ID, []string{"ordinary"})

	invalid := &types.Issue{ID: "ctx-invalid", Title: "invalid", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic, Labels: []string{"ctx:invalid"}}
	if err := kit.CreateIssue(ctx, invalid, "seed"); err != nil {
		t.Fatalf("create invalid child: %v", err)
	}
	if _, err := editor.AddDependencies(ctx, publicops.AddDependenciesRequest{
		Actor: "writer", Edges: []publicops.DependencyEdge{{IssueID: invalid.ID, DependsOnID: epic.ID, Type: publicops.DepParentChild}}, ReconcileEpicContexts: true,
	}); !errors.Is(err, storage.ErrValidation) {
		t.Fatalf("invalid child error = %v, want validation error", err)
	}
	deps, err := RunTxRead(ctx, provider, func(ctx context.Context, uw UnitOfWork) ([]*types.Dependency, error) {
		byIssue, err := uw.DependencyUseCase().GetIssueDependencyRecords(ctx, []string{invalid.ID})
		return byIssue[invalid.ID], err
	})
	if err != nil {
		t.Fatalf("read invalid child dependencies: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("invalid child dependency count = %d, want 0 after rollback", len(deps))
	}
	assertUOWLabels(t, ctx, provider, epic.ID, []string{"ordinary"})
}

func TestUOWIssueLifecycleReplacesContextAndReconcilesParent(t *testing.T) {
	ctx := t.Context()
	provider := newUOWRoleFixtureProvider(t, ctx, "context-replacement")
	kit := newUOWRoleFixtureKit(provider, "context-replacement")
	ep := &types.Issue{ID: "replace-epic", Title: "epic", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic, Labels: []string{"ctx:old", "ordinary"}}
	child := &types.Issue{ID: "replace-child", Title: "child", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Labels: []string{"ctx:one"}}
	if err := kit.CreateIssue(ctx, ep, "seed"); err != nil {
		t.Fatalf("create epic: %v", err)
	}
	if err := kit.CreateIssue(ctx, child, "seed"); err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := kit.AddDependency(ctx, &types.Dependency{IssueID: child.ID, DependsOnID: ep.ID, Type: types.DepParentChild}, "seed"); err != nil {
		t.Fatalf("add dependency: %v", err)
	}
	source, ok := provider.(IssueLifecycleSource)
	if !ok {
		t.Fatalf("provider %T does not offer IssueLifecycle", provider)
	}
	lifecycle, err := source.IssueLifecycle()
	if err != nil {
		t.Fatalf("IssueLifecycle: %v", err)
	}
	if _, err := lifecycle.Update(ctx, publicops.UpdateRequest{
		Actor: "writer", IssueID: child.ID,
		Patch: publicops.IssuePatch{Contexts: publicops.Field[[]string]{Set: true, Value: []string{"two"}}},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	assertUOWLabels(t, ctx, provider, child.ID, []string{"ctx:two"})
	assertUOWLabels(t, ctx, provider, ep.ID, []string{"ctx:two", "ordinary"})
}

func TestUOWBatchApplierReplacesContextAndReconcilesParent(t *testing.T) {
	ctx := t.Context()
	provider := newUOWRoleFixtureProvider(t, ctx, "context-batch")
	kit := newUOWRoleFixtureKit(provider, "context-batch")
	ep := &types.Issue{ID: "batch-epic", Title: "epic", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic, Labels: []string{"ctx:old", "ordinary"}}
	child := &types.Issue{ID: "batch-child", Title: "child", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Labels: []string{"ctx:one"}}
	if err := kit.CreateIssue(ctx, ep, "seed"); err != nil {
		t.Fatalf("create epic: %v", err)
	}
	if err := kit.CreateIssue(ctx, child, "seed"); err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := kit.AddDependency(ctx, &types.Dependency{IssueID: child.ID, DependsOnID: ep.ID, Type: types.DepParentChild}, "seed"); err != nil {
		t.Fatalf("add dependency: %v", err)
	}
	source, ok := provider.(BatchApplierSource)
	if !ok {
		t.Fatalf("provider %T does not offer BatchApplier", provider)
	}
	applier, err := source.BatchApplier()
	if err != nil {
		t.Fatalf("BatchApplier: %v", err)
	}
	if _, err := applier.ApplyBatch(ctx, publicops.ApplyBatchRequest{
		Actor: "writer",
		Items: []publicops.ApplyItem{{Kind: publicops.ItemUpdate, Update: &publicops.UpdateItem{
			Target: publicops.Ref{ID: child.ID},
			Patch:  publicops.IssuePatch{Contexts: publicops.Field[[]string]{Set: true, Value: []string{"two"}}},
		}}},
	}); err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	assertUOWLabels(t, ctx, provider, child.ID, []string{"ctx:two"})
	assertUOWLabels(t, ctx, provider, ep.ID, []string{"ctx:two", "ordinary"})
}

func assertUOWLabels(t *testing.T, ctx context.Context, provider UnitOfWorkProvider, issueID string, want []string) {
	t.Helper()
	got, err := RunTxRead(ctx, provider, func(ctx context.Context, uw UnitOfWork) ([]string, error) {
		return uw.LabelUseCase().GetLabels(ctx, issueID)
	})
	if err != nil {
		t.Fatalf("GetLabels(%s): %v", issueID, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("labels(%s) = %v, want %v", issueID, got, want)
	}
}

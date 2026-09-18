package dolt

import (
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

func TestDoltStoreRenameScopePreservesScopeState(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	source := &types.Scope{ID: "scope-rename-source", Name: "Original"}
	if err := store.CreateScope(ctx, source, true); err != nil {
		t.Fatalf("CreateScope(source): %v", err)
	}
	if err := store.CreateScope(ctx, &types.Scope{ID: "scope-rename-target", Name: "Target"}, false); err != nil {
		t.Fatalf("CreateScope(target): %v", err)
	}
	issue := &types.Issue{ID: "scope-rename-issue", Title: "scope rename issue", Status: types.StatusOpen, IssueType: types.TypeTask}
	if err := store.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.AddScopeMembers(ctx, source.ID, []string{issue.ID}); err != nil {
		t.Fatalf("AddScopeMembers: %v", err)
	}
	createdOn := source.CreatedOn

	if err := store.RenameScope(ctx, source.ID, "RENAMED"); err != nil {
		t.Fatalf("RenameScope: %v", err)
	}
	renamed, err := store.GetScope(ctx, source.ID)
	if err != nil {
		t.Fatalf("GetScope: %v", err)
	}
	if renamed.ID != source.ID || renamed.Name != "RENAMED" || renamed.NormalizedName != "renamed" || !renamed.CreatedOn.Equal(createdOn) || len(renamed.Members) != 1 {
		t.Fatalf("renamed scope = %#v, want identity and membership preserved", renamed)
	}
	active, err := store.GetActiveScope(ctx)
	if err != nil || active == nil || active.ID != source.ID || active.Name != "RENAMED" {
		t.Fatalf("active scope = %#v, %v, want renamed source", active, err)
	}
	if err := store.RenameScope(ctx, source.ID, " target "); !errors.Is(err, storage.ErrScopeAlreadyExists) {
		t.Fatalf("colliding RenameScope error = %v, want ErrScopeAlreadyExists", err)
	}
}

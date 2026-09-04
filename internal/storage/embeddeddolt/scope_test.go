//go:build cgo

package embeddeddolt_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

func TestEmbeddedScopesLifecycleAndRead(t *testing.T) {
	if testing.Short() {
		t.Skip("scope persistence test uses the embedded engine")
	}
	te := newTestEnv(t, "scope_life")
	ctx := t.Context()

	first := &types.Scope{ID: "scope-first", Name: " First Scope "}
	if err := te.store.CreateScope(ctx, first, false); err != nil {
		t.Fatalf("CreateScope: %v", err)
	}
	if first.NormalizedName != "first scope" || first.CreatedOn.IsZero() {
		t.Fatalf("created scope = %#v, want normalized name and created time", first)
	}
	if active, err := te.store.GetActiveScope(ctx); err != nil || active != nil {
		t.Fatalf("GetActiveScope before activation = %#v, %v; want nil", active, err)
	}

	second := &types.Scope{ID: "scope-second", Name: "Second Scope"}
	if err := te.store.CreateScope(ctx, second, true); err != nil {
		t.Fatalf("CreateScope(activate): %v", err)
	}
	active, err := te.store.GetActiveScope(ctx)
	if err != nil || active == nil || active.ID != second.ID {
		t.Fatalf("active scope = %#v, %v; want %s", active, err, second.ID)
	}
	if err := te.store.ActivateScope(ctx, first.ID); err != nil {
		t.Fatalf("ActivateScope: %v", err)
	}
	active, err = te.store.GetActiveScope(ctx)
	if err != nil || active == nil || active.ID != first.ID {
		t.Fatalf("replacement active scope = %#v, %v; want %s", active, err, first.ID)
	}
	if err := te.store.DeactivateScope(ctx); err != nil {
		t.Fatalf("DeactivateScope: %v", err)
	}
	if active, err = te.store.GetActiveScope(ctx); err != nil || active != nil {
		t.Fatalf("GetActiveScope after deactivation = %#v, %v; want nil", active, err)
	}

	if got, err := te.store.ListScopes(ctx); err != nil || len(got) != 2 || got[0].ID != first.ID || got[1].ID != second.ID {
		t.Fatalf("ListScopes = %#v, %v; want creation order", got, err)
	}
}

func TestEmbeddedScopesMembershipAtomicityAndMove(t *testing.T) {
	te := newTestEnv(t, "scope_members")
	ctx := t.Context()
	createScope(t, te, "scope-a", "A")
	createScope(t, te, "scope-b", "B")
	createScopeIssue(t, te, "scope-issue-a")
	createScopeIssue(t, te, "scope-issue-b")
	createScopeIssue(t, te, "scope-issue-c")

	if err := te.store.AddScopeMembers(ctx, "scope-a", []string{"scope-issue-a"}); err != nil {
		t.Fatalf("initial add: %v", err)
	}
	if err := te.store.AddScopeMembers(ctx, "scope-a", []string{"scope-issue-b", "missing"}); !errors.Is(err, storage.ErrScopeIssueNotFound) {
		t.Fatalf("mixed missing add error = %v, want ErrScopeIssueNotFound", err)
	}
	assertScopeMembers(t, te, "scope-a", []string{"scope-issue-a"})
	if err := te.store.AddScopeMembers(ctx, "scope-b", []string{"scope-issue-a", "scope-issue-b"}); !errors.Is(err, storage.ErrScopeMembershipConflict) {
		t.Fatalf("conflicting add error = %v, want ErrScopeMembershipConflict", err)
	}
	assertScopeMembers(t, te, "scope-b", nil)

	if err := te.store.AddScopeMembers(ctx, "scope-a", []string{"scope-issue-b", "scope-issue-b"}); err != nil {
		t.Fatalf("deduplicated add: %v", err)
	}
	if err := te.store.RemoveScopeMembers(ctx, "scope-a", []string{"scope-issue-b", "scope-issue-c", "scope-issue-c"}); err != nil {
		t.Fatalf("idempotent remove: %v", err)
	}
	assertScopeMembers(t, te, "scope-a", []string{"scope-issue-a"})

	if err := te.store.MoveScopeMembers(ctx, "scope-a", "scope-b", []string{"scope-issue-a"}); err != nil {
		t.Fatalf("move: %v", err)
	}
	if err := te.store.MoveScopeMembers(ctx, "scope-a", "scope-b", []string{"scope-issue-a"}); !errors.Is(err, storage.ErrScopeSourceMembership) {
		t.Fatalf("repeat move error = %v, want ErrScopeSourceMembership", err)
	}
	assertScopeMembers(t, te, "scope-a", nil)
	assertScopeMembers(t, te, "scope-b", []string{"scope-issue-a"})
}

func TestEmbeddedScopesCapacityAndTransactionRollback(t *testing.T) {
	te := newTestEnv(t, "scope_capacity")
	ctx := t.Context()
	createScope(t, te, "scope-cap", "Capacity")
	ids := make([]string, 101)
	issues := make([]*types.Issue, len(ids))
	for i := range ids {
		ids[i] = "scope_capacity-" + threeDigits(i)
		issues[i] = scopeIssue(ids[i])
	}
	if err := te.store.CreateIssues(ctx, issues, "tester"); err != nil {
		t.Fatalf("CreateIssues: %v", err)
	}
	if err := te.store.AddScopeMembers(ctx, "scope-cap", ids[:99]); err != nil {
		t.Fatalf("add 99: %v", err)
	}
	if err := te.store.AddScopeMembers(ctx, "scope-cap", ids[99:100]); err != nil {
		t.Fatalf("add 100: %v", err)
	}
	if err := te.store.AddScopeMembers(ctx, "scope-cap", ids[100:]); !errors.Is(err, storage.ErrScopeCapacityExceeded) {
		t.Fatalf("add 101st error = %v, want ErrScopeCapacityExceeded", err)
	}
	assertScopeMembers(t, te, "scope-cap", ids[:100])

	rollbackID := "scope-rollback"
	err := te.store.RunInTransaction(ctx, "bd: rollback scope", func(tx storage.Transaction) error {
		if err := tx.CreateScope(ctx, &types.Scope{ID: rollbackID, Name: "rollback"}, false); err != nil {
			return err
		}
		return errors.New("test rollback")
	})
	if err == nil {
		t.Fatal("RunInTransaction rollback returned nil")
	}
	if _, err := te.store.GetScope(ctx, rollbackID); !errors.Is(err, storage.ErrScopeNotFound) {
		t.Fatalf("rolled-back scope error = %v, want ErrScopeNotFound", err)
	}
}

func TestEmbeddedScopesConcurrentCompetingMembership(t *testing.T) {
	te := newTestEnv(t, "scope_race")
	ctx := t.Context()
	createScope(t, te, "scope-race-a", "A")
	createScope(t, te, "scope-race-b", "B")
	createScopeIssue(t, te, "scope-race-issue")

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, scopeID := range []string{"scope-race-a", "scope-race-b"} {
		wg.Add(1)
		go func(scopeID string) {
			defer wg.Done()
			errs <- te.store.AddScopeMembers(ctx, scopeID, []string{"scope-race-issue"})
		}(scopeID)
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("competing adds succeeded %d times, want exactly once", successes)
	}
	a := scopeMemberIDs(t, te, "scope-race-a")
	b := scopeMemberIDs(t, te, "scope-race-b")
	if len(a)+len(b) != 1 {
		t.Fatalf("competing adds left memberships A=%v B=%v, want one total", a, b)
	}
}

func TestEmbeddedScopeReadOnlyIncludesInternalRelationships(t *testing.T) {
	te := newTestEnv(t, "scope_read")
	ctx := t.Context()
	createScope(t, te, "scope-read", "Read")
	for _, id := range []string{"scope-read-a", "scope-read-b", "scope-read-out"} {
		createScopeIssue(t, te, id)
	}
	if err := te.store.AddDependency(ctx, &types.Dependency{IssueID: "scope-read-a", DependsOnID: "scope-read-b", Type: types.DepRelated}, "tester"); err != nil {
		t.Fatalf("internal dependency: %v", err)
	}
	if err := te.store.AddDependency(ctx, &types.Dependency{IssueID: "scope-read-a", DependsOnID: "scope-read-out", Type: types.DepRelated}, "tester"); err != nil {
		t.Fatalf("external dependency: %v", err)
	}
	if err := te.store.AddScopeMembers(ctx, "scope-read", []string{"scope-read-a", "scope-read-b"}); err != nil {
		t.Fatalf("add members: %v", err)
	}
	details, err := te.store.GetScope(ctx, "scope-read")
	if err != nil {
		t.Fatalf("GetScope: %v", err)
	}
	if got := []string{details.Members[0].ID, details.Members[1].ID}; !reflect.DeepEqual(got, []string{"scope-read-a", "scope-read-b"}) {
		t.Fatalf("members = %v, want sorted members", got)
	}
	if len(details.Relationships) != 1 || details.Relationships[0].DependsOnID != "scope-read-b" {
		t.Fatalf("relationships = %#v, want only the internal edge", details.Relationships)
	}
}

func createScope(t *testing.T, te *testEnv, id, name string) {
	t.Helper()
	if err := te.store.CreateScope(t.Context(), &types.Scope{ID: id, Name: name}, false); err != nil {
		t.Fatalf("CreateScope(%s): %v", id, err)
	}
}

func createScopeIssue(t *testing.T, te *testEnv, id string) {
	t.Helper()
	if err := te.store.CreateIssue(t.Context(), scopeIssue(id), "tester"); err != nil {
		t.Fatalf("CreateIssue(%s): %v", id, err)
	}
}

func scopeIssue(id string) *types.Issue {
	return &types.Issue{ID: id, Title: id, Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
}

func assertScopeMembers(t *testing.T, te *testEnv, scopeID string, want []string) {
	t.Helper()
	got := scopeMemberIDs(t, te, scopeID)
	if !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
		t.Fatalf("%s members = %v, want %v", scopeID, got, want)
	}
}

func scopeMemberIDs(t *testing.T, te *testEnv, scopeID string) []string {
	t.Helper()
	details, err := te.store.GetScope(t.Context(), scopeID)
	if err != nil {
		t.Fatalf("GetScope(%s): %v", scopeID, err)
	}
	ids := make([]string, len(details.Members))
	for i, issue := range details.Members {
		ids[i] = issue.ID
	}
	return ids
}

func threeDigits(i int) string {
	return string([]byte{'0' + byte(i/100), '0' + byte((i/10)%10), '0' + byte(i%10)})
}

//go:build cgo

package embeddeddolt_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

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

func TestEmbeddedScopePagedCatalogAndMembers(t *testing.T) {
	te := newTestEnv(t, "scope_pages")
	ctx := t.Context()
	createScope(t, te, "scope-page", "Paged")
	createScope(t, te, "scope-page-empty", "Empty")
	if err := te.store.SetConfig(ctx, "status.custom", "archived:done"); err != nil {
		t.Fatalf("SetConfig(status.custom): %v", err)
	}
	issues := []*types.Issue{
		{ID: "scope-page-open", Title: "open", Description: "full", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
		{ID: "scope-page-progress", Title: "progress", Status: types.StatusInProgress, Priority: 2, IssueType: types.TypeTask},
		{ID: "scope-page-closed", Title: "closed", Status: types.StatusClosed, Priority: 2, IssueType: types.TypeTask},
		{ID: "scope-page-archived", Title: "archived", Status: types.Status("archived"), Priority: 2, IssueType: types.TypeTask},
		{ID: "scope-page-bug", Title: "bug", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeBug},
		{ID: "scope-page-blocker", Title: "blocker", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
		{ID: "scope-page-pinned-blocker", Title: "pinned blocker", Status: types.StatusPinned, Priority: 2, IssueType: types.TypeTask},
		{ID: "scope-page-pinned-dependent", Title: "pinned dependent", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
		{ID: "scope-page-deferred-parent", Title: "deferred parent", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic, DeferUntil: timePtr(time.Now().UTC().Add(time.Hour))},
		{ID: "scope-page-deferred-child", Title: "deferred child", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
	}
	if err := te.store.CreateIssues(ctx, issues, "tester"); err != nil {
		t.Fatalf("CreateIssues: %v", err)
	}
	if err := te.store.AddScopeMembers(ctx, "scope-page", []string{
		"scope-page-open", "scope-page-progress", "scope-page-closed", "scope-page-archived", "scope-page-bug", "scope-page-pinned-dependent", "scope-page-deferred-child",
	}); err != nil {
		t.Fatalf("AddScopeMembers: %v", err)
	}
	if err := te.store.AddLabel(ctx, "scope-page-open", "ctx:team-a", "tester"); err != nil {
		t.Fatalf("AddLabel: %v", err)
	}
	if err := te.store.AddDependency(ctx, &types.Dependency{
		IssueID: "scope-page-open", DependsOnID: "scope-page-blocker", Type: types.DepBlocks,
	}, "tester"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	if err := te.store.AddDependency(ctx, &types.Dependency{
		IssueID: "scope-page-pinned-dependent", DependsOnID: "scope-page-pinned-blocker", Type: types.DepBlocks,
	}, "tester"); err != nil {
		t.Fatalf("AddDependency(pinned): %v", err)
	}
	if err := te.store.AddDependency(ctx, &types.Dependency{
		IssueID: "scope-page-deferred-child", DependsOnID: "scope-page-deferred-parent", Type: types.DepParentChild,
	}, "tester"); err != nil {
		t.Fatalf("AddDependency(deferred parent): %v", err)
	}

	catalog, err := te.store.ListScopeCatalog(ctx, storage.ScopeCatalogRequest{Limit: 1})
	if err != nil {
		t.Fatalf("ListScopeCatalog: %v", err)
	}
	if len(catalog.Items) != 1 || catalog.Items[0].ID != "scope-page" || catalog.Items[0].MemberCount != 7 || catalog.Items[0].CompletedCount != 2 {
		t.Fatalf("catalog = %#v, want first scope with 7 members and 2 completed", catalog)
	}
	if catalog.TotalMatching != 2 || !catalog.HasMore || catalog.NextCursor == "" {
		t.Fatalf("catalog pagination = %#v, want total 2 and next cursor", catalog)
	}

	page, err := te.store.ListScopeMembers(ctx, "scope-page", storage.ScopeMemberPageRequest{Limit: 2})
	if err != nil {
		t.Fatalf("ListScopeMembers: %v", err)
	}
	if page.Scope.ID != "scope-page" || page.MemberCount != 7 || page.CompletedCount != 2 || page.TotalMatching != 7 || len(page.Members) != 2 || !page.HasMore {
		t.Fatalf("member page = %#v, want unfiltered counts and first page", page)
	}
	next, err := te.store.ListScopeMembers(ctx, "scope-page", storage.ScopeMemberPageRequest{Limit: 2, Cursor: page.NextCursor})
	if err != nil {
		t.Fatalf("ListScopeMembers(next): %v", err)
	}
	if next.TotalMatching != 7 || len(next.Members) != 2 || next.Members[0].ID <= page.Members[len(page.Members)-1].ID {
		t.Fatalf("next member page = %#v, want deterministic issue_id order", next)
	}

	completed, err := te.store.ListScopeMembers(ctx, "scope-page", storage.ScopeMemberPageRequest{Status: storage.ScopeMemberStatusCompleted})
	if err != nil {
		t.Fatalf("ListScopeMembers(completed): %v", err)
	}
	if completed.TotalMatching != 2 || len(completed.Members) != 2 {
		t.Fatalf("completed page = %#v, want closed and custom done rows", completed)
	}
	task, err := te.store.ListScopeMembers(ctx, "scope-page", storage.ScopeMemberPageRequest{Type: types.TypeTask})
	if err != nil {
		t.Fatalf("ListScopeMembers(type): %v", err)
	}
	if task.TotalMatching != 6 {
		t.Fatalf("exact type total = %d, want 6", task.TotalMatching)
	}
	contextPage, err := te.store.ListScopeMembers(ctx, "scope-page", storage.ScopeMemberPageRequest{Contexts: []string{"team-a"}})
	if err != nil {
		t.Fatalf("ListScopeMembers(context): %v", err)
	}
	if contextPage.TotalMatching != 1 || len(contextPage.Members) != 1 || contextPage.Members[0].ID != "scope-page-open" {
		t.Fatalf("context page = %#v, want only the exact ctx:team-a member", contextPage)
	}
	ready, err := te.store.ListScopeMembers(ctx, "scope-page", storage.ScopeMemberPageRequest{Status: storage.ScopeMemberStatusReady})
	if err != nil {
		t.Fatalf("ListScopeMembers(ready): %v", err)
	}
	if ready.TotalMatching != 2 || len(ready.Members) != 2 || ready.Members[0].ID != "scope-page-pinned-dependent" || ready.Members[1].ID != "scope-page-progress" {
		t.Fatalf("ready page = %#v, want progress and pinned-dependent only", ready)
	}
	if _, err := te.store.ListScopeMembers(ctx, "scope-page", storage.ScopeMemberPageRequest{Status: storage.ScopeMemberStatusCompleted, Cursor: page.NextCursor}); !errors.Is(err, storage.ErrScopeCursorInvalid) {
		t.Fatalf("mismatched member cursor error = %v, want ErrScopeCursorInvalid", err)
	}
	if _, err := te.store.ListScopeMembers(ctx, "scope-page", storage.ScopeMemberPageRequest{Status: storage.ScopeMemberStatus("unknown")}); !errors.Is(err, storage.ErrScopeInvalid) {
		t.Fatalf("invalid member status error = %v, want ErrScopeInvalid", err)
	}
	if _, err := te.store.ListScopeMembers(ctx, "scope-page", storage.ScopeMemberPageRequest{Cursor: "not-a-scope-cursor"}); !errors.Is(err, storage.ErrScopeCursorInvalid) {
		t.Fatalf("invalid member cursor error = %v, want ErrScopeCursorInvalid", err)
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

func timePtr(value time.Time) *time.Time { return &value }

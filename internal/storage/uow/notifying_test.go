package uow

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/hooks"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
)

// fakeHookRunner records the script-hook events fired by the notifying unit of
// work, standing in for the fire-and-forget *hooks.Runner.
type fakeHookRunner struct {
	fired []hookFire
}

type hookFire struct {
	event   string
	issueID string
}

func (r *fakeHookRunner) Run(event string, issue *types.Issue) {
	id := ""
	if issue != nil {
		id = issue.ID
	}
	r.fired = append(r.fired, hookFire{event: event, issueID: id})
}

// uowState is a tiny in-memory issue store shared across mock unit-of-work
// use cases so snapshot reads reflect prior mutations.
type uowState struct {
	issues map[string]*types.Issue
	deps   map[string][]*types.Dependency
}

func newUOWState() *uowState {
	return &uowState{issues: map[string]*types.Issue{}, deps: map[string][]*types.Dependency{}}
}

func (s *uowState) put(issue *types.Issue) { clone := *issue; s.issues[issue.ID] = &clone }
func (s *uowState) get(id string) *types.Issue {
	if issue, ok := s.issues[id]; ok {
		clone := *issue
		return &clone
	}
	return nil
}

// ── mock unit of work ───────────────────────────────────────────────

type mockJournalUOW struct {
	st          *uowState
	committed   bool
	commitCount int
}

func (m *mockJournalUOW) Close(ctx context.Context) {}
func (m *mockJournalUOW) Commit(ctx context.Context, message string) error {
	m.committed = true
	m.commitCount++
	return nil
}
func (m *mockJournalUOW) ConfigUseCase() domain.ConfigUseCase         { return nil }
func (m *mockJournalUOW) DoltRemoteUseCase() domain.DoltRemoteUseCase { return nil }
func (m *mockJournalUOW) BootstrapUseCase() domain.BootstrapUseCase   { return nil }
func (m *mockJournalUOW) RawSQLUseCase() domain.RawSQLUseCase         { return nil }
func (m *mockJournalUOW) IssueUseCase() domain.IssueUseCase           { return &mockIssueUC{st: m.st} }
func (m *mockJournalUOW) DependencyUseCase() domain.DependencyUseCase { return &mockDepUC{st: m.st} }
func (m *mockJournalUOW) LabelUseCase() domain.LabelUseCase           { return &mockLabelUC{st: m.st} }
func (m *mockJournalUOW) CommentUseCase() domain.CommentUseCase       { return &mockCommentUC{st: m.st} }

type mockJournalProvider struct {
	st   *uowState
	last *mockJournalUOW
}

func (p *mockJournalProvider) NewUOW(ctx context.Context) (UnitOfWork, error) {
	p.last = &mockJournalUOW{st: p.st}
	return p.last, nil
}
func (p *mockJournalProvider) Close(ctx context.Context) error { return nil }

// ── mock use cases (embed interface for passthrough of unused methods) ──

type mockIssueUC struct {
	domain.IssueUseCase
	st *uowState
}

func (u *mockIssueUC) GetIssue(ctx context.Context, id string) (*types.Issue, error) {
	if issue := u.st.get(id); issue != nil {
		return issue, nil
	}
	return nil, domainNotFound
}

func (u *mockIssueUC) CreateIssue(ctx context.Context, params domain.CreateIssueParams, actor string) (domain.CreateIssueResult, error) {
	u.st.put(params.Issue)
	return domain.CreateIssueResult{Issue: u.st.get(params.Issue.ID)}, nil
}

func (u *mockIssueUC) UpdateIssue(ctx context.Context, id string, updates map[string]any, actor string) error {
	if issue := u.st.issues[id]; issue != nil {
		if title, ok := updates["title"].(string); ok {
			issue.Title = title
		}
	}
	return nil
}

func (u *mockIssueUC) CloseIssue(ctx context.Context, id string, params domain.CloseIssueParams, actor string) (domain.CloseIssueResult, error) {
	if issue := u.st.issues[id]; issue != nil {
		issue.Status = types.StatusClosed
	}
	return domain.CloseIssueResult{Issue: u.st.get(id), Closed: true}, nil
}

func (u *mockIssueUC) DeleteIssue(ctx context.Context, id, actor string) (domain.DeleteIssuesResult, error) {
	delete(u.st.issues, id)
	delete(u.st.deps, id)
	return domain.DeleteIssuesResult{DeletedCount: 1}, nil
}

type mockDepUC struct {
	domain.DependencyUseCase
	st *uowState
}

func (u *mockDepUC) AddDependency(ctx context.Context, dep *types.Dependency, actor string) error {
	u.st.deps[dep.IssueID] = append(u.st.deps[dep.IssueID], dep)
	return nil
}

func (u *mockDepUC) RemoveDependency(ctx context.Context, issueID, dependsOnID, actor string) error {
	kept := u.st.deps[issueID][:0]
	for _, d := range u.st.deps[issueID] {
		if d.DependsOnID != dependsOnID {
			kept = append(kept, d)
		}
	}
	u.st.deps[issueID] = kept
	return nil
}

type mockLabelUC struct {
	domain.LabelUseCase
	st *uowState
}

func (u *mockLabelUC) AddLabel(ctx context.Context, issueID, label, actor string) error {
	if issue := u.st.issues[issueID]; issue != nil {
		issue.Labels = append(issue.Labels, label)
	}
	return nil
}

type mockCommentUC struct {
	domain.CommentUseCase
	st *uowState
}

func (u *mockCommentUC) AddCommentToIssue(ctx context.Context, issueID, author, text string) (*types.Comment, error) {
	return &types.Comment{IssueID: issueID, Author: author, Text: text}, nil
}

var domainNotFound = &notFoundError{}

type notFoundError struct{}

func (*notFoundError) Error() string { return "not found" }

// TestNotifyingUOWFiresHooksForEveryOp drives mutations through the wrapped unit
// of work and asserts the script-hook Runner fires the right event for each,
// only after commit. This is the retained bug fix: the unit-of-work plumbing
// previously fired no hooks at all.
func TestNotifyingUOWFiresHooksForEveryOp(t *testing.T) {
	ctx := context.Background()
	st := newUOWState()
	runner := &fakeHookRunner{}
	provider := NewNotifyingProvider(&mockJournalProvider{st: st}, Sinks{Hook: runner})

	err := RunTx(ctx, provider, func(ctx context.Context, uw UnitOfWork) (string, error) {
		if _, err := uw.IssueUseCase().CreateIssue(ctx, domain.CreateIssueParams{Issue: &types.Issue{ID: "a-1", Title: "one"}}, "actor"); err != nil {
			return "", err
		}
		if _, err := uw.IssueUseCase().CreateIssue(ctx, domain.CreateIssueParams{Issue: &types.Issue{ID: "a-2", Title: "two"}}, "actor"); err != nil {
			return "", err
		}
		if err := uw.IssueUseCase().UpdateIssue(ctx, "a-1", map[string]any{"title": "renamed"}, "actor"); err != nil {
			return "", err
		}
		if err := uw.LabelUseCase().AddLabel(ctx, "a-1", "urgent", "actor"); err != nil {
			return "", err
		}
		if err := uw.DependencyUseCase().AddDependency(ctx, &types.Dependency{IssueID: "a-1", DependsOnID: "a-2", Type: types.DepBlocks}, "actor"); err != nil {
			return "", err
		}
		if err := uw.DependencyUseCase().RemoveDependency(ctx, "a-1", "a-2", "actor"); err != nil {
			return "", err
		}
		if _, err := uw.CommentUseCase().AddCommentToIssue(ctx, "a-1", "actor", "note"); err != nil {
			return "", err
		}
		if _, err := uw.IssueUseCase().CloseIssue(ctx, "a-1", domain.CloseIssueParams{Reason: "done"}, "actor"); err != nil {
			return "", err
		}
		if _, err := uw.IssueUseCase().DeleteIssue(ctx, "a-2", "actor"); err != nil {
			return "", err
		}
		return "bd: mixed batch", nil
	})
	if err != nil {
		t.Fatalf("run tx: %v", err)
	}

	// create/update/close fire their namesake events; dependency and label
	// changes fire update; delete fires no script hook (matching the
	// DoltStorage plumbing).
	wantHooks := []hookFire{
		{event: hooks.EventCreate, issueID: "a-1"},
		{event: hooks.EventCreate, issueID: "a-2"},
		{event: hooks.EventUpdate, issueID: "a-1"}, // rename
		{event: hooks.EventUpdate, issueID: "a-1"}, // label
		{event: hooks.EventUpdate, issueID: "a-1"}, // dep_add
		{event: hooks.EventUpdate, issueID: "a-1"}, // dep_remove
		{event: hooks.EventUpdate, issueID: "a-1"}, // comment
		{event: hooks.EventClose, issueID: "a-1"},
		// no hook for the delete of a-2
	}
	if len(runner.fired) != len(wantHooks) {
		t.Fatalf("expected %d hook fires, got %d: %+v", len(wantHooks), len(runner.fired), runner.fired)
	}
	for i, want := range wantHooks {
		if runner.fired[i] != want {
			t.Fatalf("hook fire %d: got %+v, want %+v", i, runner.fired[i], want)
		}
	}
}

// TestNotifyingUOWFiresOnlyOnCommit ensures no hooks fire without a commit
// (empty commit message) or on rollback (error).
func TestNotifyingUOWFiresOnlyOnCommit(t *testing.T) {
	ctx := context.Background()

	t.Run("empty commit message fires nothing", func(t *testing.T) {
		st := newUOWState()
		runner := &fakeHookRunner{}
		provider := NewNotifyingProvider(&mockJournalProvider{st: st}, Sinks{Hook: runner})
		err := RunTx(ctx, provider, func(ctx context.Context, uw UnitOfWork) (string, error) {
			_, err := uw.IssueUseCase().CreateIssue(ctx, domain.CreateIssueParams{Issue: &types.Issue{ID: "a-1", Title: "one"}}, "actor")
			return "", err // empty commit message -> no commit
		})
		if err != nil {
			t.Fatalf("run tx: %v", err)
		}
		if len(runner.fired) != 0 {
			t.Fatalf("expected no hooks without commit, got %d", len(runner.fired))
		}
	})

	t.Run("error rolls back and fires nothing", func(t *testing.T) {
		st := newUOWState()
		runner := &fakeHookRunner{}
		provider := NewNotifyingProvider(&mockJournalProvider{st: st}, Sinks{Hook: runner})
		sentinel := &notFoundError{}
		err := RunTx(ctx, provider, func(ctx context.Context, uw UnitOfWork) (string, error) {
			if _, err := uw.IssueUseCase().CreateIssue(ctx, domain.CreateIssueParams{Issue: &types.Issue{ID: "a-1", Title: "one"}}, "actor"); err != nil {
				return "", err
			}
			return "bd: create", sentinel
		})
		if err == nil {
			t.Fatal("expected error")
		}
		if len(runner.fired) != 0 {
			t.Fatalf("expected no hooks on rollback, got %d", len(runner.fired))
		}
	})
}

// TestNewNotifyingProviderNoSinksPassthrough verifies that with no sinks
// configured the inner provider is returned unwrapped (zero overhead).
func TestNewNotifyingProviderNoSinksPassthrough(t *testing.T) {
	inner := &mockJournalProvider{st: newUOWState()}
	got := NewNotifyingProvider(inner, Sinks{})
	if got != inner {
		t.Fatalf("empty sinks should return inner provider unwrapped, got %T", got)
	}
}

// Package uow — notifying.go
//
// This fires the legacy script-hook Runner after commits on the unit-of-work
// write path (the second of bd's two write plumbings; the first is the
// DoltStorage decorator chain). Historically only the DoltStorage plumbing ran
// hooks after a mutation (via the script-hook decorator); the unit-of-work
// plumbing fired nothing, so any script wired to .beads/hooks/ silently missed
// every mutation that went through it. A NotifyingProvider closes that gap: it
// wraps a UnitOfWorkProvider so that every committed issue mutation performed
// through a unit of work runs the same fire-and-forget hooks the DoltStorage
// plumbing runs.
//
// Hooks fire strictly post-commit: mutations are buffered as they flow through
// the wrapped use cases (the buffered snapshot is read inside the transaction,
// so it reflects the mutation), and the buffer is drained to the Runner only
// after Commit succeeds. A rolled-back unit of work fires nothing.
//
// The durable mutations journal is a separate concern: it is written at the
// issueops seam inside the mutation's own transaction (see
// issueops.RecordMutationInTx), so it covers both plumbings structurally and is
// not wired here.
package uow

import (
	"context"

	"github.com/steveyegge/beads/internal/hooks"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
)

// hookRunner is the subset of *hooks.Runner the unit-of-work path needs — the
// same fire-and-forget contract the DoltStorage plumbing uses. Declared as an
// interface so tests can supply a recording fake.
type hookRunner interface {
	Run(event string, issue *types.Issue)
}

// Sinks are the post-commit notification targets. Hook may be nil to disable.
type Sinks struct {
	// Hook is the legacy script-hook Runner (*hooks.Runner). Its behavior is
	// unchanged; the unit-of-work plumbing simply now feeds it too.
	Hook hookRunner
}

func (s Sinks) empty() bool { return s.Hook == nil }

// NewNotifyingProvider wraps inner so committed mutations fire hooks post-commit.
// When no sinks are configured the inner provider is returned unwrapped (zero
// overhead).
func NewNotifyingProvider(inner UnitOfWorkProvider, sinks Sinks) UnitOfWorkProvider {
	if sinks.empty() {
		return inner
	}
	return &notifyingProvider{inner: inner, sinks: sinks}
}

type notifyingProvider struct {
	inner UnitOfWorkProvider
	sinks Sinks
}

func (p *notifyingProvider) NewUOW(ctx context.Context) (UnitOfWork, error) {
	uw, err := p.inner.NewUOW(ctx)
	if err != nil {
		return nil, err
	}
	return &notifyingUOW{UnitOfWork: uw, rec: &recorder{}, sinks: p.sinks}, nil
}

func (p *notifyingProvider) Close(ctx context.Context) error {
	return p.inner.Close(ctx)
}

// mutationEntry is a buffered post-commit hook notification: the resolved hook
// event and the post-mutation issue snapshot to pass to the Runner.
type mutationEntry struct {
	event string
	issue *types.Issue
}

// recorder buffers hook notifications accumulated during one unit of work.
type recorder struct {
	entries []mutationEntry
}

// record resolves op to a script-hook event and buffers a notification. Ops with
// no hook (delete) and mutations with no snapshot are dropped — matching the
// DoltStorage plumbing, where deletes fire no hook.
func (r *recorder) record(op string, issue *types.Issue) {
	event, ok := hookEventForOp(op)
	if !ok || issue == nil {
		return
	}
	r.entries = append(r.entries, mutationEntry{event: event, issue: issue})
}

func (r *recorder) drain() []mutationEntry { e := r.entries; r.entries = nil; return e }

// hookEventForOp maps a mutation op to its legacy script-hook event, matching the
// DoltStorage plumbing: create/update/close fire their namesake hooks,
// dependency changes fire the update hook, and deletes fire no script hook.
func hookEventForOp(op string) (string, bool) {
	switch op {
	case "create":
		return hooks.EventCreate, true
	case "update", "dep_add", "dep_remove":
		return hooks.EventUpdate, true
	case "close":
		return hooks.EventClose, true
	default:
		return "", false
	}
}

// notifyingUOW wraps a UnitOfWork, buffering committed mutations and firing
// hooks after commit. The embedded UnitOfWork provides passthrough for use cases
// and methods that produce no mutation notifications.
type notifyingUOW struct {
	UnitOfWork
	rec   *recorder
	sinks Sinks

	issueUC   domain.IssueUseCase
	depUC     domain.DependencyUseCase
	labelUC   domain.LabelUseCase
	commentUC domain.CommentUseCase
}

func (u *notifyingUOW) snapshotter() *snapshotter {
	return &snapshotter{
		getIssue: u.UnitOfWork.IssueUseCase().GetIssue,
	}
}

func (u *notifyingUOW) IssueUseCase() domain.IssueUseCase {
	if u.issueUC == nil {
		u.issueUC = &recordingIssueUC{IssueUseCase: u.UnitOfWork.IssueUseCase(), rec: u.rec, snap: u.snapshotter()}
	}
	return u.issueUC
}

func (u *notifyingUOW) DependencyUseCase() domain.DependencyUseCase {
	if u.depUC == nil {
		u.depUC = &recordingDepUC{DependencyUseCase: u.UnitOfWork.DependencyUseCase(), rec: u.rec, snap: u.snapshotter()}
	}
	return u.depUC
}

func (u *notifyingUOW) LabelUseCase() domain.LabelUseCase {
	if u.labelUC == nil {
		u.labelUC = &recordingLabelUC{LabelUseCase: u.UnitOfWork.LabelUseCase(), rec: u.rec, snap: u.snapshotter()}
	}
	return u.labelUC
}

func (u *notifyingUOW) CommentUseCase() domain.CommentUseCase {
	if u.commentUC == nil {
		u.commentUC = &recordingCommentUC{CommentUseCase: u.UnitOfWork.CommentUseCase(), rec: u.rec, snap: u.snapshotter()}
	}
	return u.commentUC
}

// Commit commits the underlying unit of work, then fires the buffered hooks.
// Hooks are fire-and-forget, so the user's committed mutation always succeeds.
func (u *notifyingUOW) Commit(ctx context.Context, message string) error {
	if err := u.UnitOfWork.Commit(ctx, message); err != nil {
		return err
	}
	entries := u.rec.drain()
	if u.sinks.Hook == nil {
		return nil
	}
	for _, e := range entries {
		u.sinks.Hook.Run(e.event, e.issue)
	}
	return nil
}

// snapshotter reads post-mutation issue state within the transaction so the
// buffered notifications carry the full issue after the mutation.
type snapshotter struct {
	getIssue func(context.Context, string) (*types.Issue, error)
}

func (s *snapshotter) issue(ctx context.Context, id string) *types.Issue {
	issue, err := s.getIssue(ctx, id)
	if err != nil {
		return nil
	}
	return issue
}

// ── Issue use case ──────────────────────────────────────────────────

type recordingIssueUC struct {
	domain.IssueUseCase
	rec  *recorder
	snap *snapshotter
}

func (u *recordingIssueUC) CreateIssue(ctx context.Context, params domain.CreateIssueParams, actor string) (domain.CreateIssueResult, error) {
	res, err := u.IssueUseCase.CreateIssue(ctx, params, actor)
	if err == nil && res.Issue != nil {
		u.rec.record("create", res.Issue)
	}
	return res, err
}

func (u *recordingIssueUC) CreateIssues(ctx context.Context, params []domain.CreateIssueParams, actor string) (domain.CreateIssuesResult, error) {
	res, err := u.IssueUseCase.CreateIssues(ctx, params, actor)
	if err == nil {
		for _, issue := range res.Issues {
			u.rec.record("create", issue)
		}
	}
	return res, err
}

func (u *recordingIssueUC) UpdateIssue(ctx context.Context, id string, updates map[string]any, actor string) error {
	if err := u.IssueUseCase.UpdateIssue(ctx, id, updates, actor); err != nil {
		return err
	}
	u.rec.record("update", u.snap.issue(ctx, id))
	return nil
}

func (u *recordingIssueUC) ApplyUpdate(ctx context.Context, id string, spec domain.UpdateSpec, actor string) (*types.Issue, error) {
	issue, err := u.IssueUseCase.ApplyUpdate(ctx, id, spec, actor)
	if err == nil {
		snap := issue
		if snap == nil {
			snap = u.snap.issue(ctx, id)
		}
		u.rec.record("update", snap)
	}
	return issue, err
}

func (u *recordingIssueUC) ApplyIssueGraph(ctx context.Context, plan domain.GraphPlan, actor string) (domain.GraphApplyResult, error) {
	res, err := u.IssueUseCase.ApplyIssueGraph(ctx, plan, actor)
	if err == nil {
		for _, realID := range res.IDs {
			u.rec.record("create", u.snap.issue(ctx, realID))
		}
	}
	return res, err
}

func (u *recordingIssueUC) CloseIssue(ctx context.Context, id string, params domain.CloseIssueParams, actor string) (domain.CloseIssueResult, error) {
	res, err := u.IssueUseCase.CloseIssue(ctx, id, params, actor)
	if err == nil && res.Closed {
		snap := res.Issue
		if snap == nil {
			snap = u.snap.issue(ctx, id)
		}
		u.rec.record("close", snap)
	}
	return res, err
}

func (u *recordingIssueUC) ReopenIssue(ctx context.Context, id string, params domain.ReopenIssueParams, actor string) (domain.ReopenIssueResult, error) {
	res, err := u.IssueUseCase.ReopenIssue(ctx, id, params, actor)
	if err == nil && res.Reopened {
		snap := res.Issue
		if snap == nil {
			snap = u.snap.issue(ctx, id)
		}
		u.rec.record("update", snap)
	}
	return res, err
}

func (u *recordingIssueUC) ClaimIssue(ctx context.Context, id, actor string) (domain.ClaimResult, error) {
	res, err := u.IssueUseCase.ClaimIssue(ctx, id, actor)
	if err == nil && !res.AlreadyClaimed {
		u.rec.record("update", u.snap.issue(ctx, id))
	}
	return res, err
}

func (u *recordingIssueUC) ClaimIssueIfOpen(ctx context.Context, id, actor string) (domain.ClaimResult, error) {
	res, err := u.IssueUseCase.ClaimIssueIfOpen(ctx, id, actor)
	if err == nil && !res.AlreadyClaimed {
		u.rec.record("update", u.snap.issue(ctx, id))
	}
	return res, err
}

func (u *recordingIssueUC) DeleteIssue(ctx context.Context, id, actor string) (domain.DeleteIssuesResult, error) {
	// Deletes fire no script hook (matching the DoltStorage plumbing), so there
	// is nothing to record here — the journal at the issueops seam records it.
	return u.IssueUseCase.DeleteIssue(ctx, id, actor)
}

// ── Dependency use case ─────────────────────────────────────────────

type recordingDepUC struct {
	domain.DependencyUseCase
	rec  *recorder
	snap *snapshotter
}

func (u *recordingDepUC) AddDependency(ctx context.Context, dep *types.Dependency, actor string) error {
	if err := u.DependencyUseCase.AddDependency(ctx, dep, actor); err != nil {
		return err
	}
	u.rec.record("dep_add", u.snap.issue(ctx, dep.IssueID))
	return nil
}

func (u *recordingDepUC) RemoveDependency(ctx context.Context, issueID, dependsOnID, actor string) error {
	if err := u.DependencyUseCase.RemoveDependency(ctx, issueID, dependsOnID, actor); err != nil {
		return err
	}
	u.rec.record("dep_remove", u.snap.issue(ctx, issueID))
	return nil
}

// ── Label use case ──────────────────────────────────────────────────

type recordingLabelUC struct {
	domain.LabelUseCase
	rec  *recorder
	snap *snapshotter
}

func (u *recordingLabelUC) labelUpdate(ctx context.Context, issueID string) {
	u.rec.record("update", u.snap.issue(ctx, issueID))
}

func (u *recordingLabelUC) AddLabel(ctx context.Context, issueID, label, actor string) error {
	if err := u.LabelUseCase.AddLabel(ctx, issueID, label, actor); err != nil {
		return err
	}
	u.labelUpdate(ctx, issueID)
	return nil
}

func (u *recordingLabelUC) RemoveLabel(ctx context.Context, issueID, label, actor string) error {
	if err := u.LabelUseCase.RemoveLabel(ctx, issueID, label, actor); err != nil {
		return err
	}
	u.labelUpdate(ctx, issueID)
	return nil
}

func (u *recordingLabelUC) AddLabels(ctx context.Context, issueID string, labels []string, actor string) error {
	if err := u.LabelUseCase.AddLabels(ctx, issueID, labels, actor); err != nil {
		return err
	}
	u.labelUpdate(ctx, issueID)
	return nil
}

func (u *recordingLabelUC) RemoveLabels(ctx context.Context, issueID string, labels []string, actor string) error {
	if err := u.LabelUseCase.RemoveLabels(ctx, issueID, labels, actor); err != nil {
		return err
	}
	u.labelUpdate(ctx, issueID)
	return nil
}

func (u *recordingLabelUC) SetLabels(ctx context.Context, issueID string, labels []string, actor string) error {
	if err := u.LabelUseCase.SetLabels(ctx, issueID, labels, actor); err != nil {
		return err
	}
	u.labelUpdate(ctx, issueID)
	return nil
}

// ── Comment use case ────────────────────────────────────────────────

type recordingCommentUC struct {
	domain.CommentUseCase
	rec  *recorder
	snap *snapshotter
}

func (u *recordingCommentUC) AddCommentToIssue(ctx context.Context, issueID, author, text string) (*types.Comment, error) {
	comment, err := u.CommentUseCase.AddCommentToIssue(ctx, issueID, author, text)
	if err == nil {
		u.rec.record("update", u.snap.issue(ctx, issueID))
	}
	return comment, err
}

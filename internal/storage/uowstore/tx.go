package uowstore

import (
	"context"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
)

// uowTransaction is a storage.Transaction view bound to ONE open
// uow.UnitOfWork. It is only valid inside the RunInTransaction callback; the
// underlying pinned connection is released when the UOW commits or rolls back,
// so callers must not retain it (same hazard as the embedded *sql.Tx view).
//
// The embedded unsupportedTransaction shell returns typed *storage.ErrUnsupported
// for the 21 Transaction methods this spike does not implement; the three
// overridden below (GetIssue, CloseIssue, AddDependency) are the minimal
// read-check-act set that proves multi-statement atomicity across two tables
// (issues + dependencies). Because a stubbed method's error propagates out of
// fn, calling one also exercises the rollback-on-domain-error path.
type uowTransaction struct {
	unsupportedTransaction // generated: the 21 non-slice methods return typed ErrUnsupported

	u uow.UnitOfWork
}

var _ storage.Transaction = (*uowTransaction)(nil)

// GetIssue reads through the same shared helper as (*uowStore).GetIssue, so
// read-your-writes inside the transaction and the storage.ErrNotFound
// translation behave identically to the non-transactional path.
func (t *uowTransaction) GetIssue(ctx context.Context, id string) (*types.Issue, error) {
	return getIssueInUOW(ctx, t.u, id)
}

// CloseIssue mutates through the shared close helper (issue-vs-wisp probe →
// CloseIssue/CloseWisp). The is_blocked recompute fires inside this same tx.
func (t *uowTransaction) CloseIssue(ctx context.Context, id, reason, actor, session string) error {
	return closeIssueInUOW(ctx, t.u, id, reason, actor, session)
}

// AddDependency mirrors embeddedTransaction's IsActiveWispInTx routing: a wisp
// source uses the wisp-dependency table, otherwise the regular table. The
// use-case runs its blocking-cycle check (depRepo.HasCycle) in the same tx.
func (t *uowTransaction) AddDependency(ctx context.Context, dep *types.Dependency, actor string) error {
	if dep == nil {
		return fmt.Errorf("dependency must not be nil")
	}
	isWisp, err := isWispInUOW(ctx, t.u, dep.IssueID)
	if err != nil {
		return err
	}
	if isWisp {
		return t.u.DependencyUseCase().AddWispDependency(ctx, dep, actor)
	}
	return t.u.DependencyUseCase().AddDependency(ctx, dep, actor)
}

// RunInTransaction implements storage.Storage.RunInTransaction over ONE
// uow.UnitOfWork: fn's mutations share one pinned connection/transaction and
// become exactly ONE DOLT_COMMIT carrying commitMsg (outcome-derived by the
// caller, per §4.0: the description travels on Commit).
//
// Retry semantics are inherited from uow.RunInTx and are NOT reimplemented here
// (phase-aware):
//
//   - Pre-commit transients (NewUOW, a connection pin, or a serialization
//     failure raised by fn before COMMIT) replay the whole sequence with a
//     FRESH UnitOfWork and a FRESH Transaction view; the server already rolled
//     the prior attempt back.
//   - Domain errors returned by fn (validation, not-found, and a tx stub's
//     *storage.ErrUnsupported) are permanent — no retry — and the deferred
//     Close rolls back.
//   - A connection loss AT or AFTER commit surfaces uow.ErrCommitIndeterminate
//     and is NEVER retried (double-apply risk); the caller must reconcile by
//     re-reading. "nothing to commit" (a read-only fn) maps to success with no
//     new version commit.
//
// fn must therefore be replay-safe: no external side effects, and no state
// captured from a previous attempt's view (the view is constructed INSIDE the
// retry closure below — never hoist it).
func (s *uowStore) RunInTransaction(ctx context.Context, commitMsg string, fn func(tx storage.Transaction) error) error {
	if strings.TrimSpace(commitMsg) == "" {
		// Embedded semantics for "" are "SQL-commit, defer the version commit"
		// (embeddeddolt RunInTransaction), but the uow Tx has no
		// commit-without-DOLT_COMMIT primitive and DOLT_COMMIT rejects empty
		// messages. This is unreachable from the CLI on the spike path
		// (transactHonoringAutoCommit only blanks the message in embedded mode,
		// dolt_autocommit.go:31), so refuse loudly instead of diverging version
		// history.
		return fmt.Errorf("uowstore spike: RunInTransaction requires a non-empty commit message; deferred (blank-message) version commits are embedded-only (gastownhall/beads#4547)")
	}
	return uow.RunInTx(ctx, s.provider, commitMsg, func(u uow.UnitOfWork) error {
		return fn(&uowTransaction{u: u}) // view constructed INSIDE the retry closure — never hoist
	})
}

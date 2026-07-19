package db

import (
	"errors"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
)

func (s *testSuite) TestIssueUseCase_CloseIssueChecked() {
	s.Run("DirectBlockerRefusesWithSentinelAndNamesBlocker", s.uccCloseCheckedDirectBlockerRefuses)
	s.Run("TransitivelyBlockedChildClosesWithoutForce", s.uccCloseCheckedTransitiveCloses)
	s.Run("ForceClosesDespiteDirectBlocker", s.uccCloseCheckedForceCloses)
	s.Run("AlreadyClosedMatchesUncheckedSemantics", s.uccCloseCheckedAlreadyClosed)
	s.Run("UnblockedClosesAndReturnsIssue", s.uccCloseCheckedUnblockedCloses)
	s.Run("WispDirectBlockerRefuses", s.uccCloseWispCheckedDirectBlockerRefuses)
	s.Run("WispForceClosesDespiteDirectBlocker", s.uccCloseWispCheckedForceCloses)
}

func (s *testSuite) uccCloseCheckedDirectBlockerRefuses() {
	s.seedIssueRow("bd-ucc-clc-src")
	s.seedIssueRow("bd-ucc-clc-tgt")
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-ucc-clc-src", "bd-ucc-clc-tgt", types.DepBlocks), "tester", domain.DepInsertOpts{}))
	s.Require().True(s.isBlocked("bd-ucc-clc-src"))

	res, err := s.issueUseCase().CloseIssueChecked(s.Ctx(), "bd-ucc-clc-src",
		domain.CloseIssueParams{Reason: "done"}, "tester", false)
	s.Require().Error(err)
	s.True(errors.Is(err, storage.ErrCloseBlocked), "refusal must carry the shared sentinel")
	s.ErrorContains(err, "is blocked by")
	s.ErrorContains(err, "bd-ucc-clc-tgt", "message must name the live blocker")
	s.False(res.Closed)

	// The refused issue stays open.
	var status string
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT status FROM issues WHERE id = ?", "bd-ucc-clc-src").Scan(&status))
	s.Equal(string(types.StatusOpen), status)
}

func (s *testSuite) uccCloseCheckedTransitiveCloses() {
	// parent has a live direct blocker; child inherits is_blocked=1 through the
	// parent-child edge but has no direct blocker of its own, so the historical
	// predicate (blocked && len(blockers) > 0) lets it close without force.
	s.seedIssueRow("bd-ucc-clc-tr-blocker")
	s.seedIssueRow("bd-ucc-clc-tr-parent")
	s.seedIssueRow("bd-ucc-clc-tr-child")
	dep := s.depRepo()
	s.Require().NoError(dep.Insert(s.Ctx(),
		newDep("bd-ucc-clc-tr-parent", "bd-ucc-clc-tr-blocker", types.DepBlocks), "tester", domain.DepInsertOpts{}))
	s.Require().NoError(dep.Insert(s.Ctx(),
		newDep("bd-ucc-clc-tr-child", "bd-ucc-clc-tr-parent", types.DepParentChild), "tester", domain.DepInsertOpts{}))
	s.Require().True(s.isBlocked("bd-ucc-clc-tr-child"), "child must inherit is_blocked from blocked parent")

	// Predicate parity: is_blocked=1 but no live direct blocker.
	blocked, blockers, err := s.depUseCase().IsBlocked(s.Ctx(), "bd-ucc-clc-tr-child")
	s.Require().NoError(err)
	s.True(blocked)
	s.Empty(blockers, "transitively-blocked child has no direct blocker")

	res, err := s.issueUseCase().CloseIssueChecked(s.Ctx(), "bd-ucc-clc-tr-child",
		domain.CloseIssueParams{Reason: "done"}, "tester", false)
	s.Require().NoError(err, "transitively-blocked child must close without force")
	s.True(res.Closed)
	s.Require().NotNil(res.Issue)
	s.Equal(types.StatusClosed, res.Issue.Status)
}

func (s *testSuite) uccCloseCheckedForceCloses() {
	s.seedIssueRow("bd-ucc-clc-fsrc")
	s.seedIssueRow("bd-ucc-clc-ftgt")
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-ucc-clc-fsrc", "bd-ucc-clc-ftgt", types.DepBlocks), "tester", domain.DepInsertOpts{}))
	s.Require().True(s.isBlocked("bd-ucc-clc-fsrc"))

	res, err := s.issueUseCase().CloseIssueChecked(s.Ctx(), "bd-ucc-clc-fsrc",
		domain.CloseIssueParams{Reason: "override"}, "tester", true)
	s.Require().NoError(err, "force must bypass the block guard")
	s.True(res.Closed)
	s.Require().NotNil(res.Issue)
	s.Equal(types.StatusClosed, res.Issue.Status)
}

func (s *testSuite) uccCloseCheckedAlreadyClosed() {
	s.seedIssueRow("bd-ucc-clc-idem")
	uc := s.issueUseCase()

	first, err := uc.CloseIssueChecked(s.Ctx(), "bd-ucc-clc-idem",
		domain.CloseIssueParams{Reason: "first"}, "tester", false)
	s.Require().NoError(err)
	s.True(first.Closed)

	// A second checked close reports the same already-closed semantics as the
	// unchecked verb: Closed == false, issue still closed.
	second, err := uc.CloseIssueChecked(s.Ctx(), "bd-ucc-clc-idem",
		domain.CloseIssueParams{Reason: "second"}, "tester", false)
	s.Require().NoError(err)
	s.False(second.Closed)
	s.Require().NotNil(second.Issue)
	s.Equal(types.StatusClosed, second.Issue.Status)
}

func (s *testSuite) uccCloseCheckedUnblockedCloses() {
	s.seedIssueRow("bd-ucc-clc-free")
	res, err := s.issueUseCase().CloseIssueChecked(s.Ctx(), "bd-ucc-clc-free",
		domain.CloseIssueParams{Reason: "done", Session: "sess-1"}, "tester", false)
	s.Require().NoError(err)
	s.True(res.Closed)
	s.Require().NotNil(res.Issue)
	s.Equal("bd-ucc-clc-free", res.Issue.ID)
	s.Equal(types.StatusClosed, res.Issue.Status)
}

func (s *testSuite) uccCloseWispCheckedDirectBlockerRefuses() {
	s.seedWispRow("bd-ucc-wclc-src")
	s.seedWispRow("bd-ucc-wclc-tgt")
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-ucc-wclc-src", "bd-ucc-wclc-tgt", types.DepBlocks), "tester", domain.DepInsertOpts{UseWispsTable: true}))

	blocked, blockers, err := s.depUseCase().IsWispBlocked(s.Ctx(), "bd-ucc-wclc-src")
	s.Require().NoError(err)
	s.Require().True(blocked)
	s.Require().Contains(blockers, "bd-ucc-wclc-tgt")

	res, err := s.issueUseCase().CloseWispChecked(s.Ctx(), "bd-ucc-wclc-src",
		domain.CloseIssueParams{Reason: "done"}, "tester", false)
	s.Require().Error(err)
	s.True(errors.Is(err, storage.ErrCloseBlocked))
	s.ErrorContains(err, "bd-ucc-wclc-tgt")
	s.False(res.Closed)

	var status string
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT status FROM wisps WHERE id = ?", "bd-ucc-wclc-src").Scan(&status))
	s.Equal(string(types.StatusOpen), status)
}

func (s *testSuite) uccCloseWispCheckedForceCloses() {
	s.seedWispRow("bd-ucc-wclc-fsrc")
	s.seedWispRow("bd-ucc-wclc-ftgt")
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-ucc-wclc-fsrc", "bd-ucc-wclc-ftgt", types.DepBlocks), "tester", domain.DepInsertOpts{UseWispsTable: true}))

	res, err := s.issueUseCase().CloseWispChecked(s.Ctx(), "bd-ucc-wclc-fsrc",
		domain.CloseIssueParams{Reason: "override"}, "tester", true)
	s.Require().NoError(err, "force must bypass the wisp block guard")
	s.True(res.Closed)
	s.Require().NotNil(res.Issue)
	s.Equal(types.StatusClosed, res.Issue.Status)
}

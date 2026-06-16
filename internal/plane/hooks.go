package plane

import (
	"context"

	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

// Compile-time check: the client's 429 error satisfies the engine's
// rate-limit interface so push aborts cleanly instead of failing issue by
// issue.
var _ tracker.RateLimitedError = (*RateLimitError)(nil)

// NewPullHooks returns the pull hooks every Plane sync engine must be wired
// with. Plane work item assignees are user UUIDs the adapter cannot map to
// beads assignees, so TrackerIssue.Assignee is always empty; without this
// hook the engine's unconditional assignee comparison would wipe the local
// assignee on every pull update. The hook restores the existing local value
// before the engine diffs and stores the conversion.
func NewPullHooks() *tracker.PullHooks {
	return &tracker.PullHooks{
		AfterConvert: func(_ context.Context, _ *tracker.TrackerIssue, conv *tracker.IssueConversion, _ string, existing *types.Issue, _ tracker.SyncOptions) error {
			if existing != nil && conv != nil && conv.Issue != nil {
				conv.Issue.Assignee = existing.Assignee
			}
			return nil
		},
	}
}

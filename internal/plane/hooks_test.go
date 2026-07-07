package plane

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

func TestNewPullHooksPreservesLocalAssignee(t *testing.T) {
	hooks := NewPullHooks()
	if hooks == nil || hooks.AfterConvert == nil {
		t.Fatal("NewPullHooks must wire AfterConvert")
	}

	t.Run("existing local assignee is preserved", func(t *testing.T) {
		conv := &tracker.IssueConversion{Issue: &types.Issue{Title: "x", Assignee: ""}}
		existing := &types.Issue{Assignee: "julian"}
		if err := hooks.AfterConvert(context.Background(), &tracker.TrackerIssue{}, conv, "ref", existing, tracker.SyncOptions{}); err != nil {
			t.Fatalf("AfterConvert error: %v", err)
		}
		if conv.Issue.Assignee != "julian" {
			t.Errorf("Assignee = %q, want the local value preserved", conv.Issue.Assignee)
		}
	})

	t.Run("new import keeps empty assignee", func(t *testing.T) {
		conv := &tracker.IssueConversion{Issue: &types.Issue{Title: "x"}}
		if err := hooks.AfterConvert(context.Background(), &tracker.TrackerIssue{}, conv, "ref", nil, tracker.SyncOptions{}); err != nil {
			t.Fatalf("AfterConvert error: %v", err)
		}
		if conv.Issue.Assignee != "" {
			t.Errorf("Assignee = %q, want empty for a new import", conv.Issue.Assignee)
		}
	})

	t.Run("nil conversion is tolerated", func(t *testing.T) {
		if err := hooks.AfterConvert(context.Background(), &tracker.TrackerIssue{}, nil, "ref", &types.Issue{Assignee: "x"}, tracker.SyncOptions{}); err != nil {
			t.Fatalf("AfterConvert error: %v", err)
		}
	})
}

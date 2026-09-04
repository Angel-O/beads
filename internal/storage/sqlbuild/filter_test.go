package sqlbuild

import (
	"slices"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestBuildIssueFilterClausesUnscopedUsesNotExists(t *testing.T) {
	clauses, args, err := BuildIssueFilterClauses("", types.IssueFilter{Unscoped: true}, IssuesFilterTables)
	if err != nil {
		t.Fatalf("BuildIssueFilterClauses: %v", err)
	}
	want := "NOT EXISTS (SELECT 1 FROM scope_members sm WHERE sm.issue_id = id)"
	if !slices.Contains(clauses, want) {
		t.Fatalf("clauses = %v, want %q", clauses, want)
	}
	if len(args) != 0 {
		t.Fatalf("args = %v, want none", args)
	}

	ordinary, _, err := BuildIssueFilterClauses("", types.IssueFilter{}, IssuesFilterTables)
	if err != nil {
		t.Fatalf("ordinary BuildIssueFilterClauses: %v", err)
	}
	if slices.Contains(ordinary, want) {
		t.Fatal("ordinary filter unexpectedly included the scope selection")
	}
}

// TestGlobToLikePattern exercises globToLikePattern directly against the
// package that actually calls it from BuildIssueFilterClauses (be-ucslk4).
// This used to be pinned only against a byte-identical but unreachable copy
// in internal/storage/issueops, so an escaping regression in this — the
// live — implementation could ship unseen.
func TestGlobToLikePattern(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "trailing star", in: "tech-*", want: "tech-%"},
		{name: "surrounding stars", in: "*foo*", want: "%foo%"},
		{name: "question mark", in: "v?", want: "v_"},
		{name: "literal percent", in: "5%", want: "5|%"},
		{name: "literal underscore", in: "snake_case", want: "snake|_case"},
		{name: "literal pipe", in: "a|b", want: "a||b"},
		{name: "no metachars", in: "needs-pm", want: "needs-pm"},
		{name: "empty", in: "", want: ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := globToLikePattern(tc.in)
			if got != tc.want {
				t.Errorf("globToLikePattern(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

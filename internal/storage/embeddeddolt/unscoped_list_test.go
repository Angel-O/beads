//go:build cgo

package embeddeddolt_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/issueops"
)

func TestEmbeddedUnscopedListAndQuery(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	te := newTestEnv(t, "unscoped")
	ctx := t.Context()

	base := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	issues := []*types.Issue{
		unscopedTestIssue("unscoped-scoped", types.StatusOpen, base.Add(-time.Hour)),
		unscopedTestIssue("unscoped-open", types.StatusOpen, base.Add(-2*time.Hour)),
		unscopedTestIssue("unscoped-closed", types.StatusClosed, base),
		unscopedTestIssue("unscoped-deferred", types.StatusDeferred, base),
	}
	for _, issue := range issues {
		if err := te.store.CreateIssue(ctx, issue, "tester"); err != nil {
			t.Fatalf("CreateIssue(%s): %v", issue.ID, err)
		}
	}
	if err := te.store.CreateScope(ctx, &types.Scope{ID: "unscoped-scope", Name: "unscoped test"}, false); err != nil {
		t.Fatalf("CreateScope: %v", err)
	}
	if err := te.store.AddScopeMembers(ctx, "unscoped-scope", []string{"unscoped-scoped"}); err != nil {
		t.Fatalf("AddScopeMembers: %v", err)
	}

	reader, err := te.store.IssueReader()
	if err != nil {
		t.Fatalf("IssueReader: %v", err)
	}
	allIDs := "unscoped-scoped,unscoped-open,unscoped-closed,unscoped-deferred"
	ordinary, err := reader.List(ctx, issueops.ListRequest{IDFilter: allIDs, SortBy: "updated"})
	if err != nil {
		t.Fatalf("ordinary List: %v", err)
	}
	if got := embeddedUnscopedPageIDs(ordinary); !slices.Equal(got, []string{"unscoped-deferred", "unscoped-scoped", "unscoped-open"}) {
		t.Fatalf("ordinary List IDs = %v, want scoped and non-closed rows", got)
	}

	limit := 1
	req := issueops.ListRequest{
		IDFilter: allIDs, Unscoped: true, Paginate: true, SortBy: "updated", Limit: &limit,
	}
	var walked []string
	for pageNumber := 0; pageNumber < 4; pageNumber++ {
		page, listErr := reader.List(ctx, req)
		if listErr != nil {
			t.Fatalf("unscoped List page %d: %v", pageNumber, listErr)
		}
		walked = append(walked, embeddedUnscopedPageIDs(page)...)
		if !page.HasMore {
			if page.NextCursor != "" {
				t.Fatalf("terminal page cursor = %q, want empty", page.NextCursor)
			}
			break
		}
		if page.NextCursor == "" {
			t.Fatalf("page %d has more rows but no cursor", pageNumber)
		}
		req.Cursor = page.NextCursor
	}
	if !slices.Equal(walked, []string{"unscoped-closed", "unscoped-deferred", "unscoped-open"}) {
		t.Fatalf("unscoped List walk = %v, want updated_at DESC, id ASC unscoped rows", walked)
	}

	bad := req
	bad.Cursor = "not-a-list-cursor"
	if _, err := reader.List(ctx, bad); !errors.Is(err, issueops.ErrValidation) {
		t.Fatalf("malformed cursor error = %v, want ErrValidation", err)
	}
	mismatch := req
	mismatch.Cursor = ""
	first, err := reader.List(ctx, mismatch)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first unscoped page = %+v, %v", first, err)
	}
	mismatch.Cursor = first.NextCursor
	mismatch.Unscoped = false
	if _, err := reader.List(ctx, mismatch); err == nil || !strings.Contains(err.Error(), "current list filters") {
		t.Fatalf("mismatched cursor error = %v, want selection validation", err)
	}

	querier, err := te.store.Querier()
	if err != nil {
		t.Fatalf("Querier: %v", err)
	}
	queryPage, err := querier.Query(ctx, issueops.QueryRequest{
		Expression: "type=task", Unscoped: true, IncludeClosed: false,
	})
	if err != nil {
		t.Fatalf("unscoped Query: %v", err)
	}
	if got := embeddedUnscopedPageIDs(queryPage); !slices.Equal(got, []string{"unscoped-closed", "unscoped-deferred", "unscoped-open"}) {
		t.Fatalf("unscoped Query IDs = %v, want all unscoped lifecycle states", got)
	}
}

func unscopedTestIssue(id string, status types.Status, updated time.Time) *types.Issue {
	return &types.Issue{
		ID: id, Title: id, Status: status, Priority: 2, IssueType: types.TypeTask,
		CreatedAt: updated, UpdatedAt: updated,
	}
}

func embeddedUnscopedPageIDs(page issueops.IssuePage) []string {
	ids := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.ID)
	}
	return ids
}

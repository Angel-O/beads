package workapi

import (
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/issueops"
)

func TestListCursorBindsSelectionButNotLimitOrProjection(t *testing.T) {
	limit := 2
	req := issueops.ListRequest{Paginate: true, SortBy: "updated", Limit: &limit, Labels: []string{"api"}}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	items := []*types.IssueWithCounts{{Issue: &types.Issue{ID: "bd-b", UpdatedAt: at}}}
	token, err := NextListCursor(req, items, true)
	if err != nil || token == "" {
		t.Fatalf("NextListCursor = %q, %v", token, err)
	}

	changedLimit := 7
	resumed := req
	resumed.Cursor = token
	resumed.Limit = &changedLimit
	resumed.Brief = true
	var filter types.IssueFilter
	if err := ApplyListCursor(resumed, &filter); err != nil {
		t.Fatalf("limit and Brief must not invalidate cursor: %v", err)
	}
	if !filter.AfterSortAtSet || filter.AfterSortAt == nil || !filter.AfterSortAt.Equal(at) || filter.AfterSortID != "bd-b" {
		t.Fatalf("decoded filter position = %+v", filter)
	}

	changedSelection := resumed
	changedSelection.Labels = []string{"other"}
	if err := ApplyListCursor(changedSelection, &types.IssueFilter{}); err == nil || !strings.Contains(err.Error(), "current list filters") {
		t.Fatalf("changed selection error = %v", err)
	}
	changedReady := resumed
	changedReady.ReadyFlag = true
	if err := ApplyListCursor(changedReady, &types.IssueFilter{}); err == nil || !strings.Contains(err.Error(), "current list filters") {
		t.Fatalf("changed ready selection error = %v", err)
	}
	changedSort := resumed
	changedSort.SortBy = "created"
	if err := ApplyListCursor(changedSort, &types.IssueFilter{}); err == nil || !strings.Contains(err.Error(), "created for sort") {
		t.Fatalf("changed sort error = %v", err)
	}
}

func TestListCursorClosedNullAndTerminalPages(t *testing.T) {
	limit := 1
	req := issueops.ListRequest{Paginate: true, SortBy: "closed", Limit: &limit}
	items := []*types.IssueWithCounts{{Issue: &types.Issue{ID: "bd-null"}}}
	token, err := NextListCursor(req, items, true)
	if err != nil {
		t.Fatal(err)
	}
	resumed := req
	resumed.Cursor = token
	var filter types.IssueFilter
	if err := ApplyListCursor(resumed, &filter); err != nil {
		t.Fatal(err)
	}
	if !filter.AfterSortAtSet || filter.AfterSortAt != nil || filter.AfterSortID != "bd-null" {
		t.Fatalf("null position = %+v", filter)
	}
	if token, err := NextListCursor(req, nil, false); err != nil || token != "" {
		t.Fatalf("terminal cursor = %q, %v", token, err)
	}
}

func TestListCursorComposesWithLegacyCreatedSelection(t *testing.T) {
	legacyAt := time.Date(2025, 5, 6, 7, 8, 9, 0, time.UTC)
	pageAt := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	legacy := &time.Time{}
	*legacy = legacyAt
	for _, sortBy := range []string{"updated", "closed"} {
		t.Run(sortBy, func(t *testing.T) {
			limit := 1
			req := issueops.ListRequest{
				Paginate:       true,
				SortBy:         sortBy,
				Limit:          &limit,
				AfterCreatedAt: legacy,
				AfterID:        "legacy-id",
			}
			issue := &types.Issue{ID: "page-id"}
			if sortBy == "updated" {
				issue.UpdatedAt = pageAt
			} else {
				issue.ClosedAt = &pageAt
			}
			token, err := NextListCursor(req, []*types.IssueWithCounts{{Issue: issue}}, true)
			if err != nil {
				t.Fatal(err)
			}

			resumed := req
			resumed.Cursor = token
			filter := types.IssueFilter{AfterCreatedAt: req.AfterCreatedAt, AfterID: req.AfterID}
			if err := ApplyListCursor(resumed, &filter); err != nil {
				t.Fatalf("resume: %v", err)
			}
			if filter.AfterCreatedAt == nil || !filter.AfterCreatedAt.Equal(legacyAt) || filter.AfterID != "legacy-id" {
				t.Fatalf("legacy position was changed: %+v", filter)
			}
			if filter.AfterSortID != "page-id" {
				t.Fatalf("generalized position ID = %q, want page-id", filter.AfterSortID)
			}

			changed := resumed
			changed.AfterID = "different-legacy-id"
			if err := ApplyListCursor(changed, &types.IssueFilter{}); err == nil || !strings.Contains(err.Error(), "current list filters") {
				t.Fatalf("changed legacy pair error = %v", err)
			}
			removed := resumed
			removed.AfterCreatedAt = nil
			removed.AfterID = ""
			if err := ApplyListCursor(removed, &types.IssueFilter{}); err == nil || !strings.Contains(err.Error(), "current list filters") {
				t.Fatalf("removed legacy pair error = %v", err)
			}
		})
	}
}

func TestListCursorRejectsMalformedVersions(t *testing.T) {
	limit := 1
	for _, token := range []string{"garbage", "v2.e30", "v1.not-base64"} {
		req := issueops.ListRequest{Cursor: token, SortBy: "created", Limit: &limit}
		if err := ApplyListCursor(req, &types.IssueFilter{}); err == nil || !strings.Contains(err.Error(), "restart pagination") {
			t.Errorf("ApplyListCursor(%q) error = %v", token, err)
		}
	}
}

func TestListCursorSupportsReadyListingsAndBindsSelection(t *testing.T) {
	limit := 1
	req := issueops.ListRequest{
		Paginate:  true,
		ReadyFlag: true,
		SortBy:    "created",
		Limit:     &limit,
		Labels:    []string{"ready-scope"},
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	token, err := NextListCursor(req, []*types.IssueWithCounts{{Issue: &types.Issue{ID: "bd-ready", CreatedAt: at}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	resumed := req
	resumed.Cursor = token
	var filter types.IssueFilter
	if err := ApplyListCursor(resumed, &filter); err != nil {
		t.Fatalf("ready cursor should resume: %v", err)
	}
	if !filter.AfterSortAtSet || filter.AfterSortAt == nil || !filter.AfterSortAt.Equal(at) || filter.AfterSortID != "bd-ready" {
		t.Fatalf("ready cursor position = %+v", filter)
	}
	changed := resumed
	changed.ReadyFlag = false
	if err := ApplyListCursor(changed, &types.IssueFilter{}); err == nil || !strings.Contains(err.Error(), "current list filters") {
		t.Fatalf("changed ready selection error = %v", err)
	}
}

func TestListCursorLimitCap(t *testing.T) {
	for _, limit := range []int{MaxListPageLimit, MaxListPageLimit + 1, int(^uint(0) >> 1)} {
		req := issueops.ListRequest{Paginate: true, SortBy: "created", Limit: &limit}
		err := ApplyListCursor(req, &types.IssueFilter{})
		if limit <= MaxListPageLimit {
			if err != nil {
				t.Errorf("limit %d rejected: %v", limit, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "must not exceed") {
			t.Errorf("limit %d error = %v, want maximum-limit validation", limit, err)
		}
	}
}

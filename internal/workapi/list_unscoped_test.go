package workapi

import (
	"slices"
	"testing"

	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/issueops"
)

func TestBuildListFilterUnscopedIsExplicitAndLiftsDefaultStatusExclusions(t *testing.T) {
	ordinary, err := BuildListFilter(issueops.ListRequest{}, ListConfig{})
	if err != nil {
		t.Fatalf("ordinary BuildListFilter: %v", err)
	}
	if ordinary.Unscoped {
		t.Fatal("ordinary list unexpectedly selected unscoped issues")
	}
	if !slices.Contains(ordinary.ExcludeStatus, types.StatusClosed) {
		t.Fatalf("ordinary list excludes statuses = %v, want closed excluded", ordinary.ExcludeStatus)
	}

	unscoped, err := BuildListFilter(issueops.ListRequest{Unscoped: true}, ListConfig{})
	if err != nil {
		t.Fatalf("unscoped BuildListFilter: %v", err)
	}
	if !unscoped.Unscoped {
		t.Fatal("unscoped request did not reach the storage filter")
	}
	if slices.Contains(unscoped.ExcludeStatus, types.StatusClosed) {
		t.Fatalf("unscoped list excludes closed issues: %v", unscoped.ExcludeStatus)
	}
}

func TestBuildListFilterCarriesExactLabelAndPriorityCursor(t *testing.T) {
	priority := 2
	label := "backend"
	filter, err := BuildListFilter(issueops.ListRequest{
		Label:         &label,
		AfterPriority: &priority,
	}, ListConfig{})
	if err != nil {
		t.Fatalf("BuildListFilter: %v", err)
	}
	if filter.Label == nil || *filter.Label != label {
		t.Fatalf("exact label = %v, want %q", filter.Label, label)
	}
	if filter.AfterPriority == nil || *filter.AfterPriority != priority {
		t.Fatalf("after priority = %v, want %d", filter.AfterPriority, priority)
	}
}

func TestBuildListFilterCarriesNoLabelPrefix(t *testing.T) {
	filter, err := BuildListFilter(issueops.ListRequest{NoLabelPrefix: "team:"}, ListConfig{})
	if err != nil {
		t.Fatalf("BuildListFilter: %v", err)
	}
	if filter.NoLabelPrefix != "team:" {
		t.Fatalf("no label prefix = %q, want %q", filter.NoLabelPrefix, "team:")
	}
}

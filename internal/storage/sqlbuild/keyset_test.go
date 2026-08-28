package sqlbuild

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestKeysetPredicateEmission pins the (created_at DESC, id ASC) keyset predicate
// BuildIssueFilterClauses emits for IssueFilter.AfterCreatedAt/AfterID: the exact
// sargable SQL fragment (single-sourced from KeysetCreatedAtIDPredicate) and its
// three bound args in order (created_at, created_at, id).
func TestKeysetPredicateEmission(t *testing.T) {
	t.Parallel()

	cur := time.Date(2024, 3, 2, 1, 0, 0, 0, time.UTC)

	// No keyset set: predicate absent.
	clauses, args, err := BuildIssueFilterClauses("", types.IssueFilter{}, IssuesFilterTables)
	if err != nil {
		t.Fatalf("BuildIssueFilterClauses (no keyset): %v", err)
	}
	for _, c := range clauses {
		if strings.Contains(c, KeysetCreatedAtIDPredicate) {
			t.Fatalf("keyset predicate emitted with no AfterCreatedAt set: %v", clauses)
		}
	}
	_ = args

	// Keyset set: exactly one predicate clause equal to the single-sourced
	// constant, with three args in bind order.
	clauses, args, err = BuildIssueFilterClauses("", types.IssueFilter{
		AfterCreatedAt: &cur,
		AfterID:        "bd-42",
	}, IssuesFilterTables)
	if err != nil {
		t.Fatalf("BuildIssueFilterClauses (keyset): %v", err)
	}
	found := 0
	for _, c := range clauses {
		if c == KeysetCreatedAtIDPredicate {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("keyset predicate clause count = %d, want 1; clauses=%v", found, clauses)
	}
	// The cursor time binds as time.Time (twice: sargable + strict bound), then
	// the id — bound as a value, not a formatted string, so the DATETIME columns
	// compare correctly on every backend.
	want := []any{cur, cur, "bd-42"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("arg[%d] = %v (%T), want %v (%T)", i, args[i], args[i], want[i], want[i])
		}
	}
}

// TestKeysetComposesWithCreatedBefore proves the new keyset field does not
// displace CreatedBefore: both predicates are emitted, and the keyset upper
// bound (created_at <=) is distinct from CreatedBefore's (created_at <).
func TestKeysetComposesWithCreatedBefore(t *testing.T) {
	t.Parallel()

	cur := time.Date(2024, 3, 2, 1, 0, 0, 0, time.UTC)
	before := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	clauses, args, err := BuildIssueFilterClauses("", types.IssueFilter{
		CreatedBefore:  &before,
		AfterCreatedAt: &cur,
		AfterID:        "bd-7",
	}, IssuesFilterTables)
	if err != nil {
		t.Fatalf("BuildIssueFilterClauses: %v", err)
	}
	joined := strings.Join(clauses, " AND ")
	if !strings.Contains(joined, KeysetCreatedAtIDPredicate) {
		t.Fatalf("keyset predicate missing when composed with CreatedBefore: %v", clauses)
	}
	if !strings.Contains(joined, "created_at < ?") {
		t.Fatalf("CreatedBefore predicate (created_at < ?) missing: %v", clauses)
	}
	// CreatedBefore contributes one arg, keyset contributes three.
	if len(args) != 4 {
		t.Fatalf("arg count = %d, want 4 (1 CreatedBefore + 3 keyset)", len(args))
	}
}

func TestGeneralizedDateKeysetPredicates(t *testing.T) {
	t.Parallel()
	at := time.Date(2025, 4, 3, 2, 1, 0, 0, time.UTC)
	tests := []struct {
		name   string
		filter types.IssueFilter
		clause string
		args   []any
	}{
		{"created", types.IssueFilter{SortBy: "created", AfterSortAtSet: true, AfterSortAt: &at, AfterSortID: "b"}, KeysetCreatedAtIDPredicate, []any{at, at, "b"}},
		{"updated", types.IssueFilter{SortBy: "updated", AfterSortAtSet: true, AfterSortAt: &at, AfterSortID: "b"}, keysetUpdatedAtIDPredicate, []any{at, at, "b"}},
		{"closed timestamp includes null group", types.IssueFilter{SortBy: "closed", AfterSortAtSet: true, AfterSortAt: &at, AfterSortID: "b"}, keysetClosedAtIDPredicate, []any{at, at, "b"}},
		{"closed null group", types.IssueFilter{SortBy: "closed", AfterSortAtSet: true, AfterSortID: "b"}, keysetClosedAtNullIDPredicate, []any{"b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clauses, args, err := BuildIssueFilterClauses("", tt.filter, IssuesFilterTables)
			if err != nil {
				t.Fatalf("BuildIssueFilterClauses: %v", err)
			}
			if !slices.Contains(clauses, tt.clause) {
				t.Fatalf("clauses = %v, want %q", clauses, tt.clause)
			}
			if !reflect.DeepEqual(args, tt.args) {
				t.Fatalf("args = %#v, want %#v", args, tt.args)
			}
		})
	}
}

func TestGeneralizedKeysetComposesWithLegacyCreatedSelection(t *testing.T) {
	t.Parallel()
	legacyAt := time.Date(2025, 4, 3, 2, 1, 0, 0, time.UTC)
	pageAt := time.Date(2026, 4, 3, 2, 1, 0, 0, time.UTC)
	for _, test := range []struct {
		sort   string
		clause string
	}{
		{"updated", keysetUpdatedAtIDPredicate},
		{"closed", keysetClosedAtIDPredicate},
	} {
		t.Run(test.sort, func(t *testing.T) {
			clauses, args, err := BuildIssueFilterClauses("", types.IssueFilter{
				AfterCreatedAt: &legacyAt,
				AfterID:        "legacy-id",
				SortBy:         test.sort,
				AfterSortAtSet: true,
				AfterSortAt:    &pageAt,
				AfterSortID:    "page-id",
			}, IssuesFilterTables)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(clauses, KeysetCreatedAtIDPredicate) || !slices.Contains(clauses, test.clause) {
				t.Fatalf("composed clauses = %v", clauses)
			}
			if !reflect.DeepEqual(args, []any{legacyAt, legacyAt, "legacy-id", pageAt, pageAt, "page-id"}) {
				t.Fatalf("composed args = %#v", args)
			}
		})
	}
}

func TestIssueDateBoundsPreserveFractionalPrecisionAndStrictness(t *testing.T) {
	t.Parallel()
	instant := time.Date(2026, 4, 3, 2, 1, 0, 123456789, time.UTC)
	equivalentOffset := instant.In(time.FixedZone("offset", 5*60*60+30*60))
	wantArg := instant.Format(time.RFC3339Nano)

	tests := []struct {
		name   string
		filter types.IssueFilter
		clause string
	}{
		{"created after", types.IssueFilter{CreatedAfter: &instant}, "created_at > ?"},
		{"created before equivalent offset", types.IssueFilter{CreatedBefore: &equivalentOffset}, "created_at < ?"},
		{"updated after", types.IssueFilter{UpdatedAfter: &instant}, "updated_at > ?"},
		{"updated before equivalent offset", types.IssueFilter{UpdatedBefore: &equivalentOffset}, "updated_at < ?"},
		{"closed after", types.IssueFilter{ClosedAfter: &instant}, "closed_at > ?"},
		{"closed before equivalent offset", types.IssueFilter{ClosedBefore: &equivalentOffset}, "closed_at < ?"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clauses, args, err := BuildIssueFilterClauses("", tt.filter, IssuesFilterTables)
			if err != nil {
				t.Fatalf("BuildIssueFilterClauses: %v", err)
			}
			if !slices.Contains(clauses, tt.clause) {
				t.Fatalf("clauses = %v, want strict boundary %q", clauses, tt.clause)
			}
			if len(args) != 1 || args[0] != wantArg {
				t.Fatalf("args = %#v, want nanosecond boundary %q", args, wantArg)
			}
		})
	}
}

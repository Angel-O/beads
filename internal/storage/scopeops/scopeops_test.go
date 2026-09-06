package scopeops

import (
	"errors"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
)

func TestScopeCursorIsVersionedAndBoundToReadShape(t *testing.T) {
	created := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	catalog, err := encodeCursor(scopeCatalogCursor, "", "", "", created, "scope-2")
	if err != nil {
		t.Fatalf("encode catalog cursor: %v", err)
	}
	if _, err := decodeCursor(catalog, scopeCatalogCursor, "", "", ""); err != nil {
		t.Fatalf("decode catalog cursor: %v", err)
	}
	for _, mismatch := range []struct {
		name      string
		kind      string
		scopeID   string
		status    string
		issueType string
	}{
		{"member cursor", scopeMembersCursor, "", "", ""},
		{"scope", scopeCatalogCursor, "other", "", ""},
		{"status", scopeCatalogCursor, "", "completed", ""},
	} {
		t.Run(mismatch.name, func(t *testing.T) {
			if _, err := decodeCursor(catalog, mismatch.kind, mismatch.scopeID, mismatch.status, mismatch.issueType); !errors.Is(err, storage.ErrScopeCursorInvalid) {
				t.Fatalf("decode mismatch error = %v, want ErrScopeCursorInvalid", err)
			}
		})
	}

	member, err := encodeCursor(scopeMembersCursor, "scope-1", "open", "task", time.Time{}, "issue-2")
	if err != nil {
		t.Fatalf("encode member cursor: %v", err)
	}
	if _, err := decodeCursor(member, scopeMembersCursor, "scope-1", "open", "task"); err != nil {
		t.Fatalf("decode member cursor: %v", err)
	}
	memberWithContexts, err := encodeCursor(scopeMembersCursor, "scope-1", "open", "task", time.Time{}, "issue-2", []string{"ctx-b", "ctx-a"})
	if err != nil {
		t.Fatalf("encode member context cursor: %v", err)
	}
	if _, err := decodeCursor(memberWithContexts, scopeMembersCursor, "scope-1", "open", "task", []string{"ctx-a", "ctx-b"}); err != nil {
		t.Fatalf("decode member context cursor: %v", err)
	}
	if _, err := decodeCursor(memberWithContexts, scopeMembersCursor, "scope-1", "open", "task", []string{"ctx-a"}); !errors.Is(err, storage.ErrScopeCursorInvalid) {
		t.Fatalf("decode context mismatch error = %v, want ErrScopeCursorInvalid", err)
	}
}

func TestScopeContextMatchesExactContextLabels(t *testing.T) {
	if !matchesScopeContext([]string{"ctx:team-a", "label"}, []string{"team-a"}) {
		t.Fatal("context ID did not match its exact ctx: label")
	}
	if matchesScopeContext([]string{"ctx:team-ab"}, []string{"team-a"}) {
		t.Fatal("context filter matched a non-exact ctx: label")
	}
}

func TestScopePageLimitDefaultsAndCaps(t *testing.T) {
	if got := scopePageLimit(0); got != defaultScopePageLimit {
		t.Fatalf("default scope page limit = %d, want %d", got, defaultScopePageLimit)
	}
	if got := scopePageLimit(maxScopePageLimit + 1); got != maxScopePageLimit {
		t.Fatalf("capped scope page limit = %d, want %d", got, maxScopePageLimit)
	}
}

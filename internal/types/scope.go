package types

import "time"

// Scope is the durable identity of a named, manually maintained group of
// issues. The identity fields are storage-owned after creation; callers must
// not rename or rewrite an existing scope.
type Scope struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	NormalizedName string    `json:"normalized_name"`
	CreatedOn      time.Time `json:"created_on"`
}

// ScopeDetails is the snapshot returned by a scope read. Members are complete
// issue rows, and Relationships contains only dependency edges whose source
// and target are both members of the scope.
type ScopeDetails struct {
	Scope
	Members       []*Issue      `json:"members"`
	Relationships []*Dependency `json:"relationships"`
}

// ScopeCatalogRequest selects one keyset page of the scope catalog. Cursor is
// opaque and may only be reused with this request shape.
type ScopeCatalogRequest struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

// ScopeCatalogRow is the catalog projection. It intentionally contains no
// members or relationships; those are read by ScopeMemberPage.
type ScopeCatalogRow struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	NormalizedName string    `json:"normalized_name"`
	CreatedOn      time.Time `json:"created_on"`
	MemberCount    int       `json:"member_count"`
	CompletedCount int       `json:"completed_count"`
}

// ScopeCatalogPage is a bounded catalog result. NextCursor is present only
// when HasMore is true. The cursor is versioned and opaque to callers.
type ScopeCatalogPage struct {
	Items         []*ScopeCatalogRow `json:"items"`
	Limit         int                `json:"limit"`
	ReturnedCount int                `json:"returned_count"`
	TotalMatching int                `json:"total_matching"`
	HasMore       bool               `json:"has_more"`
	NextCursor    string             `json:"next_cursor,omitempty"`
}

// ScopeMemberStatus is the small, stable status vocabulary for member pages.
type ScopeMemberStatus string

const (
	ScopeMemberStatusOpen      ScopeMemberStatus = "open"
	ScopeMemberStatusCompleted ScopeMemberStatus = "completed"
	ScopeMemberStatusReady     ScopeMemberStatus = "ready"
)

// ScopeMemberPageRequest selects a page within one exact scope membership.
// Type is an exact issue_type match; no alias expansion is performed.
type ScopeMemberPageRequest struct {
	Status ScopeMemberStatus `json:"status,omitempty"`
	Type   IssueType         `json:"type,omitempty"`
	Limit  int               `json:"limit,omitempty"`
	Cursor string            `json:"cursor,omitempty"`
}

// ScopeMemberPage is a scope identity plus a page of full issue rows. Member
// and completed counts are deliberately unfiltered; TotalMatching is filtered.
// Relationships are intentionally absent from this paged projection.
type ScopeMemberPage struct {
	Scope          Scope    `json:"scope"`
	Members        []*Issue `json:"members"`
	MemberCount    int      `json:"member_count"`
	CompletedCount int      `json:"completed_count"`
	TotalMatching  int      `json:"total_matching"`
	Limit          int      `json:"limit"`
	ReturnedCount  int      `json:"returned_count"`
	HasMore        bool     `json:"has_more"`
	NextCursor     string   `json:"next_cursor,omitempty"`
}

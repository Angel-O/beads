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

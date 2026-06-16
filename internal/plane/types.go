package plane

import (
	"fmt"
	"time"
)

// RateLimitError is a 429 response that survived retry exhaustion.
// It satisfies the engine's tracker.RateLimitedError interface
// (RateLimitRetryAfter), letting the push loop abort cleanly and keep the
// remaining queue for the next sync instead of failing issue by issue.
type RateLimitError struct {
	Method     string
	Path       string
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("plane API %s %s: status 429: rate limited (retry after %s)", e.Method, e.Path, e.RetryAfter)
	}
	return fmt.Sprintf("plane API %s %s: status 429: rate limited", e.Method, e.Path)
}

// RateLimitRetryAfter returns the server-suggested wait before retrying;
// zero means the server didn't say.
func (e *RateLimitError) RateLimitRetryAfter() time.Duration { return e.RetryAfter }

// Issue is a Plane work item as returned by the /api/v1 REST API (v1.3.0).
// Field names follow the IssueSerializer response shape; labels and
// assignees are UUID strings (the API returns IDs unless expand is used).
type Issue struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	DescriptionHTML string     `json:"description_html"`
	Priority        string     `json:"priority"`
	StateID         string     `json:"state"`
	ParentID        string     `json:"parent"`
	SequenceID      int        `json:"sequence_id"`
	ExternalID      string     `json:"external_id"`
	ExternalSource  string     `json:"external_source"`
	Labels          []string   `json:"labels"`
	Assignees       []string   `json:"assignees"`
	ProjectID       string     `json:"project"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	CompletedAt     *time.Time `json:"completed_at"`
	StartDate       string     `json:"start_date"`
	TargetDate      string     `json:"target_date"`
	IsDraft         bool       `json:"is_draft"`
}

// IssuePayload is the request body for creating (POST) or partially
// updating (PATCH) a work item. Pointer and omitempty semantics keep PATCH
// bodies minimal: only set fields are transmitted.
type IssuePayload struct {
	Name            string `json:"name,omitempty"`
	DescriptionHTML string `json:"description_html,omitempty"`
	Priority        string `json:"priority,omitempty"`
	StateID         string `json:"state,omitempty"`
	ParentID        string `json:"parent,omitempty"`
	// Labels/Assignees use pointer-to-slice so PATCH can distinguish
	// "leave unchanged" (nil, omitted) from "replace with this set"
	// (non-nil, sent even when empty — Plane replaces the full set
	// whenever the key is present).
	Labels         *[]string `json:"labels,omitempty"`
	Assignees      *[]string `json:"assignees,omitempty"`
	ExternalID     string    `json:"external_id,omitempty"`
	ExternalSource string    `json:"external_source,omitempty"`
	// CreatedAt/CreatedBy are honored by Plane on create only (importer
	// timestamp attribution); ignored on PATCH.
	CreatedAt string `json:"created_at,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
}

// State is a per-project workflow state. Group is one of the stable group
// values (the Group* constants in mapping.go, plus "triage"); names and IDs
// are project-specific.
type State struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Group   string `json:"group"`
	Color   string `json:"color"`
	Default bool   `json:"default"`
}

// Label is a per-project label entity.
type Label struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Color          string `json:"color"`
	ExternalID     string `json:"external_id"`
	ExternalSource string `json:"external_source"`
}

// Project is a Plane project (subset of fields the adapter needs).
type Project struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Identifier string `json:"identifier"`
}

// Comment is a work item comment (subset of fields the adapter needs).
type Comment struct {
	ID             string    `json:"id"`
	CommentHTML    string    `json:"comment_html"`
	ExternalID     string    `json:"external_id"`
	ExternalSource string    `json:"external_source"`
	CreatedAt      time.Time `json:"created_at"`
}

// ListIssuesOptions controls work item list requests.
type ListIssuesOptions struct {
	// OrderBy is passed through to Plane's order_by query param
	// (e.g. "-updated_at"). Empty uses the server default (-created_at).
	OrderBy string
}

// paginatedResponse is Plane's exact pagination envelope
// (plane/utils/paginator.py BasePaginator.paginate).
type paginatedResponse[T any] struct {
	TotalCount      int    `json:"total_count"`
	NextCursor      string `json:"next_cursor"`
	PrevCursor      string `json:"prev_cursor"`
	NextPageResults bool   `json:"next_page_results"`
	PrevPageResults bool   `json:"prev_page_results"`
	Count           int    `json:"count"`
	TotalPages      int    `json:"total_pages"`
	TotalResults    int    `json:"total_results"`
	Results         []T    `json:"results"`
}

// APIError is a non-2xx response from the Plane API that has no more
// specific typed representation.
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("plane API %s %s: status %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

// DuplicateError is Plane's 409 response when an entity with the same
// external_id/external_source pair (or unique name, for labels and states)
// already exists. ExistingID carries the UUID of the existing entity, which
// callers use to recover idempotently.
type DuplicateError struct {
	ExistingID string
	Message    string
}

func (e *DuplicateError) Error() string {
	return fmt.Sprintf("plane API conflict: %s (existing id %s)", e.Message, e.ExistingID)
}

// AuthError is an authentication failure: 401 (missing credentials) or 403
// with Plane's invalid-token body. Plane returns 403 (not 401) for
// invalid/expired tokens because its API-key authenticator defines no
// authenticate_header.
type AuthError struct {
	StatusCode int
	Detail     string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("plane API authentication failed (status %d): %s", e.StatusCode, e.Detail)
}

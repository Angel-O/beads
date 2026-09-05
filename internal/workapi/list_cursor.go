package workapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/issueops"
)

const listCursorVersion = "v1"

type listCursorState struct {
	Sort      string     `json:"s"`
	Selection string     `json:"f"`
	At        *time.Time `json:"t,omitempty"`
	Null      bool       `json:"n,omitempty"`
	ID        string     `json:"i"`
	Priority  *int       `json:"p,omitempty"`
}

// ApplyListCursor validates and decodes backend-owned list pagination state
// onto the storage filter. The token is never interpreted by a transport.
func ApplyListCursor(req issueops.ListRequest, filter *types.IssueFilter) error {
	paginated := req.Paginate || req.Cursor != ""
	if !paginated {
		return nil
	}
	if req.Limit == nil || *req.Limit <= 0 {
		return fmt.Errorf("%w: paginated list requires an explicitly supplied positive limit", issueops.ErrValidation)
	}
	if *req.Limit > MaxListPageLimit {
		return fmt.Errorf("%w: paginated list limit must not exceed %d", issueops.ErrValidation, MaxListPageLimit)
	}
	if req.SortBy != "created" && req.SortBy != "updated" && req.SortBy != "closed" && req.SortBy != "priority" {
		return fmt.Errorf("%w: paginated list requires sort priority, created, updated, or closed", issueops.ErrValidation)
	}
	if req.Reverse {
		return fmt.Errorf("%w: paginated list does not support reverse order", issueops.ErrValidation)
	}
	if req.Offset != 0 {
		return fmt.Errorf("%w: paginated list does not support offset", issueops.ErrValidation)
	}
	if req.Cursor == "" {
		return nil
	}

	state, err := decodeListCursor(req.Cursor)
	if err != nil {
		return err
	}
	if state.Sort != req.SortBy {
		return fmt.Errorf("%w: cursor was created for sort %q; restart pagination or use --sort %s", issueops.ErrValidation, state.Sort, state.Sort)
	}
	want, err := listSelectionFingerprint(req)
	if err != nil {
		return fmt.Errorf("%w: fingerprint list selection: %v", issueops.ErrValidation, err)
	}
	if state.Selection != want {
		return fmt.Errorf("%w: cursor does not match the current list filters; restart pagination with no --cursor", issueops.ErrValidation)
	}
	if state.ID == "" || (state.At == nil && !state.Null) || (state.At != nil && state.Null) {
		return fmt.Errorf("%w: malformed list cursor position; restart pagination with no --cursor", issueops.ErrValidation)
	}
	if state.Null && req.SortBy != "closed" {
		return fmt.Errorf("%w: malformed list cursor position for sort %q; restart pagination with no --cursor", issueops.ErrValidation, req.SortBy)
	}
	if req.SortBy == "priority" {
		if state.Priority == nil || state.At == nil {
			return fmt.Errorf("%w: malformed priority list cursor position; restart pagination with no --cursor", issueops.ErrValidation)
		}
		filter.AfterPriority = state.Priority
		filter.AfterPriorityCreatedAt = state.At
		filter.AfterPriorityID = state.ID
	} else {
		filter.AfterSortAtSet = true
		filter.AfterSortAt = state.At
		filter.AfterSortID = state.ID
	}
	return nil
}

// NextListCursor returns backend-owned state for the last delivered row.
func NextListCursor(req issueops.ListRequest, items []*types.IssueWithCounts, hasMore bool) (string, error) {
	if !(req.Paginate || req.Cursor != "") || !hasMore || len(items) == 0 {
		return "", nil
	}
	last := items[len(items)-1]
	if last == nil || last.Issue == nil {
		return "", fmt.Errorf("cannot create list cursor from an empty row")
	}
	state := listCursorState{Sort: req.SortBy, ID: last.ID}
	switch req.SortBy {
	case "created":
		at := last.CreatedAt
		state.At = &at
	case "updated":
		at := last.UpdatedAt
		state.At = &at
	case "closed":
		if last.ClosedAt == nil {
			state.Null = true
		} else {
			at := *last.ClosedAt
			state.At = &at
		}
	case "priority":
		priority := last.Priority
		state.Priority = &priority
		at := last.CreatedAt
		state.At = &at
	default:
		return "", fmt.Errorf("cannot create list cursor for sort %q", req.SortBy)
	}
	fingerprint, err := listSelectionFingerprint(req)
	if err != nil {
		return "", err
	}
	state.Selection = fingerprint
	blob, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	return listCursorVersion + "." + base64.RawURLEncoding.EncodeToString(blob), nil
}

func decodeListCursor(token string) (listCursorState, error) {
	version, encoded, ok := strings.Cut(token, ".")
	if !ok || version != listCursorVersion {
		return listCursorState{}, fmt.Errorf("%w: unsupported list cursor version %q; restart pagination with no --cursor", issueops.ErrValidation, version)
	}
	blob, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return listCursorState{}, fmt.Errorf("%w: malformed list cursor encoding; restart pagination with no --cursor", issueops.ErrValidation)
	}
	var state listCursorState
	if err := json.Unmarshal(blob, &state); err != nil {
		return listCursorState{}, fmt.Errorf("%w: malformed list cursor payload; restart pagination with no --cursor", issueops.ErrValidation)
	}
	return state, nil
}

func listSelectionFingerprint(req issueops.ListRequest) (string, error) {
	selection := req
	selection.Paginate = false
	selection.Cursor = ""
	selection.Limit = nil
	selection.Offset = 0
	if selection.AfterCreatedAt == nil {
		// AfterID alone is explicitly not a legacy position and therefore does
		// not affect selection.
		selection.AfterID = ""
	}
	selection.Brief = false
	selection.SkipLabels = false
	selection.SkipCounts = false
	selection.MaxRows = 0
	selection.MaxRowsSource = ""
	blob, err := json.Marshal(selection)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:]), nil
}

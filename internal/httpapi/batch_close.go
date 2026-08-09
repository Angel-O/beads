package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/steveyegge/beads/internal/httpapi/apigen"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/issueops"
)

const (
	// maxBatchCloseItems is the document's cap on `items`. It bounds how long one
	// request may hold a write transaction, not batch semantics — the same bound
	// batchCreate carries and for the same reason.
	maxBatchCloseItems = 100
	// maxBatchCloseBodyBytes bounds the request body at 1 MiB. A hundred items
	// each carrying an id and a short reason, plus a claim_next filter, is the
	// shape this admits; anything larger is refused before it is parsed.
	maxBatchCloseBodyBytes = 1 << 20
)

// The member vocabulary at each level. The schemas are additionalProperties:
// false, so anything else is refused BY NAME, which is why the body is decoded as
// raw members first — batchCreate's posture exactly.
var (
	batchCloseRequestMembers = []string{"actor", "items", "session", "force", "claim_next"}
	batchCloseItemMembers    = []string{"id", "reason"}
)

// handleBatchClose closes every item in the request it can, and commits the ones
// that landed together. It is the write side of `bd close a b c`.
//
// It shares the claim's posture exactly: the actor is caller-ASSERTED provenance
// and not authenticated identity, hooks do not fire, and the per-command
// auto-commit machinery never runs. The only durable effect is the single
// storage commit the role makes inside its own transaction.
//
// Everything above the role is argument validation. The close policy, the
// per-item outcomes, the atomic claim_next and the transaction retry all belong
// to issueops.BatchCloser, reached through the provider's own accessor. Unlike
// batchCreate this operation is NOT all-or-nothing: a refused id is a per-item
// outcome inside a 200, not a 4xx, so a caller keeps the answers for the ids that
// did close.
func (s *Server) handleBatchClose(w http.ResponseWriter, r *http.Request) {
	if !s.requireNoQuery(w, r) {
		return
	}
	if !s.requireJSONContent(w, r) {
		return
	}
	request, ok := s.batchCloseRequest(w, r)
	if !ok {
		return
	}

	closer, err := s.batchCloser(r)
	if err != nil {
		s.failBatchClose(w, r, err)
		return
	}
	result, err := closer.CloseBatch(r.Context(), request)
	if err != nil {
		s.failBatchClose(w, r, err)
		return
	}
	writeJSON(w, batchCloseResponse(result))
}

// batchCloseRequest decodes and validates the body, and reports whether the
// request may proceed. Every refusal here happens BEFORE any database work, which
// is what lets the 400s reflect the caller's own input back — and what lets a
// per-item outcome be reserved for the id refusals only the transaction can see.
func (s *Server) batchCloseRequest(w http.ResponseWriter, r *http.Request) (issueops.CloseBatchRequest, bool) {
	members, res := decodeJSONObject(w, r, maxBatchCloseBodyBytes)
	if res != nil {
		s.fail(w, r, *res)
		return issueops.CloseBatchRequest{}, false
	}
	if offender, unknown := unknownMember(members, batchCloseRequestMembers); unknown {
		s.failUnknownMember(w, r, offender, batchCloseRequestMembers)
		return issueops.CloseBatchRequest{}, false
	}

	// actor is required and reaches the same columns and commit message the claim
	// validates, so it is bodyActor's rules unchanged. session and reason land in
	// stored columns renderers print, so both take storedTextMember's control-
	// character and length bounds. force defaults false, the guarded answer.
	actor, ok := s.bodyActor(w, r, members)
	if !ok {
		return issueops.CloseBatchRequest{}, false
	}
	items, ok := s.batchCloseItems(w, r, members)
	if !ok {
		return issueops.CloseBatchRequest{}, false
	}
	session, ok := s.storedTextMember(w, r, members, "session")
	if !ok {
		return issueops.CloseBatchRequest{}, false
	}
	force, ok := s.booleanMember(w, r, members, "force")
	if !ok {
		return issueops.CloseBatchRequest{}, false
	}
	claimNext, ok := s.batchCloseClaimNext(w, r, members)
	if !ok {
		return issueops.CloseBatchRequest{}, false
	}

	return issueops.CloseBatchRequest{
		Actor:     actor,
		Items:     items,
		Session:   session,
		Force:     force,
		ClaimNext: claimNext,
	}, true
}

// batchCloseItems validates `items` and projects it onto the role's items. It is
// batchCreateItems' shape: the array bounds, the per-item unknown-member refusal,
// and the malformed-vs-missing distinction each item earns.
func (s *Server) batchCloseItems(w http.ResponseWriter, r *http.Request, members map[string]json.RawMessage) ([]issueops.BatchCloseItem, bool) {
	raw, ok := members["items"]
	if !ok {
		s.fail(w, r, InvalidArgument("items", ReasonInvalidValue, "`items` is required"))
		return nil, false
	}
	var rawItems []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawItems); err != nil || rawItems == nil {
		s.fail(w, r, InvalidArgument("items", ReasonInvalidValue, "`items` must be an array of objects"))
		return nil, false
	}
	switch {
	case len(rawItems) == 0:
		s.fail(w, r, InvalidArgument("items", ReasonInvalidValue,
			"`items` must name at least one issue; a close that closes nothing is refused rather than answered"))
		return nil, false
	case len(rawItems) > maxBatchCloseItems:
		s.fail(w, r, InvalidArgument("items", ReasonInvalidValue,
			fmt.Sprintf("`items` carries %d issues; the limit is %d per request", len(rawItems), maxBatchCloseItems)))
		return nil, false
	}

	items := make([]issueops.BatchCloseItem, 0, len(rawItems))
	for i, rawItem := range rawItems {
		if rawItem == nil {
			s.fail(w, r, InvalidArgument(batchCloseItemParam(i, ""), ReasonInvalidValue, "an item must be a JSON object"))
			return nil, false
		}
		if offender, unknown := unknownMember(rawItem, batchCloseItemMembers); unknown {
			s.failUnknownMember(w, r, batchCloseItemParam(i, offender), batchCloseItemMembers)
			return nil, false
		}
		item, res := batchCloseItem(i, rawItem)
		if res != nil {
			s.fail(w, r, *res)
			return nil, false
		}
		items = append(items, item)
	}
	return items, true
}

// batchCloseItem projects one decoded item onto the role's item, or reports the
// refusal it earned. It decodes into the GENERATED struct, which is what makes a
// member's type the document's type: `id: 7` is refused here rather than reaching
// a role that would have to guess.
//
// An id that names nothing is NOT refused here: it is a per-item not_found
// outcome the transaction reports, so a mistyped id in a list of five keeps the
// other four. Only a shape the request itself got wrong — a blank id, an
// over-long id or reason, a control character — is a 400.
func batchCloseItem(index int, raw map[string]json.RawMessage) (issueops.BatchCloseItem, *Result) {
	refuse := func(member, detail string) *Result {
		res := InvalidArgument(batchCloseItemParam(index, member), ReasonInvalidValue, detail)
		return &res
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return issueops.BatchCloseItem{}, refuse("", "an item must be a JSON object")
	}
	var wire apigen.BatchCloseItem
	if err := json.Unmarshal(encoded, &wire); err != nil {
		return issueops.BatchCloseItem{}, refuse("", "an item member carries the wrong JSON type")
	}
	if strings.TrimSpace(wire.Id) == "" {
		return issueops.BatchCloseItem{}, refuse("id", "`id` is required and must not be blank")
	}
	// The column is VARCHAR(255) and the id is exact, so a longer one names no row
	// that can exist. Refused here rather than let through to a 500 from the
	// column, the single close's edge bound applied per item.
	if types.CheckFieldLen("id", wire.Id) != nil {
		return issueops.BatchCloseItem{}, refuse("id",
			fmt.Sprintf("`id` is longer than the %d characters storage holds", types.MaxFieldLen))
	}
	item := issueops.BatchCloseItem{IssueID: wire.Id}
	if wire.Reason != nil {
		switch {
		case types.CheckFieldLen("reason", *wire.Reason) != nil:
			return issueops.BatchCloseItem{}, refuse("reason",
				fmt.Sprintf("`reason` is %d characters; storage holds at most %d",
					utf8.RuneCountInString(*wire.Reason), types.MaxFieldLen))
		case strings.ContainsFunc(*wire.Reason, isControlChar):
			return issueops.BatchCloseItem{}, refuse("reason", "`reason` must not contain control characters")
		}
		item.Reason = *wire.Reason
	}
	return item, nil
}

// batchCloseItemParam spells the `param` member for a refusal inside `items`, so
// a client dispatching on it learns WHICH item and WHICH member. It is
// batchCreateItemParam's shape; each batch operation keeps its own because the
// two vocabularies are not the same.
func batchCloseItemParam(index int, member string) string {
	param := fmt.Sprintf("items[%d]", index)
	if member == "" {
		return param
	}
	return param + "." + member
}

// batchCloseClaimNext decodes the optional claim_next onto the ready-request type
// through the SAME readyFilters the query decode runs. Absent stays nil — no
// claim was asked for. The filter's Limit and Offset stay unset here, which is
// what ClaimNext's contract requires and readyFilters never sets, so the role's
// own limit/offset refusal is unreachable from this front door.
func (s *Server) batchCloseClaimNext(w http.ResponseWriter, r *http.Request, members map[string]json.RawMessage) (*issueops.ReadyRequest, bool) {
	raw, ok := members["claim_next"]
	if !ok {
		return nil, true
	}
	var claimMembers map[string]json.RawMessage
	if err := json.Unmarshal(raw, &claimMembers); err != nil || claimMembers == nil {
		s.fail(w, r, InvalidArgument(claimNextParam, ReasonInvalidValue, "`"+claimNextParam+"` must be an object"))
		return nil, false
	}
	src := newReadyFilterObject(claimMembers)
	req := readyFilters(src)
	if res := src.result(); res != nil {
		s.fail(w, r, *res)
		return nil, false
	}
	return &req, true
}

// claimNextParam prefixes a refusal inside claim_next, so `claim_next.priority`
// tells a client which member of which object it must fix.
const claimNextParam = "claim_next"

// readyFilterObject decodes the ready filter vocabulary from a JSON object, the
// claim_next front door onto readyFilters. It is *query's twin: the same five
// accessors, each validating its own value against the same rules and recording a
// first refusal, plus the unknown-member refusal *query reports for a parameter
// it never read. The metadata split is splitMetadataFields, shared with the
// query so the two cannot disagree about what a malformed `key=value` is.
type readyFilterObject struct {
	members map[string]json.RawMessage
	// read marks every member readyFilters asked for, so the allowlist is the
	// vocabulary itself rather than a second copy of it — a member no accessor
	// reads is, by construction, unknown.
	read map[string]bool
	// res holds the FIRST refusal, so the answer does not depend on the order the
	// filters happen to be read in; `param` is what a client dispatches on.
	res *Result
}

func newReadyFilterObject(members map[string]json.RawMessage) *readyFilterObject {
	return &readyFilterObject{members: members, read: map[string]bool{}}
}

func (o *readyFilterObject) param(name string) string { return claimNextParam + "." + name }

func (o *readyFilterObject) invalid(name, detail string) {
	if o.res == nil {
		res := InvalidArgument(o.param(name), ReasonInvalidValue, detail)
		o.res = &res
	}
}

// raw marks name read — so it is not reported as an unknown member — and returns
// its value if the object carried one.
func (o *readyFilterObject) raw(name string) (json.RawMessage, bool) {
	o.read[name] = true
	v, ok := o.members[name]
	return v, ok
}

// str decodes through a POINTER so an explicit `null` reaches the type-mismatch
// branch rather than sliding through as the empty string, the close body's rule.
func (o *readyFilterObject) str(name string) string {
	raw, ok := o.raw(name)
	if !ok {
		return ""
	}
	var v *string
	if err := json.Unmarshal(raw, &v); err != nil || v == nil {
		o.invalid(name, "`"+name+"` must be a string")
		return ""
	}
	return *v
}

func (o *readyFilterObject) boolean(name string) bool {
	raw, ok := o.raw(name)
	if !ok {
		return false
	}
	var v *bool
	if err := json.Unmarshal(raw, &v); err != nil || v == nil {
		o.invalid(name, "`"+name+"` must be a boolean")
		return false
	}
	return *v
}

func (o *readyFilterObject) list(name string) []string {
	raw, ok := o.raw(name)
	if !ok {
		return nil
	}
	var v []string
	if err := json.Unmarshal(raw, &v); err != nil {
		o.invalid(name, "`"+name+"` must be an array of strings")
		return nil
	}
	return v
}

func (o *readyFilterObject) integer(name string) *int {
	raw, ok := o.raw(name)
	if !ok {
		return nil
	}
	var v *int
	if err := json.Unmarshal(raw, &v); err != nil || v == nil {
		o.invalid(name, "`"+name+"` must be an integer")
		return nil
	}
	return v
}

func (o *readyFilterObject) metadataFields(name string) map[string]string {
	raw, ok := o.raw(name)
	if !ok {
		return nil
	}
	var entries []string
	if err := json.Unmarshal(raw, &entries); err != nil {
		o.invalid(name, "`"+name+"` must be an array of `key=value` strings")
		return nil
	}
	return splitMetadataFields(entries, func(bad string) {
		o.invalid(name, fmt.Sprintf("%q is not key=value", bad))
	})
}

// result reports the refusal claim_next earned, if any. A malformed value on a
// member this server DOES know is reported ahead of an unknown one — the query's
// ordering — because the two carry opposite client recoveries.
func (o *readyFilterObject) result() *Result {
	if o.res != nil {
		return o.res
	}
	var unknown []string
	for name := range o.members {
		if !o.read[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	// One offender, chosen deterministically so a client dispatching on `param`
	// never sees it depend on map order.
	offender := slices.Min(unknown)
	res := InvalidArgument(o.param(offender), ReasonUnknownParameter,
		"`"+claimNextParam+"` carries the listReadyWork filter vocabulary and nothing else")
	return &res
}

// batchCloseResponse projects the role's result onto the wire body: one outcome
// per item in request order, and the claimed row when one was won.
//
// ClaimedNext is the role's *IssueWithCounts, which IS the generated pointer
// type (the schema is x-go-type-pinned), so it passes through unrewritten and
// nil — no claim asked for, nothing closed, or nothing eligible — omits the
// member.
func batchCloseResponse(result issueops.CloseBatchResult) apigen.BatchCloseResponse {
	items := make([]apigen.BatchCloseOutcome, len(result.Outcomes))
	for i := range result.Outcomes {
		items[i] = batchCloseOutcome(result.Outcomes[i])
	}
	return apigen.BatchCloseResponse{
		Items:       items,
		ClaimedNext: result.ClaimedNext,
	}
}

// batchCloseOutcome maps one role outcome onto the wire, carrying EXACTLY ONE of
// issue or error.
//
// The error branch wins when Err is set, which is also the branch a role that
// broke the exactly-one invariant lands in — checkedBatchCloser has already
// folded a neither-issue-nor-error outcome into Err, so the else branch
// dereferences a snapshot that is present by construction, the checkedClaimer
// guarantee once per item.
func batchCloseOutcome(o issueops.CloseOutcome) apigen.BatchCloseOutcome {
	out := apigen.BatchCloseOutcome{Id: o.IssueID}
	if o.Err != nil {
		itemErr := batchCloseItemError(o.Err)
		out.Error = &itemErr
		return out
	}
	// already_closed and open_children accompany the snapshot, mirroring
	// CloseIssueResponse: `already_closed` is the idempotent re-close (Changed
	// false), and a forced close reports the children it observed.
	issue := *o.Issue
	alreadyClosed := !o.Changed
	openChildren := o.OpenChildren
	out.Issue = &issue
	out.AlreadyClosed = &alreadyClosed
	out.OpenChildren = &openChildren
	return out
}

// batchCloseItemErrorCodes is the EXACT set of codes batchCloseItemError emits,
// declared once so TestBatchCloseItemErrorGovernance can pin it against the spec
// enum and the frozen problem registry in one place. Adding a case that emits a
// new code means adding it here, adding it to the schema enum, and adding it to
// the registry — all three, which is the whole point of the three-way pin.
var batchCloseItemErrorCodes = []apigen.BatchCloseItemErrorCode{
	apigen.BatchCloseItemErrorCodeNotFound,
	apigen.BatchCloseItemErrorCodeNotClosable,
}

// batchCloseItemError maps one item's typed refusal onto the wire, reusing the
// frozen close vocabulary the single close already maps: ErrNotFound is
// not_found, and both close-policy refusals are not_closable with the open-
// children count present only for the children refusal — the member-presence
// discriminator failClose uses, per item.
//
// The count and the detail come from the typed error's own field, never from
// parsing the sentinel's message, exactly as WithOpenChildren requires. The
// default branch folds anything else — a role that returned an unclassified
// error, or the neither-issue-nor-error fault checkedBatchCloser synthesizes —
// into not_closable, so the codes this function emits are exactly
// batchCloseItemErrorCodes. New per-item codes are ADDED here, which is what the
// schema's "default-branch on an unknown value" note tells clients to expect.
func batchCloseItemError(err error) apigen.BatchCloseItemError {
	detail := func(s string) *string { return &s }

	var openChildren *issueops.CloseOpenChildrenError
	switch {
	case errors.As(err, &openChildren):
		n := openChildren.OpenChildren
		return apigen.BatchCloseItemError{
			Code:         apigen.BatchCloseItemErrorCodeNotClosable,
			Detail:       detail("issue has open children; close them first or close with force"),
			OpenChildren: &n,
		}
	case errors.Is(err, issueops.ErrCloseOpenChildren):
		// The sentinel without the typed carrier. There is no count to publish, so
		// none is — the live-blocker shape, which is the honest report when the
		// number the children refusal needs did not arrive.
		return apigen.BatchCloseItemError{
			Code:   apigen.BatchCloseItemErrorCodeNotClosable,
			Detail: detail("issue has open children; close them first or close with force"),
		}
	case errors.Is(err, issueops.ErrCloseBlocked):
		return apigen.BatchCloseItemError{
			Code:   apigen.BatchCloseItemErrorCodeNotClosable,
			Detail: detail("issue is blocked; clear the blocker or close with force"),
		}
	case errors.Is(err, issueops.ErrNotFound):
		return apigen.BatchCloseItemError{
			Code:   apigen.BatchCloseItemErrorCodeNotFound,
			Detail: detail("no issue or wisp with that id"),
		}
	default:
		return apigen.BatchCloseItemError{
			Code:   apigen.BatchCloseItemErrorCodeNotClosable,
			Detail: detail("the item could not be closed"),
		}
	}
}

// failBatchClose answers a failed batch.
//
// The method's own error is reserved for request validation, cancellation and
// infrastructure — the failures that mean the batch NEVER RAN — so a per-item
// refusal never reaches here; it is a 200 outcome. The role's ErrValidation is
// answered as the 400 it is in the SERVER's own words rather than quoting the
// storage sentence, and without a `param`: it is about the request as a whole,
// which the handler already validated member by member, so this branch is
// defensive. Everything else keeps the shared classifier's codes.
func (s *Server) failBatchClose(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, issueops.ErrValidation) {
		s.event("request_refused", "request_id", requestInfo(r.Context()).id, "error", err.Error())
		s.fail(w, r, InvalidArgument("", ReasonInvalidValue,
			"the batch was refused by validation; nothing was closed"))
		return
	}
	s.failErr(w, r, err)
}

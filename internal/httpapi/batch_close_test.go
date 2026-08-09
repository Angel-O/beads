package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/httpapi/apigen"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/issueops"
)

// These cover the transport half of POST /v0/beads/issues:batchClose — the body
// shape, the refusals this operation owns, how the role's per-item outcomes are
// mapped onto the wire, and the claim_next decode's parity with the ready query.
// Everything below the wire is the role's, pinned by the BatchCloser conformance
// contract at all three backends.

const batchClosePath = "/v0/beads/issues:batchClose"

func newBatchCloseServer(t *testing.T, closer *roleBatchCloser) *testServer {
	t.Helper()
	return newTestServer(t, rolesConfig(Config{BatchCloser: closer}))
}

// TestBatchClosePathReachesItsHandler drives the DOCUMENTED path and asserts it
// reaches the batch-close handler rather than the claim's wide POST wildcard: the
// literal `:batchClose` is registered after `/v0/beads/issues/{idop}` and
// ServeMux prefers it by specificity, the sweep/delete precedent.
func TestBatchClosePathReachesItsHandler(t *testing.T) {
	closer := &roleBatchCloser{}
	ts := newBatchCloseServer(t, closer)

	resp := ts.claim(t, batchClosePath, `{"actor":"alice","items":[{"id":"bd-1"}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, readAll(t, resp))
	}
	if calls := closer.closeRequests(); len(calls) != 1 {
		t.Fatalf("role calls = %d, want 1 — the request did not reach the batch-close handler", len(calls))
	}
}

// TestBatchClosePassesTheRequestToTheRoleAndAnswersWithOutcomes pins the
// projection both ways: the wire members become the role's request, and the
// role's outcomes come back in request order carrying the snapshot, the
// idempotent flag and the open-child count.
func TestBatchClosePassesTheRequestToTheRoleAndAnswersWithOutcomes(t *testing.T) {
	closer := &roleBatchCloser{}
	ts := newBatchCloseServer(t, closer)

	resp := ts.claim(t, batchClosePath, `{"actor":"alice","session":"s-1","force":true,"items":[
		{"id":"bd-1","reason":"done"},
		{"id":"bd-2"}
	]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, readAll(t, resp))
	}
	body := decodeBody(t, resp)
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v, want two outcomes", body["items"])
	}
	for i, want := range []string{"bd-1", "bd-2"} {
		outcome, _ := items[i].(map[string]any)
		issue, ok := outcome["issue"].(map[string]any)
		if !ok {
			t.Fatalf("items[%d] carries no issue: %v", i, outcome)
		}
		if issue["id"] != want {
			t.Errorf("items[%d].issue.id = %v, want %q in request order", i, issue["id"], want)
		}
		if _, present := outcome["error"]; present {
			t.Errorf("items[%d] carries both issue and error; exactly one is the contract", i)
		}
		if outcome["already_closed"] != false {
			t.Errorf("items[%d].already_closed = %v, want false for a landed close", i, outcome["already_closed"])
		}
	}
	if _, present := body["claimed_next"]; present {
		t.Error("claimed_next is present without a claim_next request")
	}

	reqs := closer.closeRequests()
	if len(reqs) != 1 {
		t.Fatalf("role calls = %d, want 1", len(reqs))
	}
	got := reqs[0]
	if got.Actor != "alice" || got.Session != "s-1" || !got.Force {
		t.Errorf("request = %+v, want the wire's actor, session and force", got)
	}
	if len(got.Items) != 2 || got.Items[0].IssueID != "bd-1" || got.Items[0].Reason != "done" {
		t.Errorf("items = %+v, want the wire's ids and per-item reason", got.Items)
	}
	if got.Items[1].Reason != "" {
		t.Errorf("items[1].reason = %q, want empty: an omitted reason is not a lie", got.Items[1].Reason)
	}
	if got.ClaimNext != nil {
		t.Errorf("claim_next = %+v, want nil: none was sent", got.ClaimNext)
	}
}

// TestBatchCloseMapsPerItemOutcomes is the heart of the operation: a mixed batch
// of successes and refusals, each mapped to EXACTLY ONE of issue or error, with
// the open-children member present only where the discriminator says it should
// be.
func TestBatchCloseMapsPerItemOutcomes(t *testing.T) {
	closer := &roleBatchCloser{outcomes: []issueops.CloseOutcome{
		// A forced close reporting the children it observed.
		{IssueID: "bd-1", Issue: &types.Issue{ID: "bd-1", Status: types.StatusClosed}, Changed: true, OpenChildren: 2},
		// An id that names nothing: not_found, no children member.
		{IssueID: "bd-2", Err: issueops.ErrNotFound},
		// Open children refused an unforced close: not_closable WITH the count.
		{IssueID: "bd-3", Err: &issueops.CloseOpenChildrenError{IssueID: "bd-3", OpenChildren: 3}},
		// A live blocker: not_closable with NO count.
		{IssueID: "bd-4", Err: issueops.ErrCloseBlocked},
		// Idempotent re-close: a success that changed nothing.
		{IssueID: "bd-5", Issue: &types.Issue{ID: "bd-5", Status: types.StatusClosed}, Changed: false},
	}}
	ts := newBatchCloseServer(t, closer)

	resp := ts.claim(t, batchClosePath, `{"actor":"alice","items":[
		{"id":"bd-1"},{"id":"bd-2"},{"id":"bd-3"},{"id":"bd-4"},{"id":"bd-5"}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a batch that refused items still RAN): %s", resp.StatusCode, readAll(t, resp))
	}
	items, _ := decodeBody(t, resp)["items"].([]any)
	if len(items) != 5 {
		t.Fatalf("items = %d, want 5 in request order", len(items))
	}

	for i, outcome := range items {
		m, _ := outcome.(map[string]any)
		_, hasIssue := m["issue"]
		_, hasError := m["error"]
		if hasIssue == hasError {
			t.Errorf("items[%d] has issue=%v error=%v; exactly one is the contract", i, hasIssue, hasError)
		}
	}

	// items[0]: forced-close success carries open_children on the ISSUE side.
	if got := items[0].(map[string]any); got["already_closed"] != false || got["open_children"] != float64(2) {
		t.Errorf("items[0] = %v, want already_closed false and open_children 2", got)
	}
	// items[1]: not_found, no open_children.
	if e := items[1].(map[string]any)["error"].(map[string]any); e["code"] != "not_found" {
		t.Errorf("items[1].error.code = %v, want not_found", e["code"])
	} else if _, present := e["open_children"]; present {
		t.Error("items[1].error carries open_children; not_found never does")
	}
	// items[2]: not_closable WITH the count.
	if e := items[2].(map[string]any)["error"].(map[string]any); e["code"] != "not_closable" || e["open_children"] != float64(3) {
		t.Errorf("items[2].error = %v, want not_closable with open_children 3", e)
	}
	// items[3]: not_closable, the live-blocker shape — member ABSENT is the discriminator.
	if e := items[3].(map[string]any)["error"].(map[string]any); e["code"] != "not_closable" {
		t.Errorf("items[3].error.code = %v, want not_closable", e["code"])
	} else if _, present := e["open_children"]; present {
		t.Error("items[3].error carries open_children; the live-blocker refusal must not, that is the discriminator")
	}
	// items[4]: idempotent re-close.
	if got := items[4].(map[string]any); got["already_closed"] != true {
		t.Errorf("items[4].already_closed = %v, want true for an unchanged re-close", got["already_closed"])
	}
}

// TestBatchClosePostCommitFaultIsFoldedPerItem pins checkedBatchCloser: an
// outcome the role reports with NEITHER a snapshot nor a refusal is folded into
// that item's own error rather than tanking the whole batch — the survivors
// already committed, so the response keeps them.
func TestBatchClosePostCommitFaultIsFoldedPerItem(t *testing.T) {
	closer := &roleBatchCloser{outcomes: []issueops.CloseOutcome{
		{IssueID: "bd-1", Issue: &types.Issue{ID: "bd-1", Status: types.StatusClosed}, Changed: true},
		{IssueID: "bd-2"}, // neither issue nor error: a post-commit fault
	}}
	ts := newBatchCloseServer(t, closer)

	resp := ts.claim(t, batchClosePath, `{"actor":"alice","items":[{"id":"bd-1"},{"id":"bd-2"}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the survivors committed: %s", resp.StatusCode, readAll(t, resp))
	}
	items, _ := decodeBody(t, resp)["items"].([]any)
	if got := items[0].(map[string]any); got["issue"] == nil {
		t.Errorf("items[0] lost its committed snapshot: %v", got)
	}
	folded, _ := items[1].(map[string]any)
	if folded["issue"] != nil {
		t.Errorf("items[1] carries an issue for an outcome that had none: %v", folded)
	}
	e, ok := folded["error"].(map[string]any)
	if !ok || e["code"] != "not_closable" {
		t.Errorf("items[1].error = %v, want the folded fault as not_closable (the default branch)", folded["error"])
	}
	assertNoPanic(t, ts)
}

// TestBatchCloseReturnsClaimedNext pins that a claim the role won comes back in
// claimed_next, hydrated with the counts the ready element carries.
func TestBatchCloseReturnsClaimedNext(t *testing.T) {
	closer := &roleBatchCloser{claimedNext: &types.IssueWithCounts{Issue: &types.Issue{ID: "bd-next"}, DependencyCount: 1}}
	ts := newBatchCloseServer(t, closer)

	resp := ts.claim(t, batchClosePath, `{"actor":"alice","items":[{"id":"bd-1"}],"claim_next":{"assignee":"alice"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, readAll(t, resp))
	}
	body := decodeBody(t, resp)
	claimed, ok := body["claimed_next"].(map[string]any)
	if !ok {
		t.Fatalf("claimed_next is absent: %v", body)
	}
	if claimed["id"] != "bd-next" {
		t.Errorf("claimed_next.id = %v, want bd-next", claimed["id"])
	}

	reqs := closer.closeRequests()
	if len(reqs) != 1 || reqs[0].ClaimNext == nil {
		t.Fatalf("role received claim_next = %v, want the decoded filter", reqs)
	}
	if reqs[0].ClaimNext.Assignee != "alice" {
		t.Errorf("claim_next.assignee reached the role as %q, want alice", reqs[0].ClaimNext.Assignee)
	}
	// The limit/offset ClaimNext refuses stay unset, so the role's own refusal is
	// unreachable from this front door.
	if reqs[0].ClaimNext.Limit != nil || reqs[0].ClaimNext.Offset != 0 {
		t.Errorf("claim_next reached the role with a limit/offset set: %+v", reqs[0].ClaimNext)
	}
}

// TestBatchCloseRefusesTheShapesTheDocumentRefuses walks the 400s this operation
// owns. Every one is answered BEFORE the role is reached, which is also the proof
// that nothing was closed — a per-item refusal, by contrast, is a 200 outcome.
func TestBatchCloseRefusesTheShapesTheDocumentRefuses(t *testing.T) {
	long := strings.Repeat("x", types.MaxFieldLen+1)
	for _, test := range []struct {
		name      string
		body      string
		wantParam string
	}{
		{"no actor", `{"items":[{"id":"bd-1"}]}`, "actor"},
		{"blank actor", `{"actor":"  ","items":[{"id":"bd-1"}]}`, "actor"},
		{"actor with a newline", "{\"actor\":\"alice\\nbd: close\",\"items\":[{\"id\":\"bd-1\"}]}", "actor"},
		{"null actor", `{"actor":null,"items":[{"id":"bd-1"}]}`, "actor"},
		{"no items", `{"actor":"alice"}`, "items"},
		{"empty items", `{"actor":"alice","items":[]}`, "items"},
		{"items is not an array", `{"actor":"alice","items":{"id":"bd-1"}}`, "items"},
		{"unknown top-level member", `{"actor":"alice","items":[{"id":"bd-1"}],"dry_run":true}`, "dry_run"},
		{"unknown item member", `{"actor":"alice","items":[{"id":"bd-1","force":true}]}`, "items[0].force"},
		{"no id", `{"actor":"alice","items":[{"reason":"r"}]}`, "items[0].id"},
		{"blank id", `{"actor":"alice","items":[{"id":"  "}]}`, "items[0].id"},
		{"id is not a string", `{"actor":"alice","items":[{"id":7}]}`, "items[0]"},
		{"over-long id", `{"actor":"alice","items":[{"id":"` + long + `"}]}`, "items[0].id"},
		{"over-long reason", `{"actor":"alice","items":[{"id":"bd-1","reason":"` + long + `"}]}`, "items[0].reason"},
		{"reason with a control character", "{\"actor\":\"alice\",\"items\":[{\"id\":\"bd-1\",\"reason\":\"a\\u0007b\"}]}", "items[0].reason"},
		{"the second item is the bad one", `{"actor":"alice","items":[{"id":"bd-1"},{"id":""}]}`, "items[1].id"},
		{"session with a control character", "{\"actor\":\"alice\",\"session\":\"a\\u0007b\",\"items\":[{\"id\":\"bd-1\"}]}", "session"},
	} {
		t.Run(test.name, func(t *testing.T) {
			closer := &roleBatchCloser{}
			ts := newBatchCloseServer(t, closer)

			resp := ts.claim(t, batchClosePath, test.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", resp.StatusCode, readAll(t, resp))
			}
			body := decodeBody(t, resp)
			if body["code"] != string(CodeInvalidArgument) {
				t.Errorf("code = %v, want %q", body["code"], CodeInvalidArgument)
			}
			if body["param"] != test.wantParam {
				t.Errorf("param = %v, want %q — a client dispatches on this", body["param"], test.wantParam)
			}
			if calls := closer.closeRequests(); len(calls) != 0 {
				t.Errorf("the role was called %d times for a refused request; nothing may be closed", len(calls))
			}
		})
	}
}

// TestBatchCloseRefusesAMalformedClaimNext pins that the claim_next object is
// decoded with the ready query's own validators: a malformed value is refused
// under `claim_next.member`, and an unknown member is version skew.
func TestBatchCloseRefusesAMalformedClaimNext(t *testing.T) {
	for _, test := range []struct {
		name       string
		claimNext  string
		wantParam  string
		wantReason Reason
	}{
		{"claim_next is not an object", `"soon"`, "claim_next", ReasonInvalidValue},
		{"unassigned is not a boolean", `{"unassigned":"yes"}`, "claim_next.unassigned", ReasonInvalidValue},
		{"priority is not an integer", `{"priority":"high"}`, "claim_next.priority", ReasonInvalidValue},
		{"label is not an array", `{"label":"api"}`, "claim_next.label", ReasonInvalidValue},
		{"metadata_field is not key=value", `{"metadata_field":["noeq"]}`, "claim_next.metadata_field", ReasonInvalidValue},
		{"unknown member", `{"bogus":true}`, "claim_next.bogus", ReasonUnknownParameter},
	} {
		t.Run(test.name, func(t *testing.T) {
			closer := &roleBatchCloser{}
			ts := newBatchCloseServer(t, closer)

			resp := ts.claim(t, batchClosePath, `{"actor":"alice","items":[{"id":"bd-1"}],"claim_next":`+test.claimNext+`}`)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", resp.StatusCode, readAll(t, resp))
			}
			body := decodeBody(t, resp)
			if body["param"] != test.wantParam {
				t.Errorf("param = %v, want %q", body["param"], test.wantParam)
			}
			if body["reason"] != string(test.wantReason) {
				t.Errorf("reason = %v, want %q", body["reason"], test.wantReason)
			}
			if calls := closer.closeRequests(); len(calls) != 0 {
				t.Error("the role was called for a request with a bad claim_next")
			}
		})
	}
}

// TestBatchCloseClaimNextDecodesLikeTheReadyQuery is the parity pin the design
// asks for directly: the JSON claim_next object and the ready URL query, given
// the same filter values, build the SAME ReadyRequest through the SAME
// readyFilters. Copying the decoder is what would let the two come apart.
func TestBatchCloseClaimNextDecodesLikeTheReadyQuery(t *testing.T) {
	query := newQuery(url.Values{
		"assignee":          {"alice"},
		"unassigned":        {"true"},
		"type":              {"bug"},
		"exclude_type":      {"gate", "rig"},
		"label":             {"a", "b"},
		"priority":          {"2"},
		"parent":            {"bd-9"},
		"metadata_field":    {"env=prod"},
		"has_metadata_key":  {"team"},
		"include_ephemeral": {"true"},
	})
	fromQuery := readyFilters(query)
	if res := query.result(); res != nil {
		t.Fatalf("the ready query refused a valid filter: %+v", res)
	}

	object := newReadyFilterObject(map[string]json.RawMessage{
		"assignee":          json.RawMessage(`"alice"`),
		"unassigned":        json.RawMessage(`true`),
		"type":              json.RawMessage(`"bug"`),
		"exclude_type":      json.RawMessage(`["gate","rig"]`),
		"label":             json.RawMessage(`["a","b"]`),
		"priority":          json.RawMessage(`2`),
		"parent":            json.RawMessage(`"bd-9"`),
		"metadata_field":    json.RawMessage(`["env=prod"]`),
		"has_metadata_key":  json.RawMessage(`"team"`),
		"include_ephemeral": json.RawMessage(`true`),
	})
	fromObject := readyFilters(object)
	if res := object.result(); res != nil {
		t.Fatalf("the claim_next object refused a valid filter: %+v", res)
	}

	if !reflect.DeepEqual(fromQuery, fromObject) {
		t.Errorf("claim_next decoded a different ReadyRequest than the ready query:\n query  = %+v\n object = %+v", fromQuery, fromObject)
	}
}

// TestBatchCloseCapBoundary pins the one size bound this operation owns from both
// sides: 100 items serves, 101 refuses before the role is reached.
func TestBatchCloseCapBoundary(t *testing.T) {
	for _, test := range []struct {
		name       string
		count      int
		wantStatus int
		wantCalls  int
	}{
		{"at the cap", maxBatchCloseItems, http.StatusOK, 1},
		{"over the cap", maxBatchCloseItems + 1, http.StatusBadRequest, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			closer := &roleBatchCloser{}
			ts := newBatchCloseServer(t, closer)

			ids := make([]string, test.count)
			for i := range ids {
				ids[i] = fmt.Sprintf(`{"id":"bd-%d"}`, i)
			}
			resp := ts.claim(t, batchClosePath, `{"actor":"alice","items":[`+strings.Join(ids, ",")+`]}`)
			if resp.StatusCode != test.wantStatus {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode, test.wantStatus, readAll(t, resp))
			}
			if test.wantStatus == http.StatusBadRequest {
				if body := decodeBody(t, resp); body["param"] != "items" {
					t.Errorf("param = %v, want items", body["param"])
				}
			}
			if calls := closer.closeRequests(); len(calls) != test.wantCalls {
				t.Errorf("role calls = %d, want %d", len(calls), test.wantCalls)
			}
		})
	}
}

// TestBatchCloseNamesARoleValidationRefusal is failBatchClose's reason to exist:
// the role's request-level ErrValidation is answered as the 400 it is, in the
// server's own words. A non-validation failure keeps the shared classifier's 500.
func TestBatchCloseNamesARoleValidationRefusal(t *testing.T) {
	t.Run("ErrValidation is a 400", func(t *testing.T) {
		ts := newBatchCloseServer(t, &roleBatchCloser{err: fmt.Errorf("%w: close batch requires an actor", issueops.ErrValidation)})
		resp := ts.claim(t, batchClosePath, `{"actor":"alice","items":[{"id":"bd-1"}]}`)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", resp.StatusCode, readAll(t, resp))
		}
		if body := decodeBody(t, resp); body["code"] != string(CodeInvalidArgument) {
			t.Errorf("code = %v, want %q", body["code"], CodeInvalidArgument)
		}
	})
	t.Run("a generic failure is a 500", func(t *testing.T) {
		ts := newBatchCloseServer(t, &roleBatchCloser{err: errors.New("connection reset by the void")})
		resp := ts.claim(t, batchClosePath, `{"actor":"alice","items":[{"id":"bd-1"}]}`)
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500: %s", resp.StatusCode, readAll(t, resp))
		}
		if body := decodeBody(t, resp); body["code"] != string(CodeInternal) {
			t.Errorf("code = %v, want %q", body["code"], CodeInternal)
		}
	})
}

// TestBatchCloseRefusesAMiscountedResult pins the whole-batch half of
// checkedBatchCloser: a result that cannot be walked against the request is the
// generic 500 rather than a truncated or misaligned answer.
func TestBatchCloseRefusesAMiscountedResult(t *testing.T) {
	closer := &roleBatchCloser{outcomes: []issueops.CloseOutcome{
		{IssueID: "bd-1", Issue: &types.Issue{ID: "bd-1"}, Changed: true},
	}}
	ts := newBatchCloseServer(t, closer)

	resp := ts.claim(t, batchClosePath, `{"actor":"alice","items":[{"id":"bd-1"},{"id":"bd-2"}]}`)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for a result with the wrong outcome count: %s", resp.StatusCode, readAll(t, resp))
	}
	assertNoPanic(t, ts)
}

// TestBatchCloseRefusesANonJSONContentType pins the CSRF control this operation
// inherits from the claim: a JSON content type is not CORS-simple, so a
// cross-origin write always triggers a preflight this server never approves.
func TestBatchCloseRefusesANonJSONContentType(t *testing.T) {
	closer := &roleBatchCloser{}
	ts := newBatchCloseServer(t, closer)

	resp := ts.postBody(t, batchClosePath, "text/plain", `{"actor":"alice","items":[{"id":"bd-1"}]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, readAll(t, resp))
	}
	if body := decodeBody(t, resp); body["param"] != "Content-Type" {
		t.Errorf("param = %v, want Content-Type", body["param"])
	}
	if calls := closer.closeRequests(); len(calls) != 0 {
		t.Error("the role was called for a request that skipped the preflight")
	}
}

// TestBatchCloseRefusesAQueryParameter pins that the document-level unknown-
// parameter rule reaches this operation too: it declares no parameters.
func TestBatchCloseRefusesAQueryParameter(t *testing.T) {
	ts := newBatchCloseServer(t, &roleBatchCloser{})

	resp := ts.claim(t, batchClosePath+"?force=true", `{"actor":"alice","items":[{"id":"bd-1"}]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, readAll(t, resp))
	}
	body := decodeBody(t, resp)
	if body["param"] != "force" || body["reason"] != string(ReasonUnknownParameter) {
		t.Errorf("param/reason = %v/%v, want force/unknown_parameter", body["param"], body["reason"])
	}
}

// TestBatchCloseIsNotReachableByOtherMethods pins that the collection custom
// method is a POST and nothing else.
func TestBatchCloseIsNotReachableByOtherMethods(t *testing.T) {
	ts := newBatchCloseServer(t, &roleBatchCloser{})

	if resp := ts.get(t, batchClosePath); resp.StatusCode == http.StatusOK {
		t.Fatalf("GET %s = 200; the custom method is a POST", batchClosePath)
	}
}

// TestBatchCloseItemErrorGovernance is the NAMED three-way pin: the spec's
// BatchCloseItemError.code enum, the set the handler actually emits, and the
// frozen problem registry are one vocabulary. A per-item code added to any one
// without the others fails here.
func TestBatchCloseItemErrorGovernance(t *testing.T) {
	// The handler's emission set, driven over the whole close vocabulary plus an
	// unclassified error that exercises the default branch.
	emitted := map[string]bool{}
	for _, err := range []error{
		issueops.ErrNotFound,
		issueops.ErrCloseBlocked,
		issueops.ErrCloseOpenChildren,
		&issueops.CloseOpenChildrenError{IssueID: "bd-1", OpenChildren: 2},
		errors.New("a fault no close sentinel names"),
	} {
		emitted[string(batchCloseItemError(err).Code)] = true
	}

	// The declared set is the bridge the other two legs pin to.
	declared := map[string]bool{}
	for _, c := range batchCloseItemErrorCodes {
		declared[string(c)] = true
	}
	if !reflect.DeepEqual(emitted, declared) {
		t.Errorf("batchCloseItemError emits %v, batchCloseItemErrorCodes declares %v", emitted, declared)
	}

	// The documented registry: exactly {not_found, not_closable}, and every one a
	// FROZEN problem code with a status behind it.
	registry := map[string]bool{string(CodeNotFound): true, string(CodeNotClosable): true}
	for c := range declared {
		if Code(c).Status() == 0 {
			t.Errorf("per-item code %q is not in the frozen problem vocabulary", c)
		}
	}
	if !reflect.DeepEqual(declared, registry) {
		t.Errorf("per-item codes %v, the documented registry is %v", declared, registry)
	}

	// The spec enum.
	doc := loadSpec(t)
	schema := mapAt(t, mapAt(t, mapAt(t, doc, "components"), "schemas"), "BatchCloseItemError")
	code := mapAt(t, mapAt(t, schema, "properties"), "code")
	specEnum := map[string]bool{}
	for _, c := range toStrings(t, code["enum"]) {
		specEnum[c] = true
	}
	if !reflect.DeepEqual(specEnum, declared) {
		t.Errorf("spec BatchCloseItemError.code enum %v, the handler emits %v", specEnum, declared)
	}
}

// TestBatchCloseRequestMembersMatchTheHandler is the claim's and the close's
// gate for the batch-close bodies, at BOTH object levels: the handler decodes raw
// members so it can name the offending one, which makes additionalProperties:
// false enforceable — at the cost of a hand-rolled member list with nothing tying
// it to the document. A spec revision adding an optional member would otherwise
// leave every spec test green while the server refused it as unknown_parameter.
func TestBatchCloseRequestMembersMatchTheHandler(t *testing.T) {
	doc := loadSpec(t)
	schemas := mapAt(t, mapAt(t, doc, "components"), "schemas")

	for _, tc := range []struct {
		schema   string
		accepted []string
		goType   reflect.Type
	}{
		{"BatchCloseRequest", batchCloseRequestMembers, reflect.TypeOf(apigen.BatchCloseRequest{})},
		{"BatchCloseItem", batchCloseItemMembers, reflect.TypeOf(apigen.BatchCloseItem{})},
	} {
		t.Run(tc.schema, func(t *testing.T) {
			accepted := map[string]bool{}
			for _, name := range tc.accepted {
				accepted[name] = true
			}

			goFields := jsonTagNames(t, tc.goType)
			if extra := diff(goFields, accepted); len(extra) > 0 {
				t.Errorf("generated %s declares members the handler refuses as unknown: %v", tc.schema, extra)
			}
			if missing := diff(accepted, goFields); len(missing) > 0 {
				t.Errorf("the handler accepts members %s does not declare: %v", tc.schema, missing)
			}

			specProps := schemaProperties(t, doc, mapAt(t, schemas, tc.schema))
			if extra := diff(specProps, accepted); len(extra) > 0 {
				t.Errorf("the %s schema documents members the handler refuses: %v", tc.schema, extra)
			}
			if missing := diff(accepted, specProps); len(missing) > 0 {
				t.Errorf("the handler accepts members the %s schema does not document: %v", tc.schema, missing)
			}
		})
	}
}

// TestClaimNextMembersMatchTheReadyFilterDecode is the same gate for the
// claim_next object, and it derives the handler's accepted set from the decode
// itself: every member readyFilters reads is marked on the source, so the
// allowlist is the vocabulary rather than a second copy of it. The ready query
// reads that same set through the same function, so this also pins that the two
// front doors admit the same members.
func TestClaimNextMembersMatchTheReadyFilterDecode(t *testing.T) {
	src := newReadyFilterObject(map[string]json.RawMessage{})
	_ = readyFilters(src)
	accepted := src.read
	if len(accepted) == 0 {
		t.Fatal("readyFilters read no members; the decode is the source of truth for the vocabulary")
	}

	goFields := jsonTagNames(t, reflect.TypeOf(apigen.ClaimNextRequest{}))
	if extra := diff(goFields, accepted); len(extra) > 0 {
		t.Errorf("generated ClaimNextRequest declares members claim_next refuses as unknown: %v", extra)
	}
	if missing := diff(accepted, goFields); len(missing) > 0 {
		t.Errorf("claim_next accepts members ClaimNextRequest does not declare: %v", missing)
	}

	doc := loadSpec(t)
	schema := mapAt(t, mapAt(t, mapAt(t, doc, "components"), "schemas"), "ClaimNextRequest")
	specProps := schemaProperties(t, doc, schema)
	if extra := diff(specProps, accepted); len(extra) > 0 {
		t.Errorf("the ClaimNextRequest schema documents members claim_next refuses: %v", extra)
	}
	if missing := diff(accepted, specProps); len(missing) > 0 {
		t.Errorf("claim_next accepts members the ClaimNextRequest schema does not document: %v", missing)
	}
}

var _ issueops.BatchCloser = (*roleBatchCloser)(nil)

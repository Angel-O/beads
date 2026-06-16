// Package planetest provides an in-process fake Plane CE server and a
// conformance suite that runs identically against the fake and a live Plane
// instance. The fake is only trusted because it passes the same suite a
// real v1.3.0 deployment passes — when the suite grows, the fake must keep
// up, and when Plane's behavior changes, the live run catches the drift.
package planetest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/plane"
)

// ConformanceTarget describes the Plane instance under test.
type ConformanceTarget struct {
	// BaseURL is the instance root, e.g. https://plane.example.com or an
	// httptest server URL.
	BaseURL string
	// APIKey authenticates every request.
	APIKey string
	// Workspace is the workspace slug.
	Workspace string
	// ProjectID is the UUID of a project the suite may freely write to.
	// Live runs should point this at a dedicated throwaway project.
	ProjectID string
	// Live is true when running against a real deployment.
	Live bool
}

// uniqueSuffix returns a suffix for entity names so that live conformance
// runs do not collide with leftovers from previous runs.
func uniqueSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// RunConformance executes the full conformance battery against the target.
// Every behavior asserted here is part of the adapter's dependency surface
// on Plane: if the fake and a live instance both pass, the adapter's unit
// tests against the fake are trustworthy.
func RunConformance(t *testing.T, target ConformanceTarget) {
	t.Helper()
	client := plane.NewClient(target.BaseURL, target.APIKey, target.Workspace, target.ProjectID)

	t.Run("ProjectExists", func(t *testing.T) { conformProject(t, client) })
	t.Run("ProjectsList", func(t *testing.T) { conformProjectsList(t, client) })
	t.Run("StatesHaveAllGroups", func(t *testing.T) { conformStates(t, client) })
	t.Run("StateLifecycle", func(t *testing.T) { conformStateLifecycle(t, target, client) })
	t.Run("IssueCRUDLifecycle", func(t *testing.T) { conformIssueLifecycle(t, client) })
	t.Run("WorkItemDelete", func(t *testing.T) { conformWorkItemDelete(t, target, client) })
	t.Run("PutMethodNotAllowed", func(t *testing.T) { conformPutNotAllowed(t, target) })
	t.Run("ExternalIDIdempotency", func(t *testing.T) { conformExternalID(t, client) })
	t.Run("PatchExternalIDConflict", func(t *testing.T) { conformPatchExternalIDConflict(t, client) })
	t.Run("CreatedAtSpoofing", func(t *testing.T) { conformCreatedAtSpoofing(t, client) })
	t.Run("ListPagination", func(t *testing.T) { conformPagination(t, target, client) })
	t.Run("LabelLifecycle", func(t *testing.T) { conformLabels(t, target, client) })
	t.Run("CommentLifecycle", func(t *testing.T) { conformComments(t, target, client) })
	t.Run("ParentChild", func(t *testing.T) { conformParentChild(t, client) })
	t.Run("IdentifierLookup", func(t *testing.T) { conformIdentifierLookup(t, target, client) })
	t.Run("AuthFailures", func(t *testing.T) { conformAuthFailures(t, target) })
}

// rawRequest performs one authenticated request outside the production
// client, for asserting wire-level behaviors (status codes, error bodies,
// pagination envelopes) the client does not surface directly. It returns the
// status code and raw response body.
func rawRequest(t *testing.T, target ConformanceTarget, method, path string, body any) (int, []byte) {
	t.Helper()
	var payload []byte
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encoding %s %s body: %v", method, path, err)
		}
		payload = data
	}
	// Live Plane throttles per API key (API_KEY_RATE_LIMIT, default
	// 60/minute) regardless of the token's allowed_rate_limit, so raw
	// wire-level checks must wait out 429s like the production client does.
	// The fake never throttles, so this loop runs once against it.
	for attempt := 0; ; attempt++ {
		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequest(method, target.BaseURL+"/api/v1"+path, reader)
		if err != nil {
			t.Fatalf("building %s %s: %v", method, path, err)
		}
		req.Header.Set("X-Api-Key", target.APIKey)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		data, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("reading %s %s response: %v", method, path, err)
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 5 {
			wait := 5 * time.Second
			if secs, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && secs > 0 && secs <= 70 {
				wait = time.Duration(secs) * time.Second
			}
			t.Logf("%s %s throttled (429), retrying in %s", method, path, wait)
			time.Sleep(wait)
			continue
		}
		return resp.StatusCode, data
	}
}

// rawProjectPath builds a project-scoped API path for rawRequest.
func rawProjectPath(target ConformanceTarget, suffix string) string {
	return fmt.Sprintf("/workspaces/%s/projects/%s/%s", target.Workspace, target.ProjectID, suffix)
}

func conformProject(t *testing.T, c *plane.Client) {
	p, err := c.GetProject(context.Background())
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if p.ID == "" || p.Identifier == "" {
		t.Errorf("project missing id/identifier: %+v", p)
	}
	if p.Identifier != strings.ToUpper(p.Identifier) {
		t.Errorf("identifier %q not uppercase (Plane stores identifiers uppercased)", p.Identifier)
	}
}

func conformProjectsList(t *testing.T, c *plane.Client) {
	projects, err := c.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	found := false
	for _, p := range projects {
		if p.ID != c.ProjectID() {
			continue
		}
		found = true
		if p.Identifier == "" || p.Name == "" {
			t.Errorf("listed project missing identifier/name: %+v", p)
		}
	}
	if !found {
		t.Errorf("project %s missing from ListProjects (%d projects)", c.ProjectID(), len(projects))
	}
}

func conformStates(t *testing.T, c *plane.Client) {
	states, err := c.ListStates(context.Background())
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	groups := map[string]bool{}
	for _, s := range states {
		if s.ID == "" || s.Name == "" {
			t.Errorf("state missing id/name: %+v", s)
		}
		groups[s.Group] = true
	}
	// Every new Plane project carries default states covering these groups.
	for _, g := range []string{"backlog", "unstarted", "started", "completed", "cancelled"} { //nolint:misspell // Plane API wire value uses the British spelling
		if !groups[g] {
			t.Errorf("no state with group %q (states: %+v)", g, states)
		}
	}
}

// conformStateLifecycle exercises state create/update/delete. v1.3.0 quirks
// pinned here: create returns 200 (not 201); a duplicate name or duplicate
// external pair on create is a 409 carrying the existing state's id; the
// default state and non-empty states cannot be deleted (400).
func conformStateLifecycle(t *testing.T, target ConformanceTarget, c *plane.Client) {
	ctx := context.Background()
	name := "conf-state-" + uniqueSuffix()

	status, body := rawRequest(t, target, http.MethodPost, rawProjectPath(target, "states/"),
		map[string]any{"name": name, "color": "#123456", "group": "started"})
	if status != http.StatusOK {
		t.Fatalf("state create status = %d (%s), want 200 (v1.3.0 returns 200, not 201)", status, body)
	}
	var created struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Group string `json:"group"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decoding created state: %v (%s)", err, body)
	}
	if created.ID == "" || created.Name != name || created.Group != "started" {
		t.Errorf("created state = %+v, want name %q group started", created, name)
	}

	// Duplicate name -> 409 with the existing state's id.
	status, body = rawRequest(t, target, http.MethodPost, rawProjectPath(target, "states/"),
		map[string]any{"name": name, "color": "#123456"})
	if status != http.StatusConflict {
		t.Fatalf("duplicate state create status = %d (%s), want 409", status, body)
	}
	var conflict struct {
		Error string `json:"error"`
		ID    string `json:"id"`
	}
	if err := json.Unmarshal(body, &conflict); err != nil {
		t.Fatalf("decoding state conflict: %v (%s)", err, body)
	}
	if conflict.Error != "State with the same name already exists in the project" {
		t.Errorf("state name conflict error = %q", conflict.Error)
	}
	if conflict.ID != created.ID {
		t.Errorf("state name conflict id = %q, want existing %q", conflict.ID, created.ID)
	}

	// Duplicate external pair -> 409 with the existing state's id.
	extName := name + " ext"
	extID := "conf-state-ext-" + uniqueSuffix()
	status, body = rawRequest(t, target, http.MethodPost, rawProjectPath(target, "states/"),
		map[string]any{"name": extName, "color": "#123456", "external_id": extID, "external_source": "beads"})
	if status != http.StatusOK {
		t.Fatalf("external state create status = %d (%s), want 200", status, body)
	}
	var extState struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &extState); err != nil {
		t.Fatalf("decoding external state: %v (%s)", err, body)
	}
	status, body = rawRequest(t, target, http.MethodPost, rawProjectPath(target, "states/"),
		map[string]any{"name": extName + " 2", "color": "#123456", "external_id": extID, "external_source": "beads"})
	if status != http.StatusConflict {
		t.Fatalf("duplicate external state create status = %d (%s), want 409", status, body)
	}
	if err := json.Unmarshal(body, &conflict); err != nil {
		t.Fatalf("decoding external state conflict: %v (%s)", err, body)
	}
	if conflict.Error != "State with the same external id and external source already exists" {
		t.Errorf("state external conflict error = %q", conflict.Error)
	}
	if conflict.ID != extState.ID {
		t.Errorf("state external conflict id = %q, want existing %q", conflict.ID, extState.ID)
	}

	// PATCH renames.
	renamed := name + " v2"
	status, body = rawRequest(t, target, http.MethodPatch, rawProjectPath(target, "states/"+created.ID+"/"),
		map[string]any{"name": renamed})
	if status != http.StatusOK {
		t.Fatalf("state patch status = %d (%s), want 200", status, body)
	}
	var patched struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &patched); err != nil {
		t.Fatalf("decoding patched state: %v (%s)", err, body)
	}
	if patched.Name != renamed {
		t.Errorf("patched state name = %q, want %q", patched.Name, renamed)
	}

	// A state holding issues cannot be deleted.
	issue, err := c.CreateIssue(ctx, &plane.IssuePayload{Name: "state holder " + name, StateID: created.ID})
	if err != nil {
		t.Fatalf("CreateIssue(state): %v", err)
	}
	status, body = rawRequest(t, target, http.MethodDelete, rawProjectPath(target, "states/"+created.ID+"/"), nil)
	if status != http.StatusBadRequest {
		t.Fatalf("non-empty state delete status = %d (%s), want 400", status, body)
	}
	var errBody struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &errBody); err != nil {
		t.Fatalf("decoding non-empty state delete error: %v (%s)", err, body)
	}
	if errBody.Error != "The state is not empty, only empty states can be deleted" {
		t.Errorf("non-empty state delete error = %q", errBody.Error)
	}

	// Empty, non-default states delete cleanly: move the issue away first.
	states, err := c.ListStates(ctx)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	defaultID := ""
	for _, s := range states {
		if s.Default {
			defaultID = s.ID
			break
		}
	}
	if defaultID == "" {
		t.Fatal("project has no default state")
	}
	if _, err := c.UpdateIssue(ctx, issue.ID, &plane.IssuePayload{StateID: defaultID}); err != nil {
		t.Fatalf("UpdateIssue(move off state): %v", err)
	}
	status, body = rawRequest(t, target, http.MethodDelete, rawProjectPath(target, "states/"+created.ID+"/"), nil)
	if status != http.StatusNoContent {
		t.Fatalf("state delete status = %d (%s), want 204", status, body)
	}

	// The default state cannot be deleted.
	status, body = rawRequest(t, target, http.MethodDelete, rawProjectPath(target, "states/"+defaultID+"/"), nil)
	if status != http.StatusBadRequest {
		t.Fatalf("default state delete status = %d (%s), want 400", status, body)
	}
	if err := json.Unmarshal(body, &errBody); err != nil {
		t.Fatalf("decoding default state delete error: %v (%s)", err, body)
	}
	if errBody.Error != "Default state cannot be deleted" {
		t.Errorf("default state delete error = %q", errBody.Error)
	}
}

func conformIssueLifecycle(t *testing.T, c *plane.Client) {
	ctx := context.Background()
	name := "conformance lifecycle " + uniqueSuffix()

	created, err := c.CreateIssue(ctx, &plane.IssuePayload{
		Name:            name,
		DescriptionHTML: "<p>created by conformance suite</p>",
		Priority:        "high",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if created.ID == "" {
		t.Fatal("created issue has no ID")
	}
	if created.SequenceID == 0 {
		t.Error("created issue has no sequence_id (server must assign one)")
	}
	if created.StateID == "" {
		t.Error("created issue has no state (server must assign the project default)")
	}
	if created.Priority != "high" {
		t.Errorf("priority = %q, want high", created.Priority)
	}

	// GET round-trip.
	got, err := c.GetIssue(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got == nil || got.Name != name {
		t.Fatalf("GetIssue = %+v, want name %q", got, name)
	}
	if !strings.Contains(got.DescriptionHTML, "created by conformance suite") {
		t.Errorf("description_html = %q, content lost", got.DescriptionHTML)
	}

	// PATCH partial update: name only; priority must survive.
	updated, err := c.UpdateIssue(ctx, created.ID, &plane.IssuePayload{Name: name + " v2"})
	if err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if updated.Name != name+" v2" {
		t.Errorf("updated name = %q", updated.Name)
	}
	if updated.Priority != "high" {
		t.Errorf("partial update clobbered priority: %q", updated.Priority)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) && !updated.UpdatedAt.Equal(created.UpdatedAt) {
		t.Errorf("updated_at went backwards: %v -> %v", created.UpdatedAt, updated.UpdatedAt)
	}

	// State transition: move to a completed-group state.
	states, err := c.ListStates(ctx)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	var doneState string
	for _, s := range states {
		if s.Group == "completed" {
			doneState = s.ID
			break
		}
	}
	if doneState == "" {
		t.Fatal("no completed-group state available")
	}
	closed, err := c.UpdateIssue(ctx, created.ID, &plane.IssuePayload{StateID: doneState})
	if err != nil {
		t.Fatalf("UpdateIssue(state): %v", err)
	}
	if closed.StateID != doneState {
		t.Errorf("state = %q, want %q", closed.StateID, doneState)
	}
	if closed.CompletedAt == nil {
		t.Error("completed_at not set after moving to completed group (server recomputes it on save)")
	}

	// Unknown UUID lookups return nil, nil.
	missing, err := c.GetIssue(ctx, "00000000-0000-0000-0000-00000000dead")
	if err != nil {
		t.Fatalf("GetIssue(missing): %v", err)
	}
	if missing != nil {
		t.Errorf("GetIssue(missing) = %+v, want nil", missing)
	}
}

// conformWorkItemDelete pins DELETE semantics: 204 with no body, and the
// issue is gone from subsequent GETs.
func conformWorkItemDelete(t *testing.T, target ConformanceTarget, c *plane.Client) {
	ctx := context.Background()
	created, err := c.CreateIssue(ctx, &plane.IssuePayload{Name: "deleted " + uniqueSuffix()})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	status, body := rawRequest(t, target, http.MethodDelete, rawProjectPath(target, "work-items/"+created.ID+"/"), nil)
	if status != http.StatusNoContent {
		t.Fatalf("DELETE status = %d (%s), want 204", status, body)
	}
	got, err := c.GetIssue(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetIssue(deleted): %v", err)
	}
	if got != nil {
		t.Errorf("issue still fetchable after DELETE: %+v", got)
	}
}

// conformPutNotAllowed pins the v1.3.0 quirk that PUT on the work-items
// collection is 405: the upsert view exists at that version but is never
// routed.
func conformPutNotAllowed(t *testing.T, target ConformanceTarget) {
	status, body := rawRequest(t, target, http.MethodPut, rawProjectPath(target, "work-items/"),
		map[string]any{"name": "put probe"})
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("PUT status = %d (%s), want 405", status, body)
	}
	var detail struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("decoding 405 body: %v (%s)", err, body)
	}
	if detail.Detail != `Method "PUT" not allowed.` {
		t.Errorf("405 detail = %q, want %q", detail.Detail, `Method "PUT" not allowed.`)
	}
}

func conformExternalID(t *testing.T, c *plane.Client) {
	ctx := context.Background()
	externalID := "conformance-" + uniqueSuffix()

	created, err := c.CreateIssue(ctx, &plane.IssuePayload{
		Name:           "external id " + externalID,
		ExternalID:     externalID,
		ExternalSource: "beads",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if created.ExternalID != externalID || created.ExternalSource != "beads" {
		t.Errorf("external fields not persisted: %q/%q", created.ExternalID, created.ExternalSource)
	}

	// Duplicate POST with the same pair returns 409 carrying the existing UUID.
	_, err = c.CreateIssue(ctx, &plane.IssuePayload{
		Name:           "duplicate attempt",
		ExternalID:     externalID,
		ExternalSource: "beads",
	})
	var dup *plane.DuplicateError
	if !errors.As(err, &dup) {
		t.Fatalf("duplicate create error = %v (%T), want *DuplicateError", err, err)
	}
	if dup.ExistingID != created.ID {
		t.Errorf("DuplicateError.ExistingID = %q, want %q", dup.ExistingID, created.ID)
	}

	// GET-by-external-id short-circuit returns the single issue object.
	found, err := c.GetIssueByExternalID(ctx, externalID, "beads")
	if err != nil {
		t.Fatalf("GetIssueByExternalID: %v", err)
	}
	if found == nil || found.ID != created.ID {
		t.Errorf("GetIssueByExternalID = %+v, want id %s", found, created.ID)
	}

	// Unknown pair returns nil, nil (404 from the single-object path).
	missing, err := c.GetIssueByExternalID(ctx, "does-not-exist-"+externalID, "beads")
	if err != nil {
		t.Fatalf("GetIssueByExternalID(missing): %v", err)
	}
	if missing != nil {
		t.Errorf("GetIssueByExternalID(missing) = %+v, want nil", missing)
	}
}

// conformPatchExternalIDConflict pins the PATCH-side 409: re-pointing an
// issue's external pair at one another issue already holds conflicts. Note
// the v1.3.0 quirk (apps/api/plane/api/views/issue.py patch returns
// str(issue.id)): the 409 body carries the PATCHED issue's own id, not the
// conflicting issue's id — unlike the POST 409, callers must not treat it as
// a pointer to the existing entity.
func conformPatchExternalIDConflict(t *testing.T, c *plane.Client) {
	ctx := context.Background()
	suffix := uniqueSuffix()
	first, err := c.CreateIssue(ctx, &plane.IssuePayload{
		Name:           "patch conflict holder " + suffix,
		ExternalID:     "patch-conflict-a-" + suffix,
		ExternalSource: "beads",
	})
	if err != nil {
		t.Fatalf("CreateIssue(first): %v", err)
	}
	second, err := c.CreateIssue(ctx, &plane.IssuePayload{
		Name:           "patch conflict mover " + suffix,
		ExternalID:     "patch-conflict-b-" + suffix,
		ExternalSource: "beads",
	})
	if err != nil {
		t.Fatalf("CreateIssue(second): %v", err)
	}

	_, err = c.UpdateIssue(ctx, second.ID, &plane.IssuePayload{
		ExternalID:     "patch-conflict-a-" + suffix,
		ExternalSource: "beads",
	})
	var dup *plane.DuplicateError
	if !errors.As(err, &dup) {
		t.Fatalf("PATCH conflict error = %v (%T), want *DuplicateError", err, err)
	}
	if dup.ExistingID == first.ID {
		t.Errorf("PATCH 409 id = conflicting issue %q; v1.3.0 returns the patched issue's own id", first.ID)
	}
	if dup.ExistingID != second.ID {
		t.Errorf("PATCH 409 id = %q, want the patched issue's own id %q (v1.3.0 quirk)", dup.ExistingID, second.ID)
	}
}

// conformCreatedAtSpoofing pins the importer-attribution contract the adapter
// relies on for every create: v1.3.0 honors a raw created_at body key on
// POST. The 201 response body is serializer data snapshotted BEFORE the
// override is applied, so the response carries the server-assigned
// created_at and only a subsequent GET shows the spoofed value.
func conformCreatedAtSpoofing(t *testing.T, c *plane.Client) {
	ctx := context.Background()
	spoofed := time.Date(2020, 3, 14, 15, 9, 26, 0, time.UTC)
	externalID := "spoof-" + uniqueSuffix()

	created, err := c.CreateIssue(ctx, &plane.IssuePayload{
		Name:           "created_at spoofing " + externalID,
		ExternalID:     externalID,
		ExternalSource: "beads",
		CreatedAt:      spoofed.Format("2006-01-02T15:04:05.000000Z"),
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if created.CreatedAt.Equal(spoofed) {
		t.Errorf("create response echoed the spoofed created_at %v; v1.3.0 returns the pre-override serializer snapshot", spoofed)
	}
	if !created.CreatedAt.After(spoofed) {
		t.Errorf("create response created_at = %v, want a server-assigned (recent) timestamp", created.CreatedAt)
	}

	got, err := c.GetIssue(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got == nil {
		t.Fatal("GetIssue returned nil for a just-created issue")
	}
	if !got.CreatedAt.Equal(spoofed) {
		t.Errorf("stored created_at = %v, want the spoofed %v (importer attribution)", got.CreatedAt, spoofed)
	}
}

// issuePage is the slice of Plane's pagination envelope the pagination
// assertions need.
type issuePage struct {
	TotalCount      int    `json:"total_count"`
	NextCursor      string `json:"next_cursor"`
	PrevCursor      string `json:"prev_cursor"`
	NextPageResults bool   `json:"next_page_results"`
	PrevPageResults bool   `json:"prev_page_results"`
	Count           int    `json:"count"`
	Results         []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"results"`
}

// fetchIssuePage GETs one page of the project's work items at the given
// per_page/cursor and decodes the pagination envelope.
func fetchIssuePage(t *testing.T, target ConformanceTarget, perPage int, cursor string) issuePage {
	t.Helper()
	path := rawProjectPath(target, "work-items/") + fmt.Sprintf("?per_page=%d", perPage)
	if cursor != "" {
		path += "&cursor=" + url.QueryEscape(cursor)
	}
	status, body := rawRequest(t, target, http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("list page (cursor %q) status = %d (%s), want 200", cursor, status, body)
	}
	var page issuePage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decoding page (cursor %q): %v (%s)", cursor, err, body)
	}
	return page
}

func conformPagination(t *testing.T, target ConformanceTarget, c *plane.Client) {
	ctx := context.Background()
	marker := "pagination-" + uniqueSuffix()

	// Create enough issues to force at least three pages at per_page=2.
	created := map[string]bool{}
	for i := 0; i < 5; i++ {
		issue, err := c.CreateIssue(ctx, &plane.IssuePayload{
			Name: fmt.Sprintf("%s #%d", marker, i),
		})
		if err != nil {
			t.Fatalf("CreateIssue #%d: %v", i, err)
		}
		created[issue.ID] = true
	}

	// Wire-level walk: the server must actually honor per_page and page.
	const perPage = 2
	const maxWalk = 10000 // runaway guard for misbehaving servers
	seen := map[string]int{}
	markerSeen := map[string]int{}
	cursor := ""
	pages := 0
	var firstPrevCursor string
	for {
		page := fetchIssuePage(t, target, perPage, cursor)
		if pages == 0 {
			if len(page.Results) != perPage {
				t.Fatalf("first page has %d results at per_page=%d, want exactly %d", len(page.Results), perPage, perPage)
			}
			if !page.NextPageResults {
				t.Fatalf("first page reports next_page_results=false with %d total items: server ignored per_page", page.TotalCount)
			}
			if page.NextCursor == "" {
				t.Fatal("first page has no next_cursor")
			}
			if page.PrevPageResults {
				t.Error("first page reports prev_page_results=true")
			}
			firstPrevCursor = page.PrevCursor
		}
		if len(page.Results) > perPage {
			t.Errorf("page %d has %d results, exceeds per_page=%d", pages, len(page.Results), perPage)
		}
		if page.NextPageResults && len(page.Results) != perPage {
			t.Errorf("non-final page %d has %d results, want %d", pages, len(page.Results), perPage)
		}
		if page.Count != len(page.Results) {
			t.Errorf("page %d count = %d, want %d (len(results))", pages, page.Count, len(page.Results))
		}
		for _, res := range page.Results {
			seen[res.ID]++
			if strings.HasPrefix(res.Name, marker) {
				markerSeen[res.ID]++
			}
		}
		pages++
		if !page.NextPageResults {
			break
		}
		if page.NextCursor == "" {
			t.Fatal("next_page_results=true but next_cursor is empty")
		}
		if pages >= maxWalk {
			t.Fatalf("pagination did not terminate after %d pages", maxWalk)
		}
		cursor = page.NextCursor
	}
	if pages < 3 {
		t.Errorf("walk served %d pages for >=5 issues at per_page=2, want >= 3", pages)
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("issue %s appeared %d times across pages (pagination duplicate)", id, n)
		}
	}
	// The union of all pages contains exactly the created marker set.
	if len(markerSeen) != len(created) {
		t.Errorf("walk found %d marker issues, want %d", len(markerSeen), len(created))
	}
	for id := range created {
		if markerSeen[id] == 0 {
			t.Errorf("created issue %s missing from paginated walk", id)
		}
	}

	// The page-0 prev_cursor is well-formed but points before the first
	// page: v1.3.0 parses it and rejects the negative offset inside
	// get_result (BadPaginationError -> 400 "Error in parsing").
	if firstPrevCursor == "" {
		t.Error("first page has no prev_cursor")
	} else {
		path := rawProjectPath(target, "work-items/") + fmt.Sprintf("?per_page=%d&cursor=%s", perPage, url.QueryEscape(firstPrevCursor))
		status, body := rawRequest(t, target, http.MethodGet, path, nil)
		if status != http.StatusBadRequest {
			t.Errorf("page-0 prev_cursor %q status = %d (%s), want 400", firstPrevCursor, status, body)
		} else {
			var detail struct {
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal(body, &detail); err != nil {
				t.Errorf("decoding prev_cursor error body: %v (%s)", err, body)
			} else if detail.Detail != "Error in parsing" {
				t.Errorf("prev_cursor error detail = %q, want %q", detail.Detail, "Error in parsing")
			}
		}
	}

	// Malformed cursors are rejected during parsing with a distinct detail.
	path := rawProjectPath(target, "work-items/") + "?per_page=2&cursor=not-a-cursor"
	status, body := rawRequest(t, target, http.MethodGet, path, nil)
	if status != http.StatusBadRequest {
		t.Errorf("malformed cursor status = %d (%s), want 400", status, body)
	} else {
		var detail struct {
			Detail string `json:"detail"`
		}
		if err := json.Unmarshal(body, &detail); err != nil {
			t.Errorf("decoding malformed cursor body: %v (%s)", err, body)
		} else if detail.Detail != "Invalid cursor parameter." {
			t.Errorf("malformed cursor detail = %q, want %q", detail.Detail, "Invalid cursor parameter.")
		}
	}

	// per_page beyond the runtime maximum is rejected.
	path = rawProjectPath(target, "work-items/") + "?per_page=2000"
	status, body = rawRequest(t, target, http.MethodGet, path, nil)
	if status != http.StatusBadRequest {
		t.Errorf("per_page=2000 status = %d (%s), want 400", status, body)
	} else {
		var detail struct {
			Detail string `json:"detail"`
		}
		if err := json.Unmarshal(body, &detail); err != nil {
			t.Errorf("decoding per_page error body: %v (%s)", err, body)
		} else if detail.Detail != "Invalid per_page value. Cannot exceed 1000." {
			t.Errorf("per_page error detail = %q", detail.Detail)
		}
	}

	// The production client reassembles the full set across pages.
	small := plane.NewClient(target.BaseURL, target.APIKey, target.Workspace, target.ProjectID).WithPerPage(perPage)
	issues, err := small.ListIssues(ctx, plane.ListIssuesOptions{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	got := map[string]int{}
	for _, is := range issues {
		got[is.ID]++
	}
	for id := range created {
		if got[id] == 0 {
			t.Errorf("issue %s missing from client paginated list", id)
		}
		if got[id] > 1 {
			t.Errorf("issue %s appeared %d times in client list (pagination duplicate)", id, got[id])
		}
	}
}

func conformLabels(t *testing.T, target ConformanceTarget, c *plane.Client) {
	ctx := context.Background()
	name := "conf-label-" + uniqueSuffix()

	label, err := c.CreateLabel(ctx, name)
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	if label.ID == "" || label.Name != name {
		t.Errorf("label = %+v", label)
	}

	// Duplicate label name returns 409 with the existing UUID.
	_, err = c.CreateLabel(ctx, name)
	var dup *plane.DuplicateError
	if !errors.As(err, &dup) {
		t.Fatalf("duplicate label error = %v (%T), want *DuplicateError", err, err)
	}
	if dup.ExistingID != label.ID {
		t.Errorf("DuplicateError.ExistingID = %q, want %q", dup.ExistingID, label.ID)
	}

	labels, err := c.ListLabels(ctx)
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}
	found := false
	for _, l := range labels {
		if l.ID == label.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("created label %s missing from list", label.ID)
	}

	// PATCH updates label fields.
	status, body := rawRequest(t, target, http.MethodPatch, rawProjectPath(target, "labels/"+label.ID+"/"),
		map[string]any{"color": "#ABCDEF"})
	if status != http.StatusOK {
		t.Fatalf("label patch status = %d (%s), want 200", status, body)
	}
	var patched struct {
		Color string `json:"color"`
	}
	if err := json.Unmarshal(body, &patched); err != nil {
		t.Fatalf("decoding patched label: %v (%s)", err, body)
	}
	if patched.Color != "#ABCDEF" {
		t.Errorf("patched label color = %q, want #ABCDEF", patched.Color)
	}

	// Labels attach to issues and persist as UUID arrays.
	issue, err := c.CreateIssue(ctx, &plane.IssuePayload{
		Name:   "labeled " + name,
		Labels: &[]string{label.ID},
	})
	if err != nil {
		t.Fatalf("CreateIssue(labels): %v", err)
	}
	if len(issue.Labels) != 1 || issue.Labels[0] != label.ID {
		t.Errorf("issue.Labels = %v, want [%s]", issue.Labels, label.ID)
	}

	// Replacing with an empty set clears labels (full-replacement semantics).
	cleared, err := c.UpdateIssue(ctx, issue.ID, &plane.IssuePayload{Labels: &[]string{}})
	if err != nil {
		t.Fatalf("UpdateIssue(clear labels): %v", err)
	}
	if len(cleared.Labels) != 0 {
		t.Errorf("labels not cleared: %v", cleared.Labels)
	}

	// An unattached label deletes cleanly and leaves the list.
	doomed, err := c.CreateLabel(ctx, name+" doomed")
	if err != nil {
		t.Fatalf("CreateLabel(doomed): %v", err)
	}
	status, body = rawRequest(t, target, http.MethodDelete, rawProjectPath(target, "labels/"+doomed.ID+"/"), nil)
	if status != http.StatusNoContent {
		t.Fatalf("label delete status = %d (%s), want 204", status, body)
	}
	labels, err = c.ListLabels(ctx)
	if err != nil {
		t.Fatalf("ListLabels(after delete): %v", err)
	}
	for _, l := range labels {
		if l.ID == doomed.ID {
			t.Errorf("deleted label %s still listed", doomed.ID)
		}
	}
}

func conformComments(t *testing.T, target ConformanceTarget, c *plane.Client) {
	ctx := context.Background()
	issue, err := c.CreateIssue(ctx, &plane.IssuePayload{Name: "commented " + uniqueSuffix()})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	comment, err := c.CreateComment(ctx, issue.ID, "<p>conformance comment</p>")
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if comment.ID == "" {
		t.Error("comment has no ID")
	}

	comments, err := c.ListComments(ctx, issue.ID)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(comments) != 1 || !strings.Contains(comments[0].CommentHTML, "conformance comment") {
		t.Errorf("comments = %+v", comments)
	}

	commentsPath := rawProjectPath(target, "work-items/"+issue.ID+"/comments/")

	// Comments support external-id dedup: a duplicate pair is a 409 carrying
	// the existing comment's id.
	extID := "conf-comment-" + uniqueSuffix()
	extPayload := map[string]any{
		"comment_html":    "<p>external comment</p>",
		"external_id":     extID,
		"external_source": "beads",
	}
	status, body := rawRequest(t, target, http.MethodPost, commentsPath, extPayload)
	if status != http.StatusCreated {
		t.Fatalf("external comment create status = %d (%s), want 201", status, body)
	}
	var extComment struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &extComment); err != nil {
		t.Fatalf("decoding external comment: %v (%s)", err, body)
	}
	status, body = rawRequest(t, target, http.MethodPost, commentsPath, extPayload)
	if status != http.StatusConflict {
		t.Fatalf("duplicate external comment status = %d (%s), want 409", status, body)
	}
	var conflict struct {
		Error string `json:"error"`
		ID    string `json:"id"`
	}
	if err := json.Unmarshal(body, &conflict); err != nil {
		t.Fatalf("decoding comment conflict: %v (%s)", err, body)
	}
	if conflict.Error != "Work item comment with the same external id and external source already exists" {
		t.Errorf("comment conflict error = %q", conflict.Error)
	}
	if conflict.ID != extComment.ID {
		t.Errorf("comment conflict id = %q, want existing %q", conflict.ID, extComment.ID)
	}

	// PATCH replaces comment_html.
	status, body = rawRequest(t, target, http.MethodPatch, commentsPath+comment.ID+"/",
		map[string]any{"comment_html": "<p>edited comment</p>"})
	if status != http.StatusOK {
		t.Fatalf("comment patch status = %d (%s), want 200", status, body)
	}
	var patched struct {
		CommentHTML string `json:"comment_html"`
	}
	if err := json.Unmarshal(body, &patched); err != nil {
		t.Fatalf("decoding patched comment: %v (%s)", err, body)
	}
	if !strings.Contains(patched.CommentHTML, "edited comment") {
		t.Errorf("patched comment_html = %q", patched.CommentHTML)
	}

	// DELETE removes the comment from subsequent lists.
	status, body = rawRequest(t, target, http.MethodDelete, commentsPath+extComment.ID+"/", nil)
	if status != http.StatusNoContent {
		t.Fatalf("comment delete status = %d (%s), want 204", status, body)
	}
	comments, err = c.ListComments(ctx, issue.ID)
	if err != nil {
		t.Fatalf("ListComments(after delete): %v", err)
	}
	if len(comments) != 1 {
		t.Errorf("comments after delete = %+v, want only the edited one", comments)
	}
	for _, cm := range comments {
		if cm.ID == extComment.ID {
			t.Errorf("deleted comment %s still listed", extComment.ID)
		}
	}
}

func conformParentChild(t *testing.T, c *plane.Client) {
	ctx := context.Background()
	parent, err := c.CreateIssue(ctx, &plane.IssuePayload{Name: "parent " + uniqueSuffix()})
	if err != nil {
		t.Fatalf("CreateIssue(parent): %v", err)
	}
	child, err := c.CreateIssue(ctx, &plane.IssuePayload{
		Name:     "child " + uniqueSuffix(),
		ParentID: parent.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue(child): %v", err)
	}
	if child.ParentID != parent.ID {
		t.Errorf("child.ParentID = %q, want %q", child.ParentID, parent.ID)
	}

	got, err := c.GetIssue(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetIssue(child): %v", err)
	}
	if got.ParentID != parent.ID {
		t.Errorf("fetched child.ParentID = %q, want %q", got.ParentID, parent.ID)
	}
}

func conformIdentifierLookup(t *testing.T, target ConformanceTarget, c *plane.Client) {
	ctx := context.Background()
	created, err := c.CreateIssue(ctx, &plane.IssuePayload{Name: "identifier lookup " + uniqueSuffix()})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	project, err := c.GetProject(ctx)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}

	identifier := fmt.Sprintf("%s-%d", project.Identifier, created.SequenceID)
	found, err := c.GetIssueByIdentifier(ctx, identifier)
	if err != nil {
		t.Fatalf("GetIssueByIdentifier(%s): %v", identifier, err)
	}
	if found == nil || found.ID != created.ID {
		t.Errorf("GetIssueByIdentifier(%s) = %+v, want id %s", identifier, found, created.ID)
	}

	// The project-identifier match is case-sensitive and v1.3.0 resolves the
	// project for its permission check before parsing the sequence, so a
	// lowercased (or otherwise unknown) project identifier is a 403
	// PermissionDenied, not a 404 (verified live 2026-06-13). The client
	// surfaces that as *AuthError; the adapter deliberately does NOT fold it
	// into a clean miss so a genuine permission problem can never read as
	// absence.
	if lower := strings.ToLower(identifier); lower != identifier {
		_, err := c.GetIssueByIdentifier(ctx, lower)
		var authErr *plane.AuthError
		if !errors.As(err, &authErr) {
			t.Fatalf("GetIssueByIdentifier(%s) error = %v (%T), want *AuthError from the case-sensitive project resolution", lower, err, err)
		}
		if authErr.StatusCode != http.StatusForbidden {
			t.Errorf("GetIssueByIdentifier(%s) status = %d, want 403", lower, authErr.StatusCode)
		}
		if !strings.Contains(authErr.Detail, "You do not have permission") {
			t.Errorf("GetIssueByIdentifier(%s) detail = %q, want the DRF PermissionDenied detail", lower, authErr.Detail)
		}
	}

	// A well-formed identifier with an unused sequence is a clean miss.
	missIdent := fmt.Sprintf("%s-%d", project.Identifier, created.SequenceID+1000000)
	miss, err := c.GetIssueByIdentifier(ctx, missIdent)
	if err != nil {
		t.Fatalf("GetIssueByIdentifier(%s): %v", missIdent, err)
	}
	if miss != nil {
		t.Errorf("GetIssueByIdentifier(%s) = %+v, want nil", missIdent, miss)
	}

	// A non-numeric sequence component is an HTTP 500 in real v1.3.0: the
	// view feeds it into an IntegerField lookup and the resulting ValueError
	// escapes to the base view's catch-all handler. This IS live behavior,
	// not a fake artifact — the adapter must treat it as an error, never as
	// a clean miss. Retries are disabled because the client retries 5xx GETs
	// with backoff.
	noRetry := plane.NewClient(target.BaseURL, target.APIKey, target.Workspace, target.ProjectID).WithMaxRetries(0)
	_, err = noRetry.GetIssueByIdentifier(ctx, project.Identifier+"-notanumber")
	var apiErr *plane.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("GetIssueByIdentifier(non-numeric) error = %v (%T), want *APIError", err, err)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("GetIssueByIdentifier(non-numeric) status = %d, want 500", apiErr.StatusCode)
	}
}

// conformAuthFailures pins Plane's asymmetric auth errors, verified against
// live deployments: a missing X-Api-Key is a 401 with the DRF
// not-authenticated detail; an invalid key is a 403 with Plane's
// invalid-token detail.
func conformAuthFailures(t *testing.T, target ConformanceTarget) {
	ctx := context.Background()

	noKey := plane.NewClient(target.BaseURL, "", target.Workspace, target.ProjectID)
	_, err := noKey.ListStates(ctx)
	var authErr *plane.AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("missing key error = %v (%T), want *AuthError", err, err)
	}
	if authErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("missing key status = %d, want 401", authErr.StatusCode)
	}
	if authErr.Detail != "Authentication credentials were not provided." {
		t.Errorf("missing key detail = %q, want %q", authErr.Detail, "Authentication credentials were not provided.")
	}

	badKey := plane.NewClient(target.BaseURL, "plane_api_wrong", target.Workspace, target.ProjectID)
	_, err = badKey.ListStates(ctx)
	if !errors.As(err, &authErr) {
		t.Fatalf("invalid key error = %v (%T), want *AuthError", err, err)
	}
	if authErr.StatusCode != http.StatusForbidden {
		t.Errorf("invalid key status = %d, want 403 (Plane returns 403 for bad tokens)", authErr.StatusCode)
	}
	if authErr.Detail != "Given API token is not valid" {
		t.Errorf("invalid key detail = %q, want %q", authErr.Detail, "Given API token is not valid")
	}
}

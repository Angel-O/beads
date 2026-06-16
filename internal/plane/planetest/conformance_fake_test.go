package planetest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/plane"
)

// TestFakePlaneConformance runs the conformance suite against the in-process
// fake. This is what makes the fake trustworthy as a test double: it must
// pass the same battery a live Plane CE v1.3.0 instance passes.
func TestFakePlaneConformance(t *testing.T) {
	_, target := newFakeTarget(t)
	RunConformance(t, target)
}

// newFakeTarget starts a fake server with one project and returns it together
// with a ConformanceTarget pointing at that project.
func newFakeTarget(t *testing.T) (*Server, ConformanceTarget) {
	t.Helper()
	srv := NewServer(ServerConfig{
		APIKey:    "plane_api_fake_key",
		Workspace: "acme",
	})
	t.Cleanup(srv.Close)
	projectID := srv.AddProject("Gas City", "GC")
	return srv, ConformanceTarget{
		BaseURL:   srv.URL(),
		APIKey:    "plane_api_fake_key",
		Workspace: "acme",
		ProjectID: projectID,
		Live:      false,
	}
}

// TestFakeDraftWorkItemsHidden pins the fake's draft semantics, mirroring
// v1.3.0's Issue.issue_objects manager: drafts are invisible to the list,
// detail, and workspace-identifier endpoints, but the external-id
// short-circuit (which uses the unfiltered Issue.objects manager) still finds
// them. Fake-only: the adapter never creates drafts, and is_draft writability
// on a live create is not part of its dependency surface.
func TestFakeDraftWorkItemsHidden(t *testing.T) {
	_, target := newFakeTarget(t)
	client := plane.NewClient(target.BaseURL, target.APIKey, target.Workspace, target.ProjectID)

	status, body := rawRequest(t, target, http.MethodPost, rawProjectPath(target, "work-items/"), map[string]any{
		"name":            "draft item",
		"is_draft":        true,
		"external_id":     "draft-1",
		"external_source": "beads",
	})
	if status != http.StatusCreated {
		t.Fatalf("draft create status = %d (%s), want 201", status, body)
	}
	var created struct {
		ID         string `json:"id"`
		IsDraft    bool   `json:"is_draft"`
		SequenceID int    `json:"sequence_id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decoding draft: %v (%s)", err, body)
	}
	if !created.IsDraft {
		t.Error("create response lost is_draft")
	}

	// Detail GET is a 404: issue_objects excludes drafts.
	got, err := client.GetIssue(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetIssue(draft): %v", err)
	}
	if got != nil {
		t.Errorf("GetIssue(draft) = %+v, want nil (drafts hidden from detail)", got)
	}

	// The list excludes drafts.
	issues, err := client.ListIssues(t.Context(), plane.ListIssuesOptions{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	for _, is := range issues {
		if is.ID == created.ID {
			t.Errorf("draft %s visible in list", created.ID)
		}
	}

	// The workspace identifier lookup excludes drafts.
	miss, err := client.GetIssueByIdentifier(t.Context(), fmt.Sprintf("GC-%d", created.SequenceID))
	if err != nil {
		t.Fatalf("GetIssueByIdentifier(draft): %v", err)
	}
	if miss != nil {
		t.Errorf("GetIssueByIdentifier(draft) = %+v, want nil", miss)
	}

	// The external-id short-circuit still finds drafts (Issue.objects).
	found, err := client.GetIssueByExternalID(t.Context(), "draft-1", "beads")
	if err != nil {
		t.Fatalf("GetIssueByExternalID(draft): %v", err)
	}
	if found == nil || found.ID != created.ID {
		t.Errorf("GetIssueByExternalID(draft) = %+v, want id %s", found, created.ID)
	}
}

// TestFakeCreateProject pins the fake's project-creation plumbing, which
// tests use to provision projects over the wire. Fake-only: the production
// client has no project-creation surface and live conformance must not
// pollute workspaces with new projects.
func TestFakeCreateProject(t *testing.T) {
	_, target := newFakeTarget(t)

	status, body := rawRequest(t, target, http.MethodPost, "/workspaces/acme/projects/", map[string]any{
		"name":       "Beta",
		"identifier": "beta",
	})
	if status != http.StatusCreated {
		t.Fatalf("project create status = %d (%s), want 201", status, body)
	}
	var created struct {
		ID         string `json:"id"`
		Identifier string `json:"identifier"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decoding project: %v (%s)", err, body)
	}
	if created.ID == "" {
		t.Error("created project has no id")
	}
	if created.Identifier != "BETA" {
		t.Errorf("identifier = %q, want BETA (stored uppercased)", created.Identifier)
	}

	// New projects are seeded with the five default state groups.
	client := plane.NewClient(target.BaseURL, target.APIKey, target.Workspace, created.ID)
	states, err := client.ListStates(t.Context())
	if err != nil {
		t.Fatalf("ListStates(new project): %v", err)
	}
	groups := map[string]bool{}
	for _, s := range states {
		groups[s.Group] = true
	}
	for _, g := range []string{"backlog", "unstarted", "started", "completed", "cancelled"} {
		if !groups[g] {
			t.Errorf("new project missing state group %q", g)
		}
	}

	// Conflicts and validation failures.
	for _, tc := range []struct {
		name       string
		payload    map[string]any
		wantStatus int
		wantBody   string
	}{
		{"duplicate name", map[string]any{"name": "Beta", "identifier": "B2"},
			http.StatusConflict, `"name":"The project name is already taken"`},
		{"duplicate identifier", map[string]any{"name": "Beta Two", "identifier": "beta"},
			http.StatusConflict, `"identifier":"The project identifier is already taken"`},
		{"missing identifier", map[string]any{"name": "Gamma"},
			http.StatusBadRequest, `"identifier":["This field is required."]`},
		{"identifier too long", map[string]any{"name": "Gamma", "identifier": "ABCDEFGHIJKLM"},
			http.StatusBadRequest, `"identifier":["Ensure this field has no more than 12 characters."]`},
		{"missing name", map[string]any{"identifier": "G2"},
			http.StatusBadRequest, `"name":["This field is required."]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := rawRequest(t, target, http.MethodPost, "/workspaces/acme/projects/", tc.payload)
			if status != tc.wantStatus {
				t.Fatalf("status = %d (%s), want %d", status, body, tc.wantStatus)
			}
			if got := string(body); !strings.Contains(got, tc.wantBody) {
				t.Errorf("body = %s, want fragment %s", got, tc.wantBody)
			}
		})
	}
}

// TestFakeAssigneesDeduped pins the fake's documented assignee handling:
// values are accepted as given but deduplicated. Fake-only divergence — real
// v1.3.0 silently filters assignees to project members, which the fake cannot
// model without a member registry.
func TestFakeAssigneesDeduped(t *testing.T) {
	_, target := newFakeTarget(t)
	a := "11111111-1111-4111-8111-111111111111"
	b := "22222222-2222-4222-8222-222222222222"

	status, body := rawRequest(t, target, http.MethodPost, rawProjectPath(target, "work-items/"), map[string]any{
		"name":      "assigned",
		"assignees": []string{a, a, b},
	})
	if status != http.StatusCreated {
		t.Fatalf("create status = %d (%s), want 201", status, body)
	}
	var created struct {
		Assignees []string `json:"assignees"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decoding issue: %v (%s)", err, body)
	}
	if len(created.Assignees) != 2 || created.Assignees[0] != a || created.Assignees[1] != b {
		t.Errorf("assignees = %v, want [%s %s]", created.Assignees, a, b)
	}
}

// TestFakePerPageValidation pins the fake's per_page edge handling. The
// per_page<1 case is a documented fake divergence (real v1.3.0 would divide
// by zero into a 500), so it stays out of the shared suite.
func TestFakePerPageValidation(t *testing.T) {
	_, target := newFakeTarget(t)
	for _, tc := range []struct {
		name, query, wantDetail string
	}{
		{"zero", "per_page=0", "Invalid per_page parameter."},
		{"negative", "per_page=-1", "Invalid per_page parameter."},
		{"non-integer", "per_page=abc", "Invalid per_page parameter."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := rawRequest(t, target, http.MethodGet, rawProjectPath(target, "work-items/")+"?"+tc.query, nil)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d (%s), want 400", status, body)
			}
			var detail struct {
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal(body, &detail); err != nil {
				t.Fatalf("decoding body: %v (%s)", err, body)
			}
			if detail.Detail != tc.wantDetail {
				t.Errorf("detail = %q, want %q", detail.Detail, tc.wantDetail)
			}
		})
	}
}

// TestFakeCursorOffsetExtremes pins that extreme cursor offsets cannot panic
// the fake. Real v1.3.0 parses any integer offset (Python ints are
// unbounded): negative offsets are rejected by get_result as 400 "Error in
// parsing", and huge positive offsets page past the end into an empty
// results page. Offsets beyond Go's int range clamp (documented fake
// simplification).
func TestFakeCursorOffsetExtremes(t *testing.T) {
	_, target := newFakeTarget(t)
	client := plane.NewClient(target.BaseURL, target.APIKey, target.Workspace, target.ProjectID)
	if _, err := client.CreateIssue(t.Context(), &plane.IssuePayload{Name: "cursor probe"}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	listPath := rawProjectPath(target, "work-items/")

	// Huge in-range offsets must not overflow into a slice-bounds panic;
	// they serve an empty final page like a huge Postgres OFFSET would.
	for _, cursor := range []string{
		"2:4611686018427387904:0",  // page * per_page overflows int64
		"2:1099511627776:0",        // huge but multiplication stays in range
		"2:99999999999999999999:0", // beyond int64: clamps
	} {
		status, body := rawRequest(t, target, http.MethodGet, listPath+"?per_page=2&cursor="+cursor, nil)
		if status != http.StatusOK {
			t.Fatalf("cursor %q status = %d (%s), want 200 empty page", cursor, status, body)
		}
		var page issuePage
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatalf("decoding page for cursor %q: %v (%s)", cursor, err, body)
		}
		if len(page.Results) != 0 || page.NextPageResults {
			t.Errorf("cursor %q = %d results, next_page_results=%v; want empty final page",
				cursor, len(page.Results), page.NextPageResults)
		}
	}

	// Negative offsets (including beyond-int64 ones) are well-formed cursors
	// rejected with the BadPaginationError translation.
	for _, cursor := range []string{"2:-1:1", "2:-99999999999999999999:1"} {
		status, body := rawRequest(t, target, http.MethodGet, listPath+"?per_page=2&cursor="+cursor, nil)
		if status != http.StatusBadRequest {
			t.Fatalf("cursor %q status = %d (%s), want 400", cursor, status, body)
		}
		var detail struct {
			Detail string `json:"detail"`
		}
		if err := json.Unmarshal(body, &detail); err != nil {
			t.Fatalf("decoding body for cursor %q: %v (%s)", cursor, err, body)
		}
		if detail.Detail != "Error in parsing" {
			t.Errorf("cursor %q detail = %q, want %q", cursor, detail.Detail, "Error in parsing")
		}
	}
}

// TestFakeStateDefaultClearing pins the fake's default-state plumbing:
// creating or patching a state with default=true clears the previous
// default. Fake-only: live v1.3.0 default-flag semantics on the API surface
// are unverified and the adapter never sets defaults.
func TestFakeStateDefaultClearing(t *testing.T) {
	_, target := newFakeTarget(t)
	client := plane.NewClient(target.BaseURL, target.APIKey, target.Workspace, target.ProjectID)

	status, body := rawRequest(t, target, http.MethodPost, rawProjectPath(target, "states/"), map[string]any{
		"name": "New Default", "color": "#000000", "default": true,
	})
	if status != http.StatusOK {
		t.Fatalf("state create status = %d (%s), want 200", status, body)
	}
	var created struct {
		ID      string `json:"id"`
		Default bool   `json:"default"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decoding state: %v (%s)", err, body)
	}
	if !created.Default {
		t.Error("created state is not default")
	}

	states, err := client.ListStates(t.Context())
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	defaults := 0
	for _, s := range states {
		if s.Default {
			defaults++
			if s.ID != created.ID {
				t.Errorf("stale default state %s survived", s.ID)
			}
		}
	}
	if defaults != 1 {
		t.Errorf("project has %d default states, want exactly 1", defaults)
	}
}

// TestFakeLabelExternalIDConflict pins the fake's label external-id dedup
// plumbing (findLabelByExternal). Fake-only: the production client creates
// labels by name only, so the live contract for label external ids is not
// part of the adapter's dependency surface.
func TestFakeLabelExternalIDConflict(t *testing.T) {
	_, target := newFakeTarget(t)

	payload := map[string]any{
		"name":            "ext label",
		"external_id":     "label-ext-1",
		"external_source": "beads",
	}
	status, body := rawRequest(t, target, http.MethodPost, rawProjectPath(target, "labels/"), payload)
	if status != http.StatusCreated {
		t.Fatalf("label create status = %d (%s), want 201", status, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decoding label: %v (%s)", err, body)
	}

	payload["name"] = "ext label two"
	status, body = rawRequest(t, target, http.MethodPost, rawProjectPath(target, "labels/"), payload)
	if status != http.StatusConflict {
		t.Fatalf("duplicate external label status = %d (%s), want 409", status, body)
	}
	var conflict struct {
		Error string `json:"error"`
		ID    string `json:"id"`
	}
	if err := json.Unmarshal(body, &conflict); err != nil {
		t.Fatalf("decoding conflict: %v (%s)", err, body)
	}
	if conflict.Error != "Label with the same external id and external source already exists" {
		t.Errorf("conflict error = %q", conflict.Error)
	}
	if conflict.ID != created.ID {
		t.Errorf("conflict id = %q, want existing %q", conflict.ID, created.ID)
	}
}

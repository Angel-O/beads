package plane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testWorkspace = "acme"
	testProjectID = "11111111-2222-3333-4444-555555555555"
	testIssueID   = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	testAPIKey    = "plane_api_test_key"
)

// newTestClient returns a Client pointed at an httptest server running mux.
func newTestClient(t *testing.T, mux *http.ServeMux) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, testAPIKey, testWorkspace, testProjectID)
	return c, srv
}

// paginated wraps results in Plane's exact pagination envelope.
func paginated(results []any, nextCursor string, more bool) map[string]any {
	return map[string]any{
		"grouped_by":        nil,
		"sub_grouped_by":    nil,
		"total_count":       len(results),
		"next_cursor":       nextCursor,
		"prev_cursor":       "",
		"next_page_results": more,
		"prev_page_results": false,
		"count":             len(results),
		"total_pages":       1,
		"total_results":     len(results),
		"extra_stats":       nil,
		"results":           results,
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func sampleIssueJSON(id, name string) map[string]any {
	return map[string]any{
		"id":               id,
		"name":             name,
		"description_html": "<p>desc</p>",
		"priority":         "high",
		"state":            "99999999-0000-0000-0000-000000000000",
		"sequence_id":      7,
		"external_id":      "bd-42",
		"external_source":  "beads",
		"labels":           []string{},
		"assignees":        []string{},
		"parent":           nil,
		"project":          testProjectID,
		"created_at":       "2026-06-01T10:00:00.000000Z",
		"updated_at":       "2026-06-02T11:30:00.000000Z",
		"completed_at":     nil,
	}
}

func TestClientSendsAPIKeyHeader(t *testing.T) {
	var gotKey atomic.Value
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/"+testIssueID+"/",
		func(w http.ResponseWriter, r *http.Request) {
			gotKey.Store(r.Header.Get("X-Api-Key"))
			writeJSON(w, http.StatusOK, sampleIssueJSON(testIssueID, "x"))
		})
	c, _ := newTestClient(t, mux)

	if _, err := c.GetIssue(context.Background(), testIssueID); err != nil {
		t.Fatalf("GetIssue error: %v", err)
	}
	if gotKey.Load() != testAPIKey {
		t.Errorf("X-Api-Key = %v, want %q", gotKey.Load(), testAPIKey)
	}
}

func TestGetIssue(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/"+testIssueID+"/",
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("method = %s, want GET", r.Method)
			}
			writeJSON(w, http.StatusOK, sampleIssueJSON(testIssueID, "fix the thing"))
		})
	c, _ := newTestClient(t, mux)

	issue, err := c.GetIssue(context.Background(), testIssueID)
	if err != nil {
		t.Fatalf("GetIssue error: %v", err)
	}
	if issue == nil {
		t.Fatal("GetIssue returned nil issue")
	}
	if issue.ID != testIssueID || issue.Name != "fix the thing" {
		t.Errorf("issue = %+v", issue)
	}
	if issue.Priority != "high" {
		t.Errorf("priority = %q, want high", issue.Priority)
	}
	if issue.SequenceID != 7 {
		t.Errorf("sequence_id = %d, want 7", issue.SequenceID)
	}
	if issue.ExternalID != "bd-42" || issue.ExternalSource != "beads" {
		t.Errorf("external fields = %q/%q", issue.ExternalID, issue.ExternalSource)
	}
	wantUpdated := time.Date(2026, 6, 2, 11, 30, 0, 0, time.UTC)
	if !issue.UpdatedAt.Equal(wantUpdated) {
		t.Errorf("updated_at = %v, want %v", issue.UpdatedAt, wantUpdated)
	}
}

func TestGetIssueNotFoundReturnsNilNil(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "The requested resource does not exist."})
	})
	c, _ := newTestClient(t, mux)

	issue, err := c.GetIssue(context.Background(), testIssueID)
	if err != nil {
		t.Fatalf("GetIssue 404 should not error, got: %v", err)
	}
	if issue != nil {
		t.Errorf("GetIssue 404 = %+v, want nil", issue)
	}
}

func TestGetIssueServerErrorIncludesContext(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Something went wrong please try again later"})
	})
	c, _ := newTestClient(t, mux)
	c = c.WithMaxRetries(0)

	_, err := c.GetIssue(context.Background(), testIssueID)
	if err == nil {
		t.Fatal("expected error on 500")
	}
	for _, want := range []string{"500", "work-items"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestGetIssueByExternalID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/",
		func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if q.Get("external_id") != "bd-42" || q.Get("external_source") != "beads" {
				t.Errorf("query = %v", q)
			}
			// Both params present: Plane returns a SINGLE issue object, not an envelope.
			writeJSON(w, http.StatusOK, sampleIssueJSON(testIssueID, "found by external id"))
		})
	c, _ := newTestClient(t, mux)

	issue, err := c.GetIssueByExternalID(context.Background(), "bd-42", "beads")
	if err != nil {
		t.Fatalf("GetIssueByExternalID error: %v", err)
	}
	if issue == nil || issue.Name != "found by external id" {
		t.Errorf("issue = %+v", issue)
	}
}

func TestGetIssueByExternalIDNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "The requested resource does not exist."})
	})
	c, _ := newTestClient(t, mux)

	issue, err := c.GetIssueByExternalID(context.Background(), "bd-404", "beads")
	if err != nil {
		t.Fatalf("expected nil error on 404, got: %v", err)
	}
	if issue != nil {
		t.Errorf("issue = %+v, want nil", issue)
	}
}

func TestGetIssueByIdentifier(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/work-items/PROJ-7/",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, sampleIssueJSON(testIssueID, "by identifier"))
		})
	c, _ := newTestClient(t, mux)

	issue, err := c.GetIssueByIdentifier(context.Background(), "PROJ-7")
	if err != nil {
		t.Fatalf("GetIssueByIdentifier error: %v", err)
	}
	if issue == nil || issue.Name != "by identifier" {
		t.Errorf("issue = %+v", issue)
	}
}

func TestListIssuesPaginates(t *testing.T) {
	var pages atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/",
		func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Query().Get("cursor") {
			case "":
				pages.Add(1)
				writeJSON(w, http.StatusOK, paginated(
					[]any{sampleIssueJSON("00000000-0000-0000-0000-000000000001", "one")},
					"100:1:0", true))
			case "100:1:0":
				pages.Add(1)
				writeJSON(w, http.StatusOK, paginated(
					[]any{sampleIssueJSON("00000000-0000-0000-0000-000000000002", "two")},
					"100:2:0", false))
			default:
				t.Errorf("unexpected cursor %q", r.URL.Query().Get("cursor"))
				writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Invalid cursor parameter."})
			}
		})
	c, _ := newTestClient(t, mux)

	issues, err := c.ListIssues(context.Background(), ListIssuesOptions{})
	if err != nil {
		t.Fatalf("ListIssues error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(issues))
	}
	if issues[0].Name != "one" || issues[1].Name != "two" {
		t.Errorf("issues = %+v", issues)
	}
	if pages.Load() != 2 {
		t.Errorf("server saw %d pages, want 2", pages.Load())
	}
}

func TestListIssuesSendsUpdatedAtOrdering(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/",
		func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("order_by"); got != "-updated_at" {
				t.Errorf("order_by = %q, want -updated_at", got)
			}
			writeJSON(w, http.StatusOK, paginated(nil, "100:1:0", false))
		})
	c, _ := newTestClient(t, mux)

	if _, err := c.ListIssues(context.Background(), ListIssuesOptions{OrderBy: "-updated_at"}); err != nil {
		t.Fatalf("ListIssues error: %v", err)
	}
}

func TestCreateIssue(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/",
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decoding body: %v", err)
			}
			if body["name"] != "new issue" {
				t.Errorf("name = %v", body["name"])
			}
			if body["external_id"] != "bd-7" || body["external_source"] != "beads" {
				t.Errorf("external fields = %v/%v", body["external_id"], body["external_source"])
			}
			if body["priority"] != "urgent" {
				t.Errorf("priority = %v", body["priority"])
			}
			resp := sampleIssueJSON(testIssueID, "new issue")
			writeJSON(w, http.StatusCreated, resp)
		})
	c, _ := newTestClient(t, mux)

	created, err := c.CreateIssue(context.Background(), &IssuePayload{
		Name:           "new issue",
		Priority:       "urgent",
		ExternalID:     "bd-7",
		ExternalSource: "beads",
	})
	if err != nil {
		t.Fatalf("CreateIssue error: %v", err)
	}
	if created.ID != testIssueID {
		t.Errorf("created = %+v", created)
	}
}

func TestCreateIssueDuplicateReturnsTypedError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "Issue with the same external id and external source already exists",
			"id":    testIssueID,
		})
	})
	c, _ := newTestClient(t, mux)

	_, err := c.CreateIssue(context.Background(), &IssuePayload{
		Name: "dup", ExternalID: "bd-7", ExternalSource: "beads",
	})
	var dup *DuplicateError
	if !errorsAs(err, &dup) {
		t.Fatalf("error = %v (%T), want *DuplicateError", err, err)
	}
	if dup.ExistingID != testIssueID {
		t.Errorf("ExistingID = %q, want %q", dup.ExistingID, testIssueID)
	}
}

func TestUpdateIssue(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/"+testIssueID+"/",
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPatch {
				t.Errorf("method = %s, want PATCH", r.Method)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["name"] != "renamed" {
				t.Errorf("name = %v", body["name"])
			}
			if _, present := body["priority"]; present {
				t.Error("priority should be omitted from partial update")
			}
			writeJSON(w, http.StatusOK, sampleIssueJSON(testIssueID, "renamed"))
		})
	c, _ := newTestClient(t, mux)

	updated, err := c.UpdateIssue(context.Background(), testIssueID, &IssuePayload{Name: "renamed"})
	if err != nil {
		t.Fatalf("UpdateIssue error: %v", err)
	}
	if updated.Name != "renamed" {
		t.Errorf("updated = %+v", updated)
	}
}

func TestListStates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/states/",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, paginated([]any{
				map[string]any{"id": "s1", "name": "Backlog", "group": "backlog", "default": false},
				map[string]any{"id": "s2", "name": "Todo", "group": "unstarted", "default": true},
				map[string]any{"id": "s3", "name": "In Progress", "group": "started", "default": false},
				map[string]any{"id": "s4", "name": "Done", "group": "completed", "default": false},
			}, "100:1:0", false))
		})
	c, _ := newTestClient(t, mux)

	states, err := c.ListStates(context.Background())
	if err != nil {
		t.Fatalf("ListStates error: %v", err)
	}
	if len(states) != 4 {
		t.Fatalf("got %d states, want 4", len(states))
	}
	if states[1].Name != "Todo" || states[1].Group != "unstarted" || !states[1].Default {
		t.Errorf("states[1] = %+v", states[1])
	}
}

func TestListLabels(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/labels/",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, paginated([]any{
				map[string]any{"id": "l1", "name": "backend"},
				map[string]any{"id": "l2", "name": "urgent-fix"},
			}, "100:1:0", false))
		})
	c, _ := newTestClient(t, mux)

	labels, err := c.ListLabels(context.Background())
	if err != nil {
		t.Fatalf("ListLabels error: %v", err)
	}
	if len(labels) != 2 || labels[0].Name != "backend" {
		t.Errorf("labels = %+v", labels)
	}
}

func TestCreateLabel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/labels/",
		func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["name"] != "frontend" {
				t.Errorf("name = %v", body["name"])
			}
			writeJSON(w, http.StatusCreated, map[string]any{"id": "l9", "name": "frontend"})
		})
	c, _ := newTestClient(t, mux)

	label, err := c.CreateLabel(context.Background(), "frontend")
	if err != nil {
		t.Fatalf("CreateLabel error: %v", err)
	}
	if label.ID != "l9" {
		t.Errorf("label = %+v", label)
	}
}

func TestCreateLabelDuplicateReturnsTypedError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "Label with the same name already exists in the project",
			"id":    "l1",
		})
	})
	c, _ := newTestClient(t, mux)

	_, err := c.CreateLabel(context.Background(), "backend")
	var dup *DuplicateError
	if !errorsAs(err, &dup) {
		t.Fatalf("error = %v (%T), want *DuplicateError", err, err)
	}
	if dup.ExistingID != "l1" {
		t.Errorf("ExistingID = %q, want l1", dup.ExistingID)
	}
}

func TestListProjects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/projects/",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, paginated([]any{
				map[string]any{"id": testProjectID, "name": "Gas City", "identifier": "GC"},
			}, "100:1:0", false))
		})
	c, _ := newTestClient(t, mux)

	projects, err := c.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects error: %v", err)
	}
	if len(projects) != 1 || projects[0].Identifier != "GC" {
		t.Errorf("projects = %+v", projects)
	}
}

func TestGetProject(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{
				"id": testProjectID, "name": "Gas City", "identifier": "GC",
			})
		})
	c, _ := newTestClient(t, mux)

	p, err := c.GetProject(context.Background())
	if err != nil {
		t.Fatalf("GetProject error: %v", err)
	}
	if p.Identifier != "GC" {
		t.Errorf("project = %+v", p)
	}
}

func TestRateLimit429RetriesAfterWait(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"error_code": 5900, "error_message": "RATE_LIMIT_EXCEEDED",
			})
			return
		}
		writeJSON(w, http.StatusOK, sampleIssueJSON(testIssueID, "after retry"))
	})
	c, _ := newTestClient(t, mux)

	start := time.Now()
	issue, err := c.GetIssue(context.Background(), testIssueID)
	if err != nil {
		t.Fatalf("GetIssue error after retry: %v", err)
	}
	if issue == nil || issue.Name != "after retry" {
		t.Errorf("issue = %+v", issue)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Errorf("retried after %v, want >= ~1s honoring Retry-After", elapsed)
	}
}

func TestRateLimitGivesUpAfterMaxRetries(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error_code": 5900, "error_message": "RATE_LIMIT_EXCEEDED",
		})
	})
	c, _ := newTestClient(t, mux)
	c = c.WithMaxRetries(2)

	_, err := c.GetIssue(context.Background(), testIssueID)
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error %q should mention 429", err)
	}
	if calls.Load() != 3 { // initial + 2 retries
		t.Errorf("calls = %d, want 3", calls.Load())
	}
}

func TestRateLimitRespectsContextCancellation(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error_code": 5900, "error_message": "RATE_LIMIT_EXCEEDED",
		})
	})
	c, _ := newTestClient(t, mux)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := c.GetIssue(ctx, testIssueID)
	if err == nil {
		t.Fatal("expected context error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("waited %v despite cancelled context", elapsed)
	}
}

func TestAuthFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   map[string]string
	}{
		{"missing credentials 401", http.StatusUnauthorized, map[string]string{"detail": "Authentication credentials were not provided."}},
		{"invalid token 403", http.StatusForbidden, map[string]string{"detail": "Given API token is not valid"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, tt.status, tt.body)
			})
			c, _ := newTestClient(t, mux)

			_, err := c.GetIssue(context.Background(), testIssueID)
			if err == nil {
				t.Fatal("expected auth error")
			}
			var authErr *AuthError
			if !errorsAs(err, &authErr) {
				t.Fatalf("error = %v (%T), want *AuthError", err, err)
			}
			if !strings.Contains(err.Error(), tt.body["detail"]) {
				t.Errorf("error %q should include server detail %q", err, tt.body["detail"])
			}
		})
	}
}

func TestNewClientNormalizesBaseURL(t *testing.T) {
	c := NewClient("https://plane.example.com/", testAPIKey, testWorkspace, testProjectID)
	if got := c.BaseURL(); got != "https://plane.example.com" {
		t.Errorf("BaseURL = %q, want trailing slash trimmed", got)
	}
}

func TestPerPageClamped(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/",
		func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("per_page"); got != "1000" {
				t.Errorf("per_page = %q, want clamped to 1000", got)
			}
			writeJSON(w, http.StatusOK, paginated(nil, "100:1:0", false))
		})
	c, _ := newTestClient(t, mux)
	c = c.WithPerPage(5000)

	if _, err := c.ListIssues(context.Background(), ListIssuesOptions{}); err != nil {
		t.Fatalf("ListIssues error: %v", err)
	}
}

// errorsAs is a tiny alias so the table tests read cleanly.
func errorsAs(err error, target any) bool {
	return errors.As(err, target)
}

func TestListComments(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/"+testIssueID+"/comments/",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, paginated([]any{
				map[string]any{"id": "c1", "comment_html": "<p>first</p>", "created_at": "2026-06-01T10:00:00.000000Z"},
			}, "100:1:0", false))
		})
	c, _ := newTestClient(t, mux)

	comments, err := c.ListComments(context.Background(), testIssueID)
	if err != nil {
		t.Fatalf("ListComments error: %v", err)
	}
	if len(comments) != 1 || comments[0].CommentHTML != "<p>first</p>" {
		t.Errorf("comments = %+v", comments)
	}
}

func TestCreateComment(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/"+testIssueID+"/comments/",
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", r.Method)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["comment_html"] != "<p>progress: 3/5 closed</p>" {
				t.Errorf("comment_html = %v", body["comment_html"])
			}
			writeJSON(w, http.StatusCreated, map[string]any{
				"id": "c9", "comment_html": body["comment_html"], "created_at": "2026-06-12T00:00:00.000000Z",
			})
		})
	c, _ := newTestClient(t, mux)

	comment, err := c.CreateComment(context.Background(), testIssueID, "<p>progress: 3/5 closed</p>")
	if err != nil {
		t.Fatalf("CreateComment error: %v", err)
	}
	if comment.ID != "c9" {
		t.Errorf("comment = %+v", comment)
	}
}

func TestRateLimitExhaustionReturnsRateLimitedError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error_code": 5900, "error_message": "RATE_LIMIT_EXCEEDED",
		})
	})
	c, _ := newTestClient(t, mux)
	c = c.WithMaxRetries(0)

	_, err := c.GetIssue(context.Background(), testIssueID)
	if err == nil {
		t.Fatal("expected error after rate limit exhaustion")
	}
	var rl *RateLimitError
	if !errorsAs(err, &rl) {
		t.Fatalf("error = %v (%T), want *RateLimitError", err, err)
	}
	if got := rl.RateLimitRetryAfter(); got != 7*time.Second {
		t.Errorf("RateLimitRetryAfter = %v, want 7s", got)
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error %q should mention 429", err)
	}
}

func TestRetryDelayCapsAndClamps(t *testing.T) {
	resp := func(retryAfter string) *http.Response {
		h := http.Header{}
		if retryAfter != "" {
			h.Set("Retry-After", retryAfter)
		}
		return &http.Response{Header: h}
	}
	tests := []struct {
		name    string
		resp    *http.Response
		attempt int
		want    time.Duration
	}{
		{"plain exponential", resp(""), 2, 4 * time.Second},
		{"exponential capped at maxBackoff", resp(""), 10, maxBackoff},
		{"shift overflow clamped", resp(""), 200, maxBackoff},
		{"retry-after honored", resp("3"), 0, 3 * time.Second},
		{"huge retry-after capped", resp("9999"), 0, retryAfterCap},
		{"http-date retry-after capped", resp(time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)), 0, retryAfterCap},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := retryDelay(tt.resp, tt.attempt)
			if tt.name == "http-date retry-after capped" {
				// time.Until introduces sub-second jitter; the cap is the point.
				if got > retryAfterCap || got < retryAfterCap-5*time.Second {
					t.Errorf("retryDelay = %v, want ~%v (capped)", got, retryAfterCap)
				}
				return
			}
			if got != tt.want {
				t.Errorf("retryDelay = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestServerErrorRetriesGET(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "boom"})
			return
		}
		writeJSON(w, http.StatusOK, sampleIssueJSON(testIssueID, "after retry"))
	})
	c, _ := newTestClient(t, mux)

	issue, err := c.GetIssue(context.Background(), testIssueID)
	if err != nil {
		t.Fatalf("GetIssue error after 5xx retry: %v", err)
	}
	if issue == nil || issue.Name != "after retry" {
		t.Errorf("issue = %+v", issue)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2 (one retry)", calls.Load())
	}
}

func TestPostServerErrorNotRetried(t *testing.T) {
	// POSTs are not retried on 5xx: the server may have processed the
	// request before failing, and a blind retry can duplicate the entity
	// (comments have no dedup key). Work item creation recovers through
	// the external_id 409 on the next sync instead.
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "boom"})
	})
	c, _ := newTestClient(t, mux)

	_, err := c.CreateComment(context.Background(), testIssueID, "<p>x</p>")
	if err == nil {
		t.Fatal("expected error from POST 5xx")
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (no retry of non-idempotent POST)", calls.Load())
	}
}

func TestPostRateLimit429IsRetried(t *testing.T) {
	// 429 means the request was rejected before processing, so retrying a
	// POST is safe.
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"error_message": "RATE_LIMIT_EXCEEDED"})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"id": "c9", "comment_html": "<p>x</p>", "created_at": "2026-06-12T00:00:00.000000Z",
		})
	})
	c, _ := newTestClient(t, mux)

	comment, err := c.CreateComment(context.Background(), testIssueID, "<p>x</p>")
	if err != nil {
		t.Fatalf("CreateComment error after 429 retry: %v", err)
	}
	if comment.ID != "c9" {
		t.Errorf("comment = %+v", comment)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
}

func TestSuccessDecodeErrorSurfaces(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{not json"))
	})
	c, _ := newTestClient(t, mux)

	_, err := c.GetIssue(context.Background(), testIssueID)
	if err == nil {
		t.Fatal("expected decode error")
	}
	if !strings.Contains(err.Error(), "decoding response") {
		t.Errorf("error %q should mention decoding response", err)
	}
}

func TestNetworkErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	c := NewClient(srv.URL, testAPIKey, testWorkspace, testProjectID)
	srv.Close() // connection refused from here on

	_, err := c.GetIssue(context.Background(), testIssueID)
	if err == nil {
		t.Fatal("expected network error")
	}
	if !strings.Contains(err.Error(), "plane API GET") {
		t.Errorf("error %q should carry method/path context", err)
	}
}

func TestGetIssueByIdentifierNotFoundReturnsNilNil(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found."})
	})
	c, _ := newTestClient(t, mux)

	issue, err := c.GetIssueByIdentifier(context.Background(), "GC-404")
	if err != nil {
		t.Fatalf("GetIssueByIdentifier error: %v", err)
	}
	if issue != nil {
		t.Errorf("issue = %+v, want nil for 404", issue)
	}
}

func TestListPagesTerminatesOnLastPageWithNonEmptyCursor(t *testing.T) {
	// Real Plane (and the fake) emit a non-empty next_cursor on the final
	// page; next_page_results=false is the only end-of-collection signal.
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/",
		func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			writeJSON(w, http.StatusOK, paginated(
				[]any{sampleIssueJSON("00000000-0000-0000-0000-000000000001", "only")},
				"100:1:0", false))
		})
	c, _ := newTestClient(t, mux)

	issues, err := c.ListIssues(context.Background(), ListIssuesOptions{})
	if err != nil {
		t.Fatalf("ListIssues error: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (must not follow cursor past the last page)", calls.Load())
	}
}

func TestListPagesMidPaginationErrorSurfaces(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/",
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("cursor") == "" {
				writeJSON(w, http.StatusOK, paginated(
					[]any{sampleIssueJSON("00000000-0000-0000-0000-000000000001", "one")},
					"100:1:0", true))
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "upstream died"})
		})
	c, _ := newTestClient(t, mux)
	c = c.WithMaxRetries(0)

	_, err := c.ListIssues(context.Background(), ListIssuesOptions{})
	if err == nil {
		t.Fatal("expected mid-pagination error to surface")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error %q should mention the 502", err)
	}
}

func TestListPagesRunawayPaginationAborts(t *testing.T) {
	// A server that never flips next_page_results=false must not hang the
	// client forever.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, paginated(
				[]any{sampleIssueJSON("00000000-0000-0000-0000-000000000001", "loop")},
				"100:1:0", true))
		})
	c, _ := newTestClient(t, mux)

	_, err := c.ListIssues(context.Background(), ListIssuesOptions{})
	if err == nil {
		t.Fatal("expected runaway pagination to abort with an error")
	}
	if !strings.Contains(err.Error(), "pages") {
		t.Errorf("error %q should mention the page ceiling", err)
	}
}

func TestPathComponentsAreEscaped(t *testing.T) {
	var gotPath atomic.Value
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(r.URL.EscapedPath())
		writeJSON(w, http.StatusOK, sampleIssueJSON(testIssueID, "x"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, testAPIKey, "a b", "p/q")
	if _, err := c.GetIssue(context.Background(), "i d"); err != nil {
		t.Fatalf("GetIssue error: %v", err)
	}
	want := "/api/v1/workspaces/a%20b/projects/p%2Fq/work-items/i%20d/"
	if gotPath.Load() != want {
		t.Errorf("escaped path = %v, want %q", gotPath.Load(), want)
	}

	c2 := NewClient(srv.URL, testAPIKey, "a b", testProjectID)
	if _, err := c2.GetIssueByIdentifier(context.Background(), "GC 7/x"); err != nil {
		t.Fatalf("GetIssueByIdentifier error: %v", err)
	}
	want2 := "/api/v1/workspaces/a%20b/work-items/GC%207%2Fx/"
	if gotPath.Load() != want2 {
		t.Errorf("escaped path = %v, want %q", gotPath.Load(), want2)
	}
}

func TestWithBuildersDoNotMutateReceiver(t *testing.T) {
	c1 := NewClient("https://plane.example.com", testAPIKey, testWorkspace, testProjectID)
	c2 := c1.WithPerPage(7)
	c3 := c1.WithMaxRetries(9)
	if c1.perPage != defaultPerPage {
		t.Errorf("WithPerPage mutated the receiver: perPage = %d", c1.perPage)
	}
	if c1.maxRetries != defaultMaxRetries {
		t.Errorf("WithMaxRetries mutated the receiver: maxRetries = %d", c1.maxRetries)
	}
	if c2.perPage != 7 || c3.maxRetries != 9 {
		t.Errorf("builders did not apply: perPage = %d, maxRetries = %d", c2.perPage, c3.maxRetries)
	}
}

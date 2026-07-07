//go:build cgo && integration

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/plane"
	"github.com/steveyegge/beads/internal/plane/planetest"
	"github.com/steveyegge/beads/internal/storage/dolt"
	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

const planeTestAPIKey = "plane_api_roundtrip_key"

func planeTestStore(t *testing.T, prefix string) *dolt.DoltStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), ".beads", "dolt")
	return newTestStoreIsolatedDB(t, dbPath, prefix)
}

// planeTestSetup starts a fake Plane server with one project and returns the
// server, project ID, and a configured store.
func planeTestSetup(t *testing.T, prefix string) (*planetest.Server, string, *dolt.DoltStore) {
	t.Helper()
	srv := planetest.NewServer(planetest.ServerConfig{
		APIKey:    planeTestAPIKey,
		Workspace: "acme",
	})
	t.Cleanup(srv.Close)
	projectID := srv.AddProject("Round Trip", "RT")

	st := planeTestStore(t, prefix)
	ctx := context.Background()
	for k, v := range map[string]string{
		"plane.base_url":   srv.URL(),
		"plane.workspace":  "acme",
		"plane.project_id": projectID,
	} {
		if err := st.SetConfig(ctx, k, v); err != nil {
			t.Fatalf("SetConfig(%s): %v", k, err)
		}
	}
	t.Setenv("PLANE_API_KEY", planeTestAPIKey)
	return srv, projectID, st
}

func newPlaneEngine(t *testing.T, st *dolt.DoltStore) (*tracker.Engine, *plane.Tracker) {
	t.Helper()
	pt := &plane.Tracker{}
	if err := pt.Init(context.Background(), st); err != nil {
		t.Fatalf("tracker Init: %v", err)
	}
	engine := tracker.NewEngine(pt, st, "roundtrip-test")
	engine.OnMessage = func(string) {}
	engine.OnWarning = func(msg string) { t.Logf("engine warning: %s", msg) }
	engine.PullHooks = plane.NewPullHooks() // production wiring (assignee preservation)
	return engine, pt
}

// TestPlaneRoundTripCoreFields seeds beads issues, pushes them to the fake
// Plane, then pulls into a second store and verifies field fidelity.
func TestPlaneRoundTripCoreFields(t *testing.T) {
	ctx := context.Background()
	srv, projectID, sourceStore := planeTestSetup(t, "bd")

	seeds := []struct {
		title     string
		desc      string
		priority  int
		status    types.Status
		issueType types.IssueType
	}{
		{"Critical security fix", "Fix the auth bypass vulnerability", 0, types.StatusOpen, types.TypeBug},
		{"Add search feature", "Implement full-text search", 1, types.StatusInProgress, types.TypeFeature},
		{"Big migration", "Multi-quarter effort", 1, types.StatusOpen, types.TypeEpic},
		{"Routine dep update", "Bump everything", 3, types.StatusClosed, types.TypeTask},
	}
	var sourceIDs []string
	for _, s := range seeds {
		issue := &types.Issue{
			Title:       s.title,
			Description: s.desc,
			Priority:    s.priority,
			Status:      s.status,
			IssueType:   s.issueType,
		}
		if err := sourceStore.CreateIssue(ctx, issue, "roundtrip-test"); err != nil {
			t.Fatalf("CreateIssue(%s): %v", s.title, err)
		}
		sourceIDs = append(sourceIDs, issue.ID)
	}

	// --- Push ---
	engine, _ := newPlaneEngine(t, sourceStore)
	result, err := engine.Sync(ctx, tracker.SyncOptions{
		Push: true, State: "all", ConflictResolution: tracker.ConflictTimestamp,
	})
	if err != nil {
		t.Fatalf("push sync: %v", err)
	}
	if result.Stats.Errors > 0 {
		t.Fatalf("push had %d errors: %+v", result.Stats.Errors, result)
	}

	// Verify on the fake via the API: each bead landed with external_id =
	// bead ID and the right field mapping.
	client := plane.NewClient(srv.URL(), planeTestAPIKey, "acme", projectID)
	for i, beadID := range sourceIDs {
		remote, err := client.GetIssueByExternalID(ctx, beadID, "beads")
		if err != nil {
			t.Fatalf("GetIssueByExternalID(%s): %v", beadID, err)
		}
		if remote == nil {
			t.Fatalf("bead %s not found on Plane after push", beadID)
		}
		if remote.Name != seeds[i].title {
			t.Errorf("bead %s name = %q, want %q", beadID, remote.Name, seeds[i].title)
		}
		// external_ref written back locally.
		local, err := sourceStore.GetIssue(ctx, beadID)
		if err != nil {
			t.Fatalf("GetIssue(%s): %v", beadID, err)
		}
		if local.ExternalRef == nil || !plane.IsPlaneExternalRef(*local.ExternalRef) {
			t.Errorf("bead %s external_ref = %v, want plane ref", beadID, local.ExternalRef)
		}
	}

	// --- Pull into a fresh store ---
	destStore := planeTestStore(t, "pl")
	for k, v := range map[string]string{
		"plane.base_url":   srv.URL(),
		"plane.workspace":  "acme",
		"plane.project_id": projectID,
	} {
		if err := destStore.SetConfig(ctx, k, v); err != nil {
			t.Fatalf("dest SetConfig(%s): %v", k, err)
		}
	}
	destEngine, _ := newPlaneEngine(t, destStore)
	pullResult, err := destEngine.Sync(ctx, tracker.SyncOptions{
		Pull: true, State: "all", ConflictResolution: tracker.ConflictTimestamp,
	})
	if err != nil {
		t.Fatalf("pull sync: %v", err)
	}
	if pullResult.Stats.Errors > 0 {
		t.Fatalf("pull had %d errors", pullResult.Stats.Errors)
	}

	pulled, err := destStore.SearchIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	byTitle := map[string]*types.Issue{}
	for _, p := range pulled {
		byTitle[p.Title] = p
	}
	for _, s := range seeds {
		got := byTitle[s.title]
		if got == nil {
			t.Errorf("seed %q not pulled", s.title)
			continue
		}
		if got.Priority != s.priority {
			t.Errorf("%q priority = %d, want %d", s.title, got.Priority, s.priority)
		}
		// closed seeds come back closed; open/in_progress survive.
		wantStatus := s.status
		if got.Status != wantStatus {
			t.Errorf("%q status = %q, want %q", s.title, got.Status, wantStatus)
		}
		// Type round-trips via the beads:type:* label.
		if got.IssueType != s.issueType {
			t.Errorf("%q type = %q, want %q", s.title, got.IssueType, s.issueType)
		}
		if got.Description == "" {
			t.Errorf("%q lost its description", s.title)
		}
	}
}

// TestPlaneRoundTripBlockedStatus verifies blocked survives the round trip
// via the beads:blocked label (Plane has no blocked state).
func TestPlaneRoundTripBlockedStatus(t *testing.T) {
	ctx := context.Background()
	srv, projectID, sourceStore := planeTestSetup(t, "bd")

	issue := &types.Issue{
		Title:       "Stuck on upstream",
		Description: "Waiting on vendor fix",
		Priority:    1,
		Status:      types.StatusBlocked,
		IssueType:   types.TypeBug,
	}
	if err := sourceStore.CreateIssue(ctx, issue, "roundtrip-test"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	engine, _ := newPlaneEngine(t, sourceStore)
	if _, err := engine.Sync(ctx, tracker.SyncOptions{Push: true, State: "all"}); err != nil {
		t.Fatalf("push: %v", err)
	}

	// On the Plane side: started-group state plus the beads:blocked label.
	client := plane.NewClient(srv.URL(), planeTestAPIKey, "acme", projectID)
	remote, err := client.GetIssueByExternalID(ctx, issue.ID, "beads")
	if err != nil || remote == nil {
		t.Fatalf("remote lookup: %v / %+v", err, remote)
	}
	states, err := client.ListStates(ctx)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	var group string
	for _, s := range states {
		if s.ID == remote.StateID {
			group = s.Group
		}
	}
	if group != "started" {
		t.Errorf("remote state group = %q, want started", group)
	}
	labels, err := client.ListLabels(ctx)
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}
	labelName := map[string]string{}
	for _, l := range labels {
		labelName[l.ID] = l.Name
	}
	hasBlocked := false
	for _, id := range remote.Labels {
		if labelName[id] == "beads:blocked" {
			hasBlocked = true
		}
	}
	if !hasBlocked {
		t.Error("remote issue missing beads:blocked label")
	}

	// Pull into a fresh store: blocked is restored.
	destStore := planeTestStore(t, "pl")
	for k, v := range map[string]string{
		"plane.base_url": srv.URL(), "plane.workspace": "acme", "plane.project_id": projectID,
	} {
		if err := destStore.SetConfig(ctx, k, v); err != nil {
			t.Fatalf("SetConfig: %v", err)
		}
	}
	destEngine, _ := newPlaneEngine(t, destStore)
	if _, err := destEngine.Sync(ctx, tracker.SyncOptions{Pull: true, State: "all"}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	pulled, err := destStore.SearchIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(pulled) != 1 {
		t.Fatalf("pulled %d issues, want 1", len(pulled))
	}
	if pulled[0].Status != types.StatusBlocked {
		t.Errorf("pulled status = %q, want blocked", pulled[0].Status)
	}
	for _, l := range pulled[0].Labels {
		if l == "beads:blocked" {
			t.Error("internal beads:blocked label leaked into pulled issue")
		}
	}
}

// TestPlaneRoundTripParentChild verifies Plane parent/sub-item hierarchy
// becomes a parent-child dependency on pull.
func TestPlaneRoundTripParentChild(t *testing.T) {
	ctx := context.Background()
	srv, projectID, _ := planeTestSetup(t, "bd")

	client := plane.NewClient(srv.URL(), planeTestAPIKey, "acme", projectID)
	parent, err := client.CreateIssue(ctx, &plane.IssuePayload{Name: "Epic umbrella"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	_, err = client.CreateIssue(ctx, &plane.IssuePayload{Name: "Child task", ParentID: parent.ID})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	destStore := planeTestStore(t, "pl")
	for k, v := range map[string]string{
		"plane.base_url": srv.URL(), "plane.workspace": "acme", "plane.project_id": projectID,
	} {
		if err := destStore.SetConfig(ctx, k, v); err != nil {
			t.Fatalf("SetConfig: %v", err)
		}
	}
	engine, _ := newPlaneEngine(t, destStore)
	if _, err := engine.Sync(ctx, tracker.SyncOptions{Pull: true, State: "all"}); err != nil {
		t.Fatalf("pull: %v", err)
	}

	pulled, err := destStore.SearchIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	var childIssue *types.Issue
	for _, p := range pulled {
		if p.Title == "Child task" {
			childIssue = p
		}
	}
	if childIssue == nil {
		t.Fatalf("child not pulled; got %+v", pulled)
	}
	deps, err := destStore.GetDependenciesWithMetadata(ctx, childIssue.ID)
	if err != nil {
		t.Fatalf("GetDependenciesWithMetadata: %v", err)
	}
	foundParent := false
	for _, d := range deps {
		if d.DependencyType == types.DepParentChild {
			foundParent = true
		}
	}
	if !foundParent {
		t.Errorf("child has no parent-child dependency; deps = %+v", deps)
	}
}

// TestPlaneRoundTripIdempotentPush verifies repeated pushes never duplicate:
// the second push updates in place, and a lost external_ref write-back is
// recovered through Plane's 409 with the existing UUID.
func TestPlaneRoundTripIdempotentPush(t *testing.T) {
	ctx := context.Background()
	srv, projectID, sourceStore := planeTestSetup(t, "bd")

	issue := &types.Issue{
		Title:       "Idempotency check",
		Description: "Created once, pushed many times",
		Priority:    2,
		Status:      types.StatusOpen,
		IssueType:   types.TypeTask,
	}
	if err := sourceStore.CreateIssue(ctx, issue, "roundtrip-test"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	engine, _ := newPlaneEngine(t, sourceStore)
	if _, err := engine.Sync(ctx, tracker.SyncOptions{Push: true, State: "all"}); err != nil {
		t.Fatalf("push 1: %v", err)
	}
	if _, err := engine.Sync(ctx, tracker.SyncOptions{Push: true, State: "all"}); err != nil {
		t.Fatalf("push 2: %v", err)
	}

	// Simulate an interrupted sync: external_ref write-back lost. The next
	// push attempts a create; Plane's 409 carries the existing UUID and the
	// adapter recovers instead of duplicating.
	if err := sourceStore.UpdateIssue(ctx, issue.ID, map[string]interface{}{"external_ref": ""}, "roundtrip-test"); err != nil {
		t.Fatalf("clearing external_ref: %v", err)
	}
	engine2, _ := newPlaneEngine(t, sourceStore)
	if _, err := engine2.Sync(ctx, tracker.SyncOptions{Push: true, State: "all"}); err != nil {
		t.Fatalf("push 3 (recovery): %v", err)
	}

	client := plane.NewClient(srv.URL(), planeTestAPIKey, "acme", projectID)
	all, err := client.ListIssues(ctx, plane.ListIssuesOptions{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	matching := 0
	for _, r := range all {
		if r.ExternalID == issue.ID {
			matching++
		}
	}
	if matching != 1 {
		t.Errorf("found %d Plane issues for bead %s, want exactly 1 (no duplicates)", matching, issue.ID)
	}

	// external_ref restored locally after recovery.
	local, err := sourceStore.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if local.ExternalRef == nil || !plane.IsPlaneExternalRef(*local.ExternalRef) {
		t.Errorf("external_ref not restored after 409 recovery: %v", local.ExternalRef)
	}
}

// TestPlaneRoundTripPreservesLocalAssignee pins the assignee contract:
// Plane work items carry assignee user UUIDs the adapter cannot map, so a
// pull update must leave the local beads assignee untouched rather than
// blanking it.
func TestPlaneRoundTripPreservesLocalAssignee(t *testing.T) {
	ctx := context.Background()
	srv, projectID, st := planeTestSetup(t, "bd")
	_ = projectID

	issue := &types.Issue{
		Title:       "Assigned work",
		Description: "Keep my assignee",
		Priority:    2,
		Status:      types.StatusOpen,
		IssueType:   types.TypeTask,
		Assignee:    "julian",
	}
	if err := st.CreateIssue(ctx, issue, "roundtrip-test"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	engine, _ := newPlaneEngine(t, st)
	if _, err := engine.Sync(ctx, tracker.SyncOptions{
		Push: true, State: "all", ConflictResolution: tracker.ConflictTimestamp,
	}); err != nil {
		t.Fatalf("push sync: %v", err)
	}

	// Touch the work item remotely so the next pull sees it as updated.
	// last_sync is stored at second granularity; wait out the remainder of
	// the current second so the edit is unambiguously after it.
	time.Sleep(1200 * time.Millisecond)
	client := plane.NewClient(srv.URL(), planeTestAPIKey, "acme", projectID)
	remote, err := client.GetIssueByExternalID(ctx, issue.ID, plane.ExternalSource)
	if err != nil || remote == nil {
		t.Fatalf("GetIssueByExternalID: %v (issue %v)", err, remote)
	}
	if _, err := client.UpdateIssue(ctx, remote.ID, &plane.IssuePayload{Name: "Assigned work (edited remotely)"}); err != nil {
		t.Fatalf("remote edit: %v", err)
	}

	if _, err := engine.Sync(ctx, tracker.SyncOptions{
		Pull: true, State: "all", ConflictResolution: tracker.ConflictExternal,
	}); err != nil {
		t.Fatalf("pull sync: %v", err)
	}

	got, err := st.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.Title != "Assigned work (edited remotely)" {
		t.Errorf("title = %q, want the remote edit applied", got.Title)
	}
	if got.Assignee != "julian" {
		t.Errorf("assignee = %q, want %q preserved across pull", got.Assignee, "julian")
	}
}

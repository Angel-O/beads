package plane

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

func newTestMapper() *planeFieldMapper {
	return newFieldMapper(refContext{
		baseURL:   "https://plane.example.com",
		workspace: testWorkspace,
		projectID: testProjectID,
	})
}

func nativeIssue() *Issue {
	completed := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	return &Issue{
		ID:              testIssueID,
		Name:            "Fix the flux capacitor",
		DescriptionHTML: "<h2>Plan</h2><p>do the thing</p>",
		Priority:        "urgent",
		StateID:         "state-uuid-1",
		SequenceID:      42,
		ExternalID:      "bd-99",
		ExternalSource:  ExternalSource,
		ProjectID:       testProjectID,
		CreatedAt:       time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:       time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
		CompletedAt:     &completed,
	}
}

// trackerIssueFor mimics what Tracker builds: native issue in Raw, enriched
// state object and label names.
func trackerIssueFor(native *Issue, state *State, labels []string) *tracker.TrackerIssue {
	ti := &tracker.TrackerIssue{
		ID:          native.ID,
		Identifier:  "GC-42",
		Title:       native.Name,
		Labels:      labels,
		State:       state,
		Raw:         native,
		CreatedAt:   native.CreatedAt,
		UpdatedAt:   native.UpdatedAt,
		CompletedAt: native.CompletedAt,
	}
	return ti
}

func TestMapperStatusToBeads(t *testing.T) {
	m := newTestMapper()
	tests := []struct {
		name  string
		state interface{}
		want  types.Status
	}{
		{"state object started", &State{Group: GroupStarted}, types.StatusInProgress},
		{"state object completed", &State{Group: GroupCompleted}, types.StatusClosed},
		{"state value (not pointer)", State{Group: GroupCancelled}, types.StatusClosed},
		{"bare group string", "backlog", types.StatusOpen},
		{"nil falls back to open", nil, types.StatusOpen},
		{"unexpected type falls back to open", 42, types.StatusOpen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := m.StatusToBeads(tt.state); got != tt.want {
				t.Errorf("StatusToBeads(%v) = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

func TestMapperStatusToTracker(t *testing.T) {
	m := newTestMapper()
	if got := m.StatusToTracker(types.StatusInProgress); got != GroupStarted {
		t.Errorf("StatusToTracker(in_progress) = %v, want started", got)
	}
	if got := m.StatusToTracker(types.StatusClosed); got != GroupCompleted {
		t.Errorf("StatusToTracker(closed) = %v, want completed", got)
	}
}

func TestMapperPriorities(t *testing.T) {
	m := newTestMapper()
	if got := m.PriorityToBeads("urgent"); got != 0 {
		t.Errorf("PriorityToBeads(urgent) = %d, want 0", got)
	}
	if got := m.PriorityToBeads(123); got != 2 {
		t.Errorf("PriorityToBeads(non-string) = %d, want default 2", got)
	}
	if got := m.PriorityToTracker(3); got != "low" {
		t.Errorf("PriorityToTracker(3) = %v, want low", got)
	}
}

func TestMapperTypes(t *testing.T) {
	m := newTestMapper()
	// Plane CE has no work item types; everything imports as task by default.
	if got := m.TypeToBeads("anything"); got != types.TypeTask {
		t.Errorf("TypeToBeads = %q, want task", got)
	}
	// No API field to write; the type round-trips via the beads:type:* label.
	if got := m.TypeToTracker(types.TypeEpic); got != nil {
		t.Errorf("TypeToTracker = %v, want nil", got)
	}
}

func TestIssueToBeadsBasic(t *testing.T) {
	m := newTestMapper()
	native := nativeIssue()
	ti := trackerIssueFor(native, &State{ID: "state-uuid-1", Group: GroupStarted}, []string{"backend", "infra"})

	conv := m.IssueToBeads(ti)
	if conv == nil || conv.Issue == nil {
		t.Fatal("IssueToBeads returned nil conversion")
	}
	issue := conv.Issue

	if issue.Title != "Fix the flux capacitor" {
		t.Errorf("Title = %q", issue.Title)
	}
	// Description converted from HTML to markdown.
	if want := "## Plan"; !contains(issue.Description, want) {
		t.Errorf("Description = %q, missing %q", issue.Description, want)
	}
	if issue.Priority != 0 {
		t.Errorf("Priority = %d, want 0 (urgent)", issue.Priority)
	}
	if issue.Status != types.StatusInProgress {
		t.Errorf("Status = %q, want in_progress", issue.Status)
	}
	if issue.IssueType != types.TypeTask {
		t.Errorf("IssueType = %q, want task", issue.IssueType)
	}
	if len(issue.Labels) != 2 || issue.Labels[0] != "backend" {
		t.Errorf("Labels = %v", issue.Labels)
	}
	if issue.ExternalRef == nil {
		t.Fatal("ExternalRef is nil")
	}
	wantRef := "https://plane.example.com/acme/projects/" + testProjectID + "/issues/" + testIssueID
	if *issue.ExternalRef != wantRef {
		t.Errorf("ExternalRef = %q, want %q", *issue.ExternalRef, wantRef)
	}
}

func TestIssueToBeadsNilSafety(t *testing.T) {
	m := newTestMapper()
	if conv := m.IssueToBeads(nil); conv != nil {
		t.Error("IssueToBeads(nil) should be nil")
	}
	if conv := m.IssueToBeads(&tracker.TrackerIssue{Raw: nil}); conv != nil {
		t.Error("IssueToBeads with nil Raw should be nil")
	}
	if conv := m.IssueToBeads(&tracker.TrackerIssue{Raw: "wrong type"}); conv != nil {
		t.Error("IssueToBeads with non-Issue Raw should be nil")
	}
}

func TestIssueToBeadsRestoresBlockedFromLabel(t *testing.T) {
	m := newTestMapper()
	native := nativeIssue()
	ti := trackerIssueFor(native, &State{Group: GroupStarted}, []string{"backend", "beads:blocked"})

	conv := m.IssueToBeads(ti)
	if conv.Issue.Status != types.StatusBlocked {
		t.Errorf("Status = %q, want blocked (restored from beads:blocked label)", conv.Issue.Status)
	}
	// The internal label must not leak into beads labels.
	for _, l := range conv.Issue.Labels {
		if l == "beads:blocked" {
			t.Error("beads:blocked label leaked into beads labels")
		}
	}
}

func TestIssueToBeadsRestoresTypeFromLabel(t *testing.T) {
	m := newTestMapper()
	native := nativeIssue()
	ti := trackerIssueFor(native, &State{Group: GroupUnstarted}, []string{"beads:type:epic"})

	conv := m.IssueToBeads(ti)
	if conv.Issue.IssueType != types.TypeEpic {
		t.Errorf("IssueType = %q, want epic (restored from beads:type:epic label)", conv.Issue.IssueType)
	}
	if len(conv.Issue.Labels) != 0 {
		t.Errorf("Labels = %v, want internal labels filtered", conv.Issue.Labels)
	}
}

func TestIssueToBeadsIgnoresInvalidTypeLabel(t *testing.T) {
	m := newTestMapper()
	native := nativeIssue()
	ti := trackerIssueFor(native, &State{Group: GroupUnstarted}, []string{"beads:type:warlock"})

	conv := m.IssueToBeads(ti)
	if conv.Issue.IssueType != types.TypeTask {
		t.Errorf("IssueType = %q, want task (invalid type label ignored)", conv.Issue.IssueType)
	}
}

func TestIssueToBeadsParentDependency(t *testing.T) {
	m := newTestMapper()
	native := nativeIssue()
	native.ParentID = "bbbbbbbb-0000-0000-0000-000000000000"
	ti := trackerIssueFor(native, &State{Group: GroupStarted}, nil)

	conv := m.IssueToBeads(ti)
	if len(conv.Dependencies) != 1 {
		t.Fatalf("Dependencies = %+v, want 1 parent dep", conv.Dependencies)
	}
	dep := conv.Dependencies[0]
	if dep.FromExternalID != testIssueID || dep.ToExternalID != native.ParentID {
		t.Errorf("dep = %+v", dep)
	}
	if dep.Type != "parent-child" || dep.Source != tracker.DependencySourceParent {
		t.Errorf("dep type/source = %q/%q", dep.Type, dep.Source)
	}
}

func TestIssueToBeadsMetadata(t *testing.T) {
	m := newTestMapper()
	native := nativeIssue()
	ti := trackerIssueFor(native, &State{ID: "state-uuid-1", Name: "In Progress", Group: GroupStarted}, nil)

	conv := m.IssueToBeads(ti)
	if conv.Issue.Metadata == nil {
		t.Fatal("Metadata is nil")
	}
	var meta map[string]map[string]any
	if err := json.Unmarshal(conv.Issue.Metadata, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	pm := meta["plane"]
	if pm == nil {
		t.Fatalf("metadata = %v, missing plane key", meta)
	}
	if pm["project_id"] != testProjectID {
		t.Errorf("plane.project_id = %v", pm["project_id"])
	}
	if pm["state_id"] != "state-uuid-1" {
		t.Errorf("plane.state_id = %v", pm["state_id"])
	}
	if pm["sequence_id"] != float64(42) {
		t.Errorf("plane.sequence_id = %v", pm["sequence_id"])
	}
}

func TestIssueToTracker(t *testing.T) {
	m := newTestMapper()
	issue := &types.Issue{
		Title:       "Push me",
		Description: "# Heading\n\nbody",
		Priority:    1,
		Status:      types.StatusInProgress,
	}

	updates := m.IssueToTracker(issue)
	if updates["name"] != "Push me" {
		t.Errorf("name = %v", updates["name"])
	}
	html, _ := updates["description_html"].(string)
	if !contains(html, "<h1>Heading</h1>") {
		t.Errorf("description_html = %q", html)
	}
	if updates["priority"] != "high" {
		t.Errorf("priority = %v", updates["priority"])
	}
}

// pushLabelsFor computes the label set to push to Plane: the issue's own
// labels plus the internal beads:* round-trip labels.
func TestPushLabelsFor(t *testing.T) {
	tests := []struct {
		name  string
		issue *types.Issue
		want  []string
	}{
		{
			name:  "plain task keeps labels only",
			issue: &types.Issue{Labels: []string{"backend"}, IssueType: types.TypeTask, Status: types.StatusOpen},
			want:  []string{"backend"},
		},
		{
			name:  "blocked adds beads:blocked",
			issue: &types.Issue{Labels: []string{"x"}, IssueType: types.TypeTask, Status: types.StatusBlocked},
			want:  []string{"x", "beads:blocked"},
		},
		{
			name:  "epic adds type label",
			issue: &types.Issue{IssueType: types.TypeEpic, Status: types.StatusOpen},
			want:  []string{"beads:type:epic"},
		},
		{
			name:  "blocked epic adds both",
			issue: &types.Issue{IssueType: types.TypeEpic, Status: types.StatusBlocked},
			want:  []string{"beads:blocked", "beads:type:epic"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pushLabelsFor(tt.issue)
			if len(got) != len(tt.want) {
				t.Fatalf("pushLabelsFor = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("pushLabelsFor[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

func TestIssueToBeadsDescriptionConversionFallback(t *testing.T) {
	// Conversion failure must preserve content (as sanitized HTML), never
	// blank the local description on pull.
	depth := 600
	var b strings.Builder
	for i := 0; i < depth; i++ {
		b.WriteString("<blockquote>")
	}
	b.WriteString("deep content")
	for i := 0; i < depth; i++ {
		b.WriteString("</blockquote>")
	}

	m := newFieldMapper(refContext{baseURL: "https://p.example.com", workspace: "acme", projectID: "p1"})
	native := &Issue{ID: "u1", Name: "x", DescriptionHTML: b.String()}
	conv := m.IssueToBeads(&tracker.TrackerIssue{Raw: native})
	if conv == nil || conv.Issue == nil {
		t.Fatal("conversion returned nil")
	}
	if !strings.Contains(conv.Issue.Description, "deep content") {
		t.Errorf("description lost content on conversion failure; got %d bytes", len(conv.Issue.Description))
	}
}

func TestIssueToTrackerEmptyDescriptionSendsEmptyDocument(t *testing.T) {
	m := newFieldMapper(refContext{})
	fields := m.IssueToTracker(&types.Issue{Title: "x", Description: ""})
	if fields["description_html"] != emptyDescriptionHTML {
		t.Errorf("description_html = %v, want %q", fields["description_html"], emptyDescriptionHTML)
	}
}

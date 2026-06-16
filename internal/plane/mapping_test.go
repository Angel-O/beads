package plane

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestPriorityToBeads(t *testing.T) {
	tests := []struct {
		name  string
		plane string
		want  int
	}{
		{"urgent maps to critical", "urgent", 0},
		{"high maps to high", "high", 1},
		{"medium maps to medium", "medium", 2},
		{"low maps to low", "low", 3},
		{"none maps to backlog", "none", 4},
		{"empty maps to backlog", "", 4},
		{"unknown maps to default medium", "blocker", 2},
		{"case insensitive", "URGENT", 0},
		{"surrounding whitespace tolerated", " high ", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PriorityToBeads(tt.plane); got != tt.want {
				t.Errorf("PriorityToBeads(%q) = %d, want %d", tt.plane, got, tt.want)
			}
		})
	}
}

func TestPriorityToPlane(t *testing.T) {
	tests := []struct {
		name  string
		beads int
		want  string
	}{
		{"critical maps to urgent", 0, "urgent"},
		{"high maps to high", 1, "high"},
		{"medium maps to medium", 2, "medium"},
		{"low maps to low", 3, "low"},
		{"backlog maps to none", 4, "none"},
		{"out of range negative clamps to urgent", -1, "urgent"},
		{"out of range high clamps to none", 9, "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PriorityToPlane(tt.beads); got != tt.want {
				t.Errorf("PriorityToPlane(%d) = %q, want %q", tt.beads, got, tt.want)
			}
		})
	}
}

func TestPriorityRoundTrip(t *testing.T) {
	// Every beads priority must survive beads -> plane -> beads unchanged.
	for p := 0; p <= 4; p++ {
		if got := PriorityToBeads(PriorityToPlane(p)); got != p {
			t.Errorf("round trip for beads priority %d returned %d", p, got)
		}
	}
	// Every plane priority must survive plane -> beads -> plane unchanged.
	for _, pp := range []string{"urgent", "high", "medium", "low", "none"} {
		if got := PriorityToPlane(PriorityToBeads(pp)); got != pp {
			t.Errorf("round trip for plane priority %q returned %q", pp, got)
		}
	}
}

func TestStateGroupToBeadsStatus(t *testing.T) {
	tests := []struct {
		name  string
		group string
		want  types.Status
	}{
		{"backlog maps to open", "backlog", types.StatusOpen},
		{"unstarted maps to open", "unstarted", types.StatusOpen},
		{"started maps to in_progress", "started", types.StatusInProgress},
		{"completed maps to closed", "completed", types.StatusClosed},
		{"cancelled maps to closed", "cancelled", types.StatusClosed},
		{"unknown maps to open", "triage", types.StatusOpen},
		{"empty maps to open", "", types.StatusOpen},
		{"case insensitive", "STARTED", types.StatusInProgress},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StateGroupToBeadsStatus(tt.group); got != tt.want {
				t.Errorf("StateGroupToBeadsStatus(%q) = %q, want %q", tt.group, got, tt.want)
			}
		})
	}
}

func TestBeadsStatusToStateGroup(t *testing.T) {
	tests := []struct {
		name   string
		status types.Status
		want   string
	}{
		{"open maps to unstarted", types.StatusOpen, "unstarted"},
		{"in_progress maps to started", types.StatusInProgress, "started"},
		{"blocked maps to started", types.StatusBlocked, "started"},
		{"hooked maps to started", types.StatusHooked, "started"},
		{"deferred maps to backlog", types.StatusDeferred, "backlog"},
		{"pinned maps to unstarted", types.StatusPinned, "unstarted"},
		{"closed maps to completed", types.StatusClosed, "completed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BeadsStatusToStateGroup(tt.status); got != tt.want {
				t.Errorf("BeadsStatusToStateGroup(%q) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestBuildPlaneExternalRef(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		workspace string
		projectID string
		issueID   string
		want      string
	}{
		{
			name:      "standard ref",
			baseURL:   "https://plane.example.com",
			workspace: "acme",
			projectID: "11111111-2222-3333-4444-555555555555",
			issueID:   "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			want:      "https://plane.example.com/acme/projects/11111111-2222-3333-4444-555555555555/issues/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		},
		{
			name:      "trailing slash on base URL is trimmed",
			baseURL:   "https://plane.example.com/",
			workspace: "acme",
			projectID: "11111111-2222-3333-4444-555555555555",
			issueID:   "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			want:      "https://plane.example.com/acme/projects/11111111-2222-3333-4444-555555555555/issues/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		},
		{
			name:      "missing base URL falls back to compact scheme",
			baseURL:   "",
			workspace: "acme",
			projectID: "11111111-2222-3333-4444-555555555555",
			issueID:   "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			want:      "plane:acme/11111111-2222-3333-4444-555555555555/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildPlaneExternalRef(tt.baseURL, tt.workspace, tt.projectID, tt.issueID)
			if got != tt.want {
				t.Errorf("BuildPlaneExternalRef() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsPlaneExternalRef(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		want bool
	}{
		{
			name: "URL ref with UUID project and issue",
			ref:  "https://plane.example.com/acme/projects/11111111-2222-3333-4444-555555555555/issues/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			want: true,
		},
		{
			name: "compact scheme ref",
			ref:  "plane:acme/11111111-2222-3333-4444-555555555555/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			want: true,
		},
		{
			name: "gitlab issue URL is not plane",
			ref:  "https://gitlab.com/group/proj/-/issues/42",
			want: false,
		},
		{
			name: "github compact ref is not plane",
			ref:  "github:owner/repo#9",
			want: false,
		},
		{
			name: "linear URL is not plane",
			ref:  "https://linear.app/team/issue/TEAM-123",
			want: false,
		},
		{
			name: "plane-like URL without UUID segments is not plane",
			ref:  "https://example.com/acme/projects/42/issues/9",
			want: false,
		},
		{
			name: "empty ref",
			ref:  "",
			want: false,
		},
		{
			name: "ADO workitem URL is not plane",
			ref:  "https://dev.azure.com/org/proj/_workitems/edit/123",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPlaneExternalRef(tt.ref); got != tt.want {
				t.Errorf("IsPlaneExternalRef(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

func TestExtractPlaneIssueID(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		want string
	}{
		{
			name: "URL ref extracts trailing issue UUID",
			ref:  "https://plane.example.com/acme/projects/11111111-2222-3333-4444-555555555555/issues/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			want: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		},
		{
			name: "URL ref with trailing slash",
			ref:  "https://plane.example.com/acme/projects/11111111-2222-3333-4444-555555555555/issues/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/",
			want: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		},
		{
			name: "compact ref extracts issue UUID",
			ref:  "plane:acme/11111111-2222-3333-4444-555555555555/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			want: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		},
		{
			name: "non-plane ref yields empty",
			ref:  "https://gitlab.com/group/proj/-/issues/42",
			want: "",
		},
		{
			name: "empty ref yields empty",
			ref:  "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractPlaneIssueID(tt.ref); got != tt.want {
				t.Errorf("ExtractPlaneIssueID(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestExtractPlaneProjectID(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		want string
	}{
		{
			name: "URL ref extracts project UUID",
			ref:  "https://plane.example.com/acme/projects/11111111-2222-3333-4444-555555555555/issues/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			want: "11111111-2222-3333-4444-555555555555",
		},
		{
			name: "compact ref extracts project UUID",
			ref:  "plane:acme/11111111-2222-3333-4444-555555555555/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			want: "11111111-2222-3333-4444-555555555555",
		},
		{
			name: "non-plane ref yields empty",
			ref:  "github:owner/repo#9",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractPlaneProjectID(tt.ref); got != tt.want {
				t.Errorf("ExtractPlaneProjectID(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

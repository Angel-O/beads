package plane

import (
	"encoding/json"
	"strings"

	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

// Internal round-trip labels. Plane CE has no work item types and no
// blocked state, so the adapter encodes those beads concepts as labels on
// the Plane side and strips/restores them on pull (same pattern as the ADO
// adapter's beads:blocked tag).
const (
	beadsLabelPrefix = "beads:"
	blockedLabel     = "beads:blocked"
	typeLabelPrefix  = "beads:type:"
)

// emptyDescriptionHTML is Plane's canonical empty document. Pushing it (in
// place of an omitted description_html key) is what makes clearing a beads
// description actually clear the Plane description.
const emptyDescriptionHTML = "<p></p>"

// refContext carries the instance coordinates needed to build external refs.
type refContext struct {
	baseURL   string
	workspace string
	projectID string
}

// planeFieldMapper implements tracker.FieldMapper for Plane.
type planeFieldMapper struct {
	refs refContext
}

// Compile-time interface check.
var _ tracker.FieldMapper = (*planeFieldMapper)(nil)

// newFieldMapper creates a field mapper bound to the given instance
// coordinates (used for external_ref construction).
func newFieldMapper(refs refContext) *planeFieldMapper {
	return &planeFieldMapper{refs: refs}
}

// PriorityToBeads converts a Plane priority string to beads priority (0-4).
func (m *planeFieldMapper) PriorityToBeads(trackerPriority interface{}) int {
	p, ok := trackerPriority.(string)
	if !ok {
		return 2
	}
	return PriorityToBeads(p)
}

// PriorityToTracker converts a beads priority (0-4) to Plane's string enum.
func (m *planeFieldMapper) PriorityToTracker(beadsPriority int) interface{} {
	return PriorityToPlane(beadsPriority)
}

// StatusToBeads converts a Plane state (a *State/State carrying its group,
// or a bare group string) to a beads status.
func (m *planeFieldMapper) StatusToBeads(trackerState interface{}) types.Status {
	switch s := trackerState.(type) {
	case *State:
		if s == nil {
			return types.StatusOpen
		}
		return StateGroupToBeadsStatus(s.Group)
	case State:
		return StateGroupToBeadsStatus(s.Group)
	case string:
		return StateGroupToBeadsStatus(s)
	default:
		return types.StatusOpen
	}
}

// StatusToTracker converts a beads status to the Plane state group the
// issue should land in. The Tracker resolves the group to a concrete
// per-project state UUID.
func (m *planeFieldMapper) StatusToTracker(beadsStatus types.Status) interface{} {
	return BeadsStatusToStateGroup(beadsStatus)
}

// TypeToBeads returns task: Plane CE has no work item types. Beads types
// round-trip through the beads:type:* label instead (see IssueToBeads).
func (m *planeFieldMapper) TypeToBeads(trackerType interface{}) types.IssueType {
	return types.TypeTask
}

// TypeToTracker returns nil: Plane CE has no writable type field. The
// beads type is encoded as a beads:type:* label by pushLabelsFor.
func (m *planeFieldMapper) TypeToTracker(beadsType types.IssueType) interface{} {
	return nil
}

// IssueToBeads converts a Plane work item (native Issue in ti.Raw, state
// and label names enriched by the Tracker) into a beads issue plus parent
// dependency info. Returns nil when ti carries no native issue.
func (m *planeFieldMapper) IssueToBeads(ti *tracker.TrackerIssue) *tracker.IssueConversion {
	if ti == nil {
		return nil
	}
	native, ok := ti.Raw.(*Issue)
	if !ok || native == nil {
		return nil
	}

	desc := descriptionMarkdown(native.DescriptionHTML)

	status := m.StatusToBeads(ti.State)
	issueType := types.TypeTask
	var labels []string
	for _, l := range ti.Labels {
		switch {
		case l == blockedLabel:
			// Blocked round-trips through a label: Plane has no blocked
			// state, so push maps blocked -> started group + beads:blocked.
			if status == types.StatusInProgress {
				status = types.StatusBlocked
			}
		case strings.HasPrefix(l, typeLabelPrefix):
			t := types.IssueType(strings.TrimPrefix(l, typeLabelPrefix))
			if t.IsValid() {
				issueType = t
			}
		case strings.HasPrefix(l, beadsLabelPrefix):
			// Unknown internal label: strip, never import.
		default:
			labels = append(labels, l)
		}
	}

	issue := &types.Issue{
		Title:       native.Name,
		Description: desc,
		Priority:    m.PriorityToBeads(native.Priority),
		Status:      status,
		IssueType:   issueType,
		Assignee:    ti.Assignee,
		Labels:      labels,
	}

	projectID := native.ProjectID
	if projectID == "" {
		projectID = m.refs.projectID
	}
	ref := BuildPlaneExternalRef(m.refs.baseURL, m.refs.workspace, projectID, native.ID)
	issue.ExternalRef = &ref

	meta := map[string]interface{}{
		"plane": map[string]interface{}{
			"project_id":  projectID,
			"state_id":    native.StateID,
			"sequence_id": native.SequenceID,
		},
	}
	if raw, err := json.Marshal(meta); err == nil {
		issue.Metadata = json.RawMessage(raw)
	}

	conv := &tracker.IssueConversion{Issue: issue}
	if native.ParentID != "" {
		conv.Dependencies = append(conv.Dependencies, tracker.DependencyInfo{
			FromExternalID: native.ID,
			ToExternalID:   native.ParentID,
			Type:           "parent-child",
			Source:         tracker.DependencySourceParent,
		})
	}
	return conv
}

// IssueToTracker builds the Plane update fields for a beads issue, keyed by
// API field names. State and labels need per-project entity resolution and
// are handled by the Tracker, not here. When the description fails to
// convert, the description_html key is omitted (leaving the remote value
// unchanged) rather than overwriting it with an empty document.
func (m *planeFieldMapper) IssueToTracker(issue *types.Issue) map[string]interface{} {
	fields := map[string]interface{}{
		"name":     issue.Title,
		"priority": PriorityToPlane(issue.Priority),
	}
	html, err := MarkdownToHTML(issue.Description)
	if err == nil {
		if html == "" {
			html = emptyDescriptionHTML
		}
		fields["description_html"] = html
	}
	return fields
}

// pushLabelsFor computes the full label set to push to Plane for a beads
// issue: its own labels plus the internal beads:* round-trip labels for
// blocked status and non-task issue types.
func pushLabelsFor(issue *types.Issue) []string {
	labels := make([]string, 0, len(issue.Labels)+2)
	for _, l := range issue.Labels {
		if !strings.HasPrefix(l, beadsLabelPrefix) {
			labels = append(labels, l)
		}
	}
	if issue.Status == types.StatusBlocked {
		labels = append(labels, blockedLabel)
	}
	if issue.IssueType != "" && issue.IssueType != types.TypeTask {
		labels = append(labels, typeLabelPrefix+string(issue.IssueType))
	}
	return labels
}

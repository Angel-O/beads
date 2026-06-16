// Package plane implements a tracker adapter for Plane (https://github.com/makeplane/plane),
// an open-source project tracker. It targets the self-hostable Community Edition
// REST API (/api/v1/) and uses Plane's native external_id/external_source fields
// for idempotent, dedup-safe writes keyed by bead ID.
package plane

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/steveyegge/beads/internal/types"
)

// ExternalSource is the value written to Plane's external_source field on
// every work item the adapter creates, making beads-originated items
// identifiable and enabling Plane-side duplicate detection.
const ExternalSource = "beads"

// Plane priority values, ordered from most to least urgent. Plane represents
// priority as a lowercase string enum on the work item.
const (
	PriorityUrgent = "urgent"
	PriorityHigh   = "high"
	PriorityMedium = "medium"
	PriorityLow    = "low"
	PriorityNone   = "none"
)

// Plane state groups. Every per-project state belongs to exactly one group;
// groups are the stable vocabulary the adapter maps statuses through, since
// state names and IDs vary per project.
const (
	GroupBacklog   = "backlog"
	GroupUnstarted = "unstarted"
	GroupStarted   = "started"
	GroupCompleted = "completed"
	GroupCancelled = "cancelled" //nolint:misspell // Plane API wire value uses the British spelling
)

// PriorityToBeads converts a Plane priority string to a beads priority (0-4).
// Unknown values map to 2 (medium, the beads default); empty and "none" map
// to 4 (backlog) to keep the beads<->plane mapping a clean bijection.
func PriorityToBeads(planePriority string) int {
	switch strings.ToLower(strings.TrimSpace(planePriority)) {
	case PriorityUrgent:
		return 0
	case PriorityHigh:
		return 1
	case PriorityMedium:
		return 2
	case PriorityLow:
		return 3
	case PriorityNone, "":
		return 4
	default:
		return 2
	}
}

// PriorityToPlane converts a beads priority (0-4) to Plane's string enum.
// Out-of-range values clamp to the nearest valid priority.
func PriorityToPlane(beadsPriority int) string {
	switch {
	case beadsPriority <= 0:
		return PriorityUrgent
	case beadsPriority == 1:
		return PriorityHigh
	case beadsPriority == 2:
		return PriorityMedium
	case beadsPriority == 3:
		return PriorityLow
	default:
		return PriorityNone
	}
}

// StateGroupToBeadsStatus converts a Plane state group to a beads status.
// Unknown groups map to open so that pulled issues are never silently lost.
func StateGroupToBeadsStatus(group string) types.Status {
	switch strings.ToLower(strings.TrimSpace(group)) {
	case GroupStarted:
		return types.StatusInProgress
	case GroupCompleted, GroupCancelled:
		return types.StatusClosed
	default: // backlog, unstarted, unknown
		return types.StatusOpen
	}
}

// BeadsStatusToStateGroup converts a beads status to the Plane state group
// the issue should land in. Statuses with no Plane equivalent map to the
// nearest group: blocked/hooked are active work (started), deferred is
// parked (backlog), pinned stays visible (unstarted).
func BeadsStatusToStateGroup(status types.Status) string {
	switch status {
	case types.StatusInProgress, types.StatusBlocked, types.StatusHooked:
		return GroupStarted
	case types.StatusDeferred:
		return GroupBacklog
	case types.StatusClosed:
		return GroupCompleted
	default: // open, pinned
		return GroupUnstarted
	}
}

const uuidPattern = `[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`

// planeURLRefRe matches web URLs of the form
// <base>/<workspace>/projects/<project-uuid>/issues/<issue-uuid>[/].
// The UUID requirement on both path params distinguishes Plane URLs from
// other trackers' numeric issue URLs.
var planeURLRefRe = regexp.MustCompile(
	`^https?://[^/]+/([^/]+)/projects/(` + uuidPattern + `)/issues/(` + uuidPattern + `)/?$`)

// planeCompactRefRe matches the compact fallback scheme
// plane:<workspace>/<project-uuid>/<issue-uuid>, used when no base URL is
// configured.
var planeCompactRefRe = regexp.MustCompile(
	`^plane:([^/]+)/(` + uuidPattern + `)/(` + uuidPattern + `)$`)

// BuildPlaneExternalRef constructs the external_ref string for a Plane work
// item: the human-clickable web URL when a base URL is known, otherwise the
// compact plane:<workspace>/<project>/<issue> scheme.
func BuildPlaneExternalRef(baseURL, workspace, projectID, issueID string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return fmt.Sprintf("plane:%s/%s/%s", workspace, projectID, issueID)
	}
	return fmt.Sprintf("%s/%s/projects/%s/issues/%s", base, workspace, projectID, issueID)
}

// IsPlaneExternalRef reports whether ref belongs to the Plane tracker, in
// either the URL or compact form.
func IsPlaneExternalRef(ref string) bool {
	return planeURLRefRe.MatchString(ref) || planeCompactRefRe.MatchString(ref)
}

// ExtractPlaneIssueID returns the Plane work item UUID from an external_ref,
// or "" if the ref is not a Plane ref.
func ExtractPlaneIssueID(ref string) string {
	if m := planeURLRefRe.FindStringSubmatch(ref); m != nil {
		return m[3]
	}
	if m := planeCompactRefRe.FindStringSubmatch(ref); m != nil {
		return m[3]
	}
	return ""
}

// ExtractPlaneProjectID returns the Plane project UUID from an external_ref,
// or "" if the ref is not a Plane ref.
func ExtractPlaneProjectID(ref string) string {
	if m := planeURLRefRe.FindStringSubmatch(ref); m != nil {
		return m[2]
	}
	if m := planeCompactRefRe.FindStringSubmatch(ref); m != nil {
		return m[2]
	}
	return ""
}

package plane

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

func init() {
	tracker.Register("plane", func() tracker.IssueTracker {
		return &Tracker{}
	})
}

// uuidRe matches a bare UUID, used to distinguish internal IDs from
// human-readable identifiers like "GC-7" in FetchIssue.
var uuidRe = regexp.MustCompile(`^` + uuidPattern + `$`)

// Tracker implements tracker.IssueTracker for Plane CE.
type Tracker struct {
	client *Client
	store  storage.Storage
	refs   refContext

	mu           sync.Mutex
	project      *Project          // cached project (identifier for "GC-7" display IDs)
	stateByID    map[string]*State // cached per-project states
	stateByGroup map[string]string // group -> state UUID to use when pushing
	labelByName  map[string]string // label name -> UUID
	labelByID    map[string]string // label UUID -> name
}

// Compile-time interface check.
var _ tracker.IssueTracker = (*Tracker)(nil)

// Name returns the tracker registry identifier.
func (t *Tracker) Name() string { return "plane" }

// DisplayName returns the human-readable tracker name.
func (t *Tracker) DisplayName() string { return "Plane" }

// ConfigPrefix returns the config key prefix.
func (t *Tracker) ConfigPrefix() string { return "plane" }

// Init reads configuration and constructs the API client. Required keys
// (config or environment): plane.api_key/PLANE_API_KEY,
// plane.base_url/PLANE_BASE_URL, plane.workspace/PLANE_WORKSPACE,
// plane.project_id/PLANE_PROJECT_ID.
func (t *Tracker) Init(ctx context.Context, store storage.Storage) error {
	t.store = store

	apiKey := t.getConfig(ctx, "plane.api_key", "PLANE_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("Plane authentication not configured\n" +
			"Options:\n" +
			"  export PLANE_API_KEY=...    (personal API token from Plane profile settings)\n" +
			"  bd config set plane.api_key \"YOUR_API_KEY\"")
	}

	baseURL := t.getConfig(ctx, "plane.base_url", "PLANE_BASE_URL")
	if baseURL == "" {
		return fmt.Errorf("Plane base URL not configured (set plane.base_url or PLANE_BASE_URL, e.g. https://plane.example.com)")
	}
	workspace := t.getConfig(ctx, "plane.workspace", "PLANE_WORKSPACE")
	if workspace == "" {
		return fmt.Errorf("Plane workspace not configured (set plane.workspace or PLANE_WORKSPACE to the workspace slug)")
	}
	projectID := t.getConfig(ctx, "plane.project_id", "PLANE_PROJECT_ID")
	if projectID == "" {
		return fmt.Errorf("Plane project not configured (set plane.project_id or PLANE_PROJECT_ID to the project UUID)")
	}

	t.client = NewClient(baseURL, apiKey, workspace, projectID)
	t.refs = refContext{
		baseURL:   t.client.BaseURL(),
		workspace: workspace,
		projectID: projectID,
	}
	return nil
}

// Validate checks that the tracker has been initialized.
func (t *Tracker) Validate() error {
	if t.client == nil {
		return fmt.Errorf("Plane tracker not initialized")
	}
	return nil
}

// Close releases tracker resources.
func (t *Tracker) Close() error { return nil }

// FetchIssues retrieves work items from the configured project, enriched
// with state objects and label names. Plane's list API has no updated_at
// filter, so incremental sync (opts.Since) is filtered client-side after an
// -updated_at ordered fetch.
func (t *Tracker) FetchIssues(ctx context.Context, opts tracker.FetchOptions) ([]tracker.TrackerIssue, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	if err := t.ensureCaches(ctx); err != nil {
		return nil, err
	}

	issues, err := t.client.ListIssues(ctx, ListIssuesOptions{OrderBy: "-updated_at"})
	if err != nil {
		return nil, fmt.Errorf("fetching Plane work items: %w", err)
	}

	result := make([]tracker.TrackerIssue, 0, len(issues))
	for i := range issues {
		native := issues[i]
		if opts.Since != nil && native.UpdatedAt.Before(*opts.Since) {
			continue
		}
		if !t.matchesStateFilter(&native, opts.State) {
			continue
		}
		if opts.Limit > 0 && len(result) >= opts.Limit {
			break
		}
		result = append(result, t.toTrackerIssue(&native))
	}
	return result, nil
}

// matchesStateFilter reports whether the issue passes the open/closed pull
// filter. Closed means the issue's state group is GroupCompleted or GroupCancelled;
// an issue whose state is missing from the cache counts as open (matching
// StatusToBeads, which imports unknown states as open).
func (t *Tracker) matchesStateFilter(native *Issue, stateFilter string) bool {
	if stateFilter == "" || stateFilter == "all" {
		return true
	}
	t.mu.Lock()
	state := t.stateByID[native.StateID]
	t.mu.Unlock()
	closed := state != nil && (state.Group == GroupCompleted || state.Group == GroupCancelled)
	if stateFilter == "closed" {
		return closed
	}
	return !closed
}

// FetchIssue retrieves one work item by UUID or human-readable identifier
// (e.g. "GC-7"). Returns nil, nil when the issue does not exist.
func (t *Tracker) FetchIssue(ctx context.Context, identifier string) (*tracker.TrackerIssue, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	if err := t.ensureCaches(ctx); err != nil {
		return nil, err
	}

	var native *Issue
	var err error
	if uuidRe.MatchString(identifier) {
		native, err = t.client.GetIssue(ctx, identifier)
	} else {
		native, err = t.client.GetIssueByIdentifier(ctx, identifier)
	}
	if err != nil {
		return nil, fmt.Errorf("fetching Plane work item %s: %w", identifier, err)
	}
	if native == nil {
		return nil, nil
	}
	ti := t.toTrackerIssue(native)
	return &ti, nil
}

// CreateIssue creates a work item for the bead, keyed by external_id =
// bead ID for native idempotency: an interrupted previous sync surfaces as
// Plane's 409 with the existing UUID, which is fetched and reused instead
// of duplicating.
func (t *Tracker) CreateIssue(ctx context.Context, issue *types.Issue) (*tracker.TrackerIssue, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	if err := t.ensureCaches(ctx); err != nil {
		return nil, err
	}

	payload, err := t.buildPayload(ctx, issue)
	if err != nil {
		return nil, err
	}
	createPayload := *payload
	createPayload.ExternalID = issue.ID
	createPayload.ExternalSource = ExternalSource
	if !issue.CreatedAt.IsZero() {
		createPayload.CreatedAt = issue.CreatedAt.UTC().Format("2006-01-02T15:04:05.000000Z")
	}

	created, err := t.client.CreateIssue(ctx, &createPayload)
	var dup *DuplicateError
	if errors.As(err, &dup) && dup.ExistingID != "" {
		if !uuidRe.MatchString(dup.ExistingID) {
			return nil, fmt.Errorf("creating Plane work item for %s: conflict reported existing id %q, which is not a work item UUID", issue.ID, dup.ExistingID)
		}
		// Apply the bead's current fields onto the pre-existing work item
		// so dedup recovery doesn't hand back (and report as pushed) its
		// stale state. The PATCH payload deliberately excludes
		// external_id/external_source/created_at: the linkage already
		// exists and resending it risks another 409.
		existing, patchErr := t.client.UpdateIssue(ctx, dup.ExistingID, payload)
		if patchErr != nil {
			return nil, fmt.Errorf("recovering duplicate Plane work item %s: %w", dup.ExistingID, patchErr)
		}
		fmt.Fprintf(os.Stderr, "plane: dedup — reusing existing work item %s for bead %s\n", existing.ID, issue.ID)
		ti := t.toTrackerIssue(existing)
		return &ti, nil
	}
	if err != nil {
		return nil, fmt.Errorf("creating Plane work item for %s: %w", issue.ID, err)
	}
	ti := t.toTrackerIssue(created)
	return &ti, nil
}

// UpdateIssue pushes the bead's fields onto an existing work item. The
// externalID is the Plane work item UUID (what ExtractIdentifier returns).
// external_id/external_source are never resent: PATCHing a different
// external_id risks a 409 and the linkage is already established.
func (t *Tracker) UpdateIssue(ctx context.Context, externalID string, issue *types.Issue) (*tracker.TrackerIssue, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	if err := t.ensureCaches(ctx); err != nil {
		return nil, err
	}

	payload, err := t.buildPayload(ctx, issue)
	if err != nil {
		return nil, err
	}

	updated, err := t.client.UpdateIssue(ctx, externalID, payload)
	if err != nil {
		return nil, fmt.Errorf("updating Plane work item %s: %w", externalID, err)
	}
	ti := t.toTrackerIssue(updated)
	return &ti, nil
}

// FieldMapper returns the Plane field mapper.
func (t *Tracker) FieldMapper() tracker.FieldMapper {
	return newFieldMapper(t.refs)
}

// IsExternalRef reports whether ref belongs to Plane.
func (t *Tracker) IsExternalRef(ref string) bool {
	return IsPlaneExternalRef(ref)
}

// ExtractIdentifier returns the Plane work item UUID from an external_ref.
func (t *Tracker) ExtractIdentifier(ref string) string {
	return ExtractPlaneIssueID(ref)
}

// BuildExternalRef constructs the external_ref for a tracker issue.
func (t *Tracker) BuildExternalRef(issue *tracker.TrackerIssue) string {
	if issue.URL != "" {
		return issue.URL
	}
	return BuildPlaneExternalRef(t.refs.baseURL, t.refs.workspace, t.refs.projectID, issue.ID)
}

// buildPayload maps a bead's content fields onto a Plane payload: title,
// description (markdown -> HTML), priority, state (status -> group ->
// per-project state UUID), and labels (names -> ensured UUIDs, including
// the beads:* round-trip labels).
func (t *Tracker) buildPayload(ctx context.Context, issue *types.Issue) (*IssuePayload, error) {
	html, err := MarkdownToHTML(issue.Description)
	if err != nil {
		return nil, fmt.Errorf("converting description for %s: %w", issue.ID, err)
	}
	if html == "" {
		// An omitted description_html key would leave the previous remote
		// description in place (PATCH is partial), resurrecting it on the
		// next pull; send Plane's canonical empty document instead.
		html = emptyDescriptionHTML
	}

	stateID, err := t.stateForStatus(issue.Status)
	if err != nil {
		return nil, err
	}

	labelIDs, err := t.ensureLabels(ctx, pushLabelsFor(issue))
	if err != nil {
		return nil, err
	}

	return &IssuePayload{
		Name:            issue.Title,
		DescriptionHTML: html,
		Priority:        PriorityToPlane(issue.Priority),
		StateID:         stateID,
		Labels:          &labelIDs,
	}, nil
}

// ensureCaches loads the project, state, and label caches once per tracker
// instance.
func (t *Tracker) ensureCaches(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.project != nil {
		return nil
	}

	project, err := t.client.GetProject(ctx)
	if err != nil {
		return fmt.Errorf("fetching Plane project %s: %w", t.refs.projectID, err)
	}

	states, err := t.client.ListStates(ctx)
	if err != nil {
		return fmt.Errorf("fetching Plane states: %w", err)
	}
	stateByID := make(map[string]*State, len(states))
	stateByGroup := make(map[string]string, len(states))
	for i := range states {
		s := states[i]
		stateByID[s.ID] = &s
		// Prefer the project's default state for its group; otherwise the
		// first state listed wins.
		if _, ok := stateByGroup[s.Group]; !ok || s.Default {
			stateByGroup[s.Group] = s.ID
		}
	}

	labels, err := t.client.ListLabels(ctx)
	if err != nil {
		return fmt.Errorf("fetching Plane labels: %w", err)
	}
	labelByName := make(map[string]string, len(labels))
	labelByID := make(map[string]string, len(labels))
	for _, l := range labels {
		labelByName[l.Name] = l.ID
		labelByID[l.ID] = l.Name
	}

	t.project = project
	t.stateByID = stateByID
	t.stateByGroup = stateByGroup
	t.labelByName = labelByName
	t.labelByID = labelByID
	return nil
}

// stateForStatus resolves a beads status to the project state UUID to push.
func (t *Tracker) stateForStatus(status types.Status) (string, error) {
	group := BeadsStatusToStateGroup(status)
	t.mu.Lock()
	defer t.mu.Unlock()
	if id, ok := t.stateByGroup[group]; ok {
		return id, nil
	}
	return "", fmt.Errorf("Plane project has no state in group %q for beads status %q", group, status)
}

// ensureLabels maps label names to UUIDs, creating missing labels in the
// project. A concurrent-creation 409 is recovered via the existing UUID
// from the conflict body.
func (t *Tracker) ensureLabels(ctx context.Context, names []string) ([]string, error) {
	ids := make([]string, 0, len(names))
	for _, name := range names {
		t.mu.Lock()
		id, ok := t.labelByName[name]
		t.mu.Unlock()
		if ok {
			ids = append(ids, id)
			continue
		}

		label, err := t.client.CreateLabel(ctx, name)
		var dup *DuplicateError
		if errors.As(err, &dup) && dup.ExistingID != "" {
			label = &Label{ID: dup.ExistingID, Name: name}
			err = nil
		}
		if err != nil {
			return nil, fmt.Errorf("creating Plane label %q: %w", name, err)
		}

		t.mu.Lock()
		t.labelByName[label.Name] = label.ID
		t.labelByID[label.ID] = label.Name
		t.mu.Unlock()
		ids = append(ids, label.ID)
	}
	return ids, nil
}

// toTrackerIssue converts a native Issue into the generic TrackerIssue,
// resolving state objects and label names from the caches.
func (t *Tracker) toTrackerIssue(native *Issue) tracker.TrackerIssue {
	t.mu.Lock()
	state := t.stateByID[native.StateID]
	labels := make([]string, 0, len(native.Labels))
	for _, id := range native.Labels {
		if name, ok := t.labelByID[id]; ok {
			labels = append(labels, name)
		}
	}
	identifier := ""
	if t.project != nil && t.project.Identifier != "" && native.SequenceID > 0 {
		identifier = fmt.Sprintf("%s-%d", t.project.Identifier, native.SequenceID)
	}
	t.mu.Unlock()

	desc := descriptionMarkdown(native.DescriptionHTML)

	ti := tracker.TrackerIssue{
		ID:          native.ID,
		Identifier:  identifier,
		URL:         BuildPlaneExternalRef(t.refs.baseURL, t.refs.workspace, t.refs.projectID, native.ID),
		Title:       native.Name,
		Description: desc,
		Priority:    PriorityToBeads(native.Priority),
		State:       state,
		Labels:      labels,
		CreatedAt:   native.CreatedAt,
		UpdatedAt:   native.UpdatedAt,
		CompletedAt: native.CompletedAt,
		Raw:         native,
	}
	if native.ParentID != "" {
		ti.ParentID = native.ParentID
		ti.ParentInternalID = native.ParentID
	}
	return ti
}

// getConfig reads a config value from storage, falling back to env var.
// Secret keys (plane.api_key) live in config.yaml, not the Dolt database,
// so they are never pushed to remotes.
func (t *Tracker) getConfig(ctx context.Context, key, envVar string) string {
	if config.IsYamlOnlyKey(key) {
		if val := config.GetString(key); val != "" {
			return val
		}
		return os.Getenv(envVar)
	}
	if t.store != nil {
		if val, err := t.store.GetConfig(ctx, key); err == nil && val != "" {
			return val
		}
	}
	return os.Getenv(envVar)
}

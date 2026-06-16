package planetest

// server.go implements the in-process fake Plane CE server. It emulates the
// REST API surface and quirks of Plane CE v1.3.0 (tag cf696d20) as pinned by
// the conformance suite and the recorded API contract:
//
//   - All routes mount under /api/v1. v1.3.0 routes BOTH the legacy issues/
//     path family and the work-items/ family to the same view classes; the
//     fake wires both families to the same handlers.
//   - GET list with both external_id and external_source query params
//     short-circuits to a single non-paginated issue object, with the
//     standard 404 error body when no match exists.
//   - POST work-item create returns 409 {"error": "Issue with the same
//     external id and external source already exists", "id": "<existing
//     uuid>"} when the pair already exists in the project. Label name
//     conflicts return 409 {"error": "Label with the same name already
//     exists in the project", "id": ...}.
//   - States create returns 200, not 201 (v1.3.0 quirk).
//   - Missing X-Api-Key -> 401 {"detail": "Authentication credentials were
//     not provided."}; wrong key -> 403 {"detail": "Given API token is not
//     valid"} (Plane's asymmetric auth errors).
//   - 404 body is {"error": "The requested resource does not exist."}.
//   - Pagination uses the exact v1.3.0 envelope ({grouped_by,
//     sub_grouped_by, total_count, next_cursor, prev_cursor,
//     next_page_results, prev_page_results, count, total_pages,
//     total_results, extra_stats, results}) with "value:offset:is_prev"
//     cursors and offset paging (offset = cursor.offset * per_page).
//     Runtime default and max per_page are 1000; per_page > 1000 -> 400
//     {"detail": "Invalid per_page value. Cannot exceed 1000."}. Malformed
//     cursors -> 400 {"detail": "Invalid cursor parameter."}; well-formed
//     cursors with a negative offset (including the page-0 prev_cursor the
//     server itself emits) -> 400 {"detail": "Error in parsing"}, matching
//     paginate()'s BadPaginationError translation; huge offsets page past
//     the end into an empty final page.
//   - PUT on the work-items routes returns 405 {"detail": "Method \"PUT\"
//     not allowed."}: the upsert view exists at v1.3.0 but is never routed.
//   - The workspace identifier lookup matches the (uppercase-stored)
//     project identifier case-sensitively; a non-numeric sequence component
//     is a 500 {"error": "Something went wrong please try again later"}
//     because v1.3.0 feeds it into an IntegerField lookup and the
//     ValueError escapes to the base view's catch-all handler.
//   - Issue create honors raw-body created_at/created_by overrides
//     (importer spoofing); PATCH does not. The 201 create response carries
//     the pre-override (server-assigned) created_at/created_by — v1.3.0
//     snapshots serializer.data before applying the override — so only
//     subsequent GETs show the spoofed values. completed_at is recomputed
//     from the state group on every save.
//   - Draft work items (is_draft) are excluded from the list, detail, and
//     identifier-lookup endpoints (the issue_objects manager) but stay
//     reachable via the external-id short-circuit (Issue.objects).
//
// Known simplifications relative to a real v1.3.0 deployment:
//   - description_html is stored verbatim: no lxml roundtrip, no nh3
//     sanitization, no 10MB limit.
//   - assignees are accepted as given (deduplicated) because the fake has
//     no member registry; real v1.3.0 silently filters them to project
//     members with role >= 15.
//   - No rate limiting; the X-RateLimit-* headers are static placeholders.
//   - The search, members, relations, links, attachments, activities, and
//     archive endpoints are not implemented (404); project PATCH/DELETE
//     return 405.
//   - order_by supports created_at/updated_at/sequence_id/name (with "-"
//     prefixes); other values fall back to the -created_at default instead
//     of arbitrary Django column ordering.
//   - per_page < 1 returns 400 (real Plane would divide by zero into a 500).
//   - Cursor offsets beyond Go's int range clamp to the nearest int bound,
//     keeping their sign (Python ints are unbounded).
//   - Project identifier validation enforces only length and uppercasing,
//     not the forbidden-characters pattern.
//   - Deleting a parent issue clears children's parent instead of
//     cascading, and deleting a label detaches it from issues.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	fakeDefaultPerPage = 1000
	fakeMaxPerPage     = 1000
	maxRequestBody     = 16 << 20

	msgNotFound       = "The requested resource does not exist."
	msgIssueDuplicate = "Issue with the same external id and external source already exists"
)

// priorityChoices is the v1.3.0 issue priority enum.
var priorityChoices = map[string]bool{
	"urgent": true, "high": true, "medium": true, "low": true, "none": true,
}

// stateGroupChoices is the v1.3.0 state group enum (triage is a valid value
// but creating triage states is rejected by the view).
var stateGroupChoices = map[string]bool{
	"backlog": true, "unstarted": true, "started": true,
	"completed": true, "cancelled": true, "triage": true, //nolint:misspell // Plane API wire value uses the British spelling
}

// ServerConfig configures the fake Plane server.
type ServerConfig struct {
	// APIKey is the only token the server accepts via the X-Api-Key header.
	APIKey string
	// Workspace is the single workspace slug the server serves.
	Workspace string
}

// Server is an in-process fake Plane CE v1.3.0 instance backed by an
// httptest.Server. It is safe for concurrent use.
type Server struct {
	cfg ServerConfig
	hs  *httptest.Server

	mu           sync.Mutex
	workspaceID  string
	userID       string
	projects     map[string]*fakeProject
	projectOrder []string
}

// NewServer starts a fake Plane server with the given configuration. Callers
// must Close it when done.
func NewServer(cfg ServerConfig) *Server {
	s := &Server{
		cfg:         cfg,
		workspaceID: newUUID(),
		userID:      newUUID(),
		projects:    map[string]*fakeProject{},
	}
	s.hs = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// URL returns the instance root URL (the httptest listener address).
func (s *Server) URL() string { return s.hs.URL }

// Close shuts down the underlying HTTP server.
func (s *Server) Close() { s.hs.Close() }

// AddProject registers a project in the fake workspace and returns its new
// UUID. The project is seeded with Plane's five default workflow states,
// one per non-triage state group (see stateGroupChoices for the group
// vocabulary), with Todo (unstarted) as the project default — matching what
// Plane creates on project bootstrap. The identifier is stored uppercased,
// as Plane does.
func (s *Server) AddProject(name, identifier string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addProjectLocked(name, identifier).id
}

func (s *Server) addProjectLocked(name, identifier string) *fakeProject {
	now := time.Now().UTC()
	p := &fakeProject{
		id:         newUUID(),
		name:       name,
		identifier: strings.ToUpper(identifier),
		createdAt:  now,
		updatedAt:  now,
		states:     map[string]*fakeState{},
		labels:     map[string]*fakeLabel{},
		issues:     map[string]*fakeIssue{},
	}
	defaults := []struct {
		name, group, color string
		def                bool
	}{
		{"Backlog", "backlog", "#A3A3A3", false},
		{"Todo", "unstarted", "#3A3A3A", true},
		{"In Progress", "started", "#F59E0B", false},
		{"Done", "completed", "#16A34A", false},
		{"Cancelled", "cancelled", "#EF4444", false}, //nolint:misspell // Plane API wire values use the British spelling
	}
	sequence := 15000.0
	for _, d := range defaults {
		st := &fakeState{
			id:        newUUID(),
			name:      d.name,
			slug:      slugify(d.name),
			group:     d.group,
			color:     d.color,
			isDefault: d.def,
			sequence:  sequence,
			createdAt: now,
			updatedAt: now,
		}
		p.states[st.id] = st
		p.stateOrder = append(p.stateOrder, st.id)
		sequence += 15000
	}
	s.projects[p.id] = p
	s.projectOrder = append(s.projectOrder, p.id)
	return p
}

// ---------------------------------------------------------------------------
// Internal entity state
// ---------------------------------------------------------------------------

type fakeProject struct {
	id          string
	name        string
	identifier  string
	description string
	createdAt   time.Time
	updatedAt   time.Time
	seq         int

	states     map[string]*fakeState
	stateOrder []string
	labels     map[string]*fakeLabel
	labelOrder []string
	issues     map[string]*fakeIssue
	issueOrder []string
}

type fakeState struct {
	id, name, description, color string
	slug, group                  string
	sequence                     float64
	isTriage, isDefault          bool
	externalID, externalSource   string
	createdAt, updatedAt         time.Time
}

type fakeLabel struct {
	id, name, description, color string
	sortOrder                    float64
	externalID, externalSource   string
	createdAt, updatedAt         time.Time
}

type fakeIssue struct {
	id              string
	name            string
	descriptionHTML string
	priority        string
	stateID         string
	parentID        string
	sequenceID      int
	sortOrder       float64
	externalID      string
	externalSource  string
	labels          []string
	assignees       []string
	startDate       string
	targetDate      string
	isDraft         bool
	createdBy       string
	createdAt       time.Time
	updatedAt       time.Time
	completedAt     *time.Time

	comments     map[string]*fakeComment
	commentOrder []string
}

type fakeComment struct {
	id, commentHTML, access    string
	externalID, externalSource string
	createdBy                  string
	createdAt, updatedAt       time.Time
}

// ---------------------------------------------------------------------------
// Wire shapes (response bodies)
// ---------------------------------------------------------------------------

type detailBody struct {
	Detail string `json:"detail"`
}

type errorBody struct {
	Error string `json:"error"`
}

type conflictBody struct {
	Error string `json:"error"`
	ID    string `json:"id"`
}

// fieldErrors mirrors DRF's serializer.errors map: field name (or
// "non_field_errors") to a list of messages.
type fieldErrors map[string][]string

// pageEnvelope is the exact v1.3.0 pagination envelope
// (plane/utils/paginator.py BasePaginator.paginate).
type pageEnvelope struct {
	GroupedBy       any    `json:"grouped_by"`
	SubGroupedBy    any    `json:"sub_grouped_by"`
	TotalCount      int    `json:"total_count"`
	NextCursor      string `json:"next_cursor"`
	PrevCursor      string `json:"prev_cursor"`
	NextPageResults bool   `json:"next_page_results"`
	PrevPageResults bool   `json:"prev_page_results"`
	Count           int    `json:"count"`
	TotalPages      int    `json:"total_pages"`
	TotalResults    int    `json:"total_results"`
	ExtraStats      any    `json:"extra_stats"`
	Results         []any  `json:"results"`
}

// issueWire is the IssueSerializer response shape at v1.3.0 (Meta excludes
// description_json/description_stripped; labels and assignees serialize as
// UUID-string arrays).
type issueWire struct {
	ID                string     `json:"id"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	DeletedAt         *time.Time `json:"deleted_at"`
	Point             *int       `json:"point"`
	Name              string     `json:"name"`
	DescriptionHTML   string     `json:"description_html"`
	DescriptionBinary *string    `json:"description_binary"`
	Priority          string     `json:"priority"`
	StartDate         *string    `json:"start_date"`
	TargetDate        *string    `json:"target_date"`
	SequenceID        int        `json:"sequence_id"`
	SortOrder         float64    `json:"sort_order"`
	CompletedAt       *time.Time `json:"completed_at"`
	ArchivedAt        *string    `json:"archived_at"`
	IsDraft           bool       `json:"is_draft"`
	ExternalSource    *string    `json:"external_source"`
	ExternalID        *string    `json:"external_id"`
	CreatedBy         string     `json:"created_by"`
	Type              *string    `json:"type"`
	TypeID            *string    `json:"type_id"`
	Parent            *string    `json:"parent"`
	State             *string    `json:"state"`
	EstimatePoint     *string    `json:"estimate_point"`
	Project           string     `json:"project"`
	Workspace         string     `json:"workspace"`
	UpdatedBy         *string    `json:"updated_by"`
	Assignees         []string   `json:"assignees"`
	Labels            []string   `json:"labels"`
}

// stateWire is the StateSerializer response shape (fields="__all__").
type stateWire struct {
	ID             string     `json:"id"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at"`
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	Color          string     `json:"color"`
	Slug           string     `json:"slug"`
	Sequence       float64    `json:"sequence"`
	Group          string     `json:"group"`
	IsTriage       bool       `json:"is_triage"`
	Default        bool       `json:"default"`
	ExternalSource *string    `json:"external_source"`
	ExternalID     *string    `json:"external_id"`
	CreatedBy      string     `json:"created_by"`
	UpdatedBy      *string    `json:"updated_by"`
	Project        string     `json:"project"`
	Workspace      string     `json:"workspace"`
}

// labelWire is the LabelSerializer response shape (fields="__all__").
type labelWire struct {
	ID             string     `json:"id"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at"`
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	Color          string     `json:"color"`
	SortOrder      float64    `json:"sort_order"`
	ExternalSource *string    `json:"external_source"`
	ExternalID     *string    `json:"external_id"`
	CreatedBy      string     `json:"created_by"`
	UpdatedBy      *string    `json:"updated_by"`
	Project        string     `json:"project"`
	Workspace      string     `json:"workspace"`
	Parent         *string    `json:"parent"`
}

// commentWire is the IssueCommentSerializer response shape (excludes
// comment_stripped and comment_json).
type commentWire struct {
	ID             string     `json:"id"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at"`
	CommentHTML    string     `json:"comment_html"`
	Attachments    []string   `json:"attachments"`
	Access         string     `json:"access"`
	ExternalSource *string    `json:"external_source"`
	ExternalID     *string    `json:"external_id"`
	EditedAt       *time.Time `json:"edited_at"`
	Parent         *string    `json:"parent"`
	CreatedBy      string     `json:"created_by"`
	UpdatedBy      *string    `json:"updated_by"`
	Project        string     `json:"project"`
	Workspace      string     `json:"workspace"`
	Issue          string     `json:"issue"`
	Actor          string     `json:"actor"`
	IsMember       bool       `json:"is_member"`
}

// projectWire is a representative subset of the annotated ProjectSerializer
// response shape; fields the adapter never reads carry static defaults.
type projectWire struct {
	ID                   string     `json:"id"`
	TotalMembers         int        `json:"total_members"`
	TotalCycles          int        `json:"total_cycles"`
	TotalModules         int        `json:"total_modules"`
	IsMember             bool       `json:"is_member"`
	SortOrder            float64    `json:"sort_order"`
	MemberRole           int        `json:"member_role"`
	IsDeployed           bool       `json:"is_deployed"`
	CoverImageURL        *string    `json:"cover_image_url"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	DeletedAt            *time.Time `json:"deleted_at"`
	Name                 string     `json:"name"`
	Description          string     `json:"description"`
	Identifier           string     `json:"identifier"`
	Network              int        `json:"network"`
	Emoji                *string    `json:"emoji"`
	ModuleView           bool       `json:"module_view"`
	CycleView            bool       `json:"cycle_view"`
	IssueViewsView       bool       `json:"issue_views_view"`
	PageView             bool       `json:"page_view"`
	IntakeView           bool       `json:"intake_view"`
	GuestViewAllFeatures bool       `json:"guest_view_all_features"`
	ArchiveIn            int        `json:"archive_in"`
	CloseIn              int        `json:"close_in"`
	ArchivedAt           *time.Time `json:"archived_at"`
	Timezone             string     `json:"timezone"`
	ExternalSource       *string    `json:"external_source"`
	ExternalID           *string    `json:"external_id"`
	CreatedBy            string     `json:"created_by"`
	UpdatedBy            *string    `json:"updated_by"`
	Workspace            string     `json:"workspace"`
	DefaultAssignee      *string    `json:"default_assignee"`
	ProjectLead          *string    `json:"project_lead"`
	CoverImage           *string    `json:"cover_image"`
	Estimate             *string    `json:"estimate"`
	DefaultState         *string    `json:"default_state"`
}

// ---------------------------------------------------------------------------
// HTTP entry point and routing
// ---------------------------------------------------------------------------

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	const prefix = "/api/v1/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		writeNotFound(w)
		return
	}

	key := r.Header.Get("X-Api-Key")
	if key == "" {
		writeJSON(w, http.StatusUnauthorized, detailBody{Detail: "Authentication credentials were not provided."})
		return
	}
	if key != s.cfg.APIKey {
		writeJSON(w, http.StatusForbidden, detailBody{Detail: "Given API token is not valid"})
		return
	}

	// Informational only — the fake never throttles.
	w.Header().Set("X-RateLimit-Remaining", "59")
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10))

	segs := splitSegments(strings.TrimPrefix(r.URL.Path, "/api/v1"))

	s.mu.Lock()
	defer s.mu.Unlock()
	s.route(w, r, segs)
}

func (s *Server) route(w http.ResponseWriter, r *http.Request, segs []string) {
	if len(segs) < 3 || segs[0] != "workspaces" || segs[1] != s.cfg.Workspace {
		writeNotFound(w)
		return
	}
	rest := segs[2:]
	switch rest[0] {
	case "projects":
		s.routeProjects(w, r, rest[1:])
	case "work-items", "issues":
		// Workspace-level identifier lookup: {PROJECT_IDENTIFIER}-{seq}.
		if len(rest) == 2 {
			s.handleIdentifierLookup(w, r, rest[1])
			return
		}
		writeNotFound(w)
	default:
		writeNotFound(w)
	}
}

func (s *Server) routeProjects(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			items := make([]any, 0, len(s.projectOrder))
			for _, id := range s.projectOrder {
				items = append(items, s.renderProject(s.projects[id]))
			}
			paginateJSON(w, r, items)
		case http.MethodPost:
			s.createProject(w, r)
		default:
			writeMethodNotAllowed(w, r)
		}
		return
	}

	p, ok := s.projects[rest[0]]
	if !ok {
		writeNotFound(w)
		return
	}
	rest = rest[1:]

	if len(rest) == 0 {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, s.renderProject(p))
			return
		}
		// Real v1.3.0 also supports PATCH/DELETE/archive on projects; the
		// fake does not implement them (documented limitation).
		writeMethodNotAllowed(w, r)
		return
	}

	switch rest[0] {
	case "work-items", "issues":
		s.routeIssues(w, r, p, rest[1:])
	case "labels":
		s.routeLabels(w, r, p, rest[1:])
	case "states":
		s.routeStates(w, r, p, rest[1:])
	default:
		writeNotFound(w)
	}
}

func (s *Server) routeIssues(w http.ResponseWriter, r *http.Request, p *fakeProject, rest []string) {
	switch len(rest) {
	case 0:
		switch r.Method {
		case http.MethodGet:
			s.listOrLookupIssues(w, r, p)
		case http.MethodPost:
			s.createIssue(w, r, p)
		default:
			// Includes PUT: the upsert view is unrouted at v1.3.0.
			writeMethodNotAllowed(w, r)
		}
	case 1:
		switch r.Method {
		case http.MethodGet, http.MethodPatch, http.MethodDelete:
		default:
			writeMethodNotAllowed(w, r)
			return
		}
		is, ok := p.issues[rest[0]]
		if !ok || is.isDraft {
			// Drafts are invisible to the detail endpoints, mirroring
			// v1.3.0's Issue.issue_objects manager; the external-id
			// short-circuit is the only draft-visible lookup.
			writeNotFound(w)
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, s.renderIssue(p, is))
		case http.MethodPatch:
			s.patchIssue(w, r, p, is)
		case http.MethodDelete:
			s.deleteIssue(w, p, is)
		}
	default:
		if rest[1] != "comments" {
			writeNotFound(w)
			return
		}
		is, ok := p.issues[rest[0]]
		if !ok {
			writeNotFound(w)
			return
		}
		s.routeComments(w, r, p, is, rest[2:])
	}
}

func (s *Server) routeComments(w http.ResponseWriter, r *http.Request, p *fakeProject, is *fakeIssue, rest []string) {
	switch len(rest) {
	case 0:
		switch r.Method {
		case http.MethodGet:
			sorted := is.sortedComments()
			items := make([]any, 0, len(sorted))
			for _, c := range sorted {
				items = append(items, s.renderComment(p, is, c))
			}
			paginateJSON(w, r, items)
		case http.MethodPost:
			s.createComment(w, r, p, is)
		default:
			writeMethodNotAllowed(w, r)
		}
	case 1:
		switch r.Method {
		case http.MethodGet, http.MethodPatch, http.MethodDelete:
		default:
			writeMethodNotAllowed(w, r)
			return
		}
		c, ok := is.comments[rest[0]]
		if !ok {
			writeNotFound(w)
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, s.renderComment(p, is, c))
		case http.MethodPatch:
			s.patchComment(w, r, p, is, c)
		case http.MethodDelete:
			delete(is.comments, c.id)
			is.commentOrder = removeString(is.commentOrder, c.id)
			w.WriteHeader(http.StatusNoContent)
		}
	default:
		writeNotFound(w)
	}
}

func (s *Server) routeLabels(w http.ResponseWriter, r *http.Request, p *fakeProject, rest []string) {
	switch len(rest) {
	case 0:
		switch r.Method {
		case http.MethodGet:
			items := make([]any, 0, len(p.labelOrder))
			for _, id := range p.labelOrder {
				items = append(items, s.renderLabel(p, p.labels[id]))
			}
			paginateJSON(w, r, items)
		case http.MethodPost:
			s.createLabel(w, r, p)
		default:
			writeMethodNotAllowed(w, r)
		}
	case 1:
		switch r.Method {
		case http.MethodGet, http.MethodPatch, http.MethodDelete:
		default:
			writeMethodNotAllowed(w, r)
			return
		}
		lb, ok := p.labels[rest[0]]
		if !ok {
			writeNotFound(w)
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, s.renderLabel(p, lb))
		case http.MethodPatch:
			s.patchLabel(w, r, p, lb)
		case http.MethodDelete:
			s.deleteLabel(w, p, lb)
		}
	default:
		writeNotFound(w)
	}
}

func (s *Server) routeStates(w http.ResponseWriter, r *http.Request, p *fakeProject, rest []string) {
	switch len(rest) {
	case 0:
		switch r.Method {
		case http.MethodGet:
			items := make([]any, 0, len(p.stateOrder))
			for _, id := range p.stateOrder {
				if p.states[id].isTriage {
					continue // triage states are hidden from the list
				}
				items = append(items, s.renderState(p, p.states[id]))
			}
			paginateJSON(w, r, items)
		case http.MethodPost:
			s.createState(w, r, p)
		default:
			writeMethodNotAllowed(w, r)
		}
	case 1:
		switch r.Method {
		case http.MethodGet, http.MethodPatch, http.MethodDelete:
		default:
			writeMethodNotAllowed(w, r)
			return
		}
		st, ok := p.states[rest[0]]
		if !ok {
			writeNotFound(w)
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, s.renderState(p, st))
		case http.MethodPatch:
			s.patchState(w, r, p, st)
		case http.MethodDelete:
			s.deleteState(w, p, st)
		}
	default:
		writeNotFound(w)
	}
}

// handleIdentifierLookup serves GET /workspaces/{ws}/work-items/{IDENT}-{seq}/.
func (s *Server) handleIdentifierLookup(w http.ResponseWriter, r *http.Request, ident string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, r)
		return
	}
	i := strings.LastIndex(ident, "-")
	if i <= 0 {
		writeNotFound(w)
		return
	}
	projIdent := ident[:i]
	var proj *fakeProject
	for _, pid := range s.projectOrder {
		if p := s.projects[pid]; p.identifier == projIdent {
			// Exact match only: v1.3.0 compares the uppercase-stored
			// identifier case-sensitively in Postgres.
			proj = p
			break
		}
	}
	if proj == nil {
		// v1.3.0 resolves the project for its permission check before it
		// parses the sequence: an unknown (or case-mismatched) project
		// identifier is a 403 PermissionDenied, not a 404 — even when the
		// sequence component is garbage. Verified live 2026-06-13 against
		// Plane CE v1.3.0 ("bdconf-51" and "zzznosuch-notanumber" both 403).
		writeJSON(w, http.StatusForbidden,
			map[string]string{"detail": "You do not have permission to perform this action."})
		return
	}
	seq, err := strconv.Atoi(ident[i+1:])
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			// Python parses arbitrarily large sequence ids; they simply
			// match nothing.
			writeNotFound(w)
			return
		}
		// v1.3.0 feeds the raw sequence component into an IntegerField
		// lookup; the resulting ValueError is unhandled by the base view's
		// specific handlers, so the catch-all converts it to a 500.
		writeJSON(w, http.StatusInternalServerError,
			errorBody{Error: "Something went wrong please try again later"})
		return
	}
	for _, isID := range proj.issueOrder {
		// issue_objects excludes drafts from the identifier lookup.
		if is := proj.issues[isID]; is.sequenceID == seq && !is.isDraft {
			writeJSON(w, http.StatusOK, s.renderIssue(proj, is))
			return
		}
	}
	writeNotFound(w)
}

// ---------------------------------------------------------------------------
// Issue handlers
// ---------------------------------------------------------------------------

func (s *Server) listOrLookupIssues(w http.ResponseWriter, r *http.Request, p *fakeProject) {
	q := r.URL.Query()
	extID, extSrc := q.Get("external_id"), q.Get("external_source")
	if extID != "" && extSrc != "" {
		// v1.3.0 quirk: both params present short-circuits to a single,
		// non-paginated issue object.
		is := p.findIssueByExternal(extID, extSrc)
		if is == nil {
			writeNotFound(w)
			return
		}
		writeJSON(w, http.StatusOK, s.renderIssue(p, is))
		return
	}
	sorted := p.sortedIssues(q.Get("order_by"))
	items := make([]any, 0, len(sorted))
	for _, is := range sorted {
		if is.isDraft {
			// issue_objects excludes drafts from list responses.
			continue
		}
		items = append(items, s.renderIssue(p, is))
	}
	paginateJSON(w, r, items)
}

func (s *Server) createIssue(w http.ResponseWriter, r *http.Request, p *fakeProject) {
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}

	if !body.has("name") {
		writeJSON(w, http.StatusBadRequest, fieldErrors{"name": {"This field is required."}})
		return
	}
	name, ok := requireName255(w, body, "name")
	if !ok {
		return
	}

	priority := "none"
	if body.has("priority") && !body.isNull("priority") {
		v, isStr := body.str("priority")
		if !isStr || !priorityChoices[v] {
			writeJSON(w, http.StatusBadRequest, fieldErrors{"priority": {fmt.Sprintf("%q is not a valid choice.", v)}})
			return
		}
		priority = v
	}

	stateID := ""
	if body.has("state") && !body.isNull("state") {
		v, isStr := body.str("state")
		if !isStr || p.states[v] == nil {
			writeJSON(w, http.StatusBadRequest, fieldErrors{"non_field_errors": {"State is not valid please pass a valid state_id"}})
			return
		}
		stateID = v
	} else {
		stateID = p.defaultStateID()
	}

	parentID := ""
	if body.has("parent") && !body.isNull("parent") {
		v, isStr := body.str("parent")
		if !isStr || p.issues[v] == nil {
			writeJSON(w, http.StatusBadRequest, fieldErrors{"non_field_errors": {"Parent is not valid issue_id please pass a valid issue_id"}})
			return
		}
		parentID = v
	}

	startDate, _, ok := dateField(w, body, "start_date")
	if !ok {
		return
	}
	targetDate, _, ok := dateField(w, body, "target_date")
	if !ok {
		return
	}
	if startDate != "" && targetDate != "" && startDate > targetDate {
		writeJSON(w, http.StatusBadRequest, fieldErrors{"non_field_errors": {"Start date cannot exceed target date"}})
		return
	}

	// Duplicate check runs AFTER validation, matching v1.3.0 (invalid
	// payloads 400 before the dup check).
	extID, _ := body.str("external_id")
	extSrc, _ := body.str("external_source")
	if extID != "" && extSrc != "" {
		if existing := p.findIssueByExternal(extID, extSrc); existing != nil {
			writeJSON(w, http.StatusConflict, conflictBody{Error: msgIssueDuplicate, ID: existing.id})
			return
		}
	}

	descriptionHTML, _ := body.str("description_html")
	if descriptionHTML == "" {
		descriptionHTML = "<p></p>"
	}

	now := time.Now().UTC()
	createdAt := now
	if v, isStr := body.str("created_at"); isStr {
		// v1.3.0 honors a raw created_at body key on create (importer
		// timestamp spoofing); unparseable values are ignored by the fake.
		if t, perr := parseFlexibleTime(v); perr == nil {
			createdAt = t.UTC()
		}
	}
	createdBy := s.userID
	if v, isStr := body.str("created_by"); isStr && v != "" {
		createdBy = v
	}

	labels := []string{}
	if vals, isList := body.strList("labels"); isList {
		labels = p.filterLabels(vals)
	}
	assignees := []string{}
	if vals, isList := body.strList("assignees"); isList {
		// Fake limitation: accepted as-is (deduped). Real v1.3.0 silently
		// filters to project members with role >= 15.
		assignees = dedupe(vals)
	}

	isDraft, _ := body.boolVal("is_draft")

	p.seq++
	is := &fakeIssue{
		id:              newUUID(),
		name:            name,
		descriptionHTML: descriptionHTML,
		priority:        priority,
		stateID:         stateID,
		parentID:        parentID,
		sequenceID:      p.seq,
		sortOrder:       float64(p.seq) * 65535,
		externalID:      extID,
		externalSource:  extSrc,
		labels:          labels,
		assignees:       assignees,
		startDate:       startDate,
		targetDate:      targetDate,
		isDraft:         isDraft,
		createdBy:       createdBy,
		createdAt:       createdAt,
		updatedAt:       now,
		comments:        map[string]*fakeComment{},
	}
	p.recomputeCompletedAt(is, now)
	p.issues[is.id] = is
	p.issueOrder = append(p.issueOrder, is.id)

	// v1.3.0 serializes the response BEFORE applying the created_at /
	// created_by overrides (the view caches serializer.data when it reads
	// serializer.data["id"]), so the 201 body always carries the
	// server-assigned values; only subsequent GETs show the spoofed ones.
	wire := s.renderIssue(p, is)
	wire.CreatedAt = now
	wire.CreatedBy = s.userID
	writeJSON(w, http.StatusCreated, wire)
}

func (s *Server) patchIssue(w http.ResponseWriter, r *http.Request, p *fakeProject, is *fakeIssue) {
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}

	// PATCH external_id conflict: body external_id set, differs from the
	// current value, and another issue in the project holds the pair (the
	// external_source comes from the body when present, else the issue).
	if newExt, isStr := body.str("external_id"); isStr && newExt != "" && newExt != is.externalID {
		src := is.externalSource
		if v, isStr := body.str("external_source"); isStr {
			src = v
		}
		if other := p.findIssueByExternal(newExt, src); other != nil && other.id != is.id {
			// v1.3.0 quirk: the PATCH 409 body carries the PATCHED issue's
			// own id (the view returns str(issue.id)), unlike the POST 409
			// which returns the conflicting issue's id.
			writeJSON(w, http.StatusConflict, conflictBody{Error: msgIssueDuplicate, ID: is.id})
			return
		}
	}

	// Validate everything before mutating so a failed PATCH is atomic.
	name, nameSet := "", false
	if body.has("name") {
		v, ok := requireName255(w, body, "name")
		if !ok {
			return
		}
		name, nameSet = v, true
	}
	if body.has("priority") && !body.isNull("priority") {
		v, isStr := body.str("priority")
		if !isStr || !priorityChoices[v] {
			writeJSON(w, http.StatusBadRequest, fieldErrors{"priority": {fmt.Sprintf("%q is not a valid choice.", v)}})
			return
		}
	}
	if body.has("state") && !body.isNull("state") {
		v, isStr := body.str("state")
		if !isStr || p.states[v] == nil {
			writeJSON(w, http.StatusBadRequest, fieldErrors{"non_field_errors": {"State is not valid please pass a valid state_id"}})
			return
		}
	}
	if body.has("parent") && !body.isNull("parent") {
		v, isStr := body.str("parent")
		if !isStr || p.issues[v] == nil || v == is.id {
			writeJSON(w, http.StatusBadRequest, fieldErrors{"non_field_errors": {"Parent is not valid issue_id please pass a valid issue_id"}})
			return
		}
	}
	startDate, startSet, ok := dateField(w, body, "start_date")
	if !ok {
		return
	}
	targetDate, targetSet, ok := dateField(w, body, "target_date")
	if !ok {
		return
	}
	effStart, effTarget := is.startDate, is.targetDate
	if startSet {
		effStart = startDate
	}
	if targetSet {
		effTarget = targetDate
	}
	if effStart != "" && effTarget != "" && effStart > effTarget {
		writeJSON(w, http.StatusBadRequest, fieldErrors{"non_field_errors": {"Start date cannot exceed target date"}})
		return
	}

	// Apply.
	if nameSet {
		is.name = name
	}
	if body.has("priority") && !body.isNull("priority") {
		is.priority, _ = body.str("priority")
	}
	if body.has("state") {
		if body.isNull("state") {
			is.stateID = ""
		} else {
			is.stateID, _ = body.str("state")
		}
	}
	if body.has("parent") {
		if body.isNull("parent") {
			is.parentID = ""
		} else {
			is.parentID, _ = body.str("parent")
		}
	}
	if body.has("description_html") {
		v, _ := body.str("description_html")
		if v == "" {
			v = "<p></p>"
		}
		is.descriptionHTML = v
	}
	// Labels/assignees use full-replacement semantics, applied only when
	// the key is present.
	if vals, isList := body.strList("labels"); isList {
		is.labels = p.filterLabels(vals)
	}
	if vals, isList := body.strList("assignees"); isList {
		is.assignees = dedupe(vals)
	}
	if body.has("external_id") {
		is.externalID, _ = body.str("external_id")
	}
	if body.has("external_source") {
		is.externalSource, _ = body.str("external_source")
	}
	if startSet {
		is.startDate = startDate
	}
	if targetSet {
		is.targetDate = targetDate
	}
	if v, isBool := body.boolVal("is_draft"); isBool {
		is.isDraft = v
	}
	// Note: created_at/created_by overrides are NOT honored on PATCH,
	// matching v1.3.0 (create-only importer spoofing).

	now := time.Now().UTC()
	if !now.After(is.updatedAt) {
		now = is.updatedAt.Add(time.Microsecond)
	}
	is.updatedAt = now
	p.recomputeCompletedAt(is, now)

	writeJSON(w, http.StatusOK, s.renderIssue(p, is))
}

func (s *Server) deleteIssue(w http.ResponseWriter, p *fakeProject, is *fakeIssue) {
	delete(p.issues, is.id)
	p.issueOrder = removeString(p.issueOrder, is.id)
	for _, other := range p.issues {
		if other.parentID == is.id {
			other.parentID = ""
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Comment handlers
// ---------------------------------------------------------------------------

func (s *Server) createComment(w http.ResponseWriter, r *http.Request, p *fakeProject, is *fakeIssue) {
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}

	access := "INTERNAL"
	if body.has("access") && !body.isNull("access") {
		v, isStr := body.str("access")
		if !isStr || (v != "INTERNAL" && v != "EXTERNAL") {
			writeJSON(w, http.StatusBadRequest, fieldErrors{"access": {fmt.Sprintf("%q is not a valid choice.", v)}})
			return
		}
		access = v
	}

	extID, _ := body.str("external_id")
	extSrc, _ := body.str("external_source")
	if extID != "" && extSrc != "" {
		if existing := is.findCommentByExternal(extID, extSrc); existing != nil {
			writeJSON(w, http.StatusConflict, conflictBody{
				Error: "Work item comment with the same external id and external source already exists",
				ID:    existing.id,
			})
			return
		}
	}

	// comment_html is stored as-is: IssueCommentCreateSerializer has no
	// validate() at v1.3.0 (no sanitization).
	commentHTML, _ := body.str("comment_html")

	now := time.Now().UTC()
	createdAt := now
	if v, isStr := body.str("created_at"); isStr {
		if t, perr := parseFlexibleTime(v); perr == nil {
			createdAt = t.UTC()
		}
	}
	createdBy := s.userID
	if v, isStr := body.str("created_by"); isStr && v != "" {
		createdBy = v
	}

	c := &fakeComment{
		id:             newUUID(),
		commentHTML:    commentHTML,
		access:         access,
		externalID:     extID,
		externalSource: extSrc,
		createdBy:      createdBy,
		createdAt:      createdAt,
		updatedAt:      now,
	}
	is.comments[c.id] = c
	is.commentOrder = append(is.commentOrder, c.id)

	writeJSON(w, http.StatusCreated, s.renderComment(p, is, c))
}

func (s *Server) patchComment(w http.ResponseWriter, r *http.Request, p *fakeProject, is *fakeIssue, c *fakeComment) {
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}

	if newExt, isStr := body.str("external_id"); isStr && newExt != "" && newExt != c.externalID {
		src := c.externalSource
		if v, isStr := body.str("external_source"); isStr {
			src = v
		}
		if other := is.findCommentByExternal(newExt, src); other != nil && other.id != c.id {
			writeJSON(w, http.StatusConflict, conflictBody{
				Error: "Work item comment with the same external id and external source already exists",
				ID:    c.id,
			})
			return
		}
	}
	if body.has("access") && !body.isNull("access") {
		v, isStr := body.str("access")
		if !isStr || (v != "INTERNAL" && v != "EXTERNAL") {
			writeJSON(w, http.StatusBadRequest, fieldErrors{"access": {fmt.Sprintf("%q is not a valid choice.", v)}})
			return
		}
		c.access = v
	}
	if body.has("comment_html") {
		c.commentHTML, _ = body.str("comment_html")
	}
	if body.has("external_id") {
		c.externalID, _ = body.str("external_id")
	}
	if body.has("external_source") {
		c.externalSource, _ = body.str("external_source")
	}

	now := time.Now().UTC()
	if !now.After(c.updatedAt) {
		now = c.updatedAt.Add(time.Microsecond)
	}
	c.updatedAt = now

	writeJSON(w, http.StatusOK, s.renderComment(p, is, c))
}

// ---------------------------------------------------------------------------
// Label handlers
// ---------------------------------------------------------------------------

func (s *Server) createLabel(w http.ResponseWriter, r *http.Request, p *fakeProject) {
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}

	if !body.has("name") {
		writeJSON(w, http.StatusBadRequest, fieldErrors{"name": {"This field is required."}})
		return
	}
	name, ok := requireName255(w, body, "name")
	if !ok {
		return
	}

	if existing := p.findLabelByName(name); existing != nil {
		writeJSON(w, http.StatusConflict, conflictBody{
			Error: "Label with the same name already exists in the project",
			ID:    existing.id,
		})
		return
	}
	extID, _ := body.str("external_id")
	extSrc, _ := body.str("external_source")
	if extID != "" && extSrc != "" {
		if existing := p.findLabelByExternal(extID, extSrc); existing != nil {
			writeJSON(w, http.StatusConflict, conflictBody{
				Error: "Label with the same external id and external source already exists",
				ID:    existing.id,
			})
			return
		}
	}

	color, _ := body.str("color")
	description, _ := body.str("description")

	now := time.Now().UTC()
	lb := &fakeLabel{
		id:             newUUID(),
		name:           name,
		description:    description,
		color:          color,
		sortOrder:      p.maxLabelSortOrder() + 10000,
		externalID:     extID,
		externalSource: extSrc,
		createdAt:      now,
		updatedAt:      now,
	}
	p.labels[lb.id] = lb
	p.labelOrder = append(p.labelOrder, lb.id)

	writeJSON(w, http.StatusCreated, s.renderLabel(p, lb))
}

func (s *Server) patchLabel(w http.ResponseWriter, r *http.Request, p *fakeProject, lb *fakeLabel) {
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}

	name, nameSet := "", false
	if body.has("name") {
		v, ok := requireName255(w, body, "name")
		if !ok {
			return
		}
		if other := p.findLabelByName(v); other != nil && other.id != lb.id {
			writeJSON(w, http.StatusConflict, conflictBody{
				Error: "Label with the same name already exists in the project",
				ID:    other.id,
			})
			return
		}
		name, nameSet = v, true
	}
	if newExt, isStr := body.str("external_id"); isStr && newExt != "" && newExt != lb.externalID {
		src := lb.externalSource
		if v, isStr := body.str("external_source"); isStr {
			src = v
		}
		if other := p.findLabelByExternal(newExt, src); other != nil && other.id != lb.id {
			writeJSON(w, http.StatusConflict, conflictBody{
				Error: "Label with the same external id and external source already exists",
				ID:    lb.id,
			})
			return
		}
	}

	if nameSet {
		lb.name = name
	}
	if body.has("color") {
		lb.color, _ = body.str("color")
	}
	if body.has("description") {
		lb.description, _ = body.str("description")
	}
	if body.has("external_id") {
		lb.externalID, _ = body.str("external_id")
	}
	if body.has("external_source") {
		lb.externalSource, _ = body.str("external_source")
	}

	now := time.Now().UTC()
	if !now.After(lb.updatedAt) {
		now = lb.updatedAt.Add(time.Microsecond)
	}
	lb.updatedAt = now

	writeJSON(w, http.StatusOK, s.renderLabel(p, lb))
}

func (s *Server) deleteLabel(w http.ResponseWriter, p *fakeProject, lb *fakeLabel) {
	delete(p.labels, lb.id)
	p.labelOrder = removeString(p.labelOrder, lb.id)
	for _, is := range p.issues {
		is.labels = removeString(is.labels, lb.id)
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// State handlers
// ---------------------------------------------------------------------------

func (s *Server) createState(w http.ResponseWriter, r *http.Request, p *fakeProject) {
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}

	if !body.has("name") {
		writeJSON(w, http.StatusBadRequest, fieldErrors{"name": {"This field is required."}})
		return
	}
	name, ok := requireName255(w, body, "name")
	if !ok {
		return
	}
	if !body.has("color") {
		writeJSON(w, http.StatusBadRequest, fieldErrors{"color": {"This field is required."}})
		return
	}
	color, ok := requireName255(w, body, "color")
	if !ok {
		return
	}

	group := "backlog"
	if body.has("group") && !body.isNull("group") {
		v, isStr := body.str("group")
		if !isStr || !stateGroupChoices[v] {
			writeJSON(w, http.StatusBadRequest, fieldErrors{"group": {fmt.Sprintf("%q is not a valid choice.", v)}})
			return
		}
		if v == "triage" {
			writeJSON(w, http.StatusBadRequest, errorBody{Error: "Cannot create triage state"})
			return
		}
		group = v
	}

	if existing := p.findStateByName(name); existing != nil {
		writeJSON(w, http.StatusConflict, conflictBody{
			Error: "State with the same name already exists in the project",
			ID:    existing.id,
		})
		return
	}
	extID, _ := body.str("external_id")
	extSrc, _ := body.str("external_source")
	if extID != "" && extSrc != "" {
		if existing := p.findStateByExternal(extID, extSrc); existing != nil {
			writeJSON(w, http.StatusConflict, conflictBody{
				Error: "State with the same external id and external source already exists",
				ID:    existing.id,
			})
			return
		}
	}

	isDefault, _ := body.boolVal("default")
	if isDefault {
		p.clearDefaultStates()
	}
	description, _ := body.str("description")

	now := time.Now().UTC()
	st := &fakeState{
		id:             newUUID(),
		name:           name,
		slug:           slugify(name),
		description:    description,
		color:          color,
		group:          group,
		sequence:       p.maxStateSequence() + 15000,
		isDefault:      isDefault,
		externalID:     extID,
		externalSource: extSrc,
		createdAt:      now,
		updatedAt:      now,
	}
	p.states[st.id] = st
	p.stateOrder = append(p.stateOrder, st.id)

	// v1.3.0 quirk: state create returns 200, not 201.
	writeJSON(w, http.StatusOK, s.renderState(p, st))
}

func (s *Server) patchState(w http.ResponseWriter, r *http.Request, p *fakeProject, st *fakeState) {
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}

	name, nameSet := "", false
	if body.has("name") {
		v, ok := requireName255(w, body, "name")
		if !ok {
			return
		}
		if other := p.findStateByName(v); other != nil && other.id != st.id {
			writeJSON(w, http.StatusConflict, conflictBody{
				Error: "State with the same name already exists in the project",
				ID:    other.id,
			})
			return
		}
		name, nameSet = v, true
	}
	if body.has("group") && !body.isNull("group") {
		v, isStr := body.str("group")
		if !isStr || !stateGroupChoices[v] {
			writeJSON(w, http.StatusBadRequest, fieldErrors{"group": {fmt.Sprintf("%q is not a valid choice.", v)}})
			return
		}
		if v == "triage" {
			writeJSON(w, http.StatusBadRequest, errorBody{Error: "Cannot create triage state"})
			return
		}
		st.group = v
	}
	if newExt, isStr := body.str("external_id"); isStr && newExt != "" && newExt != st.externalID {
		src := st.externalSource
		if v, isStr := body.str("external_source"); isStr {
			src = v
		}
		if other := p.findStateByExternal(newExt, src); other != nil && other.id != st.id {
			writeJSON(w, http.StatusConflict, conflictBody{
				Error: "State with the same external id and external source already exists",
				ID:    st.id,
			})
			return
		}
	}

	if nameSet {
		st.name = name
		st.slug = slugify(name)
	}
	if body.has("color") {
		st.color, _ = body.str("color")
	}
	if body.has("description") {
		st.description, _ = body.str("description")
	}
	if v, isBool := body.boolVal("default"); isBool {
		if v {
			p.clearDefaultStates()
		}
		st.isDefault = v
	}
	if body.has("external_id") {
		st.externalID, _ = body.str("external_id")
	}
	if body.has("external_source") {
		st.externalSource, _ = body.str("external_source")
	}

	now := time.Now().UTC()
	if !now.After(st.updatedAt) {
		now = st.updatedAt.Add(time.Microsecond)
	}
	st.updatedAt = now

	writeJSON(w, http.StatusOK, s.renderState(p, st))
}

func (s *Server) deleteState(w http.ResponseWriter, p *fakeProject, st *fakeState) {
	if st.isDefault {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "Default state cannot be deleted"})
		return
	}
	for _, is := range p.issues {
		if is.stateID == st.id {
			writeJSON(w, http.StatusBadRequest, errorBody{Error: "The state is not empty, only empty states can be deleted"})
			return
		}
	}
	delete(p.states, st.id)
	p.stateOrder = removeString(p.stateOrder, st.id)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Project handlers
// ---------------------------------------------------------------------------

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}

	if !body.has("name") {
		writeJSON(w, http.StatusBadRequest, fieldErrors{"name": {"This field is required."}})
		return
	}
	name, ok := requireName255(w, body, "name")
	if !ok {
		return
	}
	if !body.has("identifier") || body.isNull("identifier") {
		writeJSON(w, http.StatusBadRequest, fieldErrors{"identifier": {"This field is required."}})
		return
	}
	identRaw, isStr := body.str("identifier")
	if !isStr || strings.TrimSpace(identRaw) == "" {
		writeJSON(w, http.StatusBadRequest, fieldErrors{"identifier": {"This field may not be blank."}})
		return
	}
	identifier := strings.ToUpper(strings.TrimSpace(identRaw))
	if utf8.RuneCountInString(identifier) > 12 {
		writeJSON(w, http.StatusBadRequest, fieldErrors{"identifier": {"Ensure this field has no more than 12 characters."}})
		return
	}

	for _, id := range s.projectOrder {
		if s.projects[id].name == name {
			writeJSON(w, http.StatusConflict, map[string]string{"name": "The project name is already taken"})
			return
		}
		if s.projects[id].identifier == identifier {
			writeJSON(w, http.StatusConflict, map[string]string{"identifier": "The project identifier is already taken"})
			return
		}
	}

	p := s.addProjectLocked(name, identifier)
	if v, isStr := body.str("description"); isStr {
		p.description = v
	}
	writeJSON(w, http.StatusCreated, s.renderProject(p))
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

func (s *Server) renderIssue(p *fakeProject, is *fakeIssue) issueWire {
	return issueWire{
		ID:              is.id,
		CreatedAt:       is.createdAt,
		UpdatedAt:       is.updatedAt,
		Name:            is.name,
		DescriptionHTML: is.descriptionHTML,
		Priority:        is.priority,
		StartDate:       strPtr(is.startDate),
		TargetDate:      strPtr(is.targetDate),
		SequenceID:      is.sequenceID,
		SortOrder:       is.sortOrder,
		CompletedAt:     copyTimePtr(is.completedAt),
		IsDraft:         is.isDraft,
		ExternalSource:  strPtr(is.externalSource),
		ExternalID:      strPtr(is.externalID),
		CreatedBy:       is.createdBy,
		Parent:          strPtr(is.parentID),
		State:           strPtr(is.stateID),
		Project:         p.id,
		Workspace:       s.workspaceID,
		UpdatedBy:       strPtr(s.userID),
		Assignees:       append([]string{}, is.assignees...),
		Labels:          append([]string{}, is.labels...),
	}
}

func (s *Server) renderState(p *fakeProject, st *fakeState) stateWire {
	return stateWire{
		ID:             st.id,
		CreatedAt:      st.createdAt,
		UpdatedAt:      st.updatedAt,
		Name:           st.name,
		Description:    st.description,
		Color:          st.color,
		Slug:           st.slug,
		Sequence:       st.sequence,
		Group:          st.group,
		IsTriage:       st.isTriage,
		Default:        st.isDefault,
		ExternalSource: strPtr(st.externalSource),
		ExternalID:     strPtr(st.externalID),
		CreatedBy:      s.userID,
		Project:        p.id,
		Workspace:      s.workspaceID,
	}
}

func (s *Server) renderLabel(p *fakeProject, lb *fakeLabel) labelWire {
	return labelWire{
		ID:             lb.id,
		CreatedAt:      lb.createdAt,
		UpdatedAt:      lb.updatedAt,
		Name:           lb.name,
		Description:    lb.description,
		Color:          lb.color,
		SortOrder:      lb.sortOrder,
		ExternalSource: strPtr(lb.externalSource),
		ExternalID:     strPtr(lb.externalID),
		CreatedBy:      s.userID,
		Project:        p.id,
		Workspace:      s.workspaceID,
	}
}

func (s *Server) renderComment(p *fakeProject, is *fakeIssue, c *fakeComment) commentWire {
	return commentWire{
		ID:             c.id,
		CreatedAt:      c.createdAt,
		UpdatedAt:      c.updatedAt,
		CommentHTML:    c.commentHTML,
		Attachments:    []string{},
		Access:         c.access,
		ExternalSource: strPtr(c.externalSource),
		ExternalID:     strPtr(c.externalID),
		CreatedBy:      c.createdBy,
		Project:        p.id,
		Workspace:      s.workspaceID,
		Issue:          is.id,
		Actor:          c.createdBy,
		IsMember:       true,
	}
}

func (s *Server) renderProject(p *fakeProject) projectWire {
	return projectWire{
		ID:             p.id,
		TotalMembers:   1,
		IsMember:       true,
		SortOrder:      65535,
		MemberRole:     20,
		CreatedAt:      p.createdAt,
		UpdatedAt:      p.updatedAt,
		Name:           p.name,
		Description:    p.description,
		Identifier:     p.identifier,
		Network:        2,
		ModuleView:     true,
		CycleView:      true,
		IssueViewsView: true,
		PageView:       true,
		Timezone:       "UTC",
		CreatedBy:      s.userID,
		Workspace:      s.workspaceID,
	}
}

// ---------------------------------------------------------------------------
// Project-scoped queries
// ---------------------------------------------------------------------------

func (p *fakeProject) defaultStateID() string {
	for _, id := range p.stateOrder {
		if p.states[id].isDefault {
			return id
		}
	}
	if len(p.stateOrder) > 0 {
		return p.stateOrder[0]
	}
	return ""
}

func (p *fakeProject) clearDefaultStates() {
	for _, st := range p.states {
		st.isDefault = false
	}
}

// recomputeCompletedAt mirrors v1.3.0's Issue.save(): completed_at is set
// when the state's group is "completed" and cleared otherwise, on every save.
func (p *fakeProject) recomputeCompletedAt(is *fakeIssue, now time.Time) {
	st := p.states[is.stateID]
	if st != nil && st.group == "completed" {
		if is.completedAt == nil {
			t := now
			is.completedAt = &t
		}
		return
	}
	is.completedAt = nil
}

func (p *fakeProject) findIssueByExternal(extID, extSrc string) *fakeIssue {
	for _, id := range p.issueOrder {
		is := p.issues[id]
		if is.externalID == extID && is.externalSource == extSrc {
			return is
		}
	}
	return nil
}

func (p *fakeProject) findLabelByName(name string) *fakeLabel {
	for _, id := range p.labelOrder {
		if p.labels[id].name == name {
			return p.labels[id]
		}
	}
	return nil
}

func (p *fakeProject) findLabelByExternal(extID, extSrc string) *fakeLabel {
	for _, id := range p.labelOrder {
		lb := p.labels[id]
		if lb.externalID == extID && lb.externalSource == extSrc {
			return lb
		}
	}
	return nil
}

func (p *fakeProject) findStateByName(name string) *fakeState {
	for _, id := range p.stateOrder {
		if p.states[id].name == name {
			return p.states[id]
		}
	}
	return nil
}

func (p *fakeProject) findStateByExternal(extID, extSrc string) *fakeState {
	for _, id := range p.stateOrder {
		st := p.states[id]
		if st.externalID == extID && st.externalSource == extSrc {
			return st
		}
	}
	return nil
}

func (p *fakeProject) maxLabelSortOrder() float64 {
	max := 0.0
	for _, lb := range p.labels {
		if lb.sortOrder > max {
			max = lb.sortOrder
		}
	}
	return max
}

func (p *fakeProject) maxStateSequence() float64 {
	max := 0.0
	for _, st := range p.states {
		if st.sequence > max {
			max = st.sequence
		}
	}
	return max
}

// filterLabels keeps only IDs of labels that exist in the project,
// preserving order and dropping duplicates — v1.3.0 filters silently, with
// no error for unknown UUIDs.
func (p *fakeProject) filterLabels(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if p.labels[id] != nil && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// sortedIssues returns the project's issues in the requested order. The
// server default is -created_at; unsupported order_by values fall back to it
// (fake limitation — real Plane passes them to Django).
func (p *fakeProject) sortedIssues(orderBy string) []*fakeIssue {
	list := make([]*fakeIssue, 0, len(p.issues))
	for _, id := range p.issueOrder {
		list = append(list, p.issues[id])
	}
	if orderBy == "" {
		orderBy = "-created_at"
	}
	desc := strings.HasPrefix(orderBy, "-")
	var less func(a, b *fakeIssue) bool
	switch strings.TrimPrefix(orderBy, "-") {
	case "created_at":
		less = func(a, b *fakeIssue) bool { return a.createdAt.Before(b.createdAt) }
	case "updated_at":
		less = func(a, b *fakeIssue) bool { return a.updatedAt.Before(b.updatedAt) }
	case "sequence_id":
		less = func(a, b *fakeIssue) bool { return a.sequenceID < b.sequenceID }
	case "name":
		less = func(a, b *fakeIssue) bool { return a.name < b.name }
	default:
		less = func(a, b *fakeIssue) bool { return a.createdAt.Before(b.createdAt) }
		desc = true
	}
	sort.SliceStable(list, func(i, j int) bool {
		if desc {
			return less(list[j], list[i])
		}
		return less(list[i], list[j])
	})
	return list
}

// sortedComments returns the issue's comments newest-first (-created_at).
func (is *fakeIssue) sortedComments() []*fakeComment {
	list := make([]*fakeComment, 0, len(is.commentOrder))
	for _, id := range is.commentOrder {
		list = append(list, is.comments[id])
	}
	sort.SliceStable(list, func(i, j int) bool {
		return list[j].createdAt.Before(list[i].createdAt)
	})
	return list
}

func (is *fakeIssue) findCommentByExternal(extID, extSrc string) *fakeComment {
	for _, id := range is.commentOrder {
		c := is.comments[id]
		if c.externalID == extID && c.externalSource == extSrc {
			return c
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Pagination
// ---------------------------------------------------------------------------

// paginateJSON renders items through the exact v1.3.0 pagination envelope,
// honoring the per_page and cursor query params with offset paging
// (offset = cursor.offset * per_page).
func paginateJSON(w http.ResponseWriter, r *http.Request, items []any) {
	perPage := fakeDefaultPerPage
	if raw := r.URL.Query().Get("per_page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, detailBody{Detail: "Invalid per_page parameter."})
			return
		}
		if n > fakeMaxPerPage {
			writeJSON(w, http.StatusBadRequest, detailBody{Detail: "Invalid per_page value. Cannot exceed 1000."})
			return
		}
		if n < 1 {
			// Fake divergence: real v1.3.0 would divide by zero into a 500.
			writeJSON(w, http.StatusBadRequest, detailBody{Detail: "Invalid per_page parameter."})
			return
		}
		perPage = n
	}

	page := 0
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		var ok bool
		page, ok = parseCursorPage(raw)
		if !ok {
			writeJSON(w, http.StatusBadRequest, detailBody{Detail: "Invalid cursor parameter."})
			return
		}
	}
	if page < 0 {
		// Real v1.3.0 parses negative offsets as well-formed cursors (its
		// own page-0 prev_cursor is one) and only rejects them inside
		// get_result: BadPaginationError -> ParseError "Error in parsing".
		writeJSON(w, http.StatusBadRequest, detailBody{Detail: "Error in parsing"})
		return
	}

	total := len(items)
	// Guard the offset multiplication: a huge page number must page past the
	// end (an empty final page, like a huge Postgres OFFSET), not overflow
	// into a slice-bounds panic.
	offset := total
	if page <= math.MaxInt/perPage {
		offset = page * perPage
	}
	if offset > total {
		offset = total
	}
	end := offset + perPage
	if end > total {
		end = total
	}
	results := items[offset:end]
	if results == nil {
		results = []any{}
	}

	writeJSON(w, http.StatusOK, pageEnvelope{
		TotalCount:      total,
		NextCursor:      fmt.Sprintf("%d:%d:0", perPage, page+1),
		PrevCursor:      fmt.Sprintf("%d:%d:1", perPage, page-1),
		NextPageResults: end < total,
		PrevPageResults: page > 0,
		Count:           len(results),
		TotalPages:      (total + perPage - 1) / perPage,
		TotalResults:    total,
		Results:         results,
	})
}

// parseCursorPage parses a "value:offset:is_prev" cursor and returns the
// page number (the offset component). Negative pages parse successfully —
// real v1.3.0 treats them as well-formed and rejects them later inside
// get_result — and offsets beyond Go's int range clamp to the nearest bound,
// keeping their sign (Python ints are unbounded, so such cursors are still
// well-formed there; documented fake simplification).
func parseCursorPage(raw string) (int, bool) {
	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return 0, false
	}
	if _, err := strconv.ParseFloat(parts[0], 64); err != nil {
		return 0, false
	}
	if _, err := strconv.Atoi(parts[2]); err != nil {
		return 0, false
	}
	page, err := strconv.Atoi(parts[1])
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, false
	}
	// On ErrRange, Atoi already returned the clamped math.MinInt/MaxInt.
	return page, true
}

// ---------------------------------------------------------------------------
// Request-body helpers
// ---------------------------------------------------------------------------

// jsonBody is a decoded JSON object with presence-aware accessors, used to
// implement DRF partial-update semantics (only provided keys change).
type jsonBody map[string]any

func (b jsonBody) has(key string) bool {
	_, ok := b[key]
	return ok
}

func (b jsonBody) isNull(key string) bool {
	v, ok := b[key]
	return ok && v == nil
}

// str returns the value as a string; the second result is false when the key
// is absent, null, or not a string.
func (b jsonBody) str(key string) (string, bool) {
	v, ok := b[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// strList returns the value as a string slice; non-string elements are
// dropped silently. The second result is false when the key is absent, null,
// or not an array.
func (b jsonBody) strList(key string) ([]string, bool) {
	v, ok := b[key]
	if !ok || v == nil {
		return nil, false
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out, true
}

// boolVal returns the value as a bool; the second result is false when the
// key is absent, null, or not a bool.
func (b jsonBody) boolVal(key string) (bool, bool) {
	v, ok := b[key]
	if !ok || v == nil {
		return false, false
	}
	bv, ok := v.(bool)
	return bv, ok
}

// decodeBody reads and decodes a JSON object request body. On failure it
// writes a DRF-style parse error and returns ok=false.
func decodeBody(w http.ResponseWriter, r *http.Request) (jsonBody, bool) {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, detailBody{Detail: "JSON parse error - unable to read request body."})
		return nil, false
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return jsonBody{}, true
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		writeJSON(w, http.StatusBadRequest, detailBody{Detail: "JSON parse error - " + err.Error()})
		return nil, false
	}
	if m == nil {
		m = map[string]any{}
	}
	return jsonBody(m), true
}

// requireName255 validates a present DRF CharField(max_length=255)-style
// value, writing the serializer-errors response and returning ok=false on
// failure. Callers handle the absent-key (required) case themselves.
func requireName255(w http.ResponseWriter, body jsonBody, key string) (string, bool) {
	if body.isNull(key) {
		writeJSON(w, http.StatusBadRequest, fieldErrors{key: {"This field may not be null."}})
		return "", false
	}
	v, isStr := body.str(key)
	if !isStr || strings.TrimSpace(v) == "" {
		writeJSON(w, http.StatusBadRequest, fieldErrors{key: {"This field may not be blank."}})
		return "", false
	}
	if utf8.RuneCountInString(v) > 255 {
		writeJSON(w, http.StatusBadRequest, fieldErrors{key: {"Ensure this field has no more than 255 characters."}})
		return "", false
	}
	return v, true
}

// dateField validates an optional "YYYY-MM-DD" field. It returns the value
// ("" when absent or null), whether the key was present, and ok=false when
// it already wrote a 400 response.
func dateField(w http.ResponseWriter, body jsonBody, key string) (val string, present bool, ok bool) {
	if !body.has(key) {
		return "", false, true
	}
	if body.isNull(key) {
		return "", true, true
	}
	v, isStr := body.str(key)
	if !isStr {
		writeJSON(w, http.StatusBadRequest, fieldErrors{key: {"Date has wrong format. Use one of these formats instead: YYYY-MM-DD."}})
		return "", false, false
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		writeJSON(w, http.StatusBadRequest, fieldErrors{key: {"Date has wrong format. Use one of these formats instead: YYYY-MM-DD."}})
		return "", false, false
	}
	return v, true, true
}

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		// Encoding failures cannot be reported to the client at this point;
		// the conformance suite would surface them as decode errors.
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeNotFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, errorBody{Error: msgNotFound})
}

func writeMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusMethodNotAllowed, detailBody{Detail: fmt.Sprintf("Method %q not allowed.", r.Method)})
}

// ---------------------------------------------------------------------------
// Small utilities
// ---------------------------------------------------------------------------

// newUUID returns a canonical random UUID string. uuid.NewRandom only fails
// when the OS entropy source is broken; the time-based fallback keeps the
// fake panic-free per the house rules.
func newUUID() string {
	id, err := uuid.NewRandom()
	if err != nil {
		return fmt.Sprintf("00000000-0000-4000-8000-%012x", time.Now().UnixNano()&0xffffffffffff)
	}
	return id.String()
}

func splitSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func removeString(list []string, v string) []string {
	out := list[:0]
	for _, e := range list {
		if e != v {
			out = append(out, e)
		}
	}
	return out
}

func dedupe(vals []string) []string {
	out := make([]string, 0, len(vals))
	seen := make(map[string]bool, len(vals))
	for _, v := range vals {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func copyTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	c := *t
	return &c
}

func slugify(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func parseFlexibleTime(v string) (time.Time, error) {
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp %q", v)
}

package spikeb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// ReferenceCatalog proves that exact foreign state is useful without any
// resolver. It is a PROTOTYPE local store, not a production repository.
type ReferenceCatalog struct {
	mu   sync.Mutex
	refs map[BeadID][]Reference
}

func NewReferenceCatalog() *ReferenceCatalog {
	return &ReferenceCatalog{refs: make(map[BeadID][]Reference)}
}

func (c *ReferenceCatalog) Store(source BeadID, ref Reference) error {
	if source == "" {
		return fmt.Errorf("%w: source is required", ErrInvalid)
	}
	if err := validateExactForeign(ref); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refs[source] = append(c.refs[source], ref)
	return nil
}

func (c *ReferenceCatalog) Inspect(source BeadID) []Reference {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Reference(nil), c.refs[source]...)
}

func (c *ReferenceCatalog) Export(source BeadID) []Reference {
	return c.Inspect(source)
}

func validateExactForeign(ref Reference) error {
	switch {
	case ref.Local:
		return fmt.Errorf("%w: B2 fixture requires a foreign locator", ErrInvalid)
	case ref.ProjectID == "" || ref.BeadID == "" || ref.RevisionID == "":
		return fmt.Errorf("%w: an exact foreign locator needs project, bead, and revision identity", ErrInvalid)
	case ref.RevisionID == CurrentRevision:
		return fmt.Errorf("%w: foreign revision must be exact", ErrInvalid)
	case ref.ExpectedScope == "":
		return fmt.Errorf("%w: expected target scope is required", ErrInvalid)
	case ref.ExpectedKind != KindMemory:
		return fmt.Errorf("%w: the first portable foreign target must be a memory", ErrInvalid)
	default:
		return nil
	}
}

type ResolutionStatus string

const (
	ResolutionUnconfigured    ResolutionStatus = "resolver_unconfigured"
	ResolutionDenied          ResolutionStatus = "denied"
	ResolutionUnavailable     ResolutionStatus = "unavailable"
	ResolutionProjectMismatch ResolutionStatus = "project_mismatch"
	ResolutionScopeMismatch   ResolutionStatus = "scope_mismatch"
	ResolutionKindMismatch    ResolutionStatus = "kind_mismatch"
	ResolutionMissingRevision ResolutionStatus = "missing_revision"
	ResolutionResolved        ResolutionStatus = "resolved"
)

type ResolvedMemory struct {
	Address Address `json:"address"`
	Scope   string  `json:"scope"`
	Kind    Kind    `json:"kind"`
	Body    string  `json:"body"`
}

type Resolution struct {
	Status ResolutionStatus `json:"status"`
	Memory *ResolvedMemory  `json:"memory,omitempty"`
}

// Resolver is the smallest shared B2 seam justified by both the direct and
// HTTP adapters below. It remains internal and provisional.
type Resolver interface {
	ResolveExact(context.Context, Reference) Resolution
}

func ResolveStored(ctx context.Context, resolver Resolver, ref Reference) Resolution {
	if resolver == nil {
		return Resolution{Status: ResolutionUnconfigured}
	}
	if err := validateExactForeign(ref); err != nil {
		return Resolution{Status: ResolutionUnavailable}
	}
	return resolver.ResolveExact(ctx, ref)
}

type endpointAccess string

const (
	endpointAllowed     endpointAccess = "allowed"
	endpointDenied      endpointAccess = "denied"
	endpointUnavailable endpointAccess = "unavailable"
)

// ProjectEndpoint is fixture-owned target state. Tests configure identity,
// scope, access, and exact revisions without exposing it through Resolver.
type ProjectEndpoint struct {
	ProjectID ProjectID
	Scope     string
	Kind      Kind
	Access    endpointAccess
	Memories  map[BeadID]map[RevisionID]string
}

func NewProjectEndpoint(projectID ProjectID, scope string) *ProjectEndpoint {
	return &ProjectEndpoint{
		ProjectID: projectID,
		Scope:     scope,
		Kind:      KindMemory,
		Access:    endpointAllowed,
		Memories:  make(map[BeadID]map[RevisionID]string),
	}
}

func (e *ProjectEndpoint) AddMemory(id BeadID, revision RevisionID, body string) {
	if e.Memories[id] == nil {
		e.Memories[id] = make(map[RevisionID]string)
	}
	e.Memories[id][revision] = body
}

func (e *ProjectEndpoint) resolve(ref Reference) Resolution {
	if e == nil || e.Access == endpointUnavailable {
		return Resolution{Status: ResolutionUnavailable}
	}
	if e.Access == endpointDenied {
		// Denial deliberately carries no address, mismatch detail, or body.
		return Resolution{Status: ResolutionDenied}
	}
	if e.ProjectID != ref.ProjectID {
		return Resolution{Status: ResolutionProjectMismatch}
	}
	if e.Scope != ref.ExpectedScope {
		return Resolution{Status: ResolutionScopeMismatch}
	}
	if e.Kind != ref.ExpectedKind {
		return Resolution{Status: ResolutionKindMismatch}
	}
	body, ok := e.Memories[ref.BeadID][ref.RevisionID]
	if !ok {
		return Resolution{Status: ResolutionMissingRevision}
	}
	return Resolution{
		Status: ResolutionResolved,
		Memory: &ResolvedMemory{
			Address: Address{ProjectID: e.ProjectID, BeadID: ref.BeadID, RevisionID: ref.RevisionID},
			Scope:   e.Scope,
			Kind:    e.Kind,
			Body:    body,
		},
	}
}

// ResolverDocument is the HTTP fixture's independently represented target.
// Unlike ProjectEndpoint, it models a published document containing flat
// revision records and a document-specific read policy. Keeping the target
// model separate catches accidental coupling between Resolver and one
// provider's in-process representation.
type ResolverDocument struct {
	Identity   string
	ScopeLabel string
	RecordType string
	ReadPolicy string
	Records    []ResolverDocumentRecord
}

type ResolverDocumentRecord struct {
	MemoryKey string
	Version   string
	Markdown  string
}

const (
	documentReadable    = "readable"
	documentForbidden   = "forbidden"
	documentUnreachable = "unreachable"
)

func NewResolverDocument(projectID ProjectID, scope string) *ResolverDocument {
	return &ResolverDocument{
		Identity:   string(projectID),
		ScopeLabel: scope,
		RecordType: string(KindMemory),
		ReadPolicy: documentReadable,
	}
}

func (d *ResolverDocument) AddMemory(id BeadID, revision RevisionID, body string) {
	d.Records = append(d.Records, ResolverDocumentRecord{
		MemoryKey: string(id),
		Version:   string(revision),
		Markdown:  body,
	})
}

// resolveExact implements the HTTP document provider's target semantics. It
// deliberately does not translate to ProjectEndpoint or call its resolver.
func (d *ResolverDocument) resolveExact(ref Reference) Resolution {
	if d == nil || d.ReadPolicy == documentUnreachable {
		return Resolution{Status: ResolutionUnavailable}
	}
	if d.ReadPolicy == documentForbidden {
		// A forbidden document discloses neither identity checks nor content.
		return Resolution{Status: ResolutionDenied}
	}
	if ProjectID(d.Identity) != ref.ProjectID {
		return Resolution{Status: ResolutionProjectMismatch}
	}
	if d.ScopeLabel != ref.ExpectedScope {
		return Resolution{Status: ResolutionScopeMismatch}
	}
	if Kind(d.RecordType) != ref.ExpectedKind {
		return Resolution{Status: ResolutionKindMismatch}
	}
	for _, record := range d.Records {
		if BeadID(record.MemoryKey) != ref.BeadID || RevisionID(record.Version) != ref.RevisionID {
			continue
		}
		return Resolution{
			Status: ResolutionResolved,
			Memory: &ResolvedMemory{
				Address: Address{
					ProjectID:  ProjectID(d.Identity),
					BeadID:     BeadID(record.MemoryKey),
					RevisionID: RevisionID(record.Version),
				},
				Scope: d.ScopeLabel,
				Kind:  Kind(d.RecordType),
				Body:  record.Markdown,
			},
		}
	}
	return Resolution{Status: ResolutionMissingRevision}
}

// RegistryResolver is a direct in-process route adapter.
type RegistryResolver struct {
	mu     sync.RWMutex
	routes map[ProjectID]*ProjectEndpoint
}

var _ Resolver = (*RegistryResolver)(nil)

func NewRegistryResolver() *RegistryResolver {
	return &RegistryResolver{routes: make(map[ProjectID]*ProjectEndpoint)}
}

func (r *RegistryResolver) SetRoute(projectID ProjectID, endpoint *ProjectEndpoint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes[projectID] = endpoint
}

func (r *RegistryResolver) ResolveExact(_ context.Context, ref Reference) Resolution {
	r.mu.RLock()
	endpoint := r.routes[ref.ProjectID]
	r.mu.RUnlock()
	if endpoint == nil {
		return Resolution{Status: ResolutionUnavailable}
	}
	return endpoint.resolve(ref)
}

// HTTPResolver crosses a real local HTTP transport and decodes only the shared
// semantic result. It does not wrap RegistryResolver.
type HTTPResolver struct {
	mu     sync.RWMutex
	client *http.Client
	routes map[ProjectID]string
}

var _ Resolver = (*HTTPResolver)(nil)

func NewHTTPResolver(client *http.Client) *HTTPResolver {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPResolver{client: client, routes: make(map[ProjectID]string)}
}

func (r *HTTPResolver) SetRoute(projectID ProjectID, route string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes[projectID] = route
}

func (r *HTTPResolver) ResolveExact(ctx context.Context, ref Reference) Resolution {
	r.mu.RLock()
	route := r.routes[ref.ProjectID]
	r.mu.RUnlock()
	if route == "" {
		return Resolution{Status: ResolutionUnavailable}
	}
	payload, err := json.Marshal(ref)
	if err != nil {
		return Resolution{Status: ResolutionUnavailable}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, route+"/resolve-exact", bytes.NewReader(payload))
	if err != nil {
		return Resolution{Status: ResolutionUnavailable}
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return Resolution{Status: ResolutionUnavailable}
	}
	defer func() { _ = response.Body.Close() }()
	var result Resolution
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&result) != nil {
		return Resolution{Status: ResolutionUnavailable}
	}
	return result
}

// NewResolverDocumentHTTPHandler exposes the independently implemented B2
// document provider across the local HTTP transport used by tests.
func NewResolverDocumentHTTPHandler(document *ResolverDocument) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /resolve-exact", func(writer http.ResponseWriter, request *http.Request) {
		var ref Reference
		if err := json.NewDecoder(request.Body).Decode(&ref); err != nil {
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(document.resolveExact(ref))
	})
	return mux
}

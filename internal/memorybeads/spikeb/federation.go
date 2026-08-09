package spikeb

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
)

type ProjectSummary struct {
	ProjectID    ProjectID `json:"project_id"`
	DisplayName  string    `json:"display_name"`
	Capabilities []string  `json:"capabilities"`
}

// Discovery and Contributor are PROTOTYPE seams kept independent on purpose.
// A provider may demonstrate either without acquiring the other.
type Discovery interface {
	Discover(context.Context) ([]ProjectSummary, error)
}

type Contributor interface {
	Contribute(context.Context, ContributionRequest) ContributionResult
}

type ContributionOutcome string

const (
	ContributionApplied           ContributionOutcome = "applied"
	ContributionAppliedUnverified ContributionOutcome = "applied_unverified"
	ContributionPending           ContributionOutcome = "pending"
	ContributionRejected          ContributionOutcome = "rejected"
	ContributionIndeterminate     ContributionOutcome = "indeterminate"
)

type ContributionRequest struct {
	SourceProjectID  ProjectID  `json:"source_project_id"`
	TargetProjectID  ProjectID  `json:"target_project_id"`
	BeadID           BeadID     `json:"bead_id"`
	ExpectedRevision RevisionID `json:"expected_revision"`
	Body             string     `json:"body"`
	Author           string     `json:"author"`
}

type ContributionResult struct {
	Outcome ContributionOutcome `json:"outcome"`
	Address *Address            `json:"address,omitempty"`
}

// FederationProject is target-owned fixture state. Its configured outcome
// models governance/publication observations, not a portable role policy.
type FederationProject struct {
	mu sync.Mutex

	Summary              ProjectSummary
	NextOutcome          ContributionOutcome
	PublishIndeterminate bool
	sequence             uint64
	memories             map[BeadID]ownedMemory
}

type ownedMemory struct {
	RevisionID RevisionID
	Body       string
	Author     string
}

func NewFederationProject(id ProjectID, name string) *FederationProject {
	return &FederationProject{
		Summary: ProjectSummary{
			ProjectID:    id,
			DisplayName:  name,
			Capabilities: []string{"pinned-resolution", "contribution"},
		},
		NextOutcome: ContributionApplied,
		memories:    make(map[BeadID]ownedMemory),
	}
}

func (p *FederationProject) Put(id BeadID, revision RevisionID, body string) {
	p.PutAttributed(id, revision, body, "")
}

func (p *FederationProject) PutAttributed(id BeadID, revision RevisionID, body, author string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.memories[id] = ownedMemory{RevisionID: revision, Body: body, Author: author}
}

func (p *FederationProject) Current(id BeadID) (RevisionID, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	memory := p.memories[id]
	return memory.RevisionID, memory.Body
}

func (p *FederationProject) Attribution(id BeadID) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.memories[id].Author
}

func (p *FederationProject) contribute(request ContributionRequest) ContributionResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	memory, exists := p.memories[request.BeadID]
	if !exists || request.Author == "" || request.ExpectedRevision != memory.RevisionID {
		return ContributionResult{Outcome: ContributionRejected}
	}
	outcome := p.NextOutcome
	if outcome == "" {
		outcome = ContributionApplied
	}
	shouldPublish := outcome == ContributionApplied || outcome == ContributionAppliedUnverified || (outcome == ContributionIndeterminate && p.PublishIndeterminate)
	var address *Address
	if shouldPublish {
		p.sequence++
		revision := RevisionID("fed-rev-" + formatUint(p.sequence))
		p.memories[request.BeadID] = ownedMemory{RevisionID: revision, Body: request.Body, Author: request.Author}
		if outcome == ContributionApplied || outcome == ContributionAppliedUnverified {
			value := Address{ProjectID: p.Summary.ProjectID, BeadID: request.BeadID, RevisionID: revision}
			address = &value
		}
	}
	return ContributionResult{Outcome: outcome, Address: address}
}

type FederationRegistry struct {
	mu       sync.RWMutex
	projects map[ProjectID]*FederationProject
}

func NewFederationRegistry() *FederationRegistry {
	return &FederationRegistry{projects: make(map[ProjectID]*FederationProject)}
}

func (r *FederationRegistry) Register(project *FederationProject) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.projects[project.Summary.ProjectID] = project
}

func (r *FederationRegistry) discover() []ProjectSummary {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ProjectSummary, 0, len(r.projects))
	for _, project := range r.projects {
		summary := project.Summary
		summary.Capabilities = append([]string(nil), summary.Capabilities...)
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProjectID < out[j].ProjectID })
	return out
}

func (r *FederationRegistry) contribute(request ContributionRequest) ContributionResult {
	r.mu.RLock()
	project := r.projects[request.TargetProjectID]
	r.mu.RUnlock()
	if project == nil {
		return ContributionResult{Outcome: ContributionRejected}
	}
	return project.contribute(request)
}

// RegistryFederationAdapter uses direct, explicit registration. It never scans
// local directories or treats registration as read authorization.
type RegistryFederationAdapter struct {
	registry *FederationRegistry
}

var _ Discovery = (*RegistryFederationAdapter)(nil)
var _ Contributor = (*RegistryFederationAdapter)(nil)

func NewRegistryFederationAdapter(registry *FederationRegistry) *RegistryFederationAdapter {
	return &RegistryFederationAdapter{registry: registry}
}

func (a *RegistryFederationAdapter) Discover(_ context.Context) ([]ProjectSummary, error) {
	return a.registry.discover(), nil
}

func (a *RegistryFederationAdapter) Contribute(_ context.Context, request ContributionRequest) ContributionResult {
	return a.registry.contribute(request)
}

// FederationDocumentAuthority is an independently represented HTTP fixture
// provider. Projects are flat documents and memories are document entries;
// it neither embeds FederationRegistry nor delegates publication to
// FederationProject. The separate implementation is deliberate spike
// evidence that caller needs are not an artifact of the registry model.
type FederationDocumentAuthority struct {
	mu sync.Mutex

	documents []FederationDocument
	rules     map[ProjectID]documentContributionRule
	sequence  uint64
}

type FederationDocument struct {
	Identity   string
	Label      string
	Advertised []string
	Entries    []FederationDocumentEntry
}

type FederationDocumentEntry struct {
	MemoryKey    string
	Version      string
	Markdown     string
	AttributedTo string
}

type documentContributionRule struct {
	Outcome              ContributionOutcome
	PublishIndeterminate bool
}

func NewFederationDocumentAuthority() *FederationDocumentAuthority {
	return &FederationDocumentAuthority{rules: make(map[ProjectID]documentContributionRule)}
}

func (a *FederationDocumentAuthority) AddProject(id ProjectID, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.documents = append(a.documents, FederationDocument{
		Identity:   string(id),
		Label:      name,
		Advertised: []string{"pinned-resolution", "contribution"},
	})
	a.rules[id] = documentContributionRule{Outcome: ContributionApplied}
}

func (a *FederationDocumentAuthority) Put(projectID ProjectID, id BeadID, revision RevisionID, body, author string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	document := a.documentLocked(projectID)
	if document == nil {
		return
	}
	for index := range document.Entries {
		if document.Entries[index].MemoryKey == string(id) {
			document.Entries[index] = FederationDocumentEntry{
				MemoryKey: string(id), Version: string(revision), Markdown: body, AttributedTo: author,
			}
			return
		}
	}
	document.Entries = append(document.Entries, FederationDocumentEntry{
		MemoryKey: string(id), Version: string(revision), Markdown: body, AttributedTo: author,
	})
}

func (a *FederationDocumentAuthority) Current(projectID ProjectID, id BeadID) (RevisionID, string, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	document := a.documentLocked(projectID)
	if document == nil {
		return "", "", ""
	}
	for _, entry := range document.Entries {
		if entry.MemoryKey == string(id) {
			return RevisionID(entry.Version), entry.Markdown, entry.AttributedTo
		}
	}
	return "", "", ""
}

func (a *FederationDocumentAuthority) SetOutcome(projectID ProjectID, outcome ContributionOutcome) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rule := a.rules[projectID]
	rule.Outcome = outcome
	a.rules[projectID] = rule
}

func (a *FederationDocumentAuthority) SetPublishIndeterminate(projectID ProjectID, publish bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rule := a.rules[projectID]
	rule.PublishIndeterminate = publish
	a.rules[projectID] = rule
}

func (a *FederationDocumentAuthority) documentLocked(projectID ProjectID) *FederationDocument {
	for index := range a.documents {
		if ProjectID(a.documents[index].Identity) == projectID {
			return &a.documents[index]
		}
	}
	return nil
}

func (a *FederationDocumentAuthority) discover() []ProjectSummary {
	a.mu.Lock()
	defer a.mu.Unlock()
	projects := make([]ProjectSummary, 0, len(a.documents))
	for _, document := range a.documents {
		projects = append(projects, ProjectSummary{
			ProjectID:    ProjectID(document.Identity),
			DisplayName:  document.Label,
			Capabilities: append([]string(nil), document.Advertised...),
		})
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].ProjectID < projects[j].ProjectID })
	return projects
}

func (a *FederationDocumentAuthority) contribute(request ContributionRequest) ContributionResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	document := a.documentLocked(request.TargetProjectID)
	if document == nil || request.Author == "" {
		return ContributionResult{Outcome: ContributionRejected}
	}
	entryIndex := -1
	for index := range document.Entries {
		if document.Entries[index].MemoryKey == string(request.BeadID) {
			entryIndex = index
			break
		}
	}
	if entryIndex < 0 || RevisionID(document.Entries[entryIndex].Version) != request.ExpectedRevision {
		return ContributionResult{Outcome: ContributionRejected}
	}
	rule := a.rules[request.TargetProjectID]
	outcome := rule.Outcome
	if outcome == "" {
		outcome = ContributionApplied
	}
	shouldPublish := outcome == ContributionApplied || outcome == ContributionAppliedUnverified ||
		(outcome == ContributionIndeterminate && rule.PublishIndeterminate)
	var address *Address
	if shouldPublish {
		a.sequence++
		revision := RevisionID("doc-rev-" + formatUint(a.sequence))
		document.Entries[entryIndex] = FederationDocumentEntry{
			MemoryKey:    string(request.BeadID),
			Version:      string(revision),
			Markdown:     request.Body,
			AttributedTo: request.Author,
		}
		if outcome == ContributionApplied || outcome == ContributionAppliedUnverified {
			value := Address{ProjectID: request.TargetProjectID, BeadID: request.BeadID, RevisionID: revision}
			address = &value
		}
	}
	return ContributionResult{Outcome: outcome, Address: address}
}

// HTTPFederationAdapter demonstrates the same two caller needs across a real
// transport. It shares result types, not registry implementation.
type HTTPFederationAdapter struct {
	baseURL string
	client  *http.Client
}

var _ Discovery = (*HTTPFederationAdapter)(nil)
var _ Contributor = (*HTTPFederationAdapter)(nil)

func NewHTTPFederationAdapter(baseURL string, client *http.Client) *HTTPFederationAdapter {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPFederationAdapter{baseURL: baseURL, client: client}
}

func (a *HTTPFederationAdapter) Discover(ctx context.Context) ([]ProjectSummary, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"/discover", nil)
	if err != nil {
		return nil, err
	}
	response, err := a.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	var projects []ProjectSummary
	if err := json.NewDecoder(response.Body).Decode(&projects); err != nil {
		return nil, err
	}
	return projects, nil
}

func (a *HTTPFederationAdapter) Contribute(ctx context.Context, contribution ContributionRequest) ContributionResult {
	payload, err := json.Marshal(contribution)
	if err != nil {
		return ContributionResult{Outcome: ContributionIndeterminate}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/contribute", bytes.NewReader(payload))
	if err != nil {
		return ContributionResult{Outcome: ContributionIndeterminate}
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(request)
	if err != nil {
		return ContributionResult{Outcome: ContributionIndeterminate}
	}
	defer func() { _ = response.Body.Close() }()
	var result ContributionResult
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&result) != nil {
		return ContributionResult{Outcome: ContributionIndeterminate}
	}
	return result
}

func NewFederationDocumentHTTPHandler(authority *FederationDocumentAuthority) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /discover", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(authority.discover())
	})
	mux.HandleFunc("POST /contribute", func(writer http.ResponseWriter, request *http.Request) {
		var contribution ContributionRequest
		if err := json.NewDecoder(request.Body).Decode(&contribution); err != nil {
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(authority.contribute(contribution))
	})
	return mux
}

func formatUint(value uint64) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}

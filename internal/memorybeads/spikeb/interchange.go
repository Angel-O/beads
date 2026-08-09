package spikeb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/steveyegge/beads/internal/lockfile"
	a2 "github.com/steveyegge/beads/internal/memorybeads/spikea2"
)

const harnessBImportAuthor = "Harness B Importer <harness-b@example.test>"

// InterchangeProvider is a PROTOTYPE fixture seam. Prepare and Apply are split
// only so the spike can make a destination race deterministic.
type InterchangeProvider interface {
	ProjectID() ProjectID
	Export(context.Context) (InterchangeUnit, error)
	PrepareImport(context.Context, InterchangeUnit) (ImportPlan, error)
	ApplyImport(context.Context, ImportPlan) (ImportResult, error)
	Snapshot(context.Context) ([]Record, error)
	FailBeforePublishOnce()
}

// ImportPlan is bound to one destination instance and generation. Its fields
// are private so callers cannot rewrite the state validated by preflight.
type ImportPlan struct {
	providerToken string
	generation    uint64
	unit          InterchangeUnit
	mapping       map[BeadID]BeadID
	noop          bool
}

type ImportOutcome string

const (
	ImportApplied   ImportOutcome = "applied"
	ImportUnchanged ImportOutcome = "unchanged"
)

type ImportResult struct {
	Outcome ImportOutcome
	Mapping map[BeadID]BeadID
	Records []Record
}

// LegacyImport proves declaration-first rejection: apply is never called for a
// canonical unit, even when records follow the declaration.
func LegacyImport(unit InterchangeUnit, apply func(Record) error) error {
	if unit.Declaration.Format == InterchangeFormat {
		return ErrLegacyRejected
	}
	for _, record := range unit.Records {
		if err := apply(cloneRecord(record)); err != nil {
			return err
		}
	}
	return nil
}

func prepareImport(
	providerToken string,
	projectID ProjectID,
	generation uint64,
	nextID uint64,
	prefix string,
	current map[BeadID]Record,
	unit InterchangeUnit,
) (ImportPlan, error) {
	if err := validateDeclaration(unit.Declaration); err != nil {
		return ImportPlan{}, err
	}
	if unit.SourceProjectID == "" {
		return ImportPlan{}, fmt.Errorf("%w: source project ID is required", ErrInvalid)
	}
	incoming, err := recordsByID(unit.Records)
	if err != nil {
		return ImportPlan{}, err
	}
	if len(incoming) == 0 {
		return ImportPlan{}, fmt.Errorf("%w: an interchange unit needs records", ErrInvalid)
	}
	for _, record := range unit.Records {
		if err := validateRecord(unit.SourceProjectID, incoming, record); err != nil {
			return ImportPlan{}, err
		}
	}

	if unit.SourceProjectID == projectID && exactSubset(current, incoming) {
		mapping := make(map[BeadID]BeadID, len(incoming))
		for id := range incoming {
			mapping[id] = id
		}
		return ImportPlan{
			providerToken: providerToken,
			generation:    generation,
			unit:          cloneUnit(unit),
			mapping:       mapping,
			noop:          true,
		}, nil
	}

	keys := make(map[string]BeadID)
	for id, record := range current {
		if record.Kind == KindMemory && record.Key != "" {
			keys[record.Key] = id
		}
	}
	for _, record := range unit.Records {
		if record.Kind != KindMemory || record.Key == "" {
			continue
		}
		if owner, exists := keys[record.Key]; exists {
			return ImportPlan{}, fmt.Errorf("%w: memory key %q is already owned by %q", ErrConflict, record.Key, owner)
		}
		keys[record.Key] = record.ID
	}

	mapping := make(map[BeadID]BeadID, len(unit.Records))
	reserved := make(map[BeadID]bool, len(current)+len(unit.Records))
	for id := range current {
		reserved[id] = true
	}
	for _, record := range unit.Records {
		for {
			nextID++
			candidate := BeadID(fmt.Sprintf("%s%d", prefix, nextID))
			if reserved[candidate] {
				continue
			}
			reserved[candidate] = true
			mapping[record.ID] = candidate
			break
		}
	}

	// Mapping every reference now proves that unsupported floating locals and
	// malformed historical pins fail before either provider stages state.
	placeholderRevisions := make(map[BeadID]RevisionID)
	for _, record := range unit.Records {
		if record.Kind == KindMemory {
			placeholderRevisions[record.ID] = "validated-destination-revision"
		}
	}
	for _, record := range unit.Records {
		if _, err := mapReferences(unit, incoming, mapping, placeholderRevisions, record); err != nil {
			return ImportPlan{}, err
		}
	}

	return ImportPlan{
		providerToken: providerToken,
		generation:    generation,
		unit:          cloneUnit(unit),
		mapping:       mapping,
	}, nil
}

func validateDeclaration(declaration Declaration) error {
	switch {
	case declaration.Format != InterchangeFormat:
		return fmt.Errorf("%w: format %q", ErrUnsupportedDeclaration, declaration.Format)
	case declaration.Version != InterchangeVersion:
		return fmt.Errorf("%w: version %q", ErrUnsupportedDeclaration, declaration.Version)
	case declaration.Scope != InterchangeScope:
		return fmt.Errorf("%w: scope %q", ErrUnsupportedDeclaration, declaration.Scope)
	}
	supported := map[string]bool{
		CapabilityAtomicConnected: true,
		CapabilityExactPins:       true,
	}
	seen := make(map[string]bool)
	for _, capability := range declaration.RequiredCapabilities {
		if !supported[capability] {
			return fmt.Errorf("%w: required capability %q", ErrUnsupportedDeclaration, capability)
		}
		seen[capability] = true
	}
	for capability := range supported {
		if !seen[capability] {
			return fmt.Errorf("%w: declaration omitted required capability %q", ErrUnsupportedDeclaration, capability)
		}
	}
	return nil
}

func validateRecord(sourceProject ProjectID, records map[BeadID]Record, record Record) error {
	switch record.Kind {
	case KindTask:
		if record.RevisionID != "" || record.Key != "" || record.Lifecycle != "" {
			return fmt.Errorf("%w: task %q contains memory-only state", ErrInvalid, record.ID)
		}
	case KindMemory:
		if record.RevisionID == "" || record.RevisionID == CurrentRevision {
			return fmt.Errorf("%w: memory %q needs an exact current revision", ErrInvalid, record.ID)
		}
		if record.Lifecycle != LifecycleActive && record.Lifecycle != LifecycleArchived {
			return fmt.Errorf("%w: memory %q has lifecycle %q", ErrInvalid, record.ID, record.Lifecycle)
		}
		if record.Author == "" {
			return fmt.Errorf("%w: memory %q has no revision author", ErrInvalid, record.ID)
		}
		switch record.Origin {
		case "native", "legacy_migration", "canonical_import":
		default:
			return fmt.Errorf("%w: memory %q has revision origin %q", ErrInvalid, record.ID, record.Origin)
		}
	default:
		return fmt.Errorf("%w: record %q has kind %q", ErrInvalid, record.ID, record.Kind)
	}
	for _, ref := range record.References {
		if err := validateReference(record.ID, record.Kind, sourceProject, records, ref); err != nil {
			return err
		}
	}
	return nil
}

func validateReference(sourceID BeadID, sourceKind Kind, sourceProject ProjectID, records map[BeadID]Record, ref Reference) error {
	if ref.BeadID == "" || ref.ExpectedScope == "" {
		return fmt.Errorf("%w: reference from %q lacks target identity or scope", ErrInvalid, sourceID)
	}
	if ref.ExpectedKind != KindTask && ref.ExpectedKind != KindMemory {
		return fmt.Errorf("%w: reference from %q has kind %q", ErrInvalid, sourceID, ref.ExpectedKind)
	}
	if ref.Local {
		if ref.ProjectID != "" {
			return fmt.Errorf("%w: local reference from %q is project-qualified", ErrInvalid, sourceID)
		}
		if sourceID == ref.BeadID {
			return fmt.Errorf("%w: self-reference %q", ErrInvalid, sourceID)
		}
		if target, exists := records[ref.BeadID]; exists && target.Kind != ref.ExpectedKind {
			return fmt.Errorf("%w: target %q kind does not match declaration", ErrInvalid, ref.BeadID)
		}
	} else {
		if ref.ProjectID == "" || ref.ProjectID == sourceProject {
			return fmt.Errorf("%w: foreign reference from %q needs a different project", ErrInvalid, sourceID)
		}
		if ref.ExpectedKind != KindMemory || ref.RevisionID == "" || ref.RevisionID == CurrentRevision {
			return fmt.Errorf("%w: foreign reference from %q must pin an exact memory revision", ErrInvalid, sourceID)
		}
	}
	if ref.ExpectedKind == KindTask && ref.RevisionID != "" {
		return fmt.Errorf("%w: a task target cannot carry a memory revision", ErrInvalid)
	}
	if sourceKind != KindMemory && ref.ExpectedKind != KindMemory {
		return fmt.Errorf("%w: a Bead Reference needs at least one memory endpoint", ErrInvalid)
	}
	return nil
}

func mapReferences(
	unit InterchangeUnit,
	records map[BeadID]Record,
	mapping map[BeadID]BeadID,
	newRevisions map[BeadID]RevisionID,
	record Record,
) ([]Reference, error) {
	out := make([]Reference, 0, len(record.References))
	for _, ref := range record.References {
		if !ref.Local {
			out = append(out, ref)
			continue
		}
		target, inUnit := records[ref.BeadID]
		if inUnit && ref.RevisionID != "" && ref.RevisionID != target.RevisionID {
			// A historical local pin remains an address in the source project.
			ref.Local = false
			ref.ProjectID = unit.SourceProjectID
			out = append(out, ref)
			continue
		}
		if inUnit {
			ref.BeadID = mapping[ref.BeadID]
			if ref.RevisionID != "" {
				mappedRevision := newRevisions[target.ID]
				if mappedRevision == "" {
					return nil, fmt.Errorf("%w: no mapped revision for %q", ErrInvalid, target.ID)
				}
				ref.RevisionID = mappedRevision
			}
			out = append(out, ref)
			continue
		}
		if ref.RevisionID == "" {
			return nil, fmt.Errorf("%w: floating source-local target %q is not created by this unit", ErrInvalid, ref.BeadID)
		}
		// An exact target outside the connected unit cannot be retargeted to a
		// same-looking destination record; qualify it back to its source.
		ref.Local = false
		ref.ProjectID = unit.SourceProjectID
		out = append(out, ref)
	}
	return out, nil
}

func cloneUnit(unit InterchangeUnit) InterchangeUnit {
	out := unit
	out.Declaration.RequiredCapabilities = append([]string(nil), unit.Declaration.RequiredCapabilities...)
	out.Records = cloneRecords(unit.Records)
	return out
}

func exactSubset(current, incoming map[BeadID]Record) bool {
	for id, record := range incoming {
		currentRecord, exists := current[id]
		if !exists || !reflect.DeepEqual(currentRecord, record) {
			return false
		}
	}
	return true
}

func cloneMapping(in map[BeadID]BeadID) map[BeadID]BeadID {
	out := make(map[BeadID]BeadID, len(in))
	for source, destination := range in {
		out[source] = destination
	}
	return out
}

func importedRecord(sourceProject ProjectID, source Record, destinationID BeadID, refs []Reference, revision RevisionID) Record {
	record := cloneRecord(source)
	record.ID = destinationID
	record.References = append([]Reference(nil), refs...)
	if record.Kind == KindMemory {
		record.RevisionID = revision
		record.Provenance = append(record.Provenance, RevisionEvidence{
			Address: Address{
				ProjectID:  sourceProject,
				BeadID:     source.ID,
				RevisionID: source.RevisionID,
			},
			Author:         source.Author,
			AssistingAgent: source.AssistingAgent,
			ChangeMessage:  source.ChangeMessage,
			Origin:         source.Origin,
		})
		record.Author = harnessBImportAuthor
		record.AssistingAgent = ""
		record.ChangeMessage = "Import B1 current state"
		record.Origin = "canonical_import"
	}
	return record
}

// a2InterchangeProvider adapts the existing append-only independent A2
// semantics. Each imported memory is staged in its own A2 provider; a single
// pointer-map swap publishes the connected graph only after all slots pass.
type a2InterchangeProvider struct {
	mu sync.Mutex

	projectID  ProjectID
	token      string
	generation uint64
	nextID     uint64
	memories   map[BeadID]a2MemorySlot
	tasks      map[BeadID]Record
	failNext   bool
}

type a2MemorySlot struct {
	module *a2.IndependentProvider
	beadID a2.BeadID
}

var _ InterchangeProvider = (*a2InterchangeProvider)(nil)

func NewA2InterchangeProvider(projectID ProjectID) InterchangeProvider {
	p := &a2InterchangeProvider{
		projectID: projectID,
		memories:  make(map[BeadID]a2MemorySlot),
		tasks:     make(map[BeadID]Record),
	}
	p.token = fmt.Sprintf("a2:%p", p)
	return p
}

func (p *a2InterchangeProvider) ProjectID() ProjectID { return p.projectID }

func (p *a2InterchangeProvider) PrepareImport(_ context.Context, unit InterchangeUnit) (ImportPlan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	current, err := p.snapshotLocked(context.Background())
	if err != nil {
		return ImportPlan{}, err
	}
	currentByID, _ := recordsByID(current)
	return prepareImport(p.token, p.projectID, p.generation, p.nextID, "a2-b-", currentByID, unit)
}

func (p *a2InterchangeProvider) ApplyImport(ctx context.Context, plan ImportPlan) (ImportResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if plan.providerToken != p.token {
		return ImportResult{}, fmt.Errorf("%w: plan belongs to another provider", ErrInvalid)
	}
	if plan.generation != p.generation {
		return ImportResult{}, ErrStaleDestination
	}
	if plan.noop {
		return ImportResult{Outcome: ImportUnchanged, Mapping: cloneMapping(plan.mapping)}, nil
	}
	incoming, _ := recordsByID(plan.unit.Records)
	stagedMemories := make(map[BeadID]a2MemorySlot, len(p.memories)+len(plan.unit.Records))
	for id, slot := range p.memories {
		stagedMemories[id] = slot
	}
	stagedTasks := make(map[BeadID]Record, len(p.tasks)+len(plan.unit.Records))
	for id, record := range p.tasks {
		stagedTasks[id] = cloneRecord(record)
	}

	newRevisions := make(map[BeadID]RevisionID)
	for _, source := range plan.unit.Records {
		if source.Kind == KindMemory {
			// A fresh per-memory A2 provider issues this only revision after the
			// final mapped request is validated. The value is confirmed below.
			newRevisions[source.ID] = RevisionID("ind-rev-1")
		}
	}
	applied := make([]Record, 0, len(plan.unit.Records))
	for _, source := range plan.unit.Records {
		refs, err := mapReferences(plan.unit, incoming, plan.mapping, newRevisions, source)
		if err != nil {
			return ImportResult{}, err
		}
		destinationID := plan.mapping[source.ID]
		if source.Kind == KindTask {
			record := importedRecord(plan.unit.SourceProjectID, source, destinationID, refs, "")
			stagedTasks[destinationID] = record
			applied = append(applied, record)
			continue
		}
		module := a2.NewIndependentProvider(a2.ProjectID(p.projectID))
		result, err := module.Mutate(ctx, a2.Mutation{
			Key:           source.Key,
			Title:         source.Title,
			Lifecycle:     a2.Lifecycle(source.Lifecycle),
			Body:          source.Body,
			References:    toA2References(refs),
			Author:        harnessBImportAuthor,
			ChangeMessage: "B1 current-state import",
			Origin:        a2.OriginImport,
			Provenance:    toA2Provenance(source, plan.unit.SourceProjectID),
		})
		if err != nil || result.Outcome != a2.OutcomeApplied || result.Revision == nil {
			return ImportResult{}, fmt.Errorf("stage A2 memory %q: outcome=%q: %w", source.ID, result.Outcome, err)
		}
		if got, want := RevisionID(result.Address.RevisionID), newRevisions[source.ID]; got != want {
			return ImportResult{}, fmt.Errorf("%w: A2 staged revision %q, planned mapped revision %q", ErrInvalid, got, want)
		}
		newRevisions[source.ID] = RevisionID(result.Address.RevisionID)
		record := importedRecord(plan.unit.SourceProjectID, source, destinationID, refs, RevisionID(result.Address.RevisionID))
		stagedMemories[destinationID] = a2MemorySlot{module: module, beadID: result.Address.BeadID}
		applied = append(applied, record)
	}
	if p.failNext {
		p.failNext = false
		return ImportResult{}, ErrInjectedFailure
	}
	p.memories = stagedMemories
	p.tasks = stagedTasks
	p.generation++
	p.nextID += uint64(len(plan.unit.Records))
	return ImportResult{Outcome: ImportApplied, Mapping: cloneMapping(plan.mapping), Records: cloneRecords(applied)}, nil
}

func (p *a2InterchangeProvider) Export(ctx context.Context) (InterchangeUnit, error) {
	records, err := p.Snapshot(ctx)
	if err != nil {
		return InterchangeUnit{}, err
	}
	return InterchangeUnit{Declaration: CanonicalDeclaration(), SourceProjectID: p.projectID, Records: records}, nil
}

func (p *a2InterchangeProvider) Snapshot(ctx context.Context) ([]Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshotLocked(ctx)
}

func (p *a2InterchangeProvider) snapshotLocked(ctx context.Context) ([]Record, error) {
	records := make(map[BeadID]Record, len(p.memories)+len(p.tasks))
	for id, task := range p.tasks {
		records[id] = cloneRecord(task)
	}
	for id, slot := range p.memories {
		revision, err := slot.module.Read(ctx, a2.ReadRequest{BeadID: slot.beadID})
		if err != nil {
			return nil, err
		}
		records[id] = Record{
			ID:             id,
			Kind:           KindMemory,
			RevisionID:     RevisionID(revision.Address.RevisionID),
			Key:            revision.Key,
			Title:          revision.Title,
			Body:           revision.Body,
			Lifecycle:      Lifecycle(revision.Lifecycle),
			References:     fromA2References(revision.References),
			Author:         revision.Author,
			AssistingAgent: revision.AssistingAgent,
			ChangeMessage:  revision.ChangeMessage,
			Origin:         fromA2Origin(revision.Origin),
			Provenance:     fromA2Provenance(revision.Provenance),
		}
	}
	return recordsFromMap(records), nil
}

func fromA2Origin(origin a2.RevisionOrigin) string {
	if origin == a2.OriginImport {
		return "canonical_import"
	}
	return string(origin)
}

func (p *a2InterchangeProvider) FailBeforePublishOnce() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failNext = true
}

func toA2References(refs []Reference) []a2.Reference {
	out := make([]a2.Reference, len(refs))
	for i, ref := range refs {
		out[i] = a2.Reference{Target: a2.Target{
			Local:         ref.Local,
			ProjectID:     a2.ProjectID(ref.ProjectID),
			BeadID:        a2.BeadID(ref.BeadID),
			ExpectedScope: ref.ExpectedScope,
			ExpectedKind:  a2.BeadKind(ref.ExpectedKind),
			RevisionID:    a2.RevisionID(ref.RevisionID),
		}}
	}
	return out
}

func fromA2References(refs []a2.Reference) []Reference {
	out := make([]Reference, len(refs))
	for i, ref := range refs {
		out[i] = Reference{
			Local:         ref.Target.Local,
			ProjectID:     ProjectID(ref.Target.ProjectID),
			BeadID:        BeadID(ref.Target.BeadID),
			RevisionID:    RevisionID(ref.Target.RevisionID),
			ExpectedScope: ref.Target.ExpectedScope,
			ExpectedKind:  Kind(ref.Target.ExpectedKind),
		}
	}
	return out
}

func toA2Provenance(source Record, sourceProject ProjectID) []a2.Provenance {
	out := make([]a2.Provenance, 0, len(source.Provenance)+1)
	for _, provenance := range source.Provenance {
		out = append(out, a2.Provenance{
			SourceProjectID:  a2.ProjectID(provenance.Address.ProjectID),
			SourceBeadID:     a2.BeadID(provenance.Address.BeadID),
			SourceRevisionID: a2.RevisionID(provenance.Address.RevisionID),
			Evidence:         encodeRevisionEvidence(provenance),
		})
	}
	out = append(out, a2.Provenance{
		SourceProjectID:  a2.ProjectID(sourceProject),
		SourceBeadID:     a2.BeadID(source.ID),
		SourceRevisionID: a2.RevisionID(source.RevisionID),
		Evidence: encodeRevisionEvidence(RevisionEvidence{
			Address: Address{ProjectID: sourceProject, BeadID: source.ID, RevisionID: source.RevisionID},
			Author:  source.Author, AssistingAgent: source.AssistingAgent,
			ChangeMessage: source.ChangeMessage, Origin: source.Origin,
		}),
	})
	return out
}

func fromA2Provenance(in []a2.Provenance) []RevisionEvidence {
	out := make([]RevisionEvidence, len(in))
	for i, provenance := range in {
		out[i] = decodeRevisionEvidence(provenance.Evidence)
		out[i].Address = Address{
			ProjectID: ProjectID(provenance.SourceProjectID), BeadID: BeadID(provenance.SourceBeadID),
			RevisionID: RevisionID(provenance.SourceRevisionID),
		}
	}
	return out
}

func encodeRevisionEvidence(evidence RevisionEvidence) string {
	encoded, _ := json.Marshal(evidence)
	return string(encoded)
}

func decodeRevisionEvidence(encoded string) RevisionEvidence {
	var evidence RevisionEvidence
	_ = json.Unmarshal([]byte(encoded), &evidence)
	return evidence
}

// documentProvider is materially different from A2: the whole project is one
// transactional JSON document, and publication is an atomic file replacement.
type documentProvider struct {
	mu sync.Mutex

	projectID ProjectID
	path      string
	token     string
	failNext  bool
}

type projectDocument struct {
	ProjectID  ProjectID         `json:"project_id"`
	Generation uint64            `json:"generation"`
	Sequence   uint64            `json:"sequence"`
	Records    map[BeadID]Record `json:"records"`
}

var _ InterchangeProvider = (*documentProvider)(nil)

func NewDocumentInterchangeProvider(projectID ProjectID, path string) (InterchangeProvider, error) {
	p := &documentProvider{projectID: projectID, path: path, token: "document:" + path}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := p.writeDocument(projectDocument{ProjectID: projectID, Records: make(map[BeadID]Record)}); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else if _, err := p.readDocument(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *documentProvider) ProjectID() ProjectID { return p.projectID }

func (p *documentProvider) PrepareImport(_ context.Context, unit InterchangeUnit) (ImportPlan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	document, err := p.readDocument()
	if err != nil {
		return ImportPlan{}, err
	}
	return prepareImport(p.token, p.projectID, document.Generation, document.Sequence, "doc-b-", document.Records, unit)
}

func (p *documentProvider) ApplyImport(_ context.Context, plan ImportPlan) (ImportResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	lock, err := os.OpenFile(p.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return ImportResult{}, err
	}
	defer lock.Close()
	if err := lockfile.FlockExclusiveBlocking(lock); err != nil {
		return ImportResult{}, err
	}
	defer func() { _ = lockfile.FlockUnlock(lock) }()
	if plan.providerToken != p.token {
		return ImportResult{}, fmt.Errorf("%w: plan belongs to another provider", ErrInvalid)
	}
	document, err := p.readDocument()
	if err != nil {
		return ImportResult{}, err
	}
	if plan.generation != document.Generation {
		return ImportResult{}, ErrStaleDestination
	}
	if plan.noop {
		return ImportResult{Outcome: ImportUnchanged, Mapping: cloneMapping(plan.mapping)}, nil
	}
	incoming, _ := recordsByID(plan.unit.Records)
	staged := projectDocument{
		ProjectID:  p.projectID,
		Generation: document.Generation + 1,
		Sequence:   document.Sequence,
		Records:    make(map[BeadID]Record, len(document.Records)+len(plan.unit.Records)),
	}
	for id, record := range document.Records {
		staged.Records[id] = cloneRecord(record)
	}
	newRevisions := make(map[BeadID]RevisionID)
	for _, source := range plan.unit.Records {
		if source.Kind != KindMemory {
			continue
		}
		staged.Sequence++
		newRevisions[source.ID] = RevisionID(fmt.Sprintf("doc-rev-%d", staged.Sequence))
	}
	applied := make([]Record, 0, len(plan.unit.Records))
	for _, source := range plan.unit.Records {
		refs, err := mapReferences(plan.unit, incoming, plan.mapping, newRevisions, source)
		if err != nil {
			return ImportResult{}, err
		}
		record := importedRecord(plan.unit.SourceProjectID, source, plan.mapping[source.ID], refs, newRevisions[source.ID])
		staged.Records[record.ID] = record
		applied = append(applied, record)
	}
	if p.failNext {
		p.failNext = false
		return ImportResult{}, ErrInjectedFailure
	}
	if err := p.writeDocument(staged); err != nil {
		return ImportResult{}, err
	}
	return ImportResult{Outcome: ImportApplied, Mapping: cloneMapping(plan.mapping), Records: cloneRecords(applied)}, nil
}

func (p *documentProvider) Export(_ context.Context) (InterchangeUnit, error) {
	records, err := p.Snapshot(context.Background())
	if err != nil {
		return InterchangeUnit{}, err
	}
	return InterchangeUnit{Declaration: CanonicalDeclaration(), SourceProjectID: p.projectID, Records: records}, nil
}

func (p *documentProvider) Snapshot(_ context.Context) ([]Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	document, err := p.readDocument()
	if err != nil {
		return nil, err
	}
	return recordsFromMap(document.Records), nil
}

func (p *documentProvider) FailBeforePublishOnce() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failNext = true
}

func (p *documentProvider) readDocument() (projectDocument, error) {
	data, err := os.ReadFile(p.path)
	if err != nil {
		return projectDocument{}, err
	}
	var document projectDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return projectDocument{}, err
	}
	if document.ProjectID == "" || document.ProjectID != p.projectID {
		return projectDocument{}, fmt.Errorf("%w: document project %q does not match provider %q", ErrInvalid, document.ProjectID, p.projectID)
	}
	if document.Records == nil {
		document.Records = make(map[BeadID]Record)
	}
	return document, nil
}

func (p *documentProvider) writeDocument(document projectDocument) error {
	if document.ProjectID != p.projectID {
		return fmt.Errorf("%w: refusing to publish document for project %q through provider %q", ErrInvalid, document.ProjectID, p.projectID)
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(p.path), ".memory-beads-b1-*.json")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, p.path)
}

func sortedCapabilities(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func semanticGraph(records []Record) []string {
	// This comparison helper intentionally ignores destination identity,
	// revision spelling, and provenance while retaining the graph's meaning.
	labels := make(map[BeadID]string, len(records))
	for _, record := range records {
		label := string(record.Kind) + ":" + record.Title
		if record.Key != "" {
			label += ":" + record.Key
		}
		labels[record.ID] = label
	}
	out := make([]string, 0, len(records))
	for _, record := range records {
		parts := []string{labels[record.ID], string(record.Lifecycle), record.Body}
		for _, ref := range record.References {
			target := labels[ref.BeadID]
			if target == "" {
				target = fmt.Sprintf("%s/%s", ref.ProjectID, ref.BeadID)
			}
			pin := "floating"
			if ref.RevisionID != "" {
				pin = "exact"
			}
			parts = append(parts, fmt.Sprintf("ref:%t:%s:%s:%s:%s", ref.Local, target, ref.ExpectedScope, ref.ExpectedKind, pin))
		}
		sort.Strings(parts[3:])
		out = append(out, strings.Join(parts, "|"))
	}
	sort.Strings(out)
	return out
}

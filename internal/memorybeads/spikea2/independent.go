package spikea2

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// IndependentProvider is intentionally not a wrapper around either Beads
// storage route. It keeps immutable revisions in an append-only catalog and
// branch views as sets of heads, giving the contract a materially different
// implementation to vote against.
type IndependentProvider struct {
	mu sync.Mutex

	projectID ProjectID
	instance  string
	active    string
	branches  map[string]map[BeadID][]RevisionID
	revisions map[BeadID]map[RevisionID]*storedRevision

	beadSeq     uint64
	revisionSeq uint64
	cursorSeq   uint64
	cursors     map[Continuation]cursorRecord

	nextFault           FaultPoint
	publicationAttempts int
	lastPrepared        Address
}

type storedRevision struct {
	revision   Revision
	sequence   uint64
	lineBlame  []RevisionID
	fieldBlame map[string]RevisionID
}

type cursorRecord struct {
	kind      string
	signature string
	offset    int
	history   []RevisionSummary
	search    []SearchSummary
	refs      []Reference
}

func NewIndependentProvider(projectID ProjectID) *IndependentProvider {
	return &IndependentProvider{
		projectID: projectID,
		instance:  newProviderInstanceID(),
		active:    "main",
		branches: map[string]map[BeadID][]RevisionID{
			"main": {},
		},
		revisions: make(map[BeadID]map[RevisionID]*storedRevision),
		cursors:   make(map[Continuation]cursorRecord),
	}
}

func newProviderInstanceID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(fmt.Sprintf("create independent provider identity: %v", err))
	}
	return hex.EncodeToString(value[:])
}

var _ Module = (*IndependentProvider)(nil)

func (p *IndependentProvider) Mutate(ctx context.Context, req Mutation) (MutationResult, error) {
	if err := ctx.Err(); err != nil {
		return MutationResult{Outcome: OutcomeFailed}, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.projectID == "" {
		return rejected(fmt.Errorf("%w: project ID is required", ErrInvalid))
	}
	if req.Author == "" {
		return rejected(fmt.Errorf("%w: author is required", ErrInvalid))
	}
	if req.Lifecycle == "" {
		req.Lifecycle = LifecycleActive
	}
	if req.Lifecycle != LifecycleActive && req.Lifecycle != LifecycleArchived {
		return rejected(fmt.Errorf("%w: unknown lifecycle %q", ErrInvalid, req.Lifecycle))
	}
	if req.Origin == "" {
		req.Origin = OriginNative
	}
	if req.Origin != OriginNative && req.Origin != OriginLegacyMigration && req.Origin != OriginImport {
		return rejected(fmt.Errorf("%w: unknown revision origin %q", ErrInvalid, req.Origin))
	}
	aliases, err := normalizeAliases(req.Aliases)
	if err != nil {
		return rejected(err)
	}

	beadID := req.BeadID
	creating := beadID == ""
	if creating {
		if req.ExpectedCurrent != "" {
			return rejected(fmt.Errorf("%w: a create cannot name an expected current revision", ErrInvalid))
		}
		p.beadSeq++
		beadID = BeadID("ind-bead-" + strconv.FormatUint(p.beadSeq, 10))
	}

	refs, err := normalizeReferences(p.projectID, beadID, req.References)
	if err != nil {
		return rejected(err)
	}

	var parent *storedRevision
	if !creating {
		heads := p.branches[p.active][beadID]
		switch len(heads) {
		case 0:
			return rejected(fmt.Errorf("%w: bead %q", ErrNotFound, beadID))
		case 1:
			parent = p.revisions[beadID][heads[0]]
		default:
			return rejected(newConflictError(beadID, heads))
		}
		if req.ExpectedCurrent == "" {
			return rejected(fmt.Errorf("%w: an edit requires expected current revision", ErrInvalid))
		}
		if req.ExpectedCurrent != parent.revision.Address.RevisionID {
			return rejected(&StaleError{Expected: req.ExpectedCurrent, Actual: parent.revision.Address.RevisionID})
		}
		if mutationMatchesRevision(req, aliases, refs, parent.revision) {
			rev := cloneRevision(parent.revision)
			return MutationResult{Outcome: OutcomeUnchanged, Address: rev.Address, Revision: &rev}, nil
		}
	}

	p.revisionSeq++
	revisionID := RevisionID("ind-rev-" + strconv.FormatUint(p.revisionSeq, 10))
	address := Address{ProjectID: p.projectID, BeadID: beadID, RevisionID: revisionID}
	p.lastPrepared = address

	entry := &storedRevision{
		revision: Revision{
			Address:        address,
			Key:            req.Key,
			Aliases:        append([]string(nil), aliases...),
			Title:          req.Title,
			Lifecycle:      req.Lifecycle,
			Body:           req.Body,
			References:     cloneReferences(refs),
			Author:         req.Author,
			AssistingAgent: req.AssistingAgent,
			ChangeMessage:  req.ChangeMessage,
			Origin:         req.Origin,
			Provenance:     cloneProvenance(req.Provenance),
			CreatedAt:      time.Unix(int64(p.revisionSeq), 0).UTC(),
		},
		sequence:   p.revisionSeq,
		fieldBlame: make(map[string]RevisionID),
	}
	if parent != nil {
		entry.revision.Parents = []RevisionID{parent.revision.Address.RevisionID}
	}
	entry.lineBlame = attributeLines(parent, req.Body, revisionID)
	for _, field := range semanticFields() {
		entry.fieldBlame[field] = revisionID
	}
	if parent != nil {
		for _, field := range semanticFields() {
			if semanticFieldEqual(field, parent.revision, entry.revision) {
				entry.fieldBlame[field] = parent.fieldBlame[field]
			}
		}
	}

	observation, publishErr := p.publishLocked(beadID, revisionID, entry)
	switch observation {
	case publicationFailed:
		return MutationResult{Outcome: OutcomeFailed}, publishErr
	case publicationKnownUnverified:
		return MutationResult{
			Outcome: OutcomeAppliedUnverified,
			Address: address,
			Detail:  "publication is known; complete result verification was injected to fail",
		}, nil
	case publicationUnknown:
		return MutationResult{
			Outcome: OutcomeIndeterminate,
			Detail:  "publication acknowledgement was lost; whether it landed is unknown",
		}, nil
	}

	rev := cloneRevision(entry.revision)
	return MutationResult{Outcome: OutcomeApplied, Address: address, Revision: &rev}, nil
}

type publicationObservation int

const (
	publicationVerified publicationObservation = iota
	publicationKnownUnverified
	publicationUnknown
	publicationFailed
)

// publishLocked is the canonical authority behind the provider-facing
// adapter. Both acknowledgement-loss faults return the same observation to
// the adapter even though the hidden authority makes a different decision.
func (p *IndependentProvider) publishLocked(beadID BeadID, revisionID RevisionID, entry *storedRevision) (publicationObservation, error) {
	p.publicationAttempts++
	fault := p.nextFault
	p.nextFault = FaultNone
	if fault == FaultBeforePublication {
		return publicationFailed, ErrInjectedFailure
	}
	if fault == FaultIndeterminateBefore {
		return publicationUnknown, nil
	}

	if p.revisions[beadID] == nil {
		p.revisions[beadID] = make(map[RevisionID]*storedRevision)
	}
	// The append and head move are the authority's publication unit. The
	// provider-facing adapter observes only the result category below.
	p.revisions[beadID][revisionID] = entry
	p.branches[p.active][beadID] = []RevisionID{revisionID}

	switch fault {
	case FaultAfterKnownPublication:
		return publicationKnownUnverified, nil
	case FaultIndeterminateAfter:
		return publicationUnknown, nil
	default:
		return publicationVerified, nil
	}
}

func rejected(err error) (MutationResult, error) {
	return MutationResult{Outcome: OutcomeRejected}, err
}

func (p *IndependentProvider) Read(ctx context.Context, req ReadRequest) (Revision, error) {
	if err := ctx.Err(); err != nil {
		return Revision{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, err := p.selectRevisionLocked(req.BeadID, req.RevisionID)
	if err != nil {
		return Revision{}, err
	}
	return cloneRevision(entry.revision), nil
}

func (p *IndependentProvider) selectRevisionLocked(beadID BeadID, revisionID RevisionID) (*storedRevision, error) {
	if beadID == "" {
		return nil, fmt.Errorf("%w: bead ID is required", ErrInvalid)
	}
	if revisionID == "" {
		heads := p.branches[p.active][beadID]
		switch len(heads) {
		case 0:
			return nil, fmt.Errorf("%w: bead %q", ErrNotFound, beadID)
		case 1:
			revisionID = heads[0]
		default:
			return nil, newConflictError(beadID, heads)
		}
	}
	entry := p.revisions[beadID][revisionID]
	if entry == nil {
		return nil, fmt.Errorf("%w: bead %q revision %q", ErrNotFound, beadID, revisionID)
	}
	return entry, nil
}

func (p *IndependentProvider) History(ctx context.Context, req HistoryRequest) (HistoryPage, error) {
	if err := ctx.Err(); err != nil {
		return HistoryPage{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	signature := p.cursorSignature("history|" + string(req.BeadID) + "|" + strconv.Itoa(req.Limit))
	if req.Continuation != "" {
		record, err := p.continuationLocked(req.Continuation, "history", signature)
		if err != nil {
			return HistoryPage{}, err
		}
		return p.historyPageLocked(record, req.Limit), nil
	}

	byID := p.revisions[req.BeadID]
	if len(byID) == 0 {
		return HistoryPage{}, fmt.Errorf("%w: bead %q", ErrNotFound, req.BeadID)
	}
	entries := make([]*storedRevision, 0, len(byID))
	for _, entry := range byID {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].sequence > entries[j].sequence })
	items := make([]RevisionSummary, 0, len(entries))
	for _, entry := range entries {
		rev := entry.revision
		items = append(items, RevisionSummary{
			Address:        rev.Address,
			Parents:        append([]RevisionID(nil), rev.Parents...),
			Key:            rev.Key,
			Title:          rev.Title,
			Lifecycle:      rev.Lifecycle,
			Author:         rev.Author,
			AssistingAgent: rev.AssistingAgent,
			ChangeMessage:  rev.ChangeMessage,
			Origin:         rev.Origin,
			Provenance:     cloneProvenance(rev.Provenance),
			CreatedAt:      rev.CreatedAt,
		})
	}
	return p.historyPageLocked(cursorRecord{kind: "history", signature: signature, history: items}, req.Limit), nil
}

func (p *IndependentProvider) historyPageLocked(record cursorRecord, limit int) HistoryPage {
	start, end := pageBounds(record.offset, limit, len(record.history))
	page := HistoryPage{Revisions: cloneRevisionSummaries(record.history[start:end]), Complete: end == len(record.history)}
	if !page.Complete {
		record.offset = end
		page.Continuation = p.saveCursorLocked(record)
	}
	return page
}

func (p *IndependentProvider) Search(ctx context.Context, req SearchRequest) (SearchPage, error) {
	if err := ctx.Err(); err != nil {
		return SearchPage{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	signature := p.cursorSignature("search|" + p.active + "|" + strings.ToLower(req.Query) + "|" + strconv.FormatBool(req.IncludeArchived) + "|" + strconv.Itoa(req.Limit))
	if req.Continuation != "" {
		record, err := p.continuationLocked(req.Continuation, "search", signature)
		if err != nil {
			return SearchPage{}, err
		}
		return p.searchPageLocked(record, req.Limit), nil
	}

	query := strings.ToLower(req.Query)
	items := make([]SearchSummary, 0)
	beadIDs := make([]BeadID, 0, len(p.branches[p.active]))
	for beadID := range p.branches[p.active] {
		beadIDs = append(beadIDs, beadID)
	}
	sort.Slice(beadIDs, func(i, j int) bool { return beadIDs[i] < beadIDs[j] })
	for _, beadID := range beadIDs {
		heads := p.branches[p.active][beadID]
		if len(heads) > 1 {
			return SearchPage{}, newConflictError(beadID, heads)
		}
		if len(heads) == 0 {
			continue
		}
		rev := p.revisions[beadID][heads[0]].revision
		if rev.Lifecycle == LifecycleArchived && !req.IncludeArchived {
			continue
		}
		if query != "" && !strings.Contains(searchableText(rev), query) {
			continue
		}
		items = append(items, SearchSummary{
			ProjectID:       p.projectID,
			BeadID:          beadID,
			CurrentRevision: rev.Address.RevisionID,
			Key:             rev.Key,
			Title:           rev.Title,
			Lifecycle:       rev.Lifecycle,
			Excerpt:         excerpt(rev.Body, 48),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].BeadID < items[j].BeadID })
	return p.searchPageLocked(cursorRecord{kind: "search", signature: signature, search: items}, req.Limit), nil
}

func (p *IndependentProvider) searchPageLocked(record cursorRecord, limit int) SearchPage {
	start, end := pageBounds(record.offset, limit, len(record.search))
	page := SearchPage{Memories: append([]SearchSummary(nil), record.search[start:end]...), Complete: end == len(record.search)}
	if !page.Complete {
		record.offset = end
		page.Continuation = p.saveCursorLocked(record)
	}
	return page
}

func (p *IndependentProvider) References(ctx context.Context, req ReferencesRequest) (ReferencePage, error) {
	if err := ctx.Err(); err != nil {
		return ReferencePage{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	signature := p.cursorSignature("refs|" + string(req.BeadID) + "|" + string(req.RevisionID) + "|" + strconv.Itoa(req.Limit))
	if req.Continuation != "" {
		record, err := p.continuationLocked(req.Continuation, "refs", signature)
		if err != nil {
			return ReferencePage{}, err
		}
		return p.referencePageLocked(record, req.Limit), nil
	}
	if req.RevisionID == "" {
		return ReferencePage{}, fmt.Errorf("%w: outgoing reference traversal requires an exact source revision", ErrInvalid)
	}
	entry, err := p.selectRevisionLocked(req.BeadID, req.RevisionID)
	if err != nil {
		return ReferencePage{}, err
	}
	items := cloneReferences(entry.revision.References)
	return p.referencePageLocked(cursorRecord{kind: "refs", signature: signature, refs: items}, req.Limit), nil
}

func (p *IndependentProvider) referencePageLocked(record cursorRecord, limit int) ReferencePage {
	start, end := pageBounds(record.offset, limit, len(record.refs))
	page := ReferencePage{References: cloneReferences(record.refs[start:end]), Complete: end == len(record.refs)}
	if !page.Complete {
		record.offset = end
		page.Continuation = p.saveCursorLocked(record)
	}
	return page
}

func (p *IndependentProvider) Diff(ctx context.Context, req DiffRequest) (DiffResult, error) {
	if err := ctx.Err(); err != nil {
		return DiffResult{}, err
	}
	if req.From == "" || req.To == "" {
		return DiffResult{}, fmt.Errorf("%w: diff requires two exact revision IDs", ErrInvalid)
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	from, err := p.selectRevisionLocked(req.BeadID, req.From)
	if err != nil {
		return DiffResult{}, err
	}
	to, err := p.selectRevisionLocked(req.BeadID, req.To)
	if err != nil {
		return DiffResult{}, err
	}
	result := DiffResult{From: from.revision.Address, To: to.revision.Address}
	appendFieldChange(&result, "key", from.revision.Key, to.revision.Key)
	appendFieldChange(&result, "aliases", append([]string(nil), from.revision.Aliases...), append([]string(nil), to.revision.Aliases...))
	appendFieldChange(&result, "title", from.revision.Title, to.revision.Title)
	appendFieldChange(&result, "body", from.revision.Body, to.revision.Body)
	appendFieldChange(&result, "lifecycle", from.revision.Lifecycle, to.revision.Lifecycle)
	appendFieldChange(&result, "author", from.revision.Author, to.revision.Author)
	appendFieldChange(&result, "assisting_agent", from.revision.AssistingAgent, to.revision.AssistingAgent)
	appendFieldChange(&result, "change_message", from.revision.ChangeMessage, to.revision.ChangeMessage)
	appendFieldChange(&result, "origin", from.revision.Origin, to.revision.Origin)
	appendFieldChange(&result, "provenance", cloneProvenance(from.revision.Provenance), cloneProvenance(to.revision.Provenance))
	result.ReferencesAdded, result.ReferencesRemoved = referenceSetDiff(from.revision.References, to.revision.References)
	return result, nil
}

func appendFieldChange(result *DiffResult, field string, before, after any) {
	if !reflect.DeepEqual(before, after) {
		result.Fields = append(result.Fields, FieldChange{Field: field, Before: before, After: after})
	}
}

func (p *IndependentProvider) Blame(ctx context.Context, req BlameRequest) (BlameResult, error) {
	if err := ctx.Err(); err != nil {
		return BlameResult{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	entry, err := p.selectRevisionLocked(req.BeadID, req.RevisionID)
	if err != nil {
		return BlameResult{}, err
	}
	lines := bodyLines(entry.revision.Body)
	result := BlameResult{Address: entry.revision.Address, Lines: make([]LineAttribution, len(lines))}
	for i := range lines {
		result.Lines[i] = LineAttribution{Line: lines[i], RevisionID: entry.lineBlame[i]}
	}
	for _, field := range semanticFields() {
		result.Fields = append(result.Fields, FieldAttribution{Field: field, RevisionID: entry.fieldBlame[field]})
	}
	return result, nil
}

func (p *IndependentProvider) saveCursorLocked(record cursorRecord) Continuation {
	p.cursorSeq++
	token := Continuation("ind-continuation-" + p.instance + "-" + strconv.FormatUint(p.cursorSeq, 10))
	p.cursors[token] = record
	return token
}

func (p *IndependentProvider) cursorSignature(request string) string {
	return p.instance + "|" + string(p.projectID) + "|" + request
}

func (p *IndependentProvider) continuationLocked(token Continuation, kind, signature string) (cursorRecord, error) {
	record, ok := p.cursors[token]
	if !ok || record.kind != kind || record.signature != signature {
		return cursorRecord{}, ErrInvalidContinuation
	}
	return record, nil
}

func pageBounds(offset, limit, length int) (int, int) {
	if offset > length {
		offset = length
	}
	if limit <= 0 || offset+limit > length {
		return offset, length
	}
	return offset, offset + limit
}

// Fork, Checkout, Merge, DeleteBranch, Maintain, and the inspection/fault
// methods below form the test-only provider control plane used by conformance.

func (p *IndependentProvider) Fork(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if name == "" {
		return errors.New("branch name is required")
	}
	if _, exists := p.branches[name]; exists {
		return fmt.Errorf("branch %q already exists", name)
	}
	p.branches[name] = cloneHeads(p.branches[p.active])
	return nil
}

func (p *IndependentProvider) Checkout(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.branches[name]; !exists {
		return fmt.Errorf("branch %q not found", name)
	}
	p.active = name
	return nil
}

func (p *IndependentProvider) Merge(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	incoming, exists := p.branches[name]
	if !exists {
		return fmt.Errorf("branch %q not found", name)
	}
	current := p.branches[p.active]
	ids := make(map[BeadID]struct{}, len(current)+len(incoming))
	for id := range current {
		ids[id] = struct{}{}
	}
	for id := range incoming {
		ids[id] = struct{}{}
	}
	for id := range ids {
		current[id] = p.reduceHeadsLocked(id, append(append([]RevisionID(nil), current[id]...), incoming[id]...))
	}
	return nil
}

func (p *IndependentProvider) reduceHeadsLocked(beadID BeadID, heads []RevisionID) []RevisionID {
	set := make(map[RevisionID]struct{}, len(heads))
	for _, head := range heads {
		if head != "" {
			set[head] = struct{}{}
		}
	}
	result := make([]RevisionID, 0, len(set))
	for candidate := range set {
		isAncestor := false
		for other := range set {
			if candidate != other && p.isAncestorLocked(beadID, candidate, other) {
				isAncestor = true
				break
			}
		}
		if !isAncestor {
			result = append(result, candidate)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (p *IndependentProvider) isAncestorLocked(beadID BeadID, ancestor, descendant RevisionID) bool {
	seen := make(map[RevisionID]bool)
	stack := []RevisionID{descendant}
	for len(stack) > 0 {
		last := len(stack) - 1
		candidate := stack[last]
		stack = stack[:last]
		if candidate == ancestor {
			return true
		}
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		if entry := p.revisions[beadID][candidate]; entry != nil {
			stack = append(stack, entry.revision.Parents...)
		}
	}
	return false
}

func (p *IndependentProvider) DeleteBranch(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if name == p.active {
		return fmt.Errorf("cannot delete active branch %q", name)
	}
	if _, exists := p.branches[name]; !exists {
		return fmt.Errorf("branch %q not found", name)
	}
	delete(p.branches, name)
	// Revisions are intentionally not removed. Branch heads are views over the
	// append-only catalog, not ownership of historical state.
	return nil
}

func (p *IndependentProvider) Maintain() {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Rebuild every immutable entry into fresh allocations. This is enough to
	// catch cursors or exact reads that accidentally retained object identity
	// instead of revision identity.
	for beadID, revisions := range p.revisions {
		rebuilt := make(map[RevisionID]*storedRevision, len(revisions))
		for id, entry := range revisions {
			rebuilt[id] = &storedRevision{
				revision:   cloneRevision(entry.revision),
				sequence:   entry.sequence,
				lineBlame:  append([]RevisionID(nil), entry.lineBlame...),
				fieldBlame: cloneFieldBlame(entry.fieldBlame),
			}
		}
		p.revisions[beadID] = rebuilt
	}
}

func (p *IndependentProvider) FailNext(point FaultPoint) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextFault = point
}

func (p *IndependentProvider) RevisionIDs(beadID BeadID) []RevisionID {
	p.mu.Lock()
	defer p.mu.Unlock()
	entries := make([]*storedRevision, 0, len(p.revisions[beadID]))
	for _, entry := range p.revisions[beadID] {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].sequence < entries[j].sequence })
	result := make([]RevisionID, len(entries))
	for i, entry := range entries {
		result[i] = entry.revision.Address.RevisionID
	}
	return result
}

func (p *IndependentProvider) LastPrepared() Address {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastPrepared
}

func (p *IndependentProvider) PublicationAttempts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.publicationAttempts
}

func normalizeAliases(aliases []string) ([]string, error) {
	result := append([]string(nil), aliases...)
	for _, alias := range result {
		if alias == "" {
			return nil, fmt.Errorf("%w: an alias cannot be empty", ErrInvalid)
		}
	}
	sort.Strings(result)
	write := 0
	for _, alias := range result {
		if write != 0 && result[write-1] == alias {
			continue
		}
		result[write] = alias
		write++
	}
	return result[:write], nil
}

func mutationMatchesRevision(req Mutation, aliases []string, refs []Reference, revision Revision) bool {
	return req.Key == revision.Key &&
		reflect.DeepEqual(aliases, revision.Aliases) &&
		req.Title == revision.Title &&
		req.Lifecycle == revision.Lifecycle &&
		req.Body == revision.Body &&
		referencesEqual(refs, revision.References)
}

func semanticFields() []string {
	return []string{"key", "aliases", "title", "lifecycle", "outgoing_references"}
}

func semanticFieldEqual(field string, left, right Revision) bool {
	switch field {
	case "key":
		return left.Key == right.Key
	case "aliases":
		return reflect.DeepEqual(left.Aliases, right.Aliases)
	case "title":
		return left.Title == right.Title
	case "lifecycle":
		return left.Lifecycle == right.Lifecycle
	case "outgoing_references":
		return referencesEqual(left.References, right.References)
	default:
		return false
	}
}

func searchableText(revision Revision) string {
	return strings.ToLower(strings.Join([]string{
		revision.Key,
		strings.Join(revision.Aliases, "\n"),
		revision.Title,
		revision.Body,
	}, "\n"))
}

func normalizeReferences(projectID ProjectID, source BeadID, refs []Reference) ([]Reference, error) {
	result := make([]Reference, 0, len(refs))
	byLocator := make(map[string]int, len(refs))
	for _, ref := range refs {
		target := ref.Target
		if target.BeadID == "" || target.ExpectedScope == "" {
			return nil, fmt.Errorf("%w: reference target, scope, and kind are required", ErrInvalid)
		}
		if target.ExpectedKind != BeadKindTask && target.ExpectedKind != BeadKindMemory {
			return nil, fmt.Errorf("%w: reference target kind %q", ErrInvalid, target.ExpectedKind)
		}
		if target.Local && target.ProjectID != "" {
			return nil, fmt.Errorf("%w: a source-local target cannot carry a foreign project ID", ErrInvalid)
		}
		if !target.Local && target.ProjectID == "" {
			return nil, fmt.Errorf("%w: a foreign target requires a project ID", ErrInvalid)
		}
		if !target.Local && (target.ExpectedKind != BeadKindMemory || target.RevisionID == "") {
			return nil, fmt.Errorf("%w: a foreign target must identify an exact memory revision", ErrInvalid)
		}
		if target.ExpectedKind == BeadKindTask && target.RevisionID != "" {
			return nil, fmt.Errorf("%w: a task target cannot carry a memory revision pin", ErrInvalid)
		}
		if target.BeadID == source && (target.Local || target.ProjectID == projectID) {
			return nil, fmt.Errorf("%w: a memory cannot reference itself", ErrInvalid)
		}
		locator := referenceLocatorKey(ref)
		if index, exists := byLocator[locator]; exists {
			// A pin or expected-target change updates the one edge identified by
			// this locator; it does not create a parallel edge.
			result[index] = ref
			continue
		}
		byLocator[locator] = len(result)
		result = append(result, ref)
	}
	sort.Slice(result, func(i, j int) bool { return referenceLocatorKey(result[i]) < referenceLocatorKey(result[j]) })
	return result, nil
}

func referenceLocatorKey(ref Reference) string {
	t := ref.Target
	return strings.Join([]string{
		strconv.FormatBool(t.Local), string(t.ProjectID), string(t.BeadID),
	}, "\x00")
}

func referenceKey(ref Reference) string {
	t := ref.Target
	return strings.Join([]string{
		strconv.FormatBool(t.Local), string(t.ProjectID), string(t.BeadID),
		t.ExpectedScope, string(t.ExpectedKind), string(t.RevisionID),
	}, "\x00")
}

func referencesEqual(a, b []Reference) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if referenceKey(a[i]) != referenceKey(b[i]) {
			return false
		}
	}
	return true
}

func referenceSetDiff(from, to []Reference) (added, removed []Reference) {
	fromSet := make(map[string]Reference, len(from))
	toSet := make(map[string]Reference, len(to))
	for _, ref := range from {
		fromSet[referenceKey(ref)] = ref
	}
	for _, ref := range to {
		toSet[referenceKey(ref)] = ref
	}
	for key, ref := range toSet {
		if _, ok := fromSet[key]; !ok {
			added = append(added, ref)
		}
	}
	for key, ref := range fromSet {
		if _, ok := toSet[key]; !ok {
			removed = append(removed, ref)
		}
	}
	sort.Slice(added, func(i, j int) bool { return referenceKey(added[i]) < referenceKey(added[j]) })
	sort.Slice(removed, func(i, j int) bool { return referenceKey(removed[i]) < referenceKey(removed[j]) })
	return added, removed
}

func attributeLines(parent *storedRevision, body string, revisionID RevisionID) []RevisionID {
	lines := bodyLines(body)
	result := make([]RevisionID, len(lines))
	for i := range result {
		result[i] = revisionID
	}
	if parent == nil {
		return result
	}
	old := bodyLines(parent.revision.Body)
	for _, pair := range lcsPairs(old, lines) {
		result[pair[1]] = parent.lineBlame[pair[0]]
	}
	return result
}

func bodyLines(body string) []string {
	if body == "" {
		return nil
	}
	return strings.Split(body, "\n")
}

// lcsPairs returns index pairs for one deterministic longest common
// subsequence. Blame semantics are observable; this algorithm is not.
func lcsPairs(a, b []string) [][2]int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var pairs [][2]int
	for i, j := 0, 0; i < len(a) && j < len(b); {
		switch {
		case a[i] == b[j]:
			pairs = append(pairs, [2]int{i, j})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}
	return pairs
}

func cloneRevision(rev Revision) Revision {
	rev.Parents = append([]RevisionID(nil), rev.Parents...)
	rev.Aliases = append([]string(nil), rev.Aliases...)
	rev.References = cloneReferences(rev.References)
	rev.Provenance = cloneProvenance(rev.Provenance)
	return rev
}

func cloneReferences(refs []Reference) []Reference {
	return append([]Reference(nil), refs...)
}

func cloneProvenance(provenance []Provenance) []Provenance {
	return append([]Provenance(nil), provenance...)
}

func cloneRevisionSummaries(summaries []RevisionSummary) []RevisionSummary {
	result := append([]RevisionSummary(nil), summaries...)
	for i := range result {
		result[i].Parents = append([]RevisionID(nil), result[i].Parents...)
		result[i].Provenance = cloneProvenance(result[i].Provenance)
	}
	return result
}

func cloneHeads(heads map[BeadID][]RevisionID) map[BeadID][]RevisionID {
	result := make(map[BeadID][]RevisionID, len(heads))
	for id, revisions := range heads {
		result[id] = append([]RevisionID(nil), revisions...)
	}
	return result
}

func cloneFieldBlame(fields map[string]RevisionID) map[string]RevisionID {
	result := make(map[string]RevisionID, len(fields))
	for field, revisionID := range fields {
		result[field] = revisionID
	}
	return result
}

func newConflictError(beadID BeadID, heads []RevisionID) *ConflictError {
	result := append([]RevisionID(nil), heads...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return &ConflictError{BeadID: beadID, Heads: result}
}

func excerpt(body string, maxRunes int) string {
	runes := []rune(body)
	if len(runes) <= maxRunes {
		return body
	}
	return string(runes[:maxRunes])
}

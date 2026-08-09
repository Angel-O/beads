package sqlprototype

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

	a2 "github.com/steveyegge/beads/internal/memorybeads/spikea2"
)

// Module is the SQL-backed A2 prototype. The mutex protects only one module's
// transient cursor and fault-control state; canonical revisions and heads are
// always read from the provider repository.
type Module struct {
	mu sync.Mutex

	adapter    Adapter
	repository repository
	projectID  a2.ProjectID
	instance   string
	activeView string

	cursorSeq uint64
	cursors   map[a2.Continuation]cursorRecord

	nextFault           a2.FaultPoint
	publicationAttempts int
	lastPrepared        a2.Address
}

type cursorRecord struct {
	kind      string
	signature string
	offset    int
	history   []a2.RevisionSummary
	search    []a2.SearchSummary
	refs      []a2.Reference
}

// New opens one Project in adapter's repository. Project identity, rather than
// a process-local token, selects durable revisions and head sets. A fresh
// Module for the same Project therefore observes the same canonical state.
//
// activeView is deliberately session-local and starts at main. Named views
// themselves are repository state and can be discovered with Checkout after a
// Module or provider reopen.
func New(adapter Adapter, projectID a2.ProjectID) *Module {
	instance := randomToken("sql-a2-instance-")
	return &Module{
		adapter:    adapter,
		projectID:  projectID,
		instance:   instance,
		activeView: "main",
		repository: repository{
			namespace: prototypeNamespace,
			projectID: projectID,
		},
		cursors: make(map[a2.Continuation]cursorRecord),
	}
}

var _ a2.Module = (*Module)(nil)

func randomToken(prefix string) string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(fmt.Sprintf("create A2 SQL prototype identity: %v", err))
	}
	return prefix + hex.EncodeToString(value[:])
}

// Mutate validates canonical state, prepares an opaque provider identity, and
// atomically appends the immutable revision plus its new current head.
func (m *Module) Mutate(ctx context.Context, request a2.Mutation) (a2.MutationResult, error) {
	if err := ctx.Err(); err != nil {
		return a2.MutationResult{Outcome: a2.OutcomeFailed}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.adapter == nil {
		return a2.MutationResult{Outcome: a2.OutcomeFailed}, errors.New("A2 SQL prototype adapter is nil")
	}
	if m.projectID == "" {
		return rejected(fmt.Errorf("%w: project ID is required", a2.ErrInvalid))
	}
	if request.Author == "" {
		return rejected(fmt.Errorf("%w: author is required", a2.ErrInvalid))
	}
	if request.Lifecycle == "" {
		request.Lifecycle = a2.LifecycleActive
	}
	if request.Lifecycle != a2.LifecycleActive && request.Lifecycle != a2.LifecycleArchived {
		return rejected(fmt.Errorf("%w: unknown lifecycle %q", a2.ErrInvalid, request.Lifecycle))
	}
	if request.Origin == "" {
		request.Origin = a2.OriginNative
	}
	if request.Origin != a2.OriginNative && request.Origin != a2.OriginLegacyMigration && request.Origin != a2.OriginImport {
		return rejected(fmt.Errorf("%w: unknown revision origin %q", a2.ErrInvalid, request.Origin))
	}
	aliases, err := normalizeAliases(request.Aliases)
	if err != nil {
		return rejected(err)
	}

	beadID := request.BeadID
	creating := beadID == ""
	if creating {
		if request.ExpectedCurrent != "" {
			return rejected(fmt.Errorf("%w: a create cannot name an expected current revision", a2.ErrInvalid))
		}
		beadID = a2.BeadID(randomToken("sql-bead-"))
	}
	refs, err := normalizeReferences(m.projectID, beadID, request.References)
	if err != nil {
		return rejected(err)
	}

	// The read is the no-op linearization point. Publication repeats every
	// current-head check inside the real write transaction before appending.
	var observedParent *storedRevision
	if !creating {
		err = m.adapter.Read(ctx, func(session Session) error {
			var readErr error
			observedParent, readErr = m.repository.selectRevision(ctx, session, m.activeView, beadID, "")
			return readErr
		})
		if err != nil {
			return rejected(err)
		}
		if request.ExpectedCurrent == "" {
			return rejected(fmt.Errorf("%w: an edit requires expected current revision", a2.ErrInvalid))
		}
		actual := observedParent.Revision.Address.RevisionID
		if request.ExpectedCurrent != actual {
			return rejected(&a2.StaleError{Expected: request.ExpectedCurrent, Actual: actual})
		}
		if mutationMatchesRevision(request, aliases, refs, observedParent.Revision) {
			revision := cloneRevision(observedParent.Revision)
			return a2.MutationResult{Outcome: a2.OutcomeUnchanged, Address: revision.Address, Revision: &revision}, nil
		}
	}

	revisionID := a2.RevisionID(randomToken("sql-revision-"))
	address := a2.Address{ProjectID: m.projectID, BeadID: beadID, RevisionID: revisionID}
	m.lastPrepared = address
	m.publicationAttempts++
	fault := m.nextFault
	m.nextFault = a2.FaultNone

	if fault == a2.FaultIndeterminateBefore {
		return indeterminateResult(), nil
	}

	observation := m.adapter.Publish(ctx, "spike A2: publish memory revision", func(session Session) error {
		if err := m.repository.ensureView(ctx, session, m.activeView); err != nil {
			return err
		}
		var parent *storedRevision
		if creating {
			heads, err := m.repository.heads(ctx, session, m.activeView, beadID)
			if err != nil {
				return err
			}
			if len(heads) != 0 {
				return fmt.Errorf("%w: generated bead ID collision", a2.ErrInvalid)
			}
		} else {
			var err error
			parent, err = m.repository.selectRevision(ctx, session, m.activeView, beadID, "")
			if err != nil {
				return err
			}
			actual := parent.Revision.Address.RevisionID
			if request.ExpectedCurrent != actual {
				return &a2.StaleError{Expected: request.ExpectedCurrent, Actual: actual}
			}
		}

		prepared := buildStoredRevision(address, request, aliases, refs, parent)
		if err := m.repository.insertRevision(ctx, session, prepared); err != nil {
			return err
		}
		if fault == a2.FaultBeforePublication {
			// Fail after the immutable append but before the current-head move.
			// The real provider transaction must roll both effects back; merely
			// opening a transaction and failing before any write would not prove
			// composite atomicity.
			return a2.ErrInjectedFailure
		}
		return m.repository.replaceHead(ctx, session, m.activeView, beadID, revisionID)
	})

	if observation.State == Unknown || (fault == a2.FaultIndeterminateAfter && observation.State == Published) {
		return indeterminateResult(), nil
	}
	if observation.State != Published {
		if isSemanticRejection(observation.Err) {
			return rejected(observation.Err)
		}
		if observation.Err == nil {
			observation.Err = errors.New("A2 publication did not commit")
		}
		return a2.MutationResult{Outcome: a2.OutcomeFailed}, observation.Err
	}
	if observation.Err != nil {
		return a2.MutationResult{
			Outcome: a2.OutcomeAppliedUnverified,
			Address: address,
			Detail:  "canonical state is applied; provider version publication could not be verified",
		}, nil
	}
	if fault == a2.FaultAfterKnownPublication {
		return a2.MutationResult{
			Outcome: a2.OutcomeAppliedUnverified,
			Address: address,
			Detail:  "publication is known; complete result verification was injected to fail",
		}, nil
	}

	verified, verifyErr := m.readRevision(ctx, beadID, revisionID)
	if verifyErr != nil {
		return a2.MutationResult{
			Outcome: a2.OutcomeAppliedUnverified,
			Address: address,
			Detail:  "publication is known; complete result verification failed",
		}, nil
	}
	return a2.MutationResult{Outcome: a2.OutcomeApplied, Address: address, Revision: &verified}, nil
}

func buildStoredRevision(address a2.Address, request a2.Mutation, aliases []string, refs []a2.Reference, parent *storedRevision) storedRevision {
	revision := a2.Revision{
		Address:        address,
		Key:            request.Key,
		Aliases:        append([]string(nil), aliases...),
		Title:          request.Title,
		Lifecycle:      request.Lifecycle,
		Body:           request.Body,
		References:     cloneReferences(refs),
		Author:         request.Author,
		AssistingAgent: request.AssistingAgent,
		ChangeMessage:  request.ChangeMessage,
		Origin:         request.Origin,
		Provenance:     cloneProvenance(request.Provenance),
		CreatedAt:      time.Now().UTC(),
	}
	if parent != nil {
		revision.Parents = []a2.RevisionID{parent.Revision.Address.RevisionID}
	}
	stored := storedRevision{
		Revision:   revision,
		LineBlame:  attributeLines(parent, request.Body, address.RevisionID),
		FieldBlame: make(map[string]a2.RevisionID),
	}
	for _, field := range semanticFields() {
		stored.FieldBlame[field] = address.RevisionID
		if parent != nil && semanticFieldEqual(field, parent.Revision, revision) {
			stored.FieldBlame[field] = parent.FieldBlame[field]
		}
	}
	return stored
}

func rejected(err error) (a2.MutationResult, error) {
	return a2.MutationResult{Outcome: a2.OutcomeRejected}, err
}

func indeterminateResult() a2.MutationResult {
	return a2.MutationResult{
		Outcome: a2.OutcomeIndeterminate,
		Detail:  "publication acknowledgement was lost; whether it landed is unknown",
	}
}

func (m *Module) Read(ctx context.Context, request a2.ReadRequest) (a2.Revision, error) {
	if err := ctx.Err(); err != nil {
		return a2.Revision{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.readRevision(ctx, request.BeadID, request.RevisionID)
}

func (m *Module) readRevision(ctx context.Context, beadID a2.BeadID, revisionID a2.RevisionID) (a2.Revision, error) {
	var stored *storedRevision
	err := m.adapter.Read(ctx, func(session Session) error {
		var readErr error
		stored, readErr = m.repository.selectRevision(ctx, session, m.activeView, beadID, revisionID)
		return readErr
	})
	if err != nil {
		return a2.Revision{}, err
	}
	return cloneRevision(stored.Revision), nil
}

// FailNext and the inspection methods are fixture-only controls required by
// the shared black-box contract.
func (m *Module) FailNext(point a2.FaultPoint) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextFault = point
}

func (m *Module) RevisionIDs(beadID a2.BeadID) []a2.RevisionID {
	m.mu.Lock()
	defer m.mu.Unlock()
	var stored []storedRevision
	if err := m.adapter.Read(context.Background(), func(session Session) error {
		var readErr error
		stored, readErr = m.repository.revisions(context.Background(), session, beadID)
		return readErr
	}); err != nil {
		return nil
	}
	result := make([]a2.RevisionID, len(stored))
	for i := range stored {
		result[i] = stored[i].Revision.Address.RevisionID
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (m *Module) LastPrepared() a2.Address {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastPrepared
}

func (m *Module) PublicationAttempts() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.publicationAttempts
}

func normalizeAliases(aliases []string) ([]string, error) {
	result := append([]string(nil), aliases...)
	for _, alias := range result {
		if alias == "" {
			return nil, fmt.Errorf("%w: an alias cannot be empty", a2.ErrInvalid)
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

func mutationMatchesRevision(request a2.Mutation, aliases []string, refs []a2.Reference, revision a2.Revision) bool {
	return request.Key == revision.Key &&
		reflect.DeepEqual(aliases, revision.Aliases) &&
		request.Title == revision.Title &&
		request.Lifecycle == revision.Lifecycle &&
		request.Body == revision.Body &&
		referencesEqual(refs, revision.References)
}

func semanticFields() []string {
	return []string{"key", "aliases", "title", "lifecycle", "outgoing_references"}
}

func semanticFieldEqual(field string, left, right a2.Revision) bool {
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

func normalizeReferences(projectID a2.ProjectID, source a2.BeadID, refs []a2.Reference) ([]a2.Reference, error) {
	result := make([]a2.Reference, 0, len(refs))
	byLocator := make(map[string]int, len(refs))
	for _, ref := range refs {
		target := ref.Target
		if target.BeadID == "" || target.ExpectedScope == "" {
			return nil, fmt.Errorf("%w: reference target, scope, and kind are required", a2.ErrInvalid)
		}
		if target.ExpectedKind != a2.BeadKindTask && target.ExpectedKind != a2.BeadKindMemory {
			return nil, fmt.Errorf("%w: reference target kind %q", a2.ErrInvalid, target.ExpectedKind)
		}
		if target.Local && target.ProjectID != "" {
			return nil, fmt.Errorf("%w: a source-local target cannot carry a foreign project ID", a2.ErrInvalid)
		}
		if !target.Local && target.ProjectID == "" {
			return nil, fmt.Errorf("%w: a foreign target requires a project ID", a2.ErrInvalid)
		}
		if !target.Local && (target.ExpectedKind != a2.BeadKindMemory || target.RevisionID == "") {
			return nil, fmt.Errorf("%w: a foreign target must identify an exact memory revision", a2.ErrInvalid)
		}
		if target.ExpectedKind == a2.BeadKindTask && target.RevisionID != "" {
			return nil, fmt.Errorf("%w: a task target cannot carry a memory revision pin", a2.ErrInvalid)
		}
		if target.BeadID == source && (target.Local || target.ProjectID == projectID) {
			return nil, fmt.Errorf("%w: a memory cannot reference itself", a2.ErrInvalid)
		}
		locator := referenceLocatorKey(ref)
		if index, exists := byLocator[locator]; exists {
			result[index] = ref
			continue
		}
		byLocator[locator] = len(result)
		result = append(result, ref)
	}
	sort.Slice(result, func(i, j int) bool { return referenceLocatorKey(result[i]) < referenceLocatorKey(result[j]) })
	return result, nil
}

func referenceLocatorKey(ref a2.Reference) string {
	target := ref.Target
	return strings.Join([]string{strconv.FormatBool(target.Local), string(target.ProjectID), string(target.BeadID)}, "\x00")
}

func referenceKey(ref a2.Reference) string {
	target := ref.Target
	return strings.Join([]string{
		strconv.FormatBool(target.Local), string(target.ProjectID), string(target.BeadID),
		target.ExpectedScope, string(target.ExpectedKind), string(target.RevisionID),
	}, "\x00")
}

func referencesEqual(left, right []a2.Reference) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if referenceKey(left[i]) != referenceKey(right[i]) {
			return false
		}
	}
	return true
}

func attributeLines(parent *storedRevision, body string, revisionID a2.RevisionID) []a2.RevisionID {
	lines := bodyLines(body)
	result := make([]a2.RevisionID, len(lines))
	for i := range result {
		result[i] = revisionID
	}
	if parent == nil {
		return result
	}
	old := bodyLines(parent.Revision.Body)
	for _, pair := range lcsPairs(old, lines) {
		result[pair[1]] = parent.LineBlame[pair[0]]
	}
	return result
}

func bodyLines(body string) []string {
	if body == "" {
		return nil
	}
	return strings.Split(body, "\n")
}

func lcsPairs(left, right []string) [][2]int {
	dp := make([][]int, len(left)+1)
	for i := range dp {
		dp[i] = make([]int, len(right)+1)
	}
	for i := len(left) - 1; i >= 0; i-- {
		for j := len(right) - 1; j >= 0; j-- {
			if left[i] == right[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var pairs [][2]int
	for i, j := 0, 0; i < len(left) && j < len(right); {
		switch {
		case left[i] == right[j]:
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

func cloneRevision(revision a2.Revision) a2.Revision {
	revision.Parents = append([]a2.RevisionID(nil), revision.Parents...)
	revision.Aliases = append([]string(nil), revision.Aliases...)
	revision.References = cloneReferences(revision.References)
	revision.Provenance = cloneProvenance(revision.Provenance)
	return revision
}

func cloneReferences(refs []a2.Reference) []a2.Reference {
	return append([]a2.Reference(nil), refs...)
}

func cloneProvenance(provenance []a2.Provenance) []a2.Provenance {
	return append([]a2.Provenance(nil), provenance...)
}

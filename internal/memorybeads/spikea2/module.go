// Package spikea2 contains the provider-neutral fixture used to test exact
// historical reads and atomic updates across several provider paths. Its
// revision, view, and conflict types are experimental controls rather than the
// shared Beads History or Versioned Bead contract.
package spikea2

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type ProjectID string
type BeadID string
type RevisionID string
type Continuation string

type Lifecycle string

const (
	LifecycleActive   Lifecycle = "active"
	LifecycleArchived Lifecycle = "archived"
)

type BeadKind string

const (
	BeadKindTask   BeadKind = "task"
	BeadKindMemory BeadKind = "memory"
)

type RevisionOrigin string

const (
	OriginNative          RevisionOrigin = "native"
	OriginLegacyMigration RevisionOrigin = "legacy_migration"
	OriginImport          RevisionOrigin = "import"
)

// Provenance preserves source evidence without treating it as destination
// authorship or provider-native lineage.
type Provenance struct {
	SourceProjectID  ProjectID
	SourceBeadID     BeadID
	SourceRevisionID RevisionID
	Evidence         string
	Gap              string
}

// Target is stored reference state. Local distinguishes a source-project
// locator from an explicitly project-qualified locator; ProjectID is required
// only for the latter.
type Target struct {
	Local         bool
	ProjectID     ProjectID
	BeadID        BeadID
	ExpectedScope string
	ExpectedKind  BeadKind
	RevisionID    RevisionID
}

type Reference struct {
	Target Target
}

type Address struct {
	ProjectID  ProjectID
	BeadID     BeadID
	RevisionID RevisionID
}

// Revision is one complete, immutable state. Provider commit identifiers do
// not appear in this type.
type Revision struct {
	Address        Address
	Parents        []RevisionID
	Key            string
	Aliases        []string
	Title          string
	Lifecycle      Lifecycle
	Body           string
	References     []Reference
	Author         string
	AssistingAgent string
	ChangeMessage  string
	Origin         RevisionOrigin
	Provenance     []Provenance
	CreatedAt      time.Time
}

type Mutation struct {
	BeadID          BeadID
	ExpectedCurrent RevisionID
	Key             string
	Aliases         []string
	Title           string
	Lifecycle       Lifecycle
	Body            string
	References      []Reference
	Author          string
	AssistingAgent  string
	ChangeMessage   string
	Origin          RevisionOrigin
	Provenance      []Provenance
}

type Outcome string

const (
	OutcomeApplied           Outcome = "applied"
	OutcomeAppliedUnverified Outcome = "applied_unverified"
	OutcomeUnchanged         Outcome = "unchanged"
	OutcomeRejected          Outcome = "rejected"
	OutcomeFailed            Outcome = "failed"
	OutcomeIndeterminate     Outcome = "indeterminate"
)

// MutationResult makes outcome invariants executable. Applied carries the
// complete verified revision. AppliedUnverified carries only the known stable
// address. Indeterminate carries neither an address nor a revision.
type MutationResult struct {
	Outcome  Outcome
	Address  Address
	Revision *Revision
	Detail   string
}

type ReadRequest struct {
	BeadID     BeadID
	RevisionID RevisionID // empty selects current
}

type HistoryRequest struct {
	BeadID       BeadID
	Limit        int
	Continuation Continuation
}

type RevisionSummary struct {
	Address        Address
	Parents        []RevisionID
	Key            string
	Title          string
	Lifecycle      Lifecycle
	Author         string
	AssistingAgent string
	ChangeMessage  string
	Origin         RevisionOrigin
	Provenance     []Provenance
	CreatedAt      time.Time
}

type HistoryPage struct {
	Revisions    []RevisionSummary
	Complete     bool
	Continuation Continuation
}

type SearchRequest struct {
	Query           string
	IncludeArchived bool
	Limit           int
	Continuation    Continuation
}

type SearchSummary struct {
	ProjectID       ProjectID
	BeadID          BeadID
	CurrentRevision RevisionID
	Key             string
	Title           string
	Lifecycle       Lifecycle
	Excerpt         string
}

type SearchPage struct {
	Memories     []SearchSummary
	Complete     bool
	Continuation Continuation
}

// ReferencesRequest deliberately selects an exact source revision. A current
// caller first reads current and then uses the returned revision ID.
type ReferencesRequest struct {
	BeadID       BeadID
	RevisionID   RevisionID
	Limit        int
	Continuation Continuation
}

type ReferencePage struct {
	References   []Reference
	Complete     bool
	Continuation Continuation
}

type DiffRequest struct {
	BeadID BeadID
	From   RevisionID
	To     RevisionID
}

type FieldChange struct {
	Field  string
	Before any
	After  any
}

type DiffResult struct {
	From              Address
	To                Address
	Fields            []FieldChange
	ReferencesAdded   []Reference
	ReferencesRemoved []Reference
}

type BlameRequest struct {
	BeadID     BeadID
	RevisionID RevisionID
}

type LineAttribution struct {
	Line       string
	RevisionID RevisionID
}

type FieldAttribution struct {
	Field      string
	RevisionID RevisionID
}

type BlameResult struct {
	Address Address
	Lines   []LineAttribution
	Fields  []FieldAttribution
}

// Module is the smallest caller-facing surface needed to execute A2. Branch,
// maintenance, and failure injection are provider test controls, not Memory
// Bead operations exercised by this fixture.
type Module interface {
	Mutate(context.Context, Mutation) (MutationResult, error)
	Read(context.Context, ReadRequest) (Revision, error)
	History(context.Context, HistoryRequest) (HistoryPage, error)
	Search(context.Context, SearchRequest) (SearchPage, error)
	References(context.Context, ReferencesRequest) (ReferencePage, error)
	Diff(context.Context, DiffRequest) (DiffResult, error)
	Blame(context.Context, BlameRequest) (BlameResult, error)
}

var (
	ErrNotFound            = errors.New("memory revision not found")
	ErrInvalid             = errors.New("invalid memory request")
	ErrInvalidContinuation = errors.New("invalid or mismatched continuation")
	ErrInjectedFailure     = errors.New("injected publication failure")
)

type StaleError struct {
	Expected RevisionID
	Actual   RevisionID
}

func (e *StaleError) Error() string {
	return fmt.Sprintf("stale memory revision: expected %q, current is %q", e.Expected, e.Actual)
}

// ConflictError never contains a selected winner. Heads are sorted only for
// deterministic presentation; their order has no semantic meaning.
type ConflictError struct {
	BeadID BeadID
	Heads  []RevisionID
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("memory %q has divergent current revisions: %v", e.BeadID, e.Heads)
}

type FaultPoint string

const (
	FaultNone                  FaultPoint = ""
	FaultBeforePublication     FaultPoint = "before_publication"
	FaultAfterKnownPublication FaultPoint = "after_known_publication"
	FaultIndeterminateBefore   FaultPoint = "indeterminate_before_publication"
	FaultIndeterminateAfter    FaultPoint = "indeterminate_after_publication"
)

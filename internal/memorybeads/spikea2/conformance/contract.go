// Package conformance holds the provider-neutral black-box contract for the
// A2 prototype. Provider controls are fixture-only: production callers see
// only spikea2.Module.
package conformance

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	a2 "github.com/steveyegge/beads/internal/memorybeads/spikea2"
)

// PublicationControl exposes only fixture hooks for publication-boundary
// faults. It is not part of the production module.
type PublicationControl interface {
	FailNext(a2.FaultPoint)
	RevisionIDs(a2.BeadID) []a2.RevisionID
	LastPrepared() a2.Address
	PublicationAttempts() int
}

// BranchControl is optional. Providers without branch operations can run the
// core contract without pretending to implement them.
type BranchControl interface {
	Fork(string) error
	Checkout(string) error
	Merge(string) error
	DeleteBranch(string) error
}

type Fixture struct {
	Module      a2.Module
	Publication PublicationControl
	Branches    BranchControl
	Maintain    func()
	// Reconstruct returns a fresh Module object over the same provider
	// repository and Project. It is optional because not every independent
	// implementation exposes construction as a fixture seam.
	Reconstruct func() Fixture
}

type Factory func(*testing.T) Fixture

// Run executes the minimum A2 contract. Each case gets a fresh provider so
// history, branches, cursors, and injected failures cannot leak between cases.
func Run(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("StableOpaqueExactRevisions", func(t *testing.T) {
		runStableOpaqueExactRevisions(t, factory(t))
	})
	t.Run("AtomicPublicationAndTruthfulOutcomes", func(t *testing.T) {
		runAtomicPublicationAndTruthfulOutcomes(t, factory(t))
	})
	t.Run("StoredProvenanceIsDurableState", func(t *testing.T) {
		runStoredProvenanceIsDurableState(t, factory(t))
	})
	t.Run("SemanticHistoryDiffAndBlame", func(t *testing.T) {
		runSemanticHistoryDiffAndBlame(t, factory(t))
	})
	t.Run("ConflictHasNoGuessedHead", func(t *testing.T) {
		runConflictHasNoGuessedHead(t, factory(t))
	})
	t.Run("ContinuationIsSnapshotAndScopeBound", func(t *testing.T) {
		runContinuationIsSnapshotAndScopeBound(t, factory(t))
	})
	t.Run("ContinuationRejectsAnotherProviderInstance", func(t *testing.T) {
		runContinuationRejectsAnotherProviderInstance(t, factory(t), factory(t))
	})
}

func runStableOpaqueExactRevisions(t *testing.T, fixture Fixture) {
	t.Helper()
	ctx := context.Background()
	first := apply(t, ctx, fixture.Module, a2.Mutation{
		Key:            "durable-policy",
		Aliases:        []string{"policy", "durable"},
		Title:          "Durable policy",
		Lifecycle:      a2.LifecycleActive,
		Body:           "durable body",
		References:     []a2.Reference{localTask("task-1")},
		Author:         "Ada <ada@example.test>",
		AssistingAgent: "agent-a",
		ChangeMessage:  "import durable policy",
		Origin:         a2.OriginImport,
		Provenance:     []a2.Provenance{{SourceProjectID: "source-project", SourceBeadID: "source-memory", SourceRevisionID: "source-revision", Evidence: "portable source evidence"}},
	})
	if first.Address.ProjectID == "" || first.Address.RevisionID == "" || first.Address.BeadID == "" {
		t.Fatalf("applied create returned an incomplete canonical address: %+v", first.Address)
	}

	archive := apply(t, ctx, fixture.Module, a2.Mutation{
		BeadID:          first.Address.BeadID,
		ExpectedCurrent: first.Address.RevisionID,
		Key:             first.Key,
		Aliases:         first.Aliases,
		Title:           first.Title,
		Lifecycle:       a2.LifecycleArchived,
		Body:            first.Body,
		References:      first.References,
		Author:          "Ada <ada@example.test>",
		ChangeMessage:   "archive",
		Origin:          a2.OriginNative,
	})
	restore := apply(t, ctx, fixture.Module, a2.Mutation{
		BeadID:          first.Address.BeadID,
		ExpectedCurrent: archive.Address.RevisionID,
		Key:             archive.Key,
		Aliases:         archive.Aliases,
		Title:           archive.Title,
		Lifecycle:       a2.LifecycleActive,
		Body:            first.Body,
		References:      first.References,
		Author:          "Ada <ada@example.test>",
		AssistingAgent:  "agent-b",
		ChangeMessage:   "restore",
		Origin:          a2.OriginNative,
	})
	if first.Address.RevisionID == archive.Address.RevisionID || archive.Address.RevisionID == restore.Address.RevisionID {
		t.Fatal("distinct applied mutations reused a revision ID")
	}

	saved := map[a2.RevisionID]a2.Revision{
		first.Address.RevisionID:   first,
		archive.Address.RevisionID: archive,
		restore.Address.RevisionID: restore,
	}
	assertExactReads(t, ctx, fixture.Module, saved)
	var mainBeforeReconstruction, durableBranchRevision a2.Revision

	if fixture.Branches != nil {
		// A provider-supported fast-forward merge keeps the exact address rather
		// than translating it into a provider commit or minting a replacement.
		if err := fixture.Branches.Fork("fast-forward"); err != nil {
			t.Fatalf("fork: %v", err)
		}
		if err := fixture.Branches.Checkout("fast-forward"); err != nil {
			t.Fatalf("checkout fast-forward: %v", err)
		}
		branchRevision := apply(t, ctx, fixture.Module, a2.Mutation{
			BeadID:          first.Address.BeadID,
			ExpectedCurrent: restore.Address.RevisionID,
			Key:             restore.Key,
			Aliases:         restore.Aliases,
			Title:           restore.Title,
			Lifecycle:       restore.Lifecycle,
			Body:            "branch-only exact state",
			References:      restore.References,
			Author:          "Grace <grace@example.test>",
			AssistingAgent:  "agent-c",
			ChangeMessage:   "branch revision",
			Origin:          a2.OriginNative,
		})
		saved[branchRevision.Address.RevisionID] = branchRevision
		if err := fixture.Branches.Checkout("main"); err != nil {
			t.Fatalf("checkout main: %v", err)
		}
		if err := fixture.Branches.Merge("fast-forward"); err != nil {
			t.Fatalf("fast-forward merge: %v", err)
		}
		current := readCurrent(t, ctx, fixture.Module, first.Address.BeadID)
		if current.Address.RevisionID != branchRevision.Address.RevisionID {
			t.Fatalf("fast-forward current = %q, want exact branch revision %q", current.Address.RevisionID, branchRevision.Address.RevisionID)
		}
		if err := fixture.Branches.DeleteBranch("fast-forward"); err != nil {
			t.Fatalf("delete merged branch: %v", err)
		}

		// A branch-only accepted revision also remains exactly addressable after
		// its branch is removed, even if it was never selected in the main view.
		if err := fixture.Branches.Fork("removed"); err != nil {
			t.Fatalf("fork removed: %v", err)
		}
		if err := fixture.Branches.Checkout("removed"); err != nil {
			t.Fatalf("checkout removed: %v", err)
		}
		removedRevision := apply(t, ctx, fixture.Module, a2.Mutation{
			BeadID:          first.Address.BeadID,
			ExpectedCurrent: branchRevision.Address.RevisionID,
			Key:             branchRevision.Key,
			Aliases:         branchRevision.Aliases,
			Title:           branchRevision.Title,
			Lifecycle:       branchRevision.Lifecycle,
			Body:            "accepted on a branch that will be removed",
			References:      branchRevision.References,
			Author:          "Grace <grace@example.test>",
			ChangeMessage:   "removed branch revision",
			Origin:          a2.OriginNative,
		})
		saved[removedRevision.Address.RevisionID] = removedRevision
		if err := fixture.Branches.Checkout("main"); err != nil {
			t.Fatalf("restore main: %v", err)
		}
		if err := fixture.Branches.DeleteBranch("removed"); err != nil {
			t.Fatalf("delete branch: %v", err)
		}
		assertExactReads(t, ctx, fixture.Module, saved)

		if fixture.Reconstruct != nil {
			// Keep one named view alive across provider maintenance and Module
			// reconstruction. This distinguishes durable view identity from a map
			// held by the original Go object.
			mainBeforeReconstruction = readCurrent(t, ctx, fixture.Module, first.Address.BeadID)
			if err := fixture.Branches.Fork("survives-reconstruction"); err != nil {
				t.Fatalf("fork durable view: %v", err)
			}
			if err := fixture.Branches.Checkout("survives-reconstruction"); err != nil {
				t.Fatalf("checkout durable view: %v", err)
			}
			durableBranchRevision = apply(t, ctx, fixture.Module, a2.Mutation{
				BeadID:          first.Address.BeadID,
				ExpectedCurrent: mainBeforeReconstruction.Address.RevisionID,
				Key:             mainBeforeReconstruction.Key,
				Aliases:         mainBeforeReconstruction.Aliases,
				Title:           mainBeforeReconstruction.Title,
				Lifecycle:       mainBeforeReconstruction.Lifecycle,
				Body:            "durable branch head before reconstruction",
				References:      mainBeforeReconstruction.References,
				Author:          "Grace <grace@example.test>",
				ChangeMessage:   "prove durable view reconstruction",
				Origin:          a2.OriginNative,
			})
			saved[durableBranchRevision.Address.RevisionID] = durableBranchRevision
			if err := fixture.Branches.Checkout("main"); err != nil {
				t.Fatalf("restore main before reconstruction: %v", err)
			}
		}
	}

	if fixture.Maintain != nil {
		fixture.Maintain()
	}
	active := fixture
	if fixture.Reconstruct != nil {
		active = fixture.Reconstruct()
		if active.Module == nil || active.Branches == nil {
			t.Fatal("reconstruction fixture must provide a fresh Module and durable branch controls")
		}
	}
	assertExactReads(t, ctx, active.Module, saved)

	if durableBranchRevision.Address.RevisionID != "" {
		// A fresh Module starts at main, discovers the persisted named view, and
		// sees each view's exact current head. The old and new Module objects then
		// remain coherent over the same durable identity while keeping checkout
		// context local to each object.
		if got := readCurrent(t, ctx, active.Module, first.Address.BeadID); got.Address != mainBeforeReconstruction.Address {
			t.Fatalf("reconstructed main head = %+v, want %+v", got.Address, mainBeforeReconstruction.Address)
		}
		if err := active.Branches.Checkout("survives-reconstruction"); err != nil {
			t.Fatalf("reconstructed checkout of durable view: %v", err)
		}
		if got := readCurrent(t, ctx, active.Module, first.Address.BeadID); got.Address != durableBranchRevision.Address {
			t.Fatalf("reconstructed durable-view head = %+v, want %+v", got.Address, durableBranchRevision.Address)
		}
		continued := apply(t, ctx, active.Module, a2.Mutation{
			BeadID:          first.Address.BeadID,
			ExpectedCurrent: durableBranchRevision.Address.RevisionID,
			Key:             durableBranchRevision.Key,
			Aliases:         durableBranchRevision.Aliases,
			Title:           durableBranchRevision.Title,
			Lifecycle:       durableBranchRevision.Lifecycle,
			Body:            "continued through a fresh Module instance",
			References:      durableBranchRevision.References,
			Author:          "Grace <grace@example.test>",
			ChangeMessage:   "continue durable view after reconstruction",
			Origin:          a2.OriginNative,
		})
		if got, err := fixture.Module.Read(ctx, a2.ReadRequest{BeadID: continued.Address.BeadID, RevisionID: continued.Address.RevisionID}); err != nil || !reflect.DeepEqual(got, continued) {
			t.Fatalf("original Module cannot resolve fresh Module publication: revision %+v error %v", got, err)
		}
		if got := readCurrent(t, ctx, fixture.Module, first.Address.BeadID); got.Address != mainBeforeReconstruction.Address {
			t.Fatalf("fresh Module checkout leaked into original Module: got %+v, want %+v", got.Address, mainBeforeReconstruction.Address)
		}
		if err := active.Branches.Checkout("main"); err != nil {
			t.Fatalf("restore reconstructed main: %v", err)
		}
		if err := active.Branches.DeleteBranch("survives-reconstruction"); err != nil {
			t.Fatalf("delete reconstructed durable view: %v", err)
		}
		if _, err := active.Module.Read(ctx, a2.ReadRequest{BeadID: continued.Address.BeadID, RevisionID: continued.Address.RevisionID}); err != nil {
			t.Fatalf("deleting reconstructed view removed its immutable revision: %v", err)
		}
	}

	history, err := active.Module.History(ctx, a2.HistoryRequest{BeadID: first.Address.BeadID})
	if err != nil {
		t.Fatalf("history after maintenance: %v", err)
	}
	if !history.Complete {
		t.Fatal("unbounded history unexpectedly incomplete")
	}
	for _, summary := range history.Revisions {
		if _, err := active.Module.Read(ctx, a2.ReadRequest{BeadID: summary.Address.BeadID, RevisionID: summary.Address.RevisionID}); err != nil {
			t.Fatalf("history advertised revision %q that exact read cannot retrieve: %v", summary.Address.RevisionID, err)
		}
	}
}

func runAtomicPublicationAndTruthfulOutcomes(t *testing.T, fixture Fixture) {
	t.Helper()
	ctx := context.Background()
	base := apply(t, ctx, fixture.Module, a2.Mutation{
		Key:            "atomic-policy",
		Aliases:        []string{"atomic", "publication"},
		Title:          "Atomic publication",
		Lifecycle:      a2.LifecycleActive,
		Body:           "base body",
		References:     []a2.Reference{localTask("task-base")},
		Author:         "Ada <ada@example.test>",
		ChangeMessage:  "create atomic fixture",
		Origin:         a2.OriginNative,
		AssistingAgent: "agent-a",
	})

	beforeInvalid := fixture.Publication.RevisionIDs(base.Address.BeadID)
	self := a2.Reference{Target: a2.Target{
		Local:         true,
		BeadID:        base.Address.BeadID,
		ExpectedScope: "project",
		ExpectedKind:  a2.BeadKindMemory,
	}}
	result, err := fixture.Module.Mutate(ctx, a2.Mutation{
		BeadID: base.Address.BeadID, ExpectedCurrent: base.Address.RevisionID,
		Key: base.Key, Aliases: base.Aliases, Title: base.Title,
		Lifecycle: base.Lifecycle, Body: base.Body, References: []a2.Reference{self}, Author: "Ada",
	})
	if result.Outcome != a2.OutcomeRejected || !errors.Is(err, a2.ErrInvalid) {
		t.Fatalf("self-reference = result %+v error %v, want structural rejection", result, err)
	}
	if got := fixture.Publication.RevisionIDs(base.Address.BeadID); !reflect.DeepEqual(got, beforeInvalid) {
		t.Fatalf("self-reference rejection minted a revision: before %v, after %v", beforeInvalid, got)
	}

	compositeRequestRefs := []a2.Reference{
		localTask("task-a"),
		localTask("task-b"),
	}
	beforeIDs := fixture.Publication.RevisionIDs(base.Address.BeadID)
	fixture.Publication.FailNext(a2.FaultBeforePublication)
	failed, err := fixture.Module.Mutate(ctx, a2.Mutation{
		BeadID:          base.Address.BeadID,
		ExpectedCurrent: base.Address.RevisionID,
		Lifecycle:       base.Lifecycle,
		Body:            "body that must not land alone",
		References:      compositeRequestRefs,
		Author:          "Ada <ada@example.test>",
	})
	if !errors.Is(err, a2.ErrInjectedFailure) || failed.Outcome != a2.OutcomeFailed {
		t.Fatalf("before-publication failure = result %+v error %v", failed, err)
	}
	if got := fixture.Publication.RevisionIDs(base.Address.BeadID); !reflect.DeepEqual(got, beforeIDs) {
		t.Fatalf("failed composite mutation changed revision catalog: before %v, after %v", beforeIDs, got)
	}
	if got := readCurrent(t, ctx, fixture.Module, base.Address.BeadID); !reflect.DeepEqual(got, base) {
		t.Fatalf("failed composite mutation changed current state:\n got  %+v\n want %+v", got, base)
	}

	composite := apply(t, ctx, fixture.Module, a2.Mutation{
		BeadID:          base.Address.BeadID,
		ExpectedCurrent: base.Address.RevisionID,
		Key:             base.Key,
		Aliases:         base.Aliases,
		Title:           base.Title,
		Lifecycle:       base.Lifecycle,
		Body:            "body and references publish together",
		References:      compositeRequestRefs,
		Author:          "Ada <ada@example.test>",
		AssistingAgent:  "agent-b",
		ChangeMessage:   "publish body and references",
		Origin:          a2.OriginNative,
	})
	if composite.Body != "body and references publish together" || !sameReferenceSet(composite.References, compositeRequestRefs) {
		t.Fatalf("successful composite revision is partial: %+v", composite)
	}
	idsBeforeUnchanged := fixture.Publication.RevisionIDs(base.Address.BeadID)
	unchanged, err := fixture.Module.Mutate(ctx, a2.Mutation{
		BeadID:          composite.Address.BeadID,
		ExpectedCurrent: composite.Address.RevisionID,
		Key:             composite.Key,
		Aliases:         []string{"publication", "atomic", "atomic"},
		Title:           composite.Title,
		Lifecycle:       composite.Lifecycle,
		Body:            composite.Body,
		References:      compositeRequestRefs,
		Author:          "Another Author",
		AssistingAgent:  "another-agent",
		ChangeMessage:   "must not manufacture a revision",
		Origin:          composite.Origin,
		Provenance:      composite.Provenance,
	})
	if err != nil || unchanged.Outcome != a2.OutcomeUnchanged || unchanged.Revision == nil || !reflect.DeepEqual(*unchanged.Revision, composite) {
		t.Fatalf("attribution-only change = result %+v error %v, want unchanged current revision", unchanged, err)
	}
	if got := fixture.Publication.RevisionIDs(base.Address.BeadID); !reflect.DeepEqual(got, idsBeforeUnchanged) {
		t.Fatalf("unchanged mutation minted a revision: before %v, after %v", idsBeforeUnchanged, got)
	}

	// Stale is checked before no-op evaluation. Supplying the old revision does
	// not become success merely because the desired state equals current.
	idsBeforeStale := fixture.Publication.RevisionIDs(base.Address.BeadID)
	stale, err := fixture.Module.Mutate(ctx, a2.Mutation{
		BeadID:          base.Address.BeadID,
		ExpectedCurrent: base.Address.RevisionID,
		Key:             composite.Key,
		Aliases:         composite.Aliases,
		Title:           composite.Title,
		Lifecycle:       composite.Lifecycle,
		Body:            composite.Body,
		References:      composite.References,
		Author:          "Ada <ada@example.test>",
	})
	var staleErr *a2.StaleError
	if stale.Outcome != a2.OutcomeRejected || !errors.As(err, &staleErr) {
		t.Fatalf("stale update = result %+v error %v, want typed rejection", stale, err)
	}
	if got := fixture.Publication.RevisionIDs(base.Address.BeadID); !reflect.DeepEqual(got, idsBeforeStale) {
		t.Fatalf("stale rejection minted a revision: before %v, after %v", idsBeforeStale, got)
	}

	attempts := fixture.Publication.PublicationAttempts()
	fixture.Publication.FailNext(a2.FaultAfterKnownPublication)
	known, err := fixture.Module.Mutate(ctx, a2.Mutation{
		BeadID:          base.Address.BeadID,
		ExpectedCurrent: composite.Address.RevisionID,
		Key:             composite.Key,
		Aliases:         composite.Aliases,
		Title:           composite.Title,
		Lifecycle:       composite.Lifecycle,
		Body:            "known published, result verification failed",
		References:      composite.References,
		Author:          "Lin <lin@example.test>",
		AssistingAgent:  "agent-c",
		ChangeMessage:   "known publication",
		Origin:          a2.OriginNative,
	})
	if err != nil || known.Outcome != a2.OutcomeAppliedUnverified || known.Address.RevisionID == "" || known.Revision != nil {
		t.Fatalf("known-publication outcome = result %+v error %v", known, err)
	}
	if fixture.Publication.PublicationAttempts() != attempts+1 {
		t.Fatal("applied_unverified publication was replayed")
	}
	knownRevision, err := fixture.Module.Read(ctx, a2.ReadRequest{BeadID: known.Address.BeadID, RevisionID: known.Address.RevisionID})
	if err != nil || knownRevision.Body != "known published, result verification failed" || !reflect.DeepEqual(knownRevision.References, composite.References) {
		t.Fatalf("applied_unverified address does not resolve to the complete publication: revision %+v error %v", knownRevision, err)
	}

	attempts = fixture.Publication.PublicationAttempts()
	beforeUnknown := fixture.Publication.RevisionIDs(base.Address.BeadID)
	indeterminateMutation := a2.Mutation{
		BeadID:          base.Address.BeadID,
		ExpectedCurrent: known.Address.RevisionID,
		Key:             knownRevision.Key,
		Aliases:         knownRevision.Aliases,
		Title:           knownRevision.Title,
		Lifecycle:       knownRevision.Lifecycle,
		Body:            "the adapter cannot know whether this landed",
		References:      knownRevision.References,
		Author:          "Lin <lin@example.test>",
		AssistingAgent:  "agent-d",
		ChangeMessage:   "ambiguous publication",
		Origin:          a2.OriginNative,
	}
	fixture.Publication.FailNext(a2.FaultIndeterminateBefore)
	unknownBefore, err := fixture.Module.Mutate(ctx, indeterminateMutation)
	if err != nil || unknownBefore.Outcome != a2.OutcomeIndeterminate || unknownBefore.Address != (a2.Address{}) || unknownBefore.Revision != nil {
		t.Fatalf("pre-publication indeterminate outcome = result %+v error %v", unknownBefore, err)
	}
	notPublished := fixture.Publication.LastPrepared()
	if _, err := fixture.Module.Read(ctx, a2.ReadRequest{BeadID: notPublished.BeadID, RevisionID: notPublished.RevisionID}); !errors.Is(err, a2.ErrNotFound) {
		t.Fatalf("hidden pre-publication decision exact read = %v, want ErrNotFound", err)
	}
	if got := fixture.Publication.RevisionIDs(base.Address.BeadID); !reflect.DeepEqual(got, beforeUnknown) {
		t.Fatalf("hidden pre-publication decision changed revisions: before %v, after %v", beforeUnknown, got)
	}

	fixture.Publication.FailNext(a2.FaultIndeterminateAfter)
	unknownAfter, err := fixture.Module.Mutate(ctx, indeterminateMutation)
	if err != nil || !reflect.DeepEqual(unknownAfter, unknownBefore) {
		t.Fatalf("post-publication indeterminate result %+v error %v differs from pre-publication result %+v", unknownAfter, err, unknownBefore)
	}
	if fixture.Publication.PublicationAttempts() != attempts+2 {
		t.Fatal("indeterminate publication path replayed an attempt")
	}
	published := fixture.Publication.LastPrepared()
	if _, err := fixture.Module.Read(ctx, a2.ReadRequest{BeadID: published.BeadID, RevisionID: published.RevisionID}); err != nil {
		t.Fatalf("hidden post-publication decision is not exactly readable: %v", err)
	}
	if got := fixture.Publication.RevisionIDs(base.Address.BeadID); len(got) != len(beforeUnknown)+1 {
		t.Fatalf("hidden post-publication decision revision count = %d, want %d", len(got), len(beforeUnknown)+1)
	}
}

func runStoredProvenanceIsDurableState(t *testing.T, fixture Fixture) {
	t.Helper()
	ctx := context.Background()
	initial := apply(t, ctx, fixture.Module, a2.Mutation{
		Key:           "provenance-policy",
		Title:         "Provenance policy",
		Lifecycle:     a2.LifecycleActive,
		Body:          "durable provenance body",
		Author:        "Ada <ada@example.test>",
		ChangeMessage: "create native memory",
		Origin:        a2.OriginNative,
		Provenance:    []a2.Provenance{{Gap: "created without external source evidence"}},
	})

	provenanceChanged := apply(t, ctx, fixture.Module, a2.Mutation{
		BeadID:          initial.Address.BeadID,
		ExpectedCurrent: initial.Address.RevisionID,
		Key:             initial.Key,
		Aliases:         initial.Aliases,
		Title:           initial.Title,
		Lifecycle:       initial.Lifecycle,
		Body:            initial.Body,
		References:      initial.References,
		Author:          "Ada <ada@example.test>",
		ChangeMessage:   "record source evidence",
		Origin:          initial.Origin,
		Provenance:      []a2.Provenance{{SourceProjectID: "source-project", SourceBeadID: "source-memory", SourceRevisionID: "source-revision", Evidence: "verified import source"}},
	})
	if provenanceChanged.Address.RevisionID == initial.Address.RevisionID || reflect.DeepEqual(provenanceChanged.Provenance, initial.Provenance) {
		t.Fatalf("provenance-only mutation did not create distinct durable state: initial %+v, changed %+v", initial, provenanceChanged)
	}

	originChanged := apply(t, ctx, fixture.Module, a2.Mutation{
		BeadID:          provenanceChanged.Address.BeadID,
		ExpectedCurrent: provenanceChanged.Address.RevisionID,
		Key:             provenanceChanged.Key,
		Aliases:         provenanceChanged.Aliases,
		Title:           provenanceChanged.Title,
		Lifecycle:       provenanceChanged.Lifecycle,
		Body:            provenanceChanged.Body,
		References:      provenanceChanged.References,
		Author:          "Ada <ada@example.test>",
		ChangeMessage:   "classify imported state",
		Origin:          a2.OriginImport,
		Provenance:      provenanceChanged.Provenance,
	})
	if originChanged.Address.RevisionID == provenanceChanged.Address.RevisionID || originChanged.Origin != a2.OriginImport {
		t.Fatalf("origin-only mutation did not create distinct durable state: before %+v, after %+v", provenanceChanged, originChanged)
	}

	idsBeforeAttribution := fixture.Publication.RevisionIDs(initial.Address.BeadID)
	unchanged, err := fixture.Module.Mutate(ctx, a2.Mutation{
		BeadID:          originChanged.Address.BeadID,
		ExpectedCurrent: originChanged.Address.RevisionID,
		Key:             originChanged.Key,
		Aliases:         originChanged.Aliases,
		Title:           originChanged.Title,
		Lifecycle:       originChanged.Lifecycle,
		Body:            originChanged.Body,
		References:      originChanged.References,
		Author:          "Grace <grace@example.test>",
		AssistingAgent:  "agent-b",
		ChangeMessage:   "attribution changes without durable state",
		Origin:          originChanged.Origin,
		Provenance:      originChanged.Provenance,
	})
	if err != nil || unchanged.Outcome != a2.OutcomeUnchanged || unchanged.Revision == nil || !reflect.DeepEqual(*unchanged.Revision, originChanged) {
		t.Fatalf("attribution-only mutation = result %+v error %v, want unchanged durable state", unchanged, err)
	}
	if got := fixture.Publication.RevisionIDs(initial.Address.BeadID); !reflect.DeepEqual(got, idsBeforeAttribution) {
		t.Fatalf("attribution-only mutation minted a revision: before %v, after %v", idsBeforeAttribution, got)
	}
}

func runSemanticHistoryDiffAndBlame(t *testing.T, fixture Fixture) {
	t.Helper()
	ctx := context.Background()
	one := localTask("task-one")
	two := localTask("task-two")
	first := apply(t, ctx, fixture.Module, a2.Mutation{
		Key:            "first-key",
		Aliases:        []string{"first-alias", "shared-alias"},
		Title:          "First title",
		Lifecycle:      a2.LifecycleActive,
		Body:           "keep this line\nreplace this line",
		References:     []a2.Reference{one},
		Author:         "Ada <ada@example.test>",
		AssistingAgent: "agent-a",
		ChangeMessage:  "first revision",
		Origin:         a2.OriginImport,
		Provenance:     []a2.Provenance{{SourceProjectID: "source-one", SourceBeadID: "memory-one", SourceRevisionID: "revision-one", Evidence: "first evidence"}},
	})
	second := apply(t, ctx, fixture.Module, a2.Mutation{
		BeadID:          first.Address.BeadID,
		ExpectedCurrent: first.Address.RevisionID,
		Key:             "second-key",
		Aliases:         []string{"second-alias"},
		Title:           "Second title",
		Lifecycle:       a2.LifecycleArchived,
		Body:            "keep this line\nnew replacement",
		References:      []a2.Reference{one, two},
		Author:          "Grace <grace@example.test>",
		AssistingAgent:  "agent-b",
		ChangeMessage:   "second revision",
		Origin:          a2.OriginLegacyMigration,
		Provenance:      []a2.Provenance{{Gap: "source history unavailable"}},
	})

	for _, request := range []a2.DiffRequest{
		{BeadID: first.Address.BeadID, To: second.Address.RevisionID},
		{BeadID: first.Address.BeadID, From: first.Address.RevisionID},
	} {
		if _, err := fixture.Module.Diff(ctx, request); !errors.Is(err, a2.ErrInvalid) {
			t.Fatalf("diff with floating endpoint error = %v, want ErrInvalid", err)
		}
	}

	diff, err := fixture.Module.Diff(ctx, a2.DiffRequest{BeadID: first.Address.BeadID, From: first.Address.RevisionID, To: second.Address.RevisionID})
	if err != nil {
		t.Fatalf("semantic diff: %v", err)
	}
	changedFields := make(map[string]bool, len(diff.Fields))
	for _, change := range diff.Fields {
		changedFields[change.Field] = true
	}
	for _, field := range []string{"key", "aliases", "title", "body", "lifecycle", "author", "assisting_agent", "change_message", "origin", "provenance"} {
		if !changedFields[field] {
			t.Errorf("semantic diff omitted changed field %q: %+v", field, diff.Fields)
		}
	}
	if len(diff.Fields) != 10 || len(diff.ReferencesAdded) != 1 || !reflect.DeepEqual(diff.ReferencesAdded[0], two) || len(diff.ReferencesRemoved) != 0 {
		t.Fatalf("semantic diff = %+v, want every changed field plus one added reference", diff)
	}

	blame, err := fixture.Module.Blame(ctx, a2.BlameRequest{BeadID: first.Address.BeadID, RevisionID: second.Address.RevisionID})
	if err != nil {
		t.Fatalf("semantic blame: %v", err)
	}
	if len(blame.Lines) != 2 || blame.Lines[0].RevisionID != first.Address.RevisionID || blame.Lines[1].RevisionID != second.Address.RevisionID {
		t.Fatalf("line blame = %+v, want unchanged line at first revision and replacement at second", blame.Lines)
	}
	fields := make(map[string]a2.RevisionID, len(blame.Fields))
	for _, field := range blame.Fields {
		fields[field.Field] = field.RevisionID
	}
	for _, field := range []string{"key", "aliases", "title", "lifecycle", "outgoing_references", "origin", "provenance"} {
		if fields[field] != second.Address.RevisionID {
			t.Errorf("field blame[%q] = %q, want second revision %q", field, fields[field], second.Address.RevisionID)
		}
	}

	history, err := fixture.Module.History(ctx, a2.HistoryRequest{BeadID: first.Address.BeadID})
	if err != nil || !history.Complete || len(history.Revisions) != 2 {
		t.Fatalf("history = %+v error %v, want two complete revisions", history, err)
	}
	for _, summary := range history.Revisions {
		if _, err := fixture.Module.Read(ctx, a2.ReadRequest{BeadID: first.Address.BeadID, RevisionID: summary.Address.RevisionID}); err != nil {
			t.Fatalf("history revision %q is not exactly readable: %v", summary.Address.RevisionID, err)
		}
	}
}

func runConflictHasNoGuessedHead(t *testing.T, fixture Fixture) {
	t.Helper()
	if fixture.Branches == nil {
		t.Skip("provider does not expose branch operations")
	}
	ctx := context.Background()
	base := apply(t, ctx, fixture.Module, a2.Mutation{Lifecycle: a2.LifecycleActive, Body: "base", Author: "Ada"})
	otherBase := apply(t, ctx, fixture.Module, a2.Mutation{Lifecycle: a2.LifecycleActive, Body: "other base", Author: "Ada"})
	if err := fixture.Branches.Fork("right"); err != nil {
		t.Fatalf("fork: %v", err)
	}
	left := apply(t, ctx, fixture.Module, a2.Mutation{
		BeadID: base.Address.BeadID, ExpectedCurrent: base.Address.RevisionID,
		Lifecycle: a2.LifecycleActive, Body: "left", Author: "Left Author",
	})
	apply(t, ctx, fixture.Module, a2.Mutation{
		BeadID: otherBase.Address.BeadID, ExpectedCurrent: otherBase.Address.RevisionID,
		Lifecycle: a2.LifecycleActive, Body: "other left", Author: "Left Author",
	})
	if err := fixture.Branches.Checkout("right"); err != nil {
		t.Fatalf("checkout right: %v", err)
	}
	right := apply(t, ctx, fixture.Module, a2.Mutation{
		BeadID: base.Address.BeadID, ExpectedCurrent: base.Address.RevisionID,
		Lifecycle: a2.LifecycleActive, Body: "right", Author: "Right Author",
	})
	apply(t, ctx, fixture.Module, a2.Mutation{
		BeadID: otherBase.Address.BeadID, ExpectedCurrent: otherBase.Address.RevisionID,
		Lifecycle: a2.LifecycleActive, Body: "other right", Author: "Right Author",
	})
	if err := fixture.Branches.Checkout("main"); err != nil {
		t.Fatalf("checkout main: %v", err)
	}
	if err := fixture.Branches.Merge("right"); err != nil {
		t.Fatalf("merge divergent branch: %v", err)
	}

	_, err := fixture.Module.Read(ctx, a2.ReadRequest{BeadID: base.Address.BeadID})
	conflict := requireConflict(t, err)
	if conflict.BeadID != base.Address.BeadID {
		t.Fatalf("current-read conflict bead = %q, want %q", conflict.BeadID, base.Address.BeadID)
	}
	wantHeads := []a2.RevisionID{left.Address.RevisionID, right.Address.RevisionID}
	if !sameRevisionSet(conflict.Heads, wantHeads) {
		t.Fatalf("conflict heads = %v, want %v", conflict.Heads, wantHeads)
	}

	var firstSearchConflict *a2.ConflictError
	for attempt := 0; attempt < 5; attempt++ {
		if _, err := fixture.Module.Search(ctx, a2.SearchRequest{Query: "left"}); err == nil {
			t.Fatal("unqualified search guessed through a conflicted current view")
		} else {
			got := requireConflict(t, err)
			if got.BeadID == "" {
				t.Fatal("search conflict omitted the conflicted bead identity")
			}
			if firstSearchConflict == nil {
				firstSearchConflict = got
			} else if !reflect.DeepEqual(got, firstSearchConflict) {
				t.Fatalf("search conflict changed across identical reads: first %+v, later %+v", firstSearchConflict, got)
			}
		}
	}
	before := fixture.Publication.RevisionIDs(base.Address.BeadID)
	result, err := fixture.Module.Mutate(ctx, a2.Mutation{
		BeadID: base.Address.BeadID, ExpectedCurrent: left.Address.RevisionID,
		Lifecycle: a2.LifecycleActive, Body: "pretend left won", Author: "Ada",
	})
	if result.Outcome != a2.OutcomeRejected {
		t.Fatalf("mutation against one competing head outcome = %q, want rejected", result.Outcome)
	}
	_ = requireConflict(t, err)
	if got := fixture.Publication.RevisionIDs(base.Address.BeadID); !reflect.DeepEqual(got, before) {
		t.Fatalf("conflicted mutation changed revisions: before %v, after %v", before, got)
	}

	for _, exact := range []a2.Revision{left, right} {
		got, err := fixture.Module.Read(ctx, a2.ReadRequest{BeadID: exact.Address.BeadID, RevisionID: exact.Address.RevisionID})
		if err != nil || !reflect.DeepEqual(got, exact) {
			t.Fatalf("exact competing revision %q = %+v error %v, want %+v", exact.Address.RevisionID, got, err, exact)
		}
	}
	if fixture.Reconstruct != nil {
		if fixture.Maintain != nil {
			fixture.Maintain()
		}
		fresh := fixture.Reconstruct()
		if fresh.Module == nil {
			t.Fatal("reconstruction fixture returned no fresh Module")
		}
		_, err := fresh.Module.Read(ctx, a2.ReadRequest{BeadID: base.Address.BeadID})
		reconstructed := requireConflict(t, err)
		if reconstructed.BeadID != base.Address.BeadID || !sameRevisionSet(reconstructed.Heads, wantHeads) {
			t.Fatalf("reconstructed conflict = %+v, want bead %q heads %v", reconstructed, base.Address.BeadID, wantHeads)
		}
		for _, exact := range []a2.Revision{left, right} {
			got, err := fresh.Module.Read(ctx, a2.ReadRequest{BeadID: exact.Address.BeadID, RevisionID: exact.Address.RevisionID})
			if err != nil || !reflect.DeepEqual(got, exact) {
				t.Fatalf("reconstructed exact competing revision %q = %+v error %v, want %+v", exact.Address.RevisionID, got, err, exact)
			}
		}
	}
	history, err := fixture.Module.History(ctx, a2.HistoryRequest{BeadID: base.Address.BeadID})
	if err != nil || len(history.Revisions) != 3 {
		t.Fatalf("history over competing roots = %+v error %v", history, err)
	}
}

func runContinuationIsSnapshotAndScopeBound(t *testing.T, fixture Fixture) {
	t.Helper()
	ctx := context.Background()

	// History is frozen at the first page even when a newer revision appears.
	first := apply(t, ctx, fixture.Module, a2.Mutation{Lifecycle: a2.LifecycleActive, Body: "history one", Author: "Ada"})
	second := apply(t, ctx, fixture.Module, a2.Mutation{
		BeadID: first.Address.BeadID, ExpectedCurrent: first.Address.RevisionID,
		Lifecycle: a2.LifecycleActive, Body: "history two", Author: "Ada",
	})
	third := apply(t, ctx, fixture.Module, a2.Mutation{
		BeadID: first.Address.BeadID, ExpectedCurrent: second.Address.RevisionID,
		Lifecycle: a2.LifecycleActive, Body: "history three", Author: "Ada",
	})
	historyBefore, err := fixture.Module.History(ctx, a2.HistoryRequest{BeadID: first.Address.BeadID})
	if err != nil || !historyBefore.Complete {
		t.Fatalf("unbounded history before paging = %+v error %v", historyBefore, err)
	}
	page1, err := fixture.Module.History(ctx, a2.HistoryRequest{BeadID: first.Address.BeadID, Limit: 2})
	if err != nil || page1.Complete || page1.Continuation == "" {
		t.Fatalf("first history page = %+v error %v", page1, err)
	}
	fourth := apply(t, ctx, fixture.Module, a2.Mutation{
		BeadID: first.Address.BeadID, ExpectedCurrent: third.Address.RevisionID,
		Lifecycle: a2.LifecycleActive, Body: "history four", Author: "Ada",
	})
	_ = fourth
	if fixture.Maintain != nil {
		fixture.Maintain()
	}
	page2, err := fixture.Module.History(ctx, a2.HistoryRequest{BeadID: first.Address.BeadID, Limit: 2, Continuation: page1.Continuation})
	if err != nil || !page2.Complete {
		t.Fatalf("continued history = %+v error %v", page2, err)
	}
	gotHistory := revisionIDs(append(append([]a2.RevisionSummary(nil), page1.Revisions...), page2.Revisions...))
	wantHistory := revisionIDs(historyBefore.Revisions)
	if !reflect.DeepEqual(gotHistory, wantHistory) {
		t.Fatalf("history walk mixed snapshots: got %v, want %v", gotHistory, wantHistory)
	}
	if _, err := fixture.Module.History(ctx, a2.HistoryRequest{BeadID: first.Address.BeadID, Limit: 1, Continuation: page1.Continuation}); !errors.Is(err, a2.ErrInvalidContinuation) {
		t.Fatalf("history continuation reused with another page contract = %v, want ErrInvalidContinuation", err)
	}

	// Search snapshots summaries rather than re-running the query at each page.
	var searchable []a2.Revision
	for _, body := range []string{"needle alpha", "needle beta", "needle gamma", "needle delta"} {
		searchable = append(searchable, apply(t, ctx, fixture.Module, a2.Mutation{Lifecycle: a2.LifecycleActive, Body: body, Author: "Ada"}))
	}
	fullBefore, err := fixture.Module.Search(ctx, a2.SearchRequest{Query: "needle"})
	if err != nil || !fullBefore.Complete {
		t.Fatalf("unbounded search before paging = %+v error %v", fullBefore, err)
	}
	search1, err := fixture.Module.Search(ctx, a2.SearchRequest{Query: "needle", Limit: 2})
	if err != nil || search1.Complete || search1.Continuation == "" {
		t.Fatalf("first search page = %+v error %v", search1, err)
	}
	if fixture.Branches != nil {
		if err := fixture.Branches.Fork("cursor-scope"); err != nil {
			t.Fatalf("fork cursor scope: %v", err)
		}
		if err := fixture.Branches.Checkout("cursor-scope"); err != nil {
			t.Fatalf("checkout cursor scope: %v", err)
		}
		if _, err := fixture.Module.Search(ctx, a2.SearchRequest{Query: "needle", Limit: 2, Continuation: search1.Continuation}); !errors.Is(err, a2.ErrInvalidContinuation) {
			t.Fatalf("search continuation reused in another view = %v, want ErrInvalidContinuation", err)
		}
		if err := fixture.Branches.Checkout("main"); err != nil {
			t.Fatalf("restore cursor scope: %v", err)
		}
		if err := fixture.Branches.DeleteBranch("cursor-scope"); err != nil {
			t.Fatalf("delete cursor scope: %v", err)
		}
	}
	changed := searchable[len(searchable)-1]
	apply(t, ctx, fixture.Module, a2.Mutation{
		BeadID: changed.Address.BeadID, ExpectedCurrent: changed.Address.RevisionID,
		Lifecycle: changed.Lifecycle, Body: "no longer matches", Author: "Ada",
	})
	apply(t, ctx, fixture.Module, a2.Mutation{Lifecycle: a2.LifecycleActive, Body: "needle added later", Author: "Ada"})
	searchRest := drainSearch(t, ctx, fixture.Module, a2.SearchRequest{Query: "needle", Limit: 2, Continuation: search1.Continuation})
	gotSearch := append(append([]a2.SearchSummary(nil), search1.Memories...), searchRest...)
	if !reflect.DeepEqual(gotSearch, fullBefore.Memories) {
		t.Fatalf("search walk mixed snapshots:\n got  %+v\n want %+v", gotSearch, fullBefore.Memories)
	}
	if _, err := fixture.Module.Search(ctx, a2.SearchRequest{Query: "different", Limit: 2, Continuation: search1.Continuation}); !errors.Is(err, a2.ErrInvalidContinuation) {
		t.Fatalf("search continuation reused with another query = %v, want ErrInvalidContinuation", err)
	}

	// High-degree outgoing references are bound to the exact source revision,
	// not to whichever revision is current when a later page is requested.
	refs := []a2.Reference{localTask("ref-5"), localTask("ref-1"), localTask("ref-4"), localTask("ref-2"), localTask("ref-3")}
	withRefs := apply(t, ctx, fixture.Module, a2.Mutation{Lifecycle: a2.LifecycleActive, Body: "many refs", References: refs, Author: "Ada"})
	refsBefore, err := fixture.Module.References(ctx, a2.ReferencesRequest{BeadID: withRefs.Address.BeadID, RevisionID: withRefs.Address.RevisionID})
	if err != nil || !refsBefore.Complete {
		t.Fatalf("unbounded references before paging = %+v error %v", refsBefore, err)
	}
	ref1, err := fixture.Module.References(ctx, a2.ReferencesRequest{BeadID: withRefs.Address.BeadID, RevisionID: withRefs.Address.RevisionID, Limit: 2})
	if err != nil || ref1.Complete || ref1.Continuation == "" {
		t.Fatalf("first reference page = %+v error %v", ref1, err)
	}
	newCurrent := apply(t, ctx, fixture.Module, a2.Mutation{
		BeadID: withRefs.Address.BeadID, ExpectedCurrent: withRefs.Address.RevisionID,
		Lifecycle: withRefs.Lifecycle, Body: withRefs.Body, References: []a2.Reference{localTask("replacement")}, Author: "Ada",
	})
	refRest := drainReferences(t, ctx, fixture.Module, a2.ReferencesRequest{
		BeadID: withRefs.Address.BeadID, RevisionID: withRefs.Address.RevisionID,
		Limit: 2, Continuation: ref1.Continuation,
	})
	gotRefs := append(append([]a2.Reference(nil), ref1.References...), refRest...)
	if !reflect.DeepEqual(gotRefs, refsBefore.References) {
		t.Fatalf("reference walk mixed current revisions: got %+v, want %+v", gotRefs, refsBefore.References)
	}
	if _, err := fixture.Module.References(ctx, a2.ReferencesRequest{
		BeadID: withRefs.Address.BeadID, RevisionID: newCurrent.Address.RevisionID,
		Limit: 2, Continuation: ref1.Continuation,
	}); !errors.Is(err, a2.ErrInvalidContinuation) {
		t.Fatalf("reference continuation reused against another source revision = %v, want ErrInvalidContinuation", err)
	}
}

func runContinuationRejectsAnotherProviderInstance(t *testing.T, left, right Fixture) {
	t.Helper()
	ctx := context.Background()
	for _, fixture := range []Fixture{left, right} {
		for _, body := range []string{"scope needle one", "scope needle two", "scope needle three"} {
			apply(t, ctx, fixture.Module, a2.Mutation{Lifecycle: a2.LifecycleActive, Body: body, Author: "Ada"})
		}
	}
	leftPage, err := left.Module.Search(ctx, a2.SearchRequest{Query: "scope needle", Limit: 1})
	if err != nil || leftPage.Continuation == "" {
		t.Fatalf("left provider first page = %+v error %v", leftPage, err)
	}
	rightPage, err := right.Module.Search(ctx, a2.SearchRequest{Query: "scope needle", Limit: 1})
	if err != nil || rightPage.Continuation == "" {
		t.Fatalf("right provider first page = %+v error %v", rightPage, err)
	}
	if leftPage.Continuation == rightPage.Continuation {
		t.Fatalf("provider instances minted the same opaque continuation %q", leftPage.Continuation)
	}
	if _, err := right.Module.Search(ctx, a2.SearchRequest{Query: "scope needle", Limit: 1, Continuation: leftPage.Continuation}); !errors.Is(err, a2.ErrInvalidContinuation) {
		t.Fatalf("right provider accepted left continuation: %v", err)
	}
	if _, err := left.Module.Search(ctx, a2.SearchRequest{Query: "scope needle", Limit: 1, Continuation: rightPage.Continuation}); !errors.Is(err, a2.ErrInvalidContinuation) {
		t.Fatalf("left provider accepted right continuation: %v", err)
	}
}

func apply(t *testing.T, ctx context.Context, module a2.Module, mutation a2.Mutation) a2.Revision {
	t.Helper()
	result, err := module.Mutate(ctx, mutation)
	if err != nil {
		t.Fatalf("Mutate(%+v): %v", mutation, err)
	}
	if result.Outcome != a2.OutcomeApplied || result.Revision == nil {
		t.Fatalf("Mutate(%+v) = %+v, want verified applied revision", mutation, result)
	}
	if result.Address != result.Revision.Address {
		t.Fatalf("mutation address %+v differs from verified revision address %+v", result.Address, result.Revision.Address)
	}
	return *result.Revision
}

func readCurrent(t *testing.T, ctx context.Context, module a2.Module, beadID a2.BeadID) a2.Revision {
	t.Helper()
	revision, err := module.Read(ctx, a2.ReadRequest{BeadID: beadID})
	if err != nil {
		t.Fatalf("read current %q: %v", beadID, err)
	}
	return revision
}

func assertExactReads(t *testing.T, ctx context.Context, module a2.Module, saved map[a2.RevisionID]a2.Revision) {
	t.Helper()
	for revisionID, want := range saved {
		got, err := module.Read(ctx, a2.ReadRequest{BeadID: want.Address.BeadID, RevisionID: revisionID})
		if err != nil {
			t.Fatalf("exact read %q: %v", revisionID, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("exact revision %q changed:\n got  %+v\n want %+v", revisionID, got, want)
		}
	}
}

func requireConflict(t *testing.T, err error) *a2.ConflictError {
	t.Helper()
	var conflict *a2.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want *ConflictError", err)
	}
	return conflict
}

func localTask(id string) a2.Reference {
	return a2.Reference{Target: a2.Target{
		Local:         true,
		BeadID:        a2.BeadID(id),
		ExpectedScope: "project",
		ExpectedKind:  a2.BeadKindTask,
	}}
}

func revisionIDs(revisions []a2.RevisionSummary) []a2.RevisionID {
	result := make([]a2.RevisionID, len(revisions))
	for i := range revisions {
		result[i] = revisions[i].Address.RevisionID
	}
	return result
}

func sameReferenceSet(left, right []a2.Reference) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[a2.Reference]int, len(left))
	for _, ref := range left {
		counts[ref]++
	}
	for _, ref := range right {
		counts[ref]--
		if counts[ref] < 0 {
			return false
		}
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

func sameRevisionSet(left, right []a2.RevisionID) bool {
	if len(left) != len(right) {
		return false
	}
	left = append([]a2.RevisionID(nil), left...)
	right = append([]a2.RevisionID(nil), right...)
	sort.Slice(left, func(i, j int) bool { return left[i] < left[j] })
	sort.Slice(right, func(i, j int) bool { return right[i] < right[j] })
	return reflect.DeepEqual(left, right)
}

func drainSearch(t *testing.T, ctx context.Context, module a2.Module, req a2.SearchRequest) []a2.SearchSummary {
	t.Helper()
	var result []a2.SearchSummary
	for {
		page, err := module.Search(ctx, req)
		if err != nil {
			t.Fatalf("continue search: %v", err)
		}
		result = append(result, page.Memories...)
		if page.Complete {
			return result
		}
		if page.Continuation == "" {
			t.Fatal("incomplete search page has no continuation")
		}
		req.Continuation = page.Continuation
	}
}

func drainReferences(t *testing.T, ctx context.Context, module a2.Module, req a2.ReferencesRequest) []a2.Reference {
	t.Helper()
	var result []a2.Reference
	for {
		page, err := module.References(ctx, req)
		if err != nil {
			t.Fatalf("continue references: %v", err)
		}
		result = append(result, page.References...)
		if page.Complete {
			return result
		}
		if page.Continuation == "" {
			t.Fatal("incomplete reference page has no continuation")
		}
		req.Continuation = page.Continuation
	}
}

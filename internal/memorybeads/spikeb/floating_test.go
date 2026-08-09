package spikeb

import "testing"

func TestFloatingModelComparisonOwnerProofSucceedsWithoutRouteOrder(t *testing.T) {
	proof := OwnerAuthorityProof{ProjectID: "target", Route: "owner", Verified: true}
	owner := FloatingObservation{
		Route: "owner", Status: ResolutionResolved, ProjectID: "target", Head: "owner-head",
		FreshnessProved: true, SynchronizationProved: true,
	}
	replica := FloatingObservation{
		Route: "replica", Status: ResolutionResolved, ProjectID: "target", Head: "replica-head",
		FreshnessProved: true, SynchronizationProved: true,
	}
	for _, observations := range [][]FloatingObservation{{owner, replica}, {replica, owner}} {
		comparison := CompareFloatingModels("target", proof, observations)
		assertFloatingResult(t, comparison, FloatingOwnerDesignatedModel, FloatingResolved, "owner-head")
		assertFloatingResult(t, comparison, FloatingConsensusModel, FloatingAmbiguous, "")
	}
}

func TestFloatingModelComparisonStaleOwnerFailsClosed(t *testing.T) {
	proof := OwnerAuthorityProof{ProjectID: "target", Route: "owner", Verified: true}
	observations := []FloatingObservation{
		{
			Route: "owner", Status: ResolutionResolved, ProjectID: "target", Head: "old-head",
			FreshnessProved: false, SynchronizationProved: true,
		},
		{
			Route: "replica", Status: ResolutionResolved, ProjectID: "target", Head: "newer-looking-head",
			FreshnessProved: true, SynchronizationProved: true,
		},
	}
	comparison := CompareFloatingModels("target", proof, observations)
	assertFloatingResult(t, comparison, FloatingOwnerDesignatedModel, FloatingFreshnessUnproved, "")
	assertFloatingResult(t, comparison, FloatingConsensusModel, FloatingFreshnessUnproved, "")
}

func TestFloatingModelComparisonForkFailsClosed(t *testing.T) {
	proof := OwnerAuthorityProof{ProjectID: "target", Route: "owner", Verified: true}
	observations := []FloatingObservation{{
		Route: "owner", Status: ResolutionResolved, ProjectID: "target", Head: "fork-left",
		CompetingHeads: []RevisionID{"fork-right"}, FreshnessProved: true, SynchronizationProved: true,
	}}
	comparison := CompareFloatingModels("target", proof, observations)
	assertFloatingResult(t, comparison, FloatingOwnerDesignatedModel, FloatingForked, "")
	assertFloatingResult(t, comparison, FloatingConsensusModel, FloatingForked, "")
}

func TestFloatingModelComparisonRejectsIdentityMismatch(t *testing.T) {
	proof := OwnerAuthorityProof{ProjectID: "target", Route: "owner", Verified: true}
	observations := []FloatingObservation{{
		Route: "owner", Status: ResolutionResolved, ProjectID: "impostor", Head: "head",
		FreshnessProved: true, SynchronizationProved: true,
	}}
	comparison := CompareFloatingModels("target", proof, observations)
	assertFloatingResult(t, comparison, FloatingOwnerDesignatedModel, FloatingIdentityMismatch, "")
	assertFloatingResult(t, comparison, FloatingConsensusModel, FloatingIdentityMismatch, "")
}

func TestFloatingSynchronizedConsensusStillFailsWithoutAuthority(t *testing.T) {
	observations := []FloatingObservation{
		{
			Route: "one", Status: ResolutionResolved, ProjectID: "target", Head: "same",
			FreshnessProved: true, SynchronizationProved: true,
		},
		{
			Route: "two", Status: ResolutionResolved, ProjectID: "target", Head: "same",
			FreshnessProved: true, SynchronizationProved: true,
		},
	}
	comparison := CompareFloatingModels("target", OwnerAuthorityProof{}, observations)
	assertFloatingResult(t, comparison, FloatingOwnerDesignatedModel, FloatingAuthorityUnproved, "")
	assertFloatingResult(t, comparison, FloatingConsensusModel, FloatingAuthorityUnproved, "")
}

func TestFloatingConsensusNeedsSynchronizationBeforeAuthorityQuestion(t *testing.T) {
	observations := []FloatingObservation{{
		Route: "replica", Status: ResolutionResolved, ProjectID: "target", Head: "head",
		FreshnessProved: true, SynchronizationProved: false,
	}}
	result := EvaluateSynchronizedConsensusFloating("target", observations)
	if result.Status != FloatingSyncUnproved || result.Head != "" {
		t.Fatalf("unsynchronized consensus result = %+v", result)
	}
}

func TestFloatingDenialDoesNotFallThroughOrLeakInEitherModel(t *testing.T) {
	proof := OwnerAuthorityProof{ProjectID: "target", Route: "owner", Verified: true}
	denied := FloatingObservation{Route: "owner", Status: ResolutionDenied}
	revealing := FloatingObservation{
		Route: "replica", Status: ResolutionResolved, ProjectID: "target", Head: "secret-head",
		FreshnessProved: true, SynchronizationProved: true,
	}
	for _, observations := range [][]FloatingObservation{{denied, revealing}, {revealing, denied}} {
		comparison := CompareFloatingModels("target", proof, observations)
		assertFloatingResult(t, comparison, FloatingOwnerDesignatedModel, FloatingDenied, "")
		assertFloatingResult(t, comparison, FloatingConsensusModel, FloatingDenied, "")
	}
}

func assertFloatingResult(t *testing.T, comparison []FloatingModelEvaluation, model FloatingModel, status FloatingStatus, head RevisionID) {
	t.Helper()
	for _, evaluation := range comparison {
		if evaluation.Model != model {
			continue
		}
		if evaluation.Result.Status != status || evaluation.Result.Head != head {
			t.Fatalf("%s result = %+v, want status %q head %q", model, evaluation.Result, status, head)
		}
		return
	}
	t.Fatalf("comparison omitted model %q: %+v", model, comparison)
}

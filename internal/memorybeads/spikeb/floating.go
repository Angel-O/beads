package spikeb

// FloatingObservation is evidence returned by one experimental route. Revision
// IDs are opaque; their spelling and route order carry no recency authority.
type FloatingObservation struct {
	Route     string
	Status    ResolutionStatus
	ProjectID ProjectID
	Head      RevisionID
	// CompetingHeads contains observed alternatives to Head. Any entry proves
	// that the route cannot present one non-forked current head.
	CompetingHeads        []RevisionID
	FreshnessProved       bool
	SynchronizationProved bool
}

type FloatingStatus string

const (
	FloatingDenied            FloatingStatus = "denied"
	FloatingIdentityMismatch  FloatingStatus = "identity_mismatch"
	FloatingForked            FloatingStatus = "forked"
	FloatingFreshnessUnproved FloatingStatus = "freshness_unproved"
	FloatingSyncUnproved      FloatingStatus = "synchronization_unproved"
	FloatingAmbiguous         FloatingStatus = "ambiguous_routes"
	FloatingAuthorityUnproved FloatingStatus = "owner_authority_unproved"
	FloatingUnavailable       FloatingStatus = "unavailable"
	FloatingResolved          FloatingStatus = "resolved"
)

type FloatingResult struct {
	Status FloatingStatus
	Head   RevisionID
}

// OwnerAuthorityProof is the verified output of a provider-specific owner
// designation mechanism. This spike intentionally does not prescribe how the
// owner proves the designation; callers may trust it only when Verified is
// true and its Project ID matches the stored foreign identity.
type OwnerAuthorityProof struct {
	ProjectID ProjectID
	Route     string
	Verified  bool
}

type FloatingModel string

const (
	FloatingOwnerDesignatedModel FloatingModel = "owner_designated"
	FloatingConsensusModel       FloatingModel = "synchronized_consensus"
)

type FloatingModelEvaluation struct {
	Model  FloatingModel
	Result FloatingResult
}

// CompareFloatingModels runs the same observations through both candidate
// models. Fixed result ordering is presentation only; it never chooses a
// foreign head by route order.
func CompareFloatingModels(expected ProjectID, proof OwnerAuthorityProof, observations []FloatingObservation) []FloatingModelEvaluation {
	return []FloatingModelEvaluation{
		{Model: FloatingOwnerDesignatedModel, Result: EvaluateOwnerDesignatedFloating(expected, proof, observations)},
		{Model: FloatingConsensusModel, Result: EvaluateSynchronizedConsensusFloating(expected, observations)},
	}
}

// EvaluateOwnerDesignatedFloating can select a head only from the route that
// a verified owner proof designates. Replicas cannot outvote or replace it.
func EvaluateOwnerDesignatedFloating(expected ProjectID, proof OwnerAuthorityProof, observations []FloatingObservation) FloatingResult {
	if !proof.Verified || proof.Route == "" {
		return FloatingResult{Status: FloatingAuthorityUnproved}
	}
	if proof.ProjectID != expected {
		return FloatingResult{Status: FloatingIdentityMismatch}
	}
	var owner *FloatingObservation
	for index := range observations {
		if observations[index].Route != proof.Route {
			continue
		}
		if owner != nil {
			return FloatingResult{Status: FloatingAmbiguous}
		}
		owner = &observations[index]
	}
	if owner == nil || owner.Status == ResolutionUnavailable || owner.Status == ResolutionUnconfigured {
		return FloatingResult{Status: FloatingUnavailable}
	}
	if owner.Status == ResolutionDenied {
		return FloatingResult{Status: FloatingDenied}
	}
	if owner.Status != ResolutionResolved {
		return FloatingResult{Status: FloatingUnavailable}
	}
	if owner.ProjectID != expected {
		return FloatingResult{Status: FloatingIdentityMismatch}
	}
	if len(owner.CompetingHeads) != 0 {
		return FloatingResult{Status: FloatingForked}
	}
	if !owner.FreshnessProved {
		return FloatingResult{Status: FloatingFreshnessUnproved}
	}
	if owner.Head == "" {
		return FloatingResult{Status: FloatingUnavailable}
	}
	return FloatingResult{Status: FloatingResolved, Head: owner.Head}
}

// EvaluateSynchronizedConsensusFloating tests the alternate hypothesis that
// synchronized, agreeing routes can define current state without a designated
// owner. It fails closed: synchronization can establish replica agreement but
// cannot establish which authority is entitled to choose the project head.
func EvaluateSynchronizedConsensusFloating(expected ProjectID, observations []FloatingObservation) FloatingResult {
	for _, observation := range observations {
		if observation.Status == ResolutionDenied {
			// Do not fall through to another route and leak whether a head exists.
			return FloatingResult{Status: FloatingDenied}
		}
	}
	for _, observation := range observations {
		if observation.Status == ResolutionUnavailable || observation.Status == ResolutionUnconfigured {
			return FloatingResult{Status: FloatingUnavailable}
		}
	}
	for _, observation := range observations {
		if observation.ProjectID != "" && observation.ProjectID != expected {
			return FloatingResult{Status: FloatingIdentityMismatch}
		}
	}
	for _, observation := range observations {
		if len(observation.CompetingHeads) != 0 {
			return FloatingResult{Status: FloatingForked}
		}
	}
	for _, observation := range observations {
		if observation.Status == ResolutionResolved && !observation.FreshnessProved {
			return FloatingResult{Status: FloatingFreshnessUnproved}
		}
	}
	var head RevisionID
	resolved := 0
	for _, observation := range observations {
		if observation.Status != ResolutionResolved {
			continue
		}
		resolved++
		if !observation.SynchronizationProved {
			return FloatingResult{Status: FloatingSyncUnproved}
		}
		if head != "" && observation.Head != head {
			return FloatingResult{Status: FloatingAmbiguous}
		}
		head = observation.Head
	}
	if resolved == 0 || head == "" {
		return FloatingResult{Status: FloatingUnavailable}
	}
	// Even unanimous, fresh-looking replicas do not prove which provider owns
	// the authoritative project view. That policy needs separate evidence.
	return FloatingResult{Status: FloatingAuthorityUnproved}
}

// EvaluateFloating retains the original experiment entry point while making
// its tested model explicit.
func EvaluateFloating(expected ProjectID, observations []FloatingObservation) FloatingResult {
	return EvaluateSynchronizedConsensusFloating(expected, observations)
}

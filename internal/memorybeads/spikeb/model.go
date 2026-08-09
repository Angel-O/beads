// Package spikeb contains executable, throwaway prototypes for Memory Beads
// Harness B. Nothing in this package is a production API or storage design.
package spikeb

import (
	"errors"
	"fmt"
	"sort"
)

// PROTOTYPE: these names describe only the semantic state exercised by Harness
// B. They deliberately do not alias the public Module spike or a storage type.
type (
	ProjectID  string
	BeadID     string
	RevisionID string
	Kind       string
	Lifecycle  string
)

const (
	KindTask   Kind = "task"
	KindMemory Kind = "memory"

	LifecycleActive   Lifecycle = "active"
	LifecycleArchived Lifecycle = "archived"

	InterchangeFormat  = "memory-beads"
	InterchangeVersion = "b1-current-state-v1"
	InterchangeScope   = "project-current-state"

	CapabilityAtomicConnected = "atomic-connected-graph"
	CapabilityExactPins       = "exact-memory-pins"

	// CurrentRevision is fixture input shorthand. Providers resolve it before
	// any state becomes observable; it is invalid in an interchange unit.
	CurrentRevision RevisionID = "@current"
)

var (
	ErrInvalid                = errors.New("invalid Harness B prototype request")
	ErrUnsupportedDeclaration = errors.New("unsupported interchange declaration")
	ErrLegacyRejected         = errors.New("legacy importer rejected canonical interchange")
	ErrConflict               = errors.New("destination conflict")
	ErrStaleDestination       = errors.New("destination changed after preflight")
	ErrInjectedFailure        = errors.New("injected failure before publication")
)

// Address is an exact Memory Bead address. A task has no RevisionID.
type Address struct {
	ProjectID  ProjectID  `json:"project_id"`
	BeadID     BeadID     `json:"bead_id"`
	RevisionID RevisionID `json:"revision_id,omitempty"`
}

// RevisionEvidence keeps source attribution distinct from the accountable
// destination revision created by import.
type RevisionEvidence struct {
	Address
	Author         string `json:"author,omitempty"`
	AssistingAgent string `json:"assisting_agent,omitempty"`
	ChangeMessage  string `json:"change_message,omitempty"`
	Origin         string `json:"origin,omitempty"`
}

// Reference is stored source state, not a resolution observation. Local means
// the target belongs to the source project; otherwise ProjectID is required.
type Reference struct {
	Local         bool       `json:"local"`
	ProjectID     ProjectID  `json:"project_id,omitempty"`
	BeadID        BeadID     `json:"bead_id"`
	RevisionID    RevisionID `json:"revision_id,omitempty"`
	ExpectedScope string     `json:"expected_scope"`
	ExpectedKind  Kind       `json:"expected_kind"`
}

// Record is the deliberately small current-state profile exercised by B1. It
// contains enough task and memory state to prove reference remapping, archive
// state, optional keys, exact pins, and source provenance.
type Record struct {
	ID             BeadID             `json:"id"`
	Kind           Kind               `json:"kind"`
	RevisionID     RevisionID         `json:"revision_id,omitempty"`
	Key            string             `json:"key,omitempty"`
	Title          string             `json:"title"`
	Body           string             `json:"body"`
	Lifecycle      Lifecycle          `json:"lifecycle,omitempty"`
	References     []Reference        `json:"references,omitempty"`
	Author         string             `json:"author,omitempty"`
	AssistingAgent string             `json:"assisting_agent,omitempty"`
	ChangeMessage  string             `json:"change_message,omitempty"`
	Origin         string             `json:"origin,omitempty"`
	Provenance     []RevisionEvidence `json:"provenance,omitempty"`
}

// Declaration precedes records so an old or unsupported importer can reject
// the unit before applying data.
type Declaration struct {
	Format               string   `json:"format"`
	Version              string   `json:"version"`
	Scope                string   `json:"scope"`
	RequiredCapabilities []string `json:"required_capabilities"`
}

// InterchangeUnit is one connected, atomic current-state transfer.
type InterchangeUnit struct {
	Declaration     Declaration `json:"declaration"`
	SourceProjectID ProjectID   `json:"source_project_id"`
	Records         []Record    `json:"records"`
}

// CanonicalDeclaration returns the one declaration this prototype accepts.
func CanonicalDeclaration() Declaration {
	return Declaration{
		Format:               InterchangeFormat,
		Version:              InterchangeVersion,
		Scope:                InterchangeScope,
		RequiredCapabilities: []string{CapabilityAtomicConnected, CapabilityExactPins},
	}
}

func cloneRecord(in Record) Record {
	out := in
	out.References = append([]Reference(nil), in.References...)
	out.Provenance = append([]RevisionEvidence(nil), in.Provenance...)
	return out
}

func cloneRecords(in []Record) []Record {
	out := make([]Record, len(in))
	for i := range in {
		out[i] = cloneRecord(in[i])
	}
	return out
}

func recordsFromMap(in map[BeadID]Record) []Record {
	ids := make([]string, 0, len(in))
	for id := range in {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	out := make([]Record, 0, len(ids))
	for _, id := range ids {
		out = append(out, cloneRecord(in[BeadID(id)]))
	}
	return out
}

func recordsByID(records []Record) (map[BeadID]Record, error) {
	out := make(map[BeadID]Record, len(records))
	for _, record := range records {
		if record.ID == "" {
			return nil, fmt.Errorf("%w: record ID is required", ErrInvalid)
		}
		if _, exists := out[record.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate record %q", ErrInvalid, record.ID)
		}
		out[record.ID] = cloneRecord(record)
	}
	return out, nil
}

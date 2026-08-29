package issueops

import (
	"context"
	"encoding/json"

	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/journalops"
)

// Event and ProvenanceEvent are the durable database-backed history records
// carried by SnapshotImportBundle.
type Event = types.Event
type ProvenanceEvent = types.ProvenanceEvent
type SnapshotHistoryRow = journalops.Row

// SnapshotImportMode controls how destination issue IDs are handled.
type SnapshotImportMode string

const (
	// SnapshotCreateOnly refuses if any destination issue ID already exists.
	SnapshotCreateOnly SnapshotImportMode = "create_only"
	// SnapshotReplace replaces the named issue aggregates while leaving unrelated
	// destination issues untouched. Existing history is append-only.
	SnapshotReplace SnapshotImportMode = "replace"
)

// SnapshotIDMap contains the caller-owned identity mapping for a snapshot.
// Every source issue and every audit interaction must have a destination ID.
// Other issue references are remapped through Issues; external references are
// preserved by the importer.
type SnapshotIDMap struct {
	Issues            map[string]string
	AuditInteractions map[string]string
}

// SnapshotImportBundle is the complete database portion of a snapshot plus an
// optional, not-yet-installed audit sidecar. Issues carry their labels,
// dependencies, and comments in the same form used by the existing interchange
// model. History and provenance are copied as records, not regenerated.
type SnapshotImportBundle struct {
	Issues                 []*Issue
	History                []SnapshotHistoryRow
	Events                 []*Event
	Provenance             []ProvenanceEvent
	AuditInteractionsJSONL []byte
	MigrationMarker        string
}

// SnapshotImportRequest is the explicit snapshot-import operation. It is
// intentionally separate from ImportBatch: this operation has no stale-upsert
// behavior, requires caller-supplied destination IDs, and can replace named
// aggregates in a non-empty destination.
type SnapshotImportRequest struct {
	Bundle SnapshotImportBundle
	IDs    SnapshotIDMap
	Mode   SnapshotImportMode
}

// SnapshotImportResult reports the committed database operation and the exact
// canonical sidecar payload that a higher orchestration layer may install. The
// Beads layer never writes the live interactions JSONL file.
type SnapshotImportResult struct {
	Digest                  string
	Applied                 bool
	IssuesImported          int
	HistoryImported         int
	EventsImported          int
	ProvenanceImported      int
	MigrationMarker         string
	StagedAuditInteractions []json.RawMessage
	StagedAuditJSONL        []byte
}

// SnapshotImportMetadataPrefix is the durable metadata namespace used for
// idempotency markers. The marker is written in the same transaction as the
// imported database records.
const SnapshotImportMetadataPrefix = "snapshot-import/"

// SnapshotImportMarkerKey returns the durable marker key for a digest.
func SnapshotImportMarkerKey(digest string) string {
	return SnapshotImportMetadataPrefix + digest
}

// SnapshotImporter is the database half of a snapshot copy. The caller owns
// any recoverable logical commit that installs StagedAuditJSONL and its ledger.
type SnapshotImporter interface {
	ImportSnapshot(ctx context.Context, request SnapshotImportRequest) (SnapshotImportResult, error)
}

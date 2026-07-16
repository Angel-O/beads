package storage

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"time"
)

// BackendMigrationCellKind describes the canonical representation used for a
// physical SQL cell while moving current state between storage providers.
// Keeping NULL distinct from an empty string is required for audit fidelity.
type BackendMigrationCellKind string

const (
	BackendMigrationCellNull    BackendMigrationCellKind = "null"
	BackendMigrationCellText    BackendMigrationCellKind = "text"
	BackendMigrationCellInteger BackendMigrationCellKind = "integer"
	BackendMigrationCellTime    BackendMigrationCellKind = "time"
)

// BackendMigrationCell is a typed, provider-neutral SQL value. Time values are
// UTC RFC3339Nano strings; Dolt's current schema stores whole-second DATETIME
// values, but retaining the canonical format keeps the seam future-safe.
type BackendMigrationCell struct {
	Kind  BackendMigrationCellKind `json:"kind"`
	Value string                   `json:"value"`
}

type BackendMigrationTable struct {
	Name string                   `json:"name"`
	Rows [][]BackendMigrationCell `json:"rows"`
}

// BackendMigrationSnapshot is the exact portable-current-state manifest.
// Dolt commits, branches, remotes, and provider-owned schema bookkeeping are
// deliberately outside this value and remain in the preserved source.
type BackendMigrationSnapshot struct {
	Tables []BackendMigrationTable `json:"tables"`
}

func (s BackendMigrationSnapshot) Digest() (string, error) {
	digest := sha256.New()
	writeBackendMigrationDigestString(digest, "beads-backend-migration-snapshot-v1")
	writeBackendMigrationDigestUint64(digest, uint64(len(s.Tables)))
	for _, table := range s.Tables {
		writeBackendMigrationDigestString(digest, table.Name)
		writeBackendMigrationDigestUint64(digest, uint64(len(table.Rows)))
		for _, row := range table.Rows {
			writeBackendMigrationDigestUint64(digest, uint64(len(row)))
			for _, cell := range row {
				writeBackendMigrationDigestString(digest, string(cell.Kind))
				writeBackendMigrationDigestString(digest, cell.Value)
			}
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

type backendMigrationDigestWriter interface {
	Write([]byte) (int, error)
}

func writeBackendMigrationDigestString(writer backendMigrationDigestWriter, value string) {
	writeBackendMigrationDigestUint64(writer, uint64(len(value)))
	_, _ = writer.Write([]byte(value))
}

func writeBackendMigrationDigestUint64(writer backendMigrationDigestWriter, value uint64) {
	var framed [8]byte
	binary.BigEndian.PutUint64(framed[:], value)
	_, _ = writer.Write(framed[:])
}

func (s BackendMigrationSnapshot) RowCounts() map[string]int {
	counts := make(map[string]int, len(s.Tables))
	for _, table := range s.Tables {
		counts[table.Name] = len(table.Rows)
	}
	return counts
}

// ParseBackendMigrationTime decodes the canonical time representation used in
// BackendMigrationCell. It lives at the storage boundary so target providers
// do not each invent their own timestamp conversion.
func ParseBackendMigrationTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

// BackendMigrationPortabilityReport contains aggregate, non-sensitive reasons
// why a Dolt source cannot be represented as SQLite current state. It never
// includes issue IDs, user values, or database-provided identifiers.
type BackendMigrationPortabilityReport struct {
	MissingTransferredTables     int `json:"missing_transferred_tables,omitempty"`
	UnexpectedBaseTables         int `json:"unexpected_base_tables,omitempty"`
	MissingTransferredColumns    int `json:"missing_transferred_columns,omitempty"`
	UnexpectedTransferredColumns int `json:"unexpected_transferred_columns,omitempty"`
	OmittedSemanticRows          int `json:"omitted_semantic_rows,omitempty"`
	TierMismatchedIssues         int `json:"tier_mismatched_issues,omitempty"`
	NullEventTimestamps          int `json:"null_event_timestamps,omitempty"`
	CompactedIssues              int `json:"compacted_issues,omitempty"`
	IssueSnapshots               int `json:"issue_snapshots,omitempty"`
	CompactionSnapshots          int `json:"compaction_snapshots,omitempty"`
	ReservedMetadataCollisions   int `json:"reserved_metadata_collisions,omitempty"`
	BlockedStateInconsistencies  int `json:"blocked_state_inconsistencies,omitempty"`
	UnknownIssueColumns          int `json:"unknown_issue_columns,omitempty"`
	UnknownWispColumns           int `json:"unknown_wisp_columns,omitempty"`
}

func (r BackendMigrationPortabilityReport) Portable() bool {
	return r.MissingTransferredTables == 0 && r.UnexpectedBaseTables == 0 &&
		r.MissingTransferredColumns == 0 && r.UnexpectedTransferredColumns == 0 &&
		r.OmittedSemanticRows == 0 && r.NullEventTimestamps == 0 &&
		r.IssueSnapshots == 0 && r.CompactionSnapshots == 0 &&
		r.ReservedMetadataCollisions == 0 &&
		r.UnknownIssueColumns == 0 && r.UnknownWispColumns == 0
}

// BackendMigrationSource captures all portable state and runs its portability
// census under one provider transaction.
type BackendMigrationSource interface {
	SnapshotBackendMigration(ctx context.Context) (BackendMigrationSnapshot, BackendMigrationPortabilityReport, error)
}

// BackendMigrationTarget replaces the fresh target's application state and
// verifies the result in the same provider transaction.
type BackendMigrationTarget interface {
	RestoreBackendMigration(ctx context.Context, snapshot BackendMigrationSnapshot) error
}

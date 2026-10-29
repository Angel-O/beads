package storage

import (
	"context"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

const (
	// MaxBulkHistoryIDs bounds the bind parameters and result groups in one
	// BulkHistory query. Bulk history never chunks because one query is part of
	// its contract.
	MaxBulkHistoryIDs = 1000
	// MaxBulkHistoryIDLength matches the VARCHAR(255) issue ID schema bound.
	MaxBulkHistoryIDLength = types.MaxFieldLen
)

// HistoryViewer provides time-travel queries and diffs.
type HistoryViewer interface {
	History(ctx context.Context, issueID string) ([]*HistoryEntry, error)
	AsOf(ctx context.Context, issueID string, ref string) (*types.Issue, error)
	Diff(ctx context.Context, fromRef, toRef string) ([]*DiffEntry, error)
}

// BulkHistoryViewer provides one-query history reads for exact issue IDs.
type BulkHistoryViewer interface {
	// BulkHistory trims whitespace, ignores blank IDs, deduplicates and sorts
	// IDs lexically, then returns one IssueHistory group for each normalized ID.
	// It rejects more than MaxBulkHistoryIDs unique IDs or an ID longer than
	// MaxBulkHistoryIDLength characters before querying storage.
	BulkHistory(ctx context.Context, issueIDs []string) ([]IssueHistory, error)
}

// CompactBulkHistoryViewer returns distinct normalized issue states with all
// commit occurrences, without repeating issue bodies across the storage boundary.
type CompactBulkHistoryViewer interface {
	CompactBulkHistory(ctx context.Context, issueIDs []string, limit int) ([]CompactIssueHistory, error)
}

// CompactIssueHistory is one normalized requested ID and its distinct states.
type CompactIssueHistory struct {
	IssueID string                `json:"issue_id"`
	States  []CompactHistoryState `json:"states"`
}

// CompactHistoryState stores an issue body once alongside every occurrence.
type CompactHistoryState struct {
	Issue       *types.Issue        `json:"Issue"`
	Occurrences []HistoryOccurrence `json:"occurrences"`
}

// HistoryOccurrence retains the producer metadata for one historical snapshot.
type HistoryOccurrence struct {
	CommitHash string    `json:"CommitHash"`
	Committer  string    `json:"Committer"`
	CommitDate time.Time `json:"CommitDate"`
}

// ExternalRefHistoryQuerier is implemented by history-capable Dolt storage
// backends that can resolve what a given issue's external_ref was as of a
// point in time, by querying Dolt's dolt_history_issues system table.
//
// This is intentionally a separate capability from RawDBAccessor
// (storage.go): a backend can have Dolt's history tables without exposing a
// raw *sql.DB (EmbeddedDoltStore executes all SQL through its own
// transaction-scoped connection helper rather than a pooled *sql.DB), and a
// hypothetical non-Dolt SQL backend could conceivably expose a *sql.DB
// (e.g. for diagnostics) without ever having dolt_history_issues. Gating
// dolt_history_issues queries on this capability -- rather than on DB()
// presence -- keeps the fast path tied to the thing it actually depends on.
type ExternalRefHistoryQuerier interface {
	HistoryViewer

	// PreviousExternalRef returns the external_ref value recorded for
	// issueID as of the most recent commit at or before asOf.
	//
	// found is false when no history entry exists for issueID at or
	// before asOf (e.g. the issue was created after asOf); callers should
	// treat that as "changed" since there is nothing to compare against.
	// ref is the empty string when the historical row's external_ref
	// column was NULL.
	PreviousExternalRef(ctx context.Context, issueID string, asOf time.Time) (ref string, found bool, err error)
}

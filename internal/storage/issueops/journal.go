package issueops

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// The durable mutations journal records every committed issue mutation as a row
// in the clone-local bd_mutations_journal table, written in the SAME
// transaction as the mutation itself. Because the row and the mutation commit
// atomically, the journal can never lag the data, produce a false record, or
// collide on a sequence number: the seq is an engine-assigned AUTO_INCREMENT PK,
// serialized across every connection to the shared database.
//
// External tooling reads the journal through `bd mutations tail --since <seq>`
// and `bd mutations export`, replaying the exact mutation history of the
// workspace. See internal/storage/schema/migrations/0056_create_mutations_journal.up.sql.
//
// Emission lives here, at the issueops seam, because both write plumbings — the
// DoltStorage decorator chain and the unit-of-work path — bottom out in these
// *InTx functions. Instrumenting the seam makes coverage structural: every
// mutation path (including wisps, ready-claims, lease reclaim, renames, and
// cascade deletes) flows through it. TestEveryMutationFunctionJournals guards
// against a new mutation path silently skipping the journal.

// MutationOp names the kind of mutation a journal row records.
type MutationOp string

// The closed set of journalled mutation kinds.
const (
	MutationCreate    MutationOp = "create"
	MutationUpdate    MutationOp = "update"
	MutationClose     MutationOp = "close"
	MutationDelete    MutationOp = "delete"
	MutationDepAdd    MutationOp = "dep_add"
	MutationDepRemove MutationOp = "dep_remove"
)

// MutationDep is the edge payload recorded for dependency operations.
type MutationDep struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

// journalOn gates every emission. It is process-global because a bd process
// serves a single workspace; cmd/bd sets it from the mutations-journal config
// knob, and tests set it directly. Default OFF: when off, the *InTx emit helpers
// are a cheap no-op and no rows are written.
var journalOn atomic.Bool

// SetJournalEnabled turns mutation journaling on or off for this process.
func SetJournalEnabled(on bool) { journalOn.Store(on) }

// JournalEnabled reports whether mutation journaling is currently on.
func JournalEnabled() bool { return journalOn.Load() }

// RecordMutationInTx records op for issueID, snapshotting the issue's
// post-mutation state as of tx (read-your-writes within the same transaction).
// Use it for every op except delete (which has no surviving row — use
// RecordDeleteInTx) and dependency ops (use RecordDepMutationInTx). A no-op when
// journaling is disabled.
func RecordMutationInTx(ctx context.Context, tx DBTX, op MutationOp, issueID string) error {
	if !journalOn.Load() {
		return nil
	}
	issue, err := GetIssueInTx(ctx, tx, issueID)
	if err != nil {
		// The row should exist for a non-delete op; a missing row means the
		// mutation and the journal disagree, so fail the transaction rather than
		// record a hole.
		return fmt.Errorf("journal: snapshot %s for %s: %w", op, issueID, err)
	}
	return insertMutationRow(ctx, tx, op, issueID, issue, nil)
}

// RecordDeleteInTx records a delete for issueID with a null issue payload (the
// row no longer exists). A no-op when journaling is disabled.
func RecordDeleteInTx(ctx context.Context, tx DBTX, issueID string) error {
	if !journalOn.Load() {
		return nil
	}
	return insertMutationRow(ctx, tx, MutationDelete, issueID, nil, nil)
}

// RecordDepMutationInTx records a dependency add or remove for issueID, carrying
// the edge kind and target. The issue snapshot is the post-mutation state as of
// tx. A no-op when journaling is disabled.
func RecordDepMutationInTx(ctx context.Context, tx DBTX, op MutationOp, issueID, kind, target string) error {
	if !journalOn.Load() {
		return nil
	}
	issue, err := GetIssueInTx(ctx, tx, issueID)
	if err != nil {
		// The dependency source may itself have been deleted (cascade); record
		// the edge change with a null snapshot rather than failing.
		if errors.Is(err, storage.ErrNotFound) {
			return insertMutationRow(ctx, tx, op, issueID, nil, &MutationDep{Kind: kind, Target: target})
		}
		return fmt.Errorf("journal: snapshot %s for %s: %w", op, issueID, err)
	}
	return insertMutationRow(ctx, tx, op, issueID, issue, &MutationDep{Kind: kind, Target: target})
}

// insertMutationRow performs the actual INSERT. A nil issue is stored as SQL
// NULL (deletes); a nil dep is stored as SQL NULL (non-dependency ops).
func insertMutationRow(ctx context.Context, tx DBTX, op MutationOp, issueID string, issue *types.Issue, dep *MutationDep) error {
	var issueJSON any
	if issue != nil {
		b, err := json.Marshal(issue)
		if err != nil {
			return fmt.Errorf("journal: marshal issue %s: %w", issueID, err)
		}
		issueJSON = string(b)
	}
	var depJSON any
	if dep != nil {
		b, err := json.Marshal(dep)
		if err != nil {
			return fmt.Errorf("journal: marshal dep for %s: %w", issueID, err)
		}
		depJSON = string(b)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO bd_mutations_journal (ts, op, issue_id, issue_json, dep_json)
		VALUES (?, ?, ?, ?, ?)
	`, time.Now().UTC(), string(op), issueID, issueJSON, depJSON)
	if err != nil {
		return fmt.Errorf("journal: record %s for %s: %w", op, issueID, err)
	}
	return nil
}

// compile-time assurance that *sql.Tx satisfies DBTX (the emit helpers accept
// both *sql.Tx and *sql.DB via DBTX).
var _ DBTX = (*sql.Tx)(nil)

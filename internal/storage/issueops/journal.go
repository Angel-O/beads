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

// The durable events journal records every committed issue mutation as a row
// in the clone-local bd_events_journal table, written in the SAME
// transaction as the mutation itself. Because the row and the mutation commit
// atomically, the journal can never lag the data or produce a false record.
//
// The seq is NOT an AUTO_INCREMENT. AUTO_INCREMENT assigns a value at INSERT,
// not at commit, so under concurrent transactions (the shared SQL server)
// commit-visibility order can invert seq order: a lower seq can commit after a
// higher seq is already visible, and a consumer tailing WHERE seq > cursor
// would permanently skip it. Instead each seq is drawn from the single-row
// counter table bd_events_seq inside the mutation's own transaction (see
// nextEventSeq). The shared counter row makes concurrent allocators conflict,
// so exactly one commit order survives; the surviving seqs are gapless and
// commit-ordered (a rolled-back allocator burns no seq — the increment rolls
// back with it). This holds on both of bd's Dolt concurrency models: the SQL
// server aborts the losing commit with a serialization error (retried by the
// write path), while the embedded engine serializes writers on the counter row.
//
// External tooling reads the journal through `bd events tail --since <seq>`
// and `bd events export`, replaying the exact mutation history of the
// workspace. See internal/storage/schema/migrations/0056_create_events_journal.up.sql.
//
// Emission lives here, at the issueops seam, because both write plumbings — the
// DoltStorage decorator chain and the unit-of-work path — bottom out in these
// *InTx functions, and both funnel their INSERT through insertEventRow, so
// the seq mechanism is shared by construction and cannot drift between plumbings.
// Instrumenting the seam makes coverage structural: every mutation path
// (including wisps, ready-claims, lease reclaim, renames, and cascade deletes)
// flows through it. TestEveryMutationFunctionJournals guards against a new
// mutation path silently skipping the journal.

// EventOp names the kind of mutation a journal row records.
type EventOp string

// The closed set of journalled mutation kinds.
const (
	EventCreate    EventOp = "create"
	EventUpdate    EventOp = "update"
	EventClose     EventOp = "close"
	EventDelete    EventOp = "delete"
	EventDepAdd    EventOp = "dep_add"
	EventDepRemove EventOp = "dep_remove"
)

// EventDep is the edge payload recorded for dependency operations.
type EventDep struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

// journalOn gates every emission. It is process-global because a bd process
// serves a single workspace; cmd/bd sets it from the events-journal config
// knob, and tests set it directly. Default OFF: when off, the *InTx emit helpers
// are a cheap no-op and no rows are written.
var journalOn atomic.Bool

// SetJournalEnabled turns events journaling on or off for this process.
func SetJournalEnabled(on bool) { journalOn.Store(on) }

// JournalEnabled reports whether events journaling is currently on.
func JournalEnabled() bool { return journalOn.Load() }

// RecordEventInTx records op for issueID, snapshotting the issue's
// post-mutation state as of tx (read-your-writes within the same transaction).
// Use it for every op except delete (which has no surviving row — use
// RecordDeleteInTx) and dependency ops (use RecordDepEventInTx). A no-op when
// journaling is disabled.
func RecordEventInTx(ctx context.Context, tx DBTX, op EventOp, issueID string) error {
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
	return insertEventRow(ctx, tx, op, issueID, issue, nil)
}

// RecordDeleteInTx records a delete for issueID with a null issue payload (the
// row no longer exists). A no-op when journaling is disabled.
func RecordDeleteInTx(ctx context.Context, tx DBTX, issueID string) error {
	if !journalOn.Load() {
		return nil
	}
	return insertEventRow(ctx, tx, EventDelete, issueID, nil, nil)
}

// RecordDepEventInTx records a dependency add or remove for issueID, carrying
// the edge kind and target. The issue snapshot is the post-mutation state as of
// tx. A no-op when journaling is disabled.
func RecordDepEventInTx(ctx context.Context, tx DBTX, op EventOp, issueID, kind, target string) error {
	if !journalOn.Load() {
		return nil
	}
	issue, err := GetIssueInTx(ctx, tx, issueID)
	if err != nil {
		// The dependency source may itself have been deleted (cascade); record
		// the edge change with a null snapshot rather than failing.
		if errors.Is(err, storage.ErrNotFound) {
			return insertEventRow(ctx, tx, op, issueID, nil, &EventDep{Kind: kind, Target: target})
		}
		return fmt.Errorf("journal: snapshot %s for %s: %w", op, issueID, err)
	}
	return insertEventRow(ctx, tx, op, issueID, issue, &EventDep{Kind: kind, Target: target})
}

// insertEventRow performs the actual INSERT. It is the ONE seam both write
// plumbings funnel through, so the seq mechanism cannot drift between them. A
// nil issue is stored as SQL NULL (deletes); a nil dep is stored as SQL NULL
// (non-dependency ops). ts is the insert time, stamped inside the committing
// transaction.
func insertEventRow(ctx context.Context, tx DBTX, op EventOp, issueID string, issue *types.Issue, dep *EventDep) error {
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
	seq, err := nextEventSeq(ctx, tx)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO bd_events_journal (seq, ts, op, issue_id, issue_json, dep_json)
		VALUES (?, ?, ?, ?, ?, ?)
	`, seq, time.Now().UTC(), string(op), issueID, issueJSON, depJSON)
	if err != nil {
		return fmt.Errorf("journal: record %s for %s: %w", op, issueID, err)
	}
	return nil
}

// nextEventSeq allocates the next journal sequence number from the single-row
// bd_events_seq counter, INSIDE the caller's transaction. Incrementing the
// shared counter row is what serializes seq assignment: two transactions that
// both allocate a seq contend on the one row, so only one commit order survives.
// The value becomes the journal row's seq, yielding gapless, commit-ordered seqs
// (a rolled-back transaction rolls back its increment, burning no seq). The
// counter persists across restart and prune never touches it, so seq never
// resets. The seed row is created by migration 0056 / ignored 0014; the
// self-heal below re-creates it at the journal's high-water mark if it is ever
// missing, so a re-seed can never collide with an existing seq.
func nextEventSeq(ctx context.Context, tx DBTX) (int64, error) {
	advance := func() (int64, error) {
		res, err := tx.ExecContext(ctx, "UPDATE bd_events_seq SET next_seq = next_seq + 1 WHERE id = 0")
		if err != nil {
			return 0, fmt.Errorf("journal: advance seq counter: %w", err)
		}
		return res.RowsAffected()
	}
	n, err := advance()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		// Seed the row (idempotent), then raise it past any existing journal seq so
		// the re-seed can never collide. VALUES + GREATEST, not INSERT ... SELECT
		// MAX(): in Dolt a literal+aggregate SELECT over an empty table yields zero
		// rows, so an INSERT ... SELECT would seed nothing on a fresh journal.
		if _, err := tx.ExecContext(ctx, "INSERT IGNORE INTO bd_events_seq (id, next_seq) VALUES (0, 0)"); err != nil {
			return 0, fmt.Errorf("journal: seed seq counter: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE bd_events_seq
			SET next_seq = GREATEST(next_seq, COALESCE((SELECT MAX(seq) FROM bd_events_journal), 0))
			WHERE id = 0
		`); err != nil {
			return 0, fmt.Errorf("journal: raise seq counter to high-water mark: %w", err)
		}
		if _, err := advance(); err != nil {
			return 0, err
		}
	}
	var seq int64
	if err := tx.QueryRowContext(ctx, "SELECT next_seq FROM bd_events_seq WHERE id = 0").Scan(&seq); err != nil {
		return 0, fmt.Errorf("journal: read seq counter: %w", err)
	}
	return seq, nil
}

// compile-time assurance that *sql.Tx satisfies DBTX (the emit helpers accept
// both *sql.Tx and *sql.DB via DBTX).
var _ DBTX = (*sql.Tx)(nil)

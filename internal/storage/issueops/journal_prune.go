package issueops

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/steveyegge/beads/internal/storage"
)

// Pruning the durable mutations journal (bd_mutations_journal) deletes old rows
// below a caller-supplied sequence number, but never below a configurable
// retention floor. Two independent floors compose: keep every row younger than
// mutations-journal-retain-days, and always keep the newest
// mutations-journal-retain-rows rows regardless of age. The floors are pure
// "keep" constraints AND-ed onto the delete predicate, so they can only ever
// reduce what a prune removes — a consumer that has not advanced its watermark
// stays protected even against `prune --before <huge>`.
//
// The retain-rows floor needs one scalar read (the seq of the row just past the
// retained window); the rest is a pure predicate. Both helpers here are engine
// state only — the journal table is dolt_ignored — so pruning never touches
// versioned issue data.

// MutationsPruneRowsCeilQuery returns the query that finds the highest seq a
// retain-rows floor is allowed to delete: the seq of the (retainRows+1)-th
// newest row. Bind retainRows as the OFFSET. It returns no row when the journal
// holds retainRows or fewer rows, meaning the whole journal is inside the
// retained window and nothing may be pruned by the rows floor.
func MutationsPruneRowsCeilQuery() string {
	return `SELECT seq FROM bd_mutations_journal ORDER BY seq DESC LIMIT 1 OFFSET ?`
}

// BuildMutationsPruneWhere builds the WHERE predicate (without the "WHERE"
// keyword) and its bind args for a journal prune. before is the primary bound:
// only rows with seq strictly below it are eligible. retainDays > 0 adds a
// keep-recent floor (rows with ts at or after now-retainDays survive). When
// rowsCeilOK is true, rowsCeil is the highest seq the retain-rows floor permits
// deleting (from MutationsPruneRowsCeilQuery); rows above it are the retained
// newest window and survive. Every clause is AND-ed, so a row is deleted only
// when it clears the bound and both floors.
func BuildMutationsPruneWhere(before int64, retainDays int, now time.Time, rowsCeil int64, rowsCeilOK bool) (string, []any) {
	where := "seq < ?"
	args := []any{before}
	if retainDays > 0 {
		where += " AND ts < ?"
		args = append(args, now.AddDate(0, 0, -retainDays).UTC())
	}
	if rowsCeilOK {
		where += " AND seq <= ?"
		args = append(args, rowsCeil)
	}
	return where, args
}

// ReadMutationsInTx returns journal rows with seq greater than since, ordered by
// seq ascending. limit > 0 caps the result. It runs inside the caller's
// transaction so it works on every store plumbing (embedded, server, proxied).
func ReadMutationsInTx(ctx context.Context, tx DBTX, since int64, limit int) ([]storage.MutationsJournalRow, error) {
	// CAST(ts AS CHAR) normalizes the DATETIME to a stable string across drivers.
	q := `SELECT seq, CAST(ts AS CHAR), op, issue_id, issue_json, dep_json
	      FROM bd_mutations_journal WHERE seq > ? ORDER BY seq ASC`
	if limit > 0 {
		q += " LIMIT " + strconv.Itoa(limit)
	}
	rows, err := tx.QueryContext(ctx, q, since)
	if err != nil {
		return nil, fmt.Errorf("journal: read since %d: %w", since, err)
	}
	defer rows.Close()

	var out []storage.MutationsJournalRow
	for rows.Next() {
		var (
			r       storage.MutationsJournalRow
			issueJS sql.NullString
			depJS   sql.NullString
		)
		if err := rows.Scan(&r.Seq, &r.TS, &r.Op, &r.IssueID, &issueJS, &depJS); err != nil {
			return nil, fmt.Errorf("journal: scan row: %w", err)
		}
		r.IssueJSON = issueJS.String
		r.DepJSON = depJS.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// PruneMutationsInTx deletes journal rows with seq below before, honoring the
// retain-days and retain-rows floors (0 disables a floor), and returns the
// number of rows deleted. It runs inside the caller's transaction.
func PruneMutationsInTx(ctx context.Context, tx DBTX, before int64, retainDays, retainRows int, now time.Time) (int64, error) {
	var (
		rowsCeil   int64
		rowsCeilOK bool
	)
	if retainRows > 0 {
		err := tx.QueryRowContext(ctx, MutationsPruneRowsCeilQuery(), retainRows).Scan(&rowsCeil)
		if errors.Is(err, sql.ErrNoRows) {
			// The whole journal is inside the retained window: nothing to prune.
			return 0, nil
		}
		if err != nil {
			return 0, fmt.Errorf("journal: compute retain-rows floor: %w", err)
		}
		rowsCeilOK = true
	}
	where, args := BuildMutationsPruneWhere(before, retainDays, now, rowsCeil, rowsCeilOK)
	res, err := tx.ExecContext(ctx, "DELETE FROM bd_mutations_journal WHERE "+where, args...)
	if err != nil {
		return 0, fmt.Errorf("journal: prune below %d: %w", before, err)
	}
	return res.RowsAffected()
}

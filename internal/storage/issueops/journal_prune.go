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

// Pruning the durable events journal (bd_events_journal) deletes old rows
// below a caller-supplied sequence number, but never below a configurable
// retention floor. Two independent floors compose: keep every row younger than
// events-journal-retain-days, and always keep the newest
// events-journal-retain-rows rows regardless of age. The floors are pure
// "keep" constraints AND-ed onto the delete predicate, so they can only ever
// reduce what a prune removes — a consumer that has not advanced its watermark
// stays protected even against `prune --before <huge>`.
//
// The retain-rows floor needs one scalar read (the seq of the row just past the
// retained window); the rest is a pure predicate. Both helpers here are engine
// state only — the journal table is dolt_ignored — so pruning never touches
// versioned issue data.

// EventsPruneRowsCeilQuery returns the query that finds the highest seq a
// retain-rows floor is allowed to delete: the seq of the (retainRows+1)-th
// newest row. Bind retainRows as the OFFSET. It returns no row when the journal
// holds retainRows or fewer rows, meaning the whole journal is inside the
// retained window and nothing may be pruned by the rows floor.
func EventsPruneRowsCeilQuery() string {
	return `SELECT seq FROM bd_events_journal ORDER BY seq DESC LIMIT 1 OFFSET ?`
}

// BuildEventsPruneWhere builds the WHERE predicate (without the "WHERE"
// keyword) and its bind args for a journal prune. before is the primary bound:
// only rows with seq strictly below it are eligible. retainDays > 0 adds a
// keep-recent floor (rows with ts at or after now-retainDays survive). When
// rowsCeilOK is true, rowsCeil is the highest seq the retain-rows floor permits
// deleting (from EventsPruneRowsCeilQuery); rows above it are the retained
// newest window and survive. Every clause is AND-ed, so a row is deleted only
// when it clears the bound and both floors.
func BuildEventsPruneWhere(before int64, retainDays int, now time.Time, rowsCeil int64, rowsCeilOK bool) (string, []any) {
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

// ReadEventsInTx returns journal rows with seq greater than since, ordered by
// seq ascending. limit > 0 caps the result. It runs inside the caller's
// transaction so it works on every store plumbing (embedded, server, proxied).
//
// It returns *storage.EventsJournalTruncatedError when since sits below the
// retained window — see checkEventsTruncation for why that is a hard failure
// rather than a silent skip.
func ReadEventsInTx(ctx context.Context, tx DBTX, since int64, limit int) ([]storage.EventsJournalRow, error) {
	out, err := readEventsRowsInTx(ctx, tx, since, limit)
	if err != nil {
		return nil, err
	}
	if err := ComputeEventsTruncation(since, out, func() (int64, error) {
		return readEventsHeadInTx(ctx, tx)
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// ComputeEventsTruncation decides whether the rows a read returned are the
// caller's contiguous continuation from since, or the remains of a window whose
// prefix a prune already deleted. It returns *storage.EventsJournalTruncatedError
// in the latter case and nil otherwise.
//
// This is the ONE place the decision lives, so every read plumbing that serves
// a `--since` — the DBTX path and anything projecting the journal outward —
// answers a pruned-past checkpoint identically. readHead is only invoked in the
// ambiguous cases below.
//
// Seqs are gapless by construction (see nextEventSeq) and prune only ever
// deletes a prefix (`seq < before`), so the only hole a reader can observe is a
// missing prefix. That makes the common case free: when the first row returned
// is exactly since+1 the read is contiguous and no extra query runs — which
// matters because `bd events tail --follow` calls this every second.
//
// Only the two ambiguous shapes cost a counter read:
//   - rows start above since+1: the prefix was pruned, floor is the first row.
//   - no rows at all: either genuinely caught up (since >= head) or the whole
//     journal was pruned out from under the caller (since < head), which is
//     indistinguishable from "nothing new" at the SQL level and is exactly the
//     silent-loss case this exists to catch.
func ComputeEventsTruncation(since int64, rows []storage.EventsJournalRow, readHead func() (int64, error)) error {
	if len(rows) > 0 && rows[0].Seq == since+1 {
		return nil
	}
	head, err := readHead()
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		return &storage.EventsJournalTruncatedError{Since: since, Floor: rows[0].Seq, Head: head}
	}
	if since >= head {
		return nil
	}
	// Empty journal: the floor is one past the head — nothing is retained, and
	// the caller's checkpoint is provably behind it.
	return &storage.EventsJournalTruncatedError{Since: since, Floor: head + 1, Head: head}
}

// readEventsHeadInTx returns the highest seq the counter has ever assigned.
// Prune never touches the counter, so this is the head of the journal's history
// even when every row has been deleted. A missing counter row means no mutation
// has ever been journaled here, which is a head of 0.
func readEventsHeadInTx(ctx context.Context, tx DBTX) (int64, error) {
	var head int64
	err := tx.QueryRowContext(ctx, "SELECT next_seq FROM bd_events_seq WHERE id = 0").Scan(&head)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("journal: read seq counter: %w", err)
	}
	return head, nil
}

func readEventsRowsInTx(ctx context.Context, tx DBTX, since int64, limit int) ([]storage.EventsJournalRow, error) {
	// CAST(ts AS CHAR) normalizes the DATETIME to a stable string across drivers.
	q := `SELECT seq, CAST(ts AS CHAR), op, issue_id, issue_json, dep_json, comment_json
	      FROM bd_events_journal WHERE seq > ? ORDER BY seq ASC`
	if limit > 0 {
		q += " LIMIT " + strconv.Itoa(limit)
	}
	rows, err := tx.QueryContext(ctx, q, since)
	if err != nil {
		return nil, fmt.Errorf("journal: read since %d: %w", since, err)
	}
	defer rows.Close()

	var out []storage.EventsJournalRow
	for rows.Next() {
		var (
			r         storage.EventsJournalRow
			issueJS   sql.NullString
			depJS     sql.NullString
			commentJS sql.NullString
		)
		if err := rows.Scan(&r.Seq, &r.TS, &r.Op, &r.IssueID, &issueJS, &depJS, &commentJS); err != nil {
			return nil, fmt.Errorf("journal: scan row: %w", err)
		}
		r.TS = normalizeEventsTimestamp(r.TS)
		r.IssueJSON = issueJS.String
		r.DepJSON = depJS.String
		r.CommentJSON = commentJS.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// normalizeEventsTimestamp emits the journal boundary's stable RFC3339Nano UTC
// contract. Dolt/MySQL stringify DATETIME with a space while PostgreSQL may
// include a numeric offset; consumers (including Gasworks) require a parsable
// UTC timestamp regardless of the backing driver.
func normalizeEventsTimestamp(raw string) string {
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts.UTC().Format(time.RFC3339Nano)
		}
	}
	// Preserve an unexpected driver rendering rather than silently changing the
	// event payload. Normal supported backends are covered by the layouts above.
	return raw
}

// ComputeEventsPruneWhere resolves the retain-rows floor via readCeil and
// returns the full DELETE predicate (without the "WHERE" keyword) plus its bind
// args, or skip=true when the whole journal is inside the retained window and
// nothing may be pruned. readCeil reports (ceil, found, err): found is false
// when the journal holds retainRows or fewer rows. It is only invoked when
// retainRows > 0.
//
// This is the ONE place the retain-floor orchestration lives. Both prune
// plumbings — the DBTX path (PruneEventsInTx) and the proxied raw-SQL path in
// cmd/bd — call it, so the two can never drift on which rows a floor protects.
// Only the substrate-specific ceil read and the DELETE execution differ.
func ComputeEventsPruneWhere(before int64, retainDays, retainRows int, now time.Time, readCeil func() (ceil int64, found bool, err error)) (where string, args []any, skip bool, err error) {
	var (
		rowsCeil   int64
		rowsCeilOK bool
	)
	if retainRows > 0 {
		ceil, found, cerr := readCeil()
		if cerr != nil {
			return "", nil, false, cerr
		}
		if !found {
			return "", nil, true, nil
		}
		rowsCeil, rowsCeilOK = ceil, true
	}
	where, args = BuildEventsPruneWhere(before, retainDays, now, rowsCeil, rowsCeilOK)
	return where, args, false, nil
}

// PruneEventsInTx deletes journal rows with seq below before, honoring the
// retain-days and retain-rows floors (0 disables a floor), and returns the
// number of rows deleted. It runs inside the caller's transaction.
func PruneEventsInTx(ctx context.Context, tx DBTX, before int64, retainDays, retainRows int, now time.Time) (int64, error) {
	where, args, skip, err := ComputeEventsPruneWhere(before, retainDays, retainRows, now, func() (int64, bool, error) {
		var ceil int64
		scanErr := tx.QueryRowContext(ctx, EventsPruneRowsCeilQuery(), retainRows).Scan(&ceil)
		if errors.Is(scanErr, sql.ErrNoRows) {
			return 0, false, nil
		}
		if scanErr != nil {
			return 0, false, fmt.Errorf("journal: compute retain-rows floor: %w", scanErr)
		}
		return ceil, true, nil
	})
	if err != nil {
		return 0, err
	}
	if skip {
		return 0, nil
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM bd_events_journal WHERE "+where, args...)
	if err != nil {
		return 0, fmt.Errorf("journal: prune below %d: %w", before, err)
	}
	return res.RowsAffected()
}

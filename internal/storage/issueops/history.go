package issueops

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// HistoryInTx returns the complete version history for an issue by querying
// the dolt_history_issues system table. The result is ordered newest-first.
//
// The subquery wrapper avoids Dolt's max1Row optimization on PK lookup:
// dolt_history_* tables return multiple rows per PK (one per commit), but
// the query planner incorrectly assumes WHERE id=? returns one row.
func HistoryInTx(ctx context.Context, tx DBTX, issueID string) ([]*storage.HistoryEntry, error) {
	groups, err := BulkHistoryInTx(ctx, tx, []string{issueID})
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return []*storage.HistoryEntry{}, nil
	}
	return groups[0].Entries, nil
}

// BulkHistoryInTx returns complete version history for exact issue IDs with one
// parameterized query. IDs are trimmed, blank IDs omitted, deduplicated, and
// sorted lexically. Every normalized ID has a result group, including misses.
func BulkHistoryInTx(ctx context.Context, tx DBTX, issueIDs []string) ([]storage.IssueHistory, error) {
	ids, err := NormalizeBulkHistoryIDs(issueIDs)
	if err != nil {
		return nil, err
	}
	groups := make([]storage.IssueHistory, len(ids))
	byID := make(map[string]int, len(ids))
	for i, id := range ids {
		groups[i] = storage.IssueHistory{IssueID: id, Entries: []*storage.HistoryEntry{}}
		byID[id] = i
	}
	if len(ids) == 0 {
		return groups, nil
	}

	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	query := `
		SELECT
			id, title,
			COALESCE(description, '') AS description,
			COALESCE(design, '') AS design,
			COALESCE(acceptance_criteria, '') AS acceptance_criteria,
			COALESCE(notes, '') AS notes,
			status, priority, issue_type, assignee, owner, created_by,
			estimated_minutes, created_at, updated_at, closed_at, close_reason,
			pinned, mol_type,
			commit_hash, committer, commit_date
		FROM (
			SELECT * FROM dolt_history_issues
		) h
		WHERE h.id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + `)
		ORDER BY h.id ASC, h.commit_date DESC, h.commit_hash DESC
	`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get issue history: %w", err)
	}

	for rows.Next() {
		entry, err := scanHistoryEntry(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		groups[byID[entry.Issue.ID]].Entries = append(groups[byID[entry.Issue.ID]].Entries, entry)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("failed to read history: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("failed to close history rows: %w", err)
	}
	return groups, nil
}

// CompactBulkHistoryInTx returns distinct normalized states while the history
// rows are still in SQL, so repeated bodies do not cross the storage boundary.
func CompactBulkHistoryInTx(ctx context.Context, tx DBTX, issueIDs []string, limit int, dateLocation *time.Location) ([]storage.CompactIssueHistory, error) {
	ids, err := NormalizeBulkHistoryIDs(issueIDs)
	if err != nil {
		return nil, err
	}
	groups := make([]storage.CompactIssueHistory, len(ids))
	byID := make(map[string]int, len(ids))
	stateIndexes := make([]map[string]int, len(ids))
	for i, id := range ids {
		groups[i] = storage.CompactIssueHistory{IssueID: id, States: []storage.CompactHistoryState{}}
		byID[id] = i
		stateIndexes[i] = make(map[string]int)
	}
	if len(ids) == 0 {
		return groups, nil
	}
	args := make([]any, 0, len(ids)+1)
	for _, id := range ids {
		args = append(args, id)
	}
	rankColumn := ""
	if limit > 0 {
		rankColumn = ", ROW_NUMBER() OVER (PARTITION BY h.id ORDER BY h.commit_date DESC, h.commit_hash DESC) AS occurrence_rank"
		args = append(args, limit)
	}
	query := `
		WITH selected AS (
			SELECT h.*` + rankColumn + `
			FROM (SELECT * FROM dolt_history_issues) h
			WHERE h.id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + `)
		)
		SELECT BINARY id AS id, BINARY title AS title,
			BINARY COALESCE(description, '') AS description, BINARY COALESCE(design, '') AS design,
			BINARY COALESCE(acceptance_criteria, '') AS acceptance_criteria, BINARY COALESCE(notes, '') AS notes,
			BINARY status AS status, priority, BINARY issue_type AS issue_type,
			BINARY assignee AS assignee, BINARY owner AS owner, BINARY created_by AS created_by, estimated_minutes,
			created_at, updated_at, closed_at, BINARY close_reason AS close_reason, pinned, BINARY mol_type AS mol_type,
			JSON_ARRAYAGG(JSON_ARRAY(commit_hash, committer, commit_date)) AS occurrences
		FROM selected
	`
	if limit > 0 {
		query += " WHERE occurrence_rank <= ?\n"
	}
	query += `
		GROUP BY BINARY id, BINARY title,
			BINARY COALESCE(description, ''), BINARY COALESCE(design, ''),
			BINARY COALESCE(acceptance_criteria, ''), BINARY COALESCE(notes, ''),
			BINARY status, priority, BINARY issue_type, BINARY assignee, BINARY owner, BINARY created_by,
			estimated_minutes, created_at, updated_at, closed_at, BINARY close_reason,
			pinned, BINARY mol_type
		ORDER BY BINARY id
	`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get compact issue history: %w", err)
	}
	for rows.Next() {
		issue, occurrences, err := scanCompactHistoryState(rows, dateLocation)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		id := issue.ID
		groupIndex, ok := byID[id]
		if !ok {
			_ = rows.Close()
			return nil, fmt.Errorf("compact history returned unrequested issue %q", id)
		}
		stateKey, err := json.Marshal(issue)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("failed to normalize compact history state: %w", err)
		}
		key := string(stateKey)
		if stateIndex, found := stateIndexes[groupIndex][key]; found {
			groups[groupIndex].States[stateIndex].Occurrences = append(groups[groupIndex].States[stateIndex].Occurrences, occurrences...)
			sort.Slice(groups[groupIndex].States[stateIndex].Occurrences, func(i, j int) bool {
				a, b := groups[groupIndex].States[stateIndex].Occurrences[i], groups[groupIndex].States[stateIndex].Occurrences[j]
				if !a.CommitDate.Equal(b.CommitDate) {
					return a.CommitDate.After(b.CommitDate)
				}
				return a.CommitHash > b.CommitHash
			})
		} else {
			stateIndexes[groupIndex][key] = len(groups[groupIndex].States)
			groups[groupIndex].States = append(groups[groupIndex].States, storage.CompactHistoryState{Issue: issue, Occurrences: occurrences})
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("failed to read compact history: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("failed to close compact history rows: %w", err)
	}
	return groups, nil
}

func scanCompactHistoryState(row historyScanner, dateLocation *time.Location) (*types.Issue, []storage.HistoryOccurrence, error) {
	var issue types.Issue
	var createdAt, updatedAt sql.NullString
	var closedAt sql.NullTime
	var assignee, owner, createdBy, closeReason, molType sql.NullString
	var estimatedMinutes, pinned sql.NullInt64
	var occurrenceJSON []byte
	if err := row.Scan(
		&issue.ID, &issue.Title, &issue.Description, &issue.Design, &issue.AcceptanceCriteria, &issue.Notes,
		&issue.Status, &issue.Priority, &issue.IssueType, &assignee, &owner, &createdBy,
		&estimatedMinutes, &createdAt, &updatedAt, &closedAt, &closeReason, &pinned, &molType, &occurrenceJSON,
	); err != nil {
		return nil, nil, fmt.Errorf("failed to scan compact history: %w", err)
	}
	if createdAt.Valid {
		issue.CreatedAt = ParseTimeString(createdAt.String)
	}
	if updatedAt.Valid {
		issue.UpdatedAt = ParseTimeString(updatedAt.String)
	}
	if closedAt.Valid {
		issue.ClosedAt = &closedAt.Time
	}
	issue.Assignee, issue.Owner, issue.CreatedBy = assignee.String, owner.String, createdBy.String
	if estimatedMinutes.Valid {
		minutes := int(estimatedMinutes.Int64)
		issue.EstimatedMinutes = &minutes
	}
	issue.CloseReason = closeReason.String
	issue.Pinned = pinned.Valid && pinned.Int64 != 0
	issue.MolType = types.MolType(molType.String)
	var tuples [][]json.RawMessage
	if err := json.Unmarshal(occurrenceJSON, &tuples); err != nil {
		return nil, nil, fmt.Errorf("failed to decode compact history occurrences: %w", err)
	}
	occurrences := make([]storage.HistoryOccurrence, 0, len(tuples))
	for _, tuple := range tuples {
		if len(tuple) != 3 {
			return nil, nil, fmt.Errorf("invalid compact history occurrence tuple")
		}
		var item storage.HistoryOccurrence
		var date string
		if err := json.Unmarshal(tuple[0], &item.CommitHash); err != nil {
			return nil, nil, err
		}
		if err := json.Unmarshal(tuple[1], &item.Committer); err != nil {
			return nil, nil, err
		}
		if err := json.Unmarshal(tuple[2], &date); err != nil {
			return nil, nil, err
		}
		parsedDate, err := parseCompactCommitDate(date, dateLocation)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid compact history commit date %q", date)
		}
		item.CommitDate = parsedDate
		occurrences = append(occurrences, item)
	}
	sort.Slice(occurrences, func(i, j int) bool {
		if !occurrences[i].CommitDate.Equal(occurrences[j].CommitDate) {
			return occurrences[i].CommitDate.After(occurrences[j].CommitDate)
		}
		return occurrences[i].CommitHash > occurrences[j].CommitHash
	})
	return &issue, occurrences, nil
}

func parseCompactCommitDate(value string, location *time.Location) (time.Time, error) {
	if date, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return date, nil
	}
	// JSON aggregation drops the driver's location; restore backend-specific
	// DATETIME semantics (embedded uses Local, server DSNs default to UTC).
	return time.ParseInLocation("2006-01-02 15:04:05", value, location)
}

type historyScanner interface {
	Scan(dest ...any) error
}

func scanHistoryEntry(row historyScanner) (*storage.HistoryEntry, error) {
	var issue types.Issue
	var createdAtStr, updatedAtStr sql.NullString
	var closedAt sql.NullTime
	var assignee, owner, createdBy, closeReason, molType sql.NullString
	var estimatedMinutes sql.NullInt64
	var pinned sql.NullInt64
	var commitHash, committer string
	var commitDate time.Time

	if err := row.Scan(
		&issue.ID, &issue.Title, &issue.Description, &issue.Design, &issue.AcceptanceCriteria, &issue.Notes,
		&issue.Status, &issue.Priority, &issue.IssueType, &assignee, &owner, &createdBy,
		&estimatedMinutes, &createdAtStr, &updatedAtStr, &closedAt, &closeReason,
		&pinned, &molType,
		&commitHash, &committer, &commitDate,
	); err != nil {
		return nil, fmt.Errorf("failed to scan history: %w", err)
	}
	if createdAtStr.Valid {
		issue.CreatedAt = ParseTimeString(createdAtStr.String)
	}
	if updatedAtStr.Valid {
		issue.UpdatedAt = ParseTimeString(updatedAtStr.String)
	}
	if closedAt.Valid {
		issue.ClosedAt = &closedAt.Time
	}
	if assignee.Valid {
		issue.Assignee = assignee.String
	}
	if owner.Valid {
		issue.Owner = owner.String
	}
	if createdBy.Valid {
		issue.CreatedBy = createdBy.String
	}
	if estimatedMinutes.Valid {
		mins := int(estimatedMinutes.Int64)
		issue.EstimatedMinutes = &mins
	}
	if closeReason.Valid {
		issue.CloseReason = closeReason.String
	}
	if pinned.Valid && pinned.Int64 != 0 {
		issue.Pinned = true
	}
	if molType.Valid {
		issue.MolType = types.MolType(molType.String)
	}
	return &storage.HistoryEntry{CommitHash: commitHash, Committer: committer, CommitDate: commitDate, Issue: &issue}, nil
}

// NormalizeBulkHistoryIDs validates, deduplicates, and sorts a bulk history
// request without touching storage.
func NormalizeBulkHistoryIDs(issueIDs []string) ([]string, error) {
	capacity := min(len(issueIDs), storage.MaxBulkHistoryIDs)
	seen := make(map[string]struct{}, capacity)
	for _, id := range issueIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		if err := types.CheckFieldLen("issue ID", id); err != nil {
			return nil, fmt.Errorf("%w: %v", storage.ErrValidation, err)
		}
		if len(seen) == storage.MaxBulkHistoryIDs {
			return nil, fmt.Errorf("%w: bulk history accepts at most %d unique issue IDs", storage.ErrValidation, storage.MaxBulkHistoryIDs)
		}
		seen[id] = struct{}{}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// PreviousExternalRefInTx returns the external_ref value recorded for
// issueID as of the most recent commit at or before asOf, by querying the
// dolt_history_issues system table. found is false if no history entry
// exists for issueID at or before asOf.
//
// The subquery wrapper avoids Dolt's max1Row optimization on PK lookup, for
// the same reason described on HistoryInTx above.
func PreviousExternalRefInTx(ctx context.Context, tx *sql.Tx, issueID string, asOf time.Time) (string, bool, error) {
	var previousRef sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT external_ref
		FROM (
			SELECT id, external_ref, commit_date FROM dolt_history_issues
		) h
		WHERE h.id = ? AND h.commit_date <= ?
		ORDER BY h.commit_date DESC
		LIMIT 1
	`, issueID, asOf.UTC()).Scan(&previousRef)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("failed to get previous external_ref: %w", err)
	}
	return previousRef.String, true, nil
}

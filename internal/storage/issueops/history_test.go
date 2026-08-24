package issueops

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/storage"
)

var historyColumns = []string{
	"id", "title", "description", "design", "acceptance_criteria", "notes",
	"status", "priority", "issue_type", "assignee", "owner", "created_by",
	"estimated_minutes", "created_at", "updated_at", "closed_at", "close_reason",
	"pinned", "mol_type", "commit_hash", "committer", "commit_date",
}

func historyRow(id, title, hash string, date time.Time) []driver.Value {
	return []driver.Value{
		id, title, "", "", "", "", "open", 2, "task", nil, nil, nil,
		nil, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", nil, nil,
		0, nil, hash, "tester", date,
	}
}

func historyRows(rows ...[]driver.Value) *sqlmock.Rows {
	out := sqlmock.NewRows(historyColumns)
	for _, row := range rows {
		out.AddRow(row...)
	}
	return out
}

func expectHistoryQuery(mock sqlmock.Sqlmock, ids []string, rows *sqlmock.Rows) {
	args := make([]driver.Value, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	mock.ExpectQuery(`(?s)FROM dolt_history_issues.*ORDER BY h\.id ASC, h\.commit_date DESC, h\.commit_hash DESC`).WithArgs(args...).WillReturnRows(rows)
}

func TestBulkHistoryInTxMatchesSingleHistory(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	newer := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	older := newer.Add(-time.Hour)
	expectHistoryQuery(mock, []string{"a", "b", "missing"}, historyRows(
		historyRow("a", "new", "a2", newer),
		historyRow("a", "old", "a1", older),
		historyRow("b", "only", "b1", older),
	))

	groups, err := BulkHistoryInTx(t.Context(), db, []string{" b ", "missing", "a", "b", ""})
	if err != nil {
		t.Fatalf("BulkHistoryInTx: %v", err)
	}
	if got := []string{groups[0].IssueID, groups[1].IssueID, groups[2].IssueID}; !reflect.DeepEqual(got, []string{"a", "b", "missing"}) {
		t.Fatalf("group IDs = %v", got)
	}
	if len(groups[0].Entries) != 2 || groups[0].Entries[0].CommitHash != "a2" || groups[0].Entries[1].CommitHash != "a1" {
		t.Fatalf("a history is not newest-first: %+v", groups[0].Entries)
	}
	if groups[2].Entries == nil || len(groups[2].Entries) != 0 {
		t.Fatalf("missing group entries = %#v, want non-nil empty slice", groups[2].Entries)
	}

	for i, id := range []string{"a", "b", "missing"} {
		var rows *sqlmock.Rows
		switch id {
		case "a":
			rows = historyRows(historyRow("a", "new", "a2", newer), historyRow("a", "old", "a1", older))
		case "b":
			rows = historyRows(historyRow("b", "only", "b1", older))
		default:
			rows = historyRows()
		}
		expectHistoryQuery(mock, []string{id}, rows)
		single, err := HistoryInTx(t.Context(), db, id)
		if err != nil {
			t.Fatalf("HistoryInTx(%q): %v", id, err)
		}
		if !reflect.DeepEqual(groups[i].Entries, single) {
			t.Fatalf("bulk history for %q differs from single:\nbulk=%#v\nsingle=%#v", id, groups[i].Entries, single)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBulkHistoryInTxEmptyIDsDoesNotQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	groups, err := BulkHistoryInTx(t.Context(), db, []string{"", "  "})
	if err != nil {
		t.Fatal(err)
	}
	if groups == nil || len(groups) != 0 {
		t.Fatalf("groups = %#v, want non-nil empty slice", groups)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBulkHistoryInTxInputBounds(t *testing.T) {
	t.Run("exact unique-ID boundary", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		ids := make([]string, storage.MaxBulkHistoryIDs)
		for i := range ids {
			ids[i] = fmt.Sprintf("bd-%04d", i)
		}
		expectHistoryQuery(mock, ids, historyRows())
		groups, err := BulkHistoryInTx(t.Context(), db, ids)
		if err != nil {
			t.Fatal(err)
		}
		if len(groups) != storage.MaxBulkHistoryIDs {
			t.Fatalf("groups = %d", len(groups))
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("exact ID-length boundary", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		id := strings.Repeat("x", storage.MaxBulkHistoryIDLength)
		expectHistoryQuery(mock, []string{id}, historyRows())
		groups, err := BulkHistoryInTx(t.Context(), db, []string{id})
		if err != nil {
			t.Fatal(err)
		}
		if len(groups) != 1 || groups[0].IssueID != id {
			t.Fatalf("groups = %#v", groups)
		}
	})

	for _, test := range []struct {
		name string
		ids  []string
	}{
		{
			name: "too many unique IDs",
			ids: func() []string {
				ids := make([]string, storage.MaxBulkHistoryIDs+1)
				for i := range ids {
					ids[i] = fmt.Sprintf("bd-%04d", i)
				}
				return ids
			}(),
		},
		{name: "overlong ID", ids: []string{strings.Repeat("x", storage.MaxBulkHistoryIDLength+1)}},
	} {
		t.Run(test.name+" rejects before query", func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := BulkHistoryInTx(t.Context(), db, test.ids); !errors.Is(err, storage.ErrValidation) {
				t.Fatalf("error = %v, want ErrValidation", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("validation reached storage: %v", err)
			}
		})
	}

	t.Run("duplicates do not consume boundary", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		ids := make([]string, 0, storage.MaxBulkHistoryIDs*2)
		unique := make([]string, storage.MaxBulkHistoryIDs)
		for i := range unique {
			unique[i] = fmt.Sprintf("bd-%04d", i)
			ids = append(ids, unique[i], unique[i])
		}
		expectHistoryQuery(mock, unique, historyRows())
		groups, err := BulkHistoryInTx(t.Context(), db, ids)
		if err != nil {
			t.Fatal(err)
		}
		if len(groups) != storage.MaxBulkHistoryIDs {
			t.Fatalf("groups = %d", len(groups))
		}
	})
}

func TestBulkHistoryInTxEqualTimestampsUseCommitHashOrder(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	date := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	expectHistoryQuery(mock, []string{"a"}, historyRows(
		historyRow("a", "hash b", "b", date),
		historyRow("a", "hash a", "a", date),
	))
	groups, err := BulkHistoryInTx(t.Context(), db, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{groups[0].Entries[0].CommitHash, groups[0].Entries[1].CommitHash}; !reflect.DeepEqual(got, []string{"b", "a"}) {
		t.Fatalf("equal-timestamp hashes = %v", got)
	}
}

func TestBulkHistoryInTxPropagatesQueryAndRowsErrors(t *testing.T) {
	t.Run("query cancellation", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		mock.ExpectQuery(regexp.QuoteMeta("FROM dolt_history_issues")).WithArgs("a").WillReturnError(context.Canceled)
		_, err = BulkHistoryInTx(t.Context(), db, []string{"a"})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})

	t.Run("rows iteration", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		rowsErr := errors.New("rows failed")
		rows := historyRows(historyRow("a", "one", "a1", time.Now())).RowError(0, rowsErr)
		expectHistoryQuery(mock, []string{"a"}, rows)
		_, err = BulkHistoryInTx(t.Context(), db, []string{"a"})
		if !errors.Is(err, rowsErr) {
			t.Fatalf("error = %v, want rows error", err)
		}
	})
}

// TestBulkHistoryInTxOneQueryFor100IDs is the repeatable performance boundary:
// approximately 100 requested IDs and snapshots are served by one SQL query.
func TestBulkHistoryInTxOneQueryFor100IDs(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ids := make([]string, 100)
	rows := historyRows()
	for i := range ids {
		ids[i] = fmt.Sprintf("bd-%03d", i)
		rows.AddRow(historyRow(ids[i], ids[i], fmt.Sprintf("hash-%03d", i), time.Unix(int64(1000-i), 0))...)
	}
	expectHistoryQuery(mock, ids, rows)
	groups, err := BulkHistoryInTx(t.Context(), db, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 100 {
		t.Fatalf("groups = %d, want 100", len(groups))
	}
	for i, group := range groups {
		if len(group.Entries) != 1 || group.IssueID != ids[i] {
			t.Fatalf("group %d = %#v", i, group)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("bulk history executed other than exactly one expected query: %v", err)
	}
}

// BenchmarkBulkHistoryInTx100IDs measures the shared query/scanner boundary
// with 100 exact IDs and one snapshot each. The custom metric makes the single
// query per operation contract visible without an absolute-time assertion.
func BenchmarkBulkHistoryInTx100IDs(b *testing.B) {
	ids := make([]string, 100)
	for i := range ids {
		ids[i] = fmt.Sprintf("bd-%03d", i)
	}
	b.ResetTimer()
	for range b.N {
		db, mock, err := sqlmock.New()
		if err != nil {
			b.Fatal(err)
		}
		rows := historyRows()
		for i, id := range ids {
			rows.AddRow(historyRow(id, id, fmt.Sprintf("hash-%03d", i), time.Unix(int64(1000-i), 0))...)
		}
		expectHistoryQuery(mock, ids, rows)
		groups, err := BulkHistoryInTx(b.Context(), db, ids)
		if err != nil {
			b.Fatal(err)
		}
		if len(groups) != len(ids) {
			b.Fatalf("groups = %d", len(groups))
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			b.Fatal(err)
		}
		_ = db.Close()
	}
	b.ReportMetric(100, "ids/op")
	b.ReportMetric(1, "queries/op")
}

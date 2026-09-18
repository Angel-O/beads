package scopeops

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/storage/sqlbuild"
)

func TestScopeCursorIsVersionedAndBoundToReadShape(t *testing.T) {
	created := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	catalog, err := encodeCursor(scopeCatalogCursor, "", "", "", created, "scope-2")
	if err != nil {
		t.Fatalf("encode catalog cursor: %v", err)
	}
	if _, err := decodeCursor(catalog, scopeCatalogCursor, "", "", ""); err != nil {
		t.Fatalf("decode catalog cursor: %v", err)
	}
	for _, mismatch := range []struct {
		name      string
		kind      string
		scopeID   string
		status    string
		issueType string
	}{
		{"member cursor", scopeMembersCursor, "", "", ""},
		{"scope", scopeCatalogCursor, "other", "", ""},
		{"status", scopeCatalogCursor, "", "completed", ""},
	} {
		t.Run(mismatch.name, func(t *testing.T) {
			if _, err := decodeCursor(catalog, mismatch.kind, mismatch.scopeID, mismatch.status, mismatch.issueType); !errors.Is(err, storage.ErrScopeCursorInvalid) {
				t.Fatalf("decode mismatch error = %v, want ErrScopeCursorInvalid", err)
			}
		})
	}

	member, err := encodeCursor(scopeMembersCursor, "scope-1", "open", "task", time.Time{}, "issue-2")
	if err != nil {
		t.Fatalf("encode member cursor: %v", err)
	}
	if _, err := decodeCursor(member, scopeMembersCursor, "scope-1", "open", "task"); err != nil {
		t.Fatalf("decode member cursor: %v", err)
	}
	memberWithContexts, err := encodeCursor(scopeMembersCursor, "scope-1", "open", "task", time.Time{}, "issue-2", []string{"ctx-b", "ctx-a"})
	if err != nil {
		t.Fatalf("encode member context cursor: %v", err)
	}
	if _, err := decodeCursor(memberWithContexts, scopeMembersCursor, "scope-1", "open", "task", []string{"ctx-a", "ctx-b"}); err != nil {
		t.Fatalf("decode member context cursor: %v", err)
	}
	if _, err := decodeCursor(memberWithContexts, scopeMembersCursor, "scope-1", "open", "task", []string{"ctx-a"}); !errors.Is(err, storage.ErrScopeCursorInvalid) {
		t.Fatalf("decode context mismatch error = %v, want ErrScopeCursorInvalid", err)
	}
}

func TestScopeContextMatchesExactContextLabels(t *testing.T) {
	if !matchesScopeContext([]string{"ctx:team-a", "label"}, []string{"team-a"}) {
		t.Fatal("context ID did not match its exact ctx: label")
	}
	if matchesScopeContext([]string{"ctx:team-ab"}, []string{"team-a"}) {
		t.Fatal("context filter matched a non-exact ctx: label")
	}
}

func TestRenameScopeUpdatesNamesAndExcludesTargetFromCollision(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM scopes WHERE id = ? FOR UPDATE")).
		WithArgs("scope-id").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("scope-id"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM scopes WHERE normalized_name = ? AND id <> ? FOR UPDATE")).
		WithArgs("new name", "scope-id").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE scopes SET name = ?, normalized_name = ? WHERE id = ?")).
		WithArgs(" New Name ", "new name", "scope-id").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := Rename(t.Context(), tx, "scope-id", " New Name "); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit transaction: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected rename query shape: %v", err)
	}
}

func TestRenameScopeRejectsInvalidNamesAndCollisionsWithoutWriting(t *testing.T) {
	for _, name := range []string{"", "   "} {
		if err := Rename(t.Context(), nil, "scope-id", name); !errors.Is(err, storage.ErrScopeInvalid) {
			t.Errorf("Rename(%q) error = %v, want ErrScopeInvalid", name, err)
		}
	}
	if err := Rename(t.Context(), nil, "", "valid"); !errors.Is(err, storage.ErrScopeNotFound) {
		t.Fatalf("Rename(empty id) error = %v, want ErrScopeNotFound", err)
	}

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM scopes WHERE id = ? FOR UPDATE")).
		WithArgs("scope-id").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("scope-id"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM scopes WHERE normalized_name = ? AND id <> ? FOR UPDATE")).
		WithArgs("taken", "scope-id").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("other-scope"))
	mock.ExpectRollback()
	if err := Rename(t.Context(), tx, "scope-id", "taken"); !errors.Is(err, storage.ErrScopeAlreadyExists) {
		t.Fatalf("colliding Rename error = %v, want ErrScopeAlreadyExists", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback transaction: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected collision query shape: %v", err)
	}
}

func TestScopePageLimitDefaultsAndCaps(t *testing.T) {
	if got := scopePageLimit(0); got != defaultScopePageLimit {
		t.Fatalf("default scope page limit = %d, want %d", got, defaultScopePageLimit)
	}
	if got := scopePageLimit(maxScopePageLimit + 1); got != maxScopePageLimit {
		t.Fatalf("capped scope page limit = %d, want %d", got, maxScopePageLimit)
	}
}

func TestSnapshotHydratesHundredMembersWithBatchedReads(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	scopeID := "scope-batch"
	createdOn := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, name, normalized_name, created_on FROM scopes WHERE id = ?")).
		WithArgs(scopeID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "normalized_name", "created_on"}).
			AddRow(scopeID, "Batch", "batch", createdOn))

	memberIDs := make([]string, 100)
	memberRows := sqlmock.NewRows([]string{"issue_id"})
	for i := range memberIDs {
		memberIDs[i] = fmt.Sprintf("batch-member-%02d", i)
		memberRows.AddRow(memberIDs[i])
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT issue_id FROM scope_members WHERE scope_id = ? ORDER BY issue_id")).
		WithArgs(scopeID).
		WillReturnRows(memberRows)

	issueRows := sqlmock.NewRows(snapshotIssueColumns())
	for i := len(memberIDs) - 1; i >= 0; i-- {
		issueRows.AddRow(snapshotIssueRow(memberIDs[i])...)
	}
	issueQuery := "SELECT " + issueops.IssueSelectColumns + " FROM issues " + sqlbuild.LeaseJoin("issues") + " WHERE id IN ("
	mock.ExpectQuery(regexp.QuoteMeta(issueQuery)).WithArgs(stringArgs(memberIDs)...).WillReturnRows(issueRows)

	labelQuery := "SELECT issue_id, label FROM labels WHERE issue_id IN ("
	mock.ExpectQuery(regexp.QuoteMeta(labelQuery)).WithArgs(stringArgs(memberIDs)...).
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}))
	dependencyQuery := "SELECT issue_id, " + issueops.DepTargetExpr + " AS depends_on_id, type, created_at, created_by, metadata, thread_id FROM dependencies WHERE issue_id IN ("
	mock.ExpectQuery(regexp.QuoteMeta(dependencyQuery)).WithArgs(stringArgs(memberIDs)...).
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "depends_on_id", "type", "created_at", "created_by", "metadata", "thread_id"}))
	commentQuery := "SELECT id, issue_id, author, text, created_at FROM comments WHERE issue_id IN ("
	mock.ExpectQuery(regexp.QuoteMeta(commentQuery)).WithArgs(stringArgs(memberIDs)...).
		WillReturnRows(sqlmock.NewRows([]string{"id", "issue_id", "author", "text", "created_at"}))
	mock.ExpectCommit()

	snapshot, err := Snapshot(t.Context(), tx, scopeID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if got := len(snapshot.Members); got != len(memberIDs) {
		t.Fatalf("snapshot members = %d, want %d", got, len(memberIDs))
	}
	for i, member := range snapshot.Members {
		if member.ID != memberIDs[i] {
			t.Fatalf("snapshot member %d = %q, want %q", i, member.ID, memberIDs[i])
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit transaction: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected snapshot query shape: %v", err)
	}
}

func snapshotIssueColumns() []string {
	columns := make([]string, strings.Count(issueops.IssueSelectColumns, ",")+1)
	for i := range columns {
		columns[i] = fmt.Sprintf("column-%d", i)
	}
	return columns
}

func snapshotIssueRow(id string) []driver.Value {
	row := make([]driver.Value, len(snapshotIssueColumns()))
	row[0], row[2] = id, "title"
	row[3], row[4], row[5], row[6] = "description", "design", "acceptance", "notes"
	row[7], row[8], row[9], row[20] = "open", 1, "task", 0
	return row
}

func stringArgs(values []string) []driver.Value {
	args := make([]driver.Value, len(values))
	for i, value := range values {
		args[i] = value
	}
	return args
}

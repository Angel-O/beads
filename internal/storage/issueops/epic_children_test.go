package issueops

import (
	"context"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/types"
)

func TestGetEpicChildrenSortsDurableOnlyWhenWispTableIsMissing(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	mock.ExpectQuery(`SELECT status FROM issues WHERE id = \?`).
		WithArgs("parent").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(string(types.StatusOpen)))
	durableRows := sqlmock.NewRows([]string{"dependency_id", "id", "status", "issue_type"}).
		AddRow("dep-z", "child-z", "open", "task").
		AddRow("dep-a", "child-a", "open", "task")
	mock.ExpectQuery(`(?s)SELECT dependency\.id, child\.id, child\.status, child\.issue_type\s+FROM dependencies AS dependency`).
		WithArgs("parent").
		WillReturnRows(durableRows)
	mock.ExpectQuery(`(?s)SELECT dependency\.id, child\.id, child\.status, child\.issue_type\s+FROM wisp_dependencies AS dependency`).
		WithArgs("parent").
		WillReturnError(errors.New("Error 1146 (42S02): Table 'db.wisp_dependencies' doesn't exist"))

	got, err := GetEpicChildrenInTx(ctx, db, "parent")
	if err != nil {
		t.Fatalf("GetEpicChildrenInTx: %v", err)
	}
	if len(got) != 2 || got[0].ID != "child-a" || got[1].ID != "child-z" {
		t.Fatalf("GetEpicChildrenInTx returned %+v, want child-a then child-z", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("mock expectations: %v", err)
	}
}

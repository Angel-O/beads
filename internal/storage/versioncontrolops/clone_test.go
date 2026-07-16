package versioncontrolops

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestDoltCloneWithoutUser(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(regexp.QuoteMeta("CALL DOLT_CLONE(?, ?)")).
		WithArgs("https://example.com/repo", "beads").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := DoltClone(context.Background(), db, "https://example.com/repo", "beads", ""); err != nil {
		t.Fatalf("DoltClone: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDoltCloneMalformedRemoteDoesNotExposeCredentials(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	const secret = "clone-secret"
	const remote = "https://operator:" + secret + "@provider.example/%zz"
	providerErr := errors.New("provider rejected clone")
	mock.ExpectExec(regexp.QuoteMeta("CALL DOLT_CLONE(?, ?)")).
		WithArgs(remote, "beads").
		WillReturnError(providerErr)

	err = DoltClone(context.Background(), db, remote, "beads", "")
	if err == nil {
		t.Fatal("expected clone failure")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "%zz") {
		t.Fatalf("DoltClone exposed malformed remote credentials: %v", err)
	}
	if !strings.Contains(err.Error(), "configured remote") {
		t.Fatalf("DoltClone lost safe remote context: %v", err)
	}
	if !errors.Is(err, providerErr) {
		t.Fatalf("DoltClone lost provider cause: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDoltCloneWithUser(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(regexp.QuoteMeta("CALL DOLT_CLONE('--user', ?, ?, ?)")).
		WithArgs("alice", "https://example.com/repo", "beads").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := DoltClone(context.Background(), db, "https://example.com/repo", "beads", "alice"); err != nil {
		t.Fatalf("DoltClone: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

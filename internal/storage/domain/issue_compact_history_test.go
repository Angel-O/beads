package domain

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

type compactHistoryRepositoryStub struct {
	IssueSQLRepository
	gotLimit int
	result   []storage.CompactIssueHistory
}

func (r *compactHistoryRepositoryStub) CompactBulkHistory(_ context.Context, _ []string, limit int) ([]storage.CompactIssueHistory, error) {
	r.gotLimit = limit
	return r.result, nil
}

func TestIssueUseCaseForwardsCompactBulkHistory(t *testing.T) {
	want := []storage.CompactIssueHistory{{IssueID: "test-1", States: []storage.CompactHistoryState{}}}
	repo := &compactHistoryRepositoryStub{result: want}
	useCase := NewIssueUseCase(repo, nil, nil, nil, nil, nil, nil, nil, nil)

	got, err := useCase.CompactBulkHistory(t.Context(), []string{"test-1"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if repo.gotLimit != 3 {
		t.Fatalf("limit forwarded = %d, want 3", repo.gotLimit)
	}
	if len(got) != 1 || got[0].IssueID != want[0].IssueID || got[0].States == nil {
		t.Fatalf("compact history = %#v, want %#v", got, want)
	}
}

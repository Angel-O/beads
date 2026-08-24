package backend_test

import (
	"context"

	"github.com/steveyegge/beads/backend"
)

type optionalBulkHistoryBackend struct{}

func (optionalBulkHistoryBackend) BulkHistory(context.Context, []string) ([]backend.IssueHistory, error) {
	return nil, nil
}

var _ backend.BulkHistoryViewer = optionalBulkHistoryBackend{}

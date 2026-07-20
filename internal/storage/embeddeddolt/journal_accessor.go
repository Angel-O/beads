//go:build cgo

package embeddeddolt

import (
	"context"
	"database/sql"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
)

// EmbeddedDoltStore reads and prunes the durable mutations journal through its
// own per-operation connection (withConn), so the `bd mutations` CLI works in
// embedded mode — where there is no stable *sql.DB to hand out via RawDBAccessor.
var _ storage.MutationsJournalAccessor = (*EmbeddedDoltStore)(nil)

// ReadMutationsJournal returns journal rows with seq greater than since. The
// read runs in a rolled-back transaction (no writes), matching every other
// read on this store.
func (s *EmbeddedDoltStore) ReadMutationsJournal(ctx context.Context, since int64, limit int) ([]storage.MutationsJournalRow, error) {
	var out []storage.MutationsJournalRow
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var readErr error
		out, readErr = issueops.ReadMutationsInTx(ctx, tx, since, limit)
		return readErr
	})
	return out, err
}

// PruneMutationsJournal deletes journal rows below before, honoring the retain
// floors, and returns the number of rows deleted. The delete commits.
func (s *EmbeddedDoltStore) PruneMutationsJournal(ctx context.Context, before int64, retainDays, retainRows int) (int64, error) {
	var n int64
	err := s.withConn(ctx, true, func(tx *sql.Tx) error {
		var pruneErr error
		n, pruneErr = issueops.PruneMutationsInTx(ctx, tx, before, retainDays, retainRows, time.Now().UTC())
		return pruneErr
	})
	return n, err
}

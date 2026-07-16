//go:build cgo

package embeddeddolt

import (
	"context"
	"database/sql"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
)

var _ storage.BackendMigrationSource = (*EmbeddedDoltStore)(nil)

// SnapshotBackendMigration performs the portability census and captures every
// portable application row inside one retained Dolt transaction.
func (s *EmbeddedDoltStore) SnapshotBackendMigration(ctx context.Context) (storage.BackendMigrationSnapshot, storage.BackendMigrationPortabilityReport, error) {
	var snapshot storage.BackendMigrationSnapshot
	var report storage.BackendMigrationPortabilityReport
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		snapshot, report, err = issueops.SnapshotBackendMigrationInTx(ctx, tx)
		return err
	})
	return snapshot, report, err
}

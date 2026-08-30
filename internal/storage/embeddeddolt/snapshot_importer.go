//go:build cgo

package embeddeddolt

import (
	"context"
	"database/sql"
	"fmt"

	storageissueops "github.com/steveyegge/beads/internal/storage/issueops"
	publicops "github.com/steveyegge/beads/issueops"
)

// SnapshotImporter exposes the explicit snapshot capability on the public
// store, while keeping the transaction runner and SQL implementation private.
func (s *EmbeddedDoltStore) SnapshotImporter() (publicops.SnapshotImporter, error) {
	if s == nil {
		return nil, fmt.Errorf("snapshot importer: store is not open")
	}
	return &embeddedSnapshotImporter{store: s}, nil
}

type embeddedSnapshotImporter struct {
	store *EmbeddedDoltStore
}

var _ publicops.SnapshotImporter = (*embeddedSnapshotImporter)(nil)

func (i *embeddedSnapshotImporter) PlanIDs(ctx context.Context, request publicops.SnapshotIDPlanRequest) (publicops.SnapshotIDPlan, error) {
	var plan publicops.SnapshotIDPlan
	err := i.store.runIssueOperationTxWithMessage(ctx, func(tx *sql.Tx) (storageissueops.ChangedTables, string, error) {
		var err error
		plan, err = storageissueops.PlanSnapshotIDsInTx(ctx, tx, request)
		return nil, "", err
	})
	return plan, err
}

func (i *embeddedSnapshotImporter) ImportSnapshot(ctx context.Context, request publicops.SnapshotImportRequest) (publicops.SnapshotImportResult, error) {
	normalized, result, err := storageissueops.PrepareSnapshotImport(request)
	if err != nil {
		return publicops.SnapshotImportResult{}, err
	}
	var applied publicops.SnapshotImportResult
	err = i.store.runIssueOperationTxWithMessage(ctx, func(tx *sql.Tx) (storageissueops.ChangedTables, string, error) {
		applied, err = storageissueops.ApplySnapshotImportInTx(ctx, tx, normalized, result)
		if err != nil {
			return nil, "", err
		}
		if !applied.Applied {
			return nil, "", nil
		}
		return storageissueops.SnapshotChangedTables(), fmt.Sprintf("bd snapshot import: %s", applied.Digest), nil
	})
	if err != nil {
		return publicops.SnapshotImportResult{}, err
	}
	return applied, nil
}

package uow

import (
	"context"
	"fmt"

	storageissueops "github.com/steveyegge/beads/internal/storage/issueops"
	publicops "github.com/steveyegge/beads/issueops"
)

// SnapshotImporterSource is the capability accessor for the explicit snapshot
// copy operation. It is separate from ImporterSource because snapshot copying
// has replacement semantics, identity remapping, durable-history copying, and
// staged sidecar output; it is not an incremental import/upsert.
type SnapshotImporterSource interface {
	SnapshotImporter() (publicops.SnapshotImporter, error)
}

func (p *doltSQLProvider) SnapshotImporter() (publicops.SnapshotImporter, error) {
	return NewSnapshotImporter(p)
}

func NewSnapshotImporter(provider UnitOfWorkProvider) (publicops.SnapshotImporter, error) {
	if isNilUnitOfWorkProvider(provider) {
		return nil, fmt.Errorf("new snapshot importer: unit-of-work provider must not be nil")
	}
	return &snapshotImporter{provider: provider}, nil
}

type snapshotImporter struct {
	provider UnitOfWorkProvider
}

var _ publicops.SnapshotImporter = (*snapshotImporter)(nil)

func (o *snapshotImporter) ImportSnapshot(ctx context.Context, request publicops.SnapshotImportRequest) (publicops.SnapshotImportResult, error) {
	normalized, result, err := storageissueops.PrepareSnapshotImport(request)
	if err != nil {
		return publicops.SnapshotImportResult{}, err
	}
	return RunTxResult(ctx, o.provider, func(ctx context.Context, uw UnitOfWork) (publicops.SnapshotImportResult, string, error) {
		runner, err := importStatementRunner(uw)
		if err != nil {
			return publicops.SnapshotImportResult{}, "", err
		}
		applied, err := storageissueops.ApplySnapshotImportInTx(ctx, runner, normalized, result)
		if err != nil {
			return publicops.SnapshotImportResult{}, "", err
		}
		if !applied.Applied {
			return applied, "", nil
		}
		return applied, fmt.Sprintf("bd snapshot import: %s", applied.Digest), nil
	})
}

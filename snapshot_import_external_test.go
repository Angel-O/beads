package beads_test

import (
	"context"
	"testing"

	"github.com/steveyegge/beads"
)

// snapshotImporterConsumer is intentionally compiled as an external package:
// a viewer must acquire and invoke the capability without importing internal
// storage or UOW packages.
func snapshotImporterConsumer(ctx context.Context, store beads.Storage, request beads.SnapshotImportRequest) error {
	importer, ok := beads.AsSnapshotImporter(store)
	if !ok {
		return nil
	}
	_, err := importer.ImportSnapshot(ctx, request)
	return err
}

type publicSnapshotSource struct{}

func (publicSnapshotSource) SnapshotImporter() (beads.SnapshotImporter, error) {
	return nil, nil
}

func TestSnapshotImporterPublicCapabilityCompilesForExternalConsumers(t *testing.T) {
	var source beads.SnapshotImporterSource = publicSnapshotSource{}
	if _, err := source.SnapshotImporter(); err != nil {
		t.Fatal(err)
	}
	if err := snapshotImporterConsumer(context.Background(), nil, beads.SnapshotImportRequest{}); err != nil {
		t.Fatal(err)
	}
}

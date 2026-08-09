//go:build cgo

package embeddeddolt_test

// PROTOTYPE ONLY: embedded reopen evidence for Memory Beads spike A3. The
// production embedded provider remains unchanged.

import (
	"context"
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/storage/doltmemorymigration"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/storage/schema"
)

func TestMemoryMigrationA3Closure_EmbeddedProcessReopenResumesLostAcknowledgement(t *testing.T) {
	ctx := t.Context()
	fixture := newPristineEmbeddedDoltFixture(t, "memory_a3_reopen")
	closeEmbeddedDoltStore(t, fixture.store)

	db, cleanup, err := embeddeddolt.OpenSQL(ctx, fixture.dataDir, fixture.database, "main")
	if err != nil {
		t.Fatalf("open first embedded engine: %v", err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := doltmemorymigration.InstallPrototypeSchema(ctx, conn); err != nil {
		t.Fatalf("install embedded A3 fixture: %v", err)
	}
	if _, err := conn.ExecContext(ctx,
		"REPLACE INTO config (`key`, value) VALUES (?, 'embedded body')",
		doltmemorymigration.LegacyPrefix()+"embedded"); err != nil {
		t.Fatal(err)
	}
	if err := schema.DrainCall(ctx, conn, "CALL DOLT_ADD('config')"); err != nil {
		t.Fatal(err)
	}
	if err := schema.DrainCall(ctx, conn,
		"CALL DOLT_COMMIT('-m', 'a3 fixture: embedded legacy', '--author', 'Ada Example <ada@example.com>')"); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	lostAck := errors.New("a3 embedded fixture: lost publication acknowledgement")
	first, err := doltmemorymigration.New(db, doltmemorymigration.Options{
		ProjectID: "018f6df0-7b4b-7a20-9d31-4f517f2860c1",
		Author:    "Ada Example <ada@example.com>",
		OnEvent: func(_ context.Context, event doltmemorymigration.Event) error {
			if event.Point == doltmemorymigration.EventBranchPublished {
				return &doltmemorymigration.PublicationIndeterminateError{Branch: event.Branch, Cause: lostAck}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Run(ctx); !errors.Is(err, lostAck) {
		t.Fatalf("first embedded migration error = %v, want lost acknowledgement", err)
	} else {
		var indeterminate *doltmemorymigration.PublicationIndeterminateError
		if !errors.As(err, &indeterminate) {
			t.Fatalf("first embedded migration error = %v, want typed indeterminate publication", err)
		}
	}
	if err := cleanup(); err != nil {
		t.Fatalf("close first embedded engine: %v", err)
	}

	// Reopening the directory creates a new connector and SQL engine; no Go
	// coordinator state survives this boundary.
	reopened, reopenedCleanup, err := embeddeddolt.OpenSQL(ctx, fixture.dataDir, fixture.database, "main")
	if err != nil {
		t.Fatalf("reopen embedded engine: %v", err)
	}
	defer func() {
		if err := reopenedCleanup(); err != nil {
			t.Errorf("close reopened embedded engine: %v", err)
		}
	}()
	if gateErr := doltmemorymigration.CheckMemoryAccess(ctx, reopened); gateErr == nil {
		t.Fatal("reopened embedded provider served memory before recovery")
	} else {
		var inProgress *doltmemorymigration.MigrationInProgressError
		if !errors.As(gateErr, &inProgress) {
			t.Fatalf("reopened embedded gate = %v, want typed migration_in_progress", gateErr)
		}
	}

	resume, err := doltmemorymigration.New(reopened, doltmemorymigration.Options{
		ProjectID: "018f6df0-7b4b-7a20-9d31-4f517f2860c1",
		Author:    "Ada Example <ada@example.com>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resume.Run(ctx); err != nil {
		t.Fatalf("resume after embedded reopen: %v", err)
	}
	if err := doltmemorymigration.CheckMemoryAccess(ctx, reopened); err != nil {
		t.Fatalf("completed embedded gate: %v", err)
	}
	var canonical, legacy int
	if err := reopened.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+doltmemorymigration.PrototypeBeadsTable()+" WHERE legacy_key = 'embedded'").Scan(&canonical); err != nil {
		t.Fatal(err)
	}
	if err := reopened.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM config WHERE `key` LIKE CONCAT(?, '%')", doltmemorymigration.LegacyPrefix()).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if canonical != 1 || legacy != 0 {
		t.Fatalf("embedded recovery canonical=%d legacy=%d, want 1/0", canonical, legacy)
	}
}

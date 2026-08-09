package uow

// PROTOTYPE ONLY: proxied unit-of-work evidence for Memory Beads spike A3.
// The gate is invoked explicitly at the private transaction runner seam; no
// public memory interface or production provider is changed by this test.

import (
	"context"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage/doltmemorymigration"
	"github.com/steveyegge/beads/internal/storage/domain/db"
	"github.com/stretchr/testify/require"
)

func TestMemoryMigrationA3Closure_ProxiedUOWPreservesTypedGateAndTeamServerRefusal(t *testing.T) {
	harness := newTeamServerHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	provider, err := harness.openProvider(ctx, "memory_a3_uow", false, "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = provider.Close(context.Background()) })
	sqlProvider, ok := provider.(*doltSQLProvider)
	require.True(t, ok)

	conn, err := sqlProvider.db.Conn(ctx)
	require.NoError(t, err)
	require.NoError(t, doltmemorymigration.InstallPrototypeSchema(ctx, conn))
	require.NoError(t, conn.Close())

	legacyKey := doltmemorymigration.LegacyPrefix() + "proxied"
	require.NoError(t, RunTx(ctx, provider, func(ctx context.Context, uw UnitOfWork) (string, error) {
		if err := uw.ConfigUseCase().SetConfig(ctx, legacyKey, "proxied before marker"); err != nil {
			return "", err
		}
		return "a3 fixture: proxied legacy", nil
	}))

	// The real provider flag drives the refusal: bd must not invent a local
	// migration for storage whose schema lifecycle belongs to team-server.
	teamProvider, err := harness.openProvider(ctx, "memory_a3_uow", true, "")
	require.NoError(t, err)
	teamSQLProvider, ok := teamProvider.(*doltSQLProvider)
	require.True(t, ok)
	require.True(t, teamSQLProvider.teamServer)
	teamCoordinator, err := doltmemorymigration.New(teamSQLProvider.db, doltmemorymigration.Options{
		ProjectID:     "018f6df0-7b4b-7a20-9d31-4f517f2860c1",
		Author:        "Ada Example <ada@example.com>",
		ExternalOwner: a3ExternalOwner(teamSQLProvider),
	})
	require.NoError(t, err)
	_, err = teamCoordinator.Run(ctx)
	var ownerErr *doltmemorymigration.ExternalOwnerError
	require.ErrorAs(t, err, &ownerErr)
	require.Contains(t, err.Error(), "ask its operator")
	require.NoError(t, teamProvider.Close(ctx))
	_, found, err := doltmemorymigration.InspectControl(ctx, sqlProvider.db)
	require.NoError(t, err)
	require.False(t, found, "team-server refusal must not install a migration marker")

	markerCommitted := make(chan struct{})
	allowMigration := make(chan struct{})
	coordinator, err := doltmemorymigration.New(sqlProvider.db, doltmemorymigration.Options{
		ProjectID: "018f6df0-7b4b-7a20-9d31-4f517f2860c1",
		Author:    "Ada Example <ada@example.com>",
		OnEvent: func(ctx context.Context, event doltmemorymigration.Event) error {
			if event.Point != doltmemorymigration.EventControlCommitted {
				return nil
			}
			close(markerCommitted)
			select {
			case <-allowMigration:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	require.NoError(t, err)
	migrationDone := make(chan error, 1)
	go func() {
		_, runErr := coordinator.Run(ctx)
		migrationDone <- runErr
	}()
	select {
	case <-markerCommitted:
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for proxied migration marker")
	}

	// A write through RunTx reaches the real proxied BeginTx/retry/rollback
	// path. The private gate's typed result must survive that plumbing and the
	// domain config repository must never run.
	writeErr := RunTx(ctx, provider, func(ctx context.Context, uw UnitOfWork) (string, error) {
		runner := a3UOWRunner(t, uw)
		if err := doltmemorymigration.AdmitConfigMutation(ctx, runner, legacyKey); err != nil {
			return "", err
		}
		return "must not commit", uw.ConfigUseCase().SetConfig(ctx, legacyKey, "must not land")
	})
	var inProgress *doltmemorymigration.MigrationInProgressError
	require.ErrorAs(t, writeErr, &inProgress)

	_, readErr := RunTxRead(ctx, provider, func(ctx context.Context, uw UnitOfWork) (string, error) {
		if err := doltmemorymigration.CheckMemoryAccess(ctx, a3UOWRunner(t, uw)); err != nil {
			return "", err
		}
		return uw.ConfigUseCase().GetConfig(ctx, legacyKey)
	})
	require.ErrorAs(t, readErr, &inProgress)

	var source string
	require.NoError(t, sqlProvider.db.QueryRowContext(ctx,
		"SELECT value FROM config WHERE `key` = ?", legacyKey).Scan(&source))
	require.Equal(t, "proxied before marker", source)

	close(allowMigration)
	select {
	case err := <-migrationDone:
		require.NoError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatal("timed out finishing proxied migration")
	}

	canonicalBody, err := RunTxRead(ctx, provider, func(ctx context.Context, uw UnitOfWork) (string, error) {
		runner := a3UOWRunner(t, uw)
		if err := doltmemorymigration.CheckMemoryAccess(ctx, runner); err != nil {
			return "", err
		}
		var body string
		err := runner.QueryRowContext(ctx,
			"SELECT body FROM "+doltmemorymigration.PrototypeBeadsTable()+" WHERE legacy_key = 'proxied'").Scan(&body)
		return body, err
	})
	require.NoError(t, err)
	require.Equal(t, "proxied before marker", canonicalBody)

	retiredErr := RunTx(ctx, provider, func(ctx context.Context, uw UnitOfWork) (string, error) {
		if err := doltmemorymigration.AdmitConfigMutation(ctx, a3UOWRunner(t, uw), legacyKey); err != nil {
			return "", err
		}
		return "must not commit", uw.ConfigUseCase().SetConfig(ctx, legacyKey, "resurrected")
	})
	var retired *doltmemorymigration.LegacyNamespaceRetiredError
	require.ErrorAs(t, retiredErr, &retired)

	require.NoError(t, RunTx(ctx, provider, func(ctx context.Context, uw UnitOfWork) (string, error) {
		const ordinaryKey = "a3.ordinary.config"
		if err := doltmemorymigration.AdmitConfigMutation(ctx, a3UOWRunner(t, uw), ordinaryKey); err != nil {
			return "", err
		}
		return "a3 fixture: ordinary post-migration config", uw.ConfigUseCase().SetConfig(ctx, ordinaryKey, "allowed")
	}))
}

func a3UOWRunner(t *testing.T, uw UnitOfWork) db.Runner {
	t.Helper()
	base, ok := uw.(*baseUOW)
	require.True(t, ok, "expected *baseUOW, got %T", uw)
	return base.tx.Runner()
}

func a3ExternalOwner(provider *doltSQLProvider) string {
	if provider != nil && provider.teamServer {
		return "beads-team-server"
	}
	return ""
}

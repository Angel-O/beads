//go:build cgo

package embeddeddolt

// PROTOTYPE ONLY: this file connects the provider-neutral A2 contract to the
// real embedded transaction boundary. Its DDL is fixture-only and deliberately
// absent from production migrations.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	a2 "github.com/steveyegge/beads/internal/memorybeads/spikea2"
	"github.com/steveyegge/beads/internal/memorybeads/spikea2/conformance"
	"github.com/steveyegge/beads/internal/memorybeads/spikea2/sqlprototype"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/domain"
	domaindb "github.com/steveyegge/beads/internal/storage/domain/db"
	"github.com/steveyegge/beads/internal/storage/versioncontrolops"
)

type embeddedA2Backend struct {
	mu       sync.RWMutex
	store    *EmbeddedDoltStore
	beadsDir string
	database string

	faultMu      sync.Mutex
	stageErrOnce error
}

func newEmbeddedA2Backend(t *testing.T) *embeddedA2Backend {
	t.Helper()
	backend := &embeddedA2Backend{
		beadsDir: filepath.Join(t.TempDir(), ".beads"),
		database: "memory_a2",
	}
	if err := backend.open(t.Context()); err != nil {
		t.Fatalf("open embedded A2 provider: %v", err)
	}
	t.Cleanup(func() {
		backend.mu.Lock()
		defer backend.mu.Unlock()
		if backend.store != nil {
			if err := backend.store.Close(); err != nil {
				t.Errorf("close embedded A2 provider: %v", err)
			}
			backend.store = nil
		}
	})
	return backend
}

func (b *embeddedA2Backend) open(ctx context.Context) error {
	store, err := Open(ctx, b.beadsDir, b.database, "main")
	if err != nil {
		return err
	}
	b.store = store
	return nil
}

func (b *embeddedA2Backend) reopen(t *testing.T) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.store.Close(); err != nil {
		t.Fatalf("close embedded A2 provider for reopen: %v", err)
	}
	b.store = nil
	if err := b.open(t.Context()); err != nil {
		t.Fatalf("reopen embedded A2 provider: %v", err)
	}
}

func (b *embeddedA2Backend) failVersionPublicationOnce(err error) {
	b.faultMu.Lock()
	defer b.faultMu.Unlock()
	b.stageErrOnce = err
}

func (b *embeddedA2Backend) takeVersionPublicationFailure() error {
	b.faultMu.Lock()
	defer b.faultMu.Unlock()
	err := b.stageErrOnce
	b.stageErrOnce = nil
	return err
}

type embeddedA2Adapter struct {
	backend *embeddedA2Backend
}

func (a embeddedA2Adapter) Read(ctx context.Context, fn func(sqlprototype.Session) error) error {
	a.backend.mu.RLock()
	defer a.backend.mu.RUnlock()
	return a.backend.store.withConn(ctx, false, func(tx *sql.Tx) error {
		return fn(rawA2Session{raw: domain.NewRawSQLUseCase(domaindb.NewRawSQLRepository(tx))})
	})
}

func (a embeddedA2Adapter) Publish(ctx context.Context, message string, fn func(sqlprototype.Session) error) sqlprototype.Publication {
	a.backend.mu.RLock()
	defer a.backend.mu.RUnlock()

	// Keep the SQL publication and the later Dolt version commit as separate
	// observations. A failed SQL commit response is genuinely unknown. Once
	// SQL commit succeeds, however, canonical Memory rows are visible even if
	// the provider cannot confirm its version commit; that is known application
	// with incomplete verification, not an indeterminate mutation.
	var dirty versioncontrolops.DirtyTableTracker
	err := a.backend.store.withConn(ctx, true, func(tx *sql.Tx) error {
		session := rawA2Session{raw: domain.NewRawSQLUseCase(domaindb.NewRawSQLRepository(tx))}
		if err := fn(session); err != nil {
			return err
		}
		for _, table := range sqlprototype.TableNames() {
			dirty.MarkDirty(table)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, storage.ErrCommitIndeterminate) {
			return sqlprototype.Publication{State: sqlprototype.Unknown, Err: err}
		}
		return sqlprototype.Publication{State: sqlprototype.NotPublished, Err: err}
	}
	if injected := a.backend.takeVersionPublicationFailure(); injected != nil {
		return sqlprototype.Publication{State: sqlprototype.Published, Err: injected}
	}
	err = a.backend.store.withMutatingDBConn(ctx, func(db versioncontrolops.DBConn) error {
		return versioncontrolops.StageAndCommit(ctx, db, dirty.DirtyTables(), message, commitAuthor)
	})
	if err != nil {
		return sqlprototype.Publication{State: sqlprototype.Published, Err: err}
	}
	return sqlprototype.Publication{State: sqlprototype.Published}
}

type rawA2Session struct {
	raw domain.RawSQLUseCase
}

func (s rawA2Session) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	return s.raw.Exec(ctx, query, args...)
}

func (s rawA2Session) Query(ctx context.Context, query string, args ...any) ([][]any, error) {
	result, err := s.raw.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return result.Rows, nil
}

func TestMemoryA2SQLPrototypeEmbeddedProviderContract(t *testing.T) {
	backend := newEmbeddedA2Backend(t)
	adapter := embeddedA2Adapter{backend: backend}
	installed := adapter.Publish(t.Context(), "spike A2: install throwaway schema", func(session sqlprototype.Session) error {
		return sqlprototype.Install(t.Context(), session)
	})
	if installed.State != sqlprototype.Published || installed.Err != nil {
		t.Fatalf("install embedded A2 schema: %+v", installed)
	}

	var projectSequence atomic.Uint64
	conformance.Run(t, func(t *testing.T) conformance.Fixture {
		t.Helper()
		projectID := a2.ProjectID(fmt.Sprintf("embedded-a2-project-%d", projectSequence.Add(1)))
		newFixture := func() conformance.Fixture {
			module := sqlprototype.New(adapter, projectID)
			return conformance.Fixture{
				Module:      module,
				Publication: module,
				// These are persisted Memory head-set controls over the real embedded
				// publication seam. They prove the branch-independent revision model;
				// production bd branch-command wiring remains a separate integration.
				Branches: module,
			}
		}
		fixture := newFixture()
		fixture.Maintain = func() { backend.reopen(t) }
		fixture.Reconstruct = newFixture
		return fixture
	})
}

func TestMemoryA2EmbeddedPostSQLVersionFailureIsAppliedUnverified(t *testing.T) {
	backend := newEmbeddedA2Backend(t)
	adapter := embeddedA2Adapter{backend: backend}
	installed := adapter.Publish(t.Context(), "spike A2: install throwaway schema", func(session sqlprototype.Session) error {
		return sqlprototype.Install(t.Context(), session)
	})
	if installed.State != sqlprototype.Published || installed.Err != nil {
		t.Fatalf("install embedded A2 schema: %+v", installed)
	}

	backend.failVersionPublicationOnce(errors.New("injected version publication failure after SQL commit"))
	module := sqlprototype.New(adapter, a2.ProjectID("embedded-a2-outcome-project"))
	result, err := module.Mutate(t.Context(), a2.Mutation{
		Title: "Known application", Body: "canonical rows commit first", Author: "Spike <spike@example.test>",
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if result.Outcome != a2.OutcomeAppliedUnverified || result.Address.RevisionID == "" || result.Revision != nil {
		t.Fatalf("post-SQL failure result = %+v", result)
	}
	stored, err := module.Read(t.Context(), a2.ReadRequest{BeadID: result.Address.BeadID, RevisionID: result.Address.RevisionID})
	if err != nil {
		t.Fatalf("known-applied exact read: %v", err)
	}
	if stored.Address != result.Address || stored.Body != "canonical rows commit first" {
		t.Fatalf("known-applied state = %+v", stored)
	}
}

package uow

// PROTOTYPE ONLY: this file connects the provider-neutral A2 contract to a
// real proxied Dolt-server unit of work. Its tables are installed only by the
// fixture and are not production schema.

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	a2 "github.com/steveyegge/beads/internal/memorybeads/spikea2"
	"github.com/steveyegge/beads/internal/memorybeads/spikea2/conformance"
	"github.com/steveyegge/beads/internal/memorybeads/spikea2/sqlprototype"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/dbproxy/proxy"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/testutil"
)

type uowA2Backend struct {
	mu       sync.RWMutex
	provider UnitOfWorkProvider

	storeRootDir string
	database     string
	logPath      string
	configPath   string
	doltBin      string
}

func newUOWA2Backend(t *testing.T) *uowA2Backend {
	t.Helper()
	testutil.RequireDoltBinary(t)
	doltBin, err := exec.LookPath("dolt")
	if err != nil {
		t.Fatalf("find dolt for A2 proxied provider: %v", err)
	}

	bdBin := buildBDBinary(t)
	previousResolver := proxy.ResolveExecutable
	proxy.ResolveExecutable = func() (string, error) { return bdBin, nil }
	t.Cleanup(func() { proxy.ResolveExecutable = previousResolver })
	t.Setenv("HOME", t.TempDir())

	port, err := proxy.PickFreePort()
	if err != nil {
		t.Fatalf("pick A2 proxied provider port: %v", err)
	}
	backend := &uowA2Backend{
		storeRootDir: t.TempDir(),
		database:     "memory_a2",
		logPath:      filepath.Join(t.TempDir(), "server.log"),
		configPath:   writeServerConfig(t, port),
		doltBin:      doltBin,
	}
	shutdownOnInterrupt(t, backend.storeRootDir)
	if err := backend.open(t.Context()); err != nil {
		t.Fatalf("open A2 proxied provider: %v", err)
	}
	t.Cleanup(func() {
		backend.mu.Lock()
		defer backend.mu.Unlock()
		if backend.provider != nil {
			if err := backend.provider.Close(context.Background()); err != nil {
				t.Errorf("close A2 proxied provider: %v", err)
			}
			backend.provider = nil
		}
		if err := proxy.Shutdown(backend.storeRootDir); err != nil {
			t.Logf("proxy.Shutdown(%s): %v", backend.storeRootDir, err)
		}
	})
	return backend
}

func (b *uowA2Backend) open(ctx context.Context) error {
	provider, err := NewDoltServerUOWProvider(
		ctx,
		b.storeRootDir,
		b.database,
		b.logPath,
		b.configPath,
		proxy.BackendLocalServer,
		"root",
		"",
		b.doltBin,
		0,
		0,
		false,
		"",
	)
	if err != nil {
		return err
	}
	b.provider = provider
	return nil
}

func (b *uowA2Backend) reopen(t *testing.T) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.provider.Close(t.Context()); err != nil {
		t.Fatalf("close A2 proxied provider for reopen: %v", err)
	}
	b.provider = nil
	if err := b.open(t.Context()); err != nil {
		t.Fatalf("reopen A2 proxied provider: %v", err)
	}
}

type uowA2Adapter struct {
	backend *uowA2Backend
}

func (a uowA2Adapter) Read(ctx context.Context, fn func(sqlprototype.Session) error) error {
	a.backend.mu.RLock()
	defer a.backend.mu.RUnlock()
	_, err := RunTxRead(ctx, a.backend.provider, func(ctx context.Context, uw UnitOfWork) (struct{}, error) {
		return struct{}{}, fn(uowRawA2Session{raw: uw.RawSQLUseCase()})
	})
	return err
}

func (a uowA2Adapter) Publish(ctx context.Context, message string, fn func(sqlprototype.Session) error) sqlprototype.Publication {
	a.backend.mu.RLock()
	defer a.backend.mu.RUnlock()
	_, err := RunTxResult(ctx, a.backend.provider, func(ctx context.Context, uw UnitOfWork) (struct{}, string, error) {
		if err := fn(uowRawA2Session{raw: uw.RawSQLUseCase()}); err != nil {
			return struct{}{}, "", err
		}
		return struct{}{}, message, nil
	})
	if err == nil {
		return sqlprototype.Publication{State: sqlprototype.Published}
	}
	if errors.Is(err, storage.ErrCommitIndeterminate) {
		return sqlprototype.Publication{State: sqlprototype.Unknown, Err: err}
	}
	return sqlprototype.Publication{State: sqlprototype.NotPublished, Err: err}
}

type uowRawA2Session struct {
	raw domain.RawSQLUseCase
}

func (s uowRawA2Session) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	return s.raw.Exec(ctx, query, args...)
}

func (s uowRawA2Session) Query(ctx context.Context, query string, args ...any) ([][]any, error) {
	result, err := s.raw.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return result.Rows, nil
}

func TestMemoryA2SQLPrototypeProxiedProviderContract(t *testing.T) {
	backend := newUOWA2Backend(t)
	adapter := uowA2Adapter{backend: backend}
	installed := adapter.Publish(t.Context(), "spike A2: install throwaway schema", func(session sqlprototype.Session) error {
		return sqlprototype.Install(t.Context(), session)
	})
	if installed.State != sqlprototype.Published || installed.Err != nil {
		t.Fatalf("install proxied A2 schema: %+v", installed)
	}

	var projectSequence atomic.Uint64
	conformance.Run(t, func(t *testing.T) conformance.Fixture {
		t.Helper()
		projectID := a2.ProjectID(fmt.Sprintf("proxied-a2-project-%d", projectSequence.Add(1)))
		newFixture := func() conformance.Fixture {
			module := sqlprototype.New(adapter, projectID)
			return conformance.Fixture{
				Module:      module,
				Publication: module,
				// These are persisted Memory head-set controls over the real proxied
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

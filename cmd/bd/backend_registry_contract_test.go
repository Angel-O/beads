package main

// Extension-contract tests for the backend registry: registering a backend
// by name (one additive registrant file in a real extension) is enough for
// the metadata-driven store factories to dispatch to it, wins over the
// removed-backend tombstones, and leaves unregistered names failing closed
// exactly as before.

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/backends"
	"github.com/steveyegge/beads/internal/storage/sqlite"
)

var (
	errContractOpen         = errors.New("contract backend: read-write open")
	errContractOpenReadOnly = errors.New("contract backend: read-only open")
)

// registerContractBackend registers a fixture backend whose open functions
// fail with recognizable sentinels, proving dispatch reached the registrant
// without standing up a real store.
func registerContractBackend(t *testing.T, name string) {
	t.Helper()
	backends.Register(name, backends.Backend{
		Open: func(context.Context, string) (storage.DoltStorage, error) {
			return nil, errContractOpen
		},
		OpenReadOnly: func(context.Context, string) (storage.DoltStorage, error) {
			return nil, errContractOpenReadOnly
		},
	})
	t.Cleanup(func() { backends.Deregister(name) })
}

func writeBackendWorkspace(t *testing.T, backend string) string {
	t.Helper()
	beadsDir := t.TempDir()
	if err := (&configfile.Config{Backend: backend}).Save(beadsDir); err != nil {
		t.Fatalf("save metadata.json: %v", err)
	}
	return beadsDir
}

// TestRegisteredBackendProvisionOptionsAndPersistRoundTrip pins the generic
// provisioning contract: options reach the registrant's Provision hook via
// ProvisionRequest untouched, and the persist entries it returns land in
// metadata.json's backend_config map and survive a reload.
func TestRegisteredBackendProvisionOptionsAndPersistRoundTrip(t *testing.T) {
	const name = "contractkv-provision"
	wantPersist := map[string]string{"dsn": "contract://server/db", "schema": "beads_ws"}
	var got backends.ProvisionRequest
	b := backends.Backend{
		Open: func(context.Context, string) (storage.DoltStorage, error) {
			return nil, errContractOpen
		},
		OpenReadOnly: func(context.Context, string) (storage.DoltStorage, error) {
			return nil, errContractOpenReadOnly
		},
		Provision: func(_ context.Context, req backends.ProvisionRequest) (map[string]string, error) {
			got = req
			return wantPersist, nil
		},
	}
	backends.Register(name, b)
	t.Cleanup(func() { backends.Deregister(name) })

	beadsDir := t.TempDir()
	options := map[string]string{"dsn": "contract://server/db", "schema": "beads_ws"}

	reg, ok := backends.Lookup(name)
	if !ok || reg.Provision == nil {
		t.Fatalf("Lookup(%q) lost the Provision hook", name)
	}
	persist, err := reg.Provision(t.Context(), backends.ProvisionRequest{BeadsDir: beadsDir, Options: options})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if got.BeadsDir != beadsDir {
		t.Errorf("ProvisionRequest.BeadsDir = %q, want %q", got.BeadsDir, beadsDir)
	}
	if !maps.Equal(got.Options, options) {
		t.Errorf("ProvisionRequest.Options = %v, want %v", got.Options, options)
	}

	// Persist plumbing: init records the returned entries via
	// applyBackendPersist; they must round-trip through metadata.json.
	cfg := configfile.DefaultConfig()
	cfg.Backend = name
	applyBackendPersist(cfg, name, persist)
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatalf("save metadata.json: %v", err)
	}
	loaded, err := configfile.Load(beadsDir)
	if err != nil {
		t.Fatalf("reload metadata.json: %v", err)
	}
	if !maps.Equal(loaded.GetBackendConfig(), wantPersist) {
		t.Errorf("backend_config round-trip = %v, want %v", loaded.GetBackendConfig(), wantPersist)
	}
	if loaded.SQLitePath != "" {
		t.Errorf("non-sqlite persist leaked into legacy sqlite_path: %q", loaded.SQLitePath)
	}
}

// TestApplyBackendPersistSQLiteLegacyPath pins the compatibility mapping: the
// sqlite backend's path entry is persisted in the legacy sqlite_path field
// (not backend_config) so existing workspaces and older bd versions keep
// reading the same location.
func TestApplyBackendPersistSQLiteLegacyPath(t *testing.T) {
	cfg := configfile.DefaultConfig()
	applyBackendPersist(cfg, configfile.BackendSQLite, map[string]string{sqlite.ProvisionOptionPath: "custom.db"})
	if cfg.SQLitePath != "custom.db" {
		t.Errorf("SQLitePath = %q, want %q", cfg.SQLitePath, "custom.db")
	}
	if len(cfg.BackendConfig) != 0 {
		t.Errorf("sqlite path entry leaked into backend_config: %v", cfg.BackendConfig)
	}
}

func TestRegisteredBackendDispatchesThroughStoreFactories(t *testing.T) {
	const name = "contractkv"
	registerContractBackend(t, name)
	beadsDir := writeBackendWorkspace(t, name)

	if _, err := newDoltStoreFromConfig(t.Context(), beadsDir); !errors.Is(err, errContractOpen) {
		t.Errorf("newDoltStoreFromConfig dispatched wrong: err = %v, want %v", err, errContractOpen)
	}
	if _, err := newReadOnlyStoreFromConfig(t.Context(), beadsDir); !errors.Is(err, errContractOpenReadOnly) {
		t.Errorf("newReadOnlyStoreFromConfig dispatched wrong: err = %v, want %v", err, errContractOpenReadOnly)
	}
	if err := validateConfiguredBackend(&configfile.Config{Backend: name}); err != nil {
		t.Errorf("validateConfiguredBackend rejected registered backend: %v", err)
	}
}

func TestRegisteredNameBeatsTombstoneInStoreFactory(t *testing.T) {
	registerContractBackend(t, configfile.BackendPostgres)
	beadsDir := writeBackendWorkspace(t, configfile.BackendPostgres)

	// Registry wins over the removed-name tombstone: the factory dispatches
	// to the registered backend instead of failing with the removal error.
	if _, err := newDoltStoreFromConfig(t.Context(), beadsDir); !errors.Is(err, errContractOpen) {
		t.Errorf("registered removed name did not dispatch: err = %v, want %v", err, errContractOpen)
	}
	if err := validateConfiguredBackend(&configfile.Config{Backend: configfile.BackendPostgres}); err != nil {
		t.Errorf("validateConfiguredBackend rejected registered removed name: %v", err)
	}
}

func TestUnregisteredRemovedNamesStillTombstoned(t *testing.T) {
	// No registration here: the tombstones must keep failing closed with the
	// standard removed-backend error surface.
	for _, backend := range []string{configfile.BackendPostgres, configfile.BackendMySQL} {
		beadsDir := writeBackendWorkspace(t, backend)
		_, err := newDoltStoreFromConfig(t.Context(), beadsDir)
		if err == nil || !strings.Contains(err.Error(), "no longer supported") {
			t.Errorf("unregistered removed name %q: err = %v, want RemovedBackendError", backend, err)
		}
		wantErr := configfile.RemovedBackendError(backend)
		if got := validateConfiguredBackend(&configfile.Config{Backend: backend}); got == nil || got.Error() != wantErr.Error() {
			t.Errorf("validateConfiguredBackend(%q) = %v, want %v", backend, got, wantErr)
		}
	}
}

func TestUnregisteredUnknownNameStillRejected(t *testing.T) {
	const name = "contract-unregistered"
	wantErr := configfile.UnknownBackendError(name)
	if got := validateConfiguredBackend(&configfile.Config{Backend: name}); got == nil || got.Error() != wantErr.Error() {
		t.Errorf("validateConfiguredBackend(%q) = %v, want %v", name, got, wantErr)
	}
	beadsDir := writeBackendWorkspace(t, name)
	if _, err := newDoltStoreFromConfig(t.Context(), beadsDir); err == nil || !strings.Contains(err.Error(), "not recognized") {
		t.Errorf("unknown backend factory error = %v, want UnknownBackendError", err)
	}
}

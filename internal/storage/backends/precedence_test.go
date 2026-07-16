package backends_test

import (
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/backends"
)

// These tests pin the classification precedence contract: a name registered
// in the backend registry is supported even when it appears on the
// removed-backend tombstone list, and the tombstones apply only to names
// nobody registered. This is what lets an out-of-tree backend revive a
// removed name without editing shared files.

func TestRegisteredNameBeatsRemovedTombstone(t *testing.T) {
	if configfile.IsSupportedBackend(configfile.BackendPostgres) {
		t.Fatal("precondition: unregistered removed name must be unsupported")
	}

	backends.Register(configfile.BackendPostgres, fakeBackend())
	t.Cleanup(func() { backends.Deregister(configfile.BackendPostgres) })

	if !configfile.IsSupportedBackend(configfile.BackendPostgres) {
		t.Error("registered removed name must be supported (registry wins over the tombstone)")
	}
	cfg := &configfile.Config{Backend: configfile.BackendPostgres}
	if got := cfg.GetBackend(); got != configfile.BackendPostgres {
		t.Errorf("GetBackend() = %q, want %q", got, configfile.BackendPostgres)
	}
	// Registration revives only the registered name; the sibling tombstone
	// still applies.
	if configfile.IsSupportedBackend(configfile.BackendMySQL) {
		t.Error("unregistered removed name must remain unsupported")
	}
}

func TestRegisteredCustomNameIsSupported(t *testing.T) {
	const name = "customkv"
	if configfile.IsSupportedBackend(name) {
		t.Fatalf("precondition: %q must be unsupported before registration", name)
	}
	cfg := &configfile.Config{Backend: name}
	if got := cfg.GetBackend(); got != configfile.BackendDolt {
		t.Fatalf("unregistered custom name should keep the historical Dolt fallback, got %q", got)
	}

	backends.Register(name, fakeBackend())
	t.Cleanup(func() { backends.Deregister(name) })

	if !configfile.IsSupportedBackend(name) {
		t.Error("registered custom name must be supported")
	}
	if got := cfg.GetBackend(); got != name {
		t.Errorf("GetBackend() = %q, want registered name %q", got, name)
	}
}

func TestDeregisteredNameRevertsToTombstone(t *testing.T) {
	backends.Register(configfile.BackendMySQL, fakeBackend())
	if !configfile.IsSupportedBackend(configfile.BackendMySQL) {
		t.Fatal("registered removed name must be supported")
	}
	backends.Deregister(configfile.BackendMySQL)
	if configfile.IsSupportedBackend(configfile.BackendMySQL) {
		t.Error("tombstone must apply again once the name is no longer registered")
	}
}

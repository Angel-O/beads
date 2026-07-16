package dolt

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/backends"
)

// TestRequireDoltBackendGuard pins the defense-in-depth backstop: metadata
// that selects any non-Dolt backend — built-in, registered at runtime, or
// unknown — must never be opened as Dolt, and the error surface for
// pre-existing names must not change.
func TestRequireDoltBackendGuard(t *testing.T) {
	fakeOpen := func(context.Context, string) (storage.DoltStorage, error) {
		return nil, errors.New("fake open")
	}

	t.Run("dolt and empty accepted", func(t *testing.T) {
		for _, cfg := range []*configfile.Config{{}, {Backend: configfile.BackendDolt}} {
			if err := requireDoltBackend(cfg); err != nil {
				t.Errorf("requireDoltBackend(%+v) = %v, want nil", cfg, err)
			}
		}
	})

	t.Run("sqlite rejected as non-dolt", func(t *testing.T) {
		err := requireDoltBackend(&configfile.Config{Backend: configfile.BackendSQLite})
		if err == nil || !strings.Contains(err.Error(), "cannot be opened as Dolt") {
			t.Errorf("sqlite guard error = %v, want non-Dolt rejection", err)
		}
	})

	t.Run("unregistered removed names keep the tombstone error", func(t *testing.T) {
		for _, backend := range []string{configfile.BackendPostgres, configfile.BackendMySQL} {
			err := requireDoltBackend(&configfile.Config{Backend: backend})
			if err == nil || !strings.Contains(err.Error(), "no longer supported") {
				t.Errorf("removed backend %q error = %v, want removal guidance", backend, err)
			}
		}
	})

	t.Run("unknown name rejected as unrecognized", func(t *testing.T) {
		err := requireDoltBackend(&configfile.Config{Backend: "mystery"})
		if err == nil || !strings.Contains(err.Error(), "not recognized") {
			t.Errorf("unknown backend error = %v, want unrecognized rejection", err)
		}
	})

	t.Run("registered custom name still rejected", func(t *testing.T) {
		const name = "guardkv-fixture"
		backends.Register(name, backends.Backend{Open: fakeOpen, OpenReadOnly: fakeOpen})
		t.Cleanup(func() { backends.Deregister(name) })

		err := requireDoltBackend(&configfile.Config{Backend: name})
		if err == nil || !strings.Contains(err.Error(), "cannot be opened as Dolt") {
			t.Errorf("registered non-Dolt backend error = %v, want non-Dolt rejection", err)
		}
	})

	t.Run("registered removed name gets non-dolt rejection not tombstone", func(t *testing.T) {
		backends.Register(configfile.BackendPostgres, backends.Backend{Open: fakeOpen, OpenReadOnly: fakeOpen})
		t.Cleanup(func() { backends.Deregister(configfile.BackendPostgres) })

		err := requireDoltBackend(&configfile.Config{Backend: configfile.BackendPostgres})
		if err == nil || !strings.Contains(err.Error(), "cannot be opened as Dolt") {
			t.Errorf("registered removed name error = %v, want non-Dolt rejection", err)
		}
		if err != nil && strings.Contains(err.Error(), "no longer supported") {
			t.Errorf("registered removed name hit the tombstone error: %v", err)
		}
	})
}

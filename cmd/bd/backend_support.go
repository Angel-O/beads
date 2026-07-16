package main

import (
	"fmt"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/backends"
)

// registeredBackendWorkspaceIsBeadsDir reports whether metadata selects a
// registered backend whose workspace IS the .beads directory itself (no
// separately discoverable database directory) — the WorkspaceIsBeadsDir
// discovery capability. Unregistered names, including the Dolt family,
// report false.
func registeredBackendWorkspaceIsBeadsDir(cfg *configfile.Config) bool {
	b, ok := backends.Lookup(cfg.GetBackend())
	return ok && b.WorkspaceIsBeadsDir
}

func validateConfiguredBackend(cfg *configfile.Config) error {
	if cfg == nil {
		return nil
	}
	switch cfg.Backend {
	case configfile.BackendPostgres, configfile.BackendMySQL:
		return configfile.RemovedBackendError(cfg.Backend)
	case "", configfile.BackendDolt, configfile.BackendSQLite:
		return nil
	default:
		return configfile.UnknownBackendError(cfg.Backend)
	}
}

func requireDoltBackend(cfg *configfile.Config) error {
	if err := validateConfiguredBackend(cfg); err != nil {
		return err
	}
	if cfg != nil && cfg.GetBackend() != configfile.BackendDolt {
		return fmt.Errorf("not using Dolt backend (configured backend %q)", cfg.GetBackend())
	}
	return nil
}

func loadDoltBackendConfig(beadsDir string) (*configfile.Config, error) {
	cfg, err := configfile.Load(beadsDir)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	if cfg == nil {
		cfg = configfile.DefaultConfig()
	}
	if err := requireDoltBackend(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

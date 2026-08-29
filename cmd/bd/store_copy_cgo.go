//go:build cgo

package main

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
)

func openOfflineStore(ctx context.Context, beadsDir string, readOnly bool) (s storage.DoltStorage, err error) {
	defer func() { s, err = activateEventsJournalStore(beadsDir, s, err) }()
	cfg, err := configfile.LoadForDiscovery(beadsDir)
	if err != nil {
		return nil, fmt.Errorf("load metadata: %w", err)
	}
	if cfg == nil {
		cfg = &configfile.Config{}
	}
	if cfg.GetBackend() != configfile.BackendDolt {
		return nil, fmt.Errorf("offline store-copy requires Dolt storage, got backend %q", cfg.GetBackend())
	}
	if cfg.IsDoltServerMode() || cfg.IsDoltProxiedServerMode() {
		return nil, fmt.Errorf("offline store-copy does not support server-mode stores")
	}
	database := configfile.DefaultDoltDatabase
	if cfg.DoltDatabase != "" {
		database = cfg.DoltDatabase
	}
	if readOnly {
		return embeddeddolt.OpenReadOnly(ctx, beadsDir, database, "main")
	}
	return embeddeddolt.Open(ctx, beadsDir, database, "main")
}

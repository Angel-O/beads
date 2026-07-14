// Package workspacestate classifies provider initialization evidence without
// exposing engine-specific probes to CLI callers.
package workspacestate

import (
	"errors"
	"fmt"

	"github.com/steveyegge/beads/internal/configfile"
	doltstorage "github.com/steveyegge/beads/internal/storage/dolt"
	sqlitestorage "github.com/steveyegge/beads/internal/storage/sqlite"
)

// ErrConflictingEvidence marks a workspace containing initialization evidence
// for more than one local storage provider.
var ErrConflictingEvidence = errors.New("conflicting local provider initialization evidence")

// LocalState describes verified local provider initialization evidence.
// Backend is empty when Initialized is false.
type LocalState struct {
	Backend     string
	Initialized bool
}

// EffectiveConfigInspection retains both the effective metadata copy and the
// exact local evidence used to resolve the narrow legacy bare-SQLite
// ambiguity. Local is nil when metadata was unambiguous and local evidence was
// deliberately not inspected; a non-nil zero LocalState means inspection
// verified that evidence was absent.
type EffectiveConfigInspection struct {
	Config configfile.Config
	Local  *LocalState
}

// InspectLocal classifies bounded, read-only Dolt and SQLite initialization
// evidence. Only verified absence returns an uninitialized state; malformed or
// conflicting evidence fails closed.
func InspectLocal(beadsDir, configuredSQLitePath string) (LocalState, error) {
	if beadsDir == "" {
		return LocalState{}, errors.New("local workspace evidence requires a workspace directory")
	}
	doltInitialized, err := doltstorage.HasLocalInitializationEvidence(beadsDir)
	if err != nil {
		return LocalState{}, fmt.Errorf("inspect Dolt initialization evidence in %q: %w", beadsDir, err)
	}
	sqliteInitialized, err := sqlitestorage.HasLocalInitializationEvidence(beadsDir, configuredSQLitePath)
	if err != nil {
		return LocalState{}, fmt.Errorf("inspect SQLite initialization evidence in %q: %w", beadsDir, err)
	}
	if doltInitialized && sqliteInitialized {
		return LocalState{}, fmt.Errorf("%w in workspace %q: Dolt and SQLite are both initialized", ErrConflictingEvidence, beadsDir)
	}
	if doltInitialized {
		return LocalState{Backend: configfile.BackendDolt, Initialized: true}, nil
	}
	if sqliteInitialized {
		return LocalState{Backend: configfile.BackendSQLite, Initialized: true}, nil
	}
	return LocalState{}, nil
}

// InspectEffectiveConfig returns a metadata copy together with any local state
// needed to resolve it. Current SQLite initialization persists sqlite_path;
// when that positive marker is absent but live Dolt evidence exists, the old
// SQLite backend value is stale rollout metadata and the effective backend is
// Dolt. Unambiguous metadata does not probe unrelated provider artifacts.
func InspectEffectiveConfig(beadsDir string, cfg *configfile.Config) (EffectiveConfigInspection, error) {
	if cfg == nil {
		return EffectiveConfigInspection{}, errors.New("effective workspace config requires metadata")
	}
	inspection := EffectiveConfigInspection{Config: *cfg}
	if inspection.Config.Backend != configfile.BackendSQLite || inspection.Config.SQLitePath != "" {
		return inspection, nil
	}
	state, err := InspectLocal(beadsDir, "")
	if err != nil {
		return EffectiveConfigInspection{}, err
	}
	inspection.Local = &state
	if state.Initialized && state.Backend == configfile.BackendDolt {
		inspection.Config.Backend = configfile.BackendDolt
	}
	return inspection, nil
}

// EffectiveConfig is the compatibility view for callers that need only the
// effective metadata copy.
func EffectiveConfig(beadsDir string, cfg *configfile.Config) (*configfile.Config, error) {
	inspection, err := InspectEffectiveConfig(beadsDir, cfg)
	if err != nil {
		return nil, err
	}
	return &inspection.Config, nil
}

package main

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/safefile"
	"github.com/steveyegge/beads/internal/storage/workspacestate"
)

var errBackendWorkspaceChangedDuringInspection = errors.New("backend workspace changed during inspection")

type backendWorkspaceLane uint8

const (
	backendWorkspaceLaneBinding backendWorkspaceLane = iota + 1
	backendWorkspaceLaneStructural
)

type backendPathFact struct {
	path           string
	exists         bool
	mode           os.FileMode
	identity       os.FileInfo
	linkCount      uint64
	linkCountKnown bool
}

type backendWorkspaceRoute struct {
	lane                         backendWorkspaceLane
	source, target, owned        backendPathFact
	bindingBackend, mappedSQLite string
	bindingSource                databaseOwnershipSource
	bindingScope                 databaseOwnershipScope
	bindingSources               []backendPathFact
}

type backendWorkspaceState struct {
	backend        string
	initialized    bool
	localInspected bool
	local          workspacestate.LocalState
}

// backendWorkspaceSnapshot contains inert facts from one read-only
// observation. Equality is an optimistic drift check only: it neither prevents
// ABA/changes after return nor authorizes provider effects. Those require the
// descriptor-bound lifetime fence tracked by bd-3u1fs.
type backendWorkspaceSnapshot struct {
	selector     string
	selectorFact backendPathFact
	route        backendWorkspaceRoute
	metadata     configfile.ReadOnlySnapshot
	state        backendWorkspaceState
}

type backendWorkspaceObserver func() (*backendWorkspaceSnapshot, error)

// inspectBackendWorkspaceSnapshot owns strict selector resolution. Automatic
// and caller-supplied hints are intentionally unavailable so ambient workspace
// state cannot broaden structural provenance.
func inspectBackendWorkspaceSnapshot(selector string) (*backendWorkspaceSnapshot, error) {
	if selector == "" {
		return nil, errors.New("database selector is empty")
	}
	absolute, err := absoluteCleanDatabasePath(selector)
	if err != nil {
		return nil, err
	}
	return stabilizeBackendWorkspaceSnapshot(func() (*backendWorkspaceSnapshot, error) {
		return observeBackendWorkspaceOnce(absolute)
	})
}

func observeBackendWorkspaceOnce(selector string) (*backendWorkspaceSnapshot, error) {
	if selector == "" {
		return nil, errors.New("database selector is empty")
	}
	selector, err := absoluteCleanDatabasePath(selector)
	if err != nil {
		return nil, err
	}
	resolution, err := resolveDatabaseOwnershipStrictResult(selector, false)
	if err != nil {
		return nil, err
	}
	if resolution.binding == nil && resolution.structural == nil {
		return nil, nil
	}
	if resolution.binding != nil && resolution.structural != nil {
		return nil, errors.New("backend workspace resolution has multiple lanes")
	}
	selected, err := validatedDatabaseSelector(selector)
	if err != nil {
		return nil, err
	}
	selectedFact, err := backendSnapshotPathFact(selected)
	if err != nil {
		return nil, err
	}
	snapshot := &backendWorkspaceSnapshot{selector: selector, selectorFact: selectedFact}

	if binding := resolution.binding; binding != nil {
		snapshot.route.lane = backendWorkspaceLaneBinding
		snapshot.route.bindingBackend, snapshot.route.bindingSource, snapshot.route.bindingScope = binding.backend, binding.source, binding.scope
		if snapshot.route.target, err = backendSnapshotPathFact(binding.beadsResolved); err != nil {
			return nil, err
		}
		if snapshot.route.owned, err = backendSnapshotPathFact(binding.ownedResolved); err != nil {
			return nil, err
		}
		for _, source := range binding.sourceResolved {
			fact, factErr := backendSnapshotPathFact(source)
			if factErr != nil {
				return nil, factErr
			}
			snapshot.route.bindingSources = append(snapshot.route.bindingSources, fact)
		}
		sort.Slice(snapshot.route.bindingSources, func(i, j int) bool {
			return snapshot.route.bindingSources[i].path < snapshot.route.bindingSources[j].path
		})
		cfg, metadata, loadErr := configfile.LoadReadOnlySnapshot(snapshot.route.target.path)
		if loadErr != nil {
			return nil, loadErr
		}
		if cfg == nil || !metadata.Present() {
			return nil, errors.New("backend workspace binding lost metadata")
		}
		inspection, inspectErr := workspacestate.InspectEffectiveConfig(snapshot.route.target.path, cfg)
		if inspectErr != nil {
			return nil, inspectErr
		}
		backend := inspection.Config.GetBackend()
		if backend != binding.backend {
			return nil, errors.New("backend workspace binding disagrees with metadata")
		}
		snapshot.metadata = metadata
		snapshot.state = backendWorkspaceState{backend: backend, initialized: true}
		if inspection.Local != nil {
			snapshot.state.localInspected, snapshot.state.local = true, *inspection.Local
		}
		return snapshot, nil
	}

	structural := resolution.structural
	snapshot.route.lane = backendWorkspaceLaneStructural
	if snapshot.route.source, err = backendSnapshotPathFact(structural.sourceResolved); err != nil {
		return nil, err
	}
	if snapshot.route.target, err = backendSnapshotPathFact(structural.resolved); err != nil {
		return nil, err
	}
	if !resolvedDatabasePathEqualOrDescendant(selected, structural.sourceResolved) {
		return nil, errors.New("database selector is outside structural workspace")
	}
	cfg, metadata, err := configfile.LoadReadOnlySnapshot(snapshot.route.target.path)
	if err != nil {
		return nil, err
	}
	if cfg != nil || metadata.Present() {
		return nil, errors.New("structural backend workspace acquired metadata")
	}
	snapshot.route.mappedSQLite, err = backendSnapshotMappedSQLite(selected, structural.sourceResolved, structural.resolved)
	if err != nil {
		return nil, err
	}
	local, err := workspacestate.InspectLocal(snapshot.route.target.path, snapshot.route.mappedSQLite)
	if err != nil {
		return nil, err
	}
	snapshot.metadata = metadata
	snapshot.state = backendWorkspaceState{backend: local.Backend, initialized: local.Initialized, localInspected: true, local: local}
	return snapshot, nil
}

func backendSnapshotPathFact(resolved *resolvedDatabasePath) (backendPathFact, error) {
	if resolved == nil || resolved.observed == nil || resolved.observed.Info == nil || resolved.path == "" {
		return backendPathFact{}, errors.New("backend workspace path observation is incomplete")
	}
	return backendPathFact{path: resolved.path, exists: resolved.exists, mode: resolved.observed.Info.Mode(), identity: resolved.observed.Info,
		linkCount: resolved.observed.LinkCount, linkCountKnown: resolved.observed.LinkCountKnown}, nil
}

func backendSnapshotMappedSQLite(selector, source, target *resolvedDatabasePath) (string, error) {
	if selector.exists && selector.observed.Info.IsDir() {
		return "", nil
	}
	if !resolvedDatabasePathEqualOrDescendant(selector, source) {
		return "", errors.New("structural SQLite selector escapes its source")
	}
	relative, err := filepath.Rel(source.path, selector.path)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("structural SQLite selector mapping is invalid")
	}
	mappedPath := filepath.Clean(filepath.Join(target.path, relative))
	if err := safefile.ValidateMetadataPath(mappedPath); err != nil {
		return "", databasePathOperationError("validate mapped structural SQLite path", mappedPath, err)
	}
	current := mappedPath
	var missing []string
	for {
		named, statErr := os.Lstat(current)
		if statErr == nil {
			if len(missing) == 0 && named.Mode()&os.ModeSymlink != 0 {
				return "", errors.New("mapped structural SQLite path is a symlink")
			}
			ancestor, resolveErr := resolveCanonicalDatabasePath(current)
			if resolveErr != nil {
				return "", resolveErr
			}
			if len(missing) > 0 && !ancestor.observed.Info.IsDir() {
				return "", errors.New("mapped structural SQLite ancestor is not a directory")
			}
			if !resolvedDatabasePathEqualOrDescendant(ancestor, target) {
				return "", errors.New("structural SQLite selector escapes its target")
			}
			mappedPath = ancestor.path
			for index := len(missing) - 1; index >= 0; index-- {
				mappedPath = filepath.Join(mappedPath, missing[index])
			}
			return mappedPath, nil
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return "", databasePathOperationError("inspect mapped structural SQLite path", current, statErr)
		}
		missing = append(missing, filepath.Base(current))
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("mapped structural SQLite path has no existing ancestor")
		}
		current = parent
	}
}

// stabilizeBackendWorkspaceSnapshot accepts only two equivalent observations
// (including two verified non-resolutions) and returns the second observation.
func stabilizeBackendWorkspaceSnapshot(observe backendWorkspaceObserver) (*backendWorkspaceSnapshot, error) {
	if observe == nil {
		return nil, errors.New("backend workspace observer is missing")
	}
	first, err := observe()
	if err != nil {
		return nil, err
	}
	first = cloneBackendWorkspaceSnapshot(first)
	second, err := observe()
	if err != nil {
		return nil, err
	}
	if !equalBackendWorkspaceSnapshots(first, second) {
		return nil, errBackendWorkspaceChangedDuringInspection
	}
	return second, nil
}

func equalBackendWorkspaceSnapshots(left, right *backendWorkspaceSnapshot) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if !validBackendWorkspaceState(left.state) || !validBackendWorkspaceState(right.state) ||
		left.selector != right.selector || !equalBackendPathFact(left.selectorFact, right.selectorFact) ||
		!left.metadata.Equal(right.metadata) || left.state.backend != right.state.backend || left.state.initialized != right.state.initialized ||
		left.state.localInspected != right.state.localInspected || left.state.local != right.state.local {
		return false
	}
	return equalBackendWorkspaceRoutes(left.route, right.route)
}

func equalBackendWorkspaceRoutes(left, right backendWorkspaceRoute) bool {
	if left.lane != right.lane || left.bindingBackend != right.bindingBackend || left.bindingSource != right.bindingSource ||
		left.bindingScope != right.bindingScope || left.mappedSQLite != right.mappedSQLite ||
		!equalBackendPathFact(left.source, right.source) || !equalBackendPathFact(left.target, right.target) ||
		!equalBackendPathFact(left.owned, right.owned) || len(left.bindingSources) != len(right.bindingSources) {
		return false
	}
	for i := range left.bindingSources {
		if !equalBackendPathFact(left.bindingSources[i], right.bindingSources[i]) {
			return false
		}
	}
	return true
}

func equalBackendPathFact(left, right backendPathFact) bool {
	if left.path == "" || right.path == "" {
		return zeroBackendPathFact(left) && zeroBackendPathFact(right)
	}
	return left.path == right.path && left.exists == right.exists && left.mode == right.mode &&
		left.linkCount == right.linkCount && left.linkCountKnown == right.linkCountKnown &&
		left.identity != nil && right.identity != nil && os.SameFile(left.identity, right.identity)
}

func cloneBackendWorkspaceSnapshot(source *backendWorkspaceSnapshot) *backendWorkspaceSnapshot {
	if source == nil {
		return nil
	}
	clone := *source
	clone.route.bindingSources = append([]backendPathFact(nil), source.route.bindingSources...)
	return &clone
}

func validBackendWorkspaceState(state backendWorkspaceState) bool {
	return state.localInspected || state.local == (workspacestate.LocalState{})
}

func zeroBackendPathFact(fact backendPathFact) bool {
	return fact.path == "" && !fact.exists && fact.mode == 0 && fact.identity == nil && fact.linkCount == 0 && !fact.linkCountKnown
}

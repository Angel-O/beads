package main

import (
	"errors"
	"os"

	"github.com/steveyegge/beads/internal/configfile"
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

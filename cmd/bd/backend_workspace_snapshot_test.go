package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/workspacestate"
)

func TestStabilizeBackendWorkspaceSnapshot(t *testing.T) {
	stable := newBackendStabilizerSnapshot(t)

	t.Run("returns second equal observation", func(t *testing.T) {
		calls := 0
		second := cloneBackendStabilizerSnapshot(stable)
		got, err := stabilizeBackendWorkspaceSnapshot(func() (*backendWorkspaceSnapshot, error) {
			calls++
			if calls == 1 {
				return stable, nil
			}
			return second, nil
		})
		if err != nil || got != second || calls != 2 {
			t.Fatalf("snapshot=%#v err=%v calls=%d, want second equal observation", got, err, calls)
		}
	})

	t.Run("two non-resolutions return nil", func(t *testing.T) {
		calls := 0
		got, err := stabilizeBackendWorkspaceSnapshot(func() (*backendWorkspaceSnapshot, error) {
			calls++
			return nil, nil
		})
		if err != nil || got != nil || calls != 2 {
			t.Fatalf("snapshot=%#v err=%v calls=%d, want nil after two calls", got, err, calls)
		}
	})

	t.Run("freezes a reused observer object", func(t *testing.T) {
		calls := 0
		shared := cloneBackendStabilizerSnapshot(stable)
		got, err := stabilizeBackendWorkspaceSnapshot(func() (*backendWorkspaceSnapshot, error) {
			calls++
			if calls == 2 {
				shared.state.backend = configfile.BackendDolt
				shared.state.local.Backend = configfile.BackendDolt
				shared.route.bindingSources[0].exists = false
			}
			return shared, nil
		})
		if got != nil || !errors.Is(err, errBackendWorkspaceChangedDuringInspection) || calls != 2 {
			t.Fatalf("snapshot=%#v err=%v calls=%d, want aliased-observation drift", got, err, calls)
		}
	})

	t.Run("nil observer", func(t *testing.T) {
		if got, err := stabilizeBackendWorkspaceSnapshot(nil); got != nil || err == nil {
			t.Fatalf("snapshot=%#v err=%v, want error", got, err)
		}
	})

	for _, test := range []struct {
		name      string
		first     *backendWorkspaceSnapshot
		firstErr  error
		second    *backendWorkspaceSnapshot
		secondErr error
		wantCalls int
		wantDrift bool
	}{
		{name: "first error", firstErr: errors.New("first failed"), wantCalls: 1},
		{name: "second error", first: stable, secondErr: errors.New("second failed"), wantCalls: 2},
		{name: "workspace appeared", second: stable, wantCalls: 2, wantDrift: true},
		{name: "workspace disappeared", first: stable, wantCalls: 2, wantDrift: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			got, err := stabilizeBackendWorkspaceSnapshot(func() (*backendWorkspaceSnapshot, error) {
				calls++
				if calls == 1 {
					return test.first, test.firstErr
				}
				return test.second, test.secondErr
			})
			if got != nil || calls != test.wantCalls || test.wantDrift != errors.Is(err, errBackendWorkspaceChangedDuringInspection) || err == nil {
				t.Fatalf("snapshot=%#v err=%v calls=%d, want calls=%d drift=%t", got, err, calls, test.wantCalls, test.wantDrift)
			}
		})
	}
}

func TestEqualBackendWorkspaceSnapshotsComparesAllFacts(t *testing.T) {
	base := newBackendStabilizerSnapshot(t)
	otherMetadata := backendStabilizerMetadata(t, filepath.Join(t.TempDir(), ".beads"))

	for _, test := range []struct {
		name   string
		mutate func(*backendWorkspaceSnapshot)
	}{
		{name: "selector", mutate: func(got *backendWorkspaceSnapshot) { got.selector += "-other" }},
		{name: "selector fact", mutate: func(got *backendWorkspaceSnapshot) { got.selectorFact.mode ^= 0o100 }},
		{name: "lane", mutate: func(got *backendWorkspaceSnapshot) { got.route.lane = backendWorkspaceLaneStructural }},
		{name: "source", mutate: func(got *backendWorkspaceSnapshot) { got.route.source.path += "-other" }},
		{name: "target", mutate: func(got *backendWorkspaceSnapshot) { got.route.target.path += "-other" }},
		{name: "owned", mutate: func(got *backendWorkspaceSnapshot) { got.route.owned.path += "-other" }},
		{name: "binding backend", mutate: func(got *backendWorkspaceSnapshot) { got.route.bindingBackend = configfile.BackendDolt }},
		{name: "binding source", mutate: func(got *backendWorkspaceSnapshot) { got.route.bindingSource = databaseOwnershipExplicitEnvironment }},
		{name: "binding scope", mutate: func(got *backendWorkspaceSnapshot) { got.route.bindingScope = databaseOwnershipScopeDescendant }},
		{name: "mapped SQLite", mutate: func(got *backendWorkspaceSnapshot) { got.route.mappedSQLite = "other.db" }},
		{name: "binding source count", mutate: func(got *backendWorkspaceSnapshot) { got.route.bindingSources = nil }},
		{name: "binding source fact", mutate: func(got *backendWorkspaceSnapshot) { got.route.bindingSources[0].exists = false }},
		{name: "metadata", mutate: func(got *backendWorkspaceSnapshot) { got.metadata = otherMetadata }},
		{name: "backend", mutate: func(got *backendWorkspaceSnapshot) { got.state.backend = configfile.BackendDolt }},
		{name: "initialized", mutate: func(got *backendWorkspaceSnapshot) { got.state.initialized = false }},
		{name: "local inspection presence", mutate: func(got *backendWorkspaceSnapshot) { got.state.localInspected = false }},
		{name: "local state", mutate: func(got *backendWorkspaceSnapshot) { got.state.local.Backend = configfile.BackendDolt }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneBackendStabilizerSnapshot(base)
			test.mutate(changed)
			if equalBackendWorkspaceSnapshots(base, changed) {
				t.Fatalf("snapshots compare equal after %s changed", test.name)
			}
		})
	}
}

func TestEqualBackendPathFactRequiresObjectIdentity(t *testing.T) {
	firstPath, secondPath := filepath.Join(t.TempDir(), "first"), filepath.Join(t.TempDir(), "second")
	if err := os.WriteFile(firstPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	first, _ := os.Stat(firstPath)
	second, _ := os.Stat(secondPath)
	left := backendPathFact{path: "same", exists: true, mode: first.Mode(), identity: first, linkCountKnown: true, linkCount: 1}
	right := left
	right.identity = second
	if equalBackendPathFact(left, right) {
		t.Fatal("different objects at the same recorded path compared equal")
	}
	if !equalBackendPathFact(backendPathFact{}, backendPathFact{}) {
		t.Fatal("two irrelevant empty path facts must compare equal")
	}
	malformed := backendPathFact{exists: true, mode: 0o600, identity: first, linkCountKnown: true, linkCount: 1}
	if equalBackendPathFact(backendPathFact{}, malformed) || equalBackendPathFact(malformed, malformed) {
		t.Fatal("empty-path facts with nonzero state must never compare equal")
	}
}

func newBackendStabilizerSnapshot(t *testing.T) *backendWorkspaceSnapshot {
	t.Helper()
	root := t.TempDir()
	fact := func(name string) backendPathFact {
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return backendPathFact{path: path, exists: true, mode: info.Mode(), identity: info, linkCountKnown: true, linkCount: 1}
	}
	local := workspacestate.LocalState{}
	source, target, owned := fact("source"), fact("target"), fact("owned")
	return &backendWorkspaceSnapshot{
		selector:     source.path,
		selectorFact: source,
		route: backendWorkspaceRoute{
			lane:           backendWorkspaceLaneBinding,
			source:         source,
			target:         target,
			owned:          owned,
			bindingBackend: configfile.BackendSQLite,
			bindingSource:  databaseOwnershipPersisted,
			bindingScope:   databaseOwnershipScopeExact,
			bindingSources: []backendPathFact{source},
		},
		metadata: backendStabilizerMetadata(t, filepath.Join(root, ".beads")),
		state: backendWorkspaceState{
			backend:        configfile.BackendSQLite,
			initialized:    true,
			localInspected: true,
			local:          local,
		},
	}
}

func cloneBackendStabilizerSnapshot(source *backendWorkspaceSnapshot) *backendWorkspaceSnapshot {
	clone := *source
	clone.route.bindingSources = append([]backendPathFact(nil), source.route.bindingSources...)
	return &clone
}

func backendStabilizerMetadata(t *testing.T, beadsDir string) configfile.ReadOnlySnapshot {
	t.Helper()
	if err := os.MkdirAll(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := configfile.Config{Backend: configfile.BackendSQLite, SQLitePath: "beads.db"}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	_, snapshot, err := configfile.LoadReadOnlySnapshot(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

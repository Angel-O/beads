package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/workspacestate"
)

func TestNormalizeInitBackend(t *testing.T) {
	for _, test := range []struct {
		raw, want string
		wantError bool
	}{
		{raw: "", want: configfile.BackendDolt},
		{raw: configfile.BackendDolt, want: configfile.BackendDolt},
		{raw: configfile.BackendPostgres, want: configfile.BackendPostgres},
		{raw: configfile.BackendMySQL, want: configfile.BackendMySQL},
		{raw: configfile.BackendSQLite, want: configfile.BackendSQLite},
		{raw: "DOLT", wantError: true},
		{raw: " sqlite", wantError: true},
		{raw: "mongodb", wantError: true},
	} {
		got, err := normalizeInitBackend(test.raw)
		if got != test.want || (err != nil) != test.wantError {
			t.Fatalf("normalizeInitBackend(%q) = %q, %v; want %q, error=%t", test.raw, got, err, test.want, test.wantError)
		}
		if test.wantError && !strings.Contains(err.Error(), "unknown backend") {
			t.Fatalf("normalizeInitBackend(%q) error = %q, want existing unknown-backend contract", test.raw, err)
		}
	}
}

func TestAdmitInitBackendProviderMatrix(t *testing.T) {
	providers := []string{
		configfile.BackendDolt,
		configfile.BackendPostgres,
		configfile.BackendMySQL,
		configfile.BackendSQLite,
	}
	for _, current := range providers {
		for _, requested := range providers {
			t.Run(current+"_to_"+requested, func(t *testing.T) {
				snapshot := &backendWorkspaceSnapshot{
					route: backendWorkspaceRoute{lane: backendWorkspaceLaneBinding, bindingBackend: current},
					state: backendWorkspaceState{backend: current, initialized: true},
				}
				got, err := admitInitBackend(requested, snapshot)
				if current == requested {
					if err != nil || got != requested {
						t.Fatalf("admission = %q, %v; want same-provider allow", got, err)
					}
					return
				}
				if got != "" || !errors.Is(err, errBackendChangeRequiresMigration) {
					t.Fatalf("admission = %q, %v; want migration refusal", got, err)
				}
				var typed *backendChangeRequiresMigrationError
				if !errors.As(err, &typed) || typed.current != current || typed.requested != requested ||
					err.Error() != backendChangeRequiresMigrationCode+": "+backendChangeRequiresMigrationCopy {
					t.Fatalf("typed refusal = %#v, %q", typed, err)
				}
			})
		}
	}
}

func TestAdmitInitBackendAllowsFreshWorkspace(t *testing.T) {
	for _, snapshot := range []*backendWorkspaceSnapshot{
		nil,
		{
			route: backendWorkspaceRoute{lane: backendWorkspaceLaneStructural},
			state: backendWorkspaceState{localInspected: true},
		},
	} {
		for _, requested := range []string{"", configfile.BackendSQLite} {
			got, err := admitInitBackend(requested, snapshot)
			want := requested
			if want == "" {
				want = configfile.BackendDolt
			}
			if err != nil || got != want {
				t.Fatalf("admission = %q, %v; want %q", got, err, want)
			}
		}
	}
}

func TestAdmitInitBackendAllowsVerifiedInitializedLocalStates(t *testing.T) {
	for _, test := range []struct {
		name    string
		current string
		state   backendWorkspaceSnapshot
	}{
		{
			name:    "binding SQLite with verified absent evidence",
			current: configfile.BackendSQLite,
			state: backendWorkspaceSnapshot{
				route: backendWorkspaceRoute{lane: backendWorkspaceLaneBinding, bindingBackend: configfile.BackendSQLite},
				state: backendWorkspaceState{backend: configfile.BackendSQLite, initialized: true, localInspected: true},
			},
		},
		{
			name:    "binding SQLite with SQLite evidence",
			current: configfile.BackendSQLite,
			state: backendWorkspaceSnapshot{
				route: backendWorkspaceRoute{lane: backendWorkspaceLaneBinding, bindingBackend: configfile.BackendSQLite},
				state: backendWorkspaceState{backend: configfile.BackendSQLite, initialized: true, localInspected: true, local: workspacestate.LocalState{Backend: configfile.BackendSQLite, Initialized: true}},
			},
		},
		{
			name:    "binding Dolt with Dolt evidence",
			current: configfile.BackendDolt,
			state: backendWorkspaceSnapshot{
				route: backendWorkspaceRoute{lane: backendWorkspaceLaneBinding, bindingBackend: configfile.BackendDolt},
				state: backendWorkspaceState{backend: configfile.BackendDolt, initialized: true, localInspected: true, local: workspacestate.LocalState{Backend: configfile.BackendDolt, Initialized: true}},
			},
		},
		{
			name:    "structural SQLite evidence",
			current: configfile.BackendSQLite,
			state: backendWorkspaceSnapshot{
				route: backendWorkspaceRoute{lane: backendWorkspaceLaneStructural},
				state: backendWorkspaceState{backend: configfile.BackendSQLite, initialized: true, localInspected: true, local: workspacestate.LocalState{Backend: configfile.BackendSQLite, Initialized: true}},
			},
		},
		{
			name:    "structural Dolt evidence",
			current: configfile.BackendDolt,
			state: backendWorkspaceSnapshot{
				route: backendWorkspaceRoute{lane: backendWorkspaceLaneStructural},
				state: backendWorkspaceState{backend: configfile.BackendDolt, initialized: true, localInspected: true, local: workspacestate.LocalState{Backend: configfile.BackendDolt, Initialized: true}},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got, err := admitInitBackend(test.current, &test.state); err != nil || got != test.current {
				t.Fatalf("same-provider admission = %q, %v; want %q", got, err, test.current)
			}
			if got, err := admitInitBackend(configfile.BackendPostgres, &test.state); got != "" || !errors.Is(err, errBackendChangeRequiresMigration) {
				t.Fatalf("cross-provider admission = %q, %v; want migration refusal", got, err)
			}
		})
	}
}

func TestAdmitInitBackendValidatesRequestBeforeSnapshot(t *testing.T) {
	got, err := admitInitBackend("mongodb", nil)
	if got != "" || err == nil || !strings.Contains(err.Error(), "unknown backend") {
		t.Fatalf("admission = %q, %v; want unknown-backend error", got, err)
	}
}

func TestAdmitInitBackendRejectsImpossibleSnapshot(t *testing.T) {
	for _, state := range []backendWorkspaceSnapshot{
		{},
		{route: backendWorkspaceRoute{lane: backendWorkspaceLaneBinding}, state: backendWorkspaceState{}},
		{route: backendWorkspaceRoute{lane: backendWorkspaceLaneBinding}, state: backendWorkspaceState{backend: "mongodb", initialized: true}},
		{route: backendWorkspaceRoute{lane: backendWorkspaceLaneBinding}, state: backendWorkspaceState{backend: configfile.BackendDolt, initialized: true}},
		{route: backendWorkspaceRoute{lane: backendWorkspaceLaneBinding, bindingBackend: configfile.BackendSQLite}, state: backendWorkspaceState{backend: configfile.BackendDolt, initialized: true}},
		{route: backendWorkspaceRoute{lane: backendWorkspaceLaneBinding, bindingBackend: configfile.BackendDolt}, state: backendWorkspaceState{backend: configfile.BackendDolt, initialized: true, localInspected: true, local: workspacestate.LocalState{Backend: configfile.BackendMySQL, Initialized: true}}},
		{route: backendWorkspaceRoute{lane: backendWorkspaceLaneStructural}, state: backendWorkspaceState{backend: configfile.BackendSQLite}},
		{route: backendWorkspaceRoute{lane: backendWorkspaceLaneStructural}, state: backendWorkspaceState{localInspected: true, local: workspacestate.LocalState{Backend: configfile.BackendSQLite}}},
		{route: backendWorkspaceRoute{lane: backendWorkspaceLaneStructural}, state: backendWorkspaceState{backend: configfile.BackendDolt, initialized: true, localInspected: true}},
	} {
		if got, err := admitInitBackend(configfile.BackendDolt, &state); got != "" || err == nil || errors.Is(err, errBackendChangeRequiresMigration) {
			t.Fatalf("admission = %q, %v; want invalid-state error", got, err)
		}
	}
}

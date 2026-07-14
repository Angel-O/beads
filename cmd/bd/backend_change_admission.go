package main

import (
	"errors"
	"fmt"

	"github.com/steveyegge/beads/internal/configfile"
)

const backendChangeRequiresMigrationCode = "backend_change_requires_migration"
const backendChangeRequiresMigrationCopy = "Changing backend providers is not supported by this release. Upgrade to a release that includes backend migration; your current backend is unchanged."

var errBackendChangeRequiresMigration = errors.New(backendChangeRequiresMigrationCode)

type backendChangeRequiresMigrationError struct {
	current   string
	requested string
}

func (*backendChangeRequiresMigrationError) Error() string {
	return backendChangeRequiresMigrationCode + ": " + backendChangeRequiresMigrationCopy
}

func (*backendChangeRequiresMigrationError) Unwrap() error { return errBackendChangeRequiresMigration }

func normalizeInitBackend(raw string) (string, error) {
	if raw == "" {
		return configfile.BackendDolt, nil
	}
	if isInitBackend(raw) {
		return raw, nil
	}
	return "", fmt.Errorf("unknown backend %q: supported backends are \"dolt\" (default), \"postgres\", \"mysql\", and \"sqlite\"", raw)
}

func admitInitBackend(requested string, snapshot *backendWorkspaceSnapshot) (string, error) {
	requested, err := normalizeInitBackend(requested)
	if err != nil {
		return "", err
	}
	if snapshot == nil {
		return requested, nil
	}
	state := snapshot.state
	valid := validBackendWorkspaceState(state) && (!state.localInspected ||
		!state.local.Initialized && state.local.Backend == "" ||
		state.local.Initialized && (state.local.Backend == configfile.BackendDolt || state.local.Backend == configfile.BackendSQLite))
	switch snapshot.route.lane {
	case backendWorkspaceLaneBinding:
		valid = valid && state.initialized && isInitBackend(state.backend) && snapshot.route.bindingBackend == state.backend
		valid = valid && (!state.localInspected || state.local.Backend == configfile.BackendDolt && state.backend == configfile.BackendDolt ||
			state.local.Backend != configfile.BackendDolt && state.backend == configfile.BackendSQLite)
	case backendWorkspaceLaneStructural:
		valid = valid && state.localInspected && state.backend == state.local.Backend && state.initialized == state.local.Initialized &&
			(!state.initialized && state.backend == "" || state.initialized && (state.backend == configfile.BackendDolt || state.backend == configfile.BackendSQLite))
	default:
		valid = false
	}
	if !valid {
		return "", errors.New("backend workspace snapshot has invalid provider state")
	}
	if state.initialized && state.backend != requested {
		return "", &backendChangeRequiresMigrationError{current: state.backend, requested: requested}
	}
	return requested, nil
}

func isInitBackend(backend string) bool {
	return backend == configfile.BackendDolt || backend == configfile.BackendPostgres ||
		backend == configfile.BackendMySQL || backend == configfile.BackendSQLite
}

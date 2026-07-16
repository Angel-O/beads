//go:build !cgo

package embeddeddolt

import (
	"context"
	"errors"

	"github.com/steveyegge/beads/internal/storage"
)

// EmbeddedDoltStore is a stub for builds without CGO.
type EmbeddedDoltStore struct {
	dataDir  string
	database string
	branch   string
}

type MigrationSourceBinding struct{}

var errNoCGO = errors.New("embeddeddolt: requires CGO (build with CGO_ENABLED=1)")

var (
	ErrBackendMigrationBusy   = errors.New("embedded workspace is active in another process")
	ErrBackendProviderChanged = errors.New("workspace backend changed while waiting for embedded Dolt")
)

// Open returns an error when CGO is not enabled.
func Open(_ context.Context, _, _, _ string) (*EmbeddedDoltStore, error) {
	return nil, errNoCGO
}

// OpenReadOnly returns an error when CGO is not enabled.
func OpenReadOnly(_ context.Context, _, _, _ string) (*EmbeddedDoltStore, error) {
	return nil, errNoCGO
}

func BindMigrationSource(string, string) (*MigrationSourceBinding, error) { return nil, errNoCGO }
func (*MigrationSourceBinding) Verify() error                             { return errNoCGO }
func (*MigrationSourceBinding) Witness() (string, error)                  { return "", errNoCGO }
func (*MigrationSourceBinding) VerifyWitness(string) error                { return errNoCGO }
func (*MigrationSourceBinding) OpenReadOnly(context.Context, string, string) (storage.DoltStorage, error) {
	return nil, errNoCGO
}
func (*MigrationSourceBinding) Close() error { return nil }

// OpenForReadOnlyCommand returns an error when CGO is not enabled.
func OpenForReadOnlyCommand(_ context.Context, _, _, _ string) (*EmbeddedDoltStore, error) {
	return nil, errNoCGO
}

// OpenForWorkingSetReconcile returns an error when CGO is not enabled.
func OpenForWorkingSetReconcile(_ context.Context, _, _, _ string) (*EmbeddedDoltStore, error) {
	return nil, errNoCGO
}

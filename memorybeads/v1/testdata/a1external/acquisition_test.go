package externalbackend

import (
	"errors"
	"testing"

	"github.com/steveyegge/beads/backend"
	memorybeadsv1 "github.com/steveyegge/beads/memorybeads/v1"
)

// TestUnsupportedBackendRemainsAValidCaller proves a concrete external
// backend that implements the existing public contract does not need to opt in
// to Memory Beads. Acquisition fails explicitly rather than changing the
// backend method set or succeeding with a partial module.
func TestUnsupportedBackendRemainsAValidCaller(t *testing.T) {
	var store backend.DoltStorage = &Store{}
	_, err := memorybeadsv1.Acquire(store)
	var unsupported *memorybeadsv1.ErrUnsupported
	if !errors.As(err, &unsupported) {
		t.Fatalf("Acquire(*Store) error = %v, want *ErrUnsupported", err)
	}
	if unsupported.Op != "MemoryModuleV1" {
		t.Fatalf("unsupported operation = %q, want MemoryModuleV1", unsupported.Op)
	}
}

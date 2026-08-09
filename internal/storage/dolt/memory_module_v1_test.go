package dolt

import (
	"errors"
	"testing"

	memorybeadsv1 "github.com/steveyegge/beads/memorybeads/v1"
)

func TestMemoryModuleV1AcquiresFromServerBackedStore(t *testing.T) {
	module, err := memorybeadsv1.Acquire(&DoltStore{})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := module.Descriptor().InterfaceVersion; got != memorybeadsv1.InterfaceVersion {
		t.Fatalf("descriptor version = %q, want %q", got, memorybeadsv1.InterfaceVersion)
	}
}

func TestMemoryModuleV1RejectsNilServerBackedStore(t *testing.T) {
	_, err := memorybeadsv1.Acquire((*DoltStore)(nil))
	var unsupported *memorybeadsv1.ErrUnsupported
	if !errors.As(err, &unsupported) {
		t.Fatalf("Acquire(nil store) error = %v, want *ErrUnsupported", err)
	}
}

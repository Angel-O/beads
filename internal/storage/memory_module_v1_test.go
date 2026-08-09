package storage

import (
	"testing"

	memorybeadsv1 "github.com/steveyegge/beads/memorybeads/v1"
)

type descriptorMemoryModuleV1 struct{}

func (descriptorMemoryModuleV1) Descriptor() memorybeadsv1.Descriptor {
	return memorybeadsv1.Descriptor{InterfaceVersion: memorybeadsv1.InterfaceVersion}
}

type memoryModuleV1Store struct {
	DoltStorage
}

func (*memoryModuleV1Store) MemoryModuleV1() (memorybeadsv1.Module, error) {
	return descriptorMemoryModuleV1{}, nil
}

func TestHookFiringStorePreservesMemoryModuleV1(t *testing.T) {
	wrapped := NewHookFiringStore(&memoryModuleV1Store{}, nil)
	module, err := memorybeadsv1.Acquire(wrapped)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := module.Descriptor().InterfaceVersion; got != memorybeadsv1.InterfaceVersion {
		t.Fatalf("descriptor version = %q, want %q", got, memorybeadsv1.InterfaceVersion)
	}
}

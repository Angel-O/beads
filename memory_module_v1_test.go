package beads

import (
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	memorybeadsv1 "github.com/steveyegge/beads/memorybeads/v1"
)

type gatedMemoryModuleV1 struct{}

func (gatedMemoryModuleV1) Descriptor() memorybeadsv1.Descriptor {
	return memorybeadsv1.Descriptor{InterfaceVersion: memorybeadsv1.InterfaceVersion}
}

type gatedMemoryModuleV1Store struct {
	storage.DoltStorage
}

func (*gatedMemoryModuleV1Store) MemoryModuleV1() (memorybeadsv1.Module, error) {
	return gatedMemoryModuleV1{}, nil
}

func TestGatedStoragePreservesMemoryModuleV1(t *testing.T) {
	wrapper := &gatedStorage{DoltStorage: &gatedMemoryModuleV1Store{}}
	module, err := memorybeadsv1.Acquire(wrapper)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := module.Descriptor().InterfaceVersion; got != memorybeadsv1.InterfaceVersion {
		t.Fatalf("descriptor version = %q, want %q", got, memorybeadsv1.InterfaceVersion)
	}
}

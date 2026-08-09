package dolt

import (
	"github.com/steveyegge/beads/internal/storage"
	memorybeadsv1 "github.com/steveyegge/beads/memorybeads/v1"
)

// MemoryModuleV1 exposes the optional versioned Memory Beads seam without
// widening storage.Storage or storage.DoltStorage.
func (s *DoltStore) MemoryModuleV1() (memorybeadsv1.Module, error) {
	if s == nil {
		return nil, &storage.ErrUnsupported{Op: "MemoryModuleV1", Backend: "nil"}
	}
	return &memoryModuleV1{store: s}, nil
}

type memoryModuleV1 struct {
	store *DoltStore
}

func (m *memoryModuleV1) Descriptor() memorybeadsv1.Descriptor {
	return memorybeadsv1.Descriptor{InterfaceVersion: memorybeadsv1.InterfaceVersion}
}

var _ memorybeadsv1.Source = (*DoltStore)(nil)
var _ memorybeadsv1.Module = (*memoryModuleV1)(nil)

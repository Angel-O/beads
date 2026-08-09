//go:build cgo

package embeddeddolt

import (
	"github.com/steveyegge/beads/internal/storage"
	memorybeadsv1 "github.com/steveyegge/beads/memorybeads/v1"
)

// MemoryModuleV1 exposes the optional versioned Memory Beads seam without
// widening storage.Storage or storage.DoltStorage.
func (s *EmbeddedDoltStore) MemoryModuleV1() (memorybeadsv1.Module, error) {
	if s == nil {
		return nil, &storage.ErrUnsupported{Op: "MemoryModuleV1", Backend: "nil"}
	}
	return &memoryModuleV1{store: s}, nil
}

type memoryModuleV1 struct {
	store *EmbeddedDoltStore
}

func (m *memoryModuleV1) Descriptor() memorybeadsv1.Descriptor {
	return memorybeadsv1.Descriptor{InterfaceVersion: memorybeadsv1.InterfaceVersion}
}

var _ memorybeadsv1.Source = (*EmbeddedDoltStore)(nil)
var _ memorybeadsv1.Module = (*memoryModuleV1)(nil)

package uow

import (
	"fmt"

	memorybeadsv1 "github.com/steveyegge/beads/memorybeads/v1"
)

// MemoryModuleV1 exposes the optional versioned Memory Beads seam on the
// proxied provider without widening UnitOfWorkProvider.
func (p *doltSQLProvider) MemoryModuleV1() (memorybeadsv1.Module, error) {
	return NewMemoryModuleV1(p)
}

// MemoryModuleV1 rebuilds the descriptor module on the notifying wrapper so a
// provider decorator does not hide the optional capability.
func (p *notifyingProvider) MemoryModuleV1() (memorybeadsv1.Module, error) {
	return NewMemoryModuleV1(p)
}

// NewMemoryModuleV1 constructs the descriptor-only spike module over provider.
// It is exported within this internal package so provider decorators in sibling
// packages can bind the module to themselves instead of bypassing their layer.
func NewMemoryModuleV1(provider UnitOfWorkProvider) (memorybeadsv1.Module, error) {
	if isNilUnitOfWorkProvider(provider) {
		return nil, fmt.Errorf("new memory module v1: unit-of-work provider must not be nil")
	}
	return &memoryModuleV1{provider: provider}, nil
}

type memoryModuleV1 struct {
	provider UnitOfWorkProvider
}

func (m *memoryModuleV1) Descriptor() memorybeadsv1.Descriptor {
	return memorybeadsv1.Descriptor{InterfaceVersion: memorybeadsv1.InterfaceVersion}
}

var _ memorybeadsv1.Source = (*doltSQLProvider)(nil)
var _ memorybeadsv1.Source = (*notifyingProvider)(nil)
var _ memorybeadsv1.Module = (*memoryModuleV1)(nil)

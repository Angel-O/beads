package uow

import (
	"reflect"
	"testing"

	memorybeadsv1 "github.com/steveyegge/beads/memorybeads/v1"
)

func TestMemoryModuleV1AcquiresFromProxiedProvider(t *testing.T) {
	for _, provider := range []struct {
		name   string
		source any
	}{
		{name: "raw", source: &doltSQLProvider{}},
		{name: "notifying wrapper", source: &notifyingProvider{inner: &doltSQLProvider{}}},
	} {
		t.Run(provider.name, func(t *testing.T) {
			module, err := memorybeadsv1.Acquire(provider.source)
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			if got := module.Descriptor().InterfaceVersion; got != memorybeadsv1.InterfaceVersion {
				t.Fatalf("descriptor version = %q, want %q", got, memorybeadsv1.InterfaceVersion)
			}
		})
	}
}

func TestUnitOfWorkProviderDoesNotRequireMemoryModuleV1(t *testing.T) {
	contract := reflect.TypeOf((*UnitOfWorkProvider)(nil)).Elem()
	if _, ok := contract.MethodByName("MemoryModuleV1"); ok {
		t.Fatal("UnitOfWorkProvider unexpectedly requires MemoryModuleV1")
	}
}

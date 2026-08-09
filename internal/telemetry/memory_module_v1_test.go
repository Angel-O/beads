package telemetry

import (
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	memorybeadsv1 "github.com/steveyegge/beads/memorybeads/v1"
)

type telemetryMemoryModuleV1 struct{}

func (telemetryMemoryModuleV1) Descriptor() memorybeadsv1.Descriptor {
	return memorybeadsv1.Descriptor{InterfaceVersion: memorybeadsv1.InterfaceVersion}
}

type memoryModuleV1Store struct {
	storage.DoltStorage
}

func (*memoryModuleV1Store) MemoryModuleV1() (memorybeadsv1.Module, error) {
	return telemetryMemoryModuleV1{}, nil
}

func TestInstrumentedStoragePreservesMemoryModuleV1(t *testing.T) {
	clearTelemetryEnv(t)
	t.Setenv("BD_OTEL_STDOUT", "true")

	wrapped := WrapStorage(&memoryModuleV1Store{})
	module, err := memorybeadsv1.Acquire(wrapped)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := module.Descriptor().InterfaceVersion; got != memorybeadsv1.InterfaceVersion {
		t.Fatalf("descriptor version = %q, want %q", got, memorybeadsv1.InterfaceVersion)
	}
}

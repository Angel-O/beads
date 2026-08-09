package v1_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/steveyegge/beads/backend"
	memorybeadsv1 "github.com/steveyegge/beads/memorybeads/v1"
	"github.com/steveyegge/beads/memoryops"
)

type descriptorModule struct{}

func (descriptorModule) Descriptor() memorybeadsv1.Descriptor {
	return memorybeadsv1.Descriptor{InterfaceVersion: memorybeadsv1.InterfaceVersion}
}

type moduleSource struct {
	module memorybeadsv1.Module
	err    error
}

func (s moduleSource) MemoryModuleV1() (memorybeadsv1.Module, error) {
	return s.module, s.err
}

func TestAcquireUsesOptionalSource(t *testing.T) {
	module, err := memorybeadsv1.Acquire(moduleSource{module: descriptorModule{}})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := module.Descriptor().InterfaceVersion; got != memorybeadsv1.InterfaceVersion {
		t.Fatalf("descriptor version = %q, want %q", got, memorybeadsv1.InterfaceVersion)
	}
}

func TestAcquirePreservesProviderError(t *testing.T) {
	want := errors.New("provider failed")
	_, err := memorybeadsv1.Acquire(moduleSource{err: want})
	if !errors.Is(err, want) {
		t.Fatalf("Acquire error = %v, want %v", err, want)
	}
}

func TestAcquireRejectsNilModule(t *testing.T) {
	_, err := memorybeadsv1.Acquire(moduleSource{})
	if err == nil {
		t.Fatal("Acquire accepted a nil module")
	}
}

func TestAcquireReturnsTypedUnsupported(t *testing.T) {
	for _, source := range []any{nil, struct{}{}, (*moduleSource)(nil)} {
		_, err := memorybeadsv1.Acquire(source)
		var unsupported *memorybeadsv1.ErrUnsupported
		if !errors.As(err, &unsupported) {
			t.Errorf("Acquire(%T) error = %v, want *ErrUnsupported", source, err)
			continue
		}
		if unsupported.Op != "MemoryModuleV1" {
			t.Errorf("Acquire(%T) unsupported operation = %q", source, unsupported.Op)
		}
	}
}

// legacyMemories is a public-only compile fixture for a caller that implemented
// the pre-A1 role. Its four methods remain the complete memoryops.Memories
// contract; versioned acquisition does not add a fifth.
type legacyMemories struct{}

func (legacyMemories) Remember(context.Context, memoryops.RememberRequest) (memoryops.RememberResult, error) {
	return memoryops.RememberResult{}, nil
}

func (legacyMemories) Recall(context.Context, memoryops.RecallRequest) (memoryops.RecallResult, error) {
	return memoryops.RecallResult{}, nil
}

func (legacyMemories) Forget(context.Context, memoryops.ForgetRequest) (memoryops.ForgetResult, error) {
	return memoryops.ForgetResult{}, nil
}

func (legacyMemories) List(context.Context, memoryops.ListRequest) (memoryops.ListResult, error) {
	return memoryops.ListResult{}, nil
}

var _ memoryops.Memories = legacyMemories{}

// nonAdvertisingBackendValue is a backend-typed value whose dynamic type does
// not opt in to Source. Embedding keeps this unsupported-acquisition fixture
// compact; it is not a source-compatibility fixture for a concrete out-of-tree
// backend implementation.
type nonAdvertisingBackendValue struct {
	backend.DoltStorage
}

var _ backend.DoltStorage = (*nonAdvertisingBackendValue)(nil)

func TestBackendValueDoesNotAcquireModuleImplicitly(t *testing.T) {
	var external backend.DoltStorage = &nonAdvertisingBackendValue{}
	_, err := memorybeadsv1.Acquire(external)
	var unsupported *memorybeadsv1.ErrUnsupported
	if !errors.As(err, &unsupported) {
		t.Fatalf("Acquire(non-advertising backend value) error = %v, want *ErrUnsupported", err)
	}
}

func TestLegacyInterfacesDoNotRequireMemoryModuleV1(t *testing.T) {
	interfaces := []struct {
		name   string
		typeOf reflect.Type
	}{
		{name: "memoryops.Memories", typeOf: reflect.TypeOf((*memoryops.Memories)(nil)).Elem()},
		{name: "backend.Storage", typeOf: reflect.TypeOf((*backend.Storage)(nil)).Elem()},
		{name: "backend.DoltStorage", typeOf: reflect.TypeOf((*backend.DoltStorage)(nil)).Elem()},
	}
	for _, contract := range interfaces {
		if _, ok := contract.typeOf.MethodByName("MemoryModuleV1"); ok {
			t.Errorf("%s unexpectedly requires MemoryModuleV1", contract.name)
		}
	}
}

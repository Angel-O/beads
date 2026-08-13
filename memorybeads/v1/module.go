package v1

import (
	"fmt"
	"reflect"

	"github.com/steveyegge/beads/beadserrors"
)

// InterfaceVersion identifies this experimental descriptor contract.
const InterfaceVersion = "memorybeads/v1"

// Descriptor identifies the acquired experimental seam without exposing a
// storage engine, connection, transaction, or transport.
type Descriptor struct {
	InterfaceVersion string
}

// Module is the descriptor-only surface exercised by the A1 experiment.
// It is not an operational Memory Beads API.
type Module interface {
	Descriptor() Descriptor
}

// Source is the optional experimental capability exercised by direct stores,
// proxied providers, and decorators. It remains separate from existing storage
// and unit-of-work interfaces so the experiment does not widen them.
type Source interface {
	MemoryModuleV1() (Module, error)
}

// ErrUnsupported is returned when a selected provider does not expose Source.
// It aliases the repository-wide capability error so errors.As works through
// either package name.
type ErrUnsupported = beadserrors.ErrUnsupported

// Acquire obtains the experimental descriptor surface from source.
func Acquire(source any) (Module, error) {
	if isNil(source) {
		return nil, unsupported(source)
	}

	provider, ok := source.(Source)
	if !ok {
		return nil, unsupported(source)
	}
	module, err := provider.MemoryModuleV1()
	if err != nil {
		return nil, err
	}
	if isNil(module) {
		return nil, fmt.Errorf("%s: provider %T returned a nil module", InterfaceVersion, source)
	}
	return module, nil
}

func unsupported(source any) error {
	backend := fmt.Sprintf("%T", source)
	if isNil(source) {
		backend = "nil"
	}
	return &ErrUnsupported{Op: "MemoryModuleV1", Backend: backend}
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

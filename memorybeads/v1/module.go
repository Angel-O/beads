package v1

import (
	"fmt"
	"reflect"

	"github.com/steveyegge/beads/beadserrors"
)

// InterfaceVersion identifies the contract in this package.
const InterfaceVersion = "memorybeads/v1"

// Descriptor identifies the acquired Memory Module contract without exposing
// a storage engine, connection, transaction, or transport.
type Descriptor struct {
	InterfaceVersion string
}

// Module is the versioned Memory Beads surface acquired by new Go callers.
//
// The A1 spike intentionally gives it only a descriptor. Memory operations are
// not tunneled through the legacy memoryops.Memories role; they will be defined
// as versioned Module behavior in Phase 2.
type Module interface {
	Descriptor() Descriptor
}

// Source is the optional capability a direct store, proxied provider, or
// decorator may expose. It is intentionally separate from all existing storage
// and unit-of-work interfaces so old consumers and out-of-tree backends do not
// acquire a new required method.
type Source interface {
	MemoryModuleV1() (Module, error)
}

// ErrUnsupported is returned when a selected provider does not expose Source.
// It aliases the repository-wide capability error so errors.As works through
// either package name.
type ErrUnsupported = beadserrors.ErrUnsupported

// Acquire obtains the v1 Memory Module from source. The same function accepts
// a direct store, an embedded store, a proxied unit-of-work provider, or a
// decorator around one of those values.
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

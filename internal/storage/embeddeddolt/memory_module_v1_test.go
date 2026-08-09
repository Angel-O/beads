//go:build cgo

package embeddeddolt

import (
	"testing"

	memorybeadsv1 "github.com/steveyegge/beads/memorybeads/v1"
)

func TestMemoryModuleV1AcquiresFromEmbeddedStore(t *testing.T) {
	module, err := memorybeadsv1.Acquire(&EmbeddedDoltStore{})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := module.Descriptor().InterfaceVersion; got != memorybeadsv1.InterfaceVersion {
		t.Fatalf("descriptor version = %q, want %q", got, memorybeadsv1.InterfaceVersion)
	}
}

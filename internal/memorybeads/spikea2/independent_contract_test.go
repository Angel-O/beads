package spikea2_test

import (
	"testing"

	a2 "github.com/steveyegge/beads/internal/memorybeads/spikea2"
	"github.com/steveyegge/beads/internal/memorybeads/spikea2/conformance"
)

func TestIndependentProviderA2Contract(t *testing.T) {
	conformance.Run(t, func(t *testing.T) conformance.Fixture {
		t.Helper()
		provider := a2.NewIndependentProvider("independent-project")
		return conformance.Fixture{
			Module:      provider,
			Publication: provider,
			Branches:    provider,
			Maintain:    provider.Maintain,
		}
	})
}

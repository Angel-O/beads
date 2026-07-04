package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/conformance"
)

// TestConformance runs bd's backend-agnostic storage conformance suite against the
// SQLite backend. SQLite is embedded (pure-Go modernc), so it always runs — no env
// gate. Every failure is a SQLite gap: an allowlisted-unsupported method or a latent
// divergence.
func TestConformance(t *testing.T) {
	conformance.RunAll(t, func(t *testing.T) storage.DoltStorage {
		ctx := context.Background()
		st, err := Provision(ctx, filepath.Join(t.TempDir(), "conf.db"))
		if err != nil {
			t.Fatalf("Provision: %v", err)
		}
		if err := st.SetConfig(ctx, "issue_prefix", "test"); err != nil {
			t.Fatalf("SetConfig(issue_prefix): %v", err)
		}
		t.Cleanup(func() { _ = st.Close() })
		return st
	})
}

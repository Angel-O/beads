package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/conformance"
)

// TestConformance runs bd's backend-agnostic storage conformance suite against the
// MySQL backend. Every failure is a MySQL gap: an allowlisted-unsupported method
// (expected — completeness_test.go) or a latent divergence. Gated on
// BEADS_MYSQL_TEST_URL (a server DSN, e.g. user:pass@tcp(host:3306)/).
func TestConformance(t *testing.T) {
	url := os.Getenv("BEADS_MYSQL_TEST_URL")
	if url == "" {
		t.Skip("BEADS_MYSQL_TEST_URL not set")
	}
	base := time.Now().UnixNano()
	var seq int64

	conformance.RunAll(t, func(t *testing.T) storage.DoltStorage {
		ctx := context.Background()
		database := fmt.Sprintf("conf_%d_%d", base, atomic.AddInt64(&seq, 1))

		st, err := Provision(ctx, url, database)
		if err != nil {
			t.Fatalf("Provision: %v", err)
		}
		// The conformance factory contract wants an init'd store (issue_prefix set),
		// as `bd init` leaves it. No Commit — MySQL has no commit graph.
		if err := st.SetConfig(ctx, "issue_prefix", "test"); err != nil {
			t.Fatalf("SetConfig(issue_prefix): %v", err)
		}
		t.Cleanup(func() {
			if serverDSN, e := withDatabase(url, ""); e == nil {
				if srv, e2 := sql.Open("mysql", serverDSN); e2 == nil {
					_, _ = srv.ExecContext(context.Background(), "DROP DATABASE IF EXISTS `"+database+"`")
					_ = srv.Close()
				}
			}
			_ = st.Close()
		})
		return st
	})
}

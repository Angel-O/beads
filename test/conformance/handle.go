package conformance

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/steveyegge/beads/internal/storage/pgdialect"
)

var handleSeq int64

// freshSchemaName mints a process-unique Postgres schema name. The pid keeps names
// distinct across concurrent runs and after a crashed run; teardown drops them.
func freshSchemaName() string {
	return fmt.Sprintf("e2e_%d_%d", os.Getpid(), atomic.AddInt64(&handleSeq, 1))
}

// dropPostgresSchema tears down a workspace's schema. Best effort: a failure here
// only leaves a stray schema, which the next run's freshSchemaName avoids colliding
// with. Uses a raw (non-translating) connection since DROP SCHEMA is native DDL.
func dropPostgresSchema(ws *Workspace) {
	if ws.Handle == "" {
		return
	}
	url := os.Getenv("BEADS_PG_TEST_URL")
	if url == "" {
		return
	}
	raw, err := pgdialect.OpenRaw(url, "public")
	if err != nil {
		return
	}
	defer raw.Close()
	_, _ = raw.ExecContext(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE`, ws.Handle))
}

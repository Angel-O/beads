package db

import (
	"database/sql"

	"github.com/steveyegge/beads/internal/storage/doltutil"
	"github.com/steveyegge/beads/internal/testutil"
)

// TestMutationsJournal_SeqMonotonicAcrossHandles proves the journal seq is a
// single shared, strictly-increasing sequence across independent connections to
// the same database — the guarantee that holds across processes and restarts,
// since the AUTO_INCREMENT counter lives in the durable (dolt_ignored) table,
// not in any one handle. A second sql.DB stands in for a second process.
func (s *testSuite) TestMutationsJournal_SeqMonotonicAcrossHandles() {
	ctx := s.Ctx()
	_, err := s.Runner().ExecContext(ctx, "DELETE FROM bd_mutations_journal")
	s.Require().NoError(err)

	port := testutil.DoltContainerPortInt()
	s.Require().NotZero(port)
	dsn := doltutil.ServerDSN{Host: "127.0.0.1", Port: port, User: "root", Database: s.dbName}.String()
	db2, err := sql.Open("mysql", dsn)
	s.Require().NoError(err)
	defer db2.Close()
	s.Require().NoError(db2.PingContext(ctx))

	insert := func(runner *sql.DB, id string) {
		_, err := runner.ExecContext(ctx,
			`INSERT INTO bd_mutations_journal (ts, op, issue_id, issue_json, dep_json) VALUES (UTC_TIMESTAMP(), 'create', ?, NULL, NULL)`, id)
		s.Require().NoError(err)
	}

	// Interleave writes across the two handles.
	insert(s.db, "h-a1")
	insert(db2, "h-b1")
	insert(s.db, "h-a2")
	insert(db2, "h-b2")

	rows, err := s.Runner().QueryContext(ctx, "SELECT seq FROM bd_mutations_journal ORDER BY seq ASC")
	s.Require().NoError(err)
	defer rows.Close()
	var seqs []int64
	for rows.Next() {
		var seq int64
		s.Require().NoError(rows.Scan(&seq))
		seqs = append(seqs, seq)
	}
	s.Require().NoError(rows.Err())
	s.Require().Len(seqs, 4, "all four interleaved writes must be recorded once")
	for i := 1; i < len(seqs); i++ {
		s.Greater(seqs[i], seqs[i-1], "seq must be strictly increasing across handles (shared engine counter)")
	}
}

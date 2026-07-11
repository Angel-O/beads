//go:build integration && !windows

package sqlite

// Cross-process regression guard for the sqlite backend's multiprocess safety
// case (WAL + busy_timeout(5000) + _txlock=immediate + single-connection pool,
// see dsn.go and dialect.go). The 2026-07 concurrency audit proved this
// configuration safe with a one-off hammer (~5,900 real cross-process bd ops,
// zero corruption, 640/640 single-winner claims); this test is the in-repo
// version of that hammer, so a future change — dropping _txlock=immediate,
// weakening the claim CAS in issueops.ClaimIssueInTx, a driver downgrade —
// fails CI instead of silently reopening a corruption or double-claim class.
//
// It builds the real bd binary once and spawns real OS subprocesses against
// ONE sqlite workspace:
//
//	(a) claim races: several bd processes race `bd update --claim` on each of
//	    many issues — exactly one winner per issue, losers exit nonzero, and
//	    the stored assignee matches the winner.
//	(b) readonly `bd ready` loops run during concurrent writer loops — zero
//	    reader errors (WAL: readers never block on or fail against writers).
//	(c) PRAGMA integrity_check == ok and the row count reconciles exactly
//	    against the ledger of successful creates.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	// mpClaimIssues × mpClaimersPerIssue racing `bd update --claim` calls.
	mpClaimIssues      = 20
	mpClaimersPerIssue = 6 // concurrent real bd subprocesses per issue

	// mpWriters concurrent writer loops (each iteration: create + update in
	// separate bd processes) against mpReaders concurrent readonly ready loops.
	mpWriters         = 4
	mpReaders         = 4
	mpWritesPerWriter = 25
)

// TestSQLiteCrossProcessConcurrency is the deployment-shape guard: many
// short-lived bd processes sharing one beads.db file. Serial subtests share
// the workspace on purpose — the integrity check at the end audits everything
// the earlier phases did.
func TestSQLiteCrossProcessConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cross-process concurrency test in -short mode")
	}

	bin := buildBDBinary(t)
	ws := t.TempDir()
	beadsDir := filepath.Join(ws, ".beads")

	if out, code := runBDProc(bin, ws, mpEnv(ws, beadsDir, "init-actor"), "init", "--backend", "sqlite", "--prefix", "mp", "--quiet"); code != 0 {
		t.Fatalf("bd init --backend sqlite failed (exit %d):\n%s", code, out)
	}

	t.Run("claim_race_single_winner", func(t *testing.T) {
		for i := 0; i < mpClaimIssues; i++ {
			id := fmt.Sprintf("mp-claim-%d", i)
			if out, code := runBDProc(bin, ws, mpEnv(ws, beadsDir, "creator"), "create", "claim target", "--id", id); code != 0 {
				t.Fatalf("bd create %s failed (exit %d):\n%s", id, code, out)
			}

			type attempt struct {
				actor string
				out   string
				code  int
			}
			results := make([]attempt, mpClaimersPerIssue)
			var wg sync.WaitGroup
			for k := 0; k < mpClaimersPerIssue; k++ {
				wg.Add(1)
				go func(k int) {
					defer wg.Done()
					actor := fmt.Sprintf("claimer-%d", k)
					out, code := runBDProc(bin, ws, mpEnv(ws, beadsDir, actor), "update", id, "--claim")
					results[k] = attempt{actor: actor, out: out, code: code}
				}(k)
			}
			wg.Wait()

			var winners []string
			for _, r := range results {
				if r.code == 0 {
					winners = append(winners, r.actor)
				}
			}
			if len(winners) != 1 {
				t.Fatalf("issue %s: want exactly 1 claim winner, got %d (%v); results: %+v", id, len(winners), winners, results)
			}
			if assignee := showAssignee(t, bin, ws, beadsDir, id); assignee != winners[0] {
				t.Fatalf("issue %s: stored assignee %q does not match claim winner %q", id, assignee, winners[0])
			}
		}
	})

	t.Run("readonly_scans_during_writes", func(t *testing.T) {
		writersDone := make(chan struct{})
		var wg sync.WaitGroup
		writerErrs := make([]error, mpWriters)
		for w := 0; w < mpWriters; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				actor := fmt.Sprintf("writer-%d", w)
				env := mpEnv(ws, beadsDir, actor)
				for i := 0; i < mpWritesPerWriter; i++ {
					id := fmt.Sprintf("mp-w%d-%d", w, i)
					if out, code := runBDProc(bin, ws, env, "create", "writer load", "--id", id); code != 0 {
						writerErrs[w] = fmt.Errorf("bd create %s failed (exit %d):\n%s", id, code, out)
						return
					}
					if out, code := runBDProc(bin, ws, env, "update", id, "-p", "2"); code != 0 {
						writerErrs[w] = fmt.Errorf("bd update %s failed (exit %d):\n%s", id, code, out)
						return
					}
				}
			}(w)
		}

		readerErrs := make([]error, mpReaders)
		readerOps := make([]int, mpReaders)
		var rwg sync.WaitGroup
		for r := 0; r < mpReaders; r++ {
			rwg.Add(1)
			go func(r int) {
				defer rwg.Done()
				env := mpEnv(ws, beadsDir, fmt.Sprintf("reader-%d", r))
				for {
					select {
					case <-writersDone:
						return
					default:
					}
					out, code := runBDProc(bin, ws, env, "ready", "--json")
					if code != 0 {
						readerErrs[r] = fmt.Errorf("bd ready --json failed (exit %d):\n%s", code, out)
						return
					}
					readerOps[r]++
				}
			}(r)
		}

		wg.Wait()
		close(writersDone)
		rwg.Wait()

		for w, err := range writerErrs {
			if err != nil {
				t.Errorf("writer %d: %v", w, err)
			}
		}
		total := 0
		for r, err := range readerErrs {
			if err != nil {
				t.Errorf("reader %d saw an error after %d clean scans: %v", r, readerOps[r], err)
			}
			total += readerOps[r]
		}
		if total == 0 {
			t.Error("readers completed zero ready scans — the scenario exercised nothing")
		}
		t.Logf("readonly scans completed with zero errors: %d across %d readers", total, mpReaders)
	})

	t.Run("integrity_check", func(t *testing.T) {
		dbPath := filepath.Join(beadsDir, "beads.db")
		db, err := sql.Open("sqlite", dsn(dbPath))
		if err != nil {
			t.Fatalf("open %s: %v", dbPath, err)
		}
		defer db.Close()
		db.SetMaxOpenConns(1)

		var verdict string
		if err := db.QueryRow("PRAGMA integrity_check").Scan(&verdict); err != nil {
			t.Fatalf("PRAGMA integrity_check: %v", err)
		}
		if verdict != "ok" {
			t.Fatalf("PRAGMA integrity_check = %q, want \"ok\"", verdict)
		}

		// Reconcile the row count against the op ledger: every create above
		// was asserted to exit 0, so the table must hold exactly that many
		// rows — a torn or lost row is corruption even when the b-tree is
		// structurally sound.
		wantRows := mpClaimIssues + mpWriters*mpWritesPerWriter
		var gotRows int
		if err := db.QueryRow("SELECT COUNT(*) FROM issues").Scan(&gotRows); err != nil {
			t.Fatalf("count issues: %v", err)
		}
		if gotRows != wantRows {
			t.Fatalf("issues table has %d rows, want %d (one per successful create)", gotRows, wantRows)
		}
	})
}

// buildBDBinary builds the real bd binary once for this test, into the test's
// own temp dir — NEVER the repository root (a stray root bd binary poisons the
// init tests; see cmd/bd/test_helpers_pure_test.go).
func buildBDBinary(t *testing.T) string {
	t.Helper()
	if configured := os.Getenv("BEADS_TEST_BD_BINARY"); configured != "" {
		if _, err := os.Stat(configured); err != nil {
			t.Fatalf("BEADS_TEST_BD_BINARY %q is not usable: %v", configured, err)
		}
		abs, err := filepath.Abs(configured)
		if err != nil {
			t.Fatalf("resolve BEADS_TEST_BD_BINARY: %v", err)
		}
		return abs
	}
	bin := filepath.Join(t.TempDir(), "bd")
	cmd := exec.Command("go", "build", "-tags", "gms_pure_go", "-o", bin, "./cmd/bd")
	cmd.Dir = repoRootDir()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build bd: %v\n%s", err, out)
	}
	return bin
}

// mpEnv is a scrubbed subprocess environment pinned to the shared workspace:
// all ambient BEADS_*/BD_* are dropped, BEADS_DIR pins the store, HOME is the
// workspace (no ambient git identity), background side channels are off, and
// BEADS_ACTOR gives each subprocess a distinct identity so claim winners are
// attributable.
func mpEnv(ws, beadsDir, actor string) []string {
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "BEADS_") || strings.HasPrefix(e, "BD_") {
			continue
		}
		env = append(env, e)
	}
	return append(env,
		"HOME="+ws,
		"BEADS_DIR="+beadsDir,
		"BEADS_ACTOR="+actor,
		"BEADS_NO_DAEMON=1",
		"BEADS_DOLT_AUTO_START=0",
		"BD_DISABLE_METRICS=1",
		"BD_DISABLE_EVENT_FLUSH=1",
	)
}

// runBDProc runs one real bd subprocess in dir and returns its combined output
// and exit code.
func runBDProc(bin, dir string, env []string, args ...string) (string, int) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if err == nil {
		return buf.String(), 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return buf.String(), ee.ExitCode()
	}
	return buf.String() + "\nexec error: " + err.Error(), -1
}

// showAssignee reads an issue's stored assignee through the real CLI
// (`bd show --json` returns an array of issue objects).
func showAssignee(t *testing.T, bin, ws, beadsDir, id string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		out, code := runBDProc(bin, ws, mpEnv(ws, beadsDir, "auditor"), "show", id, "--json")
		if code == 0 {
			var issues []struct {
				ID       string `json:"id"`
				Assignee string `json:"assignee"`
			}
			if err := json.Unmarshal([]byte(out), &issues); err != nil || len(issues) == 0 {
				t.Fatalf("bd show %s --json: unparseable output (%v):\n%s", id, err, out)
			}
			return issues[0].Assignee
		}
		// bd show is a read; with busy_timeout it should not fail, but give
		// the audit read the same retry courtesy a human operator would.
		if time.Now().After(deadline) {
			t.Fatalf("bd show %s --json kept failing (exit %d):\n%s", id, code, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

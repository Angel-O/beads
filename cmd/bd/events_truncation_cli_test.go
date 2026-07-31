package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

// TestEventsTailReportsTruncationToTheCLI is the end-to-end guard for the
// retention boundary as a consumer actually meets it: through the binary, not
// through the store API.
//
// It exists because the store returning a typed error is only half the
// contract — the CLI has two read call sites (the one-shot read and the
// --follow poll) and wiring only one of them still lets `bd events export`
// present a pruned journal as a complete history. A store-level test cannot see
// that; this one fails if either path regresses.
func TestEventsTailReportsTruncationToTheCLI(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "evt")

	// Disable both retention floors so the prune can actually reach the rows;
	// the shipped defaults are non-zero precisely to prevent this by accident.
	env := append(os.Environ(),
		"BD_EVENTS_JOURNAL=1",
		"BD_EVENTS_JOURNAL_RETAIN_DAYS=0",
		"BD_EVENTS_JOURNAL_RETAIN_ROWS=0",
	)
	run := func(args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	for _, title := range []string{"one", "two", "three", "four", "five"} {
		if out, err := run("create", title); err != nil {
			t.Fatalf("create %s: %v\n%s", title, err, out)
		}
	}
	if out, err := run("events", "prune", "--before", "4"); err != nil {
		t.Fatalf("prune: %v\n%s", err, out)
	}

	// Both read commands must fail rather than serve the surviving suffix.
	for _, args := range [][]string{
		{"events", "export", "--json"},
		{"events", "tail", "--since", "0", "--json"},
	} {
		out, err := run(args...)
		if err == nil {
			t.Fatalf("%v succeeded on a pruned-past checkpoint; want a truncation failure\n%s", args, out)
		}
		var got struct {
			Code  string `json:"code"`
			Since int64  `json:"since"`
			Floor int64  `json:"floor"`
			Head  int64  `json:"head"`
		}
		if decErr := json.Unmarshal([]byte(firstJSONObject(out)), &got); decErr != nil {
			t.Fatalf("%v: output is not a JSON object: %v\n%s", args, decErr, out)
		}
		if got.Code != storage.EventsJournalTruncatedCode {
			t.Errorf("%v: code = %q, want %q\n%s", args, got.Code, storage.EventsJournalTruncatedCode, out)
		}
		if got.Since != 0 || got.Floor != 4 || got.Head != 5 {
			t.Errorf("%v: since/floor/head = %d/%d/%d, want 0/4/5\n%s", args, got.Since, got.Floor, got.Head, out)
		}
	}

	// Resuming from the retained floor-1 still works and returns the surviving
	// records — a truncation must not be a dead end.
	out, err := run("events", "tail", "--since", "3")
	if err != nil {
		t.Fatalf("resume from floor-1: %v\n%s", err, out)
	}
	if lines := strings.Count(strings.TrimSpace(out), "\n") + 1; lines != 2 {
		t.Errorf("resume from floor-1 emitted %d records, want 2\n%s", lines, out)
	}
}

// firstJSONObject extracts the JSON object from combined output, tolerating any
// non-JSON preamble the binary may log around it.
func firstJSONObject(out string) string {
	start := strings.Index(out, "{")
	end := strings.LastIndex(out, "}")
	if start < 0 || end < start {
		return out
	}
	return out[start : end+1]
}

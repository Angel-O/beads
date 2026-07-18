//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestUnclaimIfAssigneeCLI drives `bd unclaim --if-assignee` end-to-end against
// the embedded Dolt backend: the conditional release must be a compare-and-swap,
// not a read-then-clobber. A stale expectation exits nonzero, names the current
// holder, and leaves the claim untouched; the matching expectation releases the
// claim; and releasing an already-released issue is a distinct failure, not a
// silent success (the exactly-once property a release-if-current supervisor
// depends on).
func TestUnclaimIfAssigneeCLI(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ur")

	issue := bdCreate(t, bd, dir, "Conditional release", "--type", "task")
	bdUpdate(t, bd, dir, issue.ID, "--assignee", "alice", "--status", "in_progress")

	// Stale expectation: distinct failure that names the current holder, claim intact.
	out := bdUnclaimFail(t, bd, dir, issue.ID, "--if-assignee", "bob")
	if !strings.Contains(out, "alice") {
		t.Errorf("mismatch error should name the current holder alice, got:\n%s", out)
	}
	got := bdShow(t, bd, dir, issue.ID)
	if got.Assignee != "alice" {
		t.Errorf("stale --if-assignee clobbered the claim: assignee = %q, want alice", got.Assignee)
	}
	if got.Status != types.StatusInProgress {
		t.Errorf("stale --if-assignee changed status to %q, want in_progress", got.Status)
	}

	// Matching expectation: releases the claim.
	bdUnclaim(t, bd, dir, issue.ID, "--if-assignee", "alice")
	got = bdShow(t, bd, dir, issue.ID)
	if got.Assignee != "" {
		t.Errorf("after matching --if-assignee: assignee = %q, want empty", got.Assignee)
	}
	if got.Status != types.StatusOpen {
		t.Errorf("after matching --if-assignee: status = %q, want open", got.Status)
	}

	// Releasing an already-released issue with --if-assignee is also a distinct
	// failure (exactly-once), not a silent success.
	_ = bdUnclaimFail(t, bd, dir, issue.ID, "--if-assignee", "alice")
}

package dolt

import (
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// TestCloseIssueChecked exercises the atomic, guarded close. The guard
// (is_blocked) and the close share ONE transaction, so a blocked issue is
// refused with storage.ErrCloseBlocked and — critically — the transaction rolls
// back leaving the issue open with no `closed` event recorded (the atomic-refuse
// property). Force bypasses the guard, and already-closed is an idempotent
// success (Unchanged=true).
func TestCloseIssueChecked(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	// mkBlocked creates a blocker and a target with a `blocks` dependency so the
	// target's is_blocked is 1, mirroring the fixtures in is_blocked_test.go.
	mkBlocked := func(t *testing.T, prefix string) string {
		t.Helper()
		blocker, target := prefix+"-blocker", prefix+"-target"
		createPerm(t, ctx, store, blocker)
		createPerm(t, ctx, store, target)
		if err := store.AddDependency(ctx, &types.Dependency{
			IssueID: target, DependsOnID: blocker, Type: types.DepBlocks,
		}, "tester"); err != nil {
			t.Fatalf("AddDependency: %v", err)
		}
		if !getIsBlocked(t, ctx, store, "issues", target) {
			t.Fatalf("%s should be is_blocked = 1 after adding blocks dep", target)
		}
		return target
	}

	// mkBlockedWisp is mkBlocked's ephemeral twin: the target is a wisp, so the
	// close routes through closeWispChecked (its own rollback wrapper, no
	// DOLT_COMMIT) rather than the permanent withRetryTx path.
	mkBlockedWisp := func(t *testing.T, prefix string) string {
		t.Helper()
		blocker, target := prefix+"-blocker", prefix+"-target"
		createPerm(t, ctx, store, blocker)
		createWisp(t, ctx, store, target)
		if err := store.AddDependency(ctx, &types.Dependency{
			IssueID: target, DependsOnID: blocker, Type: types.DepBlocks,
		}, "tester"); err != nil {
			t.Fatalf("AddDependency: %v", err)
		}
		if !getIsBlocked(t, ctx, store, "wisps", target) {
			t.Fatalf("wisp %s should be is_blocked = 1 after adding blocks dep", target)
		}
		return target
	}

	getStatus := func(t *testing.T, id string) types.Status {
		t.Helper()
		iss, err := store.GetIssue(ctx, id)
		if err != nil {
			t.Fatalf("GetIssue(%s): %v", id, err)
		}
		if iss == nil {
			t.Fatalf("GetIssue(%s) returned nil issue", id)
		}
		return iss.Status
	}

	countClosedEvents := func(t *testing.T, id string) int {
		t.Helper()
		events, err := store.GetEvents(ctx, id, 0)
		if err != nil {
			t.Fatalf("GetEvents(%s): %v", id, err)
		}
		n := 0
		for _, e := range events {
			if e.EventType == types.EventClosed {
				n++
			}
		}
		return n
	}

	// mkClosed creates a plain issue and closes it via the guarded path so the
	// already-closed case can re-close it.
	mkClosed := func(t *testing.T, id string) string {
		t.Helper()
		createPerm(t, ctx, store, id)
		if _, err := store.CloseIssueChecked(ctx, id, "tester",
			storage.CloseIssueOptions{Reason: "done"}); err != nil {
			t.Fatalf("initial CloseIssueChecked(%s): %v", id, err)
		}
		return id
	}

	tests := []struct {
		name  string
		setup func(t *testing.T) string
		opts  storage.CloseIssueOptions
		check func(t *testing.T, id string, res storage.CloseIssueResult, err error)
	}{
		{
			name:  "blocked without force refuses and rolls back",
			setup: func(t *testing.T) string { return mkBlocked(t, "cic-noforce") },
			opts:  storage.CloseIssueOptions{Reason: "done"},
			check: func(t *testing.T, id string, res storage.CloseIssueResult, err error) {
				if !errors.Is(err, storage.ErrCloseBlocked) {
					t.Fatalf("err = %v, want errors.Is(_, ErrCloseBlocked)", err)
				}
				// Atomic refuse: the issue must still be open (not closed) and
				// no closed event may have been written — the guard aborted the
				// tx before the close ran.
				if got := getStatus(t, id); got == types.StatusClosed {
					t.Fatalf("issue %s status = closed after refused close; guard did not abort", id)
				}
				if n := countClosedEvents(t, id); n != 0 {
					t.Fatalf("closed event count for %s = %d, want 0 (tx must have rolled back)", id, n)
				}
			},
		},
		{
			name:  "blocked with force closes",
			setup: func(t *testing.T) string { return mkBlocked(t, "cic-force") },
			opts:  storage.CloseIssueOptions{Reason: "done", Force: true},
			check: func(t *testing.T, id string, res storage.CloseIssueResult, err error) {
				if err != nil {
					t.Fatalf("Force close err = %v, want nil", err)
				}
				if res.Unchanged {
					t.Fatalf("res.Unchanged = true, want false (a real close)")
				}
				if got := getStatus(t, id); got != types.StatusClosed {
					t.Fatalf("issue %s status = %q, want closed", id, got)
				}
			},
		},
		{
			name: "not blocked closes",
			setup: func(t *testing.T) string {
				createPerm(t, ctx, store, "cic-plain")
				return "cic-plain"
			},
			opts: storage.CloseIssueOptions{Reason: "done"},
			check: func(t *testing.T, id string, res storage.CloseIssueResult, err error) {
				if err != nil {
					t.Fatalf("close err = %v, want nil", err)
				}
				if res.Unchanged {
					t.Fatalf("res.Unchanged = true, want false (a real close)")
				}
				if got := getStatus(t, id); got != types.StatusClosed {
					t.Fatalf("issue %s status = %q, want closed", id, got)
				}
			},
		},
		{
			name:  "already closed is idempotent",
			setup: func(t *testing.T) string { return mkClosed(t, "cic-idem") },
			opts:  storage.CloseIssueOptions{Reason: "again"},
			check: func(t *testing.T, id string, res storage.CloseIssueResult, err error) {
				if err != nil {
					t.Fatalf("re-close err = %v, want nil", err)
				}
				if !res.Unchanged {
					t.Fatalf("res.Unchanged = false, want true (already closed)")
				}
				if got := getStatus(t, id); got != types.StatusClosed {
					t.Fatalf("issue %s status = %q, want closed", id, got)
				}
			},
		},
		{
			name:  "force on already closed is unchanged",
			setup: func(t *testing.T) string { return mkClosed(t, "cic-force-idem") },
			opts:  storage.CloseIssueOptions{Reason: "again", Force: true},
			check: func(t *testing.T, id string, res storage.CloseIssueResult, err error) {
				if err != nil {
					t.Fatalf("force re-close err = %v, want nil", err)
				}
				if !res.Unchanged {
					t.Fatalf("res.Unchanged = false, want true (already closed, even with Force)")
				}
				if got := getStatus(t, id); got != types.StatusClosed {
					t.Fatalf("issue %s status = %q, want closed", id, got)
				}
			},
		},
		{
			name: "non-blocking dep does not trip guard",
			setup: func(t *testing.T) string {
				createPerm(t, ctx, store, "cic-rel-other")
				createPerm(t, ctx, store, "cic-rel-target")
				if err := store.AddDependency(ctx, &types.Dependency{
					IssueID: "cic-rel-target", DependsOnID: "cic-rel-other", Type: types.DepRelated,
				}, "tester"); err != nil {
					t.Fatalf("AddDependency(related): %v", err)
				}
				if getIsBlocked(t, ctx, store, "issues", "cic-rel-target") {
					t.Fatalf("cic-rel-target should NOT be is_blocked with only a related dep")
				}
				return "cic-rel-target"
			},
			opts: storage.CloseIssueOptions{Reason: "done"},
			check: func(t *testing.T, id string, res storage.CloseIssueResult, err error) {
				if err != nil {
					t.Fatalf("close with non-blocking dep err = %v, want nil", err)
				}
				if res.Unchanged {
					t.Fatalf("res.Unchanged = true, want false (a real close)")
				}
				if got := getStatus(t, id); got != types.StatusClosed {
					t.Fatalf("issue %s status = %q, want closed", id, got)
				}
			},
		},
		{
			name:  "wisp blocked without force refuses and rolls back",
			setup: func(t *testing.T) string { return mkBlockedWisp(t, "cic-wisp-noforce") },
			opts:  storage.CloseIssueOptions{Reason: "done"},
			check: func(t *testing.T, id string, res storage.CloseIssueResult, err error) {
				if !errors.Is(err, storage.ErrCloseBlocked) {
					t.Fatalf("err = %v, want errors.Is(_, ErrCloseBlocked)", err)
				}
				// closeWispChecked's deferred Rollback must discard the tx: the
				// wisp stays open with no closed event.
				if got := getStatus(t, id); got == types.StatusClosed {
					t.Fatalf("wisp %s status = closed after refused close; guard did not abort", id)
				}
				if n := countClosedEvents(t, id); n != 0 {
					t.Fatalf("closed event count for wisp %s = %d, want 0 (tx must have rolled back)", id, n)
				}
			},
		},
		{
			name:  "wisp blocked with force closes",
			setup: func(t *testing.T) string { return mkBlockedWisp(t, "cic-wisp-force") },
			opts:  storage.CloseIssueOptions{Reason: "done", Force: true},
			check: func(t *testing.T, id string, res storage.CloseIssueResult, err error) {
				if err != nil {
					t.Fatalf("Force close wisp err = %v, want nil", err)
				}
				if res.Unchanged {
					t.Fatalf("res.Unchanged = true, want false (a real close)")
				}
				if got := getStatus(t, id); got != types.StatusClosed {
					t.Fatalf("wisp %s status = %q, want closed", id, got)
				}
			},
		},
		{
			name:  "missing id returns ErrNotFound",
			setup: func(t *testing.T) string { return "cic-does-not-exist" },
			opts:  storage.CloseIssueOptions{Reason: "done"},
			check: func(t *testing.T, id string, res storage.CloseIssueResult, err error) {
				if !errors.Is(err, storage.ErrNotFound) {
					t.Fatalf("err = %v, want errors.Is(_, ErrNotFound)", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id := tc.setup(t)
			res, err := store.CloseIssueChecked(ctx, id, "tester", tc.opts)
			tc.check(t, id, res, err)
		})
	}
}

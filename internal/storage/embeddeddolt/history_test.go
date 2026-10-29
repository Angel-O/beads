//go:build cgo

package embeddeddolt_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

func TestBulkHistoryMatchesRepeatedHistory(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	te := newTestEnv(t, "bh")
	ctx := t.Context()
	for _, issue := range []*types.Issue{
		{ID: "bh-b", Title: "B v1", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
		{ID: "bh-a", Title: "A v1", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
	} {
		if err := te.store.CreateIssue(ctx, issue, "tester"); err != nil {
			t.Fatalf("CreateIssue(%s): %v", issue.ID, err)
		}
	}
	if err := te.store.Commit(ctx, "create bulk history fixtures"); err != nil {
		t.Fatal(err)
	}
	if err := te.store.UpdateIssue(ctx, "bh-a", map[string]interface{}{"title": "A v2"}, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := te.store.Commit(ctx, "update A"); err != nil {
		t.Fatal(err)
	}

	groups, err := te.store.BulkHistory(ctx, []string{"bh-b", "bh-missing", "bh-a", "bh-b"})
	if err != nil {
		t.Fatalf("BulkHistory: %v", err)
	}
	if len(groups) != 3 || groups[0].IssueID != "bh-a" || groups[1].IssueID != "bh-b" || groups[2].IssueID != "bh-missing" {
		t.Fatalf("groups = %#v", groups)
	}
	for _, group := range groups {
		single, err := te.store.History(ctx, group.IssueID)
		if err != nil {
			t.Fatalf("History(%s): %v", group.IssueID, err)
		}
		if !reflect.DeepEqual(group.Entries, single) {
			t.Fatalf("BulkHistory(%s) differs from History", group.IssueID)
		}
	}
}

func TestCompactBulkHistoryMatchesRealEngineBaseline(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	previousLocal := time.Local
	time.Local = time.FixedZone("compact-history UTC+1", 60*60)
	t.Cleanup(func() { time.Local = previousLocal })
	te := newTestEnv(t, "ch")
	ctx := t.Context()
	issue := &types.Issue{ID: "ch-issue", Title: "Alpha", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	if err := te.store.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatal(err)
	}
	graphDates := []time.Time{commitAt(t, te, "compact history initial", "2030-01-03T12:00:05")}
	for i, title := range []string{"Beta", "Alpha", "alpha"} {
		if err := te.store.UpdateIssue(ctx, issue.ID, map[string]any{"title": title}, "tester"); err != nil {
			t.Fatal(err)
		}
		// Deliberately tie the first two edits and make the third commit
		// date earlier than its parent to test producer ordering independently
		// from graph chronology.
		updateDates := []string{"2030-01-03T12:00:10.123456", "2030-01-03T12:00:10.123456", "2030-01-03T12:00:07.654321"}
		graphDates = append(graphDates, commitAt(t, te, "compact history update", updateDates[i]))
		// Add a commit without touching the target, exercising unchanged historical
		// rows as returned by the pinned engine's real history table.
		other := &types.Issue{ID: "ch-other", Title: title, Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, other, "tester"); err != nil {
			t.Fatal(err)
		}
		unrelatedDates := []string{"2030-01-03T12:00:03.333333", "2030-01-03T12:00:02.222222", "2030-01-03T12:00:01.111111"}
		graphDates = append(graphDates, commitAt(t, te, "unrelated commit", unrelatedDates[i]))
	}
	if !graphDates[1].Equal(graphDates[3]) {
		t.Fatalf("fixture did not create a timestamp tie: %v != %v", graphDates[1], graphDates[3])
	}
	if !graphDates[2].Before(graphDates[1]) {
		t.Fatalf("fixture did not create a nonmonotonic commit date: child=%v parent=%v", graphDates[2], graphDates[1])
	}
	mainBranch, err := te.store.CurrentBranch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := te.store.Branch(ctx, "compact-side"); err != nil {
		t.Fatal(err)
	}
	if err := te.store.Checkout(ctx, "compact-side"); err != nil {
		t.Fatal(err)
	}
	if err := te.store.UpdateIssue(ctx, issue.ID, map[string]any{"title": "Side branch"}, "tester"); err != nil {
		t.Fatal(err)
	}
	commitAt(t, te, "compact side branch edit", "2030-01-03T12:00:12")
	if err := te.store.Checkout(ctx, mainBranch); err != nil {
		t.Fatal(err)
	}
	mainOnly := &types.Issue{ID: "ch-mainline", Title: "Mainline", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	if err := te.store.CreateIssue(ctx, mainOnly, "tester"); err != nil {
		t.Fatal(err)
	}
	commitAt(t, te, "compact mainline edit", "2030-01-03T12:00:11")
	conflicts, err := te.store.Merge(ctx, "compact-side")
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("merge compact-side conflicts=%v err=%v", conflicts, err)
	}
	if err := te.store.Commit(ctx, "compact merge follow-up"); err != nil {
		t.Fatal(err)
	}
	if err := te.store.DeleteIssue(ctx, issue.ID); err != nil {
		t.Fatal(err)
	}
	if err := te.store.Commit(ctx, "delete compact history issue"); err != nil {
		t.Fatal(err)
	}
	if err := te.store.CreateIssue(ctx, &types.Issue{ID: issue.ID, Title: "Recreated", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := te.store.Commit(ctx, "recreate compact history issue"); err != nil {
		t.Fatal(err)
	}
	baseline, err := te.store.BulkHistory(ctx, []string{"ch-issue"})
	if err != nil {
		t.Fatalf("baseline BulkHistory: %v", err)
	}
	compact, err := te.store.CompactBulkHistory(ctx, []string{" ch-issue ", "ch-missing", "ch-issue"}, 0)
	if err != nil {
		t.Fatalf("CompactBulkHistory: %v", err)
	}
	if len(compact) != 2 || compact[0].IssueID != "ch-issue" || compact[1].IssueID != "ch-missing" || compact[1].States == nil || len(compact[1].States) != 0 {
		t.Fatalf("groups/miss = %#v", compact)
	}
	want := make(map[string][]storage.HistoryOccurrence)
	for _, entry := range baseline[0].Entries {
		key, _ := json.Marshal(entry.Issue)
		want[string(key)] = append(want[string(key)], storage.HistoryOccurrence{CommitHash: entry.CommitHash, Committer: entry.Committer, CommitDate: entry.CommitDate})
	}
	got := make(map[string][]storage.HistoryOccurrence)
	for _, state := range compact[0].States {
		key, _ := json.Marshal(state.Issue)
		if _, exists := got[string(key)]; exists {
			t.Fatalf("duplicate equal normalized state: %s", key)
		}
		got[string(key)] = state.Occurrences
	}
	if len(got) != len(want) {
		t.Fatalf("compact state count=%d, baseline=%d", len(got), len(want))
	}
	sawFractionalDate := false
	for key, wantOccurrences := range want {
		gotOccurrences, ok := got[key]
		if !ok || len(gotOccurrences) != len(wantOccurrences) {
			t.Fatalf("occurrences for %s: got=%#v want=%#v", key, gotOccurrences, wantOccurrences)
		}
		for i := range wantOccurrences {
			g, w := gotOccurrences[i], wantOccurrences[i]
			wantDateJSON, err := json.Marshal(w.CommitDate)
			if err != nil {
				t.Fatal(err)
			}
			gotDateJSON, err := json.Marshal(g.CommitDate)
			if err != nil {
				t.Fatal(err)
			}
			sawFractionalDate = sawFractionalDate || strings.Contains(string(wantDateJSON), ".")
			if g.CommitHash != w.CommitHash || g.Committer != w.Committer || string(gotDateJSON) != string(wantDateJSON) {
				t.Fatalf("occurrence mismatch for %s at %d: got=%+v want=%+v", key, i, g, w)
			}
		}
	}
	if !sawFractionalDate {
		t.Fatal("real-engine history fixture did not preserve any fractional CommitDate precision")
	}

	limited, err := te.store.CompactBulkHistory(ctx, []string{issue.ID}, 2)
	if err != nil {
		t.Fatalf("limited CompactBulkHistory: %v", err)
	}
	var occurrences []storage.HistoryOccurrence
	for _, state := range limited[0].States {
		occurrences = append(occurrences, state.Occurrences...)
	}
	sort.Slice(occurrences, func(i, j int) bool {
		if !occurrences[i].CommitDate.Equal(occurrences[j].CommitDate) {
			return occurrences[i].CommitDate.After(occurrences[j].CommitDate)
		}
		return occurrences[i].CommitHash > occurrences[j].CommitHash
	})
	if len(occurrences) != 2 {
		t.Fatalf("limited occurrences=%d, want 2", len(occurrences))
	}
	for i := range occurrences {
		entry := baseline[0].Entries[i]
		if occurrences[i].CommitHash != entry.CommitHash || !occurrences[i].CommitDate.Equal(entry.CommitDate) || occurrences[i].Committer != entry.Committer {
			t.Fatalf("limited occurrence %d=%+v, baseline=%+v", i, occurrences[i], entry)
		}
	}
	unlimited, err := te.store.CompactBulkHistory(ctx, []string{issue.ID}, -1)
	if err != nil {
		t.Fatalf("nonpositive CompactBulkHistory: %v", err)
	}
	var unlimitedCount int
	for _, state := range unlimited[0].States {
		unlimitedCount += len(state.Occurrences)
	}
	if unlimitedCount != len(baseline[0].Entries) {
		t.Fatalf("nonpositive limit retained %d occurrences, baseline has %d", unlimitedCount, len(baseline[0].Entries))
	}
}

func commitAt(t *testing.T, te *testEnv, message, date string) time.Time {
	t.Helper()
	te.exec(t, t.Context(), "CALL DOLT_COMMIT('-Am', ?, '--date', ?)", message, date)
	commits, err := te.store.Log(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, commit := range commits {
		if commit.Message == message {
			return commit.Date
		}
	}
	t.Fatalf("commit %q not found in real Dolt log", message)
	return time.Time{}
}

// migration0049DDL replays migration 0049's exact column widening: the four
// issues text columns move from TEXT to LONGTEXT NOT NULL. See
// internal/storage/schema/cli_migrations.go (cliMigration0049LongtextLargeContentColumns).
const migration0049DDL = "ALTER TABLE issues " +
	"MODIFY COLUMN description LONGTEXT NOT NULL, " +
	"MODIFY COLUMN design LONGTEXT NOT NULL, " +
	"MODIFY COLUMN acceptance_criteria LONGTEXT NOT NULL, " +
	"MODIFY COLUMN notes LONGTEXT NOT NULL"

// TestHistory_NullTextColumns reproduces GH#4867: dolt_history_issues
// projects every historical row against the CURRENT branch-head schema. A
// row committed while the issues text columns were still TEXT (pre-0049)
// type-mismatches the post-0049 LONGTEXT column definition when Dolt
// re-projects it, which surfaces as NULL rather than the original value.
// This is real migration behavior, not a hand-written NULL row: schema
// widening never mutates existing row bytes, only the type Dolt uses to
// project them.
func TestHistory_NullTextColumns(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	te := newTestEnv(t, "nh")
	ctx := t.Context()

	// (a) Simulate the pre-0049 schema: TEXT columns, not yet migrated.
	for _, col := range []string{"description", "design", "acceptance_criteria", "notes"} {
		te.exec(t, ctx, "ALTER TABLE issues MODIFY COLUMN `"+col+"` TEXT NOT NULL")
	}
	if err := te.store.Commit(ctx, "narrow issues text columns to TEXT (pre-0049 schema)"); err != nil {
		t.Fatalf("Commit (TEXT schema): %v", err)
	}

	// (b) Commit an issue under the pre-0049 TEXT schema. This becomes the
	// OLDER history entry.
	issue := &types.Issue{
		ID:                 "nh-null1",
		Title:              "Null history test",
		Description:        "original description",
		Design:             "original design",
		AcceptanceCriteria: "original AC",
		Notes:              "original notes",
		Status:             types.StatusOpen,
		Priority:           2,
		IssueType:          types.TypeTask,
	}
	if err := te.store.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := te.store.Commit(ctx, "initial commit under TEXT schema"); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// (c) Replay migration 0049's exact DDL, widening to LONGTEXT. The row
	// data is untouched; only the branch-head column type changes. This
	// becomes the NEWEST history entry.
	te.exec(t, ctx, migration0049DDL)
	if err := te.store.Commit(ctx, "replay migration 0049 (TEXT -> LONGTEXT)"); err != nil {
		t.Fatalf("Commit (migration 0049): %v", err)
	}

	history, err := te.store.History(ctx, issue.ID)
	if err != nil {
		t.Fatalf("History() failed across a TEXT -> LONGTEXT migration: %v", err)
	}
	if len(history) < 2 {
		t.Fatalf("expected at least 2 history entries, got %d", len(history))
	}

	// Newest entry (post-migration commit): schema matches branch head, so
	// the real values project through untouched.
	newest := history[0].Issue
	if newest.Description != issue.Description {
		t.Errorf("expected newest description %q, got %q", issue.Description, newest.Description)
	}
	if newest.Design != issue.Design {
		t.Errorf("expected newest design %q, got %q", issue.Design, newest.Design)
	}
	if newest.AcceptanceCriteria != issue.AcceptanceCriteria {
		t.Errorf("expected newest acceptance_criteria %q, got %q", issue.AcceptanceCriteria, newest.AcceptanceCriteria)
	}
	if newest.Notes != issue.Notes {
		t.Errorf("expected newest notes %q, got %q", issue.Notes, newest.Notes)
	}

	// Older entry (pre-migration commit, TEXT-era): re-projected against the
	// current LONGTEXT schema, the type mismatch surfaces as NULL, which the
	// COALESCE in the scan turns into "".
	older := history[1].Issue
	if older.Description != "" {
		t.Errorf("expected pre-migration description to coalesce to \"\", got %q", older.Description)
	}
	if older.Design != "" {
		t.Errorf("expected pre-migration design to coalesce to \"\", got %q", older.Design)
	}
	if older.AcceptanceCriteria != "" {
		t.Errorf("expected pre-migration acceptance_criteria to coalesce to \"\", got %q", older.AcceptanceCriteria)
	}
	if older.Notes != "" {
		t.Errorf("expected pre-migration notes to coalesce to \"\", got %q", older.Notes)
	}
	compact, err := te.store.CompactBulkHistory(ctx, []string{issue.ID}, 0)
	if err != nil {
		t.Fatalf("CompactBulkHistory across TEXT -> LONGTEXT schema: %v", err)
	}
	var compactEntries int
	for _, state := range compact[0].States {
		for _, occurrence := range state.Occurrences {
			compactEntries++
			found := false
			for _, entry := range history {
				if entry.CommitHash == occurrence.CommitHash && entry.Committer == occurrence.Committer && entry.CommitDate.Equal(occurrence.CommitDate) && reflect.DeepEqual(entry.Issue, state.Issue) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("compact historical occurrence %+v has no baseline match", occurrence)
			}
		}
	}
	if compactEntries != len(history) {
		t.Fatalf("compact occurrences=%d, baseline history=%d", compactEntries, len(history))
	}
}

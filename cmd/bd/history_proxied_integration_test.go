//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

func TestProxiedServerHistory(t *testing.T) {
	requireSharedProxiedServer(t)
	t.Parallel()
	bd := buildEmbeddedBD(t)
	p := newSharedProxiedProject(t, bd, "hst")

	// Seed: create an issue then mutate it several times to build commit history.
	issue := bdProxiedCreate(t, bd, p.dir, "History test issue", "--type", "task", "--priority", "3")
	bdProxiedUpdate(t, bd, p.dir, issue.ID, "--status", "in_progress")
	bdProxiedUpdate(t, bd, p.dir, issue.ID, "--priority", "1")
	bdProxiedUpdate(t, bd, p.dir, issue.ID, "--title", "History test issue updated")

	t.Run("compact_history_v2", func(t *testing.T) {
		// The proxied MySQL DSN leaves Loc unset (ParseTime defaults to UTC),
		// while this non-UTC child timezone catches accidental Local parsing.
		const testTZ = "Etc/GMT-1"
		baselineOutput, baselineStderr, err := bdProxiedRunBuffersWithEnv(t, bd, p.dir, []string{"TZ=" + testTZ}, "history", "--json", issue.ID)
		if err != nil {
			t.Fatalf("baseline history: %v\n%s\n%s", err, baselineOutput, baselineStderr)
		}
		baselineJSON := []byte(baselineOutput)
		type occurrence struct {
			CommitHash string          `json:"CommitHash"`
			Committer  string          `json:"Committer"`
			CommitDate json.RawMessage `json:"CommitDate"`
		}
		type reconstructedEntry struct {
			Issue      json.RawMessage
			occurrence occurrence
		}
		var baseline struct {
			SchemaVersion int `json:"schema_version"`
			Issues        []struct {
				IssueID   string `json:"issue_id"`
				Snapshots []struct {
					Issue      json.RawMessage `json:"Issue"`
					CommitHash string          `json:"CommitHash"`
					Committer  string          `json:"Committer"`
					CommitDate json.RawMessage `json:"CommitDate"`
				} `json:"snapshots"`
			} `json:"issues"`
		}
		if err := json.Unmarshal(baselineJSON, &baseline); err != nil {
			t.Fatalf("parse baseline history: %v\n%s", err, baselineJSON)
		}
		if baseline.SchemaVersion != 1 || len(baseline.Issues) != 1 || baseline.Issues[0].IssueID != issue.ID {
			t.Fatalf("unexpected baseline envelope: %s", baselineJSON)
		}
		want := make([]reconstructedEntry, 0, len(baseline.Issues[0].Snapshots))
		for _, entry := range baseline.Issues[0].Snapshots {
			want = append(want, reconstructedEntry{Issue: entry.Issue, occurrence: occurrence{CommitHash: entry.CommitHash, Committer: entry.Committer, CommitDate: entry.CommitDate}})
		}

		compactOutput, compactStderr, err := bdProxiedRunBuffersWithEnv(t, bd, p.dir, []string{"TZ=" + testTZ}, "history", "--json", "--compact", issue.ID, "hst-missing")
		if err != nil {
			t.Fatalf("compact history: %v\n%s\n%s", err, compactOutput, compactStderr)
		}
		out := []byte(compactOutput)
		type compactGroup struct {
			IssueID string `json:"issue_id"`
			States  []struct {
				Issue       json.RawMessage `json:"Issue"`
				Occurrences []occurrence    `json:"occurrences"`
			} `json:"states"`
		}
		var envelope struct {
			SchemaVersion int            `json:"schema_version"`
			Issues        []compactGroup `json:"issues"`
		}
		if err := json.Unmarshal(out, &envelope); err != nil {
			t.Fatalf("parse compact history: %v\n%s", err, out)
		}
		wantIDs := []string{issue.ID, "hst-missing"}
		sort.Strings(wantIDs)
		gotIDs := make([]string, len(envelope.Issues))
		var issueGroup, missingGroup *compactGroup
		for i := range envelope.Issues {
			group := &envelope.Issues[i]
			gotIDs[i] = group.IssueID
			switch group.IssueID {
			case issue.ID:
				issueGroup = group
			case "hst-missing":
				missingGroup = group
			}
		}
		if envelope.SchemaVersion != 2 || len(envelope.Issues) != 2 || strings.Join(gotIDs, "\x00") != strings.Join(wantIDs, "\x00") || issueGroup == nil || missingGroup == nil || missingGroup.States == nil || len(missingGroup.States) != 0 {
			t.Fatalf("unexpected compact envelope: %s", out)
		}
		var got []reconstructedEntry
		for _, state := range issueGroup.States {
			if len(state.Issue) == 0 || len(state.Occurrences) == 0 {
				t.Fatalf("invalid compact state: %+v", state)
			}
			for _, occurrence := range state.Occurrences {
				got = append(got, reconstructedEntry{Issue: state.Issue, occurrence: occurrence})
			}
		}
		if len(got) != len(want) {
			t.Fatalf("compact occurrences=%d, baseline=%d", len(got), len(want))
		}
		dateOf := func(entry reconstructedEntry) time.Time {
			t.Helper()
			var date time.Time
			if err := json.Unmarshal(entry.occurrence.CommitDate, &date); err != nil {
				t.Fatalf("decode CommitDate %s: %v", entry.occurrence.CommitDate, err)
			}
			return date
		}
		sort.Slice(got, func(i, j int) bool {
			a, b := dateOf(got[i]), dateOf(got[j])
			if !a.Equal(b) {
				return a.After(b)
			}
			return got[i].occurrence.CommitHash > got[j].occurrence.CommitHash
		})
		for i := range want {
			g, w := got[i], want[i]
			if string(g.Issue) != string(w.Issue) || g.occurrence.CommitHash != w.occurrence.CommitHash || g.occurrence.Committer != w.occurrence.Committer || string(g.occurrence.CommitDate) != string(w.occurrence.CommitDate) {
				t.Fatalf("reconstructed occurrence %d differs from baseline:\ngot=%+v\nwant=%+v", i, g, w)
			}
		}
	})

	const missingID = "hst-nonexistent999"

	// historyJSON parses commit-history (or --events) JSON into a generic slice.
	historyJSON := func(t *testing.T, args ...string) []map[string]interface{} {
		t.Helper()
		out, err := bdProxiedRun(t, bd, p.dir, append([]string{"history", "--json"}, args...)...)
		if err != nil {
			t.Fatalf("history --json %v: %v\n%s", args, err, out)
		}
		var events bool
		for _, arg := range args {
			if arg == "--events" {
				events = true
				break
			}
		}
		if events {
			var entries []map[string]interface{}
			if err := json.Unmarshal(out, &entries); err != nil {
				t.Fatalf("parse history events JSON: %v\n%s", err, out)
			}
			return entries
		}
		var envelope struct {
			SchemaVersion int `json:"schema_version"`
			Issues        []struct {
				Snapshots []map[string]interface{} `json:"snapshots"`
			} `json:"issues"`
		}
		if err := json.Unmarshal(out, &envelope); err != nil {
			t.Fatalf("parse history JSON envelope: %v\n%s", err, out)
		}
		if envelope.SchemaVersion != 1 || len(envelope.Issues) != 1 {
			t.Fatalf("unexpected history JSON envelope: %#v", envelope)
		}
		return envelope.Issues[0].Snapshots
	}

	// ===== Basic plain-text history =====

	t.Run("basic_history", func(t *testing.T) {
		out, err := bdProxiedRun(t, bd, p.dir, "history", issue.ID)
		if err != nil {
			t.Fatalf("history %s: %v\n%s", issue.ID, err, out)
		}
		s := string(out)
		if !strings.Contains(s, issue.ID) {
			t.Errorf("expected issue ID in history output: %s", s)
		}
		if !strings.Contains(s, "History for") {
			t.Errorf("expected 'History for' header: %s", s)
		}
		if !strings.Contains(s, "Author:") {
			t.Errorf("expected 'Author:' in history output: %s", s)
		}
	})

	t.Run("history_shows_multiple_entries", func(t *testing.T) {
		entries := historyJSON(t, issue.ID)
		// create + 3 updates = at least 4 commits touching this issue.
		if len(entries) < 4 {
			t.Errorf("expected at least 4 history entries, got %d", len(entries))
		}
	})

	// ===== --limit restricts entries =====

	t.Run("limit_restricts_entries", func(t *testing.T) {
		entries := historyJSON(t, issue.ID, "--limit", "2")
		if len(entries) > 2 {
			t.Errorf("expected at most 2 entries with --limit 2, got %d", len(entries))
		}
		if len(entries) == 0 {
			t.Error("expected at least 1 entry")
		}
	})

	t.Run("limit_1", func(t *testing.T) {
		entries := historyJSON(t, issue.ID, "--limit", "1")
		if len(entries) != 1 {
			t.Errorf("expected exactly 1 entry with --limit 1, got %d", len(entries))
		}
	})

	// ===== --events audit output =====

	t.Run("events_json_output", func(t *testing.T) {
		out, err := bdProxiedRun(t, bd, p.dir, "history", issue.ID, "--events", "--json")
		if err != nil {
			t.Fatalf("history --events --json: %v\n%s", err, out)
		}
		var events []types.Event
		if err := json.Unmarshal(out, &events); err != nil {
			t.Fatalf("unmarshal events: %v\n%s", err, out)
		}
		if len(events) == 0 {
			t.Fatal("expected non-empty history events")
		}
		var sawCreated, sawStatus bool
		for _, e := range events {
			if e.IssueID != issue.ID {
				t.Errorf("event for wrong issue: %s", e.IssueID)
			}
			if e.EventType == types.EventCreated {
				sawCreated = true
			}
			if e.EventType == types.EventStatusChanged {
				sawStatus = true
			}
		}
		if !sawCreated {
			t.Errorf("expected a %q event, got %+v", types.EventCreated, events)
		}
		if !sawStatus {
			t.Errorf("expected a %q event, got %+v", types.EventStatusChanged, events)
		}
	})

	t.Run("events_limit", func(t *testing.T) {
		out, err := bdProxiedRun(t, bd, p.dir, "history", issue.ID, "--events", "--limit", "1", "--json")
		if err != nil {
			t.Fatalf("history --events --limit 1 --json: %v\n%s", err, out)
		}
		var events []types.Event
		if err := json.Unmarshal(out, &events); err != nil {
			t.Fatalf("unmarshal events: %v\n%s", err, out)
		}
		if len(events) != 1 {
			t.Errorf("expected exactly 1 event with --limit 1, got %d", len(events))
		}
	})

	// ===== Commit-history --json shape =====

	t.Run("json_output_structure", func(t *testing.T) {
		entries := historyJSON(t, issue.ID)
		if len(entries) == 0 {
			t.Fatal("expected non-empty history")
		}
		e := entries[0]
		for _, key := range []string{"CommitHash", "CommitDate", "Committer", "Issue"} {
			if _, ok := e[key]; !ok {
				t.Errorf("expected %q key in history entry", key)
			}
		}
	})

	t.Run("json_issue_snapshot_has_fields", func(t *testing.T) {
		entries := historyJSON(t, issue.ID)
		if len(entries) == 0 {
			t.Fatal("expected non-empty history")
		}
		// Most recent entry should carry the updated title.
		issueMap, ok := entries[0]["Issue"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected Issue to be a map, got %T", entries[0]["Issue"])
		}
		if issueMap["title"] != "History test issue updated" {
			t.Errorf("expected latest title 'History test issue updated', got %v", issueMap["title"])
		}
	})

	t.Run("bulk_history_uses_proxied_batch_contract", func(t *testing.T) {
		second := bdProxiedCreate(t, bd, p.dir, "Second history issue", "--type", "task")
		cmd := exec.Command(bd, "history", second.ID, missingID, issue.ID, second.ID, "--json")
		cmd.Dir = p.dir
		cmd.Env = bdProxiedEnv(p.dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bulk proxied history: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		var envelope struct {
			SchemaVersion int `json:"schema_version"`
			Issues        []struct {
				IssueID   string            `json:"issue_id"`
				Snapshots []json.RawMessage `json:"snapshots"`
			} `json:"issues"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal bulk proxied history: %v\n%s", err, stdout.String())
		}
		if envelope.SchemaVersion != 1 || len(envelope.Issues) != 3 {
			t.Fatalf("bulk envelope = %#v", envelope)
		}
		for _, group := range envelope.Issues {
			if group.IssueID == missingID && len(group.Snapshots) != 0 {
				t.Fatalf("missing group has snapshots: %#v", group)
			}
		}
	})

	t.Run("commit_history_wrong_issue_guard", func(t *testing.T) {
		entries := historyJSON(t, issue.ID)
		for _, e := range entries {
			if e["CommitHash"] == "" {
				t.Error("entry missing CommitHash")
			}
			if snapshot, ok := e["Issue"].(map[string]interface{}); ok && snapshot["id"] != issue.ID {
				t.Errorf("history entry for wrong issue: %v", snapshot["id"])
			}
		}
	})

	// ===== Nonexistent issue =====

	t.Run("nonexistent_issue_empty_history", func(t *testing.T) {
		out, err := bdProxiedRun(t, bd, p.dir, "history", missingID)
		if err != nil {
			t.Fatalf("history %s: %v\n%s", missingID, err, out)
		}
		if !strings.Contains(string(out), "No history") {
			t.Errorf("expected 'No history' message for nonexistent issue, got: %s", out)
		}
	})

	t.Run("nonexistent_issue_json_returns_empty_snapshots", func(t *testing.T) {
		entries := historyJSON(t, missingID)
		if len(entries) != 0 {
			t.Errorf("expected empty snapshots for nonexistent issue, got %d entries", len(entries))
		}
	})

	t.Run("nonexistent_issue_json_with_limit_returns_empty_snapshots", func(t *testing.T) {
		entries := historyJSON(t, missingID, "--limit", "2")
		if len(entries) != 0 {
			t.Errorf("expected empty snapshots for nonexistent issue with --limit, got %d entries", len(entries))
		}
	})

	// ===== Argument validation =====

	t.Run("no_args_fails", func(t *testing.T) {
		if out, err := bdProxiedRun(t, bd, p.dir, "history"); err == nil {
			t.Fatalf("expected 'history' with no args to fail, got:\n%s", out)
		}
	})

	t.Run("too_many_args_fails", func(t *testing.T) {
		if out, err := bdProxiedRun(t, bd, p.dir, "history", issue.ID, "extra"); err == nil {
			t.Fatalf("expected 'history %s extra' to fail, got:\n%s", issue.ID, out)
		}
	})

	// ===== Newly created issue has history =====

	t.Run("single_entry_for_new_issue", func(t *testing.T) {
		fresh := bdProxiedCreate(t, bd, p.dir, "Fresh issue no updates", "--type", "task")
		entries := historyJSON(t, fresh.ID)
		if len(entries) < 1 {
			t.Error("expected at least 1 history entry for a newly created issue")
		}
	})
}

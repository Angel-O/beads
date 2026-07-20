package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

// goldenPath is the committed consumer contract: one journal line per record, in
// the exact shape `bd mutations tail`/`export` emit. Regenerate with
// BD_UPDATE_GOLDEN=1 go test ./cmd/bd/ -run TestMutationsJournalGolden.
const goldenPath = "testdata/mutations_journal_records.jsonl"

// TestMutationsJournalGolden pins the external record contract for the durable
// mutations journal. It marshals REAL beads types.Issue and MutationDep values
// through the same buildRecord path `bd mutations tail` uses, so the golden
// captures bd's actual field marshaling — issue_type, omitempty elision, and the
// real dependency fields — that external consumers parse. A change to the wire
// shape (a renamed/added/removed field, a lost omitempty) fails this test until
// the golden is regenerated deliberately.
func TestMutationsJournalGolden(t *testing.T) {
	got := renderGoldenLines(t)

	if os.Getenv("BD_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated golden %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (regenerate with BD_UPDATE_GOLDEN=1): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("journal record contract drifted from %s.\n--- got ---\n%s\n--- want ---\n%s\nregenerate with BD_UPDATE_GOLDEN=1 if intended", goldenPath, got, want)
	}
}

// renderGoldenLines builds the fixture records and returns them as JSONL exactly
// as buildRecord + the tail encoder would emit them.
func renderGoldenLines(t *testing.T) []byte {
	t.Helper()
	ts := "2026-01-02 03:04:05" // fixed committed-at string, as CAST(ts AS CHAR) yields
	created := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	closed := time.Date(2026, 1, 2, 3, 30, 0, 0, time.UTC)
	est := 90

	// A minimal open task — the common case; exercises omitempty elision.
	minimal := &types.Issue{
		ID: "bd-100", Title: "wire the seam", Status: types.StatusOpen,
		IssueType: types.TypeTask, Priority: 1, CreatedAt: created, UpdatedAt: updated,
	}
	// A richly populated feature with a real dependency edge, labels, metadata,
	// and an external ref — exercises the full field surface consumers may read.
	full := &types.Issue{
		ID: "bd-101", Title: "durable journal", Description: "append-only record",
		AcceptanceCriteria: "replayable", Status: types.StatusInProgress,
		IssueType: types.TypeFeature, Priority: 0, Assignee: "worker-1",
		Owner: "dev@example.com", EstimatedMinutes: &est, CreatedAt: created,
		CreatedBy: "author", UpdatedAt: updated, ExternalRef: strptr("gh-9"),
		SourceSystem: "github", Metadata: json.RawMessage(`{"k":"v"}`),
		Labels: []string{"infra", "urgent"},
		Dependencies: []*types.Dependency{{
			IssueID: "bd-101", DependsOnID: "bd-100", Type: types.DepBlocks,
			CreatedAt: created, CreatedBy: "author",
		}},
	}
	// A closed issue — exercises close_reason / closed_at marshaling.
	closedIssue := &types.Issue{
		ID: "bd-101", Title: "durable journal", Status: types.StatusClosed,
		IssueType: types.TypeFeature, Priority: 0, CreatedAt: created,
		UpdatedAt: closed, ClosedAt: &closed, CloseReason: "shipped",
	}
	// An ephemeral wisp — exercises the ephemeral/wisp_type fields.
	wisp := &types.Issue{
		ID: "bd-wisp-1", Title: "convoy member", Status: types.StatusOpen,
		IssueType: types.TypeTask, Priority: 2, CreatedAt: created, UpdatedAt: updated,
		Ephemeral: true, WispType: types.WispType("convoy"),
	}

	records := []mutationRecord{
		buildRecord(1, ts, string(issueops.MutationCreate), minimal.ID, mustJSON(t, minimal), ""),
		buildRecord(2, ts, string(issueops.MutationCreate), full.ID, mustJSON(t, full), ""),
		buildRecord(3, ts, string(issueops.MutationDepAdd), "bd-101", mustJSON(t, full),
			mustJSON(t, &issueops.MutationDep{Kind: string(types.DepBlocks), Target: "bd-100"})),
		buildRecord(4, ts, string(issueops.MutationDepRemove), "bd-101", mustJSON(t, full),
			mustJSON(t, &issueops.MutationDep{Kind: string(types.DepBlocks), Target: "bd-100"})),
		buildRecord(5, ts, string(issueops.MutationClose), closedIssue.ID, mustJSON(t, closedIssue), ""),
		buildRecord(6, ts, string(issueops.MutationCreate), wisp.ID, mustJSON(t, wisp), ""),
		buildRecord(7, ts, string(issueops.MutationDelete), "bd-100", "", ""), // null issue on delete
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			t.Fatalf("encode record: %v", err)
		}
	}
	return buf.Bytes()
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	return string(b)
}

func strptr(s string) *string { return &s }

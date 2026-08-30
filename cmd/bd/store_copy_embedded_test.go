//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/types"
)

func TestEmbeddedStoreCopyRoundTripIsAtomicAndIdempotent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded Dolt integration tests")
	}

	ctx := t.Context()
	sourceDir := t.TempDir()
	destinationDir := t.TempDir()
	source, err := embeddeddolt.Open(ctx, sourceDir, "beads", "main")
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer func() { _ = source.Close() }()
	destination, err := embeddeddolt.Open(ctx, destinationDir, "beads", "main")
	if err != nil {
		t.Fatalf("open destination: %v", err)
	}
	defer func() { _ = destination.Close() }()
	if err := source.SetConfig(ctx, "issue_prefix", "src"); err != nil {
		t.Fatalf("set source prefix: %v", err)
	}
	if err := destination.SetConfig(ctx, "issue_prefix", "dst"); err != nil {
		t.Fatalf("set destination prefix: %v", err)
	}
	source.SetEventsJournalEnabled(true)

	created := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	for _, issue := range []*types.Issue{
		{ID: "src-1", Title: "round-trip parent", Status: types.StatusOpen, IssueType: types.TypeTask, Priority: 1, CreatedAt: created, UpdatedAt: created},
		{ID: "src-2", Title: "round-trip child", Status: types.StatusClosed, IssueType: types.TypeTask, Priority: 2, CreatedAt: created, UpdatedAt: created},
	} {
		if err := source.CreateIssue(ctx, issue, "round-trip"); err != nil {
			t.Fatalf("create %s: %v", issue.ID, err)
		}
	}
	if err := source.AddLabel(ctx, "src-1", "source-label", "round-trip"); err != nil {
		t.Fatalf("add label: %v", err)
	}
	if _, err := source.AddIssueComment(ctx, "src-1", "round-trip", "source comment"); err != nil {
		t.Fatalf("add comment: %v", err)
	}
	if err := source.AddDependency(ctx, &types.Dependency{IssueID: "src-1", DependsOnID: "src-2", Type: types.DepBlocks, CreatedAt: created, CreatedBy: "round-trip"}, "round-trip"); err != nil {
		t.Fatalf("add dependency: %v", err)
	}
	ref := "branch-1"
	if _, _, err := source.RecordProvenanceEvent(ctx, types.ProvenanceEvent{
		IssueID: "src-1", Kind: types.ProvCommit, Source: "round-trip-test", Ref: &ref, RefKind: storeCopyStringPtr("branch"), OccurredAt: &created,
	}); err != nil {
		t.Fatalf("record provenance: %v", err)
	}
	interaction := `{"id":"int-source","kind":"tool","created_at":"2026-01-02T03:04:05Z","issue_id":"src-1"}` + "\n"
	if err := os.WriteFile(filepath.Join(sourceDir, "interactions.jsonl"), []byte(interaction), 0o644); err != nil {
		t.Fatalf("write source interactions: %v", err)
	}

	bundle, ids, err := readStoreCopySnapshot(ctx, source, sourceDir, "copy", "repo-a", []string{"copied"})
	if err != nil {
		t.Fatalf("read source snapshot: %v", err)
	}
	beforeSource := append([]byte(nil), mustMarshalStoreCopy(t, bundle)...)
	importer, ok := beads.AsSnapshotImporter(destination)
	if !ok {
		t.Fatal("destination does not expose SnapshotImporter")
	}
	request := beads.SnapshotImportRequest{Bundle: bundle, IDs: ids, Mode: beads.SnapshotCreateOnly}
	first, err := importer.ImportSnapshot(ctx, request)
	if err != nil {
		t.Fatalf("first ImportSnapshot: %v", err)
	}
	if !first.Applied || first.IssuesImported != 2 || first.HistoryImported == 0 || first.EventsImported == 0 || first.ProvenanceImported != 1 {
		t.Fatalf("first import result = %+v", first)
	}
	if err := installStoreCopyInteractions(destinationDir, first.StagedAuditJSONL); err != nil {
		t.Fatalf("install interactions: %v", err)
	}

	parentID := ids.Issues["src-1"]
	childID := ids.Issues["src-2"]
	parent, err := destination.GetIssue(ctx, parentID)
	if err != nil {
		t.Fatalf("read destination parent: %v", err)
	}
	if parent.Title != "round-trip parent" || parent.Status != types.StatusOpen {
		t.Fatalf("destination parent = %+v", parent)
	}
	labels, err := destination.GetLabels(ctx, parentID)
	if err != nil || !containsStoreCopy(labels, "source-label") || !containsStoreCopy(labels, "copied") {
		t.Fatalf("destination labels = %v, err=%v", labels, err)
	}
	dependencies, err := destination.GetDependencyRecords(ctx, parentID)
	if err != nil || len(dependencies) != 1 || dependencies[0].DependsOnID != childID {
		t.Fatalf("destination dependencies = %+v, err=%v", dependencies, err)
	}
	comments, err := destination.GetIssueComments(ctx, parentID)
	if err != nil || len(comments) != 1 || comments[0].Text != "source comment" {
		t.Fatalf("destination comments = %+v, err=%v", comments, err)
	}
	history, err := destination.ReadEventsJournal(ctx, 0, 0)
	if err != nil || len(history) == 0 || history[0].IssueID != parentID && history[0].IssueID != childID {
		t.Fatalf("destination history = %+v, err=%v", history, err)
	}
	events, err := destination.GetEvents(ctx, parentID, 0)
	if err != nil || len(events) == 0 {
		t.Fatalf("destination events = %+v, err=%v", events, err)
	}
	provenance, err := destination.GetProvenanceEvents(ctx, parentID, "")
	if err != nil || len(provenance) != 1 || provenance[0].Ref == nil || *provenance[0].Ref != ref {
		t.Fatalf("destination provenance = %+v, err=%v", provenance, err)
	}
	destinationInteractions, err := os.ReadFile(filepath.Join(destinationDir, "interactions.jsonl"))
	if err != nil {
		t.Fatalf("read destination interactions: %v", err)
	}
	if !strings.Contains(string(destinationInteractions), `"id":"`+storeCopyID("copy", "repo-a", "interaction", "int-source")+`"`) || !strings.Contains(string(destinationInteractions), `"issue_id":"`+parentID+`"`) {
		t.Fatalf("destination interactions = %s", destinationInteractions)
	}
	info, err := os.Stat(filepath.Join(destinationDir, "interactions.jsonl"))
	if err != nil {
		t.Fatalf("stat destination interactions: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("destination interaction mode = %o, want 600", got)
	}

	second, err := importer.ImportSnapshot(ctx, request)
	if err != nil {
		t.Fatalf("repeated ImportSnapshot: %v", err)
	}
	if second.Applied || second.IssuesImported != 0 || second.HistoryImported != 0 || second.EventsImported != 0 || second.ProvenanceImported != 0 {
		t.Fatalf("repeated import result = %+v", second)
	}
	if err := installStoreCopyInteractions(destinationDir, second.StagedAuditJSONL); err != nil {
		t.Fatalf("reinstall interactions: %v", err)
	}

	afterBundle, _, err := readStoreCopySnapshot(ctx, source, sourceDir, "copy", "repo-a", []string{"copied"})
	if err != nil {
		t.Fatalf("read source after copy: %v", err)
	}
	if after := mustMarshalStoreCopy(t, afterBundle); !reflect.DeepEqual(beforeSource, after) {
		t.Fatalf("source changed during copy\nbefore: %s\nafter:  %s", beforeSource, after)
	}
}

func mustMarshalStoreCopy(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal store-copy state: %v", err)
	}
	return raw
}

func containsStoreCopy(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads"
	"github.com/steveyegge/beads/internal/storage"
	storageissueops "github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

type storeCopySourceStub struct {
	issues       []*types.Issue
	labels       map[string][]string
	dependencies map[string][]*types.Dependency
	comments     map[string][]*types.Comment
	history      []storage.EventsJournalRow
	events       []*types.Event
	provenance   map[string][]types.ProvenanceEvent
}

func (s *storeCopySourceStub) SearchIssues(context.Context, string, types.IssueFilter) ([]*types.Issue, error) {
	return s.issues, nil
}

func (s *storeCopySourceStub) GetLabelsForIssues(context.Context, []string) (map[string][]string, error) {
	return s.labels, nil
}

func (s *storeCopySourceStub) GetDependencyRecordsForIssues(context.Context, []string) (map[string][]*types.Dependency, error) {
	return s.dependencies, nil
}

func (s *storeCopySourceStub) GetCommentsForIssues(context.Context, []string) (map[string][]*types.Comment, error) {
	return s.comments, nil
}

func (s *storeCopySourceStub) ReadEventsJournal(context.Context, int64, int) ([]storage.EventsJournalRow, error) {
	return s.history, nil
}

func (s *storeCopySourceStub) GetAllEventsSince(context.Context, time.Time) ([]*types.Event, error) {
	return s.events, nil
}

func (s *storeCopySourceStub) GetProvenanceEvents(_ context.Context, issueID, _ string) ([]types.ProvenanceEvent, error) {
	return s.provenance[issueID], nil
}

func TestReadStoreCopySnapshotPreservesAndRemapsFullState(t *testing.T) {
	created := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	issue := &types.Issue{ID: "src-1", Title: "copy me", Status: types.StatusOpen, IssueType: types.TypeTask, Priority: 2, CreatedAt: created, UpdatedAt: created}
	issue2 := &types.Issue{ID: "src-2", Title: "dependency target", Status: types.StatusOpen, IssueType: types.TypeTask, Priority: 2, CreatedAt: created, UpdatedAt: created}
	dep := &types.Dependency{IssueID: "src-1", DependsOnID: "src-2", Type: types.DepBlocks, CreatedAt: created, CreatedBy: "alice"}
	comment := &types.Comment{ID: "comment-1", IssueID: "src-1", Author: "alice", Text: "keep this", CreatedAt: created}
	event := &types.Event{ID: "event-1", IssueID: "src-1", EventType: types.EventUpdated, Actor: "alice", CreatedAt: created}
	prov := types.ProvenanceEvent{IssueID: "src-1", Kind: types.ProvCommit, Source: "test", Ref: storeCopyStringPtr("abc"), RefKind: storeCopyStringPtr("branch"), CreatedAt: created}
	history := storage.EventsJournalRow{Seq: 1, TS: created.Format(time.RFC3339), Op: "update", IssueID: "src-1", IssueJSON: `{"id":"src-1"}`}

	source := &storeCopySourceStub{
		issues:       []*types.Issue{issue, issue2},
		labels:       map[string][]string{"src-1": {"existing"}},
		dependencies: map[string][]*types.Dependency{"src-1": {dep}},
		comments:     map[string][]*types.Comment{"src-1": {comment}},
		history:      []storage.EventsJournalRow{history},
		events:       []*types.Event{event},
		provenance:   map[string][]types.ProvenanceEvent{"src-1": {prov}},
	}
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "interactions.jsonl"), []byte(strings.Join([]string{
		`{"id":"int-1","kind":"tool","created_at":"2026-01-02T03:04:05Z","issue_id":"src-1"}`,
		`{"id":"int-deleted","kind":"tool","created_at":"2026-01-02T03:04:05Z","issue_id":"src-deleted"}`,
		"",
	}, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	ids := beads.SnapshotIDMap{
		Issues:            map[string]string{"src-1": "copy-8c2", "src-2": "copy-k91"},
		AuditInteractions: make(map[string]string),
	}
	bundle, err := readStoreCopySnapshot(t.Context(), source, sourceDir, "copy", "repo-a", []string{"copied"}, source.issues, ids)
	if err != nil {
		t.Fatalf("readStoreCopySnapshot: %v", err)
	}
	if got := ids.Issues["src-1"]; !regexp.MustCompile(`^copy-[0-9a-z]{3,8}$`).MatchString(got) {
		t.Fatalf("issue mapping = %q, want ordinary short hash ID", got)
	}
	if got := ids.AuditInteractions["int-1"]; got != storeCopyID("copy", "repo-a", "interaction", "int-1") {
		t.Fatalf("interaction mapping = %q, want deterministic namespace ID", got)
	}
	if _, copied := ids.AuditInteractions["int-deleted"]; copied || strings.Contains(string(bundle.AuditInteractionsJSONL), "int-deleted") {
		t.Fatal("interaction for deleted issue was copied")
	}
	if issue.ID != "src-1" || issue.Labels != nil || issue.Dependencies != nil || issue.Comments != nil {
		t.Fatal("source issue was mutated while building the snapshot")
	}

	normalized, result, err := storageissueops.PrepareSnapshotImport(beads.SnapshotImportRequest{Bundle: bundle, IDs: ids, Mode: beads.SnapshotCreateOnly})
	if err != nil {
		t.Fatalf("PrepareSnapshotImport: %v", err)
	}
	got := normalized.Bundle.Issues[0]
	if got.ID != ids.Issues["src-1"] || got.Dependencies[0].DependsOnID != ids.Issues["src-2"] || got.Comments[0].IssueID != got.ID {
		t.Fatalf("remapped issue aggregate = %+v", got)
	}
	if !strings.Contains(string(result.StagedAuditJSONL), `"id":"`+storeCopyID("copy", "repo-a", "interaction", "int-1")+`"`) || !strings.Contains(string(result.StagedAuditJSONL), `"issue_id":"`+ids.Issues["src-1"]+`"`) {
		t.Fatalf("staged interactions were not remapped: %s", result.StagedAuditJSONL)
	}
}

func TestInstallStoreCopyInteractionsIsIdempotentAndDetectsCollision(t *testing.T) {
	destination := t.TempDir()
	path := filepath.Join(destination, "interactions.jsonl")
	if err := os.WriteFile(path, []byte(`{"id":"local","kind":"local"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	staged := []byte(`{"id":"copied","kind":"tool"}` + "\n")
	if err := installStoreCopyInteractions(destination, staged); err != nil {
		t.Fatalf("first install: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("interaction mode = %o, want 600", got)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := installStoreCopyInteractions(destination, staged); err != nil {
		t.Fatalf("replay install: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || !strings.Contains(string(second), `"id":"local"`) {
		t.Fatalf("replay changed interactions: %s", second)
	}
	err = installStoreCopyInteractions(destination, []byte(`{"id":"copied","kind":"different"}`+"\n"))
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("collision error = %v", err)
	}
}

func storeCopyStringPtr(value string) *string { return &value }

func TestStoreCopyContextAndInteractionIDsAreNamespaceScoped(t *testing.T) {
	mapA := storeCopyMapKey("copy", "repo:a")
	mapB := storeCopyMapKey("copy", "repo:b")
	interaction := storeCopyID("copy", "repo:a", "interaction", "same-id")
	if mapA == mapB || !strings.HasPrefix(mapA, "store-copy/id-map/") || strings.Contains(mapA, "repo:a") || !strings.HasPrefix(interaction, "copy-") {
		t.Fatalf("store-copy identities are not namespace scoped: mapA=%q mapB=%q interaction=%q", mapA, mapB, interaction)
	}
}

func TestCanonicalStoreCopyPathAndContainment(t *testing.T) {
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(child, link); err != nil {
		t.Fatal(err)
	}
	canonicalChild, err := canonicalStoreCopyPath(link)
	if err != nil {
		t.Fatalf("canonicalStoreCopyPath: %v", err)
	}
	canonicalParent, err := canonicalStoreCopyPath(parent)
	if err != nil {
		t.Fatalf("canonicalStoreCopyPath: %v", err)
	}
	canonicalRealChild, err := canonicalStoreCopyPath(child)
	if err != nil {
		t.Fatalf("canonicalStoreCopyPath: %v", err)
	}
	if canonicalChild != canonicalRealChild || !storeCopyContains(canonicalParent, canonicalChild) || storeCopyContains(canonicalChild, canonicalParent) {
		t.Fatalf("canonical paths/containment incorrect: parent=%q child=%q", canonicalParent, canonicalChild)
	}
}

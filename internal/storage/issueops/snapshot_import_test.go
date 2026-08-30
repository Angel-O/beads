package issueops

import (
	"bytes"
	"strings"
	"testing"
	"time"

	publicops "github.com/steveyegge/beads/issueops"
	"github.com/steveyegge/beads/journalops"
)

func TestPrepareSnapshotImportRemapsAndCanonicalizesWithoutMutating(t *testing.T) {
	when := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	issue := &publicops.Issue{
		ID: "src-one", Title: "one", Status: publicops.StatusOpen,
		IssueType: "task", Priority: 2, CreatedAt: when, UpdatedAt: when,
		Target:       "src-two",
		Dependencies: []*publicops.Dependency{{IssueID: "src-one", DependsOnID: "src-two", Type: "blocks"}},
		Comments:     []*publicops.Comment{{ID: "comment-one", Author: "alice", Text: "hello", CreatedAt: when}},
	}
	request := publicops.SnapshotImportRequest{
		Mode: publicops.SnapshotReplace,
		IDs: publicops.SnapshotIDMap{
			Issues:            map[string]string{"src-one": "dst-one", "src-two": "dst-two"},
			AuditInteractions: map[string]string{"int-1": "dst-int-1", "int-2": "dst-int-2"},
		},
		Bundle: publicops.SnapshotImportBundle{
			Issues: []*publicops.Issue{issue, {
				ID: "src-two", Title: "two", Status: publicops.StatusOpen,
				IssueType: "task", Priority: 2, CreatedAt: when, UpdatedAt: when,
			}},
			History: []journalops.Row{{
				Seq: 1, TS: when.Format(time.RFC3339Nano), Op: "create", IssueID: "src-one",
				IssueJSON: `{"id":"src-one","title":"one","target":"src-two","dependencies":[{"id":"source-dep","issue_id":"src-one","depends_on_id":"src-two","type":"blocks"}]}`,
			}},
			AuditInteractionsJSONL: []byte(strings.Join([]string{
				`{"id":"int-2","kind":"tool","created_at":"2026-08-29T12:00:02Z","issue_id":"src-one","target":"src-two"}`,
				`{"id":"int-1","kind":"llm","created_at":"2026-08-29T12:00:01Z","issue_id":"src-one"}`,
			}, "\n")),
			MigrationMarker: "source-v1",
		},
	}

	normalized, result, err := PrepareSnapshotImport(request)
	if err != nil {
		t.Fatalf("PrepareSnapshotImport: %v", err)
	}
	if result.Digest == "" || result.Applied {
		t.Fatalf("unexpected result: %+v", result)
	}
	if got := request.Bundle.Issues[0].ID; got != "src-one" {
		t.Fatalf("prepare mutated caller issue ID to %q", got)
	}
	if len(result.StagedAuditInteractions) != 2 {
		t.Fatalf("staged audit records = %d, want 2", len(result.StagedAuditInteractions))
	}
	if !bytes.Contains(result.StagedAuditJSONL, []byte(`"id":"dst-int-1"`)) || !bytes.Contains(result.StagedAuditJSONL, []byte(`"issue_id":"dst-one"`)) || !bytes.Contains(result.StagedAuditJSONL, []byte(`"target":"dst-two"`)) {
		t.Fatalf("staged sidecar was not remapped: %s", result.StagedAuditJSONL)
	}
	if !strings.Contains(normalized.Bundle.History[0].IssueJSON, `"id":"dst-one"`) {
		t.Fatalf("history issue was not remapped: %s", normalized.Bundle.History[0].IssueJSON)
	}
	if !strings.Contains(normalized.Bundle.History[0].IssueJSON, `"target":"dst-two"`) || !strings.Contains(normalized.Bundle.History[0].IssueJSON, `"depends_on_id":"dst-two"`) {
		t.Fatalf("history issue references were not remapped: %s", normalized.Bundle.History[0].IssueJSON)
	}
	if normalized.Bundle.Issues[0].Target != "dst-two" {
		t.Fatalf("issue target = %q, want dst-two", normalized.Bundle.Issues[0].Target)
	}

	_, resultAgain, err := PrepareSnapshotImport(request)
	if err != nil {
		t.Fatalf("second PrepareSnapshotImport: %v", err)
	}
	if result.Digest != resultAgain.Digest {
		t.Fatalf("digest changed on repeat: %s != %s", result.Digest, resultAgain.Digest)
	}
}

func TestPrepareSnapshotImportRejectsUnmappedAuditAndDependencyIDs(t *testing.T) {
	when := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	base := publicops.SnapshotImportRequest{
		Mode: publicops.SnapshotCreateOnly,
		IDs:  publicops.SnapshotIDMap{Issues: map[string]string{"src": "dst"}},
		Bundle: publicops.SnapshotImportBundle{Issues: []*publicops.Issue{{
			ID: "src", Title: "source", Status: publicops.StatusOpen,
			IssueType: "task", Priority: 2, CreatedAt: when, UpdatedAt: when,
		}}, MigrationMarker: "marker"},
	}
	base.Bundle.Issues[0].Dependencies = []*publicops.Dependency{{IssueID: "src", DependsOnID: "missing", Type: "blocks"}}
	if _, _, err := PrepareSnapshotImport(base); err == nil || !strings.Contains(err.Error(), "dependency target") {
		t.Fatalf("unmapped dependency error = %v", err)
	}
	base.Bundle.Issues[0].Dependencies = nil
	base.Bundle.AuditInteractionsJSONL = []byte(`{"id":"int-1","kind":"tool","created_at":"2026-08-29T12:00:00Z","issue_id":"src"}`)
	if _, _, err := PrepareSnapshotImport(base); err == nil || !strings.Contains(err.Error(), "audit interaction") {
		t.Fatalf("unmapped audit error = %v", err)
	}
	base.Bundle.AuditInteractionsJSONL = nil
	base.Bundle.History = []journalops.Row{{
		Seq: 1, TS: when.Format(time.RFC3339Nano), Op: "dep_add", IssueID: "src",
		DepJSON: `{"kind":"blocks","target":"unknown-source-id"}`,
	}}
	if _, _, err := PrepareSnapshotImport(base); err == nil || !strings.Contains(err.Error(), "history dependency") {
		t.Fatalf("unmapped durable-history error = %v", err)
	}
}

func TestPrepareSnapshotImportRequiresOwnedDependenciesAndSortsHistory(t *testing.T) {
	when := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	base := publicops.SnapshotImportRequest{
		Mode: publicops.SnapshotCreateOnly,
		IDs:  publicops.SnapshotIDMap{Issues: map[string]string{"src-one": "dst-one", "src-two": "dst-two"}},
		Bundle: publicops.SnapshotImportBundle{Issues: []*publicops.Issue{
			{ID: "src-one", Title: "one", Status: publicops.StatusOpen, IssueType: "task", Priority: 2, CreatedAt: when, UpdatedAt: when},
			{ID: "src-two", Title: "two", Status: publicops.StatusOpen, IssueType: "task", Priority: 2, CreatedAt: when, UpdatedAt: when},
		}, MigrationMarker: "marker"},
	}
	base.Bundle.Issues[0].Dependencies = []*publicops.Dependency{{IssueID: "src-two", DependsOnID: "src-two", Type: publicops.DepBlocks}}
	if _, _, err := PrepareSnapshotImport(base); err == nil || !strings.Contains(err.Error(), "dependency owner") {
		t.Fatalf("mismatched dependency owner error = %v", err)
	}

	base.Bundle.Issues[0].Dependencies = nil
	base.Bundle.History = []journalops.Row{
		{Seq: 2, TS: when.Format(time.RFC3339Nano), Op: "update", IssueID: "src-two"},
		{Seq: 1, TS: when.Format(time.RFC3339Nano), Op: "create", IssueID: "src-one"},
	}
	normalized, _, err := PrepareSnapshotImport(base)
	if err != nil {
		t.Fatalf("PrepareSnapshotImport: %v", err)
	}
	if normalized.Bundle.History[0].Seq != 1 || normalized.Bundle.History[1].Seq != 2 {
		t.Fatalf("history order = %+v, want source sequence order", normalized.Bundle.History)
	}

	base.Mode = publicops.SnapshotReplace
	_, replaceResult, err := PrepareSnapshotImport(base)
	if err != nil {
		t.Fatalf("PrepareSnapshotImport replace: %v", err)
	}
	base.Mode = publicops.SnapshotCreateOnly
	_, createResult, err := PrepareSnapshotImport(base)
	if err != nil {
		t.Fatalf("PrepareSnapshotImport create-only: %v", err)
	}
	if replaceResult.Digest == createResult.Digest {
		t.Fatal("create-only and replacement requests share a digest")
	}

	reordered := base
	reordered.Mode = publicops.SnapshotReplace
	reordered.Bundle.Issues = []*publicops.Issue{reordered.Bundle.Issues[1], reordered.Bundle.Issues[0]}
	_, reorderedResult, err := PrepareSnapshotImport(reordered)
	if err != nil {
		t.Fatalf("PrepareSnapshotImport reordered: %v", err)
	}
	if replaceResult.Digest != reorderedResult.Digest {
		t.Fatalf("reordered request digest = %s, want %s", reorderedResult.Digest, replaceResult.Digest)
	}
}

func TestPrepareSnapshotImportDigestIncludesHiddenIssueStateAndPlane(t *testing.T) {
	base := hiddenStateSnapshotRequest("hash-a", "repo-a", false)
	_, baseResult, err := PrepareSnapshotImport(base)
	if err != nil {
		t.Fatalf("base PrepareSnapshotImport: %v", err)
	}
	for name, request := range map[string]publicops.SnapshotImportRequest{
		"content hash":  hiddenStateSnapshotRequest("hash-b", "repo-a", false),
		"source repo":   hiddenStateSnapshotRequest("hash-a", "repo-b", false),
		"storage plane": hiddenStateSnapshotRequest("hash-a", "repo-a", true),
	} {
		_, result, err := PrepareSnapshotImport(request)
		if err != nil {
			t.Fatalf("%s PrepareSnapshotImport: %v", name, err)
		}
		if result.Digest == baseResult.Digest {
			t.Errorf("%s did not change canonical digest %s", name, result.Digest)
		}
	}
}

func hiddenStateSnapshotRequest(contentHash, sourceRepo string, wisp bool) publicops.SnapshotImportRequest {
	when := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	return publicops.SnapshotImportRequest{
		Mode: publicops.SnapshotReplace,
		IDs:  publicops.SnapshotIDMap{Issues: map[string]string{"source": "destination"}},
		Bundle: publicops.SnapshotImportBundle{
			Issues: []*publicops.Issue{{
				ID: "source", Title: "source", Status: publicops.StatusOpen, IssueType: "task", Priority: 2,
				CreatedAt: when, UpdatedAt: when, ContentHash: contentHash, SourceRepo: sourceRepo,
				WispPlaneOverride: &wisp,
			}},
			MigrationMarker: "hidden-state",
		},
	}
}

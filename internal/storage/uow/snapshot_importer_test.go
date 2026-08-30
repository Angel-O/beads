package uow

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
	"github.com/steveyegge/beads/journalops"
)

func TestSnapshotImporterCopiesDatabaseStateAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	provider := newUOWRoleFixtureProvider(t, ctx, "snap")
	source, ok := provider.(SnapshotImporterSource)
	if !ok {
		t.Fatalf("provider %T does not offer SnapshotImporter", provider)
	}
	importer, err := source.SnapshotImporter()
	if err != nil {
		t.Fatalf("SnapshotImporter: %v", err)
	}
	when := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	marker := "viewer-migration-1"
	request := snapshotRequest(when, marker, publicops.SnapshotReplace)

	result, err := importer.ImportSnapshot(ctx, request)
	if err != nil {
		t.Fatalf("ImportSnapshot: %v", err)
	}
	if !result.Applied || result.IssuesImported != 2 || result.HistoryImported != 1 || result.EventsImported != 1 || result.ProvenanceImported != 1 {
		t.Fatalf("unexpected import result: %+v", result)
	}
	if !strings.Contains(string(result.StagedAuditJSONL), `"issue_id":"snap-one"`) || !strings.Contains(string(result.StagedAuditJSONL), `"id":"snap-int"`) {
		t.Fatalf("staged audit payload was not remapped: %s", result.StagedAuditJSONL)
	}
	kit := newUOWRoleFixtureKit(provider, "snap")
	if err := kit.CreateIssue(ctx, &types.Issue{ID: "snap-unrelated", Title: "unrelated", Status: types.StatusOpen, IssueType: types.TypeTask, Priority: 2, CreatedAt: when, UpdatedAt: when}, "seed"); err != nil {
		t.Fatalf("seed unrelated issue: %v", err)
	}
	if err := RunTx(ctx, provider, func(ctx context.Context, uw UnitOfWork) (string, error) {
		current, err := uw.RawSQLUseCase().Query(ctx, "SELECT next_seq FROM bd_events_seq WHERE id = 0")
		if err != nil || len(current.Rows) != 1 {
			return "", fmt.Errorf("read journal sequence: %v", err)
		}
		var seq int64
		if err := scanRawSQLValue(&seq, current.Rows[0][0]); err != nil {
			return "", err
		}
		if _, err := uw.RawSQLUseCase().Exec(ctx, "UPDATE bd_events_seq SET next_seq = next_seq + 1 WHERE id = 0"); err != nil {
			return "", err
		}
		_, err = uw.RawSQLUseCase().Exec(ctx, "INSERT INTO bd_events_journal (seq, ts, op, issue_id, actor, issue_json) VALUES (?, ?, ?, ?, ?, ?)", seq+1, when, "update", "snap-unrelated", "seed", `{"id":"snap-unrelated","notes":"unrelated history"}`)
		return "seed unrelated history", err
	}); err != nil {
		t.Fatalf("write unrelated history: %v", err)
	}

	query := newUOWRoleFixtureKit(provider, "snap").QueryScalar
	var count int
	if err := query(ctx, "SELECT COUNT(*) FROM issues WHERE id IN ('snap-one','snap-two')", nil, &count); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if count != 2 {
		t.Fatalf("imported issue count = %d, want 2", count)
	}
	if err := query(ctx, "SELECT COUNT(*) FROM metadata WHERE `key` = ? AND value LIKE ?", []any{publicops.SnapshotImportMarkerKey(result.Digest), "%" + marker + "%"}, &count); err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if count != 1 {
		t.Fatalf("migration marker count = %d, want 1", count)
	}
	resultAgain, err := importer.ImportSnapshot(ctx, request)
	if err != nil {
		t.Fatalf("exact repeat ImportSnapshot: %v", err)
	}
	if resultAgain.Applied || resultAgain.Digest != result.Digest || resultAgain.IssuesImported != 0 {
		t.Fatalf("exact repeat result = %+v, want an unapplied no-op with the same digest", resultAgain)
	}
	reorderedRequest := request
	reorderedRequest.Bundle.Issues = []*publicops.Issue{reorderedRequest.Bundle.Issues[1], reorderedRequest.Bundle.Issues[0]}
	reordered, err := importer.ImportSnapshot(ctx, reorderedRequest)
	if err != nil {
		t.Fatalf("reordered repeat ImportSnapshot: %v", err)
	}
	if reordered.Applied || reordered.Digest != result.Digest {
		t.Fatalf("reordered repeat result = %+v, want the same verified no-op", reordered)
	}
	var title string
	conflictingProvenance := snapshotRequest(when, "provenance-conflict", publicops.SnapshotReplace)
	conflictingProvenance.Bundle.Provenance[0].Payload = stringPtr(`{"commit":"different"}`)
	if _, err := importer.ImportSnapshot(ctx, conflictingProvenance); err == nil || !strings.Contains(err.Error(), "provenance deterministic ID") {
		t.Fatalf("conflicting provenance error = %v, want deterministic-ID conflict", err)
	}
	if err := query(ctx, "SELECT title FROM issues WHERE id = 'snap-one'", nil, &title); err != nil {
		t.Fatalf("read issue after provenance conflict: %v", err)
	}
	if title != "one" {
		t.Fatalf("provenance conflict mutated issue title to %q", title)
	}

	createOnly := snapshotRequest(when, "create-only-conflict", publicops.SnapshotCreateOnly)
	if _, err := importer.ImportSnapshot(ctx, createOnly); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("create-only conflict error = %v, want destination collision", err)
	}

	replacement := snapshotRequest(when, "replacement", publicops.SnapshotReplace)
	replacement.Bundle.Issues[0].Title = "replaced"
	replacement.Bundle.Issues[0].Labels = []string{"replacement"}
	replacement.Bundle.Issues[0].Comments = []*publicops.Comment{{ID: "replacement-comment", Author: "bob", Text: "new", CreatedAt: when}}
	if _, err := importer.ImportSnapshot(ctx, replacement); err != nil {
		t.Fatalf("replacement ImportSnapshot: %v", err)
	}
	if err := query(ctx, "SELECT title FROM issues WHERE id = 'snap-one'", nil, &title); err != nil {
		t.Fatalf("read replaced title: %v", err)
	}
	if title != "replaced" {
		t.Fatalf("replaced title = %q, want replaced", title)
	}
	if err := query(ctx, "SELECT COUNT(*) FROM labels WHERE issue_id = 'snap-one' AND label = 'copied'", nil, &count); err != nil {
		t.Fatalf("count removed label: %v", err)
	}
	if count != 0 {
		t.Fatalf("old label count after replacement = %d, want 0", count)
	}
	if err := query(ctx, "SELECT COUNT(*) FROM comments WHERE issue_id = 'snap-one'", nil, &count); err != nil {
		t.Fatalf("count replacement comments: %v", err)
	}
	if count != 1 {
		t.Fatalf("replacement comment count = %d, want 1", count)
	}

	if err := kit.CreateIssue(ctx, &types.Issue{ID: "snap-dependent", Title: "dependent", Status: types.StatusOpen, IssueType: types.TypeTask, Priority: 2, CreatedAt: when, UpdatedAt: when}, "seed"); err != nil {
		t.Fatalf("seed status dependent: %v", err)
	}
	if err := kit.CreateIssue(ctx, &types.Issue{ID: "snap-child", Title: "child", Status: types.StatusOpen, IssueType: types.TypeTask, Priority: 2, CreatedAt: when, UpdatedAt: when}, "seed"); err != nil {
		t.Fatalf("seed edge dependent: %v", err)
	}
	if err := kit.AddDependency(ctx, &types.Dependency{IssueID: "snap-dependent", DependsOnID: "snap-one", Type: types.DepBlocks, CreatedAt: when}, "seed"); err != nil {
		t.Fatalf("seed status dependency: %v", err)
	}
	if err := kit.AddDependency(ctx, &types.Dependency{IssueID: "snap-child", DependsOnID: "snap-two", Type: types.DepParentChild, CreatedAt: when}, "seed"); err != nil {
		t.Fatalf("seed edge dependency: %v", err)
	}
	if err := query(ctx, "SELECT is_blocked FROM issues WHERE id = 'snap-dependent'", nil, &count); err != nil {
		t.Fatalf("read initial status-dependent block: %v", err)
	}
	if count != 1 {
		t.Fatalf("initial status-dependent blocked = %d, want 1", count)
	}

	replacement.Bundle.Issues[0].Status = types.StatusClosed
	replacement.Bundle.Issues[0].ClosedAt = timePtr(when)
	if _, err := importer.ImportSnapshot(ctx, replacement); err != nil {
		t.Fatalf("status replacement ImportSnapshot: %v", err)
	}
	if err := query(ctx, "SELECT is_blocked FROM issues WHERE id = 'snap-dependent'", nil, &count); err != nil {
		t.Fatalf("read status-dependent block after replacement: %v", err)
	}
	if count != 0 {
		t.Fatalf("status-dependent blocked after replacement = %d, want 0", count)
	}

	edgeReplacement := snapshotRequest(when, "edge-replacement", publicops.SnapshotReplace)
	edgeReplacement.Bundle.Issues[1].Dependencies = nil
	if _, err := importer.ImportSnapshot(ctx, edgeReplacement); err != nil {
		t.Fatalf("edge replacement ImportSnapshot: %v", err)
	}
	if err := query(ctx, "SELECT is_blocked FROM issues WHERE id = 'snap-child'", nil, &count); err != nil {
		t.Fatalf("read edge-dependent block after replacement: %v", err)
	}
	if count != 0 {
		t.Fatalf("edge-dependent blocked after replacement = %d, want 0", count)
	}

	drifted, err := importer.ImportSnapshot(ctx, request)
	if err != nil {
		t.Fatalf("drifted-marker ImportSnapshot: %v", err)
	}
	if !drifted.Applied || drifted.Digest != result.Digest {
		t.Fatalf("drifted-marker result = %+v, want a re-application with the same digest", drifted)
	}
	driftedAgain, err := importer.ImportSnapshot(ctx, request)
	if err != nil {
		t.Fatalf("repeated drift repair ImportSnapshot: %v", err)
	}
	if driftedAgain.Applied || driftedAgain.Digest != result.Digest {
		t.Fatalf("repeated drift repair result = %+v, want a verified no-op", driftedAgain)
	}
	if err := query(ctx, "SELECT COUNT(*) FROM bd_events_journal WHERE issue_id = 'snap-one'", nil, &count); err != nil {
		t.Fatalf("count imported history after drift repair: %v", err)
	}
	if count != 1 {
		t.Fatalf("imported history count after drift repair = %d, want 1", count)
	}
	var repairedIssueJSON, repairedDepJSON string
	if err := query(ctx, "SELECT issue_json, dep_json FROM bd_events_journal WHERE issue_id = 'snap-one'", nil, &repairedIssueJSON, &repairedDepJSON); err != nil {
		t.Fatalf("read repaired history: %v", err)
	}
	if !strings.Contains(repairedIssueJSON, `"id":"snap-one"`) || !strings.Contains(repairedDepJSON, `"target":"snap-two"`) {
		t.Fatalf("repaired history contents = issue %q dep %q", repairedIssueJSON, repairedDepJSON)
	}
	if err := query(ctx, "SELECT COUNT(*) FROM bd_events_journal WHERE issue_id = 'snap-unrelated'", nil, &count); err != nil {
		t.Fatalf("count unrelated history after drift repair: %v", err)
	}
	if count != 1 {
		t.Fatalf("unrelated history count after drift repair = %d, want 1", count)
	}

	var description, assignee, createdBy, owner, externalRef, specID string
	var createdAt, updatedAt, startedAt time.Time
	if err := query(ctx, "SELECT description, assignee, created_by, owner, external_ref, spec_id, created_at, updated_at, started_at FROM issues WHERE id = 'snap-one'", nil, &description, &assignee, &createdBy, &owner, &externalRef, &specID, &createdAt, &updatedAt, &startedAt); err != nil {
		t.Fatalf("read full issue row: %v", err)
	}
	if description != "full description" || assignee != "alice" || createdBy != "importer" || owner != "owner@example.com" || externalRef != "gh-123" || specID != "spec-1" || !createdAt.Equal(when) || !updatedAt.Equal(when) || !startedAt.Equal(when) {
		t.Fatalf("full issue row lost fidelity: description=%q assignee=%q created_by=%q owner=%q external_ref=%q spec_id=%q created_at=%v updated_at=%v started_at=%v", description, assignee, createdBy, owner, externalRef, specID, createdAt, updatedAt, startedAt)
	}

	var depCreatedBy, depMetadata, depThread string
	var depCreatedAt time.Time
	if err := query(ctx, "SELECT created_by, metadata, thread_id, created_at FROM dependencies WHERE issue_id = 'snap-two' AND depends_on_issue_id = 'snap-one'", nil, &depCreatedBy, &depMetadata, &depThread, &depCreatedAt); err != nil {
		t.Fatalf("read dependency fidelity: %v", err)
	}
	if depCreatedBy != "alice" || depMetadata != `{"edge":"copied"}` || depThread != "thread-1" || !depCreatedAt.Equal(when) {
		t.Fatalf("dependency lost fidelity: created_by=%q metadata=%q thread=%q created_at=%v", depCreatedBy, depMetadata, depThread, depCreatedAt)
	}

	var commentAuthor, commentText string
	var commentCreatedAt time.Time
	if err := query(ctx, "SELECT author, text, created_at FROM comments WHERE id = 'snap-comment'", nil, &commentAuthor, &commentText, &commentCreatedAt); err != nil {
		t.Fatalf("read comment fidelity: %v", err)
	}
	if commentAuthor != "alice" || commentText != "copied" || !commentCreatedAt.Equal(when) {
		t.Fatalf("comment lost fidelity: author=%q text=%q created_at=%v", commentAuthor, commentText, commentCreatedAt)
	}

	var eventActor, eventOld, eventNew, eventComment string
	if err := query(ctx, "SELECT actor, old_value, new_value, comment FROM events WHERE id = 'snap-event'", nil, &eventActor, &eventOld, &eventNew, &eventComment); err != nil {
		t.Fatalf("read event fidelity: %v", err)
	}
	if eventActor != "alice" || eventOld != "old" || eventNew != "new" || eventComment != "copied" {
		t.Fatalf("event lost fidelity: actor=%q old=%q new=%q comment=%q", eventActor, eventOld, eventNew, eventComment)
	}

	var journalSeq int64
	var journalIssueJSON, journalDepJSON string
	if err := query(ctx, "SELECT seq, issue_json, dep_json FROM bd_events_journal WHERE issue_id = 'snap-one' ORDER BY seq LIMIT 1", nil, &journalSeq, &journalIssueJSON, &journalDepJSON); err != nil {
		t.Fatalf("read journal fidelity: %v", err)
	}
	if journalSeq <= 0 || !strings.Contains(journalIssueJSON, `"id":"snap-one"`) || !strings.Contains(journalDepJSON, `"target":"snap-two"`) {
		t.Fatalf("journal lost sequence or remapped payload: seq=%d issue=%q dep=%q", journalSeq, journalIssueJSON, journalDepJSON)
	}

	var provenanceRef, provenancePayload string
	var occurredAt, provenanceCreatedAt time.Time
	if err := query(ctx, "SELECT ref, payload, occurred_at, created_at FROM provenance_events WHERE issue_id = 'snap-one'", nil, &provenanceRef, &provenancePayload, &occurredAt, &provenanceCreatedAt); err != nil {
		t.Fatalf("read provenance fidelity: %v", err)
	}
	if provenanceRef != "abc" || provenancePayload != `{"commit":"abc"}` || !occurredAt.Equal(when) || !provenanceCreatedAt.Equal(when) {
		t.Fatalf("provenance lost fidelity: ref=%q payload=%q occurred_at=%v created_at=%v", provenanceRef, provenancePayload, occurredAt, provenanceCreatedAt)
	}

	var markerValue string
	if err := query(ctx, "SELECT value FROM metadata WHERE `key` = ?", []any{publicops.SnapshotImportMarkerKey(result.Digest)}, &markerValue); err != nil {
		t.Fatalf("read stateful marker: %v", err)
	}
	if !strings.Contains(markerValue, "state_digest") {
		t.Fatalf("marker does not record destination state: %q", markerValue)
	}
}

func TestSnapshotImporterReplacementScopesHistoryByIssue(t *testing.T) {
	ctx := context.Background()
	provider := newUOWRoleFixtureProvider(t, ctx, "snapoverlap")
	importer, err := provider.(SnapshotImporterSource).SnapshotImporter()
	if err != nil {
		t.Fatalf("SnapshotImporter: %v", err)
	}
	when := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	ab := snapshotRequest(when, "a-b", publicops.SnapshotReplace)
	ab.Bundle.History = append(ab.Bundle.History, journalops.Row{
		Seq: 8, TS: when.Add(time.Second).Format(time.RFC3339Nano), Op: "update", IssueID: "source-two", Actor: "alice",
		IssueJSON: `{"id":"source-two","title":"two"}`, DepJSON: `{"target":"source-one"}`, CommentJSON: `{"id":"b-comment"}`,
	})
	if _, err := importer.ImportSnapshot(ctx, ab); err != nil {
		t.Fatalf("A+B ImportSnapshot: %v", err)
	}

	query := newUOWRoleFixtureKit(provider, "snapoverlap").QueryScalar
	var beforeSeq int64
	var beforeTS, beforeOp, beforeIssueID, beforeActor, beforeIssueJSON, beforeDepJSON, beforeCommentJSON string
	if err := query(ctx, "SELECT seq, ts, op, issue_id, actor, issue_json, dep_json, comment_json FROM bd_events_journal WHERE issue_id = 'snap-two'", nil, &beforeSeq, &beforeTS, &beforeOp, &beforeIssueID, &beforeActor, &beforeIssueJSON, &beforeDepJSON, &beforeCommentJSON); err != nil {
		t.Fatalf("read B history before A-only replacement: %v", err)
	}

	aOnly := ab
	aOnly.Bundle.Issues = []*publicops.Issue{ab.Bundle.Issues[0]}
	aOnly.Bundle.History = []journalops.Row{ab.Bundle.History[0]}
	aOnly.Bundle.MigrationMarker = "a-only"
	aOnly.Bundle.Issues[0].Title = "A-only replacement"
	if _, err := importer.ImportSnapshot(ctx, aOnly); err != nil {
		t.Fatalf("A-only replacement ImportSnapshot: %v", err)
	}

	var afterSeq int64
	var afterTS, afterOp, afterIssueID, afterActor, afterIssueJSON, afterDepJSON, afterCommentJSON string
	if err := query(ctx, "SELECT seq, ts, op, issue_id, actor, issue_json, dep_json, comment_json FROM bd_events_journal WHERE issue_id = 'snap-two'", nil, &afterSeq, &afterTS, &afterOp, &afterIssueID, &afterActor, &afterIssueJSON, &afterDepJSON, &afterCommentJSON); err != nil {
		t.Fatalf("read B history after A-only replacement: %v", err)
	}
	if beforeSeq != afterSeq || beforeTS != afterTS || beforeOp != afterOp || beforeIssueID != afterIssueID || beforeActor != afterActor || beforeIssueJSON != afterIssueJSON || beforeDepJSON != afterDepJSON || beforeCommentJSON != afterCommentJSON {
		t.Fatalf("B history changed after A-only replacement: before=(%d,%q,%q,%q,%q,%q,%q,%q) after=(%d,%q,%q,%q,%q,%q,%q,%q)", beforeSeq, beforeTS, beforeOp, beforeIssueID, beforeActor, beforeIssueJSON, beforeDepJSON, beforeCommentJSON, afterSeq, afterTS, afterOp, afterIssueID, afterActor, afterIssueJSON, afterDepJSON, afterCommentJSON)
	}
}

func TestSnapshotImporterCreateOnlyReplayAndCrossModeConflict(t *testing.T) {
	ctx := context.Background()
	when := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)

	replayProvider := newUOWRoleFixtureProvider(t, ctx, "snapreplay")
	replayImporter, err := replayProvider.(SnapshotImporterSource).SnapshotImporter()
	if err != nil {
		t.Fatalf("create-only SnapshotImporter: %v", err)
	}
	createOnly := snapshotRequest(when, "create-only-replay", publicops.SnapshotCreateOnly)
	if result, err := replayImporter.ImportSnapshot(ctx, createOnly); err != nil || !result.Applied {
		t.Fatalf("first create-only import: result=%+v err=%v", result, err)
	}
	result, err := replayImporter.ImportSnapshot(ctx, createOnly)
	if err != nil {
		t.Fatalf("exact create-only replay: %v", err)
	}
	if result.Applied || result.IssuesImported != 0 {
		t.Fatalf("exact create-only replay result = %+v, want verified no-op", result)
	}

	crossProvider := newUOWRoleFixtureProvider(t, ctx, "snapcross")
	crossImporter, err := crossProvider.(SnapshotImporterSource).SnapshotImporter()
	if err != nil {
		t.Fatalf("replacement SnapshotImporter: %v", err)
	}
	replacement := snapshotRequest(when, "cross-mode", publicops.SnapshotReplace)
	if _, err := crossImporter.ImportSnapshot(ctx, replacement); err != nil {
		t.Fatalf("replacement import: %v", err)
	}
	replacement.Mode = publicops.SnapshotCreateOnly
	if _, err := crossImporter.ImportSnapshot(ctx, replacement); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("create-only replay after replacement error = %v, want existing-ID rejection", err)
	}
}

func TestSnapshotImporterHiddenIssueStateDriftInvalidatesNoOp(t *testing.T) {
	ctx := context.Background()
	provider := newUOWRoleFixtureProvider(t, ctx, "snaphidden")
	importer, err := provider.(SnapshotImporterSource).SnapshotImporter()
	if err != nil {
		t.Fatalf("SnapshotImporter: %v", err)
	}
	when := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	request := snapshotRequest(when, "hidden-state-drift", publicops.SnapshotReplace)
	request.Bundle.Issues[0].ContentHash = "hash-a"
	request.Bundle.Issues[0].SourceRepo = "repo-a"
	if _, err := importer.ImportSnapshot(ctx, request); err != nil {
		t.Fatalf("initial import: %v", err)
	}

	mutate := func(query string, args ...any) {
		t.Helper()
		if err := RunTx(ctx, provider, func(ctx context.Context, uw UnitOfWork) (string, error) {
			_, err := uw.RawSQLUseCase().Exec(ctx, query, args...)
			return "drift hidden issue state", err
		}); err != nil {
			t.Fatalf("mutate hidden issue state: %v", err)
		}
	}
	mutate("UPDATE issues SET content_hash = ? WHERE id = ?", "hash-drift", "snap-one")
	var contentHash string
	if err := newUOWRoleFixtureKit(provider, "snaphidden").QueryScalar(ctx, "SELECT content_hash FROM issues WHERE id = ?", []any{"snap-one"}, &contentHash); err != nil {
		t.Fatalf("read content hash drift: %v", err)
	}
	if contentHash != "hash-drift" {
		t.Fatalf("content hash drift was not persisted: %q", contentHash)
	}
	contentHashRepair, err := importer.ImportSnapshot(ctx, request)
	if err != nil {
		t.Fatalf("content hash drift repair: %v", err)
	}
	if !contentHashRepair.Applied {
		t.Fatal("content hash drift was incorrectly treated as a no-op")
	}

	mutate("UPDATE issues SET source_repo = ? WHERE id = ?", "repo-drift", "snap-one")
	sourceRepoRepair, err := importer.ImportSnapshot(ctx, request)
	if err != nil {
		t.Fatalf("source repo drift repair: %v", err)
	}
	if !sourceRepoRepair.Applied {
		t.Fatal("source repo drift was incorrectly treated as a no-op")
	}
}

func TestSnapshotImporterRollsBackAuxiliaryFailure(t *testing.T) {
	ctx := context.Background()
	provider := newUOWRoleFixtureProvider(t, ctx, "snaprb")
	source := provider.(SnapshotImporterSource)
	importer, err := source.SnapshotImporter()
	if err != nil {
		t.Fatalf("SnapshotImporter: %v", err)
	}
	when := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	request := snapshotRequest(when, "rollback", publicops.SnapshotCreateOnly)
	request.Bundle.Issues[0].Comments = []*publicops.Comment{
		{ID: "same-comment", Author: "a", Text: "one", CreatedAt: when},
		{ID: "same-comment", Author: "b", Text: "two", CreatedAt: when.Add(time.Second)},
	}
	if _, err := importer.ImportSnapshot(ctx, request); err == nil {
		t.Fatal("ImportSnapshot with duplicate comment IDs succeeded")
	}
	query := newUOWRoleFixtureKit(provider, "snaprb").QueryScalar
	var count int
	if err := query(ctx, "SELECT COUNT(*) FROM issues WHERE id = 'snaprb-one'", nil, &count); err != nil {
		t.Fatalf("count rolled-back issue: %v", err)
	}
	if count != 0 {
		t.Fatalf("rolled-back issue count = %d, want 0", count)
	}
}

func snapshotRequest(when time.Time, marker string, mode publicops.SnapshotImportMode) publicops.SnapshotImportRequest {
	return publicops.SnapshotImportRequest{
		Mode: mode,
		IDs: publicops.SnapshotIDMap{
			Issues:            map[string]string{"source-one": "snap-one", "source-two": "snap-two"},
			AuditInteractions: map[string]string{"source-int": "snap-int"},
		},
		Bundle: publicops.SnapshotImportBundle{
			Issues: []*publicops.Issue{
				{ID: "source-one", Title: "one", Description: "full description", Assignee: "alice", CreatedBy: "importer", Owner: "owner@example.com", ExternalRef: stringPtr("gh-123"), SpecID: "spec-1", StartedAt: timePtr(when), Status: publicops.StatusOpen, IssueType: types.TypeTask, Priority: 2, CreatedAt: when, UpdatedAt: when,
					Labels: []string{"copied"}, Comments: []*publicops.Comment{{ID: "snap-comment", Author: "alice", Text: "copied", CreatedAt: when}}},
				{ID: "source-two", Title: "two", Status: publicops.StatusOpen, IssueType: types.TypeTask, Priority: 2, CreatedAt: when, UpdatedAt: when,
					Dependencies: []*publicops.Dependency{{IssueID: "source-two", DependsOnID: "source-one", Type: publicops.DepBlocks, CreatedAt: when, CreatedBy: "alice", Metadata: `{"edge":"copied"}`, ThreadID: "thread-1"}}},
			},
			History:                []journalops.Row{{Seq: 7, TS: when.Format(time.RFC3339Nano), Op: "create", IssueID: "source-one", Actor: "alice", IssueJSON: `{"id":"source-one","title":"one"}`, DepJSON: `{"target":"source-two"}`}},
			Events:                 []*publicops.Event{{ID: "snap-event", IssueID: "source-one", EventType: "created", Actor: "alice", OldValue: stringPtr("old"), NewValue: stringPtr("new"), Comment: stringPtr("copied"), CreatedAt: when}},
			Provenance:             []publicops.ProvenanceEvent{{IssueID: "source-one", Kind: types.ProvCommit, Source: "viewer", Ref: stringPtr("abc"), RefKind: stringPtr("branch"), Payload: stringPtr(`{"commit":"abc"}`), OccurredAt: timePtr(when), CreatedAt: when}},
			AuditInteractionsJSONL: []byte(`{"id":"source-int","kind":"tool","created_at":"2026-08-29T12:00:00Z","issue_id":"source-one"}`),
			MigrationMarker:        marker,
		},
	}
}

func stringPtr(value string) *string { return &value }

func timePtr(value time.Time) *time.Time { return &value }

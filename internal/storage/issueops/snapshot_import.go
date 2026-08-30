package issueops

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/depid"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
	"github.com/steveyegge/beads/journalops"
)

// PrepareSnapshotImport parses and normalizes the caller-owned bundle without
// opening a database transaction. It is deliberately separate from applying the
// plan so a caller can treat the returned sidecar as staged output only.
func PrepareSnapshotImport(request publicops.SnapshotImportRequest) (publicops.SnapshotImportRequest, publicops.SnapshotImportResult, error) {
	request.Bundle.Issues = cloneSnapshotIssues(request.Bundle.Issues)
	request.Bundle.Events = cloneSnapshotEvents(request.Bundle.Events)
	request.Bundle.History = append([]journalops.Row(nil), request.Bundle.History...)
	request.Bundle.Provenance = append([]types.ProvenanceEvent(nil), request.Bundle.Provenance...)

	if request.Mode != publicops.SnapshotCreateOnly && request.Mode != publicops.SnapshotReplace {
		return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: invalid mode %q", request.Mode)
	}
	if strings.TrimSpace(request.Bundle.MigrationMarker) == "" {
		return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: migration marker is required")
	}
	if len(request.Bundle.Issues) == 0 {
		return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: at least one issue is required")
	}

	seenSource := make(map[string]struct{}, len(request.Bundle.Issues))
	seenDestination := make(map[string]struct{}, len(request.Bundle.Issues))
	seenHistorySeq := make(map[int64]struct{}, len(request.Bundle.History))
	seenComments := make(map[string]struct{})
	seenEvents := make(map[string]struct{}, len(request.Bundle.Events))
	seenProvenance := make(map[string]struct{}, len(request.Bundle.Provenance))
	seenDependencies := make(map[string]struct{})
	for _, issue := range request.Bundle.Issues {
		if issue == nil || strings.TrimSpace(issue.ID) == "" {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: every issue requires a source ID")
		}
		if _, ok := seenSource[issue.ID]; ok {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: duplicate source issue ID %q", issue.ID)
		}
		seenSource[issue.ID] = struct{}{}
		destination, ok := request.IDs.Issues[issue.ID]
		if !ok || strings.TrimSpace(destination) == "" {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: no destination ID for source issue %q", issue.ID)
		}
		if _, ok := seenDestination[destination]; ok {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: destination issue ID %q is assigned more than once", destination)
		}
		seenDestination[destination] = struct{}{}
		issue.ID = destination
		if issue.Target != "" {
			mappedTarget, err := remapIssueReference(issue.Target, request.IDs.Issues)
			if err != nil {
				return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: issue %q target: %w", destination, err)
			}
			issue.Target = mappedTarget
		}
		if issue.CreatedAt.IsZero() || issue.UpdatedAt.IsZero() {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: issue %q requires created_at and updated_at", destination)
		}
		if err := issue.ValidateWithCustom(nil, nil); err != nil {
			// Custom status/type validation belongs to the destination transaction,
			// but structural validation must happen before any write.
			if strings.Contains(err.Error(), "invalid status") || strings.Contains(err.Error(), "invalid issue type") {
				continue
			}
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: validate issue %q: %w", destination, err)
		}
	}
	if request.IDMapMetadataKey != "" {
		if err := validateSnapshotIssueMap(request.IDs.Issues, seenSource, ""); err != nil {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: ID map: %w", err)
		}
	}

	for _, issue := range request.Bundle.Issues {
		seenLabels := make(map[string]struct{}, len(issue.Labels))
		for _, label := range issue.Labels {
			if _, ok := seenLabels[label]; ok {
				return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: issue %q contains duplicate label %q", issue.ID, label)
			}
			seenLabels[label] = struct{}{}
		}
		for _, dep := range issue.Dependencies {
			if dep == nil {
				return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: issue %q contains a nil dependency", issue.ID)
			}
			if dep.Type == "" || !dep.Type.IsValid() {
				return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: issue %q contains invalid dependency type %q", issue.ID, dep.Type)
			}
			depKey := strings.Join([]string{issue.ID, dep.DependsOnID, string(dep.Type)}, "\x00")
			if _, ok := seenDependencies[depKey]; ok {
				return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: duplicate dependency %q -> %q (%s)", issue.ID, dep.DependsOnID, dep.Type)
			}
			if dep.IssueID == "" {
				dep.IssueID = issue.ID
			} else {
				mapped, ok := request.IDs.Issues[dep.IssueID]
				if !ok || mapped != issue.ID {
					return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: dependency owner %q does not match enclosing issue %q", dep.IssueID, issue.ID)
				}
				dep.IssueID = mapped
			}
			if strings.HasPrefix(dep.DependsOnID, "external:") {
				if strings.TrimSpace(dep.DependsOnID) == "external:" {
					return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: issue %q contains an empty external dependency target", issue.ID)
				}
				seenDependencies[depKey] = struct{}{}
				continue
			}
			mapped, ok := request.IDs.Issues[dep.DependsOnID]
			if !ok {
				return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: no destination ID for dependency target %q", dep.DependsOnID)
			}
			if _, ok := seenSource[dep.DependsOnID]; !ok {
				return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: dependency target %q is not in the bundled issue set", dep.DependsOnID)
			}
			dep.DependsOnID = mapped
			depKey = strings.Join([]string{dep.IssueID, dep.DependsOnID, string(dep.Type)}, "\x00")
			if _, ok := seenDependencies[depKey]; ok {
				return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: duplicate dependency %q -> %q (%s)", dep.IssueID, dep.DependsOnID, dep.Type)
			}
			seenDependencies[depKey] = struct{}{}
		}
		for _, comment := range issue.Comments {
			if comment == nil || comment.ID == "" || comment.CreatedAt.IsZero() {
				return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: issue %q contains an invalid comment", issue.ID)
			}
			if strings.TrimSpace(comment.Author) == "" || strings.TrimSpace(comment.Text) == "" {
				return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: issue %q contains a blank comment", issue.ID)
			}
			if _, ok := seenComments[comment.ID]; ok {
				return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: duplicate comment ID %q", comment.ID)
			}
			seenComments[comment.ID] = struct{}{}
			comment.IssueID = issue.ID
		}
		for i := range issue.Labels {
			if err := types.CheckFieldLen("label", issue.Labels[i]); err != nil {
				return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: issue %q: %w", issue.ID, err)
			}
		}
	}

	for i := range request.Bundle.Events {
		event := request.Bundle.Events[i]
		if event == nil || event.ID == "" || event.CreatedAt.IsZero() {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: every durable event requires id and created_at")
		}
		if _, ok := seenEvents[event.ID]; ok {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: duplicate durable event ID %q", event.ID)
		}
		seenEvents[event.ID] = struct{}{}
		if _, ok := seenSource[event.IssueID]; !ok {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: event issue %q is not in the bundled issue set", event.IssueID)
		}
		mapped, ok := request.IDs.Issues[event.IssueID]
		if !ok {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: no destination ID for event issue %q", event.IssueID)
		}
		event.IssueID = mapped
	}

	for i := range request.Bundle.Provenance {
		ev := &request.Bundle.Provenance[i]
		if _, ok := seenSource[ev.IssueID]; !ok {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: provenance issue %q is not in the bundled issue set", ev.IssueID)
		}
		mapped, ok := request.IDs.Issues[ev.IssueID]
		if !ok {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: no destination ID for provenance issue %q", ev.IssueID)
		}
		ev.IssueID = mapped
		if ev.CreatedAt.IsZero() {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: provenance event %q requires created_at", ev.Kind)
		}
		if err := ValidateProvenanceEvent(*ev); err != nil {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: validate provenance: %w", err)
		}
		id := ProvenanceEventID(*ev)
		if _, ok := seenProvenance[id]; ok {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: duplicate provenance event ID %q", id)
		}
		seenProvenance[id] = struct{}{}
	}

	for i := range request.Bundle.History {
		if request.Bundle.History[i].Seq <= 0 || request.Bundle.History[i].IssueID == "" || strings.TrimSpace(request.Bundle.History[i].TS) == "" {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: history rows require positive seq and issue_id")
		}
		if _, ok := seenSource[request.Bundle.History[i].IssueID]; !ok {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: history issue %q is not in the bundled issue set", request.Bundle.History[i].IssueID)
		}
		if _, ok := seenHistorySeq[request.Bundle.History[i].Seq]; ok {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: duplicate history seq %d", request.Bundle.History[i].Seq)
		}
		seenHistorySeq[request.Bundle.History[i].Seq] = struct{}{}
		if _, err := parseSnapshotTimestamp(request.Bundle.History[i].TS); err != nil {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: invalid history timestamp %q: %w", request.Bundle.History[i].TS, err)
		}
		mapped, ok := request.IDs.Issues[request.Bundle.History[i].IssueID]
		if !ok {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: no destination ID for history issue %q", request.Bundle.History[i].IssueID)
		}
		request.Bundle.History[i].IssueID = mapped
		var err error
		request.Bundle.History[i].IssueJSON, err = remapHistoryIssueJSON(request.Bundle.History[i].IssueJSON, request.IDs.Issues)
		if err != nil {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, err
		}
		request.Bundle.History[i].DepJSON, err = remapHistoryDepJSON(request.Bundle.History[i].DepJSON, request.IDs.Issues)
		if err != nil {
			return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, err
		}
	}

	audit, auditJSONL, err := prepareAuditInteractions(request.Bundle.AuditInteractionsJSONL, request.IDs)
	if err != nil {
		return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, err
	}
	request.Bundle.AuditInteractionsJSONL = auditJSONL
	sort.Slice(request.Bundle.History, func(i, j int) bool { return request.Bundle.History[i].Seq < request.Bundle.History[j].Seq })
	canonical := canonicalSnapshot(request, audit)
	digestBytes, err := json.Marshal(canonical)
	if err != nil {
		return publicops.SnapshotImportRequest{}, publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: canonicalize bundle: %w", err)
	}
	digest := sha256.Sum256(digestBytes)
	result := publicops.SnapshotImportResult{
		Digest:                  hex.EncodeToString(digest[:]),
		MigrationMarker:         request.Bundle.MigrationMarker,
		StagedAuditInteractions: cloneRawMessages(audit),
		StagedAuditJSONL:        append([]byte(nil), auditJSONL...),
	}
	return request, result, nil
}

// PlanSnapshotIDsInTx loads a committed source-to-destination map or generates
// one with the same adaptive hash and collision retries used by issue creation.
func PlanSnapshotIDsInTx(ctx context.Context, tx DBTX, request publicops.SnapshotIDPlanRequest) (publicops.SnapshotIDPlan, error) {
	if strings.TrimSpace(request.Prefix) == "" || strings.TrimSpace(request.Actor) == "" || strings.TrimSpace(request.MetadataKey) == "" {
		return publicops.SnapshotIDPlan{}, fmt.Errorf("snapshot ID plan: prefix, actor, and metadata key are required")
	}
	issues := cloneSnapshotIssues(request.Issues)
	if len(issues) == 0 {
		return publicops.SnapshotIDPlan{}, fmt.Errorf("snapshot ID plan: at least one source issue is required")
	}
	seen := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		if issue == nil || strings.TrimSpace(issue.ID) == "" {
			return publicops.SnapshotIDPlan{}, fmt.Errorf("snapshot ID plan: every source issue requires an ID")
		}
		if _, exists := seen[issue.ID]; exists {
			return publicops.SnapshotIDPlan{}, fmt.Errorf("snapshot ID plan: duplicate source issue ID %q", issue.ID)
		}
		seen[issue.ID] = struct{}{}
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].ID < issues[j].ID })
	raw, err := GetMetadataInTx(ctx, tx, request.MetadataKey)
	if err != nil {
		return publicops.SnapshotIDPlan{}, fmt.Errorf("snapshot ID plan: read persisted map: %w", err)
	}
	if raw != "" {
		var ids map[string]string
		if err := json.Unmarshal([]byte(raw), &ids); err != nil {
			return publicops.SnapshotIDPlan{}, fmt.Errorf("snapshot ID plan: decode persisted map: %w", err)
		}
		if err := validateSnapshotIssueMap(ids, seen, request.Prefix); err != nil {
			return publicops.SnapshotIDPlan{}, fmt.Errorf("snapshot ID plan: persisted map: %w", err)
		}
		return publicops.SnapshotIDPlan{Issues: ids, Persisted: true}, nil
	}

	ids := make(map[string]string, len(issues))
	reserved := make(map[string]struct{}, len(issues))
	rows, err := tx.QueryContext(ctx, "SELECT id FROM issues UNION SELECT id FROM wisps")
	if err != nil {
		return publicops.SnapshotIDPlan{}, fmt.Errorf("snapshot ID plan: read destination IDs: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return publicops.SnapshotIDPlan{}, fmt.Errorf("snapshot ID plan: scan destination ID: %w", err)
		}
		reserved[id] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return publicops.SnapshotIDPlan{}, fmt.Errorf("snapshot ID plan: read destination IDs: %w", err)
	}
	for _, issue := range issues {
		table, _ := TableRouting(issue)
		id, err := GenerateIssueIDInTableAvoiding(ctx, tx, table, request.Prefix, issue, request.Actor, reserved)
		if err != nil {
			return publicops.SnapshotIDPlan{}, fmt.Errorf("snapshot ID plan: generate ID for %q: %w", issue.ID, err)
		}
		ids[issue.ID] = id
		reserved[id] = struct{}{}
	}
	return publicops.SnapshotIDPlan{Issues: ids}, nil
}

// ApplySnapshotImportInTx applies a prepared snapshot to one transaction. It
// never writes the audit interaction sidecar; callers receive that payload from
// PrepareSnapshotImport and install it in their own recoverable logical commit.
func ApplySnapshotImportInTx(ctx context.Context, tx DBTX, request publicops.SnapshotImportRequest, result publicops.SnapshotImportResult) (publicops.SnapshotImportResult, error) {
	mapValue, err := snapshotIssueMapValue(request)
	if err != nil {
		return publicops.SnapshotImportResult{}, err
	}
	if request.IDMapMetadataKey != "" {
		existing, err := GetMetadataInTx(ctx, tx, request.IDMapMetadataKey)
		if err != nil {
			return publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: read ID map: %w", err)
		}
		if existing != "" {
			var persisted map[string]string
			if err := json.Unmarshal([]byte(existing), &persisted); err != nil {
				return publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: decode persisted ID map: %w", err)
			}
			if !equalSnapshotIssueMaps(persisted, request.IDs.Issues) {
				return publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: persisted ID map conflicts with request")
			}
		}
	}
	markerKey := publicops.SnapshotImportMarkerKey(result.Digest)
	var recorded snapshotMarker
	var marker string
	if err := tx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE `key` = ?", markerKey).Scan(&marker); err == nil {
		if json.Unmarshal([]byte(marker), &recorded) == nil && recorded.Mode == request.Mode && recorded.MigrationMarker == request.Bundle.MigrationMarker && recorded.StateDigest != "" {
			current, err := snapshotDestinationDigest(ctx, tx, request)
			if err != nil {
				return publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: verify idempotency marker: %w", err)
			}
			if current == recorded.StateDigest {
				result.Applied = false
				result.IssuesImported = 0
				result.HistoryImported = 0
				result.EventsImported = 0
				result.ProvenanceImported = 0
				return result, nil
			}
		}
	} else if err != sql.ErrNoRows {
		return publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: read idempotency marker: %w", err)
	}

	// A marker from a prior replacement is not permission to turn this
	// create-only request into an upsert. Check conflicts only after an exact
	// same-mode marker has been verified as a no-op.
	if request.Mode == publicops.SnapshotCreateOnly {
		if err := checkSnapshotConflicts(ctx, tx, request); err != nil {
			return publicops.SnapshotImportResult{}, err
		}
	}
	if request.Mode == publicops.SnapshotReplace {
		if err := checkSnapshotConflicts(ctx, tx, request); err != nil {
			return publicops.SnapshotImportResult{}, err
		}
	}
	if err := preflightSnapshotImport(ctx, tx, request); err != nil {
		return publicops.SnapshotImportResult{}, err
	}
	affectedIssues, affectedWisps, err := collectSnapshotAffectedIDs(ctx, tx, request, nil, nil)
	if err != nil {
		return publicops.SnapshotImportResult{}, err
	}
	if request.Mode == publicops.SnapshotReplace {
		historySeqs, err := importedSnapshotHistorySeqs(ctx, tx, request)
		if err != nil {
			return publicops.SnapshotImportResult{}, err
		}
		if err := removeImportedSnapshotHistory(ctx, tx, historySeqs); err != nil {
			return publicops.SnapshotImportResult{}, err
		}
	}
	if request.Mode == publicops.SnapshotReplace {
		if err := removeSnapshotAggregates(ctx, tx, request); err != nil {
			return publicops.SnapshotImportResult{}, err
		}
	}

	ctx = WithEventsJournal(ctx, false)
	batchContext, err := NewBatchContext(ctx, tx, storage.BatchCreateOptions{})
	if err != nil {
		return publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: read destination validation context: %w", err)
	}
	for _, issue := range request.Bundle.Issues {
		if err := ValidateMetadataIfConfigured(issue.Metadata); err != nil {
			return publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: validate metadata for %q: %w", issue.ID, err)
		}
		if err := issue.ValidateWithCustom(batchContext.CustomStatuses, batchContext.CustomTypes); err != nil {
			return publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: validate issue %q: %w", issue.ID, err)
		}
		if issue.ContentHash == "" {
			issue.ContentHash = issue.ComputeContentHash()
		}
		table, _ := TableRouting(issue)
		var err error
		if request.Mode == publicops.SnapshotCreateOnly {
			err = InsertIssueStrictInTx(ctx, tx, table, issue)
		} else {
			err = ReplaceIssueIntoTable(ctx, tx, table, issue)
		}
		if err != nil {
			return publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: write issue %q: %w", issue.ID, err)
		}
		if err := insertSnapshotLabels(ctx, tx, issue); err != nil {
			return publicops.SnapshotImportResult{}, err
		}
		if err := insertSnapshotComments(ctx, tx, issue); err != nil {
			return publicops.SnapshotImportResult{}, err
		}
	}

	if err := insertSnapshotDependencies(ctx, tx, request.Bundle.Issues); err != nil {
		return publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: write dependencies: %w", err)
	}
	affectedIssues, affectedWisps, err = collectSnapshotAffectedIDs(ctx, tx, request, affectedIssues, affectedWisps)
	if err != nil {
		return publicops.SnapshotImportResult{}, err
	}
	if err := recomputeSnapshotBlockedState(ctx, tx, request.Bundle.Issues, affectedIssues, affectedWisps); err != nil {
		return publicops.SnapshotImportResult{}, err
	}
	if err := clearGeneratedSnapshotEvents(ctx, tx, request.Bundle.Issues); err != nil {
		return publicops.SnapshotImportResult{}, err
	}
	if err := insertSnapshotEvents(ctx, tx, request.Bundle.Events); err != nil {
		return publicops.SnapshotImportResult{}, err
	}
	historyOwnership, err := insertSnapshotHistory(ctx, tx, request.Bundle.History)
	if err != nil {
		return publicops.SnapshotImportResult{}, err
	}
	provenanceImported := 0
	for _, ev := range request.Bundle.Provenance {
		inserted, err := InsertImportedProvenanceEventInTx(ctx, tx, ev)
		if err != nil {
			return publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: write provenance: %w", err)
		}
		if inserted {
			provenanceImported++
		}
	}
	stateDigest, err := snapshotDestinationDigest(ctx, tx, request)
	if err != nil {
		return publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: fingerprint destination state: %w", err)
	}
	markerIssueIDs := make([]string, 0, len(request.Bundle.Issues))
	for _, issue := range request.Bundle.Issues {
		markerIssueIDs = append(markerIssueIDs, issue.ID)
	}
	sort.Strings(markerIssueIDs)
	markerValue, err := json.Marshal(snapshotMarker{Mode: request.Mode, MigrationMarker: request.Bundle.MigrationMarker, StateDigest: stateDigest, IssueIDs: markerIssueIDs, History: historyOwnership})
	if err != nil {
		return publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: encode migration marker: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "REPLACE INTO metadata (`key`, value) VALUES (?, ?)", markerKey, markerValue); err != nil {
		return publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: write migration marker: %w", err)
	}
	if request.IDMapMetadataKey != "" {
		if err := SetMetadataInTx(ctx, tx, request.IDMapMetadataKey, mapValue); err != nil {
			return publicops.SnapshotImportResult{}, fmt.Errorf("snapshot import: write ID map: %w", err)
		}
	}

	result.Applied = true
	result.IssuesImported = len(request.Bundle.Issues)
	result.HistoryImported = len(request.Bundle.History)
	result.EventsImported = len(request.Bundle.Events)
	result.ProvenanceImported = provenanceImported
	return result, nil
}

func snapshotIssueMapValue(request publicops.SnapshotImportRequest) (string, error) {
	if request.IDMapMetadataKey == "" {
		return "", nil
	}
	if strings.TrimSpace(request.IDMapMetadataKey) == "" {
		return "", fmt.Errorf("snapshot import: ID map metadata key must not be blank")
	}
	raw, err := json.Marshal(request.IDs.Issues)
	if err != nil {
		return "", fmt.Errorf("snapshot import: encode ID map: %w", err)
	}
	return string(raw), nil
}

func validateSnapshotIssueMap(ids map[string]string, sources map[string]struct{}, prefix string) error {
	if len(ids) != len(sources) {
		return fmt.Errorf("contains %d entries for %d source issues", len(ids), len(sources))
	}
	destinations := make(map[string]string, len(ids))
	for source := range sources {
		destination, ok := ids[source]
		if !ok || strings.TrimSpace(destination) == "" {
			return fmt.Errorf("no destination ID for source issue %q", source)
		}
		if prefix != "" && !strings.HasPrefix(destination, prefix+"-") {
			return fmt.Errorf("destination ID %q does not use prefix %q", destination, prefix)
		}
		if previous, exists := destinations[destination]; exists {
			return fmt.Errorf("source issues %q and %q share destination ID %q", previous, source, destination)
		}
		destinations[destination] = source
	}
	return nil
}

func equalSnapshotIssueMaps(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for source, destination := range left {
		if right[source] != destination {
			return false
		}
	}
	return true
}

// SnapshotChangedTables is the durable table set an explicit snapshot may
// write. Wisp tables are intentionally filtered by ChangedTables.Add because
// they are clone-local and excluded from Dolt version commits.
func SnapshotChangedTables() ChangedTables {
	tables := ChangedTables{}
	tables.Add("issues", "labels", "comments", "dependencies", "events", "bd_events_journal", "provenance_events", "metadata")
	return tables
}

func collectSnapshotAffectedIDs(ctx context.Context, tx DBTX, request publicops.SnapshotImportRequest, issueIDs, wispIDs []string) ([]string, []string, error) {
	issueSeen := make(map[string]struct{}, len(issueIDs)+len(request.Bundle.Issues))
	wispSeen := make(map[string]struct{}, len(wispIDs))
	for _, id := range issueIDs {
		issueSeen[id] = struct{}{}
	}
	for _, id := range wispIDs {
		wispSeen[id] = struct{}{}
	}
	add := func(issues, wisps []string) {
		for _, id := range issues {
			issueSeen[id] = struct{}{}
		}
		for _, id := range wisps {
			wispSeen[id] = struct{}{}
		}
	}
	for _, issue := range request.Bundle.Issues {
		if IsActiveWispInTx(ctx, tx, issue.ID) {
			issues, wisps, err := AffectedByStatusChangeForWispInTx(ctx, tx, issue.ID)
			if err != nil {
				return nil, nil, fmt.Errorf("snapshot import: collect affected wisps for %q: %w", issue.ID, err)
			}
			add(issues, wisps)
		} else {
			issues, wisps, err := AffectedByStatusChangeInTx(ctx, tx, issue.ID)
			if err != nil {
				return nil, nil, fmt.Errorf("snapshot import: collect affected issues for %q: %w", issue.ID, err)
			}
			add(issues, wisps)
		}
	}
	dependencies, err := GetAllDependencyRecordsInTx(ctx, tx)
	if err != nil {
		return nil, nil, fmt.Errorf("snapshot import: collect existing dependency effects: %w", err)
	}
	owned := make(map[string]struct{}, len(request.Bundle.Issues))
	for _, issue := range request.Bundle.Issues {
		owned[issue.ID] = struct{}{}
		for _, dep := range issue.Dependencies {
			if dep == nil {
				continue
			}
			dependencies[issue.ID] = append(dependencies[issue.ID], dep)
		}
	}
	for source := range owned {
		for _, dep := range dependencies[source] {
			var issues, wisps []string
			var err error
			if IsActiveWispInTx(ctx, tx, source) {
				issues, wisps, err = AffectedByDepChangeForWispInTx(ctx, tx, source, dep.DependsOnID, dep.Type)
			} else {
				issues, wisps, err = AffectedByDepChangeInTx(ctx, tx, source, dep.DependsOnID, dep.Type)
			}
			if err != nil {
				return nil, nil, fmt.Errorf("snapshot import: collect affected dependency %q -> %q: %w", source, dep.DependsOnID, err)
			}
			add(issues, wisps)
		}
	}
	resultIssues := make([]string, 0, len(issueSeen))
	for id := range issueSeen {
		resultIssues = append(resultIssues, id)
	}
	resultWisps := make([]string, 0, len(wispSeen))
	for id := range wispSeen {
		resultWisps = append(resultWisps, id)
	}
	sort.Strings(resultIssues)
	sort.Strings(resultWisps)
	return resultIssues, resultWisps, nil
}

func removeImportedSnapshotHistory(ctx context.Context, tx DBTX, seqs []int64) error {
	if len(seqs) == 0 {
		return nil
	}
	placeholders := make([]string, len(seqs))
	args := make([]any, len(seqs))
	for i, seq := range seqs {
		placeholders[i] = "?"
		args[i] = seq
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM bd_events_journal WHERE seq IN ("+strings.Join(placeholders, ",")+")", args...); err != nil {
		return fmt.Errorf("snapshot import: remove prior imported history: %w", err)
	}
	return nil
}

func importedSnapshotHistorySeqs(ctx context.Context, tx DBTX, request publicops.SnapshotImportRequest) ([]int64, error) {
	owned := make(map[string]struct{}, len(request.Bundle.Issues))
	for _, issue := range request.Bundle.Issues {
		owned[issue.ID] = struct{}{}
	}
	rows, err := tx.QueryContext(ctx, "SELECT value FROM metadata WHERE `key` LIKE ?", publicops.SnapshotImportMetadataPrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("snapshot import: read prior snapshot markers: %w", err)
	}
	defer rows.Close()
	seen := make(map[int64]struct{})
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("snapshot import: scan prior snapshot marker: %w", err)
		}
		var marker snapshotMarker
		if err := json.Unmarshal([]byte(raw), &marker); err != nil || marker.Mode != publicops.SnapshotReplace {
			continue
		}
		overlaps := false
		for _, id := range marker.IssueIDs {
			if _, ok := owned[id]; ok {
				overlaps = true
				break
			}
		}
		if !overlaps {
			continue
		}
		for _, history := range marker.History {
			if _, ok := owned[history.IssueID]; ok {
				seen[history.Seq] = struct{}{}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("snapshot import: read prior snapshot markers: %w", err)
	}
	seqs := make([]int64, 0, len(seen))
	for seq := range seen {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	return seqs, nil
}

func checkSnapshotConflicts(ctx context.Context, tx DBTX, request publicops.SnapshotImportRequest) error {
	for _, issue := range request.Bundle.Issues {
		var issues, wisps int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM issues WHERE id = ?", issue.ID).Scan(&issues); err != nil {
			return fmt.Errorf("snapshot import: check destination %q: %w", issue.ID, err)
		}
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM wisps WHERE id = ?", issue.ID).Scan(&wisps); err != nil {
			return fmt.Errorf("snapshot import: check destination %q: %w", issue.ID, err)
		}
		if issues > 0 && wisps > 0 {
			return fmt.Errorf("snapshot import: destination ID %q exists in both storage planes", issue.ID)
		}
		if request.Mode == publicops.SnapshotCreateOnly && issues+wisps > 0 {
			return fmt.Errorf("snapshot import: destination ID %q already exists", issue.ID)
		}
		if request.Mode == publicops.SnapshotReplace {
			desiredTable, _ := TableRouting(issue)
			if (desiredTable == "issues" && wisps > 0) || (desiredTable == "wisps" && issues > 0) {
				return fmt.Errorf("snapshot import: replacement cannot move destination ID %q between storage planes", issue.ID)
			}
		}
	}
	return nil
}

func preflightSnapshotImport(ctx context.Context, tx DBTX, request publicops.SnapshotImportRequest) error {
	batchContext, err := NewBatchContext(ctx, tx, storage.BatchCreateOptions{})
	if err != nil {
		return fmt.Errorf("snapshot import: read destination validation context: %w", err)
	}
	for _, issue := range request.Bundle.Issues {
		if err := ValidateMetadataIfConfigured(issue.Metadata); err != nil {
			return fmt.Errorf("snapshot import: validate metadata for %q: %w", issue.ID, err)
		}
		if err := issue.ValidateWithCustom(batchContext.CustomStatuses, batchContext.CustomTypes); err != nil {
			return fmt.Errorf("snapshot import: validate issue %q: %w", issue.ID, err)
		}
		for _, dep := range issue.Dependencies {
			if dep.IssueID != issue.ID {
				return fmt.Errorf("snapshot import: dependency owner %q does not match enclosing issue %q", dep.IssueID, issue.ID)
			}
			if dep.CreatedAt.IsZero() {
				return fmt.Errorf("snapshot import: dependency %q -> %q requires created_at", dep.IssueID, dep.DependsOnID)
			}
			if dep.Metadata != "" && !json.Valid([]byte(dep.Metadata)) {
				return fmt.Errorf("snapshot import: dependency %q -> %q has invalid metadata JSON", dep.IssueID, dep.DependsOnID)
			}
		}
	}
	if err := preflightSnapshotDependencies(ctx, tx, request); err != nil {
		return err
	}
	if err := preflightSnapshotAuxiliaryKeys(ctx, tx, request); err != nil {
		return err
	}
	for _, ev := range request.Bundle.Provenance {
		for _, issue := range request.Bundle.Issues {
			if issue.ID == ev.IssueID && IsWisp(issue) {
				return fmt.Errorf("snapshot import: provenance cannot target wisp issue %q", ev.IssueID)
			}
		}
		if err := checkImportedProvenanceConflict(ctx, tx, ev); err != nil {
			return err
		}
	}
	return nil
}

func preflightSnapshotAuxiliaryKeys(ctx context.Context, tx DBTX, request publicops.SnapshotImportRequest) error {
	owned := make(map[string]struct{}, len(request.Bundle.Issues))
	for _, issue := range request.Bundle.Issues {
		owned[issue.ID] = struct{}{}
	}
	for _, issue := range request.Bundle.Issues {
		for _, comment := range issue.Comments {
			for _, table := range []string{"comments", "wisp_comments"} {
				var owner string
				err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT issue_id FROM %s WHERE id = ?", table), comment.ID).Scan(&owner)
				if err == nil && (request.Mode == publicops.SnapshotCreateOnly || ownerNotReplaced(owner, owned)) {
					return fmt.Errorf("snapshot import: comment ID %q already belongs to issue %q", comment.ID, owner)
				}
				if err != nil && err != sql.ErrNoRows {
					return fmt.Errorf("snapshot import: check comment ID %q: %w", comment.ID, err)
				}
			}
		}
	}
	for _, event := range request.Bundle.Events {
		for _, table := range []string{"events", "wisp_events"} {
			var owner string
			err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT issue_id FROM %s WHERE id = ?", table), event.ID).Scan(&owner)
			if err == nil && (request.Mode == publicops.SnapshotCreateOnly || ownerNotReplaced(owner, owned)) {
				return fmt.Errorf("snapshot import: event ID %q already belongs to issue %q", event.ID, owner)
			}
			if err != nil && err != sql.ErrNoRows {
				return fmt.Errorf("snapshot import: check event ID %q: %w", event.ID, err)
			}
		}
	}
	return nil
}

func ownerNotReplaced(owner string, replaced map[string]struct{}) bool {
	_, ok := replaced[owner]
	return !ok
}

func preflightSnapshotDependencies(ctx context.Context, tx DBTX, request publicops.SnapshotImportRequest) error {
	existing, err := GetAllDependencyRecordsInTx(ctx, tx)
	if err != nil {
		return fmt.Errorf("snapshot import: read existing dependencies: %w", err)
	}
	graph := make(map[string][]string)
	for source, deps := range existing {
		if request.Mode == publicops.SnapshotReplace && snapshotIssueIDSet(request, source) {
			continue
		}
		for _, dep := range deps {
			if types.IsSchedulingEdge(dep.Type) {
				graph[source] = append(graph[source], dep.DependsOnID)
			}
		}
	}
	for _, issue := range request.Bundle.Issues {
		for _, dep := range issue.Dependencies {
			if !types.IsSchedulingEdge(dep.Type) {
				continue
			}
			if dep.IssueID == dep.DependsOnID {
				return fmt.Errorf("snapshot import: dependency %q cannot depend on itself", dep.IssueID)
			}
			graph[dep.IssueID] = append(graph[dep.IssueID], dep.DependsOnID)
		}
	}
	colors := make(map[string]uint8)
	var visit func(string) bool
	visit = func(node string) bool {
		if colors[node] == 1 {
			return true
		}
		if colors[node] == 2 {
			return false
		}
		colors[node] = 1
		for _, next := range graph[node] {
			if visit(next) {
				return true
			}
		}
		colors[node] = 2
		return false
	}
	for node := range graph {
		if visit(node) {
			return fmt.Errorf("snapshot import: dependency graph contains a scheduling cycle at %q", node)
		}
	}
	return nil
}

func snapshotIssueIDSet(request publicops.SnapshotImportRequest, id string) bool {
	for _, issue := range request.Bundle.Issues {
		if issue.ID == id {
			return true
		}
	}
	return false
}

func checkImportedProvenanceConflict(ctx context.Context, tx DBTX, ev types.ProvenanceEvent) error {
	id := ProvenanceEventID(ev)
	var (
		issueID, kind, source        string
		actor, ref, refKind, payload sql.NullString
		occurredAt, createdAt        sql.NullTime
	)
	err := tx.QueryRowContext(ctx, `
		SELECT issue_id, kind, actor, ref, ref_kind, payload, source, occurred_at, created_at
		FROM provenance_events WHERE id = ?
	`, id).Scan(&issueID, &kind, &actor, &ref, &refKind, &payload, &source, &occurredAt, &createdAt)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("snapshot import: check provenance %q: %w", id, err)
	}
	if provenanceRowMatches(ev, issueID, kind, actor, ref, refKind, payload, source, occurredAt, createdAt) {
		return nil
	}
	return fmt.Errorf("snapshot import: provenance deterministic ID %q conflicts with an existing event", id)
}

func insertSnapshotDependencies(ctx context.Context, tx DBTX, issues []*types.Issue) error {
	for _, issue := range issues {
		for _, dep := range issue.Dependencies {
			kind := DepTargetIssue
			if strings.HasPrefix(dep.DependsOnID, "external:") {
				kind = DepTargetExternal
			} else if IsActiveWispInTx(ctx, tx, dep.DependsOnID) {
				kind = DepTargetWisp
			}
			depTable := "dependencies"
			if IsActiveWispInTx(ctx, tx, dep.IssueID) {
				depTable = "wisp_dependencies"
			}
			createdBy := dep.CreatedBy
			if createdBy == "" {
				createdBy = "snapshot"
			}
			metadata := dep.Metadata
			if metadata == "" {
				metadata = "{}"
			}
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
				INSERT INTO %s (id, issue_id, %s, type, created_by, created_at, metadata, thread_id)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			`, depTable, kind.Column()), depid.New(dep.IssueID, dep.DependsOnID), dep.IssueID, dep.DependsOnID, dep.Type, createdBy, dep.CreatedAt.UTC().Truncate(time.Second), metadata, dep.ThreadID); err != nil {
				return fmt.Errorf("write dependency %q -> %q: %w", dep.IssueID, dep.DependsOnID, err)
			}
		}
	}
	return nil
}

func removeSnapshotAggregates(ctx context.Context, tx DBTX, request publicops.SnapshotImportRequest) error {
	for _, issue := range request.Bundle.Issues {
		table, _ := TableRouting(issue)
		labelTable, commentTable, eventTable, depTable := "labels", "comments", "events", "dependencies"
		if table == "wisps" {
			labelTable, commentTable, eventTable, depTable = "wisp_labels", "wisp_comments", "wisp_events", "wisp_dependencies"
		}
		for _, statement := range []string{
			fmt.Sprintf("DELETE FROM %s WHERE issue_id = ?", labelTable),
			fmt.Sprintf("DELETE FROM %s WHERE issue_id = ?", commentTable),
			fmt.Sprintf("DELETE FROM %s WHERE issue_id = ?", depTable),
			fmt.Sprintf("DELETE FROM %s WHERE issue_id = ?", eventTable),
		} {
			if _, err := tx.ExecContext(ctx, statement, issue.ID); err != nil {
				return fmt.Errorf("snapshot import: clear %s for %q: %w", table, issue.ID, err)
			}
		}
		if table == "issues" {
			if _, err := tx.ExecContext(ctx, "DELETE FROM leases WHERE issue_id = ?", issue.ID); err != nil {
				return fmt.Errorf("snapshot import: clear lease for %q: %w", issue.ID, err)
			}
		}
	}
	return nil
}

func insertSnapshotLabels(ctx context.Context, tx DBTX, issue *types.Issue) error {
	table := "labels"
	if IsWisp(issue) {
		table = "wisp_labels"
	}
	seen := make(map[string]struct{}, len(issue.Labels))
	for _, label := range issue.Labels {
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (issue_id, label) VALUES (?, ?)", table), issue.ID, label); err != nil {
			return fmt.Errorf("snapshot import: write label %q for %q: %w", label, issue.ID, err)
		}
	}
	return nil
}

func insertSnapshotComments(ctx context.Context, tx DBTX, issue *types.Issue) error {
	table := "comments"
	if IsWisp(issue) {
		table = "wisp_comments"
	}
	for _, comment := range issue.Comments {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (id, issue_id, author, text, created_at) VALUES (?, ?, ?, ?, ?)", table), comment.ID, issue.ID, comment.Author, comment.Text, FormatAuxTime(comment.CreatedAt)); err != nil {
			return fmt.Errorf("snapshot import: write comment %q for %q: %w", comment.ID, issue.ID, err)
		}
	}
	return nil
}

func clearGeneratedSnapshotEvents(ctx context.Context, tx DBTX, issues []*types.Issue) error {
	for _, table := range []string{"events", "wisp_events"} {
		for _, issue := range issues {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE issue_id = ?", table), issue.ID); err != nil {
				return fmt.Errorf("snapshot import: clear generated events: %w", err)
			}
		}
	}
	return nil
}

func insertSnapshotEvents(ctx context.Context, tx DBTX, events []*types.Event) error {
	for _, event := range events {
		table := "events"
		if issueIsWisp(ctx, tx, event.IssueID) {
			table = "wisp_events"
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (id, issue_id, event_type, actor, old_value, new_value, comment, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, table), event.ID, event.IssueID, event.EventType, event.Actor, nullableString(event.OldValue), nullableString(event.NewValue), nullableString(event.Comment), event.CreatedAt.UTC().Truncate(time.Second)); err != nil {
			return fmt.Errorf("snapshot import: write event %q: %w", event.ID, err)
		}
	}
	return nil
}

func issueIsWisp(ctx context.Context, tx DBTX, id string) bool {
	var count int
	return tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM wisps WHERE id = ?", id).Scan(&count) == nil && count > 0
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func insertSnapshotHistory(ctx context.Context, tx DBTX, rows []journalops.Row) ([]snapshotHistoryMarker, error) {
	history := make([]snapshotHistoryMarker, 0, len(rows))
	for _, row := range rows {
		ts, err := parseSnapshotTimestamp(row.TS)
		if err != nil {
			return nil, fmt.Errorf("snapshot import: parse history timestamp %q: %w", row.TS, err)
		}
		seq, err := nextEventSeq(ctx, tx)
		if err != nil {
			return nil, fmt.Errorf("snapshot import: allocate history sequence: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO bd_events_journal (seq, ts, op, issue_id, actor, issue_json, dep_json, comment_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, seq, ts, row.Op, row.IssueID, row.Actor, nullableJSON(row.IssueJSON), nullableJSON(row.DepJSON), nullableJSON(row.CommentJSON)); err != nil {
			return nil, fmt.Errorf("snapshot import: write history row %d: %w", row.Seq, err)
		}
		history = append(history, snapshotHistoryMarker{Seq: seq, IssueID: row.IssueID})
	}
	return history, nil
}

func nullableJSON(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func recomputeSnapshotBlockedState(ctx context.Context, tx DBTX, issues []*types.Issue, affectedIssues, affectedWisps []string) error {
	seen := make(map[string]struct{}, len(issues)+len(affectedIssues)+len(affectedWisps))
	ids := make([]string, 0, len(issues)+len(affectedIssues)+len(affectedWisps))
	addID := func(id string) {
		if id == "" || strings.HasPrefix(id, "external:") {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for _, issue := range issues {
		addID(issue.ID)
		for _, dep := range issue.Dependencies {
			if dep != nil && !strings.HasPrefix(dep.DependsOnID, "external:") {
				addID(dep.DependsOnID)
			}
		}
	}
	for _, id := range affectedIssues {
		addID(id)
	}
	for _, id := range affectedWisps {
		addID(id)
	}
	wisps, durable, err := PartitionWispIDsInTx(ctx, tx, ids)
	if err != nil {
		return fmt.Errorf("snapshot import: partition blocked-state IDs: %w", err)
	}
	if err := RecomputeIsBlockedInTx(ctx, tx, durable, wisps); err != nil {
		return fmt.Errorf("snapshot import: recompute blocked state: %w", err)
	}
	return nil
}

func remapHistoryIssueJSON(raw string, mapping map[string]string) (string, error) {
	if raw == "" {
		return raw, nil
	}
	var issue types.Issue
	if err := json.Unmarshal([]byte(raw), &issue); err != nil {
		return "", fmt.Errorf("snapshot import: parse history issue JSON: %w", err)
	}
	if issue.ID != "" {
		mapped, err := remapRequiredIssueID(issue.ID, "history issue", mapping)
		if err != nil {
			return "", err
		}
		issue.ID = mapped
	}
	if issue.Target != "" {
		mapped, err := remapIssueReference(issue.Target, mapping)
		if err != nil {
			return "", fmt.Errorf("snapshot import: history issue target: %w", err)
		}
		issue.Target = mapped
	}
	for _, dep := range issue.Dependencies {
		if dep == nil {
			continue
		}
		if dep.IssueID != "" {
			mapped, err := remapRequiredIssueID(dep.IssueID, "history dependency owner", mapping)
			if err != nil {
				return "", err
			}
			dep.IssueID = mapped
		}
		if dep.DependsOnID != "" {
			mapped, err := remapDependencyID(dep.DependsOnID, mapping)
			if err != nil {
				return "", err
			}
			dep.DependsOnID = mapped
			if dep.ID != "" {
				dep.ID = depid.New(dep.IssueID, dep.DependsOnID)
			}
		}
	}
	for _, comment := range issue.Comments {
		if comment != nil {
			if comment.IssueID != "" {
				mapped, err := remapRequiredIssueID(comment.IssueID, "history comment owner", mapping)
				if err != nil {
					return "", err
				}
				comment.IssueID = mapped
			}
			comment.IssueID = issue.ID
		}
	}
	b, err := json.Marshal(issue)
	if err != nil {
		return "", fmt.Errorf("snapshot import: canonicalize history issue JSON: %w", err)
	}
	return string(b), nil
}

func remapHistoryDepJSON(raw string, mapping map[string]string) (string, error) {
	if raw == "" {
		return raw, nil
	}
	var dep EventDep
	if err := json.Unmarshal([]byte(raw), &dep); err != nil {
		return "", fmt.Errorf("snapshot import: parse history dependency JSON: %w", err)
	}
	if dep.Target != "" {
		mapped, err := remapDependencyID(dep.Target, mapping)
		if err != nil {
			return "", fmt.Errorf("snapshot import: history dependency target: %w", err)
		}
		dep.Target = mapped
	}
	b, err := json.Marshal(dep)
	if err != nil {
		return "", fmt.Errorf("snapshot import: canonicalize history dependency JSON: %w", err)
	}
	return string(b), nil
}

func remapRequiredIssueID(id, field string, mapping map[string]string) (string, error) {
	mapped, ok := mapping[id]
	if !ok || strings.TrimSpace(mapped) == "" {
		return "", fmt.Errorf("snapshot import: no destination ID for %s %q", field, id)
	}
	return mapped, nil
}

func remapDependencyID(id string, mapping map[string]string) (string, error) {
	if strings.HasPrefix(id, "external:") {
		if strings.TrimSpace(id) == "external:" {
			return "", fmt.Errorf("snapshot import: empty external dependency ID")
		}
		return id, nil
	}
	return remapRequiredIssueID(id, "history dependency ID", mapping)
}

func remapIssueReference(id string, mapping map[string]string) (string, error) {
	if mapped, ok := mapping[id]; ok {
		if strings.TrimSpace(mapped) == "" {
			return "", fmt.Errorf("snapshot import: destination ID for issue reference %q is empty", id)
		}
		return mapped, nil
	}
	// Entity URIs and external references are opaque. A bare target is a bead
	// reference and must resolve through the caller's identity map.
	if strings.Contains(id, ":") || strings.Contains(id, "/") {
		return id, nil
	}
	return "", fmt.Errorf("snapshot import: no destination ID for issue reference %q", id)
}

type snapshotAuditObject map[string]json.RawMessage

func prepareAuditInteractions(raw []byte, ids publicops.SnapshotIDMap) ([]json.RawMessage, []byte, error) {
	var records []json.RawMessage
	seenIDs := make(map[string]struct{})
	for lineNo, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var object snapshotAuditObject
		if err := json.Unmarshal([]byte(line), &object); err != nil {
			return nil, nil, fmt.Errorf("snapshot import: parse audit interaction line %d: %w", lineNo+1, err)
		}
		if err := remapAuditString(object, "id", ids.AuditInteractions, true); err != nil {
			return nil, nil, fmt.Errorf("snapshot import: audit interaction line %d: %w", lineNo+1, err)
		}
		var interactionID string
		if err := json.Unmarshal(object["id"], &interactionID); err != nil {
			return nil, nil, fmt.Errorf("snapshot import: audit interaction line %d has an invalid id", lineNo+1)
		}
		if _, ok := seenIDs[interactionID]; ok {
			return nil, nil, fmt.Errorf("snapshot import: duplicate destination audit interaction ID %q", interactionID)
		}
		seenIDs[interactionID] = struct{}{}
		if err := remapAuditString(object, "parent_id", ids.AuditInteractions, false); err != nil {
			return nil, nil, fmt.Errorf("snapshot import: audit interaction line %d: %w", lineNo+1, err)
		}
		if err := remapAuditString(object, "issue_id", ids.Issues, false); err != nil {
			return nil, nil, fmt.Errorf("snapshot import: audit interaction line %d: %w", lineNo+1, err)
		}
		if target, ok := object["target"]; ok {
			var value string
			if err := json.Unmarshal(target, &value); err == nil && value != "" {
				mapped, err := remapIssueReference(value, ids.Issues)
				if err != nil {
					return nil, nil, fmt.Errorf("snapshot import: audit interaction line %d: %w", lineNo+1, err)
				}
				object["target"], _ = json.Marshal(mapped)
			}
		}
		var kind string
		if err := json.Unmarshal(object["kind"], &kind); err != nil || strings.TrimSpace(kind) == "" {
			return nil, nil, fmt.Errorf("snapshot import: audit interaction kind is required")
		}
		var createdAt time.Time
		if err := json.Unmarshal(object["created_at"], &createdAt); err != nil || createdAt.IsZero() {
			return nil, nil, fmt.Errorf("snapshot import: audit interaction created_at is required")
		}
		object["created_at"], _ = json.Marshal(createdAt.UTC())
		canonical, err := json.Marshal(object)
		if err != nil {
			return nil, nil, fmt.Errorf("snapshot import: canonicalize audit interaction: %w", err)
		}
		records = append(records, canonical)
	}
	sort.Slice(records, func(i, j int) bool { return bytes.Compare(records[i], records[j]) < 0 })
	var out bytes.Buffer
	for _, record := range records {
		out.Write(record)
		out.WriteByte('\n')
	}
	return records, out.Bytes(), nil
}

func remapAuditString(object snapshotAuditObject, key string, mapping map[string]string, required bool) error {
	raw, ok := object[key]
	if !ok {
		if required {
			return fmt.Errorf("%s is required", key)
		}
		return nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || value == "" {
		return fmt.Errorf("%s must be a non-empty string", key)
	}
	mapped, ok := mapping[value]
	if !ok {
		return fmt.Errorf("no destination ID for %s %q", key, value)
	}
	if mapped == "" {
		return fmt.Errorf("destination ID for %s %q is empty", key, value)
	}
	object[key], _ = json.Marshal(mapped)
	return nil
}

func parseSnapshotTimestamp(raw string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp format")
}

type canonicalSnapshotPayload struct {
	Issues          []canonicalSnapshotIssue
	History         []journalops.Row
	Events          []*types.Event
	Provenance      []types.ProvenanceEvent
	Audit           []json.RawMessage
	MigrationMarker string
	Mode            publicops.SnapshotImportMode
}

type snapshotMarker struct {
	Mode            publicops.SnapshotImportMode `json:"mode"`
	MigrationMarker string                       `json:"migration_marker"`
	StateDigest     string                       `json:"state_digest"`
	IssueIDs        []string                     `json:"issue_ids,omitempty"`
	History         []snapshotHistoryMarker      `json:"history,omitempty"`
}

type snapshotHistoryMarker struct {
	Seq     int64  `json:"seq"`
	IssueID string `json:"issue_id"`
}

type snapshotDestinationState struct {
	Issues     []canonicalSnapshotIssue  `json:"issues"`
	Events     map[string][]*types.Event `json:"events"`
	History    []journalops.Row          `json:"history"`
	Provenance []types.ProvenanceEvent   `json:"provenance"`
}

// canonicalSnapshotIssue is the explicit import-state representation. Do not
// replace it with types.Issue: several import-relevant persisted fields are
// intentionally json:"-" on that public type, and storage plane is a property
// of the row's table rather than the row itself. RowVersion/row_lock, leases,
// ID-generation overrides, and hydration flags are intentionally absent: they
// are local or regenerated state, not snapshot-import state.
type canonicalSnapshotIssue struct {
	ID                 string              `json:"id"`
	ContentHash        string              `json:"content_hash"`
	Title              string              `json:"title"`
	Description        string              `json:"description"`
	Design             string              `json:"design"`
	AcceptanceCriteria string              `json:"acceptance_criteria"`
	Notes              string              `json:"notes"`
	SpecID             string              `json:"spec_id"`
	Status             types.Status        `json:"status"`
	Priority           int                 `json:"priority"`
	IssueType          types.IssueType     `json:"issue_type"`
	IsBlocked          bool                `json:"is_blocked"`
	Assignee           string              `json:"assignee"`
	Owner              string              `json:"owner"`
	EstimatedMinutes   *int                `json:"estimated_minutes"`
	CreatedAt          time.Time           `json:"created_at"`
	CreatedBy          string              `json:"created_by"`
	UpdatedAt          time.Time           `json:"updated_at"`
	StartedAt          *time.Time          `json:"started_at"`
	ClosedAt           *time.Time          `json:"closed_at"`
	CloseReason        string              `json:"close_reason"`
	ClosedBySession    string              `json:"closed_by_session"`
	DueAt              *time.Time          `json:"due_at"`
	DeferUntil         *time.Time          `json:"defer_until"`
	ExternalRef        *string             `json:"external_ref"`
	SourceSystem       string              `json:"source_system"`
	Metadata           json.RawMessage     `json:"metadata"`
	CompactionLevel    int                 `json:"compaction_level"`
	CompactedAt        *time.Time          `json:"compacted_at"`
	CompactedAtCommit  *string             `json:"compacted_at_commit"`
	OriginalSize       int                 `json:"original_size"`
	SourceRepo         string              `json:"source_repo"`
	Sender             string              `json:"sender"`
	Ephemeral          bool                `json:"ephemeral"`
	NoHistory          bool                `json:"no_history"`
	WispType           types.WispType      `json:"wisp_type"`
	Pinned             bool                `json:"pinned"`
	IsTemplate         bool                `json:"is_template"`
	AwaitType          string              `json:"await_type"`
	AwaitID            string              `json:"await_id"`
	Timeout            time.Duration       `json:"timeout"`
	Waiters            []string            `json:"waiters"`
	MolType            types.MolType       `json:"mol_type"`
	WorkType           types.WorkType      `json:"work_type"`
	EventKind          string              `json:"event_kind"`
	Actor              string              `json:"actor"`
	Target             string              `json:"target"`
	Payload            string              `json:"payload"`
	StorageClass       types.StorageClass  `json:"storage_class"`
	StoragePlane       string              `json:"storage_plane"`
	Labels             []string            `json:"labels"`
	Dependencies       []*types.Dependency `json:"dependencies"`
	Comments           []*types.Comment    `json:"comments"`
}

func canonicalSnapshotIssueFrom(issue *types.Issue, storagePlane string) canonicalSnapshotIssue {
	waiters := append([]string(nil), issue.Waiters...)
	if len(waiters) == 0 {
		waiters = nil
	}
	labels := append([]string(nil), issue.Labels...)
	if len(labels) == 0 {
		labels = nil
	}
	metadata := append(json.RawMessage(nil), issue.Metadata...)
	if string(metadata) == "{}" {
		metadata = nil
	}
	return canonicalSnapshotIssue{
		ID: issue.ID, ContentHash: issue.ContentHash, Title: issue.Title, Description: issue.Description,
		Design: issue.Design, AcceptanceCriteria: issue.AcceptanceCriteria, Notes: issue.Notes, SpecID: issue.SpecID,
		Status: issue.Status, Priority: issue.Priority, IssueType: issue.IssueType, IsBlocked: issue.IsBlocked,
		Assignee: issue.Assignee, Owner: issue.Owner, EstimatedMinutes: issue.EstimatedMinutes,
		CreatedAt: issue.CreatedAt, CreatedBy: issue.CreatedBy, UpdatedAt: issue.UpdatedAt, StartedAt: issue.StartedAt,
		ClosedAt: issue.ClosedAt, CloseReason: issue.CloseReason, ClosedBySession: issue.ClosedBySession,
		DueAt: issue.DueAt, DeferUntil: issue.DeferUntil, ExternalRef: issue.ExternalRef, SourceSystem: issue.SourceSystem,
		Metadata: metadata, CompactionLevel: issue.CompactionLevel, CompactedAt: issue.CompactedAt,
		CompactedAtCommit: issue.CompactedAtCommit, OriginalSize: issue.OriginalSize, SourceRepo: issue.SourceRepo,
		Sender: issue.Sender, Ephemeral: issue.Ephemeral, NoHistory: issue.NoHistory, WispType: issue.WispType,
		Pinned: issue.Pinned, IsTemplate: issue.IsTemplate, AwaitType: issue.AwaitType, AwaitID: issue.AwaitID,
		Timeout: issue.Timeout, Waiters: waiters, MolType: issue.MolType, WorkType: issue.WorkType,
		EventKind: issue.EventKind, Actor: issue.Actor, Target: issue.Target, Payload: issue.Payload,
		StorageClass: issue.StorageClass.Normalize(), StoragePlane: storagePlane, Labels: labels,
		Dependencies: issue.Dependencies, Comments: issue.Comments,
	}
}

func snapshotStoragePlane(issue *types.Issue) string {
	if IsWisp(issue) {
		return "wisps"
	}
	return "issues"
}

func canonicalSnapshot(request publicops.SnapshotImportRequest, audit []json.RawMessage) canonicalSnapshotPayload {
	issues := cloneSnapshotIssues(request.Bundle.Issues)
	sort.Slice(issues, func(i, j int) bool { return issues[i].ID < issues[j].ID })
	for _, issue := range issues {
		sort.Strings(issue.Labels)
		sort.Slice(issue.Dependencies, func(i, j int) bool {
			if issue.Dependencies[i].DependsOnID != issue.Dependencies[j].DependsOnID {
				return issue.Dependencies[i].DependsOnID < issue.Dependencies[j].DependsOnID
			}
			return issue.Dependencies[i].Type < issue.Dependencies[j].Type
		})
		sort.Slice(issue.Comments, func(i, j int) bool { return issue.Comments[i].ID < issue.Comments[j].ID })
	}
	history := append([]journalops.Row(nil), request.Bundle.History...)
	sort.Slice(history, func(i, j int) bool { return history[i].Seq < history[j].Seq })
	events := cloneSnapshotEvents(request.Bundle.Events)
	sort.Slice(events, func(i, j int) bool { return events[i].ID < events[j].ID })
	provenance := append([]types.ProvenanceEvent(nil), request.Bundle.Provenance...)
	sort.Slice(provenance, func(i, j int) bool { return ProvenanceEventID(provenance[i]) < ProvenanceEventID(provenance[j]) })
	canonicalIssues := make([]canonicalSnapshotIssue, 0, len(issues))
	for _, issue := range issues {
		canonicalIssues = append(canonicalIssues, canonicalSnapshotIssueFrom(issue, snapshotStoragePlane(issue)))
	}
	return canonicalSnapshotPayload{Issues: canonicalIssues, History: history, Events: events, Provenance: provenance, Audit: audit, MigrationMarker: request.Bundle.MigrationMarker, Mode: request.Mode}
}

func snapshotDestinationPlane(ctx context.Context, tx DBTX, id string) (string, error) {
	var issues, wisps int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM issues WHERE id = ?", id).Scan(&issues); err != nil {
		return "", fmt.Errorf("read destination issue plane for %q: %w", id, err)
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM wisps WHERE id = ?", id).Scan(&wisps); err != nil {
		return "", fmt.Errorf("read destination issue plane for %q: %w", id, err)
	}
	if issues+wisps != 1 {
		return "", fmt.Errorf("destination issue %q exists in %d storage planes", id, issues+wisps)
	}
	if wisps == 1 {
		return "wisps", nil
	}
	return "issues", nil
}

func snapshotDestinationBlocked(ctx context.Context, tx DBTX, id, plane string) (bool, error) {
	if plane != "issues" && plane != "wisps" {
		return false, fmt.Errorf("read destination blocked state for %q: invalid storage plane %q", id, plane)
	}
	var blocked int
	if err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT is_blocked FROM %s WHERE id = ?", plane), id).Scan(&blocked); err != nil {
		return false, fmt.Errorf("read destination blocked state for %q: %w", id, err)
	}
	return blocked != 0, nil
}

func snapshotDestinationDigest(ctx context.Context, tx DBTX, request publicops.SnapshotImportRequest) (string, error) {
	state := snapshotDestinationState{
		Events: make(map[string][]*types.Event),
	}
	ids := make([]string, 0, len(request.Bundle.Issues))
	for _, requested := range request.Bundle.Issues {
		issue, err := GetIssueInTx(ctx, tx, requested.ID)
		if err != nil {
			return "", fmt.Errorf("read destination issue %q: %w", requested.ID, err)
		}
		plane, err := snapshotDestinationPlane(ctx, tx, requested.ID)
		if err != nil {
			return "", err
		}
		blocked, err := snapshotDestinationBlocked(ctx, tx, requested.ID, plane)
		if err != nil {
			return "", err
		}
		ids = append(ids, requested.ID)
		comments, err := GetIssueCommentsInTx(ctx, tx, requested.ID)
		if err != nil {
			return "", fmt.Errorf("read destination comments for %q: %w", requested.ID, err)
		}
		sort.Slice(comments, func(i, j int) bool {
			if comments[i].ID != comments[j].ID {
				return comments[i].ID < comments[j].ID
			}
			return comments[i].CreatedAt.Before(comments[j].CreatedAt)
		})
		issue.Comments = comments
		issue.IsBlocked = blocked
		state.Issues = append(state.Issues, canonicalSnapshotIssueFrom(issue, plane))
		events, err := GetEventsInTx(ctx, tx, requested.ID, 0)
		if err != nil {
			return "", fmt.Errorf("read destination events for %q: %w", requested.ID, err)
		}
		state.Events[requested.ID] = events
	}
	sort.Slice(state.Issues, func(i, j int) bool { return state.Issues[i].ID < state.Issues[j].ID })
	for id, events := range state.Events {
		sort.Slice(events, func(i, j int) bool {
			if events[i].ID != events[j].ID {
				return events[i].ID < events[j].ID
			}
			return events[i].CreatedAt.Before(events[j].CreatedAt)
		})
		state.Events[id] = events
	}
	sort.Strings(ids)
	dependencies, err := GetDependencyRecordsForIssuesInTx(ctx, tx, ids)
	if err != nil {
		return "", fmt.Errorf("read destination dependencies: %w", err)
	}
	issueIndexes := make(map[string]int, len(state.Issues))
	for i := range state.Issues {
		issueIndexes[state.Issues[i].ID] = i
	}
	for id, deps := range dependencies {
		sort.Slice(deps, func(i, j int) bool {
			if deps[i].DependsOnID != deps[j].DependsOnID {
				return deps[i].DependsOnID < deps[j].DependsOnID
			}
			if deps[i].Type != deps[j].Type {
				return deps[i].Type < deps[j].Type
			}
			return deps[i].ID < deps[j].ID
		})
		if index, ok := issueIndexes[id]; ok {
			state.Issues[index].Dependencies = deps
		}
	}
	state.History, err = readSnapshotHistory(ctx, tx, ids)
	if err != nil {
		return "", err
	}
	state.Provenance, err = readSnapshotProvenance(ctx, tx, ids)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func readSnapshotHistory(ctx context.Context, tx DBTX, ids []string) ([]journalops.Row, error) {
	rows, err := querySnapshotRows(ctx, tx, `
		SELECT seq, ts, op, issue_id, actor, issue_json, dep_json, comment_json
		FROM bd_events_journal WHERE issue_id IN (%s) ORDER BY seq`, ids)
	if err != nil {
		return nil, fmt.Errorf("read destination history: %w", err)
	}
	defer rows.Close()
	var result []journalops.Row
	for rows.Next() {
		var row journalops.Row
		var issueJSON, depJSON, commentJSON sql.NullString
		if err := rows.Scan(&row.Seq, &row.TS, &row.Op, &row.IssueID, &row.Actor, &issueJSON, &depJSON, &commentJSON); err != nil {
			return nil, fmt.Errorf("scan destination history: %w", err)
		}
		row.TS = normalizeSnapshotTimestamp(row.TS)
		row.IssueJSON = canonicalJSONText(issueJSON.String)
		row.DepJSON = canonicalJSONText(depJSON.String)
		row.CommentJSON = canonicalJSONText(commentJSON.String)
		result = append(result, row)
	}
	return result, rows.Err()
}

func normalizeSnapshotTimestamp(raw string) string {
	parsed, err := parseSnapshotTimestamp(raw)
	if err != nil {
		return raw
	}
	return parsed.UTC().Format(time.RFC3339Nano)
}

func canonicalJSONText(raw string) string {
	if raw == "" {
		return ""
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return raw
	}
	b, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return string(b)
}

func readSnapshotProvenance(ctx context.Context, tx DBTX, ids []string) ([]types.ProvenanceEvent, error) {
	rows, err := querySnapshotRows(ctx, tx, `
		SELECT id, issue_id, kind, actor, ref, ref_kind, payload, source, occurred_at, created_at
		FROM provenance_events WHERE issue_id IN (%s) ORDER BY id`, ids)
	if err != nil {
		return nil, fmt.Errorf("read destination provenance: %w", err)
	}
	defer rows.Close()
	var result []types.ProvenanceEvent
	for rows.Next() {
		var event types.ProvenanceEvent
		var kind string
		var actor, ref, refKind, payload sql.NullString
		var occurredAt sql.NullTime
		if err := rows.Scan(&event.ID, &event.IssueID, &kind, &actor, &ref, &refKind, &payload, &event.Source, &occurredAt, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan destination provenance: %w", err)
		}
		event.Kind = types.ProvKind(kind)
		if actor.Valid {
			event.Actor = &actor.String
		}
		if ref.Valid {
			event.Ref = &ref.String
		}
		if refKind.Valid {
			event.RefKind = &refKind.String
		}
		if payload.Valid {
			event.Payload = &payload.String
		}
		if occurredAt.Valid {
			value := occurredAt.Time
			event.OccurredAt = &value
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func querySnapshotRows(ctx context.Context, tx DBTX, format string, ids []string) (*sql.Rows, error) {
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return tx.QueryContext(ctx, fmt.Sprintf(format, placeholders), args...)
}

func cloneSnapshotIssues(issues []*types.Issue) []*types.Issue {
	out := make([]*types.Issue, len(issues))
	for i, issue := range issues {
		if issue != nil {
			out[i] = clonePublicIssue((*publicops.Issue)(issue))
		}
	}
	return out
}

func cloneSnapshotEvents(events []*types.Event) []*types.Event {
	out := make([]*types.Event, len(events))
	for i, event := range events {
		if event == nil {
			continue
		}
		copy := *event
		copy.OldValue = cloneEventString(event.OldValue)
		copy.NewValue = cloneEventString(event.NewValue)
		copy.Comment = cloneEventString(event.Comment)
		out[i] = &copy
	}
	return out
}

func cloneEventString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneRawMessages(values []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, len(values))
	for i := range values {
		out[i] = append(json.RawMessage(nil), values[i]...)
	}
	return out
}

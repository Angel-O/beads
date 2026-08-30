package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads"
	"github.com/steveyegge/beads/internal/atomicfile"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

var (
	storeCopyPrefix    string
	storeCopyNamespace string
	storeCopyLabels    []string
)

var storeCopyCmd = &cobra.Command{
	Use:           "store-copy SOURCE_BEADS_DIR DESTINATION_BEADS_DIR",
	Short:         "Copy an offline store with full issue history",
	Args:          cobra.ExactArgs(2),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runStoreCopy,
}

func init() {
	storeCopyCmd.Flags().StringVar(&storeCopyPrefix, "prefix", "", "Prefix for destination IDs")
	storeCopyCmd.Flags().StringVar(&storeCopyNamespace, "namespace", "", "Namespace for deterministic destination IDs")
	storeCopyCmd.Flags().StringArrayVar(&storeCopyLabels, "label", nil, "Add this label to every copied issue (repeatable)")
	_ = storeCopyCmd.MarkFlagRequired("prefix")
	_ = storeCopyCmd.MarkFlagRequired("namespace")
	rootCmd.AddCommand(storeCopyCmd)
}

func runStoreCopy(cmd *cobra.Command, args []string) error {
	sourceDir, err := canonicalStoreCopyPath(args[0])
	if err != nil {
		return HandleErrorRespectJSON("canonicalize source store path: %v", err)
	}
	destinationDir, err := canonicalStoreCopyPath(args[1])
	if err != nil {
		return HandleErrorRespectJSON("canonicalize destination store path: %v", err)
	}
	if sourceDir == destinationDir || storeCopyContains(sourceDir, destinationDir) || storeCopyContains(destinationDir, sourceDir) {
		return HandleErrorRespectJSON("source and destination store paths must be distinct and non-overlapping")
	}
	prefix := strings.TrimRight(storeCopyPrefix, "-")
	if err := validatePrefix(prefix); err != nil {
		return HandleErrorRespectJSON("invalid prefix: %v", err)
	}
	namespace := strings.TrimSpace(storeCopyNamespace)
	if namespace == "" {
		return HandleErrorRespectJSON("namespace must not be empty")
	}

	ctx := rootCtx
	source, err := openOfflineStore(ctx, sourceDir, true)
	if err != nil {
		return HandleErrorRespectJSON("open source store: %v", err)
	}
	defer func() { _ = source.Close() }()

	destination, err := openOfflineStore(ctx, destinationDir, false)
	if err != nil {
		return HandleErrorRespectJSON("open destination store: %v", err)
	}
	defer func() { _ = destination.Close() }()

	sourceReader, ok := source.(storeCopySource)
	if !ok {
		return HandleErrorRespectJSON("source store does not expose the full offline copy read surface")
	}
	importer, ok := beads.AsSnapshotImporter(destination)
	if !ok {
		return HandleErrorRespectJSON("destination store does not support atomic snapshot import")
	}
	issues, err := readStoreCopyIssues(ctx, sourceReader)
	if err != nil {
		return HandleErrorRespectJSON("read source issues: %v", err)
	}
	mapKey := storeCopyMapKey(prefix, namespace)
	plan, err := importer.PlanIDs(ctx, beads.SnapshotIDPlanRequest{
		Issues: issues, Prefix: prefix, Actor: "store-copy:" + namespace, MetadataKey: mapKey,
	})
	if err != nil {
		return HandleErrorRespectJSON("plan destination IDs: %v", err)
	}
	ids := beads.SnapshotIDMap{Issues: plan.Issues, AuditInteractions: make(map[string]string)}
	bundle, err := readStoreCopySnapshot(ctx, sourceReader, sourceDir, prefix, namespace, storeCopyLabels, issues, ids)
	if err != nil {
		return HandleErrorRespectJSON("read source store: %v", err)
	}
	result, err := importer.ImportSnapshot(ctx, beads.SnapshotImportRequest{
		Bundle: bundle, IDs: ids, Mode: beads.SnapshotCreateOnly, IDMapMetadataKey: mapKey,
	})
	if err != nil {
		return HandleErrorRespectJSON("import snapshot: %v", err)
	}
	if err := installStoreCopyInteractions(destinationDir, result.StagedAuditJSONL); err != nil {
		return HandleErrorRespectJSON("install interactions: %v", err)
	}

	output := struct {
		Source             string            `json:"source"`
		Destination        string            `json:"destination"`
		Digest             string            `json:"digest"`
		Applied            bool              `json:"applied"`
		IssuesImported     int               `json:"issues_imported"`
		HistoryImported    int               `json:"history_imported"`
		EventsImported     int               `json:"events_imported"`
		ProvenanceImported int               `json:"provenance_imported"`
		IssueMap           map[string]string `json:"issue_map"`
	}{sourceDir, destinationDir, result.Digest, result.Applied, result.IssuesImported, result.HistoryImported, result.EventsImported, result.ProvenanceImported, ids.Issues}
	if jsonOutput {
		return outputJSON(output)
	}
	fmt.Fprintf(cmd.OutOrStderr(), "Copied %d issues from %s to %s\n", output.IssuesImported, sourceDir, destinationDir)
	return nil
}

type storeCopySource interface {
	SearchIssues(context.Context, string, types.IssueFilter) ([]*types.Issue, error)
	GetLabelsForIssues(context.Context, []string) (map[string][]string, error)
	GetDependencyRecordsForIssues(context.Context, []string) (map[string][]*types.Dependency, error)
	GetCommentsForIssues(context.Context, []string) (map[string][]*types.Comment, error)
	ReadEventsJournal(context.Context, int64, int) ([]storage.EventsJournalRow, error)
	GetAllEventsSince(context.Context, time.Time) ([]*types.Event, error)
	GetProvenanceEvents(context.Context, string, string) ([]types.ProvenanceEvent, error)
}

func readStoreCopyIssues(ctx context.Context, source storeCopySource) ([]*types.Issue, error) {
	issues, err := source.SearchIssues(ctx, "", types.IssueFilter{
		Limit:      0,
		MaxRows:    0,
		Ephemeral:  nil, // include persistent issues and wisps
		IsTemplate: nil, // include templates
		SkipWisps:  false,
	})
	if err != nil {
		return nil, err
	}
	return issues, nil
}

func readStoreCopySnapshot(ctx context.Context, source storeCopySource, sourceDir, prefix, namespace string, extraLabels []string, issues []*types.Issue, ids beads.SnapshotIDMap) (beads.SnapshotImportBundle, error) {
	seenDestinations := make(map[string]string, len(issues))
	for _, issue := range issues {
		if issue == nil || strings.TrimSpace(issue.ID) == "" {
			return beads.SnapshotImportBundle{}, fmt.Errorf("source contains an issue without an ID")
		}
		mapped, exists := ids.Issues[issue.ID]
		if !exists || strings.TrimSpace(mapped) == "" {
			return beads.SnapshotImportBundle{}, fmt.Errorf("source issue ID %q has no planned destination ID", issue.ID)
		}
		if previous, exists := seenDestinations[mapped]; exists {
			return beads.SnapshotImportBundle{}, fmt.Errorf("source issue IDs %q and %q both map to destination ID %q", previous, issue.ID, mapped)
		}
		seenDestinations[mapped] = issue.ID
	}
	if len(ids.Issues) != len(issues) {
		return beads.SnapshotImportBundle{}, fmt.Errorf("planned ID map contains %d entries for %d source issues", len(ids.Issues), len(issues))
	}

	issueIDs := make([]string, 0, len(issues))
	clonedIssues := make([]*types.Issue, len(issues))
	for i, issue := range issues {
		issueIDs = append(issueIDs, issue.ID)
		clonedIssues[i] = cloneStoreCopyIssue(issue)
	}
	labels, err := source.GetLabelsForIssues(ctx, issueIDs)
	if err != nil {
		return beads.SnapshotImportBundle{}, fmt.Errorf("read labels: %w", err)
	}
	dependencies, err := source.GetDependencyRecordsForIssues(ctx, issueIDs)
	if err != nil {
		return beads.SnapshotImportBundle{}, fmt.Errorf("read dependencies: %w", err)
	}
	comments, err := source.GetCommentsForIssues(ctx, issueIDs)
	if err != nil {
		return beads.SnapshotImportBundle{}, fmt.Errorf("read comments: %w", err)
	}
	for i, issue := range clonedIssues {
		issue.Labels = appendUniqueStoreCopyLabels(labels[issueIDs[i]], extraLabels)
		issue.Dependencies = cloneStoreCopyDependencies(dependencies[issueIDs[i]])
		issue.Comments = cloneStoreCopyComments(comments[issueIDs[i]])
	}

	history, err := source.ReadEventsJournal(ctx, 0, 0)
	if err != nil {
		return beads.SnapshotImportBundle{}, fmt.Errorf("read journal history: %w", err)
	}
	events, err := source.GetAllEventsSince(ctx, timeZero)
	if err != nil {
		return beads.SnapshotImportBundle{}, fmt.Errorf("read events: %w", err)
	}
	var provenance []types.ProvenanceEvent
	for _, issueID := range issueIDs {
		rows, err := source.GetProvenanceEvents(ctx, issueID, "")
		if err != nil {
			return beads.SnapshotImportBundle{}, fmt.Errorf("read provenance for %q: %w", issueID, err)
		}
		provenance = append(provenance, rows...)
	}
	auditJSONL, err := readStoreCopyInteractions(sourceDir, prefix, namespace, ids)
	if err != nil {
		return beads.SnapshotImportBundle{}, err
	}

	for _, event := range events {
		if event == nil {
			continue
		}
		if _, ok := ids.Issues[event.IssueID]; !ok {
			return beads.SnapshotImportBundle{}, fmt.Errorf("event %q references issue %q outside the source snapshot", event.ID, event.IssueID)
		}
	}
	for i := range history {
		if _, ok := ids.Issues[history[i].IssueID]; !ok {
			return beads.SnapshotImportBundle{}, fmt.Errorf("journal row %d references issue %q outside the source snapshot", history[i].Seq, history[i].IssueID)
		}
	}

	return beads.SnapshotImportBundle{
		Issues:                 clonedIssues,
		History:                history,
		Events:                 events,
		Provenance:             provenance,
		AuditInteractionsJSONL: auditJSONL,
		MigrationMarker:        "bd-store-copy-v1",
	}, nil
}

var timeZero = time.Time{}

func storeCopyID(prefix, namespace, kind, sourceID string) string {
	input := namespace + "\x00" + kind + "\x00" + sourceID
	sum := sha256.Sum256([]byte(input))
	return prefix + "-" + hex.EncodeToString(sum[:])
}

func storeCopyMapKey(prefix, namespace string) string {
	sum := sha256.Sum256([]byte(prefix + "\x00" + namespace))
	return "store-copy/id-map/" + hex.EncodeToString(sum[:])
}

func cloneStoreCopyIssue(issue *types.Issue) *types.Issue {
	clone := *issue
	clone.Labels = nil
	clone.Dependencies = nil
	clone.Comments = nil
	clone.Waiters = append([]string(nil), issue.Waiters...)
	clone.BondedFrom = append([]types.BondRef(nil), issue.BondedFrom...)
	return &clone
}

func cloneStoreCopyDependencies(dependencies []*types.Dependency) []*types.Dependency {
	clones := make([]*types.Dependency, 0, len(dependencies))
	for _, dependency := range dependencies {
		if dependency == nil {
			continue
		}
		clone := *dependency
		clones = append(clones, &clone)
	}
	return clones
}

func cloneStoreCopyComments(comments []*types.Comment) []*types.Comment {
	clones := make([]*types.Comment, 0, len(comments))
	for _, comment := range comments {
		if comment == nil {
			continue
		}
		clone := *comment
		clones = append(clones, &clone)
	}
	return clones
}

func appendUniqueStoreCopyLabels(labels, extra []string) []string {
	result := append([]string(nil), labels...)
	seen := make(map[string]struct{}, len(result)+len(extra))
	for _, label := range result {
		seen[label] = struct{}{}
	}
	for _, label := range extra {
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		if _, ok := seen[label]; !ok {
			result = append(result, label)
			seen[label] = struct{}{}
		}
	}
	return result
}

func readStoreCopyInteractions(sourceDir, prefix, namespace string, ids beads.SnapshotIDMap) ([]byte, error) {
	path := filepath.Join(sourceDir, "interactions.jsonl")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read interactions: %w", err)
	}
	lines := strings.Split(string(raw), "\n")
	var filtered bytes.Buffer
	for lineNo, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &object); err != nil {
			return nil, fmt.Errorf("parse interactions line %d: %w", lineNo+1, err)
		}
		var id string
		if err := json.Unmarshal(object["id"], &id); err != nil || id == "" {
			return nil, fmt.Errorf("interactions line %d has no valid id", lineNo+1)
		}
		if rawIssueID, ok := object["issue_id"]; ok {
			var issueID string
			if err := json.Unmarshal(rawIssueID, &issueID); err != nil || issueID == "" {
				return nil, fmt.Errorf("interactions line %d has no valid issue_id", lineNo+1)
			}
			if _, copied := ids.Issues[issueID]; !copied {
				continue
			}
		}
		mappedID := storeCopyID(prefix, namespace, "interaction", id)
		ids.AuditInteractions[id] = mappedID
		filtered.WriteString(line)
		filtered.WriteByte('\n')
	}
	// The importer performs the actual interaction and issue-reference remapping.
	return filtered.Bytes(), nil
}

func canonicalStoreCopyPath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absPath)
}

func storeCopyContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func installStoreCopyInteractions(destinationDir string, staged []byte) error {
	if len(bytes.TrimSpace(staged)) == 0 {
		return nil
	}
	path := filepath.Join(destinationDir, "interactions.jsonl")
	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		existing = nil
	} else if err != nil {
		return err
	}
	known := make(map[string][]byte)
	for lineNo, line := range strings.Split(string(existing), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		id, canonical, err := canonicalStoreCopyInteraction([]byte(line))
		if err != nil {
			return fmt.Errorf("parse destination interactions line %d: %w", lineNo+1, err)
		}
		known[id] = canonical
	}
	merged := append([]byte(nil), existing...)
	if len(merged) > 0 && merged[len(merged)-1] != '\n' {
		merged = append(merged, '\n')
	}
	for lineNo, line := range strings.Split(string(staged), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		id, canonical, err := canonicalStoreCopyInteraction([]byte(line))
		if err != nil {
			return fmt.Errorf("parse staged interactions line %d: %w", lineNo+1, err)
		}
		if previous, ok := known[id]; ok {
			if !bytes.Equal(previous, canonical) {
				return fmt.Errorf("destination interaction ID %q conflicts with copied interaction", id)
			}
			continue
		}
		merged = append(merged, canonical...)
		merged = append(merged, '\n')
		known[id] = canonical
	}
	return atomicfile.WriteFile(path, merged, 0o600)
}

func canonicalStoreCopyInteraction(raw []byte) (string, []byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", nil, err
	}
	var id string
	if err := json.Unmarshal(object["id"], &id); err != nil || id == "" {
		return "", nil, fmt.Errorf("interaction ID is required")
	}
	canonical, err := json.Marshal(object)
	return id, canonical, err
}

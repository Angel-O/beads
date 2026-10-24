package issueops

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

const todoIssueType types.IssueType = "todo"

// ContextReplacementLabels validates a structured context replacement and
// returns the complete label set with ordinary labels preserved.
func ContextReplacementLabels(issue *types.Issue, contexts []string) ([]string, error) {
	if err := ValidateContextReplacementTarget(issue); err != nil {
		return nil, err
	}
	normalized, err := normalizeContextLabels(contexts)
	if err != nil {
		return nil, err
	}
	if err := validateContextCardinality(issue.IssueType, normalized); err != nil {
		return nil, err
	}

	labels := make([]string, 0, len(issue.Labels)+len(normalized))
	for _, label := range issue.Labels {
		if !strings.HasPrefix(label, epicContextLabelPrefix) {
			labels = append(labels, label)
		}
	}
	labels = append(labels, normalized...)
	sort.Strings(labels)
	return labels, nil
}

// ValidateContextReplacementTarget rejects an existing epic before any
// requested IssueType patch is considered. Callers must also complete their
// external-history preflight and must not call context replacement for a bead
// with any external-history correlation.
func ValidateContextReplacementTarget(issue *types.Issue) error {
	if issue == nil {
		return fmt.Errorf("%w: context replacement requires an issue", storage.ErrValidation)
	}
	if issue.IssueType == types.TypeEpic {
		return fmt.Errorf("%w: epic issues derive contexts from their children", storage.ErrValidation)
	}
	return nil
}

func normalizeContextLabels(contexts []string) ([]string, error) {
	seen := make(map[string]struct{}, len(contexts))
	labels := make([]string, 0, len(contexts))
	for _, contextID := range contexts {
		contextID = strings.TrimSpace(contextID)
		contextID = strings.TrimPrefix(contextID, epicContextLabelPrefix)
		if contextID == "" {
			return nil, fmt.Errorf("%w: context IDs must not be empty", storage.ErrValidation)
		}
		label := epicContextLabelPrefix + contextID
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return labels, nil
}

func validateContextCardinality(issueType types.IssueType, contexts []string) error {
	if issueType == types.TypeEpic {
		return fmt.Errorf("%w: epic issues derive contexts from their children", storage.ErrValidation)
	}
	if issueType != todoIssueType && len(contexts) != 1 {
		return fmt.Errorf("%w: issue type %q requires exactly one context", storage.ErrValidation, issueType)
	}
	return nil
}

// ReconcileDirectEpicParentsInTx re-derives every direct epic parent of a
// child after its contexts change. It deliberately runs in the caller's
// transaction so a child or parent refusal rolls back the complete mutation.
func ReconcileDirectEpicParentsInTx(ctx context.Context, tx DBTX, childID, actor string) (ChangedTables, error) {
	dependencies, err := GetDependencyRecordsForIssuesInTx(ctx, tx, []string{childID})
	if err != nil {
		return nil, fmt.Errorf("reconcile direct epic parents: read parents: %w", err)
	}
	parents := make(map[string]struct{})
	for _, dependency := range dependencies[childID] {
		if dependency.Type == types.DepParentChild {
			parents[dependency.DependsOnID] = struct{}{}
		}
	}
	parentIDs := make([]string, 0, len(parents))
	for parentID := range parents {
		parent, err := GetIssueInTx(ctx, tx, parentID)
		if err != nil {
			return nil, fmt.Errorf("reconcile direct epic parents: read parent %s: %w", parentID, err)
		}
		if parent.IssueType == types.TypeEpic {
			parentIDs = append(parentIDs, parentID)
		}
	}
	sort.Strings(parentIDs)

	tables := ChangedTables{}
	for _, parentID := range parentIDs {
		reconciled, err := ReconcileEpicContextsInTx(ctx, tx, parentID, actor)
		if err != nil {
			return nil, err
		}
		tables.Merge(reconciled)
	}
	return tables, nil
}

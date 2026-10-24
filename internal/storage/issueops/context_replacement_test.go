package issueops

import (
	"errors"
	"reflect"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

func TestContextReplacementLabelsPreservesOrdinaryLabels(t *testing.T) {
	labels, err := ContextReplacementLabels(&types.Issue{
		IssueType: types.TypeTask,
		Labels:    []string{"ctx:old", "ordinary"},
	}, []string{"ctx:new", "new"})
	if err != nil {
		t.Fatalf("ContextReplacementLabels: %v", err)
	}
	if want := []string{"ctx:new", "ordinary"}; !reflect.DeepEqual(labels, want) {
		t.Fatalf("labels = %v, want %v", labels, want)
	}
}

func TestContextReplacementLabelsCardinality(t *testing.T) {
	tests := []struct {
		name     string
		issue    types.Issue
		contexts []string
		wantErr  bool
	}{
		{name: "epic", issue: types.Issue{IssueType: types.TypeEpic}, contexts: []string{"one"}, wantErr: true},
		{name: "task cannot clear", issue: types.Issue{IssueType: types.TypeTask}, wantErr: true},
		{name: "task cannot have two", issue: types.Issue{IssueType: types.TypeTask}, contexts: []string{"one", "two"}, wantErr: true},
		{name: "custom requires one", issue: types.Issue{IssueType: types.IssueType("custom")}, contexts: []string{"one"}, wantErr: false},
		{name: "custom cannot clear", issue: types.Issue{IssueType: types.IssueType("custom")}, wantErr: true},
		{name: "todo can clear", issue: types.Issue{IssueType: todoIssueType}, wantErr: false},
		{name: "todo can have many", issue: types.Issue{IssueType: todoIssueType}, contexts: []string{"one", "two"}, wantErr: false},
		{name: "empty context", issue: types.Issue{IssueType: types.TypeTask}, contexts: []string{""}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ContextReplacementLabels(&tt.issue, tt.contexts)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, storage.ErrValidation) {
				t.Fatalf("error = %v, want ErrValidation", err)
			}
		})
	}
}

func TestValidateUpdateRequestRejectsGenericLabelBypass(t *testing.T) {
	err := ValidateUpdateRequest(publicops.UpdateRequest{Patch: publicops.IssuePatch{
		Contexts: publicops.Field[[]string]{Set: true, Value: []string{"one"}},
		Labels:   publicops.LabelPatch{Add: []string{"ctx:two"}},
	}})
	if !errors.Is(err, storage.ErrValidation) {
		t.Fatalf("error = %v, want ErrValidation", err)
	}
}

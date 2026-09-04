//go:build cgo

package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/types"
)

func TestScopeCommandsDelegateAndEmitJSON(t *testing.T) {
	s := newTestStore(t, filepath.Join(t.TempDir(), ".beads", "beads.db"))
	oldStore, oldRoot, oldJSON, oldProxied := store, rootCtx, jsonOutput, proxiedServerMode
	t.Cleanup(func() {
		store, rootCtx, jsonOutput, proxiedServerMode = oldStore, oldRoot, oldJSON, oldProxied
	})
	store = s
	rootCtx = context.Background()
	jsonOutput = true
	proxiedServerMode = false

	issueA := &types.Issue{ID: "test-scope-a", Title: "Scope A issue", Status: types.StatusOpen, IssueType: types.TypeTask}
	issueB := &types.Issue{ID: "test-scope-b", Title: "Scope B issue", Status: types.StatusOpen, IssueType: types.TypeTask}
	for _, issue := range []*types.Issue{issueA, issueB} {
		if err := s.CreateIssue(rootCtx, issue, "scope-test"); err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
	}

	if err := scopeCreateCmd.Flags().Set("activate", "true"); err != nil {
		t.Fatalf("set --activate: %v", err)
	}
	t.Cleanup(func() { _ = scopeCreateCmd.Flags().Set("activate", "false") })
	var created types.Scope
	decodeScopeJSON(t, captureStdout(t, func() error {
		return scopeCreateCmd.RunE(scopeCreateCmd, []string{"scope-a", "Scope A"})
	}), &created)
	if created.ID != "scope-a" || created.NormalizedName != "scope a" {
		t.Fatalf("created scope = %#v", created)
	}

	if err := scopeCreateCmd.Flags().Set("activate", "false"); err != nil {
		t.Fatalf("clear --activate: %v", err)
	}
	run := func(command *cobra.Command, args ...string) {
		captureStdout(t, func() error { return command.RunE(command, args) })
	}
	run(scopeCreateCmd, "scope-b", "Scope B")
	var scopes []*types.Scope
	decodeScopeJSON(t, captureStdout(t, func() error {
		return scopeListCmd.RunE(scopeListCmd, nil)
	}), &scopes)
	if len(scopes) != 2 {
		t.Fatalf("listed scopes = %d, want 2", len(scopes))
	}
	run(scopeAddCmd, "scope-a", issueA.ID, issueB.ID)
	run(scopeMoveCmd, "scope-a", "scope-b", issueB.ID)
	run(scopeRemoveCmd, "scope-a", issueA.ID)

	var details types.ScopeDetails
	decodeScopeJSON(t, captureStdout(t, func() error {
		return scopeShowCmd.RunE(scopeShowCmd, []string{"scope-a"})
	}), &details)
	if len(details.Members) != 0 {
		t.Fatalf("scope-a members = %d, want 0", len(details.Members))
	}

	var active types.Scope
	decodeScopeJSON(t, captureStdout(t, func() error {
		return scopeActiveCmd.RunE(scopeActiveCmd, nil)
	}), &active)
	if active.ID != "scope-a" {
		t.Fatalf("active scope = %#v", active)
	}

	var mutation map[string]any
	decodeScopeJSON(t, captureStdout(t, func() error {
		return scopeDeactivateCmd.RunE(scopeDeactivateCmd, nil)
	}), &mutation)
	if mutation["status"] != "deactivated" {
		t.Fatalf("deactivate output = %#v", mutation)
	}
	if got := captureStdout(t, func() error {
		return scopeActiveCmd.RunE(scopeActiveCmd, nil)
	}); got != "{\n  \"schema_version\": 1\n}\n" {
		t.Fatalf("inactive active-scope JSON = %q", got)
	}
	decodeScopeJSON(t, captureStdout(t, func() error {
		return scopeActivateCmd.RunE(scopeActivateCmd, []string{"scope-b"})
	}), &mutation)
	if mutation["status"] != "activated" || mutation["scope_id"] != "scope-b" {
		t.Fatalf("activate output = %#v", mutation)
	}
}

func decodeScopeJSON(t *testing.T, raw string, dst any) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), dst); err != nil {
		t.Fatalf("decode scope JSON: %v\n%s", err, raw)
	}
}

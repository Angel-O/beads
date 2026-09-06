//go:build cgo

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/storage"
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
	oldListPaginate, _ := scopeListCmd.Flags().GetBool("paginate")
	oldListLimit, _ := scopeListCmd.Flags().GetInt("limit")
	oldListCursor, _ := scopeListCmd.Flags().GetString("cursor")
	oldShowPaginate, _ := scopeShowCmd.Flags().GetBool("paginate")
	oldShowLimit, _ := scopeShowCmd.Flags().GetInt("limit")
	oldShowCursor, _ := scopeShowCmd.Flags().GetString("cursor")
	oldShowStatus, _ := scopeShowCmd.Flags().GetString("status")
	oldShowType, _ := scopeShowCmd.Flags().GetString("type")
	t.Cleanup(func() {
		_ = scopeListCmd.Flags().Set("paginate", fmt.Sprint(oldListPaginate))
		_ = scopeListCmd.Flags().Set("limit", fmt.Sprint(oldListLimit))
		_ = scopeListCmd.Flags().Set("cursor", oldListCursor)
		_ = scopeShowCmd.Flags().Set("paginate", fmt.Sprint(oldShowPaginate))
		_ = scopeShowCmd.Flags().Set("limit", fmt.Sprint(oldShowLimit))
		_ = scopeShowCmd.Flags().Set("cursor", oldShowCursor)
		_ = scopeShowCmd.Flags().Set("status", oldShowStatus)
		_ = scopeShowCmd.Flags().Set("type", oldShowType)
	})

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

	if err := scopeListCmd.Flags().Set("paginate", "true"); err != nil {
		t.Fatalf("set list --paginate: %v", err)
	}
	if err := scopeListCmd.Flags().Set("limit", "1"); err != nil {
		t.Fatalf("set list --limit: %v", err)
	}
	var catalog storage.ScopeCatalogPage
	decodeScopeJSON(t, captureStdout(t, func() error {
		return scopeListCmd.RunE(scopeListCmd, nil)
	}), &catalog)
	if len(catalog.Items) != 1 || catalog.Limit != 1 || !catalog.HasMore {
		t.Fatalf("catalog page = %#v, want first bounded page", catalog)
	}

	if err := scopeShowCmd.Flags().Set("paginate", "true"); err != nil {
		t.Fatalf("set show --paginate: %v", err)
	}
	if err := scopeShowCmd.Flags().Set("limit", "1"); err != nil {
		t.Fatalf("set show --limit: %v", err)
	}
	if err := scopeShowCmd.Flags().Set("status", "open"); err != nil {
		t.Fatalf("set show --status: %v", err)
	}
	if err := scopeShowCmd.Flags().Set("type", "task"); err != nil {
		t.Fatalf("set show --type: %v", err)
	}
	var members storage.ScopeMemberPage
	decodeScopeJSON(t, captureStdout(t, func() error {
		return scopeShowCmd.RunE(scopeShowCmd, []string{"scope-b"})
	}), &members)
	if members.Scope.ID != "scope-b" || members.TotalMatching != 1 || len(members.Members) != 1 || members.Members[0].ID != issueB.ID {
		t.Fatalf("member page = %#v, want filtered scope-b member", members)
	}
}

func decodeScopeJSON(t *testing.T, raw string, dst any) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), dst); err != nil {
		t.Fatalf("decode scope JSON: %v\n%s", err, raw)
	}
}

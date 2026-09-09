//go:build cgo

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
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
	createdRaw := captureStdout(t, func() error {
		return scopeCreateCmd.RunE(scopeCreateCmd, []string{"scope-a", "Scope A"})
	})
	assertMemberLimit(t, createdRaw)
	var created types.Scope
	decodeScopeJSON(t, createdRaw, &created)
	if created.ID != "scope-a" || created.NormalizedName != "scope a" || created.MemberLimit != storage.MaxScopeMembers {
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
	listRaw := captureStdout(t, func() error {
		return scopeListCmd.RunE(scopeListCmd, nil)
	})
	assertMemberLimit(t, listRaw, "0")
	assertMemberLimit(t, listRaw, "1")
	decodeScopeJSON(t, listRaw, &scopes)
	if len(scopes) != 2 {
		t.Fatalf("listed scopes = %d, want 2", len(scopes))
	}
	run(scopeAddCmd, "scope-a", issueA.ID, issueB.ID)
	run(scopeMoveCmd, "scope-a", "scope-b", issueB.ID)
	run(scopeRemoveCmd, "scope-a", issueA.ID)

	var details types.ScopeDetails
	detailsRaw := captureStdout(t, func() error {
		return scopeShowCmd.RunE(scopeShowCmd, []string{"scope-a"})
	})
	assertMemberLimit(t, detailsRaw)
	decodeScopeJSON(t, detailsRaw, &details)
	if len(details.Members) != 0 {
		t.Fatalf("scope-a members = %d, want 0", len(details.Members))
	}

	var active types.Scope
	activeRaw := captureStdout(t, func() error {
		return scopeActiveCmd.RunE(scopeActiveCmd, nil)
	})
	assertMemberLimit(t, activeRaw)
	decodeScopeJSON(t, activeRaw, &active)
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
	catalogRaw := captureStdout(t, func() error {
		return scopeListCmd.RunE(scopeListCmd, nil)
	})
	assertMemberLimit(t, catalogRaw, "items", "0")
	decodeScopeJSON(t, catalogRaw, &catalog)
	if len(catalog.Items) != 1 || catalog.Items[0].MemberLimit != storage.MaxScopeMembers || catalog.Limit != 1 || !catalog.HasMore {
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
	membersRaw := captureStdout(t, func() error {
		return scopeShowCmd.RunE(scopeShowCmd, []string{"scope-b"})
	})
	assertMemberLimit(t, membersRaw, "scope")
	decodeScopeJSON(t, membersRaw, &members)
	if members.Scope.ID != "scope-b" || members.Scope.MemberLimit != storage.MaxScopeMembers || members.TotalMatching != 1 || len(members.Members) != 1 || members.Members[0].ID != issueB.ID {
		t.Fatalf("member page = %#v, want filtered scope-b member", members)
	}
}

func decodeScopeJSON(t *testing.T, raw string, dst any) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), dst); err != nil {
		t.Fatalf("decode scope JSON: %v\n%s", err, raw)
	}
}

func assertMemberLimit(t *testing.T, raw string, path ...string) {
	t.Helper()
	path = append(path, "member_limit")
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatalf("decode scope JSON for member_limit: %v\n%s", err, raw)
	}
	for _, part := range path {
		switch current := value.(type) {
		case map[string]any:
			next, ok := current[part]
			if !ok {
				t.Fatalf("scope JSON missing %q\n%s", part, raw)
			}
			value = next
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(current) {
				t.Fatalf("invalid scope JSON array path %q\n%s", part, raw)
			}
			value = current[index]
		default:
			t.Fatalf("scope JSON path %q reached %T\n%s", part, value, raw)
		}
	}
	got, ok := value.(float64)
	if !ok {
		t.Fatalf("scope JSON member_limit has type %T\n%s", value, raw)
	}
	if int(got) != storage.MaxScopeMembers || got != float64(storage.MaxScopeMembers) {
		t.Fatalf("scope JSON member_limit = %v, want %d\n%s", got, storage.MaxScopeMembers, raw)
	}
}

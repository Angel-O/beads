package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
)

func TestScopeCommandsAreRegisteredWithRequiredArguments(t *testing.T) {
	want := map[string]struct {
		args []string
	}{
		"create":     {args: []string{"scope-id", "Scope name"}},
		"list":       {args: nil},
		"show":       {args: []string{"scope-id"}},
		"active":     {args: nil},
		"activate":   {args: []string{"scope-id"}},
		"deactivate": {args: nil},
		"add":        {args: []string{"scope-id", "issue-id"}},
		"remove":     {args: []string{"scope-id", "issue-id"}},
		"move":       {args: []string{"source-id", "target-id", "issue-id"}},
	}

	children := make(map[string]*cobra.Command, len(scopeCmd.Commands()))
	for _, command := range scopeCmd.Commands() {
		children[command.Name()] = command
	}
	if len(children) != len(want) {
		t.Fatalf("registered scope commands = %d, want %d", len(children), len(want))
	}
	for name, tc := range want {
		command := children[name]
		if command == nil {
			t.Fatalf("scope command %q is not registered", name)
		}
		if err := command.Args(command, tc.args); err != nil {
			t.Errorf("scope %s valid args rejected: %v", name, err)
		}
	}

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "create", args: []string{"only-id"}},
		{name: "show", args: nil},
		{name: "activate", args: nil},
		{name: "deactivate", args: []string{"unexpected"}},
		{name: "add", args: []string{"scope-only"}},
		{name: "remove", args: []string{"scope-only"}},
		{name: "move", args: []string{"source", "target"}},
	} {
		if err := children[tc.name].Args(children[tc.name], tc.args); err == nil {
			t.Errorf("scope %s accepted invalid args %v", tc.name, tc.args)
		}
	}

	if scopeCreateCmd.Flags().Lookup("activate") == nil {
		t.Fatal("scope create is missing --activate")
	}
}

func TestListParsesUnscopedFlag(t *testing.T) {
	command := &cobra.Command{}
	command.Flags().Bool("unscoped", false, "")
	if err := command.ParseFlags([]string{"--unscoped"}); err != nil {
		t.Fatalf("parse --unscoped: %v", err)
	}

	in, err := gatherListInput(command)
	if err != nil {
		t.Fatalf("gather list input: %v", err)
	}
	if !in.Unscoped {
		t.Fatal("--unscoped did not reach the list request")
	}
}

func TestScopePagedFlagsAreAdditiveAndContextIsRepeatable(t *testing.T) {
	for _, command := range []*cobra.Command{scopeListCmd, scopeShowCmd} {
		for _, name := range []string{"paginate", "limit", "cursor"} {
			if command.Flags().Lookup(name) == nil {
				t.Errorf("scope %s is missing --%s", command.Name(), name)
			}
		}
	}
	for _, name := range []string{"status", "type", "context"} {
		if scopeShowCmd.Flags().Lookup(name) == nil {
			t.Errorf("scope show is missing --%s", name)
		}
	}

	command := &cobra.Command{}
	command.Flags().StringArray("context", nil, "")
	if err := command.Flags().Set("context", "team-a"); err != nil {
		t.Fatalf("set first --context: %v", err)
	}
	if err := command.Flags().Set("context", "team-b"); err != nil {
		t.Fatalf("set second --context: %v", err)
	}
	contexts, err := command.Flags().GetStringArray("context")
	if err != nil {
		t.Fatalf("read --context: %v", err)
	}
	if len(contexts) != 2 || contexts[0] != "team-a" || contexts[1] != "team-b" {
		t.Fatalf("contexts = %v, want [team-a team-b]", contexts)
	}
}

func TestScopePaginationIsJSONOnly(t *testing.T) {
	oldJSON := jsonOutput
	oldPaginate, _ := scopeListCmd.Flags().GetBool("paginate")
	t.Cleanup(func() {
		jsonOutput = oldJSON
		_ = scopeListCmd.Flags().Set("paginate", fmt.Sprint(oldPaginate))
	})
	jsonOutput = false
	if err := scopeListCmd.Flags().Set("paginate", "true"); err != nil {
		t.Fatalf("set --paginate: %v", err)
	}
	if err := scopeListCmd.RunE(scopeListCmd, nil); err == nil {
		t.Fatal("scope list pagination without --json succeeded")
	}
}

type scopeReadUseCaseStub struct {
	domain.ScopeUseCase
	catalogRequest storage.ScopeCatalogRequest
	membersRequest storage.ScopeMemberPageRequest
}

func (s *scopeReadUseCaseStub) ListScopeCatalog(_ context.Context, request types.ScopeCatalogRequest) (*types.ScopeCatalogPage, error) {
	s.catalogRequest = request
	return &types.ScopeCatalogPage{}, nil
}

func (s *scopeReadUseCaseStub) ListScopeMembers(_ context.Context, _ string, request types.ScopeMemberPageRequest) (*types.ScopeMemberPage, error) {
	s.membersRequest = request
	return &types.ScopeMemberPage{}, nil
}

type scopeReadUOW struct {
	uow.UnitOfWork
	scope domain.ScopeUseCase
}

func (s scopeReadUOW) Close(context.Context)             {}
func (s scopeReadUOW) ScopeUseCase() domain.ScopeUseCase { return s.scope }

type scopeReadProvider struct{ unit uow.UnitOfWork }

func (p scopeReadProvider) NewUOW(context.Context) (uow.UnitOfWork, error) { return p.unit, nil }
func (scopeReadProvider) Close(context.Context) error                      { return nil }

func TestScopePagedCommandsUseProxiedScopeContract(t *testing.T) {
	stub := &scopeReadUseCaseStub{}
	oldProvider, oldMode, oldJSON, oldRoot := uowProvider, proxiedServerMode, jsonOutput, rootCtx
	t.Cleanup(func() {
		uowProvider, proxiedServerMode, jsonOutput, rootCtx = oldProvider, oldMode, oldJSON, oldRoot
	})
	uowProvider = scopeReadProvider{unit: scopeReadUOW{scope: stub}}
	proxiedServerMode = true
	jsonOutput = true
	rootCtx = context.Background()

	oldListPaginate, _ := scopeListCmd.Flags().GetBool("paginate")
	oldListLimit, _ := scopeListCmd.Flags().GetInt("limit")
	oldShowPaginate, _ := scopeShowCmd.Flags().GetBool("paginate")
	oldShowLimit, _ := scopeShowCmd.Flags().GetInt("limit")
	oldShowCursor, _ := scopeShowCmd.Flags().GetString("cursor")
	oldShowStatus, _ := scopeShowCmd.Flags().GetString("status")
	oldShowType, _ := scopeShowCmd.Flags().GetString("type")
	oldShowContexts, _ := scopeShowCmd.Flags().GetStringArray("context")
	contextFlag := scopeShowCmd.Flags().Lookup("context")
	oldContextChanged, oldContextDefValue := contextFlag.Changed, contextFlag.DefValue
	t.Cleanup(func() {
		_ = scopeListCmd.Flags().Set("paginate", fmt.Sprint(oldListPaginate))
		_ = scopeListCmd.Flags().Set("limit", fmt.Sprint(oldListLimit))
		_ = scopeShowCmd.Flags().Set("paginate", fmt.Sprint(oldShowPaginate))
		_ = scopeShowCmd.Flags().Set("limit", fmt.Sprint(oldShowLimit))
		_ = scopeShowCmd.Flags().Set("cursor", oldShowCursor)
		_ = scopeShowCmd.Flags().Set("status", oldShowStatus)
		_ = scopeShowCmd.Flags().Set("type", oldShowType)
		_ = contextFlag.Value.(pflag.SliceValue).Replace(oldShowContexts)
		contextFlag.Changed, contextFlag.DefValue = oldContextChanged, oldContextDefValue
	})

	_ = scopeListCmd.Flags().Set("paginate", "true")
	_ = scopeListCmd.Flags().Set("limit", "7")
	if err := scopeListCmd.RunE(scopeListCmd, nil); err != nil {
		t.Fatalf("proxied scope list: %v", err)
	}
	if stub.catalogRequest.Limit != 7 {
		t.Fatalf("proxied catalog request = %#v, want limit 7", stub.catalogRequest)
	}

	_ = scopeShowCmd.Flags().Set("paginate", "true")
	_ = scopeShowCmd.Flags().Set("limit", "3")
	_ = scopeShowCmd.Flags().Set("status", "completed")
	_ = scopeShowCmd.Flags().Set("type", "task")
	_ = scopeShowCmd.Flags().Set("context", "team-a")
	if err := scopeShowCmd.RunE(scopeShowCmd, []string{"scope-a"}); err != nil {
		t.Fatalf("proxied scope show: %v", err)
	}
	if stub.membersRequest.Limit != 3 || stub.membersRequest.Status != types.ScopeMemberStatusCompleted || stub.membersRequest.Type != types.TypeTask || len(stub.membersRequest.Contexts) != 1 || stub.membersRequest.Contexts[0] != "team-a" {
		t.Fatalf("proxied member request = %#v", stub.membersRequest)
	}

	_ = scopeShowCmd.Flags().Set("paginate", "false")
	_ = scopeShowCmd.Flags().Set("cursor", "opaque")
	if err := scopeShowCmd.RunE(scopeShowCmd, []string{"scope-a"}); err != nil {
		t.Fatalf("proxied scope show from cursor: %v", err)
	}
	if stub.membersRequest.Cursor != "opaque" {
		t.Fatalf("proxied cursor request = %#v, want cursor-driven page", stub.membersRequest)
	}
}

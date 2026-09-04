package main

import (
	"testing"

	"github.com/spf13/cobra"
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

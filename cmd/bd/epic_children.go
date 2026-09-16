package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

var epicChildrenCmd = &cobra.Command{
	Use:   "child-closure <parent-id>",
	Short: "Show authoritative direct children of a parent",
	Long: `Show the direct parent-child rows evaluated by the epic close gate.

The JSON response is a bare array. Each item has "id", "status", and
"storage_plane" ("durable" or "wisp"); "issue_type" is included when present.
Closed children are included. This query does not apply scope or visibility
filters, and a duplicate wisp edge already represented durably is omitted.

Example:
  bd epic child-closure bd-epic --json`,
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("epic-children")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		if usesProxiedServer() {
			return runEpicChildrenProxiedServer(rootCtx, args[0])
		}
		query, ok := store.(storage.EpicChildQueryStore)
		if !ok {
			return HandleErrorRespectJSON("getting epic children: storage backend does not support the authoritative child query")
		}
		children, err := query.GetEpicChildren(rootCtx, args[0])
		if err != nil {
			return HandleErrorRespectJSON("getting epic children: %v", err)
		}
		return renderEpicChildren(args[0], children)
	},
}

func renderEpicChildren(parentID string, children []*types.EpicChild) error {
	if children == nil {
		children = []*types.EpicChild{}
	}
	if jsonOutput {
		return outputJSON(children)
	}
	if len(children) == 0 {
		fmt.Printf("%s has no direct children\n", parentID)
		return nil
	}
	for _, child := range children {
		fmt.Printf("%s (%s) [%s, %s]\n", child.ID, child.Status, child.IssueType, child.StoragePlane)
	}
	return nil
}

func init() {
	readOnlyCommands["child-closure"] = true
	epicCmd.AddCommand(epicChildrenCmd)
}

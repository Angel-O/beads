package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// scopeOperations is the common transactional scope surface. The direct
// route supplies a storage.Transaction; proxied mode supplies its UOW scope
// use case. Keeping the command on this surface avoids duplicating scope rules.
type scopeOperations interface {
	CreateScope(context.Context, *types.Scope, bool) error
	ListScopes(context.Context) ([]*types.Scope, error)
	ListScopeCatalog(context.Context, storage.ScopeCatalogRequest) (*storage.ScopeCatalogPage, error)
	GetScope(context.Context, string) (*types.ScopeDetails, error)
	ListScopeMembers(context.Context, string, storage.ScopeMemberPageRequest) (*storage.ScopeMemberPage, error)
	GetActiveScope(context.Context) (*types.Scope, error)
	ActivateScope(context.Context, string) error
	DeactivateScope(context.Context) error
	AddScopeMembers(context.Context, string, []string) error
	RemoveScopeMembers(context.Context, string, []string) error
	MoveScopeMembers(context.Context, string, string, []string) error
}

func runScopeWrite(ctx context.Context, commitMsg string, fn func(scopeOperations) error) error {
	if usesProxiedServer() {
		if uowProvider == nil {
			return fmt.Errorf("proxied-server UOW provider not initialized")
		}
		return uow.RunTx(ctx, uowProvider, func(ctx context.Context, uw uow.UnitOfWork) (string, error) {
			if err := fn(uw.ScopeUseCase()); err != nil {
				return "", err
			}
			return commitMsg, nil
		})
	}
	return transact(ctx, store, commitMsg, func(tx storage.Transaction) error {
		return fn(tx)
	})
}

func runScopeRead[T any](ctx context.Context, fn func(scopeOperations) (T, error)) (T, error) {
	if usesProxiedServer() {
		if uowProvider == nil {
			var zero T
			return zero, fmt.Errorf("proxied-server UOW provider not initialized")
		}
		return uow.RunTxRead(ctx, uowProvider, func(ctx context.Context, uw uow.UnitOfWork) (T, error) {
			return fn(uw.ScopeUseCase())
		})
	}
	return fn(store)
}

func runScopeCommand(name string, fn func() error) error {
	evt := metrics.NewCommandEvent(name)
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()
	return fn()
}

var scopeCmd = &cobra.Command{
	Use:     "scope",
	GroupID: "issues",
	Short:   "Manage named issue scopes",
}

var scopeCreateCmd = &cobra.Command{
	Use:           "create <id> <name>",
	Short:         "Create a scope",
	Args:          cobra.ExactArgs(2),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScopeCommand("scope-create", func() error {
			CheckReadonly("scope create")
			activate, _ := cmd.Flags().GetBool("activate")
			scope := &types.Scope{ID: args[0], Name: args[1]}
			if err := runScopeWrite(rootCtx, "bd: create scope", func(ops scopeOperations) error {
				return ops.CreateScope(rootCtx, scope, activate)
			}); err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			if jsonOutput {
				return outputJSON(scope)
			}
			fmt.Printf("%s Created scope: %s (%s)\n", ui.RenderPass("✓"), scope.ID, scope.Name)
			return nil
		})
	},
}

// Paged scope reads are an additive JSON contract; the unflagged branches below
// intentionally keep the legacy slice and ScopeDetails responses unchanged.
var scopeListCmd = &cobra.Command{
	Use:           "list",
	Short:         "List scopes",
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScopeCommand("scope-list", func() error {
			paginate, _ := cmd.Flags().GetBool("paginate")
			limit, _ := cmd.Flags().GetInt("limit")
			cursor, _ := cmd.Flags().GetString("cursor")
			if cursor != "" {
				paginate = true
			}
			if paginate {
				if !jsonOutput {
					return HandleErrorRespectJSON("scope pagination requires --json")
				}
				page, err := runScopeRead(rootCtx, func(ops scopeOperations) (*storage.ScopeCatalogPage, error) {
					return ops.ListScopeCatalog(rootCtx, storage.ScopeCatalogRequest{Limit: limit, Cursor: cursor})
				})
				if err != nil {
					return HandleErrorRespectJSON("%v", err)
				}
				return outputJSON(page)
			}
			if limit != 0 {
				return HandleErrorRespectJSON("--limit requires --paginate")
			}
			scopes, err := runScopeRead(rootCtx, func(ops scopeOperations) ([]*types.Scope, error) {
				return ops.ListScopes(rootCtx)
			})
			if err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			if scopes == nil {
				scopes = []*types.Scope{}
			}
			if jsonOutput {
				return outputJSON(scopes)
			}
			for _, scope := range scopes {
				fmt.Printf("%s\t%s\n", scope.ID, scope.Name)
			}
			return nil
		})
	},
}

var scopeShowCmd = &cobra.Command{
	Use:           "show <id>",
	Short:         "Show a scope and its members",
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScopeCommand("scope-show", func() error {
			paginate, _ := cmd.Flags().GetBool("paginate")
			limit, _ := cmd.Flags().GetInt("limit")
			cursor, _ := cmd.Flags().GetString("cursor")
			status, _ := cmd.Flags().GetString("status")
			issueType, _ := cmd.Flags().GetString("type")
			contexts, _ := cmd.Flags().GetStringArray("context")
			if cursor != "" {
				paginate = true
			}
			if status != "" || issueType != "" || len(contexts) > 0 {
				if !paginate {
					return HandleErrorRespectJSON("scope member filters require --paginate")
				}
			}
			if paginate {
				if !jsonOutput {
					return HandleErrorRespectJSON("scope pagination requires --json")
				}
				page, err := runScopeRead(rootCtx, func(ops scopeOperations) (*storage.ScopeMemberPage, error) {
					return ops.ListScopeMembers(rootCtx, args[0], storage.ScopeMemberPageRequest{
						Status:   types.ScopeMemberStatus(status),
						Type:     types.IssueType(issueType),
						Contexts: contexts,
						Limit:    limit,
						Cursor:   cursor,
					})
				})
				if err != nil {
					return HandleErrorRespectJSON("%v", err)
				}
				return outputJSON(page)
			}
			if limit != 0 {
				return HandleErrorRespectJSON("--limit requires --paginate")
			}
			details, err := runScopeRead(rootCtx, func(ops scopeOperations) (*types.ScopeDetails, error) {
				return ops.GetScope(rootCtx, args[0])
			})
			if err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			if jsonOutput {
				return outputJSON(details)
			}
			fmt.Printf("%s (%s)\n", details.ID, details.Name)
			for _, member := range details.Members {
				fmt.Printf("  %s\n", member.ID)
			}
			return nil
		})
	},
}

var scopeActiveCmd = &cobra.Command{
	Use:           "active",
	Short:         "Show the active scope",
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScopeCommand("scope-active", func() error {
			active, err := runScopeRead(rootCtx, func(ops scopeOperations) (*types.Scope, error) {
				return ops.GetActiveScope(rootCtx)
			})
			if err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			if active == nil {
				if jsonOutput {
					return outputJSON(nil)
				}
				fmt.Println("No active scope")
				return nil
			}
			if jsonOutput {
				return outputJSON(active)
			}
			fmt.Printf("%s (%s)\n", active.ID, active.Name)
			return nil
		})
	},
}

var scopeActivateCmd = &cobra.Command{
	Use:           "activate <id>",
	Short:         "Activate a scope",
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScopeCommand("scope-activate", func() error {
			CheckReadonly("scope activate")
			if err := runScopeWrite(rootCtx, "bd: activate scope", func(ops scopeOperations) error {
				return ops.ActivateScope(rootCtx, args[0])
			}); err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			return outputScopeMutation("activated", map[string]any{"scope_id": args[0]})
		})
	},
}

var scopeDeactivateCmd = &cobra.Command{
	Use:           "deactivate",
	Short:         "Deactivate the active scope",
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScopeCommand("scope-deactivate", func() error {
			CheckReadonly("scope deactivate")
			if err := runScopeWrite(rootCtx, "bd: deactivate scope", func(ops scopeOperations) error {
				return ops.DeactivateScope(rootCtx)
			}); err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			return outputScopeMutation("deactivated", nil)
		})
	},
}

var scopeAddCmd = &cobra.Command{
	Use:           "add <scope-id> <issue-id>...",
	Short:         "Add issues to a scope",
	Args:          cobra.MinimumNArgs(2),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScopeCommand("scope-add", func() error {
			CheckReadonly("scope add")
			return mutateScopeMembers("added", args[0], args[1:], func(ops scopeOperations) error {
				return ops.AddScopeMembers(rootCtx, args[0], args[1:])
			})
		})
	},
}

var scopeRemoveCmd = &cobra.Command{
	Use:           "remove <scope-id> <issue-id>...",
	Short:         "Remove issues from a scope",
	Args:          cobra.MinimumNArgs(2),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScopeCommand("scope-remove", func() error {
			CheckReadonly("scope remove")
			return mutateScopeMembers("removed", args[0], args[1:], func(ops scopeOperations) error {
				return ops.RemoveScopeMembers(rootCtx, args[0], args[1:])
			})
		})
	},
}

var scopeMoveCmd = &cobra.Command{
	Use:           "move <source-scope-id> <target-scope-id> <issue-id>...",
	Short:         "Move issues between scopes",
	Args:          cobra.MinimumNArgs(3),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScopeCommand("scope-move", func() error {
			CheckReadonly("scope move")
			if err := runScopeWrite(rootCtx, "bd: move scope members", func(ops scopeOperations) error {
				return ops.MoveScopeMembers(rootCtx, args[0], args[1], args[2:])
			}); err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			return outputScopeMutation("moved", map[string]any{
				"source_scope_id": args[0],
				"target_scope_id": args[1],
				"issue_ids":       args[2:],
			})
		})
	},
}

func mutateScopeMembers(status, scopeID string, issueIDs []string, fn func(scopeOperations) error) error {
	verb := "add"
	if status == "removed" {
		verb = "remove"
	}
	if err := runScopeWrite(rootCtx, "bd: "+verb+" scope members", fn); err != nil {
		return HandleErrorRespectJSON("%v", err)
	}
	return outputScopeMutation(status, map[string]any{
		"scope_id":  scopeID,
		"issue_ids": issueIDs,
	})
}

func outputScopeMutation(status string, fields map[string]any) error {
	if jsonOutput {
		result := map[string]any{"status": status}
		for key, value := range fields {
			result[key] = value
		}
		return outputJSON(result)
	}
	verb := status
	if len(verb) > 0 {
		verb = string(verb[0]-'a'+'A') + verb[1:]
	}
	fmt.Printf("%s %s scope\n", ui.RenderPass("✓"), verb)
	return nil
}

func init() {
	scopeCreateCmd.Flags().Bool("activate", false, "Activate the new scope")
	scopeListCmd.Flags().Bool("paginate", false, "Return a bounded JSON page")
	scopeListCmd.Flags().Int("limit", 0, "Maximum scopes to return in the page")
	scopeListCmd.Flags().String("cursor", "", "Opaque catalog page cursor")
	scopeShowCmd.Flags().Bool("paginate", false, "Return a bounded JSON page")
	scopeShowCmd.Flags().Int("limit", 0, "Maximum members to return in the page")
	scopeShowCmd.Flags().String("cursor", "", "Opaque member page cursor")
	scopeShowCmd.Flags().String("status", "", "Filter members by open, completed, or ready")
	scopeShowCmd.Flags().String("type", "", "Filter members by exact issue type")
	scopeShowCmd.Flags().StringArray("context", nil, "Filter members by exact context membership (repeatable)")
	scopeCmd.AddCommand(scopeCreateCmd, scopeListCmd, scopeShowCmd, scopeActiveCmd, scopeActivateCmd, scopeDeactivateCmd, scopeAddCmd, scopeRemoveCmd, scopeMoveCmd)
	rootCmd.AddCommand(scopeCmd)
}

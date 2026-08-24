package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
)

var (
	historyLimit   int
	historyEvents  bool
	historyIDsFile string
)

var historyCmd = &cobra.Command{
	Use:     "history <id> | history --ids-file <path|-> --json",
	GroupID: "views",
	Short:   "Show version history for an issue",
	Long: `Show the complete version history of an issue, including all commits
where the issue was modified.

Bulk mode reads newline-delimited exact IDs without partial-ID resolution. It
trims whitespace, ignores blank lines, deduplicates and sorts IDs, and emits an
empty snapshots array for a missing ID. --limit applies to each issue group.

Examples:
  bd history bd-123           # Show all history for issue bd-123
  bd history bd-123 --limit 5 # Show last 5 changes
  bd history bd-123 --events  # Show database audit events
  bd history --ids-file - --json < issue-ids.txt # Bulk exact-ID snapshots`,
	Args:          cobra.MaximumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("history")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		if historyIDsFile != "" {
			if len(args) != 0 {
				return HandleErrorRespectJSON("an issue argument cannot be combined with --ids-file")
			}
			if !jsonOutput {
				return HandleErrorRespectJSON("--ids-file requires --json")
			}
			if historyEvents {
				return HandleErrorRespectJSON("--events is not supported with --ids-file")
			}
			ids, err := readHistoryIDs(historyIDsFile, cmd.InOrStdin())
			if err != nil {
				return HandleErrorRespectJSON("reading history IDs: %v", err)
			}
			if usesProxiedServer() {
				return runBulkHistoryProxiedServer(rootCtx, ids, historyLimit)
			}
			bulkBackend, ok := storage.UnwrapStore(store).(bulkHistoryBackend)
			if !ok {
				return HandleErrorRespectJSON("the active storage backend does not support bulk history")
			}
			return runBulkHistory(rootCtx, bulkBackend, ids, historyLimit)
		}
		if len(args) != 1 {
			return HandleErrorRespectJSON("exactly one issue ID is required unless --ids-file is used")
		}

		issueID := args[0]

		if usesProxiedServer() {
			// Proxied mode has no local store to resolve against, so partial-ID
			// resolution is unavailable here -- pass the raw ID through and let
			// the proxied server's own lookup handle it.
			return runHistoryProxiedServer(rootCtx, issueID, historyLimit, historyEvents)
		}

		if resolved, err := utils.ResolvePartialID(rootCtx, store, issueID); err == nil {
			issueID = resolved
		} else if errors.Is(err, utils.ErrAmbiguousID) {
			return HandleErrorRespectJSON("%v", err)
		}
		// Not-found IDs fall through unchanged -- the queries below just find
		// nothing and hit the existing "No history found" path (GH#3502), so we
		// don't hard-error on an id that never existed.

		return runHistory(rootCtx, store, issueID, historyLimit, historyEvents)
	},
}

type historyBackend interface {
	History(ctx context.Context, id string) ([]*storage.HistoryEntry, error)
	IterEvents(ctx context.Context, id string, limit int) (storage.Iter[types.Event], error)
}

type bulkHistoryBackend interface {
	BulkHistory(ctx context.Context, ids []string) ([]storage.IssueHistory, error)
}

type bulkHistoryEnvelope struct {
	SchemaVersion int                    `json:"schema_version"`
	Issues        []storage.IssueHistory `json:"issues"`
}

func runBulkHistory(ctx context.Context, backend bulkHistoryBackend, issueIDs []string, limit int) error {
	groups, err := backend.BulkHistory(ctx, issueIDs)
	if err != nil {
		return HandleErrorRespectJSON("failed to get bulk history: %v", err)
	}
	if limit > 0 {
		for i := range groups {
			if limit < len(groups[i].Entries) {
				groups[i].Entries = groups[i].Entries[:limit]
			}
		}
	}
	return outputJSONRaw(bulkHistoryEnvelope{SchemaVersion: 1, Issues: groups})
}

func runHistory(ctx context.Context, backend historyBackend, issueID string, limit int, showEvents bool) error {
	if showEvents {
		events, err := collectHistoryEvents(ctx, backend, issueID, limit)
		if err != nil {
			return HandleErrorRespectJSON("failed to get history events: %v", err)
		}
		if jsonOutput {
			return outputJSON(events)
		}
		printHistoryEvents(issueID, events)
		return nil
	}

	history, err := backend.History(ctx, issueID)
	if err != nil {
		return HandleErrorRespectJSON("failed to get history: %v", err)
	}

	if len(history) == 0 {
		if jsonOutput {
			return outputJSON(history)
		}
		fmt.Printf("No history found for issue %s\n", issueID)
		return nil
	}

	if limit > 0 && limit < len(history) {
		history = history[:limit]
	}

	if jsonOutput {
		return outputJSON(history)
	}

	fmt.Printf("\n%s History for %s (%d entries)\n\n",
		ui.RenderAccent("📜"), issueID, len(history))

	for i, entry := range history {
		fmt.Printf("%s %s\n",
			ui.RenderMuted(entry.CommitHash[:8]),
			ui.RenderMuted(entry.CommitDate.Format("2006-01-02 15:04:05")))
		fmt.Printf("  Author: %s\n", entry.Committer)

		if entry.Issue != nil {
			statusIcon := ui.GetStatusIcon(string(entry.Issue.Status))
			fmt.Printf("  %s %s: %s [P%d - %s]\n",
				statusIcon,
				entry.Issue.ID,
				entry.Issue.Title,
				entry.Issue.Priority,
				entry.Issue.Status)
		}

		if i < len(history)-1 {
			fmt.Println()
		}
	}
	fmt.Println()
	return nil
}

func init() {
	historyCmd.Flags().IntVar(&historyLimit, "limit", 0, "Limit number of history entries (0 = all)")
	historyCmd.Flags().BoolVar(&historyEvents, "events", false, "Show database audit events instead of commit snapshots")
	historyCmd.Flags().StringVar(&historyIDsFile, "ids-file", "", "Read exact issue IDs, one per line (use - for stdin; requires --json)")
	historyCmd.ValidArgsFunction = issueIDCompletion
	rootCmd.AddCommand(historyCmd)
}

func readHistoryIDs(path string, stdin io.Reader) ([]string, error) {
	var reader io.Reader
	if path == "-" {
		reader = stdin
	} else {
		// #nosec G304 -- the path is explicitly supplied by the user.
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer func() { _ = file.Close() }()
		reader = file
	}

	var ids []string
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		id := strings.TrimSpace(scanner.Text())
		if id != "" {
			ids = append(ids, id)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func collectHistoryEvents(ctx context.Context, backend historyBackend, issueID string, limit int) ([]types.Event, error) {
	iter, err := backend.IterEvents(ctx, issueID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = iter.Close() }()

	var events []types.Event
	for iter.Next(ctx) {
		event := iter.Value()
		if event != nil {
			events = append(events, *event)
		}
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func printHistoryEvents(issueID string, events []types.Event) {
	if len(events) == 0 {
		fmt.Printf("No history events found for issue %s\n", issueID)
		return
	}

	fmt.Printf("\n%s History events for %s (%d entries)\n\n",
		ui.RenderAccent("📜"), issueID, len(events))
	for i, event := range events {
		fmt.Printf("%s %s by %s\n",
			ui.RenderMuted(event.CreatedAt.Format("2006-01-02 15:04:05")),
			event.EventType,
			event.Actor)
		if event.OldValue != nil && *event.OldValue != "" {
			fmt.Printf("  Old: %s\n", *event.OldValue)
		}
		if event.NewValue != nil && *event.NewValue != "" {
			fmt.Printf("  New: %s\n", *event.NewValue)
		}
		if event.Comment != nil && *event.Comment != "" {
			fmt.Printf("  Comment: %s\n", *event.Comment)
		}
		if i < len(events)-1 {
			fmt.Println()
		}
	}
	fmt.Println()
}

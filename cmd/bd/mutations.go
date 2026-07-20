package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/storage/uow"
)

// mutationFollowPollInterval is how often `bd mutations tail --follow` polls the
// journal table for new rows. The journal is a local table read, so polling is
// cheap; a one-second cadence keeps a live consumer responsive without busy-waiting.
const mutationFollowPollInterval = time.Second

// bd mutations reads and manages the durable mutations journal
// (bd_mutations_journal). The journal is an append-only, seq-ordered record of
// every committed issue mutation, written in the same transaction as the
// mutation. Scripts and integrations tail it to replay the exact history of a
// workspace. It is OFF by default; enable with `bd config set mutations-journal
// true` (or BD_MUTATIONS_JOURNAL=1).

var mutationsCmd = &cobra.Command{
	Use:     "mutations",
	GroupID: "maint",
	Short:   "Read and manage the durable mutations journal",
	Long: `Read and manage the durable mutations journal (bd_mutations_journal).

The journal records every committed issue mutation as an ordered, replayable
row. Enable it with 'bd config set mutations-journal true' (or
BD_MUTATIONS_JOURNAL=1). Records are emitted only while it is enabled.`,
}

var mutationsTailCmd = &cobra.Command{
	Use:   "tail",
	Short: "Print journal records after a sequence number (JSON lines)",
	Long: `Print mutation journal records with seq greater than --since, in order.

Each line is a JSON record:
  {"seq":N,"ts":"...","op":"create|update|close|delete|dep_add|dep_remove",
   "issue_id":"...","issue":{...|null},"dep":{"kind":..,"target":..}}

Record contract (stable for external consumers):
  seq       int64   engine-assigned, strictly increasing, never reused or reset
  ts        string  UTC timestamp the row was committed
  op        string  one of the six ops above
  issue_id  string  the mutated issue's id
  issue     object  full issue state AFTER the mutation; null on delete
  dep       object  {"kind","target"} for dep_add / dep_remove; omitted otherwise

Poll with the highest seq seen to consume new mutations incrementally, or pass
--follow to keep printing new records as they are committed (Ctrl-C to stop).`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		since, _ := cmd.Flags().GetInt64("since")
		limit, _ := cmd.Flags().GetInt("limit")
		follow, _ := cmd.Flags().GetBool("follow")
		return runMutationsTail(rootCtx, since, limit, follow)
	},
}

var mutationsExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Print the entire journal from the beginning (JSON lines)",
	Long: `Print every mutation journal record from seq 1, in order, as JSON lines.

Equivalent to 'bd mutations tail --since 0'.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		limit, _ := cmd.Flags().GetInt("limit")
		return runMutationsTail(rootCtx, 0, limit, false)
	},
}

var mutationsPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Delete journal records below a sequence number (retention)",
	Long: `Delete mutation journal records with seq less than --before.

Use after a consumer has durably processed everything up to that seq. The
journal is clone-local operational state, so pruning never affects issue data.

Two retention floors protect recent history and can only reduce what a prune
removes (a lagging consumer is never cut off by an over-eager --before):
  mutations-journal-retain-days   keep every row younger than N days
  mutations-journal-retain-rows   always keep the newest N rows

Consumers are responsible for tracking their own watermark (the highest seq they
have durably processed). Pruned history cannot be recovered from the workspace —
the journal is the only local copy. On a Dolt backend, pair a prune with
'dolt gc' to reclaim the space, since the table is working-set (dolt_ignored)
state that ordinary Dolt commits never garbage-collect.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		before, _ := cmd.Flags().GetInt64("before")
		if before <= 0 {
			return HandleErrorRespectJSON("--before must be a positive sequence number")
		}
		return runMutationsPrune(rootCtx, before)
	},
}

func init() {
	mutationsTailCmd.Flags().Int64("since", 0, "return records with seq greater than this value")
	mutationsTailCmd.Flags().Int("limit", 0, "maximum number of records to return (0 = no limit)")
	mutationsTailCmd.Flags().Bool("follow", false, "keep printing new records as they are committed (Ctrl-C to stop)")
	mutationsExportCmd.Flags().Int("limit", 0, "maximum number of records to return (0 = no limit)")
	mutationsPruneCmd.Flags().Int64("before", 0, "delete records with seq less than this value")

	mutationsCmd.AddCommand(mutationsTailCmd)
	mutationsCmd.AddCommand(mutationsExportCmd)
	mutationsCmd.AddCommand(mutationsPruneCmd)
	rootCmd.AddCommand(mutationsCmd)
}

// mutationRecord is one journal line rendered to callers. Issue and Dep are raw
// JSON so the stored payloads are not re-encoded.
type mutationRecord struct {
	Seq     int64           `json:"seq"`
	TS      string          `json:"ts"`
	Op      string          `json:"op"`
	IssueID string          `json:"issue_id"`
	Issue   json.RawMessage `json:"issue"`
	Dep     json.RawMessage `json:"dep,omitempty"`
}

// tailSelect builds the read query. CAST(ts AS CHAR) normalizes the DATETIME to
// a string across drivers.
func tailSelect(limit int) string {
	q := `SELECT seq, CAST(ts AS CHAR), op, issue_id, issue_json, dep_json
	      FROM bd_mutations_journal WHERE seq > ? ORDER BY seq ASC`
	if limit > 0 {
		q += " LIMIT " + strconv.Itoa(limit)
	}
	return q
}

func runMutationsTail(ctx context.Context, since int64, limit int, follow bool) error {
	enc := json.NewEncoder(os.Stdout)
	emit := func(from int64) (int64, error) {
		rows, err := readJournal(ctx, from, limit)
		if err != nil {
			return from, err
		}
		for _, r := range rows {
			if err := enc.Encode(r); err != nil {
				return from, err
			}
			if r.Seq > from {
				from = r.Seq
			}
		}
		return from, nil
	}

	last, err := emit(since)
	if err != nil {
		return HandleErrorRespectJSON("reading mutations journal: %v", err)
	}
	if !follow {
		return nil
	}
	// Follow: poll for rows with seq beyond the last one emitted. The journal is
	// a local table read, so a modest poll cadence is cheap. Stop on Ctrl-C
	// (rootCtx is signal-aware), reporting no error for a clean interruption.
	ticker := time.NewTicker(mutationFollowPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if last, err = emit(last); err != nil {
				return HandleErrorRespectJSON("reading mutations journal: %v", err)
			}
		}
	}
}

func runMutationsPrune(ctx context.Context, before int64) error {
	retainDays := config.GetInt("mutations-journal-retain-days")
	retainRows := config.GetInt("mutations-journal-retain-rows")
	n, err := pruneJournal(ctx, before, retainDays, retainRows)
	if err != nil {
		return HandleErrorRespectJSON("pruning mutations journal: %v", err)
	}
	return reportMutationsPruned(n, before)
}

func reportMutationsPruned(n, before int64) error {
	if jsonOutput {
		return outputJSON(map[string]any{"pruned": n})
	}
	fmt.Printf("Pruned %d mutation journal record(s) below seq %d\n", n, before)
	return nil
}

// journalAccessor returns the active store's mutations-journal capability. The
// embedded store and the server-mode store both provide it (via their own
// transaction machinery); a backend that does not is reported as unsupported.
func journalAccessor() (storage.MutationsJournalAccessor, error) {
	if store == nil {
		return nil, fmt.Errorf("no database connection available (%s)", diagHint())
	}
	acc, ok := storage.UnwrapStore(store).(storage.MutationsJournalAccessor)
	if !ok {
		return nil, fmt.Errorf("storage backend does not support the mutations journal")
	}
	return acc, nil
}

// readJournal reads records with seq greater than since in whichever plumbing is
// active. The proxied-server path speaks raw SQL through the unit of work; every
// other backend uses the MutationsJournalAccessor capability.
func readJournal(ctx context.Context, since int64, limit int) ([]mutationRecord, error) {
	if usesProxiedServer() {
		return readJournalProxied(ctx, tailSelect(limit), since)
	}
	acc, err := journalAccessor()
	if err != nil {
		return nil, err
	}
	rows, err := acc.ReadMutationsJournal(ctx, since, limit)
	if err != nil {
		return nil, err
	}
	out := make([]mutationRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, buildRecord(r.Seq, r.TS, r.Op, r.IssueID, r.IssueJSON, r.DepJSON))
	}
	return out, nil
}

// pruneJournal deletes records below before honoring the retain floors in
// whichever plumbing is active.
func pruneJournal(ctx context.Context, before int64, retainDays, retainRows int) (int64, error) {
	if usesProxiedServer() {
		return pruneJournalProxied(ctx, before, retainDays, retainRows)
	}
	acc, err := journalAccessor()
	if err != nil {
		return 0, err
	}
	return acc.PruneMutationsJournal(ctx, before, retainDays, retainRows)
}

// readJournalProxied reads journal rows through the proxied-server unit of work.
func readJournalProxied(ctx context.Context, query string, args ...any) ([]mutationRecord, error) {
	if uowProvider == nil {
		return nil, fmt.Errorf("proxied-server UOW provider not initialized")
	}
	res, err := uow.RunTxRead(ctx, uowProvider, func(ctx context.Context, uw uow.UnitOfWork) (*domain.RawSQLResult, error) {
		return uw.RawSQLUseCase().Query(ctx, query, args...)
	})
	if err != nil {
		return nil, err
	}
	return rawResultToRecords(res)
}

// pruneJournalProxied applies the retain floors through the proxied-server unit
// of work, reusing the shared floor helpers.
func pruneJournalProxied(ctx context.Context, before int64, retainDays, retainRows int) (int64, error) {
	if uowProvider == nil {
		return 0, fmt.Errorf("proxied-server UOW provider not initialized")
	}
	var (
		rowsCeil   int64
		rowsCeilOK bool
	)
	if retainRows > 0 {
		res, err := uow.RunTxRead(ctx, uowProvider, func(ctx context.Context, uw uow.UnitOfWork) (*domain.RawSQLResult, error) {
			return uw.RawSQLUseCase().Query(ctx, issueops.MutationsPruneRowsCeilQuery(), retainRows)
		})
		if err != nil {
			return 0, err
		}
		if res == nil || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
			// The whole journal is inside the retained window: nothing to prune.
			return 0, nil
		}
		rowsCeil, rowsCeilOK = toInt64(res.Rows[0][0]), true
	}
	where, args := issueops.BuildMutationsPruneWhere(before, retainDays, time.Now().UTC(), rowsCeil, rowsCeilOK)
	return uow.RunTxResult(ctx, uowProvider, func(ctx context.Context, uw uow.UnitOfWork) (int64, string, error) {
		n, err := uw.RawSQLUseCase().Exec(ctx, "DELETE FROM bd_mutations_journal WHERE "+where, args...)
		if err != nil {
			return 0, "", err
		}
		return n, "bd: prune mutations journal", nil
	})
}

func rawResultToRecords(res *domain.RawSQLResult) ([]mutationRecord, error) {
	if res == nil {
		return nil, nil
	}
	out := make([]mutationRecord, 0, len(res.Rows))
	for _, row := range res.Rows {
		if len(row) < 6 {
			return nil, fmt.Errorf("unexpected journal row shape: %d columns", len(row))
		}
		out = append(out, buildRecord(
			toInt64(row[0]), toString(row[1]), toString(row[2]),
			toString(row[3]), toString(row[4]), toString(row[5]),
		))
	}
	return out, nil
}

func buildRecord(seq int64, ts, op, issueID, issueJS, depJS string) mutationRecord {
	rec := mutationRecord{Seq: seq, TS: ts, Op: op, IssueID: issueID, Issue: json.RawMessage("null")}
	if issueJS != "" {
		rec.Issue = json.RawMessage(issueJS)
	}
	if depJS != "" {
		rec.Dep = json.RawMessage(depJS)
	}
	return rec
}

func toString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case []byte:
		n, _ := strconv.ParseInt(string(t), 10, 64)
		return n
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	default:
		return 0
	}
}

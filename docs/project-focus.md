# Project Focus

The current integration and reference branch is:

```text
feat/bulk-history-read
```

Use this branch, rather than `main`, as the base and integration reference for
the current bulk History work.

## Git Workflow

- Fetch and update `feat/bulk-history-read` before creating a worktree.
- Create dependent task branches and worktrees from its latest tip.
- Keep the bulk History implementation aligned with the Viewer consumer on
  `beads_viewer` branch `feature/repository-aware-correlations`.
- Do not merge or retarget this integration to `main` unless the user
  explicitly ends the feature-integration phase.
- The reference branch is an accepted integration artifact; merging into
  `main` is not required for this cross-repository work.

## Current Architecture Focus

The active feature area is the bulk History contract used by the Viewer:

- `bd history --json` accepts multiple exact positional issue IDs.
- Bulk IDs can also be supplied through stdin for external integrations.
- Inputs are normalized, deduplicated, bounded, and returned in deterministic
  issue groups.
- `--limit` applies per issue group while preserving the existing single-ID
  history behavior and audit-event mode.
- Embedded and proxied/server execution paths must expose the same contract.

Useful starting points:

- `cmd/bd/history.go`
- `cmd/bd/history_proxied_server.go`
- `internal/storage/history_viewer.go`
- `internal/storage/issueops/history.go`
- `backend/bulk_history_test.go`
- `cmd/bd/history_bulk_input_test.go`

The corresponding Viewer consumer is tracked in the separate
`beads_viewer` repository. Keep the command shape and JSON envelope compatible
with that integration before extending the bulk History surface.

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
- Do not merge or retarget this integration to `main` unless the user
  explicitly ends the feature-integration phase.
- The reference branch is an accepted integration artifact; merging into
  `main` is not required for this cross-repository work.

## Hub Workflow

- Use `wbd` for private Beads Hub issue tracking, correlations, and status
  changes across repositories.
- Do not use raw `bd`, `bv`, or `br` for Hub operations.
- Keep private Hub IDs and `ctx:` identities out of branches, commits, source,
  tests, documentation, and other Git-visible metadata.
- Treat this repository's reference branch as the accepted integration point
  for the current work unless the user explicitly changes that policy.

## Current Architecture Focus

The active feature area is bulk History support in this repository:

- Bulk History reads are bounded and deterministic.
- Positional and stdin-driven bulk inputs share one validation contract.
- Single-item history behavior and audit-event handling remain separate.
- Embedded and proxied/server execution paths should preserve the same
  externally visible behavior.

Useful starting points:

- `cmd/bd/history.go`
- `cmd/bd/history_proxied_server.go`
- `internal/storage/history_viewer.go`
- `internal/storage/issueops/history.go`
- `backend/bulk_history_test.go`
- `cmd/bd/history_bulk_input_test.go`

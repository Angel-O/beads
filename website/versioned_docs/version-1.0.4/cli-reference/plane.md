---
id: plane
title: bd plane
slug: /cli-reference/plane
sidebar_position: 999
---

<!-- AUTO-GENERATED: do not edit manually -->
Generated from `bd help --doc plane`

## bd plane

Synchronize issues between beads and Plane (https://github.com/makeplane/plane).

Targets self-hosted Plane Community Edition via the /api/v1 REST API.
Work items are linked by Plane's native external_id/external_source fields
(external_id = bead ID), making creation idempotent and duplicate-safe.

Configuration:
  bd config set plane.api_key "YOUR_API_KEY"   # personal token from Plane profile settings
  bd config set plane.base_url "https://plane.example.com"
  bd config set plane.workspace "myworkspace"  # workspace slug
  bd config set plane.project_id "UUID"        # target project UUID

Environment variables (alternative to config):
  PLANE_API_KEY     - Plane personal API token
  PLANE_BASE_URL    - Instance root URL
  PLANE_WORKSPACE   - Workspace slug
  PLANE_PROJECT_ID  - Project UUID

Field mapping notes:
  - Plane CE has no work item types and no blocked state: beads issue
    types and blocked status round-trip via beads:type:* and
    beads:blocked labels on the Plane side.
  - Status maps through Plane state groups (backlog/unstarted/started/
    completed/cancelled), not state names, so custom project states work.
  - Descriptions convert between Markdown (beads) and HTML (Plane).

Examples:
  bd plane sync --pull         # Import issues from Plane
  bd plane sync --push         # Export issues to Plane
  bd plane sync                # Bidirectional sync (pull then push)
  bd plane sync --dry-run      # Preview sync without changes
  bd plane status              # Show sync status

```
bd plane
```

### bd plane status

Show the current Plane sync status, including:
  - Last sync timestamp
  - Configuration status
  - Number of issues with Plane links
  - Issues pending push (no external_ref)

```
bd plane status
```

### bd plane sync

Synchronize issues between beads and Plane.

Modes:
  --pull         Import issues from Plane into beads
  --push         Export issues from beads to Plane
  (no flags)     Bidirectional sync: pull then push, with conflict resolution

Filtering:
  --state open|closed|all   Restrict sync to open or closed issues
  --include-ephemeral       Include ephemeral issues (wisps, etc.) when
                            pushing; default is to keep them local

Conflict Resolution:
  By default, newer timestamp wins. Override with:
  --prefer-local   Always prefer local beads version
  --prefer-plane   Always prefer Plane version

Examples:
  bd plane sync --pull                # Import from Plane
  bd plane sync --push --create-only  # Push new issues only
  bd plane sync --dry-run             # Preview without changes
  bd plane sync --prefer-local        # Bidirectional, local wins

```
bd plane sync [flags]
```

**Flags:**

```
      --create-only         Only create new issues, don't update existing
      --dry-run             Preview sync without making changes
      --include-ephemeral   Include ephemeral issues (wisps, etc.) when pushing to Plane
      --issues string       Comma-separated bead IDs to sync selectively (e.g., bd-abc,bd-def). Mutually exclusive with --parent.
      --parent string       Limit push to this bead and its descendants (push only). Mutually exclusive with --issues.
      --prefer-local        Prefer local version on conflicts
      --prefer-plane        Prefer Plane version on conflicts
      --pull                Pull issues from Plane
      --push                Push issues to Plane
      --state string        Issue state to sync: open, closed, all (default "all")
```

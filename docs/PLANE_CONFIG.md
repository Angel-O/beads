# Plane Integration Configuration

Synchronize issues between beads and [Plane](https://github.com/makeplane/plane),
the open-source project tracker. The adapter targets self-hosted Plane
**Community Edition** via the `/api/v1/` REST API and was built against the
CE v1.3.x contract.

## Quick Start

```bash
# Set required config
bd config set plane.api_key "YOUR_API_KEY"     # personal token (Profile Settings -> API tokens)
bd config set plane.base_url "https://plane.example.com"
bd config set plane.workspace "myworkspace"    # workspace slug from the URL
bd config set plane.project_id "PROJECT_UUID"  # target project UUID

# Or use environment variables
export PLANE_API_KEY=...
export PLANE_BASE_URL=https://plane.example.com
export PLANE_WORKSPACE=myworkspace
export PLANE_PROJECT_ID=...

# Sync (bidirectional)
bd plane sync

# Pull only (import from Plane)
bd plane sync --pull

# Push only (export to Plane)
bd plane sync --push

# Preview without making changes
bd plane sync --dry-run

# Show sync status
bd plane status
```

`plane.api_key` is a **yaml-only secret**: it is stored in local
`config.yaml`, never in the Dolt database, so it cannot leak through
`bd dolt push`.

## How Issues Are Linked

The adapter uses Plane's native `external_id`/`external_source` fields:
every work item created from beads carries `external_source = "beads"` and
`external_id = <bead ID>`. This makes creation **idempotent** — if a
previous sync was interrupted after the API call but before the local
write-back, the next push receives Plane's 409 conflict with the existing
work item UUID and reuses it instead of creating a duplicate.

On the beads side, the link is the `external_ref` field, set to the Plane
web URL:

```
https://plane.example.com/<workspace>/projects/<project-uuid>/issues/<issue-uuid>
```

Pre-linking works like other trackers: `bd create "Title" --external-ref
<plane-issue-url>` ties a bead to an existing Plane work item so push
updates it instead of creating a new one.

## Default Mappings

### Priority Mapping

Bijective — every value survives a round trip:

| beads | Plane  |
| ----- | ------ |
| 0     | urgent |
| 1     | high   |
| 2     | medium |
| 3     | low    |
| 4     | none   |

### Status Mapping

Plane states are per-project entities; the adapter maps through their
stable **state groups**, so custom state names work without configuration.
When pushing, the project's default state for the group is used (or the
first state in that group).

| beads status | Plane state group        |
| ------------ | ------------------------ |
| open         | unstarted                |
| in_progress  | started                  |
| blocked      | started + `beads:blocked` label |
| hooked       | started                  |
| deferred     | backlog                  |
| pinned       | unstarted                |
| closed       | completed                |

Pulling maps groups back: backlog/unstarted → open, started → in_progress
(or blocked when the `beads:blocked` label is present), completed and
cancelled → closed.

### Type Mapping

Plane CE has no work item types (Epics and Work Item Types are paid
features). Beads issue types round-trip through an internal label:
a bead of type `epic` is pushed with the `beads:type:epic` label, and pull
restores the type from that label. Beads with type `task` carry no type
label. All `beads:*` labels are stripped from the imported label set.

### Description Mapping

Beads stores Markdown; Plane stores HTML (`description_html`). The adapter
converts Markdown → HTML on push (goldmark with GFM extensions, no raw HTML
passthrough) and HTML → Markdown on pull (bluemonday sanitization, then
html-to-markdown). GitHub-Flavored Markdown round-trips: tables,
strikethrough, and task-list checkboxes are preserved through push and
pull. Plane additionally sanitizes incoming HTML server-side (nh3);
disallowed tags are silently removed.

Clearing a description in beads clears it in Plane too: the adapter pushes
Plane's canonical empty document rather than omitting the field, so a
deleted description cannot resurrect on the next pull.

Only the `Description` field syncs. `Design`, `AcceptanceCriteria`, and
`Notes` stay local to beads.

## Sync Filtering

- **Ephemeral issues (wisps) stay local by default.** A push skips beads
  marked ephemeral, so heartbeats, patrols, and other wisps do not become
  permanent Plane work items. Pass `--include-ephemeral` to push them
  anyway.
- **`--state` filters both directions.** `--state open` syncs only open
  work — on pull it imports only work items whose state group is not
  `completed`/`cancelled`, and on push it skips closed beads.
  `--state closed` pulls only `completed`/`cancelled` work items. The
  default `all` syncs everything.

## Conflict Resolution

Default is newest-timestamp-wins. Override per sync:

```bash
bd plane sync --prefer-local   # beads always wins
bd plane sync --prefer-plane   # Plane always wins
```

Note that Plane's `updated_at` is server-controlled (it cannot be
backdated), so timestamp resolution compares beads wall-clock against
Plane server time.

## Rate Limits

Self-hosted Plane CE throttles personal API tokens at 60 requests/minute
by default (`API_KEY_RATE_LIMIT` env on the Plane API container). The
client honors `Retry-After` on 429 responses (capped at 2 minutes per
attempt), falling back to capped exponential backoff. If a 429 persists
after retries are exhausted, the push aborts cleanly: already-pushed
issues keep their links, and the remaining queue is picked up by the next
sync.

5xx responses are retried only for idempotent requests (GET/PATCH/DELETE).
Creates are never blindly retried — if a create's response is lost, the
`external_id` 409 dedup recovers the existing work item on the next sync
instead of duplicating it.

Large projects on the default limit will sync slowly on first backfill;
subsequent syncs are incremental (`plane.last_sync` watermark).

## Known Limitations

- **One project per beads database** (`plane.project_id` is singular).
  Multi-project sync like Linear/Jira is not yet implemented.
- **Assignees do not sync.** Plane assignees are workspace-member UUIDs;
  mapping to beads assignee strings is not yet implemented. A pull never
  touches the local beads assignee, so locally assigned beads stay
  assigned.
- **Sub-issue hierarchy syncs pull-only**: a Plane parent/sub-item link
  becomes a `parent-child` dependency in beads on pull, but beads
  parent-child dependencies are not yet pushed to Plane.
- **No comment sync.** The API client supports comments (for future
  progress-posting features), but `bd plane sync` does not sync them.
- Plane's list API has no `updated_at` filter; incremental pulls fetch
  ordered pages and filter client-side.

## Testing Against a Live Instance

The adapter ships a conformance suite that runs identically against an
in-process fake Plane server (always, in unit tests) and a live instance
(opt-in). To validate a deployment, point the suite at a **dedicated
throwaway project** (it creates issues/labels/comments and does not clean
up):

```bash
export PLANE_CONFORMANCE_BASE_URL=https://plane.example.com
export PLANE_CONFORMANCE_API_KEY=plane_api_...
export PLANE_CONFORMANCE_WORKSPACE=myworkspace
export PLANE_CONFORMANCE_PROJECT_ID=<uuid of throwaway project>
go test ./internal/plane/planetest/ -run TestLivePlaneConformance -v
```

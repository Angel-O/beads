# Memory Beads A3: legacy migration spike

Status: executable, provider-specific prototype. This report answers spike A3
from `beads-memory/specs/plans/memory-beads-architecture-spikes.md`. It does not
define production schema or relax the product contract.

## Verdict

A checked-out Dolt branch can migrate its complete visible `kv.memory.*` state
in one publication when config writes are quiescent. That includes rows already
in `HEAD` and memory-only rows in the working set. A database-scoped lock plus a
durable marker makes concurrent migration attempts and retry after a lost
acknowledgement converge.

That lock does not make automatic first use safe alongside ordinary commands.
It is an advisory migration lock; current config writers do not acquire it. The
prototype proves that an ordinary writer can invalidate a completed preflight
while the migration still holds the lock. Because Dolt stages `config` as a
whole table, publication could then sweep that late write. Production needs a
write gate shared by every config path or a provider-level snapshot/CAS that
rejects the stale publication. Neither exists today.

The full A3 contract is not implementable through the current provider seams.
Dolt has no atomic operation that publishes coordinated commits to several
branch refs. Rewriting related branches one at a time creates an observable
interval where one branch is canonical-only and another is legacy-only. The
prototype reproduces that split. Deterministic IDs make a later resume converge
on identity, but they cannot make the earlier multi-branch transition atomic or
recover shared revision ancestry by themselves.

That failure sits at the provider boundary. Production needs one of two
explicit choices:

- narrow automatic migration to the selected branch and change the
  all-retained-branches atomicity requirement; or
- put the whole database into a resumable maintenance state while a
  provider-specific upgrader rewrites branch refs, then change the interruption
  contract to permit a blocked in-progress state.

The current requirement that an interrupted attempt expose either all-old or
all-new state across related branches cannot be met by the Dolt API Beads uses.

## Executable evidence

The throwaway harness is
`internal/storage/dolt/memory_migration_spike_test.go`. It creates test-only
tables in an isolated Dolt database or test branch. No production open path,
schema migration, public interface, or command calls it.

The focused run exercises five cases:

1. A single current-branch transaction converts committed rows, uncommitted
   memory-only rows, a 245-character legacy key, and an empty value while an
   unrelated dirty issue stays working-only. Faults before mutation and after
   scoped staging both roll the conversion and staged set back; a clean retry
   succeeds. A fault after known publication leaves the marker and canonical
   rows committed; retry reads the marker and creates no second revision or
   commit. If a compatibility writer later recreates a legacy row, the marker
   path rejects without mutation or body leakage.
2. A missing human author or sentinel Project ID fails before the state hash
   moves. The successful revision and Dolt commit retain the configured author.
   Unrelated dirty config also fails before mutation because Dolt stages
   `config` as a table. The diagnostic names `issue_prefix` without printing its
   value.
3. Two simultaneous first-use attempts serialize on the existing
   database-scoped schema lock. Exactly one applies and one observes the durable
   marker. The result is one revision and one Dolt commit.
4. Divergent branches derive the same Bead ID from Project ID plus exact legacy
   key bytes and derive distinct current-state revisions for different bodies.
   After the first branch migrates, the other branch still exposes legacy-only
   state. That is the multi-ref atomicity failure.
5. A second connection updates `config` while the migration connection holds
   the database-scoped lock. The write succeeds and turns a completed clean
   preflight stale. This is the missing shared-write-gate blocker.

Run it with the real Dolt test container:

```bash
BEADS_TEST_ENV_RUN_DOLT=1 ./scripts/test.sh -count=1 -v \
  -run '^TestMemoryMigrationSpike_' ./internal/storage/dolt
```

The `-count=1` matters: an earlier locally cached run may record the package's
intentional `BEADS_TEST_SKIP=dolt` result instead of starting the container.

The prototype's scope is the transaction shape. Its tables intentionally omit
titles, aliases, lineage, references, and provider history. In particular, the
empty-value case proves byte preservation only.
The requirements still need to say whether an empty body can produce a valid
derived title or must enter recovery.

## Where the current implementation puts the seams

| Concern | Current code | Consequence for A3 |
| --- | --- | --- |
| Ordinary dispatch | `cmd/bd/main.go` resolves the actor, runs version maintenance, then opens either a store or a proxied UOW provider | The strict migration author must be resolved before any migration mutation. `getActorWithGit` is too permissive because it falls back to `$USER` and `unknown`. |
| Explicit upgrade | `cmd/bd/migrate.go` exposes `bd migrate schema` through optional `storage.SchemaMigrator` | A memory-specific command can follow this optional-capability pattern without adding a required method to `storage.DoltStorage` or an out-of-tree backend interface. |
| Legacy data | `internal/storage/memoryops/memories.go`, `internal/storage/dolt/memories.go`, `internal/storage/embeddeddolt/memories.go`, and `internal/storage/uow/memories.go` | Raw config access is required for migration. The legacy role cannot distinguish a missing row from an empty value. |
| Schema migration | `internal/storage/schema/schema.go` and `internal/storage/schema/lock.go` | The existing lock is reusable. The ordinary schema runner rejects a dirty table touched by a pending migration, so it cannot absorb dirty `config` rows without a memory-aware preflight. |
| Config staging | `internal/storage/dolt/store.go` and `internal/storage/embeddeddolt/version_control.go` | Dolt stages a whole table, not selected config rows. A dirty config diff containing anything outside `kv.memory.*` must stop. Embedded `Commit` stages all tables, while server-mode plain `Commit` excludes config; migration cannot rely on post-command commit behavior. |
| Transaction and retry | `internal/storage/uow/tx.go` and `internal/storage/uow/doltserver_tx.go` | A pinned `START TRANSACTION` followed by `DOLT_COMMIT` can publish config deletion, canonical inserts, revisions, and the marker together. The current migration lock does not gate ordinary writers, so production still needs a shared write gate or provider CAS. Serialization errors may retry; an indeterminate publication must re-read the marker instead of replaying blindly. |
| Branches and history | `internal/storage/versioncontrolops/branches.go`, `internal/storage/dolt/versioned.go`, and `internal/storage/dolt/history.go` | Existing history methods are issue-shaped. There is no legacy-config history reader or cross-branch transaction. A Dolt implementation must privately inspect refs and historical config snapshots. |
| Merge behavior | `internal/storage/versioncontrolops/mergesettle.go` | Existing memory-only config conflicts resolve with `--theirs`, so some old local divergence may already be gone. Migration must report that as a provenance gap; it cannot reconstruct a value Dolt no longer retains. |
| Global scope | `internal/doltserver/doltserver.go`, `cmd/bd/init.go`, `cmd/bd/main.go`, and `cmd/bd/uow_factory.go` | `beads_global` currently uses the universal all-zero Project ID. The proposed installation-unique identity needs a database-authoritative, compare-and-set migration; updating every workspace's `metadata.json` atomically is impossible. |
| Team server | `internal/storage/uow/dolt_sql_provider.go` and `internal/storage/uow/team_server_schema.go` | `bd` is forbidden from migrating a `bts`-owned schema. The same conversion must be a team-server migration or the capability must reject with operator guidance. |
| Imports and aliases | `cmd/bd/import.go`, `cmd/bd/import_shared.go`, `cmd/bd/auto_import_upgrade.go`, `cmd/bd/kv.go`, `issueops/workspaceconfig.go`, and `internal/httpapi/settings.go` | There are two legacy import parsers with different duplicate behavior, and config writes can still create `kv.memory.*` rows. Every retained write path must route through the canonical module after the gate. |

Open contributor PR #4383 addresses server-mode config publication with a
scoped `CommitConfigOnly` path. Its staging rule matches this spike's finding:
explicitly add owned tables and commit without `-A`. Production migration work
should reuse that pattern if the PR lands and preserve its regression tests. The
PR does not supply the canonical conversion, durable marker, or branch/history
gate tested here.

## What is feasible

Subject to a shared config-write gate or equivalent provider CAS, the smallest
safe current-branch conversion has this shape:

1. Resolve a configured human identity and stable non-sentinel Project ID.
   Fail before opening a write transaction if either is missing. The spike uses
   `Name <email>` because Dolt publication already accepts that form. The public
   Change Author shape is still an A2/domain decision.
2. Acquire the existing per-database migration lock on one pinned connection.
   Also exclude ordinary config writers through a shared gate, or begin a
   provider transaction that will reject publication if the preflight snapshot
   changes. The existing named lock alone is insufficient.
3. Inspect `dolt_diff('HEAD', 'WORKING', 'config')`. Continue only when every
   dirty row is under `kv.memory.*`. Stage only the migration-owned tables so
   unrelated dirty tables remain in the working set.
4. Read raw working config so empty values and the full stored key space are
   visible. Derive Bead ID deterministically from Project ID plus exact legacy
   key bytes. Do not normalize Unicode, trim, or reinterpret ID-looking keys.
5. In one pinned transaction, write canonical memory state, its attributed
   `legacy_migration` revision, provenance or gap records, and a durable
   migration ledger row, then delete the legacy config rows.
6. Publish that unit explicitly. A failure before publication, including after
   scoped staging, rolls back both row and staged state. An error after the call
   may be indeterminate; retry reacquires the lock and reads the ledger before
   deciding whether any work remains. A marker with recreated legacy rows is an
   invariant violation, not an already-applied success.

This path can preserve an empty body and the 245-character key boundary at the
storage level. It can also stop without leaking body text. It does not make a
decision about title validity, historical lineage, or accountable discard.

## What remains provider-specific or blocked

### Related branches

`schema.MigrateUp` applies to the active branch. `DOLT_CHECKOUT`,
`DOLT_BRANCH`, and `DOLT_COMMIT` are session-scoped stored procedures, and the
embedded version-control code documents that they do not compose inside an
ordinary SQL transaction. There is no multi-ref compare-and-swap operation in
the storage API.

A maintenance upgrader could inventory every ref, record a durable plan, update
branches one at a time, and resume after interruption. It would be useful, but
it would expose a third state: `migration_in_progress`. Calling that all-old or
all-new would be false.

### Historical reconstruction

Dolt retains enough evidence for many histories. A private scanner can walk the
union of `dolt_log('<branch>')` commit hashes and read `config AS OF <hash>`,
using the same commit hash once when branches share ancestry. Treat source
commit time and committer as evidence. Current embedded and UOW memory commits
often use a machine committer, so the migration must attribute the destination
revisions to the current configured human and keep source evidence separate.

History is not always recoverable. A prior `--theirs` config merge, compaction,
deleted refs, or an import that collapsed duplicate keys may have removed the
only evidence. Those cases can seed valid current state with a named provenance
gap. They cannot invent a transition, source author, timestamp, or parent.

This scanner belongs behind the Dolt provider. An independently represented
provider may have no branches or historical snapshots at all, and today's
generic storage interfaces promise neither.

### Global identity

The all-zero global sentinel cannot feed deterministic canonical IDs. The
database should own a generated installation identity and replace the sentinel
with a compare-and-set under the migration lock. Losing clients re-read the
winner. Local workspace metadata can cache that ID later; it cannot be part of
the same atomic unit because the upgrader does not know every workspace path.

This needs an identity ADR before implementation. Otherwise two workspaces can
mint different Bead IDs for the same global legacy key.

### Custom `memory` issue types

Preflight has to inspect both normalized `custom_types` state and the
`types.custom` config/YAML fallback, then search retained issue and wisp state
for actual `issue_type = 'memory'` rows. Existing rows are a collision and must
stop. The specifications do not yet say whether a configured-but-unused custom
type may be removed automatically; the conservative answer is to stop and ask
the operator.

### Imports, compatibility writes, repair, and discard

`parseImportRecords` preserves duplicate memory records in file order.
`parseJSONLFile` stores them in a map and keeps only the last value. Upgrade
auto-import ignores a file containing memories but no issues. These behaviors
live at the source front doors; database migration cannot repair them. They need
compatibility tests at those commands.

After success, retained keyed-memory compatibility writers call the canonical
Memory Module and require their configured accountable author:

- memory CLI and `/v0` HTTP adapters;
- `memoryops.Memories` compatibility implementations;
- both import paths and auto-import recovery.

Generic `WorkspaceConfig.SetSetting`/`UnsetSetting`, config HTTP, and KV aliases
must reject writes whose exact key is under `kv.memory.*`; they are not alternate
canonical Memory Module front doors.

No current command records an accountable decision to discard an invalid empty
or colliding legacy entry. `bd doctor` may repair derived state, but its contract
does not authorize content loss. A production recovery surface should first
write a durable decision containing the affected key, reason, human author, and
source-state fingerprint. The eventual command names are a CLI design choice;
the minimum behaviors are inspect, retry/resume, repair-by-explicit-replacement,
and discard-with-recorded-accountability. None should print unrelated bodies.

## Suggested production files and tests

Do not promote the prototype helper. Build the real path after A1 and A2 settle
the module and revision seams.

Likely implementation files:

- `internal/memorybeads/migration.go`: storage-independent preflight result,
  typed blocker categories, and recovery/result vocabulary;
- `internal/storage/dolt/memory_migration.go`: Dolt ref/history inventory and
  direct/server execution;
- `internal/storage/embeddeddolt/memory_migration.go`: embedded acquisition and
  explicit publication using the same private executor rules;
- `internal/storage/uow/memory_migration.go`: proxied UOW entry point and
  team-server capability refusal;
- the next numbered `internal/storage/schema/migrations/*memory*.up.sql` for
  canonical tables and ledger, after the physical model is chosen;
- `cmd/bd/memory_migration_gate.go` for ordinary dispatch and
  `cmd/bd/migrate_memory.go` for explicit inspect/recovery.

Keep migration acquisition optional. Do not append a method to
`storage.DoltStorage`, `memoryops.Memories`, `backend.Storage`, or
`backend.DoltStorage`.

Permanent tests should include:

- a provider-neutral migration behavior fixture only for capabilities every
  advertised provider actually implements;
- `internal/storage/dolt/memory_migration_test.go` for raw history, dirty config,
  deterministic branch identity, interruption, marker replay, and provenance
  gaps;
- `internal/storage/embeddeddolt/memory_migration_test.go` for embedded
  publication and process interruption;
- `internal/storage/uow/memory_migration_test.go` for concurrent first use,
  transaction rollback, indeterminate commit handling, and team-server refusal;
- `cmd/bd/memory_migration_gate_test.go` for strict identity before store
  mutation and body-safe diagnostics;
- focused compatibility additions beside existing memory, import, KV,
  workspace-config, HTTP, global-identity, and custom-type tests.

The existing migration tests worth extending rather than copying are
`internal/storage/embeddeddolt/migrate_selfheal_test.go`,
`internal/storage/dolt/schema_migration_dirty_test.go`,
`internal/storage/dolt/cross_upgrade_merge_test.go`,
`internal/storage/embeddeddolt/migrate_repair_atomicity_test.go`, and
`internal/storage/schema/lock_test.go`.

## Decision required before production

The current-branch transaction shape passed under quiescent config writes. The
ordinary-writer race and multi-branch transition remain blocked, so A3 as
written did not pass.

Before implementation, add a shared config-write gate or a provider CAS that
makes preflight freshness enforceable. Revise the requirements or accept a
provider maintenance state for multi-branch upgrades. Also decide the
authoritative global identity, the configured Change Author shape, empty-body
validity and title derivation, and whether an unused custom `memory` type is an
automatic cleanup or a blocker. Those choices affect irreversible identity or
accountable data handling and should not be buried in a migration helper.

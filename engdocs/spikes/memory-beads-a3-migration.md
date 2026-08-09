# Memory Beads A3: legacy migration spike

Status: executable provider-specific closure prototype. The original A3 probe
at commit `347d8cb2c` found that Dolt cannot atomically replace memory state on
several branch refs. This follow-up tests the maintenance-state contract adopted
in Memory Beads ADR 0013. It does not install production schema or change a
released interface.

## Verdict

The revised current-state migration contract is feasible on the three intended
Dolt execution paths.

Dolt still has no multi-ref transaction. A provider can work around that fact
without serving a mixed result: install one clone-local
`migration_in_progress` marker, reject canonical and deprecated memory access,
convert branches one at a time, and report success only after every current
branch view passes a final audit. Interruption may leave physical branches in
different formats, but callers see a typed unavailable result until the set has
converged.

The shared config-writer race from the first spike is also closable. An ordinary
writer and marker installation rewrite the same clone-local coordination cell
inside their respective transactions. Whichever starts first wins a real Dolt
serialization ordering. The loser retries against fresh state; a writer that
then sees the marker returns `migration_in_progress` before changing config.

This removes the coordination and multi-branch feasibility blockers behind ADR
0013. It does not close the production A3 release gate. The throwaway tables do
not reconstruct legacy history, model the final Memory revision schema, or wire
the gate into every live config, memory, import, HTTP, and ref-mutating path.
Those remain production work, and the A2 revision seam still has to settle
before the schema can be frozen.

Nothing in these results makes the Memory Beads vision infeasible. The original
all-old-or-all-new interruption rule was infeasible for Dolt; the accepted
resumable unavailable state is the necessary correction.

## Prototype shape

The coordinator lives in
`internal/storage/doltmemorymigration/prototype.go`. It is an internal package,
is not registered with the schema runner, and is not acquired through
`memorybeads/v1`.

The implementation uses Dolt details that stay private to this provider:

- One existing `local_metadata` working table holds the control record and the
  config-writer coordination cell. The table is addressed through its
  branch-qualified database name. This matters: an ignored table belongs to a
  branch working set, so an old branch may not contain an unqualified
  `local_metadata` table after checkout.
- The control record stores a migration ID, phase, project and author inputs,
  and a plan for the branch views visible to this clone. Its source hashes,
  fingerprints, encoding, and table names are prototype mechanics rather than
  Memory Beads interchange values.
- Config writers touch the same coordination cell in the transaction that
  performs their config mutation. Marker installation touches that cell and
  checks the source inventory again before commit.
- Each branch is converted on a pinned connection. The conversion applies the
  current throwaway schema in that view, stages only `config` and the three
  canonical fixture tables, then publishes one Dolt commit with the
  branch-local ledger. This also works for a branch from before those tables
  existed.
- Dolt DDL changes the working root even when its surrounding SQL transaction
  rolls back. Before publication, the prototype compensates by removing only
  fixture tables that were absent when the attempt began. It does not reset
  `config` or any unrelated table. The maintenance gate remains closed during
  this unpublished work.
- A published branch is reconciled from its ledger after response loss. The
  clone-local plan is only progress bookkeeping; it is never accepted as proof
  that branch publication happened.
- Canonical and deprecated memory entry points use the same private access
  gate. While the global phase is in progress they return a typed
  `MigrationInProgressError`. After completion the complete-path gate still
  audits every current related view for the matching ledger and absence of
  legacy rows before returning success.
- Provider-controlled branch/ref creation takes the existing database named
  lock. The coordinator inventories again before finalization and audits again
  after the complete marker. A historical branch created after completion is
  unavailable on first access and is included by the next coordinator run.

The source inventory reads each branch-qualified working view, not only its
committed `HEAD`. That is how an unpublished `kv.memory.*` update on a related
branch survives conversion. Since Dolt stages `config` by table, an unrelated
dirty config key stops before marker installation on the active branch and
before conversion on any later branch. The diagnostic names keys but never
their values.

## Executable evidence

### Direct/server path

`internal/storage/dolt/memory_migration_a3_closure_test.go` runs against the real
Dolt SQL server. It covers these cases:

- Writer first: a config transaction updates legacy memory while the migration
  has read preflight state. After that writer commits, marker installation
  rejects the stale inventory and performs a fresh preflight. The new value is
  the one converted.
- Marker first: a writer begins while the marker transaction is prepared but
  uncommitted. Its coordination-cell write serializes, the UOW-style retry sees
  the committed marker, and it returns typed `migration_in_progress` without
  changing the legacy row.
- Two concurrent coordinators serialize on the provider lock. One publishes the
  conversion and the other observes completion; the database contains one
  legacy-derived revision.
- Related working state: a peer branch has an unpublished legacy update. The
  branch-qualified inventory sees it and conversion preserves that body while
  retaining one Bead identity across the two branches.
- Before publication: an injected error after exact staging rolls back the
  canonical inserts, legacy deletion, ledger, and staged state. A new
  coordinator resumes successfully.
- Pre-schema history: a real branch is cut before the fixture schema is
  installed. The active branch publishes first, then an injected failure stops
  the historical branch after staging. That branch still has its legacy row,
  has none of the fixture tables, and has no staged changes. Both branch views
  return typed unavailable until retry installs the schema and data together.
- Lost acknowledgement: the first branch commit lands and the response is
  treated as lost before progress bookkeeping. Physical storage is then one
  canonical branch plus one legacy branch. All four tested entry categories,
  canonical read/write and deprecated read/write, return the same typed
  unavailable result. A new coordinator reads the ledger, avoids a duplicate
  revision or commit, and completes the remaining branch.
- Completed retry: running again creates no revision and no Dolt commit.
- Lost clone-local phase record: surviving versioned branch ledgers keep memory
  unavailable and prevent a fresh migration ID from being invented. Recovery
  must restore or rebuild the control record explicitly.
- Dirty config: an unrelated working config key returns a body-safe typed
  preflight refusal before the marker or canonical rows exist.
- Supported ref race: a provider-controlled ref mutation waits for the same
  named lock. If it creates an old branch after completion, that branch fails
  closed, all memory entry points remain unavailable while the related set is
  mixed, and a later run converts it.
- Uncoordinated finalization race: a historical branch appears after the final
  inventory but before the complete-marker transaction commits. The post-commit
  audit reopens the maintenance state and converts the branch. A gate executed
  in the short interval between the first complete marker and that audit still
  returns typed unavailable; success waits until both views are canonical.

The test also checks that divergent bodies produce distinct prototype revision
IDs while the same legacy key and Project ID produce one Bead ID. That proves
cross-branch semantic identity for the conversion; it does not select the
public A2 revision-ID format.

### Embedded reopen path

`internal/storage/embeddeddolt/memory_migration_a3_closure_test.go` uses the real
embedded driver. It publishes a branch, loses the acknowledgement, closes the
database and underlying connector, then opens the directory with a new engine.
The reopened provider reads the clone-local marker, returns typed unavailable,
reconciles the versioned branch ledger, and completes without replaying the
canonical revision.

The embedded driver supports the same named lock and branch-qualified ignored
working table used by the server path; the test does not use an in-memory lock
or retain Go coordinator state across reopen.

### Proxied unit-of-work path

`internal/storage/uow/memory_migration_a3_closure_test.go` starts an isolated
Dolt server and opens the real external proxy/UOW provider. A guarded write
runs through `RunTx`; a guarded read runs through `RunTxRead`. Both preserve the
typed migration error through the retry/rollback layer, and the source row is
unchanged. After completion the guarded canonical read succeeds, legacy config
writes return `LegacyNamespaceRetiredError`, and an ordinary config write is
still allowed.

The same test opens the database in its actual `teamServer` mode. Mapping that
provider ownership flag to the coordinator returns an actionable typed refusal
before a migration marker is installed. The message directs the caller to the
team-server operator.

## What the closure proves

For current visible state, the prototype establishes these safety claims:

- Marker installation is conditional on the config state it inspected when
  ordinary writers participate in the shared private gate.
- Input that cannot be included safely stops before versioned migration
  mutation. Diagnostics do not disclose memory bodies.
- One branch publication is atomic across the view-local schema, canonical
  rows, attributed revision, branch ledger, and legacy deletion. A branch from
  before schema installation is handled by the same publication boundary.
- Response loss is resumable from versioned evidence and does not invite blind
  replay.
- The provider may hold a physical one-branch-at-a-time split, but no ordinary
  memory surface serves that split as success.
- Process restart loses no migration phase or applied-branch evidence.
- Final success means every branch in the final provider inventory is
  canonical. A ref created later remains fail-closed until another run includes
  it.
- Team-server ownership prevents local migration rather than silently taking
  over its schema.

These are observable properties. The specific metadata key, fingerprints,
named lock, branch ledger, and conversion loop are Dolt implementation choices.

## What remains open for production

The prototype is intentionally incomplete in several material ways.

1. Every production config writer must participate in marker coordination,
   including direct, embedded, proxied, import, workspace-config, HTTP, and
   generic KV aliases. Every canonical and deprecated memory operation must
   invoke the access gate. The prototype tests the seam but does not wire those
   call sites.
2. Every production branch/ref mutator must use the same exclusion protocol and
   preserve the branch that owns the clone-local control plane. The prototype
   proves branch creation and the final-inventory fallback. Deleting or renaming
   the control anchor is outside its supported helper and needs an explicit
   production rule.
3. This pass converts current state only. The earlier A3 findings still apply
   to reconstructible commit history, shared ancestry, lost-history provenance
   gaps, accountable repair, and discard. Production must preserve available
   history and must not invent unavailable transitions.
4. The tables and IDs are fixtures. Production must use the A2-approved
   revision and publication model, including its provider-private per-view
   schema upgrade sequence, collision checks, title rules, reference
   representation, and authorship fields.
5. Global installation identity, custom `memory` issue-type collisions, empty
   legacy values, and import duplicate behavior remain governed by their
   product decisions and recovery rules. This coordinator does not choose new
   answers for them.
6. A production marker needs inspection and recovery commands. Operators must
   be able to distinguish in-progress work, stale source, unsafe dirty config,
   external ownership, an indeterminate publication, and a completed run
   without reading private bodies.

The production gate should therefore be described as implementation work with
known feasible coordination, not as an unresolved Dolt atomicity question and
not as completed migration conformance.

## Verification

Focused commands used for the closure:

```bash
BEADS_TEST_ENV_RUN_DOLT=1 ./scripts/test.sh -count=1 -v \
  -run '^TestMemoryMigrationA3Closure_' ./internal/storage/dolt

BEADS_TEST_ENV_RUN_DOLT=1 ./scripts/test.sh -count=1 -v \
  -run '^TestMemoryMigrationA3Closure_' ./internal/storage/embeddeddolt

BEADS_TEST_ENV_RUN_DOLT=1 ./scripts/test.sh -count=1 -v \
  -run '^TestMemoryMigrationA3Closure_' ./internal/storage/uow
```

All focused cases passed against real Dolt behavior. The test files and
prototype are new and disjoint from A1/A2. No production schema migration,
public Memory Module change, or release-path wiring was added.

# Memory Beads A3: Dolt conversion-coordination experiment

**Status:** Executable Dolt-specific evidence. The prototype does not define required Memory Beads conversion mechanics.

## Question

Can a Dolt provider convert legacy keyed Memory state across multiple branch working views without reporting false success, silently losing a concurrent write, or serving a mixed legacy/canonical result as complete?

## What ran

The coordinator lives in `internal/storage/doltmemorymigration/prototype.go`. It is internal, is not registered with the production schema runner, and is not acquired through `memorybeads/v1`.

Dolt has no multi-ref transaction, so the fixture tested one provider-specific strategy:

- a clone-local maintenance marker and config-writer coordination cell;
- branch-qualified inventory of working state, not only committed `HEAD` state;
- one branch-local publication at a time with a versioned ledger;
- temporary unavailability of canonical and legacy Memory entry points while branch views differed; and
- reconciliation from published evidence after acknowledgement loss or process restart.

The direct/server suite exercised writer-before-marker and marker-before-writer races, concurrent coordinators, unpublished branch state, pre-schema branches, failure before publication, lost acknowledgement, completed retry, loss of clone-local progress state, dirty config, and branch-creation races.

The embedded suite closed and reopened the real embedded driver after a published branch lost its acknowledgement, then completed without replaying the converted record. The proxied suite exercised guarded reads and writes through the real unit-of-work path and verified that team-server ownership refused local conversion before installing the marker.

Run the recorded evidence with:

```bash
BEADS_TEST_ENV_RUN_DOLT=1 ./scripts/test.sh -count=1 -v \
  -run '^TestMemoryMigrationA3Closure_' ./internal/storage/dolt

BEADS_TEST_ENV_RUN_DOLT=1 ./scripts/test.sh -count=1 -v \
  -run '^TestMemoryMigrationA3Closure_' ./internal/storage/embeddeddolt

BEADS_TEST_ENV_RUN_DOLT=1 ./scripts/test.sh -count=1 -v \
  -run '^TestMemoryMigrationA3Closure_' ./internal/storage/uow
```

All focused cases passed against real Dolt behavior when the experiment was recorded.

## Observed results

Within the fixture:

- marker installation was conditional on the config state it inspected;
- unsafe or dirty input stopped before conversion mutation and diagnostics omitted Memory bodies;
- each branch-local publication kept fixture schema, canonical rows, attribution, ledger, and legacy deletion together;
- acknowledgement loss resumed from stored evidence without creating a duplicate fixture record;
- process restart preserved enough evidence to continue;
- mixed physical branch formats remained unavailable through the tested Memory gates; and
- final success was reported only after the provider's final inventory was canonical.

The experiment also preserved one fixture Bead identity for the same legacy key across tested branches while allowing divergent branch bodies to produce different historical fixture states. That was a conversion observation, not a selected public identifier format.

## Current interpretation

A Dolt provider can coordinate a truthful conversion even though it cannot atomically update several refs. The maintenance marker, coordination cell, branch ledger, fingerprints, named lock, compensating DDL cleanup, and conversion loop are one feasible Dolt strategy—not portable Memory semantics.

At the feature level, conversion needs to preserve representable content and provenance, avoid invented evidence, and never claim complete success over loss or partial visibility. The implementation may use this strategy, another strategy, or no automatic conversion. Rollout, timing, recovery commands, and deprecation policy are separate implementation decisions.

The observable recovery boundary is stronger than a success message: after an
interruption, Memory callers must see either the still-usable legacy view, the
complete canonical view, or an inspectable recoverable-unavailable state. This
spike supplies evidence for the third outcome through its maintenance gates and
restart reconciliation; it does not prescribe that mechanism.

## Limits

The prototype did not wire every production config, HTTP, import, Memory, or branch-mutating path. Its tables and identifiers are fixtures. It did not reconstruct the full available history or select retention behavior, production schema, title rules, reference encoding, or shared Beads History mechanics. The result is evidence that the tested Dolt coordination problem has at least one solution, not an obligation to ship that solution.

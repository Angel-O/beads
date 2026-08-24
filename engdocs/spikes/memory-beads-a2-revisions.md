# Memory Beads A2: exact-history provider experiment

**Status:** Executable internal evidence. The prototypes are reversible and do not define the production Beads History model.

## Question

Can materially different provider paths expose exact historical Memory states and atomic updates without exposing their transaction or commit identifiers as the public address?

## What ran

The provider-neutral fixture lives under `internal/memorybeads/spikea2`. A SQL realization is isolated in `internal/memorybeads/spikea2/sqlprototype`, with adapters exercised through a real `EmbeddedDoltStore` path and a real proxied Dolt-server unit-of-work path. The experiment also used an independently represented provider.

The SQL fixtures used throwaway tables for immutable state, named views, and head sets. No production migration creates those tables, and the experiment did not widen `memoryops.Memories` or storage interfaces.

Across the exercised providers, the fixtures demonstrated:

- exact reads of complete historical Memory state after archive, restoration, view removal, provider close, and reopen;
- reconstruction of persisted current and conflicting fixture state by a fresh module object;
- atomic body-plus-reference updates and rejection of stale compare-and-swap attempts;
- Stored Provenance changes, including origin or transfer evidence, produced a
  new fixture version, while Change Attribution-only differences did not;
- readable historical states when the fixture represented competing current states, without silently choosing one;
- history, diff, line-blame, and field-blame computations over fixture data;
- deterministic bounded history, search, and reference traversal tied to the original scope and snapshot; and
- distinct observations for known application, known non-application, and acknowledgement uncertainty at the exercised transaction seams.

The embedded fixture injected failure after SQL application but before version verification and recovered an exactly readable address. Proxied transaction tests classified commit-response loss as indeterminate and prohibited blind replay. The acknowledgement-loss controls model the observation; they do not claim a real network was severed at that exact instant.

Run the focused evidence with:

```bash
./scripts/test.sh -count=1 ./internal/memorybeads/spikea2/...
./scripts/test.sh -count=1 -v \
  -run '^TestMemoryA2(SQLPrototypeEmbeddedProviderContract|EmbeddedPostSQLVersionFailureIsAppliedUnverified)$' \
  ./internal/storage/embeddeddolt
BEADS_TEST_ENV_RUN_DOLT=1 ./scripts/test.sh -count=1 -v \
  -run '^TestMemoryA2SQLPrototypeProxiedProviderContract$' ./internal/storage/uow
./scripts/test.sh -count=1 \
  -run '^(TestDoltServerTx.*|TestRunTx_IndeterminateCommitIsNotRetried)$' \
  ./internal/storage/uow
```

These commands passed when the experiment was recorded. Race and vet runs over the same packages also passed.

## Current interpretation

The experiment establishes feasibility, not the canonical design. A provider-private identity layer can expose exact historical states across the tested transaction paths, and an atomic provider operation can keep a Memory body and its outgoing references together.

The following remain fixture choices rather than Memory requirements:

- revision IDs, named views, head sets, lineage, and blame representation;
- the fixture's publication outcomes and retry controls;
- cursor encoding, repository tables, and comparison algorithms; and
- the choice not to use Dolt commit hashes in this prototype.

Memory Beads now relies on shared, provider-neutral Beads History and Versioned Bead contracts. Those contracts define historical addressing, branches, retention, comparison, Change Attribution, writes, retries, concurrency, and conflicts. A2 shows that the tested providers can support an exact-history abstraction; it does not define that abstraction or a Memory-private mutation protocol.

Within the fixture, origin and source or transfer evidence are durable Stored
Provenance. Changing either produces a version. Author, assisting agent, and
change message describe Change Attribution for an accepted version; differences
in those fields alone do not turn otherwise unchanged Memory state into a new
version. These fixture fields do not prescribe the shared History contract's
representation.

At the Memory boundary, the result supports requiring an exact, non-substituted
read while a historical state is retained. The experiment did not exercise
policy-driven removal, so it does not establish a retention duration or the
shared History facility's representation of explicit unavailability.

## Limits

The experiment did not select production schema, public API, storage limits, native branch integration, or history-retention policy. It did not benchmark the fixture or prove production-scale concurrency. Any continuing use of this code should treat it as executable evidence behind a future shared-history implementation, not as a release gate for a Memory-specific revision system.

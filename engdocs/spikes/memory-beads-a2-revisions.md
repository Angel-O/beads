# Memory Beads A2: revision identity and publication spike

Status: intended-provider publication gate passed with reversible prototypes;
production promotion remains open. This report answers spike A2 from
`beads-memory/specs/plans/memory-beads-architecture-spikes.md` without claiming
that the fixture schema is a production design or that providers advertise
branching Memory Beads.

## Verdict

The proposed caller-visible revision contract is coherent. The independently
represented provider, a real `EmbeddedDoltStore` direct path, and a real
proxied Dolt-server unit-of-work path run the same black-box contract without
using provider commit identifiers as Memory revision identity.

The two intended SQL paths use a reversible, fixture-only revision catalog and
persist named Memory views and head sets separately from immutable revisions.
They publish one provider-issued opaque revision, its complete immutable state
and blame, and the applicable head set through their real transaction
boundaries. The same branch-independent contract passes on both paths:
competing heads are not guessed, exact reads survive deletion of the view that
created them, and fresh Module objects recover revisions and named-view heads
after each provider is closed and reopened. The production schema, public
operational Module version, and connection to native `bd` branch commands
remain deliberately unselected.

## Executable evidence

The provider-neutral prototype lives under `internal/memorybeads/spikea2`. The
SQL realization is isolated in `internal/memorybeads/spikea2/sqlprototype`, and
the real-provider fixtures live beside the embedded and UOW adapters. They are
internal and do not extend `memoryops.Memories`, storage interfaces, or the
descriptor-only A1 module. No production migration creates the spike tables.

The provider-neutral contract covers:

- stable opaque revision addresses and complete exact reads of key, aliases,
  title, lifecycle, body, outgoing references, lineage, attribution, change
  message, origin, and provenance after archive, restore, branch removal, and
  provider maintenance;
- reconstruction of a fresh Module over the same repository and Project,
  including exact reads, the current `main` head, and a branch-only current
  head discovered through the persisted named-view registry; a separate
  reconstruction preserves the exact competing-head set and both exact sides
  of the conflict;
- atomic body-plus-reference publication and compare-and-swap rejection;
- normalized one-edge-per-locator reference state, including repinning and
  rejection of local or same-project-qualified self-reference;
- competing heads that remain readable while unqualified current reads,
  searches, and writes fail deterministically, identify the conflicted bead,
  and do not choose a winner;
- semantic history, diff, line blame, and field blame;
- deterministic history, search, and exact-source-reference continuation bound
  to the original provider instance, project scope, query, and snapshot;
- distinct `applied`, `applied_unverified`, `unchanged`, `rejected`, `failed`,
  and `indeterminate` outcomes, with the same indeterminate caller result for
  hidden not-published and published acknowledgement-loss decisions;
- structural validation of local and Project-qualified exact reference state.

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
go test -race -tags gms_pure_go -count=1 \
  -run '^TestMemoryA2(SQLPrototypeEmbeddedProviderContract|EmbeddedPostSQLVersionFailureIsAppliedUnverified)$' \
  ./internal/storage/embeddeddolt
BEADS_TEST_ENV_RUN_DOLT=1 go test -race -tags gms_pure_go -count=1 \
  -run '^TestMemoryA2SQLPrototypeProxiedProviderContract$' ./internal/storage/uow
go vet -tags gms_pure_go ./internal/memorybeads/spikea2/... \
  ./internal/storage/embeddeddolt ./internal/storage/uow
```

All commands pass. The provider fixtures exercise actual SQL publication,
rollback after the immutable append but before the head move, persisted logical
head-set behavior, and close/reopen boundaries followed by fresh Module
construction. Two live Module objects over one Project see each other's exact
publications while keeping their checkout context local. Each conformance case
uses a distinct Project ID, so this reconstruction test does not hide state
leakage between cases. A conflict remains a conflict with the same head set
after reconstruction. An embedded failure after SQL application but before
provider-version verification returns
`applied_unverified` with an exactly readable address. The proxied transaction
tests classify commit-response loss as indeterminate and prohibit replay; the
black-box acknowledgement-loss choices are fixture controls, not a claim that
the suite severed a real network connection at precisely that instant. No A2
case is skipped on either intended provider path.

## What the spike selected

- Memory revision IDs are immutable provider-issued addresses. A provider's
  native identifier may be used only if it satisfies permanent exact
  addressability and the rest of the public revision contract. Current Dolt
  commit hashes do not meet that bar.
- One applied revision contains the complete memory state and outgoing
  references published by that mutation.
- Re-adding identical normalized state—including reordered or duplicate aliases
  and repeated entries for the same stored locator—returns `unchanged` without
  minting a revision. Source-local and Project-qualified locator spellings stay
  distinct.
- A caller supplies the expected current Memory revision for an ordinary
  mutation. Stale comparison happens before no-op evaluation.
- A known publication whose complete result cannot be verified returns
  `applied_unverified` with its stable address. The publication adapter receives
  the same `indeterminate` observation whether the hidden authority accepted
  the unit before acknowledgement loss or did not publish it; neither result
  fabricates a success address.
- Continuation is an opaque provider token whose observable contract is stable
  scope, ordering, and snapshot—not a shared encoding.
- A canonical Memory revision ID can be prepared before publication and
  inserted with the immutable revision. Once the transaction authority reports
  a known commit, that prepared ID is the known canonical address; no Dolt
  commit hash has to cross the Module seam.

These choices belong in the eventual versioned Memory Module. The prototype's
particular in-memory representation, ID spelling, cursor map, history traversal,
diff, and blame algorithms do not.

## What changed at the provider seam

The legacy memory role still stores keyed config values and exposes no Memory
Bead identity, immutable revision repository, reference state, history, or
continuation. The spike does not route through or widen that role.

The private SQL prototype owns the semantic repository and gives both adapters
one narrow seam: run a read snapshot or attempt one publication and report
`published`, `not_published`, or `unknown`. The Module prepares opaque Bead and
revision IDs, appends the immutable revision payload, and moves the current
head inside that one publication. A subsequent exact read verifies the complete
result. This is enough to distinguish known publication with failed
verification from unknown acknowledgement without asking the transaction layer
to invent product identity.

Repository lookup no longer depends on a random token held by the Module. The
prototype uses a fixed schema discriminator; the physical repository and
Project ID select the durable revision catalog, view registry, and head sets.
A fresh Module starts on `main` and can check out any persisted named view.
Checkout itself remains local to the Module object, which lets two callers use
different views without overwriting shared session state. This is evidence for
the identity boundary, not a requirement that a production provider use these
tables or this discriminator.

The embedded path already preserved the no-replay indeterminate sentinel. The
proxied `doltServerTx` did not: an untyped transport or protocol error from the
commit statement looked like an ordinary failure. It now marks that response
loss with `storage.ErrCommitIndeterminate`, while decoded server rejection and
failures before the commit statement remain definite. Focused transaction tests
pin those distinctions.

Current Dolt commit hashes remain an unacceptable shortcut. Branch deletion,
history flattening, garbage collection, and provider maintenance can change
their retention, while a public Memory revision must remain exactly
addressable. Legacy `kv.memory.*` merge settlement also chooses `--theirs`, so
it cannot represent the required competing-head state.

## What remains open

- Select and review the production representation. The fixture uses full
  revision-and-blame JSON plus separate view and head tables because that is
  the smallest reversible proof, not because every provider must use that
  schema.
- Finish the complete public operational Module version and bind production
  adapters behind the optional A1 source. The descriptor-only v1 was not
  widened by this spike.
- Wire native provider branch and merge operations to the proven durable Memory
  view/head-set semantics and define how the provider supplies the active view
  when it opens a Module. The fixtures establish the semantic model over both
  real transaction paths; they do not claim existing `bd` branch commands
  already drive those head sets.
- Add production concurrency and scale evidence around the selected
  representation. This suite proves stale-precondition behavior and the real
  publication boundaries; it does not benchmark or freeze an indexing plan.

The intended local publication gate is no longer blocked on canonical identity
or truthful acknowledgement. Promotion remains gated on the production choices
above rather than on feasibility of the caller contract.

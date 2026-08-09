# Memory Beads A2: revision identity and publication spike

Status: executable contract prototype; provider integration blocked. This
report answers spike A2 from
`beads-memory/specs/plans/memory-beads-architecture-spikes.md` without claiming
that existing Beads providers already conform.

## Verdict

The proposed caller-visible revision contract is coherent. An independently
represented append-only provider implements it without exposing provider commit
identifiers, and the reusable black-box suite passes.

A2 as a provider gate has not passed. Existing embedded/direct and proxied/UOW
seams cannot yet expose durable canonical Memory revision identities or
distinguish known publication with failed verification from genuinely unknown
acknowledgement. Adapting those paths today would either expose Dolt commit
hashes as product identity or report an unsafe retry outcome.

## Executable evidence

The prototype lives under `internal/memorybeads/spikea2`. It is internal and
does not extend `memoryops.Memories`, storage interfaces, or the descriptor-only
A1 module.

The provider-neutral contract covers:

- stable opaque revision addresses and complete exact reads of key, aliases,
  title, lifecycle, body, outgoing references, lineage, attribution, change
  message, origin, and provenance after archive, restore, branch removal, and
  provider maintenance;
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
go test ./internal/memorybeads/spikea2/...
go test -race ./internal/memorybeads/spikea2/...
go vet ./internal/memorybeads/spikea2/...
```

All three commands pass. Provider branch, maintenance, and fault controls are
fixture-only; they are not part of the caller interface.

## What the spike selected

- Memory revision IDs are immutable provider-issued addresses, not provider
  commit IDs.
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

These choices belong in the eventual versioned Memory Module. The prototype's
particular in-memory representation, ID spelling, cursor map, history traversal,
diff, and blame algorithms do not.

## Why current providers do not pass yet

The legacy memory role stores keyed config values and exposes no Memory Bead
identity, immutable revision repository, reference state, history, or
continuation.

The direct transaction path can report commit indeterminacy, but it does not
return a canonical Memory publication identity that lets the module distinguish
known publication followed by failed verification. The proxied unit-of-work
path likewise exposes neither that identity nor an acknowledgement-loss class.

Dolt commit hashes are not an acceptable shortcut. Branch deletion, history
flattening, garbage collection, and provider maintenance can change retention
of provider commits, while a public Memory revision must remain exactly
addressable. Legacy `kv.memory.*` merge settlement also chooses `--theirs`, so
it cannot represent the required competing-head state.

## Production work required to pass A2

1. Add provider-private immutable Memory revision storage and mutable view
   heads. Keep provider commit identifiers private.
2. Add a Memory-specific publication result seam that distinguishes failure
   before publication, known publication with incomplete verification, and
   unknown acknowledgement.
3. Implement embedded/direct and proxied adapters behind the optional A1
   source.
4. Run the same black-box contract against those adapters and the independent
   provider. Enable branch-specific cases only for providers that advertise
   branch semantics.

Use reversible provider prototypes to make those paths pass before freezing or
promoting an irreversible production schema. Do not claim A2 complete until all
three provider paths pass. Harness C and cross-provider interchange remain
gated on that local vertical slice.

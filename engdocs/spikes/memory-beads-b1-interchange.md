# Memory Beads B1: current-state interchange spike

Status: executable internal prototype. The guarded wire profile passed in both
directions between the two providers named here; no production provider or
public format is claimed.

## Verdict

A small current-state profile can move a connected task/memory graph in both
directions without changing its meaning or losing source attribution. The unit
needs a guarded declaration before its records, an explicit source project,
source-to-destination identity mapping, and one destination precondition
covering every participant.

The result does not close the production interchange gate. The intended local
providers still have the A2 release work described in the A2 report, and this
spike added a throwaway connected-unit coordinator that they do not yet have.

## What ran

The prototype is under `internal/memorybeads/spikeb`. It uses two different
state and publication models:

- The A2 adapter stages every imported memory through the existing append-only
  independent A2 provider, then publishes all new memory slots and task records
  with one pointer-map swap.
- The document provider stores the whole destination project in a JSON
  document, verifies its Project ID, checks its generation under a cross-process
  file lock, and publishes with an atomic file replace.

Both providers emit strict JSONL and consume it through the decoder before
preflight. The shared code is limited to wire and declaration validation,
locator mapping, and the connected import plan. Revision creation and
publication stay with each provider.

The fixture contains active and archived memories, a keyed and an unkeyed
memory, a task-to-memory reference, a memory-to-memory reference, an exact
current pin, a historical pin, and a foreign exact locator. Tests cover:

- A2 to document and document to A2 wire round trips;
- unchanged producer self-import;
- rejection by both supported legacy parser families at the first guarded line,
  before either returns issue, memory, or config data;
- strict rejection of unknown wire fields and misplaced or malformed
  declarations before provider preflight;
- unknown format, version, scope, and required capability;
- current and floating source-local mapping only when the unit creates the
  target;
- preservation of foreign locators, historical pins, and an outside-unit
  source target that happens to share a destination Bead ID;
- rejection of an unmapped floating local target;
- destination key conflict, a destination change after preflight, and two
  independent document-provider instances racing on the same destination; and
- an injected failure after staging but before publication, followed by a
  clean retry.

Both providers leave the destination unchanged on every rejected, stale, or
injected-failure path. Imported revisions use the accountable destination
importer as author and retain the source address, author, agent, message, and
origin as evidence rather than impersonating the source author.

Run the evidence with:

```bash
./scripts/test.sh -count=1 -run '^TestB1' ./internal/memorybeads/spikeb
go test -race -count=1 ./internal/memorybeads/spikeb -run '^TestB1'
./scripts/test.sh -count=1 \
  -run '^TestMemoryBeadsInterchangeGuardStopsLegacyParsersBeforeData$' ./cmd/bd
```

## Profile selected by the spike

The declaration is `memory-beads` / `b1-current-state-v1` /
`project-current-state` and requires connected atomic application plus exact
memory pins. Those spellings belong only to the prototype until a production
decision promotes them.

Import creates destination-owned identities and reports the complete mapping.
A source-local target maps to the new destination target only when the same
unit creates it. Its exported-current pin maps to the target's new destination
revision. A historical pin or an exact target outside the unit becomes
source-qualified. Foreign state stays foreign. A floating local target outside
the unit is rejected.

Self-import of an identical unit is unchanged. Other imports are conditional
on the destination generation observed by preflight; any intervening change
rejects the whole plan.

## What this exposed

The single-memory A2 mutation surface is not enough by itself to publish a
connected task/memory import. The spike needed a coordinator above it. That
coordinator is evidence for an atomic-unit requirement, not a production
implementation choice.

The exercised profile does not transfer full history, blame, aliases, provider
branches, governance, or provider extensions. It creates new participants and
rejects destination key conflicts; it does not choose or revise an existing
destination identity. File replacement here proves the tested process-level
boundary, not crash durability on every filesystem. Environmental limits and
large-unit behavior remain untested.

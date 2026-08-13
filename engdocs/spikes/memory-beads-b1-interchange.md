# Memory Beads B1: current-state interchange experiment

**Status:** Executable internal evidence. No public wire format or production provider behavior is selected.

## Question

Can two differently implemented providers exchange a connected task/Memory current-state unit without silent retargeting, false attribution, or partial destination publication?

## What ran

The prototype under `internal/memorybeads/spikeb` used two different state and publication models:

- an adapter over the independent A2 fixture staged Memory state and published its new slots and task records with one pointer-map swap; and
- a document provider stored the destination project in JSON, checked its generation under a cross-process file lock, and published with an atomic file replace.

Both emitted and consumed strict JSONL through a shared declaration and mapping layer. Provider-specific state creation and publication remained separate.

The fixture included active and archived Memory, keyed and unkeyed Memory, task-to-Memory and Memory-to-Memory references, current and historical addresses, and an outside-project address. Tests exercised bidirectional round trips, unchanged self-import, fail-closed parsing, unknown declarations and fields, explicit identity mapping, omitted targets, destination key conflicts, stale destination state, concurrent document-provider instances, and injected failure before publication.

Rejected, stale, and injected-failure paths left the destination unchanged. Successful imports used the accountable destination importer while retaining source attribution and origin as provenance rather than impersonating the source author.

Run the evidence with:

```bash
./scripts/test.sh -count=1 -run '^TestB1' ./internal/memorybeads/spikeb
go test -race -count=1 ./internal/memorybeads/spikeb -run '^TestB1'
./scripts/test.sh -count=1 \
  -run '^TestMemoryBeadsInterchangeGuardStopsLegacyParsersBeforeData$' ./cmd/bd
```

## Current interpretation

The experiment supports a small portable claim: interchange can be self-identifying, scope-aware, explicitly mapped, and all-or-nothing for the connected unit. A single-record mutation surface was insufficient for the tested connected import, but the coordinator used here is only one implementation.

The prototype declaration names, JSONL encoding, provider transactions, exact-address mapping rules, and destination generation mechanism are not a standard. The experiment did not transfer full history, blame, provider branches, governance, or provider extensions, and it did not test large units or filesystem crash behavior. Cross-project resolution remains outside the Memory proposal even though the fixture preserved an outside-project address as uninterpreted structured state.

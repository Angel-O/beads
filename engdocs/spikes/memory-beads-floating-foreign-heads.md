# Floating foreign-current-state model comparison

**Status:** Executable Beads-wide research. Cross-project current-state resolution is outside the Memory Beads proposal.

## Question

What evidence would be needed to choose the current state of a foreign Project without relying on a stored exact historical address?

## What ran

`CompareFloatingModels` evaluated identical observations under two fixture rules:

1. **Owner-designated:** only a route named by verified owner evidence could select current state. A matching, fresh, non-forked owner observation succeeded.
2. **Synchronized consensus:** routes proved synchronization and freshness and agreed on an opaque state, but agreement alone could not establish which provider was entitled to define current state.

The executable cases covered route-order permutations, a stale designated owner, competing owner observations, wrong Project identity, synchronized replicas without owner evidence, missing synchronization evidence, and owner denial alongside a more revealing replica. Ambiguous or unauthorized cases failed closed without falling through to a different route.

Run the evidence with:

```bash
go test ./internal/memorybeads/spikeb -run '^TestFloating'
go test -race ./internal/memorybeads/spikeb -run '^TestFloating'
```

## Current interpretation

The model comparison suggests that replica freshness and agreement do not by themselves prove authority over foreign current state. An owner-designated approach can work in the model only when owner identity, route identity, freshness, and fork handling are independently established.

No production provider in this experiment supplied that evidence. The authority proof was a fixture input, not a wire format or trust algorithm. The result does not select exact references or floating resolution for a Memory release; it records constraints for a future Beads-wide cross-project design.

# B2: exact foreign-reference resolution experiment

**Status:** Executable Beads-wide research. Cross-project resolution is outside the Memory Beads proposal.

## Question

Can a project preserve and inspect an exact foreign reference independently of routing, and can different resolver implementations report useful results without rewriting that stored reference?

## What ran

Two fixture paths shared only a provisional result shape:

- a direct adapter routed to an in-memory `ProjectEndpoint`; and
- an HTTP adapter crossed a real local transport to an independently implemented flat `ResolverDocument` authority.

The fixtures exercised unconfigured routing, denial, unavailability, Project identity mismatch, expected scope and kind mismatch, a missing historical state, exact success, and route relocation. Only success returned a body. Denial returned no target address, mismatch detail, or body.

A separate `ReferenceCatalog` checked structural validity before storage. Changing the route did not change the stored or exported locator. The source fixture could still read, search, inspect history, and traverse its local outgoing references with no resolver installed.

Run the evidence with:

```bash
go test ./internal/memorybeads/spikeb -run '^TestB2'
go test -race ./internal/memorybeads/spikeb -run '^TestB2'
```

## Current interpretation

The experiment shows that an exact foreign address can remain useful structured data without a working route, and that resolution can be a separate non-mutating observation. It does not define a public resolver, Historical Bead Reference format, authentication model, route authority, availability contract, or cross-project product behavior. Those are general Beads concerns and require evidence from real providers before becoming a shared contract.

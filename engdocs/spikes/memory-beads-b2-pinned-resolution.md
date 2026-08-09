# Memory Beads B2: pinned foreign resolution spike

Status: executable internal prototype. Two independently implemented fixture
providers satisfy the same provisional resolver operation. No public resolver
package or production routing claim is made.

## Verdict

An exact foreign locator remains useful without routing. The source can store,
inspect, and export Project ID, Bead ID, exact revision, expected scope, and
expected memory kind before any resolver exists. Resolution is a separate
observation and never rewrites that state.

Resolver absence also does not break ordinary local Memory Module behavior.
The B2 suite stores an exact foreign reference in A2's independent Memory
Module, then successfully reads the memory, searches current state, inspects
history, and traverses the exact source revision's outgoing references without
installing a resolver.

## Independent adapter evidence

The two paths share only the provisional `Resolver` result contract:

- the direct adapter routes to `ProjectEndpoint`, whose target state is a
  nested in-memory revision map; and
- the HTTP adapter crosses a real local transport to `ResolverDocument`, whose
  target state is a flat document record set with its own policy vocabulary and
  independent resolution implementation.

The HTTP path does not translate to `ProjectEndpoint` or call its resolution
method. Both implementations exercise resolver unconfigured, denial,
unavailability, Project identity mismatch, expected scope and kind mismatch,
missing exact revision, exact success, and route relocation. Only success
carries a body. Denial carries no target address, mismatch detail, or body.

`ReferenceCatalog` separately proves fail-before-storage structural validation.
Relocation changes only adapter routing; the stored and exported locator is
unchanged as structured state.

Run the evidence with:

```bash
go test ./internal/memorybeads/spikeb -run '^TestB2'
go test -race ./internal/memorybeads/spikeb -run '^TestB2'
```

## Seam and release gate

The fixture implementations justify one candidate semantic operation: accept a
stored exact reference and return resolver unconfigured, denied, unavailable,
Project mismatch, scope mismatch, kind mismatch, missing revision, or resolved.
Only `resolved` returns the exact addressed memory.

This is enough to retain the seam inside the spike. It is not enough to freeze
a public Project Resolver: neither adapter is a production provider with real
identity, authentication, routing, and availability behavior. A production
adapter gate remains before the interface becomes a compatibility promise.

## Limits

The spike does not prove remote authentication, transport identity, route
freshness, replica selection, bounded failure details, or production
availability. The portable target remains an exact Memory Revision; task
targets and floating foreign heads are outside this seam.

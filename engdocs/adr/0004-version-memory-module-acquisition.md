# ADR-0004: Acquire Memory Beads through an optional versioned source

## Status

Proposed — the acquisition seam was validated by spike A1 on 2026-08-08;
the out-of-tree backend compile gate remains pending.

## Context

The shipped `memoryops.Memories` role and the public storage interfaces are
append-closed contracts implemented outside this repository. Memory Beads need
a deeper API, but adding methods to any of those interfaces would break existing
consumers and backends. Direct, embedded, proxied, and decorated execution also
need one caller-facing acquisition rule.

## Decision

Publish each Memory Module version in its own leaf package. Version 1 is
acquired through `memorybeads/v1.Acquire(source)`. A source opts in by
implementing the optional `memorybeads/v1.Source` interface; unsupported
sources return the repository's existing typed capability error.

Concrete stores, unit-of-work providers, and decorators may implement or
forward the optional source method. The method is not added to
`memoryops.Memories`, `storage.Storage`, `storage.DoltStorage`,
`backend.Storage`, `backend.DoltStorage`, or `uow.UnitOfWorkProvider`.

The spike module exposes only a storage-neutral descriptor. Operational methods
belong to the Phase 2 vertical slice and must remain free of storage, SQL,
transaction, and transport types.

The descriptor-only package is evidence, not a releasable v1 contract. Before
promotion, Phase 2 must define the complete operational v1 interface. If any
descriptor-only version has already shipped, operations go into a new version
or separate optional capability; the released interface is never widened.

## Evidence

- A public-only compile fixture pins the existing four-method memory role.
  Public method-set guards fail if the optional accessor is added to that role
  or either public backend interface; those interface definitions remain
  unchanged, preserving current consumer and backend implementations.
- A backend-typed value that does not opt in exercises typed unsupported
  acquisition. It is not presented as a complete out-of-tree implementation.
- The same `Acquire` call succeeds for server-backed Dolt, embedded Dolt, and
  proxied unit-of-work sources.
- Hook, telemetry, notification, request-timing, and public gate decorators
  preserve the optional capability.
- A provider that does not opt in returns `*beadserrors.ErrUnsupported`.

The focused package suites pass in the normal and non-cgo build paths. These
tests remain as compatibility guards beside the acquisition code.

The branch deliberately does not call the backend-typed value a concrete
out-of-tree implementation: embedding `backend.DoltStorage` would make that
claim vacuous. Before A1 is declared complete or this package is released, a
real out-of-tree backend must compile unchanged against the selected seam. The
active flat-file backend work is the most direct candidate once it is cleanly
based on the selected main revision.

## Consequences

New versions can evolve without widening old interfaces. A decorator must
forward optional capabilities deliberately, and a caller must handle an
unsupported provider explicitly. This ADR selects only acquisition and
versioning; it does not bless the descriptor-only spike module as the final
Memory Beads operation surface or authorize releasing it before Phase 2.

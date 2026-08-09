# ADR-0004: Acquire Memory Beads through an optional versioned source

## Status

Accepted — spike A1 validated the acquisition seam and its out-of-tree
source-compatibility gate on 2026-08-08. The descriptor-only module remains
spike evidence rather than a releasable operation contract.

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
- A separate Go module under `memorybeads/v1/testdata/a1external` implements
  the complete public `backend.DoltStorage` method set explicitly. It imports
  public Beads packages only and does not anonymously embed the interface it
  claims to implement. The same concrete value remains a valid caller without
  `MemoryModuleV1` and receives typed unsupported acquisition.
- The same `Acquire` call succeeds for server-backed Dolt, embedded Dolt, and
  proxied unit-of-work sources.
- Hook, telemetry, notification, request-timing, and public gate decorators
  preserve the optional capability.
- A provider that does not opt in returns `*beadserrors.ErrUnsupported`.

The parent compatibility test invokes that module with the network and
workspace disabled, module updates forbidden, and its source hashed before and
after the compile. This proves the checked-in external source compiles unchanged
against the current checkout; it does not claim that the deliberately
unsupported fixture has production storage behavior. The focused package
suites pass in the normal and non-cgo build paths. These tests remain as
compatibility guards beside the acquisition code.

## Consequences

New versions can evolve without widening old interfaces. A decorator must
forward optional capabilities deliberately, and a caller must handle an
unsupported provider explicitly. This ADR selects only acquisition and
versioning; it does not bless the descriptor-only spike module as the final
Memory Beads operation surface or authorize releasing it before Phase 2.

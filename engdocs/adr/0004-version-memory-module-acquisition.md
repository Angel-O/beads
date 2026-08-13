# ADR-0004: Optional Memory acquisition seam experiment

## Status

Experimental evidence from spike A1, completed 2026-08-08. This document does not select a public API or make Memory Beads an optional product module.

## Question

Could new Memory operations be reached without adding methods to the append-closed `memoryops.Memories` role or existing public storage interfaces implemented outside this repository?

## Experiment

The spike added a descriptor-only `memorybeads/v1` package with `Acquire(source)` and an optional `Source` interface. It exercised the same acquisition call with server-backed Dolt, embedded Dolt, and proxied unit-of-work sources. Hook, telemetry, notification, request-timing, and public-gate decorators forwarded the experimental capability.

An out-of-tree module under `memorybeads/v1/testdata/a1external` explicitly implements the public `backend.DoltStorage` method set. It compiles unchanged without implementing the experimental source and receives the existing typed unsupported-capability result. Public method-set guards verify that the spike did not widen `memoryops.Memories` or the public backend interfaces.

The compatibility test disables network and workspace access, forbids module updates, and verifies that the external fixture source is unchanged before and after compilation. Focused tests passed in the normal and non-cgo build paths.

## Result

An optional acquisition seam is technically feasible without breaking the interfaces exercised by the fixture. Decorators can preserve such a seam deliberately, and unsupported implementations can fail explicitly.

This result is narrower than the current product model:

- Memory Beads is a first-class core Bead kind beside Task Beads.
- The experiment does not choose the production package boundary, acquisition API, or provider interface.
- The descriptor-only package is not an operational Memory contract.
- Shared Beads History, Versioned Bead, graph, and write contracts remain outside this experiment.

The A1 code is retained as interface-compatibility evidence, not as an accepted API or product-placement decision.

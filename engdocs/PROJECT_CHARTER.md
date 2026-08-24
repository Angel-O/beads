# Project Charter

This document defines the product boundary for beads. It is the source of
truth for deciding whether proposed work belongs in core, belongs in an
integration or plugin, belongs in an orchestration layer, or should stay
outside the project.

Beads is a focused system of record for AI-supervised software projects. It
owns two peer core concepts: project work and the durable project knowledge
that informs that work. It should stay small enough to remain reliable,
understandable, and composable.

## Core Scope

Beads owns these core primitives:

- canonical Bead identity and kind dispatch
- Task Beads for project work, including lifecycle, dependency relationships,
  readiness, labels, comments, status, priority, assignment, and task metadata
- Memory Beads for durable Markdown project knowledge, including their
  active-or-archived lifecycle, selective discovery, and complete recall
- project-local graph relationships between supported Bead kinds
- local CLI workflows around Task and Memory concepts
- import, export, sync, backup, and recovery for beads data
- integrations that translate external tracker data into Task Bead concepts

Within those boundaries, the project should absorb useful contributor work
when practical. If a contribution has value but does not fit as submitted,
prefer preserving the value by simplifying it, moving it to metadata, routing
it to an integration or plugin, cherry-picking the reusable part, or
reimplementing the use case in a smaller design.

## Orchestration Boundary

Beads should not know about orchestration layers built on top of it. Systems
such as schedulers, swarms, release coordinators, and future
workflow engines may use beads, but beads should not encode their concepts in
core.

Core Beads can expose stable Task and Memory data, metadata, CLI output, and
documented extension points. The orchestration layer owns orchestration policy:
agent routing, task assignment strategy, model choice, retry plans, scheduling,
workflow semantics, and cross-system coordination.

When orchestration needs extra per-task data, prefer Task metadata before
adding first-class fields or commands. Application-specific Memory structure
belongs in its Markdown or frontmatter unless it becomes portable core
semantics.

## Storage Boundary

Beads should not become a storage engine. Storage providers supply persistence
and the capabilities they advertise through deliberate Beads boundaries. Dolt
is the current primary provider, not the definition of Task or Memory semantics.
Shared Beads contracts define portable history, versioning, sync, merge,
concurrency, and recovery outcomes when those capabilities are present.

Storage-engine details should not leak into beads packages unless they are part
of a deliberate storage interface. Avoid beads-side flocks, engine
introspection, storage-specific retry loops, crash-recovery workarounds, or
schema poking that belongs in Dolt or the Dolt driver.

If a storage boundary cannot express a needed operation, widen the deliberate
interface or route the issue to the provider instead of embedding
storage-engine logic in core.

This boundary is mechanically enforced for non-test code by a `depguard`
rule in `.golangci.yml` that denies `github.com/dolthub/` imports outside
`internal/storage/` and `internal/doltserver/` (the linter config does not
analyze `_test.go` files). The rule's `files` list documents the only
justified exceptions (the proxied-server surface and DoltHub's `eventkit`
telemetry client) alongside why each one is allowed.

## Cross-Cutting Capability Boundary

Storage-provider choice; embedded or remote operation; single-user or
multi-user deployment; cross-project federation; authentication and
authorization; governance; History; and interchange are cross-cutting Beads
concerns. They do not define what a Task Bead or Memory Bead means.

A core kind may require observable outcomes from a shared capability. Its
feature contract states those required outcomes without owning the general
capability's API, storage mechanism, routing, security model, or provider
implementation. An optional facility composes with every supported Bead kind
to which its own contract applies; its absence does not narrow the meaning of a
local Task or Memory.

## Schema Boundary

The database schema is considered stable. Schema changes are allowed when there
is a pressing product or correctness need, but they should not be the first
answer to extension requests.

Use Task metadata or Memory frontmatter first when:

- the data is specific to one integration, orchestrator, or team workflow
- the data is advisory rather than part of beads' core Bead model
- the data can be represented as JSON without harming queryability
- the shape may evolve before it deserves a stable CLI or schema contract

Promote data to first-class schema only when the field has broad, durable
meaning for a core Bead kind or shared Beads capability and the migration cost
is justified.

## Integration Boundary

Tracker integrations are adoption bridges, not a second product surface. They
should map external tracker data into Task Bead concepts and keep the dependency
graph useful. They should not replicate tracker UIs, notification systems,
credential vaults, webhook gateways, or cross-tracker automation.

See [Integration Charter](INTEGRATION_CHARTER.md) for the detailed policy for
GitHub, GitLab, Jira, Linear, Azure DevOps, and similar tracker integrations.

## Review Posture

These boundaries are fences, not bounce messages. Maintainers should not reject
useful work reflexively just because the first version crosses a boundary.

For pull requests and proposals:

- identify the contributor value first
- keep the part that belongs in core when possible
- move boundary-crossing behavior to metadata, integrations, plugins, or
  external tools when that preserves the use case
- preserve attribution when transforming, cherry-picking, or reimplementing
  contributor work
- explain clearly when a feature belongs outside beads

Use request-changes or rejection only after considering whether the project can
absorb, transform, or reroute the useful part.

## Related Documents

- [Integration Charter](INTEGRATION_CHARTER.md) - tracker integration scope
- [Issue Metadata](../docs/core-concepts/metadata.md) - metadata extension point
- [Architecture](../docs/architecture/index.md) - data model and storage architecture
- [Maintainer PR Guidelines](../PR_MAINTAINER_GUIDELINES.md) - PR triage and
  contributor handling

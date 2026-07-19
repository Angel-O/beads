---
plan_slug: v12-universal-upgrades
phase: design
rig: beads
rig_root: /data/projects/beads-v12-universal
artifact_root: /data/projects/beads-v12-universal/.gc/plans
requirements_file: /data/projects/beads-v12-universal/.gc/plans/v12-universal-upgrades/requirements.md
status: approved
created_at: 2026-07-18T00:00:00Z
updated_at: 2026-07-19T06:50:12Z
architecture_review: .gc/plans/v12-universal-upgrades/.council/ultra-architecture.md
---

# Design: Beads v1.2 Universal Upgrades

## 1. Architecture Decision

Build one storage-neutral upgrade planner/executor, backed by driver-owned
operations, and one catalog-driven black-box historical-release harness.

Production code does not contain one path per release. It normalizes physical
workspace evidence into a small set of format/provider/topology families and
selects a route from those facts. The test catalog maps every historical
producer to one of those probed families and proves the mapping against a real
workspace.

Startup and `bd migrate` share the same observer, route graph, safety
classification, and deterministic plan. Startup can execute only `NoOp` and
strictly bounded `StartupSafe` plans. Everything else is an explicit
`bd migrate` workflow.

Candidate construction is a barrier, not ordinary implementation. All product,
packaging, harness, oracle, qualification, and release-gate helper changes land
first. Their bytes are frozen, one exact candidate is then constructed, and all
qualification and publication-eligibility checks consume that epoch read-only.

## 2. Base Integration

The accepted upgrade chain and multi-provider base diverge from
`380c5b1dca372507dbfd457bd4d022fb4fec4cdb` and have no textual path conflicts.
Integrate them with a no-fast-forward two-parent merge:

```text
parent 1: af136f8857dd3e0461e06597f37e925088a98a49
parent 2: dce8d066de983b4fa4487890f48157a7264d86d2
```

This preserves the accepted chain's commit objects, tests, authorship, and PR
history while retaining the later multi-provider selection, binding, lifecycle,
and effect census.

Do not cherry-pick or squash the chain. Do not merge PR #4801's head
`a0a51638c`: its tree equals accepted commit `2ef7c61e0`'s tree. The integration
PR records that equivalence and preserves the intent, tests, accepted commits,
authorship, and attribution of both #4801 and #4845. Any boundary adaptation is
a separate reviewed commit after the provenance merge. The no-FF merge's
ordered parent 1 is exactly `af136f8857dd3e0461e06597f37e925088a98a49`
and parent 2 is exactly `dce8d066de983b4fa4487890f48157a7264d86d2`.

## 3. Components

### 3.1 Historical release and provider locks

Test-only checked-in inputs:

- `scripts/migration-test/releases.lock.json`
- `scripts/migration-test/applicability-evidence.lock.json`
- `scripts/migration-test/providers.lock.json`
- `scripts/migration-test/install-channels.lock.json`
- `scripts/migration-test/focused-cases.lock.json`

`releases.lock.json` records the cutoff, each `v*` tag and tag commit, release
classification, release/assets when present, producer variants, artifact
checksums or pinned source-build recipe, expected workspace family, and
supported platform/build flavor. Rows are stable and reviewable; remote GitHub
queries occur only in the explicit refresh/validation job, not every local test.

Each tag has an explicit variant ledger. It reconciles the standalone local
producer plus every historically applicable redirected/shared (`BEADS_DIR`,
worktree, or shared-server), remote/server-backed, and proxied-server form. A
variant is locked either as mandatory or as proposed-inapplicable with a pinned
introduction/removal boundary and immutable tag-bound negative create/open probe
specification. Build-flavor and platform variants use the same rule. U1 locks
this complete ledger and its probe specifications without executing historical
binaries. U2 later executes every specification with producer-fan-in bytes and
writes `applicability-evidence.lock.json`, binding result and binary digests.
Pending, missing, stale, or mismatched evidence leaves the identity mandatory.

`providers.lock.json` records every current provider/topology/build variant.
Its validator derives the denominator independently from configuration
constants and validation, init flags, store factories including non-CGO paths,
and storage implementations. The proposed lock and derived denominator must be
bijective.

`install-channels.lock.json` enumerates direct installation and every
maintained v1.2 distribution channel whose artifact can create or open a
workspace. Its validator rejects missing, duplicate, stale, or
non-workspace-producing classifications. Qualification installs the frozen
candidate bytes through channel-specific candidate paths without publishing a
stable release or mutating a maintained public channel.

The lock checker fails on missing/extra/duplicate tags, unknown families,
unbound producers, unexplained variant inapplicability, evidence not bijective
with locked probe specifications, constructor disagreement, or a changed
cutoff. U6 and U8 consume and validate both denominator and evidence locks. The
current baseline is 173 tags, 104 release records, and 714 assets; these numbers
are assertions about the cutoff, not hardcoded future truth.

### 3.2 Authentic binary and workspace factory

The existing `scripts/migration-test` harness remains the test surface. It is
extended rather than replaced with a second framework.

Responsibilities:

- derive a producer key from exact tag commit, platform, build flavor, recipe
  digest, and toolchain digest, using pinned `N/A` recipe/toolchain values for
  verified official artifacts;
- fan in before row execution: resolve an official artifact by exact checksum
  or build the exact tag commit with the pinned toolchain/build flavor exactly
  once per unique producer key per run, then distribute digest-verified
  immutable bytes;
- allow row workers to retrieve only the workflow-internal, digest-addressed
  producer bundle; prohibit external-origin fetching and compilation in workers;
- cache immutable historical binaries and workspace inputs by digest;
- create an isolated home, Git repository, hook path, workspace, and any pinned
  historical Dolt runtime;
- allocate row-exclusive ephemeral remote/server/proxy endpoints and
  credentials, construct a sanitized DSN environment, normalize endpoint
  aliases, reject production/shared endpoints, and deny execution-time egress
  except to declared row endpoints;
- invoke the historical binary to initialize and populate its own workspace;
- capture raw source identities before candidate execution; and
- verify the supplied `candidate_set_digest` and invoke only the exact
  manifest-selected platform/build artifact whose bytes match that row's
  `candidate_artifact_digest`.

Every terminal result carries its own provenance envelope: source tag/commit and
binary digest or separate build-recipe and toolchain digests; frozen helper
tree; candidate commit/tree, the one `candidate_set_digest`, and the
manifest-selected `candidate_artifact_digest`; OS/architecture and
runner/container identity; relevant allowlisted environment; external Dolt
identity or explicit version-gated `N/A`; and origin plus digest for every
downloaded input. The aggregator validates these fields on each result rather
than joining them from ambient job metadata.

Before either binary runs, the workspace factory creates fresh row-local
`HOME`, XDG, repository, and database roots; forces `core.hooksPath` into that
repository while disabling system/global hook influence; and resolves every
`BEADS_DIR`, `BEADS_DB`, `BD_DB`, and provider storage path beneath the row
root. Realpath and inode guards reject aliases to the production checkout, its
`.beads`, or a configured production database before either binary is invoked.
Acquisition and source builds finish before row workers enter their execution
sandbox; no external-origin acquisition network access remains available during
row execution. Remote/server/proxy setup uses a fresh endpoint and credential namespace per
row. The worker environment is rebuilt from an allowlist, contains only its
sanitized declared test DSNs, normalizes aliases, rejects any production/shared endpoint before
execution, and applies an egress allowlist while the old and candidate binaries
run. Result and retained-log self-tests seed credentials, authenticated URLs,
tokens, user names, hosts, DSNs, and filesystem locators and prove they are
redacted while immutable digests and opaque identities remain. Cross-row
endpoint leakage, production-DSN, and undeclared-egress mutants must fail.

Acquisition/build/run failure is a terminal failed row. The harness has no
success-bearing `SKIP` state.

### 3.3 Independent semantic oracle

The oracle keeps raw generation-specific snapshots and a normalized semantic
view. Normalization is version-aware but independent of production route
selection. It verifies all concepts the source generation could create, not
fields that did not yet exist.

The deep oracle has an explicit, per-generation feature matrix. For each source
it individually covers ID, title, description, status, priority, type, every
supported timestamp, assignee, owner, external reference, and custom metadata;
label values; comment bodies/authors/timestamps; dependency endpoints/types;
blocker/readiness behavior; issue/label/comment/dependency/readiness counts;
workspace and repository identity; and applicable branch, remote,
redirected/shared, server, and proxied semantics. It then tests create, field
update, dependency add/remove and resulting readiness changes, close, semantic
export, reopen, and completed-migration `NoOp` plus another reopen.

Every named cell is either exercised as supported or marked absent with a
pinned feature boundary and a negative invocation against the historical
binary. Empty fixtures and broad "supported fields" assertions are invalid.
Refusal, dry-run, failed-apply, and rollback paths compare raw source hashes or
the strongest immutable driver identity; rollback must reopen and match the
original oracle.

Minimal per-tag smoke always creates nonempty data and verifies direct upgrade,
reopen, mutation, and idempotent rerun. Its `smoke_row_id` is exactly
tag/producer/topology/platform/build-flavor, and every mandatory identity emits
one direct terminal result; smoke is never deduplicated. Deep oracle coverage
uses the separate `focused_case_id` namespace, keyed by exact
suite/equivalence-class/fault-case, and executes one result for every required
four-axis equivalence class or focused fault/current-provider/install-channel
case, including a semantic-only feature/operation boundary even when physical
format, route, and topology do not change.

The focused denominator is derived independently as the deterministic union of
the feature/equivalence matrix, provider lock, install-channel lock, and
`focused-cases.lock.json` fault/route registry. Validators reject deleting a
source entry or one of its derived cases.

A focused case may cover multiple identities only when the manifest separately
proves identical physical format/schema, selected route/ordered plan,
provider/topology, and semantic feature/operation set. Its
`covered_identities` are traceability metadata only and never emit, satisfy,
reuse, or duplicate smoke results. Each terminal result has exactly one
namespace key; both/neither keys and unknown coverage references fail.
Aggregation validates the complete
`smoke_row_id` and `focused_case_id` denominators independently.

### 3.4 Storage-neutral migration core

A small internal migration core owns only:

- normalized workspace observations;
- the route graph;
- deterministic plan ordering and rendering;
- the `NoOp`, `StartupSafe`, `ManualRequired`, and `Unsupported` safety class;
- orchestration of opaque storage capabilities; and
- typed outcomes consumed by CLI adapters.

It does not inspect engine files or SQL, implement locks or retries, provision a
provider, infer crash completion, or expose driver internals. Historical
workspace semver and requested destination provider may appear in diagnostics
or consent validation but are never route authority.

The core accepts observations only from bounded, storage-driver-owned,
read-only probes. All applicable probes run without opening or provisioning a
store; conflicting/indeterminate evidence is typed failure, and registry order
cannot select a winner. A requested destination is checked only after physical
evidence has selected a route that explicitly permits it. Unknown physical
formats fail closed rather than falling through to a current provider.

Provider-local schema migration and provider-changing migration stay distinct.
The only backend-changing route admitted by this design is the existing
embedded-Dolt-to-PostgreSQL path. Version upgrades never silently select a new
provider.

### 3.5 Storage and driver contract

`internal/storage` exposes the minimum storage-neutral capabilities needed by
the migration core. Conceptually these cover:

```text
InspectExistingReadOnly -> Observation
PlanCapabilitiesReadOnly -> CapabilitySet
AcquireQuiescence(expected source identity) -> quiesced probe scope
Prepare(plan) -> opaque prepared target/checkpoint
Verify(prepared target, oracle contract)
Activate(verified target, expected source identity)
ResumeOrRestore(opaque checkpoint)
```

Concrete naming follows existing interfaces and `dolthub/driver`; this is not a
new public SDK. Source/provider adapters own engine behavior, durability, and
cleanup. The migration core treats checkpoints and identities as opaque.

Before adding a Beads implementation, an interface-conformance test proves the
required driver capability exists. If it does not, create and link a real issue
in `dolthub/driver`, block only the affected route, and continue all independent
catalog/harness/current-route work. No Beads-side flock, engine inspection,
file-copy cutover, or retry substitute is allowed.

For every mutating path, including startup `StartupSafe`, the adapter acquires
driver-owned quiescence before the first write, re-runs observation and
capability discovery, and has the core recompute the complete route, safety
class, ordered steps, prerequisites, and verification criteria. Apply starts
only if the canonical recomputed plan and source identity equal the inspected
values. Changes to source data, capability answers, configuration, topology,
or any plan byte abort before any snapshot, prepare, or other mutation.

### 3.6 Startup and CLI adapters

The pre-store classifier runs before provider open, version tracking,
auto-import, provisioning, telemetry, or metadata writes.

```text
read-only inspect
      |
      v
deterministic plan ---- Unsupported/indeterminate ---> typed refusal, no write
      |
      +---- NoOp ------------------------------------> normal provider open
      |
      +---- StartupSafe ---> quiesce/recompute/match ---> driver apply
      |                                             ---> re-probe/verify ---> open
      |
      `---- ManualRequired --------------------------> upgrade_required, no write
```

Startup-safe is structural, not based on a row-count or elapsed-time threshold:
same local provider/topology, fixed work, no bulk/data-dependent operation, no
scan, copy, backfill, index build, snapshot/cutover, remote coordination, or
destructive rewrite, and driver-declared atomic or complete rollback safety.

The existing `bd migrate` UX is reused:

- `--inspect` prints source, selected route, safety class, prerequisites, and
  blockers without mutation.
- `--dry-run` performs all non-mutating preflight and prints the canonical plan
  and verification criteria. It creates no lock, snapshot, or receipt.
- no flag prints the same plan and asks for TTY confirmation.
- `--yes` is explicit noninteractive consent. After acquiring driver-owned
  quiescence it recomputes the complete plan and refuses if source,
  capabilities, configuration, topology, or any canonical plan byte changed.

Manual execution orders snapshot/preservation, prepare, independent verify,
storage activation, configuration/topology and authority cutover as the last
mutation, then read-only final verification. Rerunning an interrupted operation
resumes or restores through the driver; rerunning a completed operation is
`NoOp`.

## 4. Qualification Model

### Tier A: unit and contract tests on every PR

- manifest and provider-lock validation;
- deterministic route selection and plan rendering under randomized input
  order;
- startup-safety classification;
- pre-open read-only and fail-before-mutation behavior;
- driver prepare/verify/activate/restore contracts with injected failures;
- unsupported-route isolation; and
- provider-local schema behavior for Dolt, SQLite, PostgreSQL, and MySQL.

### Tier B: representative authentic E2E on every relevant PR

- v0.49.6 SQLite;
- v0.55.4 and v0.57 legacy Dolt;
- v0.62 server Dolt and public bridge/apply;
- v0.63.3 embedded Dolt;
- v1.0 and v1.1 current-era sentinels selected by the manifest;
- current workspaces for all four configured providers;
- embedded Dolt to PostgreSQL; and
- fail-before-open, source-unchanged, rollback, and idempotent-rerun cases.

### Tier C: exhaustive nightly

Before fan-out, the workflow seals a disposable prequalification record binding
the tested commit/tree and every build input, then invokes the U5-built pipeline
once in explicitly non-release mode to build/package one disposable candidate
set marked permanently ineligible for U8, release qualification, or
publication. Every shard receives its identical `candidate_set_digest`,
manifest, and immutable artifact inventory and may execute only the
manifest-selected artifact matching its platform/build
`candidate_artifact_digest`; no shard may rebuild or substitute it.

A producer fan-in independently materializes each unique historical producer
key once and distributes immutable verified bytes through the workflow's
digest-addressed artifact transport. Row workers never fetch from external
origins or compile. The matrix deterministically shards every mandatory `smoke_row_id`
and required `focused_case_id`. Each shard emits exactly one result per assigned
key and uploads machine-readable results plus concise failure logs. The
aggregator independently rejects missing, extra, duplicate, skip-like,
candidate-set-digest-mismatched, or selected-artifact-digest-mismatched results
and reports smoke `N/N` and focused `M/M`.

The workflow has a UTC-daily `schedule` trigger in addition to explicit manual
dispatch. A structural test parses the workflow and proves the scheduled path
selects the complete releases lock and exhaustive job rather than a
representative subset; removing, disabling, or retargeting that trigger fails
CI.

### Tier D: v1.2 candidate qualification

Build/package one multi-platform candidate set once, record its
`candidate_set_digest` and every manifest entry's
`candidate_artifact_digest`, and run:

- the entire Tier C matrix;
- deep tests at every distinct format/schema/topology boundary;
- crash points around prepare, verify, activate, and read-only final
  verification;
- competing actor, low disk, corruption, ambiguity, restore/reopen, and wrong
  artifact/PATH tests;
- current-provider fresh-create, defensive-open, and provider-local schema
  tests for every locked topology/build variant using the full nonempty R6
  oracle: issues/fields, labels/comments, dependency add/remove and readiness
  transitions, counts, mutations, close/reopen, semantic export comparison,
  completed-rerun `NoOp`, rollback/restore, and restored-source reopen where
  applicable; and
- every maintained install channel against the exact candidate artifact.

Only exact smoke `N/N` plus focused `M/M` for the same candidate set is
eligible for a later human release decision.

U6 depends on completion of U4's shared lifecycle-contract work. U4 may close
after that contract is complete and it has emitted children for every proven
missing route-specific capability. Those dynamic route children do not block
U6; they block U8 directly. U6 invokes U4-owned lifecycle tests and every row
executes, so a missing route capability is a terminal route-local `FAIL` while
all other rows continue. Universal `N/N` smoke and `M/M` focused passing belongs
to U9.

## 5. Candidate Epoch, CI, and Performance

The candidate lifecycle has four ordered phases:

1. **Pipeline implementation.** Land all remaining production, packaging,
   fixture, oracle, qualification-workflow, and release-gate helper changes.
   Disposable pipeline-test artifacts are not candidates.
2. **Cutoff refresh and byte-freeze barrier.** Re-run authoritative tag,
   release, asset, provider/topology/build, and install-channel discovery and
   fail back to source work on lock drift. After all prerequisite PRs merge,
   resolve the remote `feature/backend-provider-change-20260713` head, require
   it as the frozen commit, and finalize one freeze-record digest over its tree,
   build environment, recipes, and every qualification input. Every discovered
   U4 route child directly blocks this barrier; no open PR or unmerged helper
   change may remain in the epoch.
3. **Candidate construction and qualification.** Build/package one candidate
   set once from the exact frozen commit/tree with the finalized freeze-record
   digest as a hard stage input, then run all tiers without changing any frozen
   input, manifest, inventory, or artifact byte. A source mismatch or target-ref
   movement aborts the epoch.
4. **Read-only eligibility.** Consume only the frozen manifest and completed
   results and report eligible/ineligible. It consumes no PR-review records,
   reviewer assertions, participant lists, GitHub credentials, or controller
   receipts. This phase cannot modify source, rebuild, repair results, tag, or
   publish.

Any byte change after phase 2 invalidates the whole epoch and returns to phase
1; a new freeze, candidate identity, and full qualification are required.

PR jobs use cached representative binaries and stay bounded. Full catalog rows
are sharded by a stable hash of row identity so retry and aggregation are
deterministic. A pre-row producer fan-in keyed by exact tag commit, platform,
build flavor, recipe digest, and toolchain digest fetches or builds each unique
historical artifact once per run; a multi-row mutation test proves one
materialization and immutable fan-out. Failed shards retain small diagnostic
artifacts; successful shards need only result rows and checksums.

The candidate workflow emits exact immutable manifest bytes containing target-ref identity, commit,
tree, freeze-record digest, version, platform/build artifact names and SHA-256
digests, build metadata, environment identity, and the frozen-input digest
inventory. The SHA-256 of those exact manifest bytes is
`candidate_set_digest`; each artifact SHA-256 is a
`candidate_artifact_digest`. Every consumer verifies both before execution. A
local `bd`, `go run`, rebuilt binary, PATH shadow, changed helper, or mixed epoch
is rejected.

## 6. Failure and Recovery Invariants

At every boundary:

1. inspection and dry-run perform no workspace/storage mutation;
2. the complete plan exists before mutation;
3. the complete observation/capability/route/safety/step/verification plan and
   source identity are recomputed and matched under driver-owned quiescence;
4. the original remains authoritative until a prepared target passes
   independent verification;
5. activation validates the expected source and verified target, with
   configuration/topology and authority selection as the last mutation;
6. failure leaves exactly one authoritative store;
7. restore actually reopens and verifies the original; and
8. completed rerun is `NoOp`.

Any unavailable dependency, timeout, permission failure, incomplete probe,
unsupported capability, verification mismatch, or ambiguous provider evidence
is nonzero and failure-dominant.

## 7. Delivery and Review

Architecture changes use Sol/Ultra; implementation uses Terra/high with tests
written before behavior. Each implementation slice uses an isolated worktree.
Before a source commit, derive the digest directly from
`git diff --cached --binary --full-index`; exactly three independent Sol/Ultra
seats review those bytes and all three must approve with no unresolved Critical
or Important finding. Recompute immediately before commit. Any changed byte
invalidates all three reviews and requires three fresh reviews of the final
staged bytes.

PR creation and review labeling use standard `gh`/GitHub operations, not
product code. Every PR-producing child targets
`feature/backend-provider-change-20260713`, creates its PR with
`status/needs-review-auto`, captures the returned PR number/URL, and owns these
gates in its acceptance criteria. Verification checks that exact PR and fully
paginates GitHub GraphQL `timelineItems` until
`hasNextPage` is false and finds the exact `LabeledEvent`; Actor selection uses
schema-valid fragments, including `... on User { databaseId }` where identity
is needed. Automation consumption into `status/reviewing` is accepted from
history and the trigger is never re-added. Every nontrivial PR requires
substantive accountable-human review, and migration/schema/sync PRs never merge
on bot-only approval.

## 8. Deletions and Non-Designs

Use the operational controls above; add no checked-in review/bootstrap
controller, review-receipt or approval database, credential broker, commit/push
wrapper, attestation schema, PR/release orchestration service, generic storage
recovery framework, or provider conversion matrix. The former 99-slice
execution plan is retired in favor of eight source-work children and three
no-source freeze/qualification/eligibility barriers in `tasks.md`; completed
useful work and contributor history remain preserved in Git and Beads.

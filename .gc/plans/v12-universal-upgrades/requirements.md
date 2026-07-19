---
plan_slug: v12-universal-upgrades
phase: requirements
rig: beads
rig_root: /data/projects/beads-v12-universal
artifact_root: /data/projects/beads-v12-universal/.gc/plans
status: approved
created_at: 2026-07-18T00:00:00Z
updated_at: 2026-07-19T06:50:12Z
approval_basis: user-approved universal-upgrade goal and explicit instruction to proceed
---

# Beads v1.2 Universal Upgrade Requirements

## 1. Outcome

Every valid workspace created by any prior public Beads `v*` tag must have a
deterministic path to the exact packaged v1.2 candidate. Qualification is
complete only when every mandatory historical smoke identity and every required
focused case passes with no skips and the candidate set that passed is the only
candidate set eligible for a later, separate human publication decision.
Qualification itself never creates a stable tag or publishes a release.

The implementation starts from multi-provider commit
`af136f8857dd3e0461e06597f37e925088a98a49`. It must preserve the accepted
historical-upgrade chain ending at
`dce8d066de983b4fa4487890f48157a7264d86d2` as literal ancestry. PR #4801's
and PR #4845's accepted intent, tests, commit identities, authorship, and
contributor attribution must be preserved without merging duplicate equivalent
history.

## 2. Scope

In scope:

- Every public `v*` tag before the v1.2 candidate cutoff. The current
  denominator is 173 tags: 168 stable, two release candidates, and three
  `nosqlite` tags.
- All 104 GitHub release records and 714 release assets as acquisition and
  provenance inputs. Draft release status does not remove a public tag from the
  mandatory tag denominator.
- Historical SQLite, legacy/local Dolt, server Dolt, embedded Dolt, and
  current-format workspaces, including applicable shared, remote, and proxied
  topologies.
- Current Dolt, SQLite, PostgreSQL, and MySQL providers for fresh-create,
  defensive-open, provider-local schema upgrade, and no-op behavior.
- The already admitted embedded-Dolt-to-PostgreSQL backend migration.
- Direct installation and every maintained v1.2 distribution channel whose
  artifacts can create or open a workspace.

The historical topology/build inventory is explicit, rather than inferred from
the current defaults. For every tag it accounts for:

- repository-local standalone workspaces and every shipped build flavor;
- redirected or shared workspaces, including the historical `BEADS_DIR`,
  worktree, and shared-server forms that the release could create or open;
- remote or server-backed stores; and
- proxied-server workspaces.

Each tag/variant pair is either a mandatory case or carries pinned
version-gated evidence that the historical binary could not create or open that
variant. No shared, remote, proxied, platform, or build-flavor category may be
omitted merely because a current default differs.

Out of scope:

- Automatic cross-provider conversion or an arbitrary provider-pair matrix.
- Treating SQLite-to-PostgreSQL, MySQL conversion, PostgreSQL-to-Dolt, or other
  unimplemented provider changes as part of a version upgrade.
- A checked-in review/bootstrap controller, review-receipt or approval
  database, credential broker, commit/push wrapper, PR orchestration service,
  storage engine, or orchestration subsystem inside Beads.
- Stable-tag creation or public release publication without a separate human
  release decision.

## 3. Required Behavior

### R1. Authoritative release and provider denominators

1. A checked-in release lock must contain every cutoff `v*` tag exactly once.
2. Each row must bind the tag commit, release classification, workspace-format
   family, supported acquisition path, every historical platform/build/topology
   variant, and either verified official artifact checksums or a pinned
   period-correct source-build recipe. U1 locks the complete variant denominator
   and an immutable, tag-commit-bound negative create/open probe specification
   for every proposed non-applicable platform, build-flavor, shared, remote, or
   proxied variant. U2 executes those specifications with the exact historical
   binaries and writes a separate checked-in applicability-evidence lock. Prose,
   a current-binary inference, or pending, missing, stale, or mismatched evidence
   cannot remove an identity from the mandatory denominator.
3. A checked-in provider/topology lock must be independently derived from the
   provider constants, configuration validation, CLI initialization paths,
   store factories, build variants, and storage implementations in the
   multi-provider tree.
4. The denominator locks are bijective with both the tag denominator and the
   independently discovered producer/provider/topology variants. The evidence
   lock is bijective with the locked negative-probe specifications and binds
   each result to its historical binary digest. Missing, duplicate,
   unclassified, stale, or unexplained-inapplicable rows or evidence fail
   validation; U6 and U8 validate both lock classes.
5. A tag that lacks a runnable official asset must use its pinned source-build
   route; it must not be skipped.
6. A checked-in install-channel lock lists direct installation and every
   maintained v1.2 distribution channel whose artifact can create or open a
   workspace. Missing, duplicate, stale, or misclassified channels fail
   validation; qualification does not publish into those channels.

### R2. Authentic historical workspaces

1. A run-level producer fan-in keyed by exact tag commit, platform, build
   flavor, recipe digest, and toolchain digest must fetch or build each unique
   historical artifact exactly once, verify it, and fan out immutable bytes to
   all consuming rows. Official-artifact producers use pinned `N/A` recipe and
   toolchain values plus their artifact checksum. Row workers may retrieve only
   the workflow-internal, digest-addressed producer bundle; they never fetch from
   external origins or compile. Each historical row uses the fan-in-selected exact binary to
   initialize and mutate a nonempty isolated workspace.
2. Synthetic schemas and hand-authored fixtures may support unit tests but do
   not count as release qualification.
3. Each terminal result must embed immutable identities for the source tag and
   commit; source binary digest or source-build recipe and toolchain digests;
   the one `candidate_set_digest`, defined as SHA-256 of the exact immutable
   multi-platform manifest bytes containing the artifact inventory; the
   `candidate_artifact_digest` for the exact
   manifest-selected platform/build artifact executed by that result; candidate
   commit and tree; harness/helper tree; OS, architecture, runner/container
   image, and relevant environment allowlist; external Dolt
   binary/version/digest or an explicit version-gated `N/A`; and the origin plus
   digest of every downloaded input. An aggregate-side join is not a substitute
   for per-result provenance.
4. Each row must prove that `HOME` is a fresh directory under its temporary row
   root, `core.hooksPath` resolves inside its temporary repository, global and
   system hooks cannot run, and every resolved `BEADS_DIR`/`BEADS_DB`/`BD_DB`
   and storage path remains under that row root. The harness must fail before
   invoking either binary if a path aliases or enters the production checkout,
   its `.beads`, or any configured production database.
5. Every remote, server, or proxy row receives row-exclusive ephemeral
   endpoints and credentials and a newly constructed, sanitized DSN environment
   containing only its declared test endpoints. Before either binary runs, the
   harness normalizes endpoint aliases, rejects production or shared
   endpoint/DSN matches, and installs
   execution-time egress denial except to those endpoints. Credentials,
   authenticated URLs, tokens, user names, hosts, DSNs, and filesystem locators
   are redacted in terminal results and retained logs. Sentinel-secret,
   cross-row endpoint-leakage, production-DSN, and undeclared-egress self-tests
   must fail closed without erasing the immutable digests and opaque identities
   needed for diagnosis.
6. The candidate must inspect, migrate when required, reopen, verify, mutate,
   and reopen again. Repeating a completed migration must be a no-op.

### R3. One planner for startup and manual migration

1. Startup and `bd migrate` must use the same storage-neutral observation,
   route selection, safety classification, and deterministic ordered plan.
2. Only bounded, storage-driver-owned read-only probes may produce route
   observations. The migration core and CLI must not inspect engine files,
   query engine internals, launch a server, provision storage, or use registry
   order to break conflicting evidence.
3. Plans have exactly four outcomes: `NoOp`, `StartupSafe`, `ManualRequired`,
   or `Unsupported`.
4. Route selection is based only on probed physical format, provider,
   topology, schema generation, and driver capabilities. Historical semver and
   a requested destination provider are diagnostic/intent inputs only and may
   not select or override a route. Destination intent is validated only after
   evidence has selected a route that admits it.
5. An unrecognized historical format is a hard, typed failure and a release
   blocker; it is not opened as a current provider.

### R4. Startup and explicit migration UX

Before a normal command opens a store, launches an engine, provisions a
provider, runs version tracking, emits telemetry, or writes workspace metadata,
it must perform a bounded read-only inspection.

Startup may auto-apply a plan only when every step:

- remains on the same local provider and topology;
- has fixed work independent of workspace contents;
- performs no bulk scan, copy, backfill, remote coordination, index build,
  snapshot, cutover, or destructive rewrite;
- is declared atomic or fully rollback-safe by the storage driver; and
- requires no snapshot/cutover protocol.

All data-dependent transformations, historical bridges, server/embedded
transitions, provider changes, remote schema operations, and destructive steps
are manual. An ordinary store-opening command must then fail nonzero before
mutation with typed `upgrade_required` output and direct the user to:

1. `bd migrate --inspect` for read-only classification and blockers;
2. `bd migrate --dry-run` for the canonical non-mutating plan; and
3. `bd migrate` for interactive confirmation or `bd migrate --yes` for explicit
   noninteractive consent.

`help`, `version`, `bd migrate --inspect`, `bd migrate --dry-run`, and the
applicable `bd doctor` diagnostics must remain usable while ordinary commands
are blocked. No new top-level migration command is required.

Immediately before applying any mutating plan, including `StartupSafe`, the
implementation must acquire driver-owned
quiescence and use fresh driver probes to recompute the complete observation,
capability set, route, safety class, ordered steps, prerequisites, and
verification criteria. It compares that canonical plan and the source identity
with the consented plan and aborts without mutation if source data,
capabilities, configuration, topology, or any plan byte changed. For a manual
route, the target is prepared and independently verified while the source
remains authoritative; configuration/topology selection and authority cutover
are the final mutation, followed only by read-only final verification.

### R5. Storage boundary and recovery

1. Provider probing, provider open, quiescence, snapshot, transaction, cutover,
   rollback, restore, and crash guarantees belong behind `internal/storage` or
   `dolthub/driver`.
2. Beads must not implement engine introspection, file locks, engine-specific
   retry loops, or storage-specific crash recovery above that boundary.
3. A manual route that needs a missing driver primitive must fail closed and
   block only that route. Catalog, harness, current-provider, and unrelated
   historical E2E work must continue and emit their own terminal results. The
   affected mandatory row executes and reports `FAIL`, never `SKIP` or no
   result; only the final smoke `N/N` plus focused `M/M` barrier waits for
   every route to pass.
4. Routes requiring transformation prepare side-by-side, preserve the original
   as authoritative until independent verification passes, and activate the
   verified target last.
5. Interrupted execution must deterministically resume or restore through the
   same driver-owned lifecycle. At every point exactly one store is
   authoritative. A successful rerun is `NoOp`.

### R6. Fidelity and safety oracle

The pre/post oracle must independently verify every concept supported by the
source generation. Its version-gated feature matrix and every deep U2 result
enumerate, without catch-all shorthand:

- issue ID, title, description, status, priority, issue type, every
  source-supported timestamp, assignee, owner, external reference, and custom
  metadata;
- label values, comment bodies/authors/timestamps, dependency endpoints/types,
  blocker and readiness behavior, and issue/label/comment/dependency/readiness
  counts;
- workspace and repository identity plus applicable branch, remote,
  redirected/shared-store, server, or proxied topology semantics; and
- post-upgrade create, field update, dependency add/remove with readiness
  transitions, close, export with semantic comparison, and reopen, followed by
  another reopen after the completed migration reports `NoOp`.

For every named field, relationship, topology semantic, and operation, the row
must record either `supported` and perform the comparison/operation or
`absent` with pinned source-version evidence plus a negative historical-binary
probe. Silence, an empty fixture, or a catch-all "supported fields" assertion
does not prove version-gated absence.

Failure, refusal, or dry-run paths must prove the source workspace is unchanged
byte-for-byte or through the strongest driver-provided immutable identity.
Rollback tests must restore and reopen the original store; file existence alone
is insufficient.

Current-provider prequalification for Dolt, SQLite, PostgreSQL, and MySQL uses
the same nonempty oracle: all applicable issues/fields, labels/comments,
dependencies/readiness/counts, mutations, close/reopen, completed-migration
`NoOp`, and rollback/restore behavior must be exercised rather than reduced to
open/no-op/provider-retention checks.

### R7. Result semantics and coverage

1. `smoke_row_id` is the exact
   tag/producer/topology/platform/build-flavor identity. Every mandatory
   `smoke_row_id` emits exactly one direct terminal `PASS` or `FAIL`; direct
   smoke is never deduplicated.
2. `focused_case_id` is the exact suite/equivalence-class/fault-case identity.
   Every required deep, equivalence, current-provider, install-channel, or fault
   `focused_case_id` emits exactly one terminal `PASS` or `FAIL`. The
   denominator is the deterministic union of the feature/equivalence matrix,
   provider lock, install-channel lock, and a checked-in focused-case registry;
   deleting a required source entry or its derived case fails validation.
3. A focused case may be shared only after the manifest proves equality on all
   four independent axes: physical format/schema generation, selected route and
   ordered plan, provider/topology (including shared, remote, and proxied
   variants), and the version-gated semantic feature/operation set.
   `covered_identities` lists its traced
   tag/producer/topology/platform/build-flavor identities as metadata only; it
   never emits, reuses, satisfies, or duplicates a `smoke_row_id` result.
4. Aggregation independently validates the complete `smoke_row_id` denominator
   and the complete `focused_case_id` denominator. `SKIP`, warning-only,
   timeout, unavailable artifact, unsupported platform, blocked dependency,
   indeterminate probe, unknown coverage reference, missing, extra, or duplicate
   result is failure in its namespace. Every terminal result contains exactly
   one of `smoke_row_id` or `focused_case_id`; both or neither is failure.
   Progress reports both `passing smoke rows / total smoke rows` and
   `passing focused cases / total focused cases`, lists failures from each, and
   passes only at exact `N/N` and `M/M`.

### R8. Exact candidate and release gate

1. Candidate-build pipeline implementation is completed before final candidate
   construction. Immediately before freezing, read-only authoritative discovery
   revalidates the complete public-tag, release-record, release-asset,
   provider/topology/build, maintained-install-channel, and focused-case
   denominators against the locks; any drift returns to reviewed source work
   instead of shrinking or silently extending the epoch.
2. The freeze barrier resolves the current
   `feature/backend-provider-change-20260713` remote head after every prerequisite
   PR has merged, requires that exact commit to be the frozen commit, and records
   its tree plus a finalized freeze-record digest covering every production,
   packaging, fixture, semantic-oracle, qualification-workflow, release-gate
   helper, denominator/recipe, workflow, and build-input byte. Every discovered
   route child must directly block this barrier.
3. Only after the freeze record is finalized may the pipeline build and package
   one exact multi-platform candidate set from that immutable commit/tree and
   consume the freeze-record digest as a hard stage input. Its immutable
   manifest bytes contain the artifact inventory and have one SHA-256
   `candidate_set_digest`; each
   platform/build entry has its distinct `candidate_artifact_digest`. A
   missing/mismatched record, source checkout mismatch, or movement of the
   target ref before eligibility aborts the epoch. Every shard receives the
   identical manifest and inventory, and each row executes exactly the artifact
   selected by that manifest. Pipeline tests may use disposable candidate sets
   before the barrier; none counts as the final candidate.
4. Qualification is execution-only with respect to the frozen tree, helpers,
   manifests, fixtures, oracles, workflows, build environment, and candidate.
   It must execute the packaged artifact, never `go run`, an ambient `bd`, an
   in-tree substitute, or a per-shard rebuild.
5. Normal PR CI runs representative authentic boundaries and fast contracts.
   The exhaustive workflow has an explicit `schedule` trigger at least once in
   every UTC 24-hour period, and a structural workflow test proves that the
   scheduled event first seals a disposable prequalification record binding the
   tested commit/tree and all build inputs, then uses the U5-built pipeline's
   explicitly non-release mode to build/package one publication-ineligible
   candidate set before fan-out and select the complete sharded every-tag
   matrix. The record and candidate can never satisfy U8, and no shard may
   rebuild or substitute that set. Release
   qualification separately runs that matrix, deep boundary tests, current
   providers, fault tests, and maintained install channels against the exact U8
   candidate set.
6. Any production, packaging, fixture, oracle, qualification, release-gate
   helper, workflow, build-input, or artifact byte change crosses back over the
   freeze barrier, creates a new candidate identity, and invalidates every
   result from the prior qualification epoch. Inputs never change within an
   epoch.
7. The publication gate is a read-only consumer of the frozen manifest and
   qualification results. It reports eligible/ineligible and cannot rebuild,
   repair evidence, change source, create a tag, or publish a stable release.
   Eligibility requires one exact `candidate_set_digest`, a
   manifest-matching `candidate_artifact_digest` for every result, `N/N`
   smoke rows, and `M/M` focused cases with zero non-pass outcomes in either
   namespace; publication remains a separate human release action.

## 4. Non-Functional Requirements

- **Maintainability:** The release lock, route graph, result schema, and planner
  are small and reviewable. Test evidence is ordinary CI output and artifacts;
  no product-sized evidence subsystem is added.
- **Testability:** Planner and safety decisions are pure where possible. Driver
  contracts support deterministic failure injection at lifecycle boundaries.
- **Performance:** The exact producer-keyed fan-in materializes each historical
  artifact once per run before deterministic row fan-out; row workers may
  retrieve only workflow-internal digest-addressed bundles and never fetch from
  external origins or compile. Artifact and workspace inputs are cached by
  digest and the exhaustive matrix is deterministically sharded. Fast PR lanes
  do not download all historical releases.
- **Isolation:** Historical binaries run in fresh row-local homes/workspaces
  with repository-local Git hooks, explicit path-containment guards,
  row-exclusive ephemeral service endpoints and credentials, production-DSN
  rejection, and execution-time egress restricted to declared test endpoints.
- **Security:** Credentials, DSNs, and locators are redacted from results and
  retained logs; probes are storage-driver-owned, read-only, bounded to declared
  inputs, and do not scan arbitrary endpoints or provision missing stores.
- **Compatibility:** The work reuses existing `bd migrate` modes and does not
  silently change a user's provider or topology.

## 5. Acceptance Examples

| Given | When | Then |
|---|---|---|
| A real v0.49.6 SQLite workspace with issues and dependencies | The packaged candidate is run | Startup refuses unchanged, explicit migration preserves the oracle, and a second migration is `NoOp`. |
| A v0.63.3 embedded workspace needing only fixed provider-local work | A normal command opens it | Only a driver-declared `StartupSafe` plan may auto-apply; the candidate re-probes before opening. |
| A current PostgreSQL, MySQL, SQLite, or Dolt workspace | A normal command opens it | It remains on the same provider, performs only applicable provider-local schema work, and never provisions another provider. |
| A workspace with conflicting or incomplete provider evidence | Any store-opening command runs | It fails typed and non-mutating; registry order or configuration does not choose a winner. |
| A manual migration is interrupted after preparation | The same command is rerun | It resumes or restores deterministically and never exposes mixed authority. |
| One historical tag has no published Linux asset | The full matrix runs | Its pinned source-build producer runs; acquisition/build failure is `FAIL`, never `SKIP`. |
| One shard changes the candidate-set manifest or one row executes bytes whose artifact digest does not match its manifest-selected platform/build entry | Results are aggregated | The whole qualification fails even if every functional assertion passed. |
| A route requires a driver capability that is absent | Qualification reaches that route | That route fails closed and remains blocked on a real driver change; unrelated rows still run. |
| The nightly workflow is inspected | Its `schedule` configuration is parsed by the workflow test | A UTC-daily trigger reaches the exhaustive every-tag job; deleting or retargeting it fails the test. |
| Any frozen helper byte changes after candidate construction | Qualification is requested | The old epoch is rejected and a new freeze/candidate/full-qualification epoch is required. |
| A public tag, release asset, maintained install channel, or target-branch head changes after the locks were last reviewed | U8 attempts to freeze | Read-only cutoff discovery detects drift and no candidate is built until the affected source work is reviewed and merged. |

## 6. Delivery Controls

- Architecture and design changes use `gpt-5.6-sol` with Ultra reasoning.
- Implementation uses `gpt-5.6-terra` with high reasoning, TDD, and isolated
  worktrees.
- Before every source commit, derive the digest directly from
  `git diff --cached --binary --full-index`. Exactly three independent
  `gpt-5.6-sol`/Ultra reviewers inspect those bytes; all three must approve
  with no unresolved Critical or Important finding. Recompute immediately
  before commit; any byte change invalidates all three reviews and the final
  staged bytes receive three fresh reviews.
- Every nontrivial PR requires substantive accountable-human review before
  merge. Migration, schema, destructive-data, and sync paths never merge on
  bot-only approval.
- Every PR-producing live child targets
  `feature/backend-provider-change-20260713`, and its own acceptance criteria
  contain the staged-diff review, finding-resolution, fresh-review, label-event,
  and accountable-human gates.
- Every program PR is created using standard `gh`/GitHub tooling with
  `status/needs-review-auto`, and the returned PR number/URL is captured.
  Verification checks that exact PR and walks its GitHub GraphQL
  `timelineItems` connection through every page until
  `hasNextPage` is false and finds the corresponding `LabeledEvent`; the query
  uses schema-valid Actor fragments, including `... on User { databaseId }`
  wherever identity is required. If automation consumes the label into
  `status/reviewing`, the historical event is sufficient and the intake label
  is never re-added.
- No checked-in review/bootstrap controller, review-receipt or approval
  database, credential broker, commit/push wrapper, attestation schema, or
  PR-orchestration service is introduced.

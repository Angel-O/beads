## Architecture verdict

This authoring contract retains the universal-upgrade outcome for independent
review: one storage-neutral planner/executor backed by driver-owned
migration operations, plus a catalog-driven black-box historical harness. The
interrupted implementation is not approved by this architecture verdict.

Production selects routes from probed physical format, provider, topology,
schema, and driver capabilities. It never creates a code path per release,
uses a tag or semver as route authority, or moves engine behavior above the
storage boundary.

### U0 current-target correction

The prior literal start at `af136f885...` is stale. Current fetched target
`f7d0c26ec8c1e7b6b075cc49b07cb2f0f41c3a47` has sole parent `af136f885...`
and tree `8b0b9d93...`, exactly the tree of merged PR #4801's final head. Existing
PR #4907 head `0a0be15db...` still has parents `af136f885...` and
`dce8d066d...` and is now `DIRTY` against the target. Merged PR #4810 adoption
`1b5f02efd...` has its final PR-head tree but is not ancestry of the #4845 chain
tip `dce8d066d...`; `dce8d066d...` has the final #4845 tree. PR #4801's
final head is `e89ab9aa09bb178e2cfe1dec838e0e601f9663db`. Commit
`a0a51638c...` belongs to PR #4802, not #4801, and has the same tree as
accepted chain commit `2ef7c61e0...`.

U0 therefore extends #4907 rather than replaying or replacing contributor
work. From a freshly fetched, dynamically captured target OID, merge the
existing #4907 head with `--no-ff --no-commit`, review its staged resolution,
and commit only after three fresh independent Sol/Ultra approvals of the
identical staged-diff digest and recorded `git write-tree` index tree. After
commit and before any push, its commit tree must equal the approved index tree
and the SHA-256 of `git diff --binary --full-index HEAD^1 HEAD` must equal the
approved staged-diff SHA-256. Its ordered parents are exactly `[live target,
0a0be15db...]`. Then, from that reconciliation merge, merge `1b5f02efd...`
with `--no-ff --no-commit`, obtain three fresh approvals for that commit's
separate staged diff, and assert parents `[reconciliation merge oid,
1b5f02efd...]`. This keeps the PR update fast-forward from its old head and
makes the current target plus #4801, #4810, and #4845 accepted histories and
attribution literal ancestry. The overlaps are real: the architecture makes no
conflict-free, automatic-side-selection, or unproved byte-equivalence claim.
Resolution must preserve current target work and each accepted PR's tested
intent, with every changed byte freshly reviewed and tested.

Fetch the target again before accepting the corrected head. Movement invalidates
the resolution and its reviews; reinspect the graph and resolve fresh bytes
from the new target head. Final merge uses a repository-enforced up-to-date or
merge-queue guard bound to that exact base OID, so base movement automatically
invalidates eligibility rather than leaving a manual TOCTOU window.
`f7d0c26...` is recorded evidence, not a future command-time pin. PR #4907 is
the integration vehicle, not contributor intake. U0 captures canonical PR
number `4907`, URL, base and old/current head identities, and body digest;
after updating it verifies the same PR's corrected body/current head and fully
paginates the timeline to prove the existing historical
`status/needs-review-auto` `LabeledEvent`. A later `status/reviewing` state is
accepted, and the consumed trigger is never re-added.
Legacy bead `.3.27` closes on accepted #4845/`dce8d066d...` as the shipped
representation of its public-v0.62-bridge outcome, not on a same-numbered
literal commit.

### 1. Normative Initial U1 Evidence Fixture

The following **Normative Initial U1 Evidence Fixture (NIU1EF)** is the sole
authority in this plan for the initial reviewed identities and counts. Other
plan documents reference NIU1EF; quoted values in acceptance examples are
diagnostics, not independent constants. Later reviewed remote deltas append to
the locks, so U8 always derives its scope and evidence totals from the latest
reviewed lock and never requires these initial totals to remain current.

| Fixture key | Initial reviewed value |
|---|---|
| `historical.tag_names` | 173: 168 stable, two RC, three `nosqlite` |
| `historical.tag_revisions` | 179 `(tag_name, peeled_commit_oid)` identities: 178 visible across the two public remotes plus the superseded first public `v1.1.0` revision; by name class, 174 stable-revision identities, two RC, three `nosqlite` |
| `current_origin.base_refs` | 121 tag names, plus 70 peeled rows = 191 raw ref-observation rows |
| `historical.currently_absent_names` | 52: 47 lightweight, five annotated |
| `github.releases_assets` | 104 release records across 94 distinct release tags / 714 assets |
| `github.release_visibility` | 11 draft / 93 non-draft; 654 non-draft assets |
| `github.non_draft_asset_roles` | 560 executable archives / 90 checksums / four SPDX SBOMs |
| `github.nosqlite_official_assets` | zero |
| `providers.execution_identities` | Independently discovered and frozen by U1; no asserted count |
| `channels.execution_cases` | Independently discovered surface/branch/platform/manager/materialization cases frozen by U1; no asserted count |
| `v1.1.0.first_public` | tag object `48430e1de234cd134d900b80cd224a7413cfa1c3`, commit `71c8318de2f2a3546bcec4c48fcba375d3cfb6d7`, Release run `28696432661`, source-build-only |
| `v1.1.0.replacement` | tag object `7e7c8b995e5cb202032f0c9f777125fe252989e7`, commit `8e4e59d39f3459a43cf21a3236a13eca4dd874f7`, Release run `28696797389`, sole owner of official `v1.1.0` assets |

The historical denominator is the two-level NIU1EF ledger of tag names and tag
revisions, while current origin is a separate observation. Counts are derived
assertions, not generator constants. Every historical producer, variant, probe,
evidence row, and `smoke_row_id` uses `tag_revision_id`; a tag string is never
sufficient identity.

The 2026-07-22 live public-ref evidence has five divergent names, and U1 must
retain both peeled commits for each: `v0.58.0` (`origin ae14933d...`,
`groblegark c751b322...`), `v0.59.0` (`018d18e0...`, `c9f9dbd7...`),
`v0.60.0` (`91df6ef6...`, `cdedb4b0...`), `v0.61.0` (`3ac028bf...`,
`2a07d42d...`), and `v0.62.0` (`1402021b...`, `13a6343a...`). The older
`46e64b4b...`, `2e4d78b6...`, and `20a75616...` values are origin annotated
tag-object OIDs, not peeled revision identities. U1 snapshots each repository's
raw base and optional peeled rows and freezes exact full OIDs; local tags alone
are not authority.

Every revision preserves immutable base-ref and optional peeled observations,
annotated tag-object OID where applicable, peeled commit, tree, canonical source
archive SHA-256, source URI, capture time, and raw-observation digest. The
NIU1EF origin-absent names are frozen as `retained_historical_public_ref`; the
first NIU1EF `v1.1.0` revision is `superseded_historical_ref`. All retained
names resolve from `https://github.com/groblegark/beads.git` to the locked
commits, have no GitHub release record, and use source producers only. Both
NIU1EF `v1.1.0` revisions are mandatory with exactly the producer eligibility
shown in the table.

The snapshot locks each decisive GitHub workflow observation as a normalized
immutable `WorkflowRunEvidence` input with repository, run ID, `event`, full
`ref`, head SHA, run attempt, status/conclusion, created/started/updated
timestamps, source URI, raw response bytes, and SHA-256 of those raw bytes. Its
ordered publication-job records contain job ID/name, status/conclusion,
started/completed timestamps, source URI, raw bytes/digest, and an explicit
disposition (`ran`, `failed`, or `skipped_by_gate`). Every `v1.1.0` producer
references the relevant run and job evidence IDs: first-revision producers bind
the failed run and skipped-publication disposition; replacement official-asset
producers bind the successful run and publishing jobs. The offline generator
joins only through those snapshot references and rejects absent, cross-revision,
or incomplete associations; it contains no hardcoded run/tag/SHA/asset mapping.

Remote checking preserves each base and optional `^{}` target and rejects
duplicate, missing-base, multiply peeled, malformed, or kind-inconsistent
observations. Against the prior reviewed current-origin snapshot it reports
every addition, deletion, and move by tag name. A changed ref kind or raw target
OID is a move even when the commit is unchanged; a new peeled commit appends a
mandatory revision and never replaces the old one. Retained or superseded
historical absence is an expected historical state, not a current-origin delta.
Reviewed deltas are appended to the delta ledger and incorporated into a new
reviewed snapshot/lock; deletions update current observation without shrinking
historical scope. `--check-remote` is read-only and emits both the complete
delta set and its reviewed/unreviewed partition. U8 passes only when the
unreviewed-delta set is empty and freezes exact current-origin, release/asset,
and external-channel raw envelopes with internally verified digests.
Immediately before U9 aggregation acceptance and again at U10 eligibility, a
fresh read-only envelope is internally digest-validated, normalized, and
compared with U8's modeled state. Raw timestamps/serialization may differ. Any tag
addition/deletion/move, release replacement/deletion, asset mutation,
visibility/publication/channel change, raw-digest mismatch, unavailable observation, or indeterminate result
fails closed. A post-freeze delta is never reviewed into or used to rewrite the
old epoch; it returns to source/lock review and a new U8.

Release reconciliation independently reports additions, deletions, and moves
over stable release/asset IDs, names, sizes, digests, visibility, timestamps,
and publication metadata into the same reviewed/unreviewed model. Distinct
fixture tests require distinct stable diagnostics for `tag_ref_missing`,
`tag_ref_moved`, `tag_ref_extra`, `release_deleted`, `asset_name_changed`,
`asset_size_changed`, `asset_digest_changed`, `draft_public_transition`, and
`competing_nondraft_release`; each remains review-blocking while unreviewed.

### 2. Compact generated catalog

U1 owns a reviewed immutable snapshot, pure deterministic generator, compact
normalized locks, capability and family profiles, historical fallback recipes,
provider and lifecycle matrices, a semantic-surface catalog, channel relations,
performance budgets, and mutation tests. The normalized model includes
`RefSource`, `RefObservation`, `GitObjectEvidence`, `ReleaseRecord`, `Asset`,
`TagName`, `TagRevision`, `CapabilityProfile`, `FamilyProof`, `Producer`,
`Recipe`, `SemanticSurface`, `CoverageRule`, `ProbeTemplate`,
`WorkflowRunEvidence`, `ReviewedRemoteDelta`, `ProviderTuple`,
`LifecycleCapability`, and `InstallChannel`. Remote queries occur only in an
explicit pinned producer/refresh step; ordinary generation and validation are
offline and never execute a historical binary, provider, package manager, or
network request.

Every discovered repository is a versioned, content-derived `RefSource`, not an
unlabelled URL. Its record binds immutable repository identity, class
(`authoritative`, `maintainer_archive`, or `unverified_fork`), authority
rationale, discovery query/method, capture, raw evidence digest/locator, and an
authenticity proof such as repository ID/ownership lineage plus applicable
signed-tag, release, or workflow evidence. Every observation references that
record. Unverified-fork extras execute conservatively but cannot prove an
authentic canonical release. U8 refreshes and diffs authoritative/archive
records and all classification evidence; a missing source or authority-class
change is an explicit reviewed delta.

Independently for every exact revision, U1 derives a semantic-surface catalog
from schema migrations, public types/storage interfaces, and CLI create/read/
mutate/export surfaces. Each field, relation, behavior, counter, or durable
state is classified `preserve`, `intentionally_recompute`, `reviewed_retire`, or
`historically_absent` with exact source evidence. The catalog explicitly covers
events, wisps and relations, custom status/type, configuration, counters/
snapshots/compaction, routes/federation/interactions, tombstones/deletion, and
all ordinary issue/label/comment/dependency/readiness semantics. Cache or
ephemera may be excluded only by positive source evidence that it is
non-durable. U2 executes the complete catalog and negative historical probes;
R6 and U9 consume the same revision-keyed oracle, and U8 regenerates and freezes
it rather than accepting a hand-written checklist.

The initial snapshot and role classifications equal NIU1EF. Draft assets,
checksums, and SBOMs always have `official_producer_eligible=false`.

Every source recipe pins exact source commit/tree and raw `go.mod`/`go.sum`
hashes; target and actual build flavor; canonical argv, sorted environment,
tags, ldflags, and output path; Go distribution URL/version/SHA-256; immutable
OCI executor manifest or VM image plus software manifest; CGO compiler and
sysroot digests when applicable; and every referenced release, workflow, and
helper blob. Official archives use structured
`not_applicable: prebuilt-official-bytes` tool fields. Every producer has an
actionable `resolution_state`: `resolved` requires every applicable pin and an
empty `missing_fields`; `unresolved` requires a nonempty, raw-UTF-8-byte-sorted
unique `missing_fields` list plus an actionable `reason`, while the producer
remains mandatory. Structured prebuilt `not_applicable` fields are compatible
with `resolved` and never mean unresolved. `unknown`, `latest`, mutable runners,
or values merely derived from mutable metadata are invalid. U1 fails on any
unresolved producer, and U2/U5/U6/U8 refuse to select, execute, or freeze it.
Raw blob hashing consumes `git cat-file blob` bytes,
including the trailing `0a` in v0.49.6 `go.mod` whose SHA-256 is
`b2e1423260efce3d56c0e6da74763150f1823a37763a8964fa9d3c9d3c930fd1`.

Offline determinism is executable. The generator consumes one explicit
content-addressed input root containing raw ref/tag/commit/object/signature/
ancestry evidence and source-archive/tree identities; it never reads ambient
`.git` state. Two runs execute outside a repository with `GIT_DIR` and
`GIT_WORK_TREE` unset, `LC_ALL=C`, `TZ=UTC`, `umask 022`, a pinned absolute
`PATH`/tool manifest, OS-level network denial, separate output roots, distinct
empty `HOME`, temp and Go caches, and `GOTOOLCHAIN=local`, `GOPROXY=off`,
`GOSUMDB=off`, and `GOENV=off`. Their output trees and checked locks compare
byte-for-byte. Acquisition/build remains a separate pinned producer step. Any
ambient object/cache/module read, network attempt, or byte difference fails.

Family belongs to a concrete tag-revision variant, not globally to a tag. The
generator extracts a canonical capability vector for default backend,
init/mode guards, persisted metadata, constructor/open route, physical root,
server requirement, build/CGO/release configuration, and
redirected/shared/remote/proxied entry points. Every witness records Git blob
OID and raw-byte SHA-256. Exactly one family profile must match each variant;
zero is unclassified and multiple is ambiguous.

Transition proof uses the actual graph:

- `3db9d8de2bf086f36b922122b52d983246c66247`, parent
  `a081806f7c41673014909dd97485eef4e5c6ee73`, changes `cmd/bd/init.go` from
  blob `f6456951f11ad7dc0dfab3b494dfa4709e243c7d` to
  `1837f5f88cac2c7743f520371c9eaf5ea97e1137` and precedes v0.50.0.
- The server surface through v0.62.0 uses store-factory blob
  `070d5efe0e9f3c1a4a3266dc6c509016832fc1f9`. The embedded-default change is
  `a35edfd28e2fc324967b2e456aa1889634dae4c3`, parent
  `3ae2ef3a5a8fa067ff06d683cec1e903c58d0e8a`, changing that blob to
  `6d3dd163234dc1bfddf5b55c8f4ae49f6fc99cd3`; it is an ancestor of v0.63.0
  tag commit `d1f4784d3b3ed5134bc4e3d35b85a86310447f77`.
- Ordinary v0.58.8 and `v0.58.8-nosqlite` have different init blobs,
  `0a6a1e6db0097122bd311e6639d7c6d955afda34` and
  `c3c02a94eacceb92344398f31142556f5e6c488c`. v0.50.0/v0.58.8 and
  v0.58.8/v0.59.0 are incomparable; both pairs have merge base
  `6e82d1e2eea121ce5dc0964d554879f8b0c08563`.

Transition rows carry change commit, exact parent, before/after fingerprint
digests, changed witness blobs, and proved ancestry. They never carry
`predecessor_tag`. Incomparable branches require exact fingerprints; semver is
diagnostic only.

Coverage uses finite explicit named sets and profiles, never semver ranges,
regex guesses, or implicit complements. Canonical expansion partitions every
tag-revision/producer/platform/flavor/topology/`engine_runtime_id` identity
exactly once. Resolved-probe and historical-variant identities retain that same
runtime. Proposed-inapplicable identities expand to complete canonical create/
open probe leaves.
For a unique `probe_id`, let `core` be
`RFC8785(spec_without_probe_digest)`. Its per-leaf digest remains:

```text
SHA256("beads-u1-resolved-probe-v1\0" || core)
```

Reject duplicate `probe_id` values. Set `leaf` to the RFC 8785 bytes of the
complete spec including its verified `probe_digest`; sort leaves by the raw
UTF-8 bytes of their unique `probe_id`. Prefix each leaf with its byte length as
an unsigned 64-bit big-endian integer, concatenate those length-prefixed leaves,
and commit:

```text
SHA256("beads-u1-resolved-probe-set-v1\0" ||
       concatenated_length_prefixed_leaves)
```

The checked-in lock stores leaf count and that set digest. A temporary
`--emit-resolved-probes` stream supports review and sharding; repeated leaves
are not checked in. U2 re-expands the stream, verifies count/set digest, and
binds evidence to `probe_id`, `probe_digest`, and historical binary SHA-256.
Missing or mismatched evidence keeps the identity mandatory.

The provider lock is independently discovered, count-free, and bijective. Its
one canonical ordered tuple has exactly nine fields: `(provider_id,
access_path, store_scope, lifecycle_owner, endpoint_kind, proxy_upstream,
build_variant, platform_id, engine_runtime_id)`, and its versioned JCS identity
hashes all nine. `platform_id` alone binds GOOS/GOARCH. Independently,
`engine_runtime_id` binds distribution, exact server/binary version or image
digest, protocol, canonical configuration, and the semantic envelope including
collation/charset/time-zone behavior and exact source-proved supported-version
minimum/boundaries. Embedded/in-process rows use literal
`embedded/no-external-runtime`. Runtime boundary/equivalence cells come from
exact source evidence, never semver or platform. Rows differing only by runtime
remain distinct and deletion of either fails. The lock retains selector witnesses, uses reviewed platform
inapplicability rather than omission, and admits only satisfiable combinations
after alias/default normalization. It covers Dolt embedded, owned-workspace/
shared/direct-external server and managed-child/external proxy modes, plus every
discovered SQLite/PostgreSQL/MySQL topology in both builds. CGO changes only
embedded applicability absent new evidence.
Every tuple has exactly these four operation IDs:
`init_workspace`, `construct_store_generic`,
`open_store_read_only_factory`, and `open_configured_cli_uow`. Every operation
has its own exact `source_route_id` binding file, symbol, build constraint, blob
OID, and raw-byte digest; tuple-level or cross-operation route claims fail.
Non-CGO embedded Dolt rejects with `requires-cgo`; direct server Dolt and SQL
providers remain applicable in both builds. Proxied
`init_workspace` hits the early not-implemented guard in both builds;
`construct_store_generic` and `open_store_read_only_factory` reject with
`uow-provider-required`; only `open_configured_cli_uow` succeeds through the
dedicated UOW route. CLI short-circuit success is never evidence that the
read-only store factory supports proxied open. Every outcome is authentically
executed or has pinned inapplicability.

A separate lifecycle lock is a bijection over planner route, topology,
`engine_runtime_id`, build, and platform for `inspect`, `quiesce`, `snapshot`,
`prepare`, `verify`, `activate`, `final_verify`, `resume`, and `restore`. Every
cell has executable source/interface ownership evidence and an authentic
success or typed missing capability. A four-operation provider row cannot stand
in for lifecycle proof. Missing mandatory cells become exactly owned U4 or
driver blockers and directly block U8.

Install-channel discovery classifies every repository, documentation,
automation, package-manager, and live-catalog signal as
`candidate_executing|alias|non_cli|dormant_unpublished|retired`. A case ID is a
versioned JCS digest of `(surface_id, branch_id, platform, arch, manager_mode,
materialization_id)`. Branch traces cover shell archive/CGO-go/non-CGO-go/source
clone, PowerShell ZIP/Go/source clone and overrides, npm/bun resolver and CI
skip, Homebrew bottle/source fallback, mise release backend, direct Go, Nix,
AUR, and every newly found branch. Winget's authoring-time dormant/stale state,
PyPI's non-CLI state, and AUR `beads-git` mutability are reviewed observations,
not silent omissions; U1/U8/U9/U10 detect drift. Reachable branches execute
authentically through frozen injectable inputs or carry pinned inapplicability,
but U9 never claims a live public-latest selector chose an unpublished U8
candidate. After publication the U7-owned release molecule/workflow executes
the activation DAG and every locked `public_latest_only` selector authentically.
Each row pins activation mechanism/owner/prerequisite receipt, non-secret
credential reference/class, bounded propagation window, budgeted retry/backoff,
expected selector, and rollback/quarantine/escalation; missing data is reviewed
blocked/inapplicable, never skipped. Prior reviewed inapplicability removes the
runtime obligation; a reviewed blocked row blocks publication before selector
execution and never becomes a runtime skip. Its distinct receipt
cannot satisfy or repair U9.

Candidate execution is a discriminated union:

- `packaged_bytes` executes the selected manifest artifact/archive branch and verifies
  `candidate_artifact_digest`;
- `wrapper_contains_packaged_bytes` verifies wrapper tarball, resolver, and
  recovered payload, and executes bytes matching that
  `candidate_artifact_digest`; or
- `frozen_source_derivation` binds frozen source and recipe plus fully pinned
  executor/toolchain/compiler/sysroot inputs as applicable and the expected
  embedded-version inputs without claiming GoReleaser byte equality. U8 verifies
  only this realizable contract. U9 emits a canonical immutable inventory
  bijective with derivation identity, with exactly one realized-output SHA-256
  and exact `v1.2.0` version per identity; consumers reference it, aggregation
  rejects disagreement/rebuild, and U10 validates its digest. Only packaged
  manifest entries have `candidate_artifact_digest`.

### 3. Production boundaries and behavior

`internal/storage` and `dolthub/driver` own bounded read-only probes, provider
open, quiescence, snapshots, transactions, prepare/verify/activate, cutover,
rollback, restore, and crash guarantees. The migration core owns normalized
observations, a small route graph, deterministic plan rendering, and exactly
`NoOp`, `StartupSafe`, `ManualRequired`, or `Unsupported`. It contains no engine
inspection, flock, generic retry loop, storage-specific recovery, or driver
internals. Startup and `bd migrate` are adapters over this one planner.

Inspection returns an opaque witness plus the expected route. Every provider
open, including `NoOp` and read-only open, must pass both to one adapter/driver-
owned atomic validate-and-open primitive. That primitive validates raw
configuration, route, topology, source identity, and capability digests before
any effect; drift returns typed `replan_required`/`open_refused` with zero open,
provision, version, telemetry, or metadata effects. A missing primitive is a U4
or proved upstream blocker, never a best-effort Beads race check.

Inspection precedes store open, engine launch, provisioning, version tracking,
telemetry, or metadata mutation. Startup auto-applies only fixed, same-local-
provider/topology, driver-declared atomic or rollback-safe work with no bulk,
content-dependent, remote, destructive, snapshot, or cutover operation. Every
mutating path acquires driver-owned quiescence, re-probes, recomputes the full
plan, and byte-matches source/capabilities/configuration/topology/plan before its
first write. Manual execution preserves the source, prepares and verifies side
by side, activates authority/configuration/topology last, and then only reads to
verify. Its durable, authenticated, discoverable operation envelope binds
operation/source/target/plan digests, authority epoch/fencing token, CAS
revision/transitions, and current lifecycle boundary while engine-specific
state remains opaque. Every transition presents the current fencing token;
stale or foreign actors fail before effect. Crash tests restart at every
persisted boundary and prove idempotent resume/restore and exactly one
authoritative reopenable store. Completed rerun is `NoOp`; missing discovery,
authentication, fencing, or CAS support is a capability blocker, not Beads-side
storage-specific recovery.

Historical semver and requested destination are diagnostic/intent inputs only.
Unknown or conflicting evidence fails closed. Provider-local schema upgrades
remain separate from provider conversion; embedded Dolt to PostgreSQL is the
only admitted backend-changing route. Missing driver capabilities fail the
affected mandatory route. Executable interface/adapter evidence first assigns
ownership: Beads-owned `internal/storage` adapter/topology gaps (including
applicable SQLite, PostgreSQL, MySQL, server-Dolt, and proxy behavior) create
dynamic U4 children in this epic, while only a primitive proved to belong to
`dolthub/driver` creates a linked upstream issue/PR. Both child kinds directly
block U8. Beads does not patch around a driver-owned boundary.

U4 reruns repository preflight and reviews maphew's open #4878 migration/schema
PR before any overlapping implementation. It prefers adoption/fixup, preserves
tests and attribution, records disposition, and never silently replaces,
closes, or supersedes contributor work.

### 4. Qualification and exact candidate

Historical binaries create and mutate authentic nonempty workspaces. Producer
fan-in materializes each exact peeled-commit/platform/flavor/recipe/toolchain
identity once; workers use only internal digest-addressed bytes and never fetch
or compile. Each mandatory smoke identity is:

```text
tag_revision_id + producer_id + topology_id + platform_id + build_flavor_id
  + engine_runtime_id
```

Every smoke row emits one direct `PASS` or `FAIL`; smoke is never shared.
Focused cases use a separate exact namespace and may share only with proved
equality of physical format/schema, route/plan, provider/topology, and semantic
duties. `covered_identities` carries the same runtime-bearing identity and is
traceability only. `SKIP`, warnings as success,
timeout, missing producer, unsupported host, missing/extra/duplicate result,
unknown reference, invalid revision, or invalid candidate execution is failure.
Aggregation independently requires smoke `N/N` and focused `M/M`; sharding and
aggregation keep rows differing only by `engine_runtime_id` distinct, and
deleting either fails.

Each result carries source revision/binary-or-build provenance and
`engine_runtime_id` (including `embedded/no-external-runtime`), candidate
commit/tree and `candidate_set_digest`, the exact `candidate_execution`, frozen
helper tree, executor/environment, external Dolt identity or gated `N/A`, and
every download origin/digest. Packaged digest guarantees apply exactly where
packaged bytes execute; source-derived output uses its realized digest and
verified embedded version.

The fidelity oracle is generated from the complete per-revision semantic lock,
not a manually selected field list. U2 proves a bijection between catalog cells
and executed preserve/recompute/retire/absent evidence, including negative
historical-binary probes for absence/exclusion. Upgrade qualification then
executes create, field mutation, dependency add/remove and readiness transition,
close/reopen, semantic export, store reopen, completed `NoOp`, and restored-
source reopen. Silence, grouped shorthand, or cache exclusion without positive
evidence fails.

U6 executes both the provider-operation matrix and the independent full
lifecycle matrix. It also enforces U1's measured performance lock: NoOp/open
incremental p95 is at most `max(25 ms, 15%)` and p99 at most
`max(75 ms, 25%)` over at least 30 samples; PR/nightly/U9/row/producer ceilings
are 45 minutes, 8 hours, 12 hours, 20 minutes, and 30 minutes; concurrency is at
most 24 shards with at most 32 smoke plus 16 focused cases per shard; upload/
cache ceilings are 250 MiB per shard, 20 GiB per run, and 50 GiB; a full run
uses at most 1,500 GitHub API calls and each producer at most three acquisition
attempts. Receipts bind runner image/hardware plus CPU/filesystem calibration
and raw/normalized measurements. Only a reviewed expiring lock delta may change
a budget; runtime waivers cannot turn a regression into pass.

U5 implements the one-build pipeline using disposable candidates. U7 is the
last source-changing version owner: under TDD/review it makes
`scripts/check-versions.sh` cover every `scripts/update-versions.sh` and other
tracked release-version surface, including Copilot, `default.nix`, Windows JSON/
XML values, README policy, and the MCP lock. Immediately before U8 it derives
and reviews `version_date`, the intended UTC publication date in strict
`YYYY-MM-DD` form, then finalizes a top
`## [1.2.0] - <version_date>` section in `CHANGELOG.md` and newest nonempty
`VersionChange{Version: "1.2.0", Date: "<version_date>", ...}` in
`cmd/bd/info.go`, tests text/JSON
`bd info --whats-new`, runs `./scripts/update-versions.sh 1.2.0` with exact-
substitution guards, refreshes the pinned MCP lock, and proves all source,
disposable packaged/wrapper, and source-derived probes report exact `v1.2.0`.
Deprecated `scripts/bump-version.sh` and the source-changing release-prep
molecule are not post-U8 paths. U8 freezes `version_date`; malformed/mismatched
values or publication after that UTC date require a new reviewed epoch.

U7 also owns and rewrites the real publication entrypoint:
`.beads/formulas/beads-release.formula.toml`, `scripts/release.sh`,
`.github/workflows/release.yml`, and their structural tests. U10's evaluator is
strictly read-only and first emits one canonical `EligibilityDecision` binding
inputs, target/freeze/baselines, counts, outcome/reasons, evaluator version, and
`decision_digest`; it contains no bundle, locator, expiry, claim, seal, or
publication field. Only after an eligible decision is final may U10 invoke the
already-shipped U7 bundler as a separate append-only output operation. Bundle
bytes bind `decision_digest`, U8/U9 inputs, target/baselines, `version_date`, and
channel lock, but never their own digest or receipt. A distinct
`BundleSealReceipt` records decision, bundle and artifact digests, authenticated
locator, signing identity, seal/24-hour claim expiry, and retention. Evaluation
changes nothing; sealing may create only bundle/seal outputs. U8's inventory
already contains every publishable Go archive, checksum, corpus, SBOM, npm
tarball, and MCP wheel/sdist.

The concrete backend is `actions/upload-artifact@v4` with
`retention-days: 90`, authenticated run/artifact IDs, API-reported SHA-256, and
an expiry reread proving 90 days from seal. `actions/attest@v4` signs a custom
in-toto/DSSE predicate under GitHub OIDC and public-good Fulcio/Rekor roots. A
pinned upgraded `gh attestation verify` pins repository, signer workflow/ref,
frozen source commit, subject digest, hosted-runner identity, and reviewed
trust-root version. These controls make substitution detectable, not WORM;
control-plane actors can delete artifacts and the public-repository ceiling is
90 days. At expiry, identical bundle and signed receipt-chain bytes are retained
as stable release assets through `max(manual landing + 90 days, every rollback
deadline)`. Literal object-lock availability, if required, is a separately
provisioned external-storage prerequisite, not a Beads service.

The bundle writer uses only the workflow token's read/actions plus OIDC,
attestation, and artifact-metadata write permissions. Publication uses one
dedicated environment-gated release-App token: `contents:write` for protected
claim/release tags and assets, `actions:write` only for accepted run dispatch/
details, and `administration:write` only for temporary exact-ref ruleset create/
read/delete. The attestation job retains its separate least-privilege
`GITHUB_TOKEN`/OIDC identity. All credentials are non-secret references resolved
only inside the approved `beads-release` environment.

U8 waits for
U0-U7 and every route child, refreshes current-origin/release observations
and every external channel read-only, reports every delta, requires the
unreviewed-delta set to be empty,
and validates the latest reviewed lock-derived scope and evidence totals after
all reviewed append-only deltas. It freezes the exact remote target HEAD plus
snapshot/generator/profiles/recipes/derivation, the reconciled current-origin,
release/asset, and external-channel raw envelopes and internally verified
digests, and every other input, then builds
the complete publishable packaged inventory and exact manifest-bound candidate-
execution set once.
U8 does not require an unrealized source output identity. U9 changes no frozen
byte, materializes each source derivation once into the canonical inventory,
verifies its realized SHA-256 and exact `v1.2.0` embedded version, and executes
all rows against that epoch. Immediately before accepting aggregation it
captures fresh origin/release/asset/external-channel raw evidence, validates its
own digest, and compares canonical modeled state with U8. U10 independently
repeats that capture and modeled comparison while validating the inventory
digest and consuming manifest/results read-only. Different timestamps or JSON
serialization may produce different raw envelopes/digests without modeled
drift; an internal raw-digest mismatch or modeled-field drift fails.

Any frozen input, version byte, target-ref, packaged artifact,
derivation-contract, or post-freeze remote/release/asset/channel delta
invalidates the epoch. Neither U9 nor U10
rewrites frozen scope in place; the path is reviewed source work and a new U8.
Qualification creates no stable tag or publication; technical eligibility
remains input to a separate named accountable-human release decision. Before
U8, a production-capability preflight must prove the artifact, attestation,
trust, permissions, retention, release-environment, claim, tag-protection,
ruleset, and dispatch paths. Current observed GitHub state fails that gate: the
target has no protection/effective rules; existing default-branch ruleset
`15646382` and `refs/tags/v*` ruleset `15538968` carry administrator/team
bypasses; no release environment or release
credentials exist; `release.yml` still has tag triggers, rebuilding, `--clobber`,
and seven-day artifacts; installed attestations coexist with a GH CLI too old
for verification; and no GHCR package exists. U7/U8 may not describe any of
those capabilities as already installed or use GHCR as an assumed backend.

After human/environment approval, the formula reruns eligibility and live
reconciliation and seals a guard. Canonical `ClaimIntent` bytes bind version,
decision/bundle/guard, run ID, triggering actor, authority, expiry, and nonce
while excluding attestation locators/digests and envelope fields. The workflow
hashes and attests that intent; a distinct `ClaimEnvelope` and annotated tag
message then bind `claim_intent_digest` plus the resulting attestation locator/
digest before conditional creation of
`refs/tags/beads-release-claims/v1.2.0`. HTTP 201 wins. HTTP 409/422 and recovery
at the intent, attestation, tag-object, and ref boundaries separately verify the
envelope, attestation subject digest, and every intent field before permitting
only the same run/actor/authority/bundle/nonce to resume. Foreign mismatch
rejects. The claim ref is never moved, deleted, or reused.

The temporary exact-source-ref ruleset is an ordinary-writer freeze with no
operational bypass, not an atomic lease, transaction, or protection from
repository/organization administrators. The dedicated release App creates it,
polls its effective update/deletion rules, records a signed observation, and
rereads the source ref at the frozen OID. Before every later mutation it rechecks
ruleset configuration/effectiveness and ref equality; drift or indeterminacy
stops. GitHub control-plane administrators are the explicit trust boundary.
Once created, the protected annotated stable tag at the frozen OID is release
authority. A same-claim/same-guard existing tag, release, asset, or channel step
is digest-verified and resumed; a foreign collision rejects.

After protected annotated tag creation, dispatch uses `ref: v1.2.0` and
`return_run_details:true`. Before the source ruleset is deleted, the returned
run must verify `event=workflow_dispatch`, `head_sha` equal to the frozen OID,
and the exact frozen workflow identity and blob OID/digest. Those facts, the
explicit tag ref, and HTTP 200 run ID/URLs are bound into a signed
`DispatchAcceptanceReceipt`; only then is the rule deleted by exact ID with an
audited absence reread. The receipt proves acceptance but no atomicity with the
ruleset, ref, or tag. The existing formula checkpoint/resume mechanism
persists a signed hash-chain receipt for every step, rediscovers the same claim/
guard after a crash, rejects stale actors, and resumes idempotently. It never
commits or pushes the source branch. `release.yml` has no unparameterized tag
trigger and only hash-verifies/promotes U8 inventory; post-U8 rebuild, repack,
regeneration, replacement, or promotion is forbidden.

The claimed authority executes the lock-derived activation DAG and then every
applicable authentic public selector. Each signed receipt binds prerequisites,
credential reference/class, claim/guard/candidate/tag/release, selector,
observed version/digest, and outcome. Terminal failure compensates or
quarantines every activated channel in reverse activation-DAG order and resumes
that compensation idempotently; partial recovery escalates and leaves release/
root incomplete. It never moves/deletes/reuses the protected stable tag or
rebuilds 1.2.0, and the receipt cannot repair U9. The exact `owned` root never
auto-closes; the accountable human lands/closes only after all 11 macro children,
eligibility plus sealing, publication, and every applicable receipt pass. A
no-publish decision is signed cancel/supersede, not success.

### 5. Delivery controls and non-designs

Preserve the freshly fetched target and the accepted #4801/#4810/#4845 work as
literal ancestry, contributor tests and attribution, the storage boundary,
exact candidate freeze, and no-skip policy.

Live reconciliation uses two projections. The active materializer owns exactly
11 runnable mappings and 22 static edges. Pre-slice recursive live discovery is
162:
those 11 plus 151 legacy records partitioned as 11 preserved, five adopted, one
retained, ten deferred, and 124 superseded; the preserved set includes closed
`bd-4velg` under `.1` from previous plan SHA `b92f3957...`. The mapped root is separately repaired
because live `bd-ldt0f` is type `epic` with no `tracks`, while `gc convoy add`
requires custom type `convoy` and the materializer updates only root metadata.
The live root has 29 immediate parent-child members and zero tracks. Before any
payload, root, legacy, or membership reconciliation, separate city-HQ task
`mc-ucid`, owned by the Gas City/Beads maintainer, must close. It is created and
managed only in city-HQ context (`gc bd --city /data/projects/maintainer-city
...` without `--rig beads`), remains outside `bd-ldt0f`, is never parented or
tracked by the convoy, contributes no static DAG edge, and is not a twelfth
member. It schema-validates and atomically repairs
`/data/projects/beads/.beads/metadata.json`: only `.dolt_database` changes from
`bd_metrics_repo_2424946071` to `bd`, and only `.project_id` changes from
`8d69d5b6-0917-47a6-9761-db2b0dcca2fc` to
`bafe313f-9fce-4972-849d-1f825740e9a5`. It preserves
`.database == "dolt"`, mode/backend/host/port, and every unrelated field, then
schema-validates and exactly rereads that projection before running
`gc rig --city /data/projects/maintainer-city set-endpoint beads --inherit`.
The accepted route is inherited `city_canonical`, never external: city
`/data/projects/maintainer-city`, rig `beads`, rig path `/data/projects/beads`,
endpoint `127.0.0.1:3307`, physical database
`/data/services/gascity-local-dolt/bd`, and remote
`git+https://github.com/gastownhall/beads.git`. After task closure, explicit-rig
read-only context, root lookup, and recursive-subtree proof must agree with that
tuple. Environment overrides and raw SQL are diagnostics only, never an
identity or reconciliation bypass. A row revision CAS then sets exact row-owned tasks.md fields
and per-key metadata diffs while preserving open status, assignee, notes,
external-ref, owner, and audit/storage fields. Current `bd` cannot CAS labels or
parent/dependency relations, so an exclusive reviewed window applies their
separate exact reconciliations and fail-closed rereads. The exact root label set
includes `owned`, with tests proving auto-close is skipped and landing remains
manual. Every non-payload
immediate legacy child is detached/reparented (`.1.9` remains top-level), then
the immediate-child, tracks, and effective convoy member sets each equal the 11
mapped IDs including `.14`; `.16` is never inferred or mutated.

Before source work, U1, U2, U4, U5, U6, U7, and any similarly broad child pour
one bounded task-local molecule of ordered vertical slices. Each slice owns one
end-to-end outcome and explicit files/dependencies/acceptance/verification,
starts RED, lands minimal GREEN, refactors under green tests, and receives
independent review. Its descendants block only macro closure; they are never
root children/tracks and cannot change the 11 macro members or 22 macro edges.
The 162-record assertion above is therefore explicitly the pre-slice baseline.
Slice creation is one bounded setup pass, not a recursive meta-planning loop.

A deterministic bidirectional trace checker parses every numbered R1-R8 clause
and every task `requirements_trace`, expands ranges, and emits a canonical
matrix. Unknown references, requirements with no task, tasks with no valid
reverse trace, or U7 lacking any of R8.6, R8.7, R8.8, R8.9, and R8.10 fail
before source work and again at U8.

Before every source commit, exactly three independent Sol/Ultra reviewers must
approve the identical staged-diff digest and record the `git write-tree` index
tree with no unresolved Critical or Important finding. The exact staged patch
bytes plus resolved active hook path/digest are retained; checkout-local hook
execution is never assumed. After commit and before
push, the commit tree must equal that approved tree and the first-parent
`git diff --binary --full-index HEAD^1 HEAD`
SHA-256 must equal the approved
staged-diff SHA-256. Hook-induced mismatch blocks propagation; only an unpushed
replacement/amend followed by three fresh reviews and both repeated proofs may
proceed. Every PR targets
`feature/backend-provider-change-20260713`, is created with
`status/needs-review-auto`, captures its returned identity, and verifies the
fully paginated exact-PR `LabeledEvent` without re-adding a consumed label.
Nontrivial PRs require substantive accountable-human review; protected
migration/schema/sync paths never merge on bot-only approval.

This plan-correction author pass is not one of the three final staged-diff
approval seats; those seats review the later identical staged bytes.

Add no checked-in review/bootstrap controller, approval database, credential
broker, commit/push wrapper, PR/release orchestrator, storage engine, Beads-side
flock/introspection/retry/recovery layer, arbitrary provider conversion, or
unrelated feature surface.

AUTHOR_CONTRACT_READY_FOR_INDEPENDENT_REVIEW

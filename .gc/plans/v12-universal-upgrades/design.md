---
plan_slug: v12-universal-upgrades
phase: design
rig: beads
rig_root: /data/projects/beads-v12-universal
artifact_root: /data/projects/beads-v12-universal/.gc/plans
requirements_file: /data/projects/beads-v12-universal/.gc/plans/v12-universal-upgrades/requirements.md
status: approved
created_at: 2026-07-18T00:00:00Z
updated_at: 2026-07-22T00:00:00Z
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

The old instruction to start from
`af136f8857dd3e0461e06597f37e925088a98a49` is historical, not executable.
Current fetched evidence is:

- `origin/feature/backend-provider-change-20260713` is
  `f7d0c26ec8c1e7b6b075cc49b07cb2f0f41c3a47`, with sole parent
  `af136f8857dd3e0461e06597f37e925088a98a49` and tree
  `8b0b9d93aa0d688fef9e8a0c490011be3221141e`;
- that tree exactly equals merged PR #4801's final head
  `e89ab9aa09bb178e2cfe1dec838e0e601f9663db`, so the target already contains
  #4801's accepted squash/adoption bytes and attribution;
- `a0a51638c036d25923d8671949e27a2bc12ba310` belongs to PR #4802, not
  #4801, and its tree equals accepted chain commit
  `2ef7c61e0f434bf34c66d9581104d102e31c1eb1`; it is evidence of an
  equal-tree representation, not another merge input;
- PR #4907 remains open at
  `0a0be15db29250f0ebb46793e7bcfc3b1905e245`, with parents `af136f885...` and
  `dce8d066d...` and tree
  `dc754da6232a69f4d585d0d3551d696e478b44c1`, and GitHub reports
  `DIRTY`/conflicting against the current target; and
- merged PR #4810's adoption commit
  `1b5f02efddac224d933526b2025481f2b952f34f` has the same tree as its final
  head (`696c98555545bb7df7377d5b6d9c1cd5e34c99f4`) but is not ancestry of the #4845 chain tip
  `dce8d066de983b4fa4487890f48157a7264d86d2`. The latter has the same tree as
  PR #4845's final head (`61e0f715d5841c7b95b4d85b36c49c8d3dedc6f7`).

Repair #4907 in place with the smallest ancestry-preserving extension. Fetch
the target and PR refs, bind `target_oid` to the fetched current target head,
then start from `target_oid` and run a no-fast-forward, no-commit reconciliation
merge of the existing #4907 head. Review and commit that staged resolution only
after exactly three fresh independent Sol/Ultra approvals name its identical
staged-diff digest and its `git write-tree` index tree. After the commit but
before any push, require its commit tree to equal that approved index tree and
the SHA-256 of `git diff --binary --full-index HEAD^1 HEAD` to equal the
approved staged-diff SHA-256. Assert its ordered parents are `[target_oid,
0a0be15db...]`. From that reconciliation merge OID, run a second
no-fast-forward, no-commit adoption merge of `1b5f02e...`; review and commit
that separately under the same three-fresh-approval rule and assert its ordered
parents are `[reconciliation_merge_oid, 1b5f02e...]`. This keeps the update
fast-forward from the existing PR head and retains the accepted chain through
`dce8d066d...`. Do not cherry-pick, squash, drop, or recreate those contributor
histories. PR #4907 is the integration vehicle and is not included in
`contributor_intake_prs`.

Before changing #4907, capture its canonical number `4907`, URL
`https://github.com/gastownhall/beads/pull/4907`, old head OID, base ref, and
body digest. The updated PR must retain that identity, expose the corrected
body and exact current U0 head, and target the same fetched base OID. Fully
paginate its GraphQL `timelineItems` and prove the already-existing historical
`status/needs-review-auto` `LabeledEvent`; later automation consumption into
`status/reviewing` is acceptable and the trigger is never re-added.

Legacy bead `.3.27` closes on the accepted #4845/
`dce8d066de983b4fa4487890f48157a7264d86d2` representation of the public
v0.62 bridge outcome, not on a same-numbered literal commit. Its older
`dffae48d5`/`09d25b54a` commits are neither required ancestry nor an equal-tree
substitution.

The initial fail-closed evidence capture is executable as follows; a failed
identity check means inspect and review the new graph, not substitute an old
parent or continue:

```bash
git fetch origin \
  +refs/heads/feature/backend-provider-change-20260713:refs/remotes/origin/feature/backend-provider-change-20260713 \
  +refs/pull/4907/head:refs/remotes/origin/pr/4907/head \
  +refs/heads/upgrade/q1-e1-legacy-dolt-ci-gate:refs/remotes/origin/upgrade/q1-e1-legacy-dolt-ci-gate
target_oid=$(git rev-parse refs/remotes/origin/feature/backend-provider-change-20260713)
u0_head_oid=$(git rev-parse refs/remotes/origin/pr/4907/head)
pr4810_oid=$(git rev-parse refs/remotes/origin/upgrade/q1-e1-legacy-dolt-ci-gate)
test "$target_oid" = f7d0c26ec8c1e7b6b075cc49b07cb2f0f41c3a47 || exit 1
test "$u0_head_oid" = 0a0be15db29250f0ebb46793e7bcfc3b1905e245 || exit 1
test "$pr4810_oid" = 1b5f02efddac224d933526b2025481f2b952f34f || exit 1
```

The present merge bases overlap materially; no conflict-free, byte-identical,
or automatic-side-selection claim is valid. Resolve every overlap against the
three accepted PR intents and current target behavior, retain current-target
work unless a reviewed compatibility resolution requires a change, and run all
three contributor suites plus the multi-provider lifecycle/effect-census
checks. Any adaptation is a separate reviewed commit after the provenance
merges.

Fetch the target again immediately before accepting the U0 head. If its OID
differs from `target_oid`, or if any accepted PR ref or tree evidence differs
from the reviewed inputs, stop: the staged resolution and its reviews are stale.
The final PR merge additionally uses a repository-enforced up-to-date or merge-
queue guard bound to that exact base OID; base movement automatically
invalidates eligibility rather than relying on a manual check with a TOCTOU
window. Reinspect the graph, reconcile from the newly fetched head, and obtain
fresh reviews of fresh bytes. Never rewrite the target branch or freeze
`f7d0c26...` as a future command-time constant merely because it is current
evidence here.

## 3. Components

### 3.1 Historical release and provider locks

Test-only checked-in inputs:

- `scripts/migration-test/release-snapshot.json`
- `scripts/migration-test/ref-sources.lock.json`
- `scripts/migration-test/git-object-evidence.lock.json`
- `scripts/migration-test/releases.lock.json`
- `scripts/migration-test/capability-profiles.json`
- `scripts/migration-test/family-profiles.json`
- `scripts/migration-test/semantic-surfaces.lock.json`
- `scripts/migration-test/applicability-evidence.lock.json`
- `scripts/migration-test/providers.lock.json`
- `scripts/migration-test/lifecycle-capabilities.lock.json`
- `scripts/migration-test/install-channels.lock.json`
- `scripts/migration-test/focused-cases.lock.json`
- `scripts/migration-test/performance-budgets.lock.json`
- `scripts/migration-test/generate-releases-lock.py`
- `scripts/migration-test/recipes/historical/`

`release-snapshot.json` retains every remote observation exactly once. U1
discovers public tag sources rather than assuming current origin is the entire
denominator. Authoring-time live `ls-remote` observations on 2026-07-22 contain
173 names and 178 visible public `(tag_name, peeled_commit_oid)` revisions
across `origin` and `groblegark`; the superseded first `v1.1.0` revision makes
179 frozen initial revisions. Five names have two live public revisions:

| Tag | Current origin peeled commit | Groblegark peeled commit |
|---|---|---|
| `v0.58.0` | `ae14933db67a0f67da5a8fb69be72c2282ca0e73` | `c751b3228fc17aeb203b03e2a8fdab8e6d52f4ba` |
| `v0.59.0` | `018d18e013641e811483cb990243b774d04561d8` | `c9f9dbd7f146f6b4c2cff2be49330c371523d52c` |
| `v0.60.0` | `91df6ef6d343c52fe2e03061ae6fbd0c6529ada6` | `cdedb4b0797a4f9619b7681ed9fdf7bb1311eb50` |
| `v0.61.0` | `3ac028bff3088dc395b5fd5ebc6cf32353f84ba0` | `2a07d42de85732a130e3aeae363f8960c199f083` |
| `v0.62.0` | `1402021b8bf36595fabad628317d3d27b4c88aa0` | `13a6343a52362a2f21d634dbae6ef4726245f720` |

U1 independently repeats and freezes exact raw evidence; these literals are
authoring evidence, not generator constants. The pure generator normalizes it
into `TagName`, `TagRevision`, `RefObservation`,
`ReleaseRecord`, `Asset`, `CapabilityProfile`, `FamilyProof`, `Producer`,
`Recipe`, `CoverageRule`, `ProbeTemplate`, `WorkflowRunEvidence`, and
`ReviewedRemoteDelta` entities referenced by the compact lock. Counts are
derived rather than embedded as generator constants. The architecture verdict's
Normative Initial U1 Evidence Fixture (NIU1EF) is the sole authority for initial
identities and totals; the latest reviewed append-only lock is authoritative
after reviewed deltas. Current origin remains a separate observation, not the
historical denominator.

`ref-sources.lock.json` is a first-class source-of-truth catalog. Every observed
repository has a versioned content-derived `ref_source_id`, immutable GitHub
repository ID/canonical URL, class (`authoritative`, `maintainer_archive`, or
`unverified_fork`), authority rationale and evidence, discovery query/method,
capture metadata, raw-evidence locator/digest, and authenticity proof. Ref and
revision records always reference it. Unverified-fork extras execute
conservatively as compatibility rows but are prohibited from supporting the
canonical-authenticity outcome until a reviewed proof changes their class. U8
repeats discovery and diffs every authoritative/archive source and every source
classification/evidence field, not just the union of tag names.

Each revision preserves raw base-ref and optional peeled observations. Annotated
tags additionally preserve tag-object OID; lightweight tags require a commit
target. All revisions preserve commit, tree, canonical source-archive SHA-256,
source URI, capture time, and raw-observation digest. NIU1EF retained and
superseded histories preserve their exact provenance and producer eligibility.
The decisive `v1.1.0` workflow runs are normalized immutable snapshot inputs,
not generator logic: each locks run ID, event/full ref/head SHA, attempt,
conclusions, timestamps, source URI/raw digest, and ordered publication-job
IDs, conclusions, timestamps, source URIs/raw digests, and dispositions. Every
`v1.1.0` producer references its relevant run/job evidence IDs; offline joins
reject missing/cross-revision references and cannot hardcode the association.

Remote checking parses base/`^{}` pairs and rejects duplicates, missing bases,
multiple peeled rows, malformed OIDs, and ref-kind mismatches. It compares the
new observation to the prior reviewed current-origin snapshot by name and
reports every addition, deletion, and move. A changed ref kind or raw target is
a move even when the commit is unchanged; a changed peeled commit appends a
mandatory revision without replacing history. Retained/superseded historical
absence is not a delta. Release checking uses the same reviewed/unreviewed
model for stable release/asset IDs, names, sizes, SHA-256 values, visibility,
timestamps, and publication metadata. Reviewed deltas append to the ledger and
next lock; deletions never shrink historical scope. `--check-remote` is
read-only. U8 freezes its exact reconciled current-origin, release/asset, and
external-channel raw envelopes with internally verified digests and passes only
with an empty unreviewed-delta set. Immediately before U9 aggregation acceptance
and again at U10 eligibility, the checker acquires a fresh live envelope,
validates its own raw digest, normalizes it, and reconciles modeled state against
U8. Raw capture metadata/serialization may differ. An unavailable or
indeterminate check, internal raw-digest mismatch, tag addition/deletion/move,
release replacement/deletion, asset mutation, visibility/publication change,
or external-channel drift fails closed. Post-freeze deltas are never folded
into the frozen snapshot or denominator in place; they return to reviewed
source/lock work and a new U8.

Separate fixtures emit separate stable classes for missing/moved/extra tag
refs, deleted release, changed asset name, size, or digest, draft/public
transition, and competing non-draft release. The initial release/asset roles
equal NIU1EF. Draft assets, checksums, and SBOMs always have
`official_producer_eligible=false`.

Each tag revision has an explicit concrete-variant ledger keyed through
`engine_runtime_id` for standalone,
redirected/shared (`BEADS_DIR`, worktree, shared-server), remote/server,
proxied, platform, and build-flavor forms. Family is assigned per variant from a
canonical capability vector covering default backend, init/mode guards,
persisted metadata, constructor/open route, physical root, server requirement,
build/CGO/release configuration, and redirected/shared/remote/proxied entry
points. Witnesses are exact Go/YAML control-flow blobs with Git blob OID and
raw-byte SHA-256. Every variant must match exactly one capability/family profile;
zero matches is unclassified and multiple matches is ambiguous.

The initial transition identities and witness blobs are the architecture
verdict's transition proof; this design does not restate them as another
authority.

Every transition row stores its actual change commit, exact parent,
before/after fingerprint digests, changed witness blobs, and ancestry proof; it
has no `predecessor_tag`. Incomparable branches use exact fingerprints. Semver
is diagnostic metadata only.

Coverage uses explicit finite named sets and profiles, never semver ranges,
regex guesses, or implicit complements. Canonical expansion partitions every
tag-revision/producer/platform/build-flavor/topology/`engine_runtime_id`
identity once. Every resolved-probe identity carries the same runtime. A proposed-
inapplicable leaf contains its complete setup/service/readiness/timeout,
create/open argv and expected observations, failure class, and family. Each leaf
digest is `SHA256("beads-u1-resolved-probe-v1\0" ||
RFC8785(spec_without_probe_digest))`. Duplicate `probe_id` values fail. The set
sorts complete RFC 8785 leaves including the verified `probe_digest` by raw
UTF-8 bytes of unique `probe_id`, prefixes
each leaf with its byte length as an unsigned 64-bit big-endian integer, and
stores only leaf count plus `SHA256("beads-u1-resolved-probe-set-v1\0" ||
concatenated_length_prefixed_leaves)`. `--emit-resolved-probes` produces
the temporary review/sharding stream. U2 re-expands it and binds evidence to
`probe_id`, `probe_digest`, and historical binary SHA-256; the repeated expanded
leaves are not checked in. Pending, missing, stale, or mismatched evidence keeps
the identity mandatory.

Every source fallback recipe is fully resolved: source commit/tree and raw
`go.mod`/`go.sum` hashes; target/flavor; sorted environment, tags, ldflags, argv,
and output; Go distribution URL/version/SHA-256; immutable executor image or VM
plus software manifest; CGO compiler/sysroot digests when applicable; and all
referenced release/workflow/helper blobs. Official archives carry structured
`not_applicable` tool inputs with reason `prebuilt-official-bytes`. Every
producer has `resolution_state`: `resolved` has complete applicable pins and no
`missing_fields`; `unresolved` has raw-UTF-8-byte-sorted unique
`missing_fields` plus actionable `reason` and remains mandatory. Structured
prebuilt `not_applicable` is compatible with `resolved`. U1 fails any
unresolved row, and U2/U5/U6/U8 refuse it. Raw-blob evidence remains normative
in the architecture verdict rather than being copied here.

The offline determinism test performs two independent generations into
separate temporary output roots under OS-level network denial. Each run has
distinct empty/isolated `HOME`, `TMP`, `TEMP`, `TMPDIR`, `XDG_CACHE_HOME`, `GOPATH`,
`GOMODCACHE`, and `GOCACHE` and sets `GOTOOLCHAIN=local`, `GOPROXY=off`,
`GOSUMDB=off`, and `GOENV=off`. It runs outside a repository with
`GIT_DIR`/`GIT_WORK_TREE` unset, `LC_ALL=C`, `TZ=UTC`, `umask 022`, and an exact
absolute `PATH` plus tool-manifest digest. The only Git provenance input is the
content-addressed `git-object-evidence.lock.json`/blob root containing raw
`ls-remote`, base/peeled ref, tag/commit object bytes and headers, signature
material, parent/tree IDs, and ancestry paths. The generator cannot inspect an
ambient `.git` or invoke object discovery. It recursively compares the two
output trees byte-for-byte and each tree with the checked locks. Ordinary
validation never runs historical binaries, providers, package managers, or
network requests. Producer acquisition/build is separate and consumes the
pinned source archive/tree identities.

`semantic-surfaces.lock.json` is independently derived per exact revision by
walking three enumerations from its pinned source archive: schema migrations,
public types/storage interfaces, and CLI create/read/mutate/export surfaces.
Every discovered item has a stable ID, exact source witnesses, durability class,
and—when durable—one disposition: preserve, intentionally recompute with
postcondition, reviewed retire with rationale/guidance, or historically absent
with a negative binary probe. It explicitly covers all issue fields, events,
wisps/relations, custom statuses/types, configuration, counters/snapshots/
compaction, routes/federation/interactions, and tombstone/deletion semantics
where present. Cache/index/temp/telemetry ephemera require positive exclusion
evidence and never count as preserved data. Bijection mutants delete, duplicate,
misclassify, or add concepts and must fail. U2 realizes every applicable ID;
U8 independently regenerates and freezes the catalog/evidence digest.

`providers.lock.json` records every current provider/topology/runtime/build/
platform variant.
Its validator derives the denominator independently from configuration
constants and validation, init flags, store factories including non-CGO paths,
storage implementations, and lifecycle dispatch. The proposed lock and derived
denominator must be bijective; every total is derived. After default/alias
normalization, the one canonical ordered tuple has exactly nine fields:
`(provider_id, access_path, store_scope, lifecycle_owner, endpoint_kind,
proxy_upstream, build_variant, platform_id, engine_runtime_id)`. A tuple ID is
a versioned JCS digest of all nine fields, not an ordinal. `platform_id` alone
binds GOOS/GOARCH. Independently, `engine_runtime_id` binds distribution, exact
server/binary version or image digest, protocol, canonical configuration digest,
and its semantic envelope including collation/charset/time-zone behavior and
the source-proved supported-version minimum/boundaries. Embedded/in-process rows
use the literal `embedded/no-external-runtime` sentinel. Runtime boundary and
equivalence cells derive from exact source evidence rather than semver or
platform. Only satisfiable discovered combinations are admitted, and
selector witnesses preserve aliases such as `--shared-server`,
`dolt.shared-server`, and the corresponding environment selectors without
collapsing different roots, endpoints, ownership, or branch behavior. A
platform not supported by a runtime/topology is an exact reviewed
inapplicability cell, not an omitted row. Rows differing only by
`engine_runtime_id` remain separate; conflating them or deleting either is a
denominator failure.

Every tuple has exactly
`init_workspace`, `construct_store_generic`,
`open_store_read_only_factory`, and `open_configured_cli_uow`; every operation
has its own exact source-route file/symbol/build-constraint/blob/digest.
The inventory covers Dolt embedded, owned workspace server, shared managed-at-
init and external-at-runtime server, direct external server, managed-child and
external-host/socket proxy surfaces, plus every discovered SQLite, PostgreSQL,
and MySQL topology. CGO status changes only embedded-Dolt applicability absent
new evidence: non-CGO embedded rejects with `requires-cgo`, while direct server
Dolt and SQL providers work in both builds. Proxied init rejects at the early
not-implemented guard; proxied generic construction and read-only factory open
reject with `uow-provider-required`; only configured CLI/UOW open succeeds.
CLI dispatch short-circuiting does not prove read-only factory support. Each
matrix cell executes authentically or retains pinned proposed-inapplicability;
selector, route, outcome, and lifecycle invariants all validate.

`lifecycle-capabilities.lock.json` is a separate bijection over planner route ×
topology × engine runtime × build/platform and the nine stages `inspect`,
`quiesce`, `snapshot`, `prepare`, `verify`, `activate`, `final_verify`, `resume`,
and `restore`. Every cell has executable interface/adapter ownership evidence
and expected success or typed missing capability. A mandatory missing cell
creates the corresponding Beads U4 child or proven driver dependency and blocks
U8; the four provider open/init operations cannot substitute for this matrix.

`install-channels.lock.json` is generated from independent repository, docs,
release-automation, package-manager, and live-catalog discovery. Every signal
is classified as `candidate_executing`, `alias`, `non_cli`,
`dormant_unpublished`, or `retired` with reviewed evidence. This includes
Winget (currently stale/dormant staging, not a published v1.2 CLI channel),
PyPI `beads-mcp` (non-CLI), Homebrew, AUR, mise, npm/bun, direct Go, shell and
PowerShell installers, Nix, and every newly found install-looking surface.
Authoring observations are evidence to recheck, not permanent exclusions: U1,
U8, U9, and U10 detect publication/staleness drift.

A canonical case ID is the versioned JCS digest of `(surface_id, branch_id,
platform, arch, manager_mode, materialization_id)`. Each case records its branch
predicate, recipe/artifact identity, union arm, and machine-readable branch
trace. Each `public_latest_only` row also records activation mechanism,
accountable owner, prerequisite receipt, non-secret credential reference/class,
bounded propagation window, budgeted retry/backoff, expected selector, and
channel-specific rollback/quarantine/escalation. Incomplete activation data is
reviewed blocked/inapplicable, never a runtime skip. Prior reviewed
inapplicability removes the runtime obligation; a reviewed blocked row blocks
publication before selector execution and never becomes a runtime skip.
`scripts/install.sh` has release archive, CGO go-install, non-CGO
go-install, and shallow-clone/current-source CGO branches; `install.ps1` has ZIP,
ambient-CGO Go-install, and ambient-CGO source-clone branches plus skip/source
overrides. npm and bun verify wrapper plus resolver and recovered versioned
archive; CI download-skip is a non-executing branch. Homebrew bottle and source
fallback are distinct. Mise is release-packaged. AUR `beads-git` is mutable and
cannot masquerade as a pinned derivation. Packaged identity binds manifest
entry and archive/payload digest; wrapper identity binds wrapper tarball,
resolver, and payload digests; source identity binds frozen tree, exact argv/
env/tags/ldflags, toolchain/compiler/sysroot, and executor. Exact normalized
materializations alone may deduplicate. Every reachable branch executes
authentically with injectable frozen inputs or has pinned inapplicability;
U9 uses only injectable or staging selectors and cannot claim that a live
public-latest route selected an unpublished U8 candidate. Every U1-locked
`public_latest_only` case is therefore executed later by the U7-owned,
human-authorized publication molecule/workflow after publication.

U8 validates every realizable source contract without an output identity. U9
emits an immutable canonical materialization inventory bijective with
derivation identity, exactly one realized SHA-256 and exact `v1.2.0` version per
identity. Consumers reference it; aggregation rejects rebuild or disagreement,
and U10 validates its digest. Only packaged manifest entries carry
`candidate_artifact_digest`. Qualification publishes or mutates no maintained
public channel.

The lock checker fails on missing/extra/duplicate names or revisions, invalid
provenance, role-ineligible asset producers, unresolved recipes, unknown or
ambiguous variant families, unbound producers, invalid transition ancestry,
probe count/set-digest mismatch, evidence not bijective with the regenerated
stream, provider operation-route disagreement, channel-relation disagreement,
or nonempty unreviewed-delta set. U6 and U8 consume and validate both
denominator and evidence locks.

### 3.2 Authentic binary and workspace factory

The existing `scripts/migration-test` harness remains the test surface. It is
extended rather than replaced with a second framework.

Responsibilities:

- derive a producer key from exact peeled source commit, platform, build flavor,
  recipe digest, and toolchain digest, while retaining every consuming
  `tag_revision_id`; verified official artifacts use structured
  `not_applicable: prebuilt-official-bytes` recipe/tool values;
- fan in before row execution: resolve an official artifact by exact checksum
  or build the exact peeled source commit with the pinned toolchain/build flavor exactly
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
- verify the supplied `candidate_set_digest` and execute exactly one
  manifest-bound `candidate_execution`: direct packaged bytes; wrapper-recovered
  packaged bytes; or a fully pinned frozen-source derivation with a recorded
  realized output digest.

Every terminal result carries its own provenance envelope: `tag_revision_id`,
`engine_runtime_id` (including `embedded/no-external-runtime`),
source tag/commit/tree and binary digest or fully resolved build-recipe and tool
digests; frozen helper tree; candidate commit/tree and one
`candidate_set_digest`; OS/architecture and executor/container identity;
relevant allowlisted environment; external Dolt identity or explicit
version-gated `N/A`; and origin plus digest for every downloaded input. Its
`candidate_execution` union is exact: `packaged_bytes` includes the selected
`candidate_artifact_digest`; `wrapper_contains_packaged_bytes` includes wrapper
digest plus the recovered payload's matching `candidate_artifact_digest`; and
`frozen_source_derivation` includes frozen-source, recipe,
executor/toolchain/compiler/sysroot identities, realized output SHA-256, and
verified embedded version. The
aggregator validates these fields on each result rather than joining them from
ambient job metadata.

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
selection. Its denominator is the exact U1 `semantic-surfaces.lock.json`,
independently regenerated from schema migrations, public types/storage
interfaces, and CLI create/read/mutate/export surfaces for the exact revision;
it is not a hand-maintained field subset. Each durable catalog ID has one
preserve/recompute/retire/absent disposition, and each proved cache/ephemeral ID
is excluded explicitly. U2 realizes every applicable ID and U8 freezes the
regenerated catalog/evidence digest.

The deep oracle has an explicit, per-generation feature matrix. For each source
it individually covers ID, title, description, status, priority, type, every
supported timestamp, assignee, owner, external reference, and custom metadata;
events; wisps/relations; custom statuses/types; configuration; counters,
snapshots, and compaction; routes/federation/interactions; tombstone/deletion;
label values; comment bodies/authors/timestamps; dependency endpoints/types;
blocker/readiness behavior and all applicable semantic counts;
workspace and repository identity; and applicable branch, remote,
redirected/shared, server, and proxied semantics. It then tests create, field
update, dependency add/remove and resulting readiness changes, close, semantic
export, reopen, and completed-migration `NoOp` plus another reopen.

Every named cell is either exercised as supported or marked absent with a
pinned feature boundary and a negative invocation against the historical
binary. Intentionally recomputed cells verify their locked postcondition;
reviewed-retired cells verify the approved absence and guidance. Catalog
deletion/duplication/durability/disposition/new-concept mutants, empty fixtures,
and broad "supported fields" assertions are invalid.
Refusal, dry-run, failed-apply, and rollback paths compare raw source hashes or
the strongest immutable driver identity; rollback must reopen and match the
original oracle.

Minimal per-tag-revision smoke always creates nonempty data and verifies direct upgrade,
reopen, mutation, and idempotent rerun. Its `smoke_row_id` is exactly tag-
revision/producer/topology/platform/build-flavor/`engine_runtime_id`, and every mandatory identity emits
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
`covered_identities` carry the same runtime-bearing identities as traceability
metadata only and never emit, satisfy,
reuse, or duplicate smoke results. Each terminal result has exactly one
namespace key; both/neither keys and unknown coverage references fail.
Aggregation validates the complete
`smoke_row_id` and `focused_case_id` denominators independently. Sharding and
aggregation never collapse runtime: rows differing only by `engine_runtime_id`
remain distinct, and deleting either fails the denominator.

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
InspectExistingReadOnly -> (Observation, opaque InspectionWitness)
PlanCapabilitiesReadOnly -> CapabilitySet
ValidateWitnessAndOpen(witness, expected route) -> Store | typed refusal
AcquireQuiescence(expected source identity) -> quiesced probe scope
DiscoverOperationReadOnly(source identity) -> opaque state + generic envelope
AcquireOperationAuthority(operation ID, expected revision) -> epoch/fencing token
Prepare(plan) -> opaque prepared target/checkpoint
Verify(prepared target, oracle contract)
Activate(verified target, expected source identity)
ResumeOrRestore(opaque checkpoint)
```

Concrete naming follows existing interfaces and `dolthub/driver`; this is not a
new public SDK. Source/provider adapters own engine behavior, durability, and
cleanup. The migration core treats checkpoints and identities as opaque.

Every open path—including `NoOp` and read-only factory open—receives the exact
inspection witness and expected route. The factory cannot reread configuration
to reselect/reprovision a route. Its owning adapter/driver atomically validates
the witness's raw-config, route, topology, source-identity, and capability
digests before any open-time effect, then opens only that route or returns typed
`replan_required`/`open_refused`. Race suites change each witness input and prove
zero provider launch/provision/version/telemetry/metadata effects. A provider
without this primitive is a route-owned U4/upstream blocker.

Driver/adapter-owned durable operation state is authenticated and discoverable
read-only. Its generic envelope exposes operation ID, source/target/plan digests,
authority epoch/fencing token, authenticated state digest, CAS revision, and
persisted lifecycle boundary while the engine-specific state/handle remains
opaque. All transitions compare-and-swap the expected revision and present the
current fencing token; stale actors and foreign digests fail before effect.
Failure injection after each persisted boundary proves rediscovery and
idempotent resume/restore. Missing authentication/discovery/fencing/CAS support
is a U4/U8 blocker under executable ownership evidence, never a Beads-side
recovery table, flock, engine query, or inferred-completion workaround.

Before routing any missing-capability work, executable interface/adapter
evidence identifies the owning module and repository. A gap in a Beads-owned
`internal/storage` adapter or topology, including applicable SQLite,
PostgreSQL, MySQL, server-Dolt, or proxy behavior, creates a real dynamic U4
route child in this epic. Only a primitive proved to belong to the
`dolthub/driver` interface or implementation creates a linked upstream issue/PR
dependency. Both child kinds directly block U8 while only the affected route is
blocked; independent catalog/harness/current-route work continues. No Beads-
side patch may substitute for a driver-owned primitive, and no Beads-side flock,
engine inspection, file-copy cutover, or retry substitute is allowed.

Before U4 changes any migration/schema surface overlapping open PR #4878,
rerun `scripts/pr-preflight.sh` for migration/schema keywords and PR #4878,
review maphew's current head first, and prefer adoption/fixup on that
contributor branch. Preserve its tests and attribution and record the exact
disposition. No U4 PR may silently replace, close, or supersede it; a necessary
rewrite requires an explanation on the contributor PR and explicit credit.

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
      +---- NoOp ---- validate witness/expected route -> exact provider open
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

### 3.7 Version finalization before freeze

U7 is the last source-changing owner for version bytes. Under TDD and the normal
review gates it expands `scripts/check-versions.sh` until its discovered check
surface is bijective with everything `scripts/update-versions.sh` changes and
every other tracked release-version surface, including current Copilot plugin,
`default.nix`, all Windows resource JSON/XML fields, the MCP local-package lock
entry, managed hook markers when in scope, and README/released-doc policy.
Updater fixture tests require each expected replacement exactly once. Immediately
before U8, U7 derives and reviews `version_date` as the intended UTC publication
date in strict `YYYY-MM-DD` form, not an authoring-date literal. It moves the
reviewed `CHANGELOG.md` content into an exact top
`## [1.2.0] - <version_date>` section and prepends a nonempty
`VersionChange{Version: "1.2.0", Date: "<version_date>", ...}` in
`cmd/bd/info.go`; text and JSON `bd info --whats-new` tests require that newest
entry and `versionChanges[0].Version == Version`. It then runs
`./scripts/update-versions.sh 1.2.0`, refreshes the MCP lock with pinned tooling,
regenerates any in-scope managed hook sections, and runs the expanded checker.
The checker requires every full-semver surface, PE/XML numeric field, local MCP
lock entry, first versioned changelog heading, and info version/date relation to
match exactly. U8 freezes that date; malformed/mismatched values or publication
after the intended UTC date invalidate the epoch and require reviewed source
finalization plus a new U8. The deprecated `scripts/bump-version.sh` exits 1 and
is not an execution path. After U8, the old source-changing release-prep formula
mode and any date/version edit are forbidden rather than a way to finish release.

Disposable packaged, wrapper, and every source-derived branch probe must report
exact `v1.2.0` before U7 closes. U8 repeats the packaged/wrapper checks and
freezes exact `v1.2.0` source expectations; U9 proves each materialized source
output reports exact `v1.2.0`. No version-bearing byte changes after U8.

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
manifest, immutable packaged-artifact inventory, and candidate-execution
contracts. Packaged cases may execute only the selected artifact matching its
platform/build `candidate_artifact_digest`; wrappers must recover those exact
bytes. Locked source-derived channels materialize once per derivation identity
from fully pinned frozen inputs and publish one canonical immutable
materialization inventory keyed bijectively by derivation identity, with exactly
one realized SHA-256 and embedded version per key. Consumers reference it; no
shard may substitute a relation, disagree on output, or rebuild packaged bytes.

A producer fan-in independently materializes each unique historical producer
key once and distributes immutable verified bytes through the workflow's
digest-addressed artifact transport. Row workers never fetch from external
origins or compile. The matrix deterministically shards every mandatory
runtime-bearing `smoke_row_id`
and required `focused_case_id`. Each shard emits exactly one result per assigned
key and uploads machine-readable results plus concise failure logs. The
aggregator independently rejects missing, extra, duplicate, skip-like,
candidate-set-digest-mismatched, or invalid candidate-execution results
and reports smoke `N/N` and focused `M/M`.

The workflow has a UTC-daily `schedule` trigger in addition to explicit manual
dispatch. A structural test parses the workflow and proves the scheduled path
selects the complete releases lock and exhaustive job rather than a
representative subset; removing, disabling, or retargeting that trigger fails
CI.

### Tier D: v1.2 candidate qualification

Build/package one multi-platform candidate set once, record its
`candidate_set_digest` and every packaged manifest entry's
`candidate_artifact_digest`, and freeze all source-derivation contracts. A
`frozen_source_derivation` has only its frozen source/ref/recipe/executor/
toolchain/compiler/sysroot and expected-version contract at U8; its realized
output SHA-256 and embedded version exist only after U9 materialization. Then
run:

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
- every maintained install channel against its exact manifest-bound
  candidate-execution relation.

Only exact smoke `N/N` plus focused `M/M` for the same candidate set is
eligible for a later human release decision.

U7's evaluator stores every capture's raw bytes and recomputed digest and
compares canonical modeled state. U10 emits a canonical read-only
`EligibilityDecision` with input/target/freeze/baseline/result digests, counts,
outcome/reasons, evaluator version, and `decision_digest`; it has no bundle,
locator, expiry, claim, or publication fields. Only an eligible final decision
enters a distinct append-only seal operation. Bundle bytes bind that decision
digest plus U8/U9/target/version/channel inputs but never their own digest. The
separate `BundleSealReceipt` contains decision/bundle/artifact digests, locator,
signer, seal time, 24-hour claim expiry, and retention. Thus evaluation is
read-only, sealing creates only new output artifacts, and no self-reference is
possible.

The executable backend is `actions/upload-artifact@v4` with
`retention-days:90`; its locator is the authenticated repository/run/artifact ID
and API SHA-256. `actions/attest@v4` signs the bundle digest as a custom in-toto/
DSSE predicate under GitHub OIDC and GitHub/Sigstore Fulcio/Rekor public-good
roots. A pinned upgraded `gh attestation verify` enforces exact repository,
workflow/ref, frozen commit, digest, GitHub-hosted runner, and reviewed trust-
root version. GitHub artifacts are substitution-detectable, not WORM, and have
a 90-day public-repo ceiling: the plan promises 90 days from seal, then retains
identical bundle/receipt bytes as stable release assets through 90 days after
landing and every rollback window without claiming undeletability. Literal
object lock would require a separately provisioned reviewed external backend
before U8, never a new Beads service.

U7 preflight requires the human-provisioned `beads-release` environment
(reviewer, prevent self-review, UI-provisioned/API-verified
`can_admins_bypass:false`), and one dedicated environment-gated release GitHub
App. Its `contents:write` is used for protected claim/release tags and assets,
`actions:write` only for `return_run_details` dispatch, and
`administration:write` only for temporary exact-ref ruleset create/read/delete;
the attestation job retains a separate least-privilege `GITHUB_TOKEN`/OIDC
identity. Current observed absence of that environment/credential, the
unprotected target/no effective rules, bypass-bearing `v*` rule, tag-triggered
rebuilding workflow, and seven-day artifacts are failing evidence. Dedicated
stable-tag and claim-tag rules allow only release-App creation and no
operational update/delete bypass; repository/organization admins remain the
explicit GitHub control-plane trust boundary.

Publication first canonicalizes `ClaimIntent` bytes binding version, decision/
bundle/guard, run ID, triggering actor, authority, expiry, and nonce while
excluding all attestation locators/digests and envelope fields. It hashes and
attests that intent. A distinct `ClaimEnvelope` and annotated claim-tag message
then bind `claim_intent_digest` plus the resulting attestation locator/digest
before conditional creation of `refs/tags/beads-release-claims/v1.2.0`. HTTP 201
wins atomically; 409/422 and crash recovery independently verify the envelope,
attestation subject digest, and every intent field at the persisted intent,
attestation, tag-object, and ref-creation boundaries. Only the same run/actor/
authority/bundle/nonce may resume with a new attempt. The ref is never updated,
deleted, or reclaimed; foreign or expired authority requires a new epoch/version.

The source-branch freeze is an honest best-effort GitHub control: create a
temporary active exact-ref ruleset (`update` + `deletion`, empty operational
bypass), require HTTP 201, capture its ID/request digest, poll the ruleset and
effective-rules APIs, then reread the target ref before every mutation. 403/404,
ruleset drift/deletion, bypass changes, or ref movement stops publication. This
is not an atomic lease or transaction, Actions concurrency is only run
serialization, and admin control-plane changes remain a trust assumption. Once
created, the separately protected annotated `v1.2.0` at the frozen OID is the
immutable release authority.

Parameterized dispatch uses the workflow-dispatch API with
`ref: v1.2.0` and `return_run_details:true` after protected annotated tag
creation. Before the temporary rule is released, the returned run is reread and
must have `event=workflow_dispatch`, `head_sha` equal to the frozen OID, and the
exact frozen workflow identity and blob OID/digest. Those facts plus the HTTP
200 run ID/URLs and explicit tag ref become a signed
`DispatchAcceptanceReceipt`. Only then is the rule deleted by exact ID with an
audited absence reread. The receipt does not imply transactionality. The existing
formula/molecule checkpoint store persists a signed hash-chain receipt for each
tag/release/asset/channel step. Same-claim/same-guard existing state is fully
digest-verified and resumed idempotently; foreign collision rejects. Crash
recovery rediscovers the claim and latest receipt and resumes only that fenced
authority. `release.yml` removes tag triggers, source-changing/rebuilding/
`--clobber` behavior, and post-U8 reconstruction; it promotes only U8 inventory.

After publication, the workflow runs the lock-derived activation DAG. Terminal
failure compensates/quarantines activated channels in reverse DAG order with an
idempotent signed receipt chain. Partial compensation leaves release/root
incomplete and escalates; the protected tag is never moved/deleted/reused and
1.2.0 is never rebuilt. The receipt cannot satisfy or repair U9 and is not a
product attestation controller.

U6 depends on completion of U4's shared lifecycle-contract work. U4 may close
after that contract is complete and it has emitted children for every proven
missing route-specific capability, using executable ownership evidence to keep
Beads-adapter work in this epic and to link upstream only for proven
`dolthub/driver` primitives. Those dynamic route children do not block U6; both
owner classes block U8 directly. U6 invokes U4-owned lifecycle tests and every row
executes, so a missing route capability is a terminal route-local `FAIL` while
all other rows continue. Universal `N/N` smoke and `M/M` focused passing belongs
to U9.

## 5. Candidate Epoch, CI, and Performance

The qualification epoch has four ordered phases:

1. **Pipeline implementation.** Land all remaining production, packaging,
   fixture, oracle, qualification-workflow, and release-gate helper changes.
   Disposable pipeline-test artifacts are not candidates.
2. **Cutoff refresh and byte-freeze barrier.** Report every current-origin and
   release/asset addition, deletion, and move, and refresh every external
   channel observation; require an empty unreviewed-delta
   set; and revalidate the latest reviewed lock-derived tag-name/tag-revision
   scope and evidence totals after all reviewed append-only deltas. Also
   validate roles, resolved-probe commitment/evidence, provider/topology/build,
   and install-channel discovery. Retained/superseded historical absence is not
   a delta. Fail back to source work on unreviewed drift. After all prerequisite PRs merge,
   resolve the remote `feature/backend-provider-change-20260713` head, require
   it as the frozen commit, and finalize one freeze-record digest over its tree,
   build environment, recipes, every qualification input, and the exact
   reconciled current-origin, release/asset, and external-channel raw envelopes
   and internally verified digests.
   Every discovered
   U4 route child directly blocks this barrier; no open PR or unmerged helper
   change may remain in the epoch.
3. **Candidate construction and qualification.** Build/package one candidate
   set once from the exact frozen commit/tree with the finalized freeze-record
   digest as a hard stage input. U8 freezes source-derived source/ref/recipe/
   executor/toolchain/compiler/sysroot and exact `v1.2.0` expected-version inputs without
   requiring unrealized output identity. U9 materializes each derivation once,
   verifies its canonical inventory entry's realized SHA-256 and exact `v1.2.0`
   embedded version, then runs all tiers
   without changing any frozen input, manifest, inventory, or packaged artifact
   byte. Immediately before accepting aggregation, rerun read-only
   current-origin, release/asset, and external-channel discovery, internally
   validate the new envelope bytes/digest, and reconcile normalized modeled
   state against the frozen U8 observation. Raw timestamps or serialization may
   make the new envelope/digest differ without semantic drift. A source
   mismatch, target-ref movement, unavailable
   reconciliation, or unreviewed remote/release/asset delta aborts the epoch
   without rewriting its scope and returns to reviewed source work plus a new
   U8.
4. **Read-only eligibility plus append-only seal output.** Consume only the frozen manifest and completed
   results and report eligible/ineligible. It consumes no PR-review records,
   reviewer assertions, participant lists, GitHub credentials, or controller
   receipts. It validates the U9 materialization-inventory digest and repeats
   the same live current-origin, release/asset, and external-channel
   reconciliation against U8's frozen observation; drift or indeterminate
   observation is ineligible and requires a new reviewed U8 epoch. This phase
   cannot modify source, rebuild, repair results, rewrite frozen scope, tag, or
   publish. It first emits `EligibilityDecision` with no bundle fields. When
   eligible, a distinct output-only step may use U7's bundler to assemble,
   upload, attest, and return canonical bundle bytes plus a separate
   `BundleSealReceipt`. Only new artifact/attestation outputs change; no frozen
   input, source, candidate, result, release, or channel changes.

After phase 4, outside the runnable DAG, the named accountable human release
operator may authorize the U7-implemented publication-only molecule. U10's
eligible result is input, never mutation authority; the immediate fresh guard
is authoritative. The root convoy has the exact `owned` label and therefore
does not auto-close with U10. The named accountable human manually lands/closes
it only after exact-OID tag/artifact publication and a complete passing
postpublication public-latest receipt. A human refusal to
publish is an explicit signed cancel/supersede disposition, not successful
release completion. This keeps exactly 11 runnable children.

Any byte change, including a version byte, after phase 2 invalidates the whole epoch and returns to phase
1; a new freeze, candidate identity, and full qualification are required.

PR jobs use cached representative binaries and stay bounded. Full catalog rows
are sharded by a stable hash of row identity so retry and aggregation are
deterministic. A pre-row producer fan-in keyed by exact peeled source commit, platform,
build flavor, recipe digest, and toolchain digest fetches or builds each unique
historical artifact once per run; a multi-row mutation test proves one
materialization and immutable fan-out. Failed shards retain small diagnostic
artifacts; successful shards need only result rows and checksums.

U1's measured performance lock pins runner image/hardware and CPU/filesystem
calibration, at least 30 NoOp samples, and these gates: NoOp/open incremental
p95 `max(25 ms, 15%)`, p99 `max(75 ms, 25%)`; PR 45 minutes; nightly 8 hours;
U9 12 hours; row 20 minutes; producer 30 minutes; at most 24 concurrent shards
and 32 smoke + 16 focused cases per shard; 250 MiB upload/shard, 20 GiB/run,
50 GiB cache; 1,500 GitHub API calls/run and three acquisition attempts/
producer. U6/U9 publish raw plus hardware-normalized receipts and fail a gate.
Only an accountable-human reviewed lock delta with rationale/expiry changes a
budget; a running U8/U9 cannot waive failure.

The candidate workflow emits exact immutable manifest bytes containing
target-ref identity, commit, tree, freeze-record digest, version, packaged
platform/build artifact names and SHA-256 digests, candidate-execution
contracts, build metadata, environment identity, and the frozen-input digest
inventory. The SHA-256 of those exact manifest bytes is
`candidate_set_digest`; each packaged artifact SHA-256 is a
`candidate_artifact_digest`. Every consumer verifies the set and its exact
union branch before execution. A local `bd`, unauthorized `go run`, rebuilt
packaged binary, unpinned source derivation, PATH shadow, changed helper, or
mixed epoch is rejected.

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

At each persisted boundary, read-only discovery authenticates the exact
operation/source/target/plan state and CAS revision; current authority presents
its epoch/fencing token and stale actors fail before effect. Crash tests restart
from a new process after every boundary and prove idempotent current-authority
resume/restore. Every provider open consumes and atomically validates its opaque
inspection witness/expected route; races on raw config, route, topology, source,
and capability inputs produce typed refusal with zero effects.

Any unavailable dependency, timeout, permission failure, incomplete probe,
unsupported capability, verification mismatch, or ambiguous provider evidence
is nonzero and failure-dominant.

Live plan reconciliation deliberately has two projections. The active
materializer remains authoritative for the 11 runnable payload records and 22
static edges. Recursive discovery must total 162: those 11 runnables plus an
exhaustive 151-record legacy partition of 11 preserved (including closed
`bd-4velg` under `.1` from previous plan SHA `b92f3957...`), five adopted, one
retained, ten deferred, and 124 superseded. The already mapped `bd-ldt0f` root is a separate exact projection
because the live record is type `epic`, has no `tracks`, and the materializer
only updates mapped-root metadata while assuming the first child was seeded.
Before any payload, root, legacy, or membership reconciliation, separate city-
HQ task `mc-ucid`, owned by the Gas City/Beads maintainer, must close. It is
created and managed only through city-HQ context (`gc bd --city
/data/projects/maintainer-city ...` without `--rig beads`), stays outside
`bd-ldt0f`, is never parented or tracked by the convoy, contributes no static
DAG edge, and is not a twelfth member. This 162-record assertion is the exact
pre-slice baseline. The task first schema-validates and atomically edits only
`.dolt_database` and `.project_id` in
`/data/projects/beads/.beads/metadata.json`: the former changes from
`bd_metrics_repo_2424946071` to `bd`, and the latter from
`8d69d5b6-0917-47a6-9761-db2b0dcca2fc` to
`bafe313f-9fce-4972-849d-1f825740e9a5`. `.database == "dolt"`, mode/backend/
host/port, and every unrelated field are preserved; schema and exact projection
are reread before it runs exactly
`gc rig --city /data/projects/maintainer-city set-endpoint beads --inherit`.
The endpoint is inherited `city_canonical`, never external. The accepted tuple
is city `/data/projects/maintainer-city`, rig `beads`, rig path
`/data/projects/beads`, endpoint `127.0.0.1:3307`, physical database
`/data/services/gascity-local-dolt/bd`, remote
`git+https://github.com/gastownhall/beads.git`, and the canonical database/
project values above. Explicit-rig read-only context, root lookup, and recursive-
subtree proof must agree after repair. Environment overrides and raw SQL remain
diagnostic only and never bypass reconciliation. Only after `mc-ucid` closes and
those proofs pass may the reviewed one-shot reconciler reread the root revision,
require status already open, and use
`bd update --if-revision` to convert it to custom type `convoy`, P0 and to
set the exact row-owned root title/body/design/acceptance fields and metadata
map in
`tasks.md`. It preserves status, pre-read assignee, notes, external reference,
owner, and unspecified audit/storage fields by omitting their update flags and
checking them after the CAS. Exact metadata uses guarded `--set-metadata` and
`--unset-metadata` diffs because `--metadata` merges. The separate exact label
projection includes `owned`; reconciliation tests prove Gas City skips automatic
close and permits only accountable-human manual landing after the signed
postpublication receipt. Current `bd` cannot combine
label or parent relation changes with `--if-revision`; under an exclusive
reviewed reconciliation window, labels and parent/track relations are separate
non-CAS operations followed by fail-closed exact rereads. Legacy reconciliation
detaches/reparents every non-payload immediate child (retaining `.1.9` top-
level), then `gc convoy add` creates the exact 11 tracks including `.14`.
Immediate-child, tracks, and effective `gc convoy status` member sets must each
equal those 11 IDs. The reconciler never infers or mutates unmapped `.16`, and
the ordinary materializer is not represented as repairing any root field it
does not update.

## 7. Delivery and Review

Architecture changes use Sol/Ultra; implementation uses Terra/high with tests
written before behavior. Each implementation slice uses an isolated worktree.
Before source work, U1/U2/U4/U5/U6/U7 and any similarly broad macro child pour
one bounded task-local molecule of ordered vertical slices. Every slice has one
end-to-end outcome, files/dependencies/AC/verification, RED then minimal GREEN
then refactor, and independent review. Slice state blocks macro closure but
never changes the root's 11 immediate/track/effective members or 22 macro edges;
the slices are macro descendants created only after the 162-record baseline is
reconciled. No meta-slices or repeated planning loop are allowed.
Before a source commit, derive the digest directly from
`git diff --cached --binary --full-index`, preserve
those exact patch bytes immutably, and capture `git write-tree`; also record the
resolved active hook path/digest rather than assuming the checkout hook runs.
Exactly three independent Sol/Ultra seats review those bytes and all three must approve
with no unresolved Critical or Important finding. Recompute both immediately
before commit. After commit and before push, require the commit tree to equal
the approved index tree and the SHA-256 of
`git diff --binary --full-index HEAD^1 HEAD` to
equal the approved staged-diff
SHA-256, using first-parent semantics for merges. A hook-induced format/fix/
restage mismatch blocks propagation; replacement bytes are reviewed afresh and
the commit is replaced or amended only while unpushed, then both checks repeat.

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

A trace checker parses numbered R1-R8 clauses and task
`requirements_trace`, expands ranges, and emits a canonical bidirectional
matrix. Unknown references, untraced requirements, tasks lacking a reverse
trace, or U7 missing any of R8.6-R8.10 fail before source work and again at U8.

## 8. Deletions and Non-Designs

Use the operational controls above; add no checked-in review/bootstrap
controller, review-receipt or approval database, credential broker, commit/push
wrapper, general-purpose product/runtime attestation schema or service,
PR/release orchestration service, generic storage recovery framework, or
provider conversion matrix. U7's narrowly scoped canonical release-bundle
schema and signed/attested publication artifact are allowed. The former 99-slice
execution plan is retired in favor of eight source-work children and three
no-source freeze/qualification/eligibility barriers in `tasks.md`; completed
useful work and contributor history remain preserved in Git and Beads.

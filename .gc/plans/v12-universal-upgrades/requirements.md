---
plan_slug: v12-universal-upgrades
phase: requirements
rig: beads
rig_root: /data/projects/beads-v12-universal
artifact_root: /data/projects/beads-v12-universal/.gc/plans
status: approved
created_at: 2026-07-18T00:00:00Z
updated_at: 2026-07-22T00:00:00Z
approval_basis: user-approved universal-upgrade goal and explicit instruction to proceed
---

# Beads v1.2 Universal Upgrade Requirements

## 1. Outcome

Every valid workspace created by any prior public Beads `v*` tag must have a
deterministic path to the exact manifest-bound `candidate_execution` selected
from the frozen v1.2 candidate set. Packaged rows still execute exact packaged
bytes; wrapper and source-derived rows obey their union contracts.
Qualification is complete only when every mandatory historical smoke identity
and every required focused case passes with no skips and the candidate set that
passed is the only candidate set eligible for a later, separate human
publication decision. Qualification itself never creates a stable tag or
publishes a release. U10 remains read-only. If an accountable human release
operator elects to publish, the qualification-aware publication-only path that
U7 implements performs the later mutations; the root program is not complete
merely because U10 reports eligible.

U0 starts from the freshly fetched current head of
`origin/feature/backend-provider-change-20260713`, never from an obsolete
literal parent. At this authoring pass that head is
`f7d0c26ec8c1e7b6b075cc49b07cb2f0f41c3a47`, whose sole parent is
`af136f8857dd3e0461e06597f37e925088a98a49`; its tree exactly equals merged PR
#4801's final head `e89ab9aa09bb178e2cfe1dec838e0e601f9663db` tree. Commit
`a0a51638c036d25923d8671949e27a2bc12ba310` belongs to PR #4802, not #4801,
and `tree(a0a51638c) == tree(2ef7c61e0)`. Existing PR #4907 at
`0a0be15db29250f0ebb46793e7bcfc3b1905e245` is `DIRTY` against that head and
cannot be merged or treated as current reviewed bytes. U0 must extend that PR
with a freshly resolved integration that preserves the fetched target, the
accepted historical chain through
`dce8d066de983b4fa4487890f48157a7264d86d2`, and merged PR #4810's accepted
`1b5f02efddac224d933526b2025481f2b952f34f` adoption as literal ancestry.
PRs #4801, #4810, and #4845 retain their accepted intent, tests, authorship,
and contributor attribution. PR #4907 is only the integration vehicle and is
not contributor intake. No conflict-free or byte-equivalence assertion is made
beyond the tree equalities verified from current evidence; every reconciliation
byte is reviewed and tested afresh.

Both U0 provenance merges use `--no-ff --no-commit` and become separate
reviewed commits. Ordered parents are `[fresh target_oid, prior #4907 head]`
for reconciliation and `[reconciliation_merge_oid,
1b5f02efddac224d933526b2025481f2b952f34f]` for adoption. Each commit receives
three fresh independent Sol/Ultra approvals of its identical staged-diff
digest and record the approved index tree from `git write-tree`. After each
commit, and before any push, the commit tree must equal that approved index
tree and the SHA-256 of `git diff --binary --full-index HEAD^1 HEAD` must equal
the approved staged-diff SHA-256; the first parent is authoritative for both
merge commits. Final integration is protected by an enforced base-OID-bound
up-to-date or merge-queue guard that auto-invalidates on base movement. U0 also
captures PR #4907's canonical number, URL, and old/current head identities,
verifies its corrected body and current head, and proves the existing
historical `status/needs-review-auto` `LabeledEvent` through the fully paginated
timeline without re-adding the consumed trigger.
Legacy bead `.3.27` closes on accepted #4845/`dce8d066d...` as the shipped
representation of its v0.62 bridge outcome, not on a same-numbered literal
commit.

## 2. Scope

In scope:

- The initial identities and counts in the architecture verdict's Normative
  Initial U1 Evidence Fixture (NIU1EF), followed by every reviewed append-only
  remote delta incorporated into the latest lock-derived historical scope and
  evidence inventory. Drafts, checksums, and SBOMs never qualify as executable
  official producers.
- Historical SQLite, legacy/local Dolt, server Dolt, embedded Dolt, and
  current-format workspaces, including applicable shared, remote, and proxied
  topologies.
- Current Dolt, SQLite, PostgreSQL, and MySQL providers for fresh-create,
  defensive-open, provider-local schema upgrade, and no-op behavior.
- The already admitted embedded-Dolt-to-PostgreSQL backend migration.
- Direct installation and every maintained v1.2 distribution channel whose
  artifacts can create or open a workspace.

The historical topology/build inventory is explicit, rather than inferred from
the current defaults. For every tag revision it accounts for:

- repository-local standalone workspaces and every shipped build flavor;
- redirected or shared workspaces, including the historical `BEADS_DIR`,
  worktree, and shared-server forms that the release could create or open;
- remote or server-backed stores; and
- proxied-server workspaces.

Each tag-revision/variant pair is either a mandatory case or carries pinned
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

1. A reviewed immutable snapshot and generated compact release lock contain
   every frozen `tag_name` once and every public `(tag_name,
   peeled_commit_oid)` revision once across every independently discovered
   public ref source, plus every retained superseded revision. Authoring-time
   live discovery on 2026-07-22 found 173 names and 178 currently visible
   public revisions across `origin` and `groblegark`, with divergent public
   revisions for `v0.58.0`, `v0.59.0`, `v0.60.0`, `v0.61.0`, and `v0.62.0`;
   adding the superseded first `v1.1.0` revision yields the 179-revision
   NIU1EF. U1 repeats discovery and freezes the exact inventory; later reviewed
   remote deltas append, and the latest lock-derived scope and evidence totals
   are authoritative. Counts are derived assertions, never generator
   constants. Every historical smoke, producer,
   variant, probe, and evidence reference uses `tag_revision_id`, never a tag
   string as sufficient identity. Offline validation performs two independent
   snapshot-to-lock generations in separate temporary output roots under
   OS-level network denial, with distinct empty/isolated `HOME`, `TMP`, `TEMP`,
   `TMPDIR`, `XDG_CACHE_HOME`, `GOPATH`, `GOMODCACHE`, and `GOCACHE`, plus
   `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`, and `GOENV=off`; the two
   output trees and each tree versus the checked locks compare byte-for-byte.
   Ordinary validation never executes historical binaries, providers, package
   managers, or network requests.

   Every repository observed by discovery is a versioned `RefSource` record,
   not an unlabelled remote URL. Its content-derived ID binds canonical
   repository identity, repository class (`authoritative`, `maintainer_archive`,
   or `unverified_fork`), authority rationale, discovery method/query, capture
   time, raw-evidence locator/digest, and authenticity proof such as immutable
   GitHub repository ID/ownership lineage plus applicable signed-tag/release/
   workflow evidence. Every ref/revision observation references one
   `ref_source_id`. Conservative extra revisions from an unverified fork remain
   mandatory compatibility rows, but neither they nor their results may support
   an "authentic canonical release" claim until reviewed authenticity evidence
   promotes that source.

   The generator takes one explicit content-addressed input root containing raw
   `ls-remote` bytes; base and peeled ref records; raw tag-object and commit-
   object bytes/headers, signature material, parent/tree IDs, and ancestry proof;
   and exact source-archive/tree identities. It must neither discover nor read an
   ambient `.git` directory or object database. Determinism runs outside any
   repository with `GIT_DIR` and `GIT_WORK_TREE` unset, `LC_ALL=C`, `TZ=UTC`,
   `umask 022`, and a reviewed absolute `PATH`/tool-manifest digest in addition
   to the isolated environment above. Historical source acquisition/build is a
   separate network-enabled producer step and may consume only the pinned source
   archive/tree identities emitted by the lock.
2. Frozen scope is independent of the separately checked current-origin
   observation. Each revision binds immutable raw ref, optional annotated tag-
   object, peeled commit, tree, and canonical source-archive observations with
   source URI, capture time, and raw-observation digest. NIU1EF retained and
   superseded states remain historical provenance, never current-origin delta.
   Each decisive `v1.1.0` workflow observation locks repository, run ID,
   event/full ref/head SHA, attempt, status/conclusion, timestamps, source URI,
   raw bytes/digest, and ordered publication-job IDs, conclusions, timestamps,
   source URIs, raw digests, and dispositions. Every `v1.1.0` producer
   references the applicable run/job evidence; offline generation resolves the
   association only through those references and contains no hardcoded mapping.
3. Online reconciliation preserves base and optional `^{}` targets, validates
   lightweight versus annotated refs, and classifies additions, deletions, and
   moves against the prior reviewed current-origin observation. A ref-kind or
   raw-target change is a move even when the peeled commit is unchanged; a new
   peeled commit appends a mandatory revision without replacing the old one.
   Release/asset reconciliation uses the same model. U8 also captures raw live
   observations for every maintained or reviewed-excluded external install
   channel, including Homebrew, AUR, Winget, and every other U1-discovered
   surface. Every delta is reported;
   reviewed deltas append to the ledger and next lock, while U8 requires the
   unreviewed-delta set to be empty. U8 freezes the exact reconciled
   current-origin, release/asset, and external-channel observation envelopes
   and their internally verified raw digests. U9 and U10 each acquire a fresh
   independent raw envelope, validate its own bytes against its own recorded
   digest, normalize the modeled state, and require zero modeled semantic delta
   from U8. Capture timestamps, JSON serialization, and therefore raw envelope
   bytes/digests may differ across checkpoints. Unless a publication input is
   proved immutable, every checkpoint repeats origin tag, release, asset, and
   external-channel observation. A tag addition,
   deletion, or move; release replacement or deletion; asset name, size, or
   digest mutation; visibility/publication change; channel version/revision/
   digest/publication-state change; internal raw-digest mismatch; or inability
   to complete the live check fails closed. A reviewed deletion updates current observation but
   never shrinks frozen history. Distinct fixtures and
   diagnostic classes cover missing/moved/extra tag refs, deleted release,
   changed asset name, size, or digest, draft/public transition, and a competing
   non-draft release.
   U8 also repeats source discovery and refreshes/diffs every authoritative and
   archival `RefSource` record, including repository identity, classification,
   authority rationale, authenticity proof, raw capture, and content digest.
   Missing sources, changed authority evidence, newly discovered sources, or a
   changed unverified/verified classification are explicit reviewed deltas; they
   cannot be hidden by an unchanged tag-name set.
4. The initial release, visibility, asset-role, `nosqlite`, and `v1.1.0`
   producer classifications equal NIU1EF; the current authoritative inventory
   is the latest reviewed append-only lock. Draft assets always have
   `official_producer_eligible=false`, and checksums/SBOMs remain provenance
   rather than executable official producers.
5. Workspace family belongs to a concrete tag-revision variant, not globally
   to a tag. A pure generator extracts branch-aware capability fingerprints
   from exact raw source/configuration blobs and matches every variant to
   exactly one reviewed family profile. Transition evidence names the actual
   change commit and exact parent, before/after fingerprints and witness blobs,
   plus proved ancestry; incomparable branches require exact fingerprints.
   Tag adjacency, semver ranges, regex guesses, and implicit complements are
   never family or coverage authority.
6. Each producer is a discriminated official-archive or source-build record.
   Source builds pin exact source commit/tree and raw `go.mod`/`go.sum` hashes,
   target/flavor, canonical argv/environment/tags/ldflags/output, Go
   distribution URL/version/SHA-256, executor image or VM digest and software
   manifest, CGO compiler/sysroot digests when applicable, and every referenced
   release/workflow/helper blob. `resolution_state=resolved` requires all
   applicable pins and empty `missing_fields`; `resolution_state=unresolved`
   requires a nonempty raw-UTF-8-byte-sorted unique `missing_fields` list and
   actionable `reason` while remaining mandatory. Official archives use
   structured `not_applicable: prebuilt-official-bytes` tool inputs, which are
   compatible with `resolved` and are not unresolved. `unknown`, `latest`,
   mutable runners, or derivation from mutable metadata are invalid. U1 fails
   on any unresolved producer, and U2/U5/U6/U8 refuse to select, execute, or
   freeze it.
7. The generated variant ledger partitions every explicit
   tag-revision/producer/platform/build-flavor/topology/`engine_runtime_id`
   identity as mandatory or
   proposed-inapplicable. Proposed inapplicability expands to a canonical
   resolved-probe stream whose identity includes that same
   `engine_runtime_id` and complete create/open contract. Each
   `probe_digest` is `SHA256("beads-u1-resolved-probe-v1\0" ||
   RFC8785(spec_without_probe_digest))`; a complete `leaf` is the RFC 8785
   encoding of that spec including its verified digest. Reject duplicate
   `probe_id` values. Sort leaves by the raw UTF-8 bytes of unique `probe_id` and
   prefix each leaf with its byte length as an unsigned 64-bit big-endian
   integer. The checked-in lock stores leaf count and
   `SHA256("beads-u1-resolved-probe-set-v1\0" ||
   concatenated_length_prefixed_leaves)`.
   U2 re-expands the stream and binds evidence to `probe_id`, `probe_digest`,
   and historical binary SHA-256. Missing or mismatched evidence cannot remove
   a mandatory identity; expanded leaves are reviewable on demand but are not
   checked in.
8. A provider/topology lock is independently and bijectively derived from
   provider constants and validation, init and open paths, CGO/non-CGO store
   factories, storage implementations, and lifecycle dispatch. It freezes the
   exact discovered set rather than an asserted tuple count. The one canonical
   ordered tuple has exactly nine fields: `(provider_id, access_path,
   store_scope, lifecycle_owner, endpoint_kind, proxy_upstream, build_variant,
   platform_id, engine_runtime_id)`. Its versioned JCS identity hashes all nine.
   `platform_id` alone binds GOOS/GOARCH. Independently,
   `engine_runtime_id` binds distribution, exact server/binary version or image
   digest, protocol, canonical configuration digest, and its semantic envelope,
   including collation/charset/time-zone behavior and an authoritative
   supported-version envelope with explicit minimum/boundary witnesses. Every
   embedded/in-process row uses the literal
   `engine_runtime_id=embedded/no-external-runtime` sentinel. Runtime boundary
   and equivalence cells are derived only from exact source evidence, never
   inferred from semver or platform. Unsupported platforms use a
   reviewed exact platform-inapplicability record rather than disappearing. Every
   reachable selector, including `--shared-server`, `dolt.shared-server`, and
   the shared-server environment selector, maps to exactly one identity with a
   source witness. The denominator covers SQLite when present; Dolt embedded,
   owned per-workspace server, shared server, externally managed server, and
   proxied surfaces; and every discovered PostgreSQL/MySQL topology. Its
   operation/outcome matrix contains exactly `init_workspace`,
   `construct_store_generic`, `open_store_read_only_factory`, and
   `open_configured_cli_uow` per tuple, with separate lifecycle applicability
   witnesses. Each cell has its own exact source route and either an authentic expected success
   or a typed proposed-inapplicability that remains mandatory until executed
   evidence is pinned. Absent contrary evidence, CGO changes only embedded Dolt:
   non-CGO embedded rejects, while direct server Dolt and SQLite/PostgreSQL/
   MySQL work in both builds. Proxy init/generic/read-only reject and configured
   CLI/UOW open follows its dedicated path. Missing, extra, duplicate, multiply mapped,
   route-disagreeing, outcome-disagreeing, or unproved cells fail U1/U6/U8. Two
   rows differing only by `engine_runtime_id` remain distinct; a mutation that
   conflates them or deletes either row fails the denominator.
   A separate bijective lifecycle-capability lock crosses every planner route ×
   topology × `engine_runtime_id` × build/platform identity with
   `inspect`, `quiesce`, `snapshot`, `prepare`, `verify`, `activate`,
   `final_verify`, `resume`, and `restore`. Each cell has executable source/
   interface ownership evidence and an authentic success or typed missing-
   capability outcome. Every mandatory missing cell creates the exactly owned
   dynamic U4 or upstream blocker and directly blocks U8; no four-operation
   provider row can stand in for this lifecycle proof.
9. Install discovery is likewise count-free and exhaustive over repository,
   documentation, release automation, and live external catalog surfaces. It
   classifies every install-looking surface, including Winget, as
   `candidate_executing`, `alias`, `non_cli`, `dormant_unpublished`, or
   `retired`, with immutable evidence and drift detection. The execution case
   ID is a versioned JCS digest of `(surface_id, branch_id, platform, arch,
   manager_mode, materialization_id)`, not merely a channel name. Direct archive, shell-installer release, PowerShell-installer release,
   and mise packaged branches use `packaged_bytes`; npm and bun wrapper branches
   use `wrapper_contains_packaged_bytes`. Source-derived branches include shell
   CGO go-install, shell non-CGO go-install, shell source clone, PowerShell Go
   install, PowerShell source clone, Homebrew, direct Go CGO/non-CGO, canonical
   source, AUR, and Nix, subject to source verification. Every reachable branch
   executes authentically or has pinned inapplicability; no packaged-only
   assertion may hide a reachable installer fallback. Missing, extra, stale,
   duplicate, multiply mapped, or relation-disagreeing identities fail
   U1/U6/U8. Every `public_latest_only` row additionally names its activation
   mechanism and accountable owner, prerequisite receipt, credential reference
   and class (never secret material), bounded propagation window, budgeted
   retry/backoff policy, expected public selector, and channel-specific rollback/
   quarantine/escalation action. An absent field makes the row blocked or
   inapplicable only through reviewed classification, never silently skipped.
   Prior reviewed inapplicability removes the runtime obligation; a reviewed
   blocked row blocks publication before selector execution and never becomes a
   runtime skip.
   Before publication, U9 executes every reachable branch only
   through frozen injectable or staging inputs against the exact U8 bytes; it
   cannot claim that a public `latest` registry or package-manager route selects
   an unpublished candidate. Qualification publishes nothing. After a human-
   authorized publication, the U7-owned release molecule/workflow executes the
   lock-derived activation DAG, requires every prerequisite receipt, then runs
   every U1-locked `public_latest_only` case through its authentic public
   selector and records those results separately; that receipt can never
   satisfy or repair U9.

### R2. Authentic historical workspaces

1. A run-level producer fan-in keyed by exact peeled source commit, platform, build
   flavor, recipe digest, and toolchain digest must fetch or build each unique
   historical artifact exactly once, verify it, and fan out immutable bytes to
   all consuming rows. Official-artifact producers use structured
   `not_applicable: prebuilt-official-bytes` recipe and toolchain values plus
   their artifact checksum. Every consumer remains bound
   to its `tag_revision_id`. Row workers may retrieve only
   the workflow-internal, digest-addressed producer bundle; they never fetch from
   external origins or compile. Each historical row uses the fan-in-selected exact binary to
   initialize and mutate a nonempty isolated workspace.
2. Synthetic schemas and hand-authored fixtures may support unit tests but do
   not count as release qualification.
3. Each terminal result embeds `tag_revision_id`, `engine_runtime_id` (including
   the explicit `embedded/no-external-runtime` sentinel), source tag/commit/tree and
   source binary digest or fully resolved build-recipe/toolchain identities;
   candidate commit/tree and one `candidate_set_digest`; harness/helper tree;
   OS, architecture, executor/container identity, relevant environment
   allowlist; external Dolt binary/version/digest or version-gated `N/A`; and
   origin plus digest for every downloaded input. It also embeds exactly one
   `candidate_execution` variant: `packaged_bytes` carries the manifest-selected
   `candidate_artifact_digest`; `wrapper_contains_packaged_bytes` carries the
   wrapper digest and proves the recovered executed payload has that digest; or
   `frozen_source_derivation` carries frozen-source and derivation-recipe
   digests, executor/toolchain/compiler/sysroot pins as applicable, and the
   realized output SHA-256 plus verified embedded version. The last variant must
   not claim GoReleaser byte equality. An aggregate-side join is not a substitute for per-result
   provenance.
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
6. Every provider open, including `NoOp` and nominally read-only opens, must
   consume the opaque inspection witness and expected route produced by the
   planner. The factory may not reselect a provider, route, or topology and may
   not provision from reread configuration. Before any open-time effect, one
   adapter/driver-owned atomic capability validates the witness's raw-config,
   route, topology, source-identity, and capability digests and either opens that
   exact route or returns typed `replan_required`/`open_refused`. A changed input
   has zero open/provision/version/telemetry/metadata effects. A missing atomic
   validation/open primitive is an owned U4/upstream capability blocker, never a
   best-effort Beads check.

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
3. A manual route that needs a missing capability must fail closed and block
   only that route. Executable interface/adapter evidence classifies ownership
   before an issue is routed: a primitive implemented by a Beads-owned
   `internal/storage` adapter or topology (including SQLite, PostgreSQL, MySQL,
   server-Dolt, and proxy adapters where applicable) produces a real dynamic U4
   route child in this epic; only a primitive demonstrably owned by the
   `dolthub/driver` interface or implementation produces a linked upstream
   driver issue/PR dependency. Both child kinds directly block U8. Beads never
   patches around a driver-owned boundary. Catalog, harness, current-provider,
   and unrelated historical E2E work continue and emit their own terminal
   results. The affected mandatory row executes and reports `FAIL`, never
   `SKIP` or no result; only the final smoke `N/N` plus focused `M/M` barrier
   waits for every route to pass.
4. Routes requiring transformation prepare side-by-side, preserve the original
   as authoritative until independent verification passes, and activate the
   verified target last.
5. Interrupted execution must deterministically resume or restore through the
   same driver-owned lifecycle. At every point exactly one store is
   authoritative. A successful rerun is `NoOp`.
6. Each mutating operation has discoverable, authenticated, durable driver- or
   adapter-owned operation state. The storage-neutral envelope contains an
   operation ID; source, target, and canonical-plan digests; monotonically
   fenced authority epoch/token; authenticated state digest and CAS revision;
   and the current persisted lifecycle boundary. Read-only recovery discovery
   returns the opaque driver handle plus that generic envelope without engine
   introspection. Every transition is compare-and-swap, every mutating call
   presents the current fencing token, and a stale actor is rejected before an
   effect or authority change.
7. Failure injection persists and rediscovers state at every boundary from
   quiescence/snapshot through prepare/verify/activate/final-verify and every
   resume/restore transition. Resume and restore are idempotent for the current
   authority and reject foreign plan/source/target digests. Any adapter or driver
   that cannot authenticate, discover, fence, or CAS the required state produces
   a route-owned dynamic blocker that prevents U4/U8. Beads must not add a
   storage-specific recovery table, flock, engine query, or inferred-completion
   substitute above the interface.

### R6. Fidelity and safety oracle

U1 must independently derive a versioned semantic-surface catalog for every
exact `tag_revision_id` from three separately enumerated inputs in its pinned
source archive: schema migrations, public issue/types and storage interfaces,
and CLI create/read/mutate/export command surfaces. The proposed catalog is
bijective with that discovery. Every discovered concept records durability
(`durable` or proved `cache_ephemeral`) and, for every durable concept, exactly
one disposition: `preserve`, `intentionally_recompute` with a verified
postcondition, `reviewed_retire` with accountable rationale/migration guidance,
or `historically_absent` with exact source evidence and a negative historical-
binary probe. Cache/index/temp/telemetry ephemera are explicitly evidenced and
excluded; they may never be silently treated as durable preservation duties or
used to excuse loss of durable state.

The catalog explicitly enumerates every issue field and all version-present
events; wisps and relations; labels/comments/dependencies/readiness; custom
statuses and custom types; repository/workspace configuration; counters,
snapshots, and compaction state; routes, federation, and interactions; and
tombstone/deletion semantics. A category is present only where independently
discovered for that exact revision; broad era or semver inference is invalid.
Deletion, duplicate mapping, unclassified durable concepts, wrong durability,
changed disposition, absent negative probe, or a newly discovered concept not
in the catalog fails U1. U2 creates/mutates and records every applicable catalog
concept, the independent R6 oracle consumes the same IDs without trusting the
production migration path, and U8 rederives and freezes the complete catalog
and evidence digest before candidate construction.

The pre/post oracle must independently verify every catalog concept supported
by the source generation. Its version-gated feature matrix and every deep U2
result enumerate, without catch-all shorthand:

- issue ID, title, description, status, priority, issue type, every
  source-supported timestamp, assignee, owner, external reference, and custom
  metadata;
- events; wisps/relations; label values; comment bodies/authors/timestamps;
  dependency endpoints/types; blocker/readiness behavior; custom statuses/types;
  config; counters/snapshots/compaction; routes/federation/interactions;
  tombstone/deletion where present; and all applicable semantic counts;
- workspace and repository identity plus applicable branch, remote,
  redirected/shared-store, server, or proxied topology and exact runtime-
  boundary/equivalence semantics; and
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

1. `smoke_row_id` is the exact tag-revision/producer/topology/platform/build-
   flavor/`engine_runtime_id` identity. Every mandatory
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
   `covered_identities` lists its traced tag-revision/producer/topology/platform/
   build-flavor/`engine_runtime_id` identities as metadata only; it
   never emits, reuses, satisfies, or duplicates a `smoke_row_id` result.
4. Aggregation independently validates the complete `smoke_row_id` denominator
   and the complete `focused_case_id` denominator. `SKIP`, warning-only,
   timeout, unavailable artifact, unsupported platform, blocked dependency,
   indeterminate probe, unknown coverage reference, missing, extra, or duplicate
   result is failure in its namespace. Every terminal result contains exactly
   one of `smoke_row_id` or `focused_case_id`; both or neither is failure.
   Progress reports both `passing smoke rows / total smoke rows` and
   `passing focused cases / total focused cases`, lists failures from each, and
   passes only at exact `N/N` and `M/M`. Sharding and aggregation preserve the
   complete runtime-bearing identity; two rows differing only by
   `engine_runtime_id` may not share a smoke result, and deleting either fails.

### R8. Exact candidate and release gate

1. Candidate-build pipeline implementation is completed before final candidate
   construction. Immediately before freezing, read-only authoritative discovery
   reports every current-origin/release addition, deletion, and move against the
   prior reviewed observations, requires the unreviewed-delta set to be empty,
   and revalidates the latest reviewed lock-derived tag-name/tag-revision scope
   and evidence totals after every append-only reviewed delta. It also validates
   release records/assets and roles, every external-channel observation,
   provider/topology/build, maintained-install-channel, and focused-case
   denominators against the locks. Retained/superseded historical absence is not
   a delta; any unreviewed drift returns to reviewed source work instead of
   shrinking or silently extending the epoch. Before this no-source barrier,
   U7 owns the reviewed source finalization from 1.1.0 to exactly 1.2.0: expand
   `scripts/check-versions.sh` under RED/GREEN/refactor so its inventory is
   bijective with every surface changed by `scripts/update-versions.sh` and the
   previously required Copilot plugin, `default.nix`, Windows JSON/XML resource
   fields, README release policy, and `integrations/beads-mcp/uv.lock`. U7 also
   derives and reviews named `version_date` immediately before U8. It is the
   intended UTC publication date in strict `YYYY-MM-DD` form, not this plan's
   authoring date. U7 finalizes a dated `## [1.2.0] - <version_date>` entry in `CHANGELOG.md` and the
   exact newest `cmd/bd/info.go` `versionChanges` entry with
   `Version: "1.2.0"`, `Date: "<version_date>"`, and reviewed upgrade/release
   guidance; tests exercise both text and JSON `bd info --whats-new`. It then
   runs `./scripts/update-versions.sh 1.2.0` with missing/multiple-substitution
   failure, refreshes the pinned MCP lock, and proves every checked source,
   packaged, wrapper, and source-derived version probe reports exactly
   `v1.2.0`. The checker requires all full-semver fields to equal `1.2.0`, PE
   numeric fields to equal `1.2.0`, XML numeric fields to equal `1.2.0.0`, the
   MCP local lock entry to equal `1.2.0`, the first versioned changelog heading
   to equal `1.2.0`, and `versionChanges[0]` version/date to match the binary and
   changelog. U8 freezes `version_date`. Malformed/mismatched values fail; if
   publication slips past that UTC date, the epoch is stale and must restart
   with reviewed source bytes. The deprecated failing `scripts/bump-version.sh`
   and every source-changing release-prep mode are unusable after U8.
2. The freeze barrier resolves the current
   `feature/backend-provider-change-20260713` remote head after every prerequisite
   PR has merged, requires that exact commit to be the frozen commit, and records
   its tree plus a finalized freeze-record digest covering every production,
   packaging, fixture, semantic-oracle, qualification-workflow, release-gate
   helper, denominator/recipe, workflow, and build-input byte, plus the exact
   reconciled current-origin, release/asset, and external-channel raw envelopes
   and internally verified digests used as the post-freeze modeled-state
   comparator. Every discovered route child must
   directly block this barrier.
3. Only after the freeze record is finalized may the pipeline build and package
   one exact multi-platform candidate set from that immutable commit/tree and
   consume the freeze-record digest as a hard stage input. Its immutable
   manifest bytes contain the packaged artifact inventory, frozen source and
   candidate-execution contracts, and have one SHA-256
   `candidate_set_digest`; each packaged platform/build entry has its distinct
   `candidate_artifact_digest`. A
   missing/mismatched record, source checkout mismatch, or movement of the
   target ref before eligibility aborts the epoch. Every shard receives the
   identical manifest and inventory, and each row uses only its manifest-bound
   `candidate_execution`. Packaged and wrapper cases execute the exact selected
   packaged bytes. At U8, a source-derived branch binds and verifies frozen
   source/ref/recipe/executor/toolchain/compiler/sysroot contracts as applicable
   plus expected embedded-version inputs for exact `v1.2.0`; no unrealized
   output digest or embedded version is required. Every packaged and wrapper
   probe nevertheless reports exactly `v1.2.0` at U8. Pipeline tests may use disposable candidate sets
   before the barrier; none counts as the final candidate.
4. Qualification is execution-only with respect to the frozen tree, helpers,
   manifests, fixtures, oracles, workflows, build environment, and candidate
   contracts. Packaged cases execute packaged bytes, never `go run`, an ambient
   `bd`, an in-tree substitute, or a per-shard rebuild. Wrapper cases must
   recover those exact packaged bytes. Only locked source-derived channel cases
   may compile, through their fully pinned derivation, and their realized bytes
   are materialized once per derivation identity before consumer fan-out. U9
   emits one immutable canonical materialization inventory whose key is
   bijective with the frozen derivation identity and whose value is exactly one
   realized-output SHA-256 plus exact `v1.2.0` embedded version. Consumers
   reference that inventory; the aggregate rejects a rebuild, missing/duplicate
   identity, or consumer disagreement, and U10 validates the inventory digest.
   Only packaged manifest entries carry `candidate_artifact_digest`.
   Every maintained installer branch is exercised prepublication through its
   frozen injectable/staging route against these exact U8 bytes. A passing U9
   row proves that frozen route, not that any live public-latest resolver has
   already selected v1.2.0.
   Immediately before aggregation can be accepted, U9 also performs a new
   read-only current-origin, release/asset, and external-channel observation,
   internally validates its raw bytes/digest, and reconciles its normalized
   modeled state against U8's frozen observation. Any unreviewed delta invalidates the epoch even when all rows
   passed. It never rewrites the frozen observation, denominator, or scope in
   place; the only recovery is reviewed source/lock work followed by a new U8
   and full U9.
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
   epoch. No frozen source, candidate, or publishable-inventory byte may change
   after U8. Ephemeral consumer verification outputs are governed below.
   Post-freeze current-origin, release/asset, or external-channel drift likewise
   invalidates the epoch and is never absorbed by rewriting its frozen scope.
7. U10 first runs a strictly read-only evaluator over the frozen manifest,
   qualification results, materialization-inventory digest, exact `N/N` and
   `M/M` denominators, and a fresh internally digest-validated live
   reconciliation. It emits one canonical `EligibilityDecision` containing the
   evaluated input digests, target/freeze/baseline identities, counts, outcome,
   reasons, evaluator version, and `decision_digest`. This schema contains no
   bundle digest, locator, expiry, seal receipt, claim, or publication state.
   Drift, indeterminate observation, or any non-pass result is ineligible and
   returns to reviewed source work plus a new U8.

   Only after an eligible decision is final may U10 invoke U7's already-shipped
   bundler as a separate append-only output operation. Canonical bundle bytes
   bind the `decision_digest`, U8 freeze/candidate/publishable-artifact locators,
   U9 results/materialization inventory, target/baselines, `version_date`, and
   channel lock; they do not contain their own digest or receipt. A distinct
   `BundleSealReceipt` then records `decision_digest`, bundle/artifact
   digests, authenticated locator, signing identity, `sealed_at`, 24-hour
   `claim_expires_at`, and retention policy. This split forbids bundle self-
   reference. Evaluation remains read-only; sealing may only create the bundle
   and seal-receipt outputs and changes no source, candidate, qualification
   input, tag, release, registry channel, or maintained distribution channel.
8. U7 implements the production bundle path with the already supported
   `actions/upload-artifact@v4` backend. The authenticated locator is
   `github-actions-artifact://gastownhall/beads/runs/<run_id>/artifacts/<artifact_id>`
   plus the API-reported SHA-256; the upload uses `retention-days: 90` and fails
   unless the reread artifact `expires_at` supplies at least 90 days from seal.
   `actions/attest@v4` signs the bundle SHA-256 as a custom in-toto/DSSE predicate
   using GitHub Actions OIDC and the GitHub/Sigstore public-good Fulcio/Rekor
   trust chain. Verification uses a pinned upgraded `gh` supporting
   `gh attestation verify`, pins repository, exact signer workflow/ref, frozen
   source commit, subject digest, and GitHub-hosted runner, and records trust-root
   version; rotation requires a reviewed trust-lock delta. The bundle-writer
   reference is `github-actions://v12-upgrade-qualification/GITHUB_TOKEN` with
   only `contents:read`, `actions:read`, `id-token:write`,
   `attestations:write`, and `artifact-metadata:write`.
   Publication uses
   `github-environment://beads-release/GH_RELEASE_APP_INSTALLATION_TOKEN`, a
   dedicated GitHub App installation token with `contents:write` for protected
   claim/release tags and assets, `actions:write` for the
   `return_run_details` dispatch, and `administration:write` only for temporary
   exact-ref ruleset create/read/delete. The environment-gated workflow must not
   use those permissions for any other repository mutation; the attestation job
   remains on the separate least-privilege `GITHUB_TOKEN`/OIDC identity above.

   GitHub artifacts and attestations make substitution detectable but are not
   WORM: repository control-plane actors can delete them and the public-repo
   artifact ceiling is 90 days. The contract therefore requires availability
   for 90 days from seal, then copies the identical bundle and signed receipt
   chain to the stable release assets and operationally retains those until
   `max(manual_root_landing + 90 days, every channel rollback deadline)`.
   Missing/deleted bytes fail verification; no undeletability claim is made. If
   literal object-lock availability is later required, a separately provisioned
   and reviewed external object-lock backend is a pre-U8 human prerequisite,
   not a service invented inside Beads.

   Before U8, a production capability preflight proves artifact upload/download,
   API SHA-256/expiry, attestation verification, exact OIDC/trust policy,
   credential permissions, release-environment gates, retention handling, and
   the conditional claim path. Canonical `ClaimIntent` bytes bind version,
   decision/bundle/guard digests, `github.run_id`, `github.triggering_actor`,
   authority, claim expiry, and nonce, and explicitly exclude every attestation
   locator/digest and envelope field. The workflow hashes and attests those
   bytes under the exact repository/workflow/source-SHA/run policy. Only after
   that attestation exists does a distinct canonical `ClaimEnvelope` and the
   annotated claim-tag message bind `claim_intent_digest` plus the resulting
   attestation locator/digest. The claim primitive then uses
   `POST /repos/gastownhall/beads/git/tags` for that envelope followed by
   `POST /repos/gastownhall/beads/git/refs` atomically creates unique
   `refs/tags/beads-release-claims/v1.2.0`. HTTP 201 wins; 409/422 reads the ref/
   tag and separately verifies the envelope, the attestation subject digest
   against `claim_intent_digest`, and every canonical intent field before
   permitting only the identical run ID/triggering actor/authority/bundle/nonce
   to resume with a later `github.run_attempt`. Recovery repeats those three
   checks after each crash boundary: intent persistence, attestation creation,
   tag-object creation, and conditional ref creation. A foreign actor or other
   mismatch is replay and fails. The
   ref is never updated, deleted, or reclaimed; an expired claim requires a new
   reviewed epoch/version. Dedicated claim-tag rules permit creation only by the
   release App and prohibit operational update/deletion. Missing capability,
   too-short expiry, missing bytes, or unverifiable attestation blocks U8.
9. The concrete source-branch freeze is a temporary exact-ref repository
   ruleset named `beads-v12-release-freeze`, created during the guarded release
   using `POST /repos/gastownhall/beads/rulesets`. The canonical request digest
   is locked before U8. It targets only
   `refs/heads/feature/backend-provider-change-20260713`, is immediately active,
   has `update` and `deletion` rules, and has an empty operational bypass-actor
   list. The same environment-gated
   `github-environment://beads-release/GH_RELEASE_APP_INSTALLATION_TOKEN` uses
   its `administration:write` permission only for this exact ruleset's create,
   read, and delete calls; Actions `GITHUB_TOKEN` cannot alter the ruleset.
   U7 fixture-repository tests prove create/effective-rule/delete behavior. U8
   read-only preflight fails until an accountable human has provisioned the
   `beads-release` environment (required reviewer, prevent self-review, and
   `can_admins_bypass:false` provisioned in the GitHub UI and verified by API),
   the dedicated release App, and split stable/claim tag protections
   that allow the release App to create but provide no operational update/delete
   bypass. Current observed state—unprotected target, no effective target rules,
   bypass-bearing `v*` rule, no release environment/credentials, tag-triggered
   rebuilding workflow, and seven-day artifacts—is explicitly failing evidence,
   not assumed capability. Actions concurrency is run serialization only and is
   never represented as a branch lock.

   After human approval and before the first publication mutation, the formula
   requires HTTP 201 from ruleset creation, captures its numeric ID, and polls
   both `GET /repos/gastownhall/beads/rulesets/{ruleset_id}`
   and `GET /repos/gastownhall/beads/rules/branches/{branch}` until the no-bypass
   update/deletion rules are effective, and records a signed
   `BranchFreezeObservation` with run/authority ID and ruleset/config digest. It rereads the branch with
   `GET /repos/gastownhall/beads/git/ref/heads/{branch}` and requires the frozen
   OID. Before each subsequent mutation it rechecks the ruleset/effective rules
   and ref; deletion, disablement, bypass/config drift, 403/404 indeterminacy, or
   target movement stops the workflow. GitHub supplies no atomic branch lease or
   transaction across these calls: repository/organization administrators are
   the explicit control-plane trust boundary, and the repeated equality check is
   observational rather than claimed atomicity. The protected annotated stable
   tag at the frozen OID becomes immutable release authority once created. With
   no approval/wait step the workflow
   reruns eligibility/live reconciliation, seals the guard, claims the bundle,
   and creates or verifies protected annotated `v1.2.0` at the frozen OID. After
   protected annotated tag creation, dispatch uses
   `POST /repos/gastownhall/beads/actions/workflows/{workflow_id}/dispatches`
   with `ref: v1.2.0` and `return_run_details:true`. Before deleting the
   temporary ruleset, the workflow rereads the returned run and requires
   `event=workflow_dispatch`, `head_sha` equal to the frozen OID, and the exact
   frozen release-workflow identity and blob OID/digest. The HTTP 200 run ID/
   URLs, explicit tag ref, event, head SHA, and workflow identity/blob are bound
   into a signed `DispatchAcceptanceReceipt` under an `actions:write` release-
   App permission. That receipt proves acceptance, not atomicity with ruleset/
   ref/tag. Only after it verifies may exact-ID ruleset deletion and audited
   absence proceed.

   The existing release formula/molecule checkpoint-and-resume mechanism remains
   authoritative for orchestration state and persists signed receipts for every
   tag, release, asset, and channel step. A same-claim/same-guard existing tag,
   release, asset, or channel state is digest-verified and treated as an
   idempotently completed step; a foreign or mismatched collision rejects.
   Crash restart discovers the durable claim and last receipt, rejects stale
   actors, and resumes the same authority without creating a second claim. The
   workflow never commits or pushes the source branch and `.github/workflows/
   release.yml` has no unparameterized `push.tags` trigger. It only promotes
   hash-verified U8 inventory. Post-U8 reconstruction/rebuild/repack/regeneration/
   replacement/promotion remains forbidden; locked consumer verification builds
   remain ephemeral and can never repair that inventory.
10. After artifact publication, the same claimed authority executes the complete
    U1 activation DAG before authentic public selectors. Each signed step receipt
    binds the claim/guard/candidate/tag/release, prerequisite receipt, selected
    credential reference/class, observed version/digest, and outcome. On terminal
    failure it compensates or quarantines every already activated channel in
    reverse activation-DAG order, recording each attempt; resume continues that
    compensation idempotently. It never moves/deletes/reuses the protected stable
    tag or rebuilds 1.2.0. Partial compensation leaves release/root incomplete
    and escalates to the accountable human. Prior reviewed inapplicability alone
    removes a runtime obligation; a reviewed blocked row prevents publication.
    This receipt cannot satisfy or repair U9. The exact `owned` root never auto-
    closes; the accountable human lands/closes only after all 11 macro children,
    U10 eligibility plus sealing, publication, and every applicable signed
    receipt pass. A no-publish decision is signed cancel/supersede, not success.
11. Live root reconciliation has two projections: exactly 11 runnable records
    and 22 static edges are reconciled from the active materializer payload,
    while `bd-ldt0f` is repaired separately before membership changes. The root
    discovery assertion is 162 recursive records: 11 mapped runnables plus 151
    legacy records partitioned exactly as 11 preserved, five adopted, one
    retained, ten deferred, and 124 superseded. The preserved set includes
    closed `bd-4velg`, a parent-child descendant under `.1` from previous plan
    SHA `b92f3957...`; omission, duplication, or the old 150 total fails.
    The root
    must have the exact title/body/design/acceptance/labels/metadata projection
    defined in `tasks.md`, custom type `convoy`, P0, preconditioned-open status,
    the exact `owned` label,
    and exactly 11 immediate `tracks` edges including `.14`; because convoy
    membership is the union of immediate parent-child children and `tracks`,
    every non-payload legacy immediate child is first detached/reparented and
    the effective `gc convoy status` member set must also be exactly those 11.
    Contributor row `.1.9` is retained top-level, not under the convoy. The CAS omits
    status and preserves pre-read assignee, notes, external reference, owner,
    and unspecified audit/storage fields exactly. Before any payload, root, or
    legacy reconciliation, separate city-HQ task `mc-ucid`, owned by the Gas
    City/Beads maintainer, must close. The HQ record is created and managed only
    in city context (`gc bd --city /data/projects/maintainer-city ...` without
    `--rig beads`); it remains outside `bd-ldt0f`, is never parented or tracked
    by the convoy, contributes no static DAG edge, and is not a twelfth member.
    Its closed state is an external reconciliation precondition.

    `mc-ucid` first schema-validates and atomically performs one reviewed edit of
    `/data/projects/beads/.beads/metadata.json`: only `.dolt_database` changes
    from `bd_metrics_repo_2424946071` to `bd`, and only `.project_id` changes from
    `8d69d5b6-0917-47a6-9761-db2b0dcca2fc` to
    `bafe313f-9fce-4972-849d-1f825740e9a5`. It must preserve
    `.database == "dolt"`, mode/backend/host/port, and every unrelated key byte-
    semantically, then reread and revalidate the exact projection before it runs
    exactly
    `gc rig --city /data/projects/maintainer-city set-endpoint beads --inherit`;
    the endpoint must be inherited `city_canonical`, never external. The
    accepted identity is city `/data/projects/maintainer-city`, rig `beads`, rig
    path `/data/projects/beads`, inherited endpoint `127.0.0.1:3307`, physical
    database `/data/services/gascity-local-dolt/bd`, and remote
    `git+https://github.com/gastownhall/beads.git`, together with the canonical
    database and project ID above. After repair, explicit-rig read-only context,
    root lookup, and recursive-subtree proof must all agree with that tuple.
    Environment overrides and raw SQL are diagnostics only and never an actual
    reconciliation bypass. Only after `mc-ucid` closes and those proofs pass may
    a noninteractive row CAS through the explicit Gas City `beads` rig convert
    the root and set exact row-owned fields plus metadata using per-key set/unset
    diffs. Current `bd`
    cannot CAS labels or parent/dependency relations; those run separately in
    an exclusive reviewed window with exact rereads before any
    `gc convoy add`. The current materializer's mapped-root metadata update does
    not satisfy this contract.

## 4. Non-Functional Requirements

- **Maintainability:** The release lock, route graph, result schema, and planner
  are small and reviewable. Test evidence is ordinary CI output and artifacts;
  no product-sized evidence subsystem is added.
- **Testability:** Planner and safety decisions are pure where possible. Driver
  contracts support deterministic failure injection at lifecycle boundaries.
- **Performance:** U1 freezes a nonempty measured `performance-budgets.lock.json`
  with runner image/hardware identity, CPU and filesystem calibration scores,
  sample count, raw and normalized baselines, and these initial hard ceilings:
  NoOp/read-only-open incremental p95 `max(25 ms, 15% of direct-open baseline)`
  and p99 `max(75 ms, 25%)` over at least 30 samples; representative PR lane 45
  minutes; nightly 8 hours; exact U9 12 hours; each row 20 minutes; each producer
  acquisition/build 30 minutes; at most 24 concurrent shards with at most 32
  smoke plus 16 focused cases per shard; 250 MiB uploaded per shard, 20 GiB
  uploaded per run, and 50 GiB cache working set; at most 1,500 GitHub API calls
  per full run and three external acquisition attempts per producer. The exact
  producer-keyed fan-in and source-derivation fan-in remain one materialization
  per identity. U6/U9 emit measured budget receipts and fail on timeout, byte,
  call, concurrency, or normalized NoOp regression. A budget/baseline change is
  an explicit accountable-human reviewed lock delta with rationale and expiry;
  no U8/U9 runtime waiver can turn a regression into pass.
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
| The first public `v1.1.0` revision and its replacement are selected | The full matrix runs | Both `tag_revision_id` identities execute; only `8e4e59d...` may use official assets and `71c8318...` uses a pinned source build. |
| One shard changes the candidate-set manifest, a packaged payload digest differs, or a source-derived channel uses an unpinned input | Results are aggregated | The whole qualification fails even if every functional assertion passed. |
| Origin exposes 121 base refs / 191 raw base-plus-peeled rows while the cross-remote initial fixture retains 179 revisions | Remote reconciliation runs | Units remain distinct; exact additions/deletions/moves are reported, retained/superseded history is not a delta, no lock row is rewritten, and freeze can pass only with an empty unreviewed-delta set. |
| A route capability is absent | U4 executes ownership classification | A Beads-adapter gap creates a dynamic Beads U4 route child; only a proven `dolthub/driver` gap creates an upstream dependency. Either child directly blocks U8 while unrelated rows still run. |
| The nightly workflow is inspected | Its `schedule` configuration is parsed by the workflow test | A UTC-daily trigger reaches the exhaustive every-tag job; deleting or retargeting it fails the test. |
| Any frozen helper byte changes after candidate construction | Qualification is requested | The old epoch is rejected and a new freeze/candidate/full-qualification epoch is required. |
| A public tag, release asset, maintained install channel, or target-branch head changes after the locks were last reviewed | U8 attempts to freeze | Read-only cutoff discovery detects drift and no candidate is built until the affected source work is reviewed and merged. |
| A tag moves, a release is replaced or deleted, an asset mutates, or visibility/publication changes during long U9 execution | U9 attempts to accept aggregation | Fresh read-only origin and release/asset reconciliation differs from U8's frozen observation, invalidates the epoch without rewriting its scope, and requires reviewed source work plus a new U8/U9. |
| Origin or release/asset state changes after U9 acceptance | U10 evaluates eligibility | The repeated reconciliation against U8's frozen observation reports ineligible and requires reviewed source work plus a new U8; completed results cannot grandfather the stale denominator. |
| A commit hook formats, fixes, or restages bytes after approval | The commit is created but not pushed | Its commit tree or first-parent diff digest differs from the approved index tree/diff digest; propagation stops and replacement unpushed bytes receive three fresh approvals. |
| A live public-latest channel serves the wrong revision after publication | The publication workflow runs postpublication smoke | The signed receipt records failure and invokes that channel's pinned rollback/quarantine/escalation; U9 remains unchanged and the root cannot close. |

## 6. Delivery Controls

- Architecture and design changes use `gpt-5.6-sol` with Ultra reasoning.
- Implementation uses `gpt-5.6-terra` with high reasoning, TDD, and isolated
  worktrees.
- Before every source commit, derive the digest directly from
  `git diff --cached --binary --full-index`, save
  those exact patch bytes immutably, and record the index tree from
  `git write-tree`. Record the resolved active hook path and digest as well;
  never assume a checkout-local hook ran. Exactly three independent
  `gpt-5.6-sol`/Ultra reviewers inspect those bytes; all three must approve
  with no unresolved Critical or Important finding. Recompute immediately
  before commit. After commit and before any push, require
  `git rev-parse HEAD^{tree}` to equal the approved index tree and require the
  SHA-256 of `git diff --binary --full-index HEAD^1 HEAD` to equal the approved
  staged-diff SHA-256, using the first parent for merges. Any mismatch blocks
  propagation; replacement bytes are restaged and receive three fresh reviews,
  and the commit is replaced or amended only while unpushed before both checks
  are repeated.
- Every nontrivial PR requires substantive accountable-human review before
  merge. Migration, schema, destructive-data, and sync paths never merge on
  bot-only approval.
- Every PR-producing live child targets
  `feature/backend-provider-change-20260713`, and its own acceptance criteria
  contain the staged-diff review, finding-resolution, fresh-review, label-event,
  and accountable-human gates.
- U4 reruns repository PR preflight and reviews maphew's open #4878 before any
  overlapping migration/schema work, preserves tests and attribution, records
  disposition, and never silently replaces or supersedes contributor work.
- Every program PR is created using standard `gh`/GitHub tooling with
  `status/needs-review-auto`, and the returned PR number/URL is captured.
  Verification checks that exact PR and walks its GitHub GraphQL
  `timelineItems` connection through every page until
  `hasNextPage` is false and finds the corresponding `LabeledEvent`; the query
  uses schema-valid Actor fragments, including `... on User { databaseId }`
  wherever identity is required. If automation consumes the label into
  `status/reviewing`, the historical event is sufficient and the intake label
  is never re-added.
- Before source work, U1, U2, U4, U5, U6, U7, and any macro child spanning more
  than five planned files or one independent subsystem must pour one bounded
  task-local molecule of ordered nested vertical-slice beads. Every slice names
  one end-to-end outcome, files, dependencies, acceptance and verification;
  starts with a failing RED behavior test; lands minimal GREEN; refactors only
  under passing tests; and receives independent review. Open/failed slices block
  their macro parent's closure. They are descendants of that macro only, are
  never parented/tracked by the root convoy, and cannot alter the exact 11 root
  immediate/track/effective members or 22 macro edges. The 162-record legacy
  assertion is the pre-slice reconciliation baseline. Slice materialization is
  one bounded setup pass with no meta-planning slices; once its schema/topology
  check passes, implementation begins.
- A deterministic trace checker parses every numbered R1-R8 clause and every
  task `requirements_trace`, expands ranges, and fails on an unknown reference,
  an untraced requirement, a task with no valid reverse trace, or a required U7
  omission of any R8.6-R8.10 clause. Its canonical bidirectional matrix is a
  reviewed U8 freeze input.
- No checked-in review/bootstrap controller, review-receipt or approval
  database, credential broker, commit/push wrapper, general-purpose product/
  runtime attestation schema or service, or PR-orchestration service is
  introduced. This does not prohibit U7's narrowly scoped canonical release-
  bundle schema or its signed/attested publication artifact.

## Architecture verdict

Build one storage-neutral upgrade planner/executor, backed by driver-owned migration operations, plus a black-box historical-release harness. Do not create 173 production code paths: production selects routes from probed storage generation, provider, topology, and schema version—not from the Beads release tag.

### 1. Architecture and module boundaries

Use four boundaries:

1. **`internal/storage` and `dolthub/driver`**
   - Own read-only probing, engine capabilities, quiescence, snapshots, transactional behavior, cutover, rollback, and verification.
   - Expose only storage-neutral state and capability results. Do not leak Dolt/PostgreSQL/MySQL internals into CLI or public SDK types.
   - Missing capabilities return a route-specific unsupported result.

2. **Internal migration core**
   - Maintains the small route graph and deterministic ordered plan.
   - Classifies each plan as `NoOp`, `StartupSafe`, `ManualRequired`, or `Unsupported`.
   - Coordinates driver operations but contains no engine inspection, flock implementation, generic retry loop, or crash-recovery framework.
   - Uses the same planner for startup and `bd migrate`; there must not be two migration implementations.

3. **Startup and `bd migrate` adapters**
   - Startup selects only `NoOp` or `StartupSafe`.
   - Existing `--inspect`, `--dry-run`, and `--yes` are sufficient; add no persisted plan format or approval database.

4. **Test-only release catalog and harness**
   - Catalogs tags/releases/assets and acquires historical binaries.
   - Runs old and candidate binaries strictly as black boxes.
   - Never becomes production code or a release/review controller.

The production route graph should normalize historical workspaces into format families such as legacy SQLite, legacy Dolt, server Dolt, embedded Dolt, and current provider-local formats. Every catalog row must map to a probed route; an unrecognized generation is a hard failure.

Provider-local schema upgrades and provider-changing migrations are separate concerns. The only admitted provider-changing route remains embedded Dolt → PostgreSQL. SQLite → PostgreSQL, MySQL conversions, PostgreSQL → Dolt, and arbitrary provider pairs remain unsupported.

### 2. Startup and manual-migration behavior

Every normal workspace open follows these invariants:

1. Probe is read-only and must not initialize storage, launch an engine, rewrite config, or partially open a legacy database.
2. Construct and validate the complete plan before the first mutation. Every
   mutating path, including `StartupSafe`, then acquires driver-owned
   quiescence, re-probes, recomputes the complete plan, and byte-matches source,
   capabilities, configuration, topology, and plan before its first write.
3. Startup may auto-apply only when every step:
   - stays on the same local provider and topology;
   - has fixed work independent of workspace cardinality;
   - performs no bulk scan, copy, backfill, remote coordination, or destructive rewrite;
   - is declared atomic or fully rollback-safe by the driver;
   - requires no snapshot/cutover protocol.
4. Index builds, data-dependent transformations, historical bridges, server/embedded transitions, provider changes, remote schema operations, and destructive steps are always manual.
5. A manual or unsupported plan makes startup exit nonzero before mutation and directs the user to `bd migrate --inspect` and `bd migrate --dry-run`.
6. After an automatic operation, re-probe and verify the exact schema expected by that packaged candidate before opening the workspace.
7. Failure leaves either the original workspace authoritative or the fully verified v1.2 workspace authoritative—never mixed state.
8. Re-running after success produces `NoOp`.

Manual UX:

- `bd migrate --inspect`: read-only source/target, selected route, safety class, prerequisites, and blockers.
- `bd migrate --dry-run`: performs every non-mutating preflight and prints the canonical ordered plan and verification criteria. It creates no lock, snapshot, config change, or storage write.
- `bd migrate`: prints that same plan and requires an affirmative TTY confirmation.
- `bd migrate --yes`: noninteractive explicit consent. Execution recomputes the plan after acquiring driver-owned quiescence and aborts if the source state changed.
- Snapshot, apply, verification, and cutover occur in that order where required.
  Configuration/topology/authority cutover is the final mutation and is followed
  only by read-only final verification.
- Any unsupported capability, unavailable dependency, failed verification, timeout, or indeterminate state is a nonzero failure.

No arbitrary row-count or elapsed-time threshold is needed: any work whose cost scales with workspace contents is manual.

### 3. Test tiers and authentic release coverage

**Unit and contract tier**

- Route selection and deterministic plan rendering.
- Startup-safety classifier.
- Read-only probe and fail-before-open behavior.
- Driver rollback/cutover and post-apply verification contracts.
- Unsupported-route isolation.
- Provider-local current schema routes for Dolt, SQLite, PostgreSQL, and MySQL.
- Embedded Dolt → PostgreSQL as the sole backend-changing route.

**PR representative E2E**

Run authentic, nonempty workspaces from:

- v0.49.6 SQLite;
- v0.55.4 and v0.57 legacy Dolt;
- v0.62 server Dolt, including public bridge/apply;
- v0.63.3 embedded Dolt;
- current-format workspaces for all four configured providers;
- embedded Dolt → PostgreSQL;
- fail-before-open and rollback fault cases.

Each historical binary must create and mutate its own workspace. The candidate
then inspects, migrates, reopens, and exercises every version-gated field,
relationship, readiness/topology semantic, count, and post-upgrade operation in
the normative R6 list; every cell is `supported` and executed or `absent` with
tag-bound negative historical-binary evidence. A second migration must be a
no-op. Synthetic schema fixtures alone are not authentic coverage.

**Nightly exhaustive tier**

A reviewed, generated catalog covers all 173 public `v*` tags, including stable, RC, and `nosqlite` tags. The 104 release records and 714 assets feed artifact selection and inventory; draft status never removes a public-tag row.

For each tag, use a verified official runnable asset when one exists. Otherwise
build the exact tag commit with its historically correct toolchain/build
flavor. Mandatory identities include tag, producer, topology, platform, and
build flavor. Failure to acquire, build, execute, or classify one fails its
mandatory row; it is never skipped or converted to a warning.

Before row fan-out, producer materialization fans in by exact tag commit,
platform, build flavor, recipe digest, and toolchain digest: each unique
historical artifact is fetched or built once, verified, and distributed through
workflow-internal digest-addressed transport. Row workers never fetch from
external origins or compile. Remote/server/proxy rows use row-exclusive
ephemeral endpoints and credentials, sanitized DSNs, normalized
production-endpoint rejection, and execution-time egress restricted to their
declared endpoints.

Direct results use
`smoke_row_id = tag/producer/topology/platform/build-flavor` exactly once per
mandatory identity. Deep, provider, install-channel, and fault results use the
separate `focused_case_id = suite/equivalence-class/fault-case` namespace.
Their `covered_identities` values are traceability references only and never
satisfy or duplicate direct smoke. The focused denominator is independently
derived from the feature/equivalence matrix, provider lock, install-channel
lock, and a checked-in focused-case registry. Report and require smoke `N/N`
and focused `M/M` independently.

The scheduled lane seals a disposable prequalification record and uses the
U5-built pipeline once before shard fan-out to produce a disposable candidate
set that is permanently ineligible for U8, release qualification, or
publication. Every shard verifies the same exact manifest bytes and
`candidate_set_digest`, then each row verifies and executes the
manifest-selected platform/build artifact by `candidate_artifact_digest`. No
shard rebuilds or substitutes candidate bytes.

**Release qualification**

After a final authoritative denominator refresh, freeze the exact remote
multi-provider target-branch HEAD and every source/helper/workflow/build input,
finalize a freeze-record digest, then package the v1.2 candidate set once from
that immutable commit/tree. Give every shard the identical candidate-set
manifest and immutable artifact inventory; each row executes exactly the
platform/build artifact selected by that manifest. Tests must
invoke the packaged executable, not `go run`, an in-tree binary, or a per-shard
rebuild. Qualification independently requires smoke `N/N` and focused `M/M`
with the full nonempty R6 oracle for all current providers. It yields technical
eligibility only; publication is a
later separate human action. Any input change or target-ref movement creates a
new epoch and requires the full gate again.

### 4. Dependency-aware epic

1. **U0 integrate accepted history — START NOW.** Merge `dce8d066d` into
   `af136f8857dd3e0461e06597f37e925088a98a49`; retain accepted tests plus later
   factories, lifecycle, and census behavior.
2. **U1 lock denominators — START NOW.** Account for all cutoff tags,
   releases/assets, authentic producer/topology/platform/build variants,
   immutable tag-bound negative-probe specifications, current providers, and
   maintained install channels.
3. **U2/U3/U5 source foundations.** After their stated prerequisites,
   materialize historical producers once, execute U1's applicability probes
   into a checked-in evidence sidecar, generalize the authentic harness,
   implement the unified planner/startup UX, and implement the one-build
   pipeline with disposable artifacts only.
4. **U4 storage lifecycle and route children.** Follow U3. Every mutating path
   uses the shared driver contract. A missing primitive creates a route-local
   child and upstream driver dependency. U4 may close after emitting those
   children; they block U8 directly but never U6.
5. **U6 qualification infrastructure.** Follow U2, completed shared U4, and U5;
   install representative
   and UTC-daily exhaustive lanes, and keep unaffected rows runnable.
6. **U7 read-only eligibility infrastructure.** Follow U5/U6 and finish all
   release-gate code before byte freeze.
7. **U8 freeze/build barrier.** Wait for U0–U7 and every discovered U4 route
   child, refresh cutoff denominators, freeze the exact target-branch HEAD and
   all inputs, then build the sole candidate once.
8. **U9 exact qualification.** Execute the frozen smoke and focused denominators
   without changing source/helper/candidate bytes.
9. **U10 read-only eligibility.** Consume U8/U9 and report eligible/ineligible;
   never tag or publish.

### 5. Accepted-chain and PR #4801 integration

Create the integration branch from `af136f…` and make a normal no-fast-forward
merge of `dce8d066d`. This preserves all accepted commit identities, authorship,
and reviewable history. Resolve conflicts so the accepted migration
behavior/tests coexist with the later multi-provider lifecycle and census
implementation.

Do not cherry-pick, squash, or merge PR #4801’s head: its tree is already represented by `2ef7c61e0`. Retain that accepted commit unchanged, link #4801 in the integration PR, explicitly credit the contributor, and explain the tree-equivalence when closing the contributor PR after integration.

Before each new source commit, derive the digest directly from
`git diff --cached --binary --full-index`. Exactly three independent
Sol/Ultra reviewers examine those bytes and all three must approve with no
unresolved Critical or Important finding. Recompute immediately before commit;
if fixes alter it, all three re-review the final bytes. Unavailable or
indeterminate review fails the gate. Every nontrivial PR also needs accountable
human review; migration, schema, and sync changes require substantive approval.

Create each PR with standard `gh`/GitHub tooling and
`status/needs-review-auto`, capture the returned PR number/URL, and fully
paginate that exact PR's GraphQL timeline to verify its `LabeledEvent`. If
automation has already changed it to `status/reviewing`, the original event is
sufficient—never re-add the consumed label.

### 6. Delete or leave out

Use standard Git and GitHub controls; add no review/bootstrap controller,
credential broker, commit/push wrapper, attestation schema, approval service,
Beads-side engine introspection, flocks, generic retries, crash-recovery
framework, or arbitrary provider-conversion matrix. Do not add automatic
cross-era/topology migration, skip semantics, warning-as-success, or
rebuilt-candidate release qualification.

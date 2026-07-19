---
plan_slug: v12-universal-upgrades
phase: tasks
rig: beads
rig_root: /data/projects/beads-v12-universal
artifact_root: /data/projects/beads-v12-universal/.gc/plans
requirements_file: /data/projects/beads-v12-universal/.gc/plans/v12-universal-upgrades/requirements.md
design_file: /data/projects/beads-v12-universal/.gc/plans/v12-universal-upgrades/design.md
status: created
created_at: '2026-07-18T00:00:00Z'
updated_at: '2026-07-19T07:26:41Z'
created_beads_at: '2026-07-19T03:26:12Z'
---

# Task Plan: Beads v1.2 Universal Upgrades

## Summary

Reuse the existing `bd-ldt0f` umbrella epic and replace its obsolete 99-slice
execution contract with eight source-work children plus three no-source
execution barriers. The first two children start in parallel: preserve the
accepted upgrade history on the multi-provider base, and lock the complete
historical-release/provider denominator plus immutable negative-probe
specifications. Authentic E2E infrastructure then materializes historical
binaries and locks the executed applicability evidence. Qualification
infrastructure waits for the completed shared U4 lifecycle contract but not for
dynamic route-specific U4 children.

## Epic

- `epic-v12-universal` reuses `bd-ldt0f` — Deliver deterministic, robust Beads
  upgrades from every historical public release to the exact v1.2 candidate.

## Beads

| Key | Outcome | Starts after |
|---|---|---|
| `u0-integrate-history` | Accepted v0.49.6/v0.55.4/v0.57/v0.62/v0.63.3 gates coexist with the multi-provider base as literal ancestry. | Now |
| `u1-lock-denominators` | Every historical tag/release variant, immutable negative-probe specification, and current provider/topology is checked in and mechanically complete. | Now |
| `u2-authentic-harness` | Producer-fan-in old binaries create real workspaces, execute applicability probes into a checked-in evidence sidecar, and produce strict pass/fail results against a supplied candidate set. | U0, U1 |
| `u3-unified-planner` | Startup and `bd migrate` share deterministic pre-store classification and hybrid automatic/manual policy. | U0, U1 |
| `u4-transactional-apply` | The shared storage lifecycle and route-local implementations prepare, verify, activate, resume, and restore behind the driver boundary. | U3; each route only on its own capabilities |
| `u5-candidate-pipeline` | The one-build pipeline and immutable manifest are implemented and tested with disposable artifacts; no final candidate is produced. | U0 |
| `u6-qualification-infra` | Representative/nightly/exhaustive workflows, strict dual-denominator aggregation, faults, platforms, and install-channel tests are implemented; unaffected rows can run. | U2, U4 shared contract, U5 |
| `u7-release-gate-infra` | Read-only eligibility logic and user documentation are implemented and tested with fixtures before the byte freeze. | U5, U6 |
| `u8-freeze-build` | All source/helper inputs are frozen and exactly one immutable packaged candidate is built. | U0-U7 and every discovered U4 route child |
| `u9-exact-qualification` | The frozen candidate set executes every mandatory smoke row and focused case without changing any frozen byte. | U8 |
| `u10-readonly-eligibility` | The frozen manifest and U9 results are evaluated read-only as eligible/ineligible; no tag or release is published. | U9 |

## Dependency Graph

```text
u0-integrate-history ----+----> u2-authentic-harness ----------------+
                         |                                            |
                         +----> u3-unified-planner ---> u4 shared ----+----> u6-qualification-infra ---> u7-release-gate-infra
                         |                               |            |                 |
                         |                               `--> u4-route[*]                |
                         `----> u5-candidate-pipeline ----------------+-----------------+
                                                                      |
u1-lock-denominators ----+----> u2 / u3 ------------------------------+

u0-u7 + every required u4-route[*]
             |
             v
u8-freeze-build --> u9-exact-qualification --> u10-readonly-eligibility
```

`u4-transactional-apply` completes the shared lifecycle contract and may close
after emitting a route-specific U4 child plus a real linked `dolthub/driver`
issue/PR dependency for each missing primitive. U6 depends on the completed U4
parent but never on those dynamic children; it invokes U4-owned tests, and
affected smoke rows/focused cases still execute and emit terminal `FAIL` while
unaffected work continues. Creating a route child atomically adds it as a
direct `blocks` dependency of U8, and a DAG check compares the two sets. U8 is
the first global barrier and cannot run until all required route children pass.

## Legacy Bead Reconciliation

The existing `plan:deterministic-upgrades` subtree is historical input, not the
new execution DAG.

- Preserve every already-closed bead and its commits/reasons.
- Adopt `bd-ldt0f.3.23`, `.3.24`, `.3.25`, `.3.26`, and `.3.27` into U0. Close
  them as completed only after their accepted commits become literal ancestry
  on the multi-provider branch.
- Keep contributor-disposition work such as `bd-ldt0f.1.9` separate unless an
  actual path overlap makes it a blocker. Do not silently supersede external
  contributor work.
- The exhaustive legacy partition is 150 descendants: preserve 10 already
  closed; adopt/reparent five `.3.23`-`.3.27` rows into U0; retain and reparent
  contributor row `.1.9` to the root; defer ten hosted/telemetry rows; and
  supersede the remaining 124 rows to the applicable U0-U7 source-work child.
  The deferred set is `.9`, `.9.1`-`.9.5`, `.10.2`, `.12.2`, `.12.6`, and
  `.12.8`. External `bd-1gpnp` is separately deferred. This deliberately keeps
  hosted archive/object-store/KMS work valuable but outside the v1.2 upgrade
  path instead of falsely claiming U0-U7 implements it.
- Remove cross-cohort blocker edges from retained/deferred rows before
  superseding their obsolete blockers. Supersede old children before old phase
  parents. Preserve internal deferred-cohort edges and the adopted rows'
  discovered-from/bug relationships.
- Update `bd-ldt0f` to the requirements/design in this plan while retaining the
  former plan hash and description in notes as superseded history.

### Exact legacy reconciliation manifest

The manifest below is exhaustive over the 150 legacy descendants and is checked
for zero omissions and zero duplicates.

- Preserve closed: `.1.10`, `.1.13`, `.1.14`, `.1.16`, `.1.17`, `.1.19`,
  `.1.20`, `.1.21`, `.1.22`, `.3.22`.
- Reparent to U0: `.3.23`, `.3.24`, `.3.25`, `.3.26`, `.3.27`.
- Retain at root: `.1.9`.
- Defer: `.9`, `.9.1`, `.9.2`, `.9.3`, `.9.4`, `.9.5`, `.10.2`, `.12.2`,
  `.12.6`, `.12.8`. Defer external `bd-1gpnp` separately.
- Supersede to U0: `.1`, `.1.1`, `.1.2`, `.1.3`, `.1.4`, `.1.5`, `.1.6`,
  `.1.7`, `.1.8`, `.1.12`, `.1.15`, `.1.18`, `.1.23`.
- Supersede to U1: `.10.7`.
- Supersede to U2: `.3`, `.3.1`, `.3.2`, `.3.3`, `.3.4`, `.3.5`, `.3.6`,
  `.3.7`, `.3.8`, `.3.9`, `.3.10`, `.3.11`, `.3.12`, `.3.13`, `.3.15`,
  `.3.16`, `.3.17`, `.3.18`, `.3.19`, `.3.20`, `.3.21`, `.4.4`, `.10.5`,
  `.10.12`, `.10.13`, `.10.14`.
- Supersede to U3: `.2`, `.2.1`, `.2.2`, `.2.3`, `.2.4`, `.2.8`, `.2.9`,
  `.2.10`, `.2.12`, `.2.13`, `.5.4`, `.5.6`, `.10.3`, `.12.1`, `.12.4`.
- Supersede to U4: `.2.5`, `.2.6`, `.2.7`, `.2.11`, `.4`, `.4.1`, `.4.2`,
  `.4.3`, `.4.5`, `.5`, `.5.1`, `.5.2`, `.5.3`, `.5.5`, `.6`, `.6.1`,
  `.6.2`, `.7`, `.7.1`, `.7.2`, `.7.3`, `.7.4`, `.7.5`, `.7.6`, `.7.7`,
  `.7.9`, `.7.10`, `.7.11`, `.8`, `.8.1`, `.8.2`, `.8.3`, `.10`, `.10.1`,
  `.10.4`, `.10.6`, `.10.9`, `.10.10`, `.10.11`.
- Supersede to U5: `.11.5`.
- Supersede to U6: `.1.11`, `.3.14`, `.4.6`, `.4.7`, `.4.8`, `.5.7`, `.6.3`,
  `.6.4`, `.6.5`, `.6.6`, `.6.7`, `.7.8`, `.10.8`, `.11`, `.11.1`, `.11.2`,
  `.11.3`, `.11.4`, `.12.7`.
- Supersede to U7: `.11.6`, `.11.7`, `.11.8`, `.12`, `.12.3`, `.12.5`,
  `.12.9`, `.12.10`, `.12.11`, `.13`.

## Execution Rules

- Run the repository PR preflight before implementation and again before
  opening a PR. Existing contributor work is reviewed/adopted first.
- Every implementation child uses an isolated worktree and
  `gpt-5.6-terra`/high.
- Behavior changes follow RED -> minimal GREEN -> refactor. A test that passes
  before the behavior exists does not count as RED.
- Before every source commit, derive the digest directly from
  `git diff --cached --binary --full-index`. Exactly three independent
  `gpt-5.6-sol`/Ultra reviewers inspect those bytes; all three must approve
  with no unresolved Critical or Important finding. Recompute immediately
  before commit; changed bytes require three fresh reviews.
- Every nontrivial PR needs substantive accountable-human review before merge.
  Migration/schema/sync paths never merge with bot-only approval.
- Every PR targets `gastownhall/beads:feature/backend-provider-change-20260713`,
  is created using standard `gh`/GitHub tooling with
  `status/needs-review-auto`, captures the returned PR number/URL, and verifies
  that exact PR's corresponding `LabeledEvent` through fully paginated GraphQL
  `timelineItems`. Schema-valid Actor fragments are used. If automation consumes the trigger into
  `status/reviewing`, the historical event is sufficient and the trigger is
  never re-added.

## Creation Notes

- The existing parent epic mapping is intentionally pre-seeded below so the
  Gas City materializer creates no duplicate umbrella epic.
- The fenced YAML payload is the source of truth for child creation and
  dependencies.
- `files` entries are expected ownership anchors, not permission for unrelated
  cleanup.

## Open Questions

None. Route-specific driver gaps are discovered by executable conformance tests
in U4 and handled as explicit external dependencies rather than assumed Beads
workarounds.

## Created Beads

| Key | Bead ID | Title |
|---|---|---|
| epic-v12-universal | bd-ldt0f | Deliver deterministic, robust Beads upgrades |
| u0-integrate-history | bd-ldt0f.14 | Integrate accepted upgrade history onto the multi-provider base |
| u1-lock-denominators | bd-ldt0f.15 | Lock every historical release producer and current provider topology |
| u2-authentic-harness | bd-ldt0f.17 | Generalize the authentic historical-workspace harness |
| u3-unified-planner | bd-ldt0f.18 | Unify pre-store classification and explicit migration UX |
| u4-transactional-apply | bd-ldt0f.19 | Implement prepare verify activate resume and restore through storage |
| u5-candidate-pipeline | bd-ldt0f.20 | Implement the one-build v1.2 candidate pipeline |
| u6-qualification-infra | bd-ldt0f.21 | Implement universal-upgrade qualification infrastructure |
| u7-release-gate-infra | bd-ldt0f.22 | Implement and test the read-only v1.2 eligibility gate |
| u8-freeze-build | bd-ldt0f.23 | Freeze all inputs and build the exact v1.2 candidate once |
| u9-exact-qualification | bd-ldt0f.24 | Execute exact-candidate universal upgrade qualification |
| u10-readonly-eligibility | bd-ldt0f.25 | Evaluate exact-candidate release eligibility read-only |
## Bead Creation Payload

```yaml
target_rig: beads
labels:
  - plan:v12-universal-upgrades
  - upgrade-system
epics:
  - key: epic-v12-universal
    title: Deliver deterministic, robust Beads upgrades
    type: epic
    priority: 0
    description: |
      Deliver a deterministic direct path from every authentic public Beads
      release workspace to the exact packaged v1.2 candidate. Reuse existing
      epic bd-ldt0f and replace its obsolete 99-slice execution contract with
      the eight source-work children and three execution barriers in this plan.
    acceptance_criteria:
      - Every cutoff historical tag has exactly one release-lock record, and every authentic tag/producer/topology/platform/build-flavor smoke_row_id has exactly one mandatory direct-to-candidate result.
      - The exact packaged v1.2 candidate set passes N/N smoke rows and M/M focused cases with zero skips, warnings-as-success, missing results, or candidate-set/selected-artifact digest mismatches.
      - Startup and bd migrate share one deterministic planner; large or topology-changing work refuses before mutation and requires explicit consent.
      - Storage effects and crash guarantees remain behind internal/storage or dolthub/driver.
      - Every source commit receives exactly three independent Sol/Ultra staged-diff reviews, and protected paths receive substantive human review before merge.
    dependencies: []
    labels:
      - program-epic
    metadata:
      architecture_model: gpt-5.6-sol
      architecture_reasoning: ultra
      implementation_model: gpt-5.6-terra
      implementation_reasoning: high
      requirements_trace: "Outcome,R1-R8"
      previous_plan: superseded:b92f3957b4165cf70894eab764bd4abc31399bfbe53abefe448e3bc4f5ef1508
beads:
  - key: u0-integrate-history
    title: Integrate accepted upgrade history onto the multi-provider base
    type: task
    priority: 0
    epic: epic-v12-universal
    description: |
      Start from af136f8857dd3e0461e06597f37e925088a98a49 and create a
      no-fast-forward, two-parent merge of accepted chain tip
      dce8d066de983b4fa4487890f48157a7264d86d2. There are no expected textual
      conflicts. Preserve both parents, the accepted commit objects, later
      multi-provider lifecycle/census code, and all authentic migration tests.

      Do not cherry-pick or squash the chain. Do not merge PR #4801 head
      a0a51638c because its tree is already represented by 2ef7c61e0. Record
      the equal-tree mapping, preserve Julian Knutsen/Test User/Eddie the
      Engineer attribution, and reference #4801 and #4845 in the PR.

      After the provenance merge, add a separate adaptation commit only for a
      failing multi-provider/storage-boundary behavior proved by a RED test.
      Keep server_to_embedded.sh regression-only and unregistered; production
      storage effects must use the storage/driver boundary.
    acceptance_criteria:
      - af136f885 and dce8d066d are literal ancestors and the merge commit has them as ordered first and second parents.
      - Every chain-owned blob is unchanged in the provenance merge, and the multi-provider selection/binding/lifecycle/effect-census tests still pass.
      - "PR #4801 tests, intent, and attribution are preserved without duplicate equivalent history."
      - Authentic v0.49.6, v0.55.4, v0.57.0, v0.62.0, and v0.63.3 lanes pass with their expected AUTO/MANUAL behavior, plus the public v0.62 bridge suite.
      - Existing beads bd-ldt0f.3.23 through .3.27 are closed as completed only after this ancestry and verification exist.
      - Before each source commit, exactly three independent gpt-5.6-sol/Ultra reviewers inspect and explicitly APPROVE one identical staged-diff digest; every Critical/Important finding is resolved, and any byte change invalidates all three approvals and requires three fresh reviews.
      - The PR targets gastownhall/beads:feature/backend-provider-change-20260713; creation initially supplies status/needs-review-auto and captures the returned PR number/URL; verification uses that captured identity, paginates that exact PR's GraphQL timelineItems through hasNextPage == false, finds the corresponding LabeledEvent using schema-valid Actor fragments including ... on User { databaseId } when identity is needed, accepts later status/reviewing consumption, and never re-adds the trigger.
      - A substantive accountable-human review approves the PR's current head before merge; migration/schema/sync paths never merge on bot-only approval.
    dependencies: []
    labels:
      - migrations
      - contributor-adoption
      - ready-first
    files:
      - .github/workflows/migration-test.yml
      - cmd/bd/init.go
      - cmd/bd/main.go
      - cmd/bd/migration_v062_init.go
      - cmd/bd/migration_v062_init_embedded_test.go
      - cmd/bd/migration_v062_inspect.go
      - cmd/bd/migration_v062_inspect_test.go
      - cmd/bd/prestore_metadata_guard_e2e_test.go
      - cmd/bd/version_tracking.go
      - internal/v062migration/
      - scripts/migrate-v062-server-to-current.sh
      - scripts/migration-test/lib/binary.sh
      - scripts/migration-test/lib/direct_probe.sh
      - scripts/migration-test/lib/features.sh
      - scripts/migration-test/lib/report.sh
      - scripts/migration-test/lib/snapshot.sh
      - scripts/migration-test/lib/versions.sh
      - scripts/migration-test/lib/workspace.sh
      - scripts/migration-test/public-v062-bridge-test.sh
      - scripts/migration-test/recipes/fix_dash_prefix.sh
      - scripts/migration-test/recipes/server_to_embedded.sh
      - scripts/migration-test/recipes/sqlite_to_current.sh
      - scripts/migration-test/run.sh
      - scripts/migration-test/strict-mode-test.sh
      - docs/getting-started/upgrading.md
    verification:
      - git merge-base --is-ancestor af136f885 HEAD
      - git merge-base --is-ancestor dce8d066d HEAD
      - CGO_ENABLED=1 go test -tags gms_pure_go ./internal/backendmigration/... ./internal/v062migration/...
      - ./scripts/migration-test/strict-mode-test.sh
      - make build
      - PUBLIC_V062_REAL_TARGET_BD=./bd ./scripts/migration-test/public-v062-bridge-test.sh
      - make test
      - three Sol/Ultra review outputs name the final git diff --cached digest; GraphQL label-event and current-head human-review checks pass
    metadata:
      execution_agent_type: codex
      execution_suggested_model: gpt-5.6-terra
      execution_reasoning_effort: high
      execution_mode: isolated-worktree
      execution_parallel_group: wave-0
      target: feature/backend-provider-change-20260713
      contributor_intake_prs: "#4801,#4845"
      review_model: gpt-5.6-sol
      review_reasoning_effort: ultra
      review_seats: "3"
      requirements_trace: "Outcome,Base Integration,R2.1,R2.6,R6,R8.5"

  - key: u1-lock-denominators
    title: Lock every historical release producer and current provider topology
    type: task
    priority: 0
    epic: epic-v12-universal
    description: |
      Add reviewed release and provider locks plus a deterministic validator.
      Enumerate every cutoff v* tag exactly once and bind its tag commit,
      release classification, official artifact checksums or pinned source-build
      recipe, platform/build flavor, and expected physical workspace family.

      Independently derive current provider/topology/build coverage from config
      constants and validation, init paths, CGO/non-CGO store factories, and
      storage implementations. The checked-in provider lock must be bijective
      with that derivation. Remote GitHub discovery belongs to an explicit
      refresh/check mode; ordinary local validation is stable and offline.

      For every historical tag, explicitly inventory standalone, redirected or
      shared, remote/server-backed, proxied, platform, and build-flavor
      variants. Lock an immutable negative create/open probe specification bound
      to the exact tag commit for every proposed non-applicable variant. U1 does
      not acquire or execute historical binaries; U2 owns that execution and a
      separate evidence sidecar. Prose, a current-binary inference, or pending
      evidence cannot remove a mandatory identity.
      Maintain a separate checked-in inventory of direct installation and every
      supported v1.2 distribution channel that can create or open a workspace.

      No missing artifact or toolchain becomes a skip. It remains a failed,
      actionable producer row until acquisition/build succeeds.
    acceptance_criteria:
      - The cutoff lock contains all 173 current public v* tags exactly once, including stable, RC, and nosqlite classifications.
      - The lock reconciles all 104 release records and 714 assets without using draft status to remove a public-tag row.
      - Every row has verified artifact checksums or a pinned exact-tag build recipe and maps to a declared physical workspace family.
      - Every tag explicitly enumerates each shared, remote, proxied, platform, and build-flavor variant and locks a tag-commit-bound immutable negative create/open probe specification for each proposed inapplicability; no acceptance criterion requires U1 to acquire or execute a historical binary.
      - Proposed-inapplicable variants remain mandatory until U2's separate applicability-evidence lock contains matching executed historical-binary evidence; missing or mismatched evidence can never shrink U1's denominator.
      - The provider lock exactly matches Dolt, SQLite, PostgreSQL, MySQL, their shipped topologies, and CGO/non-CGO factory surfaces.
      - The maintained install-channel inventory is complete and rejects a missing, stale, duplicated, or non-workspace-producing channel classification.
      - Deletion, duplication, stale commit, bad checksum, unknown family/provider, or factory disagreement mutants fail validation.
      - Before each source commit, exactly three independent gpt-5.6-sol/Ultra reviewers inspect and explicitly APPROVE one identical staged-diff digest; every Critical/Important finding is resolved, and any byte change invalidates all three approvals and requires three fresh reviews.
      - The PR targets gastownhall/beads:feature/backend-provider-change-20260713; creation initially supplies status/needs-review-auto and captures the returned PR number/URL; verification uses that captured identity, paginates that exact PR's GraphQL timelineItems through hasNextPage == false, finds the corresponding LabeledEvent using schema-valid Actor fragments including ... on User { databaseId } when identity is needed, accepts later status/reviewing consumption, and never re-adds the trigger.
      - A substantive accountable-human review approves the PR's current head before merge; migration/schema/sync paths never merge on bot-only approval.
    dependencies: []
    labels:
      - test-infrastructure
      - release-catalog
      - ready-first
    files:
      - scripts/migration-test/releases.lock.json
      - scripts/migration-test/providers.lock.json
      - scripts/migration-test/install-channels.lock.json
      - scripts/migration-test/manifest-test.sh
      - scripts/migration-test/lib/manifest.sh
    verification:
      - ./scripts/migration-test/manifest-test.sh
      - git tag --list 'v*' count and identity reconciliation against releases.lock.json
      - validator mutation tests for missing, duplicate, stale, unknown, and constructor-disagreement rows
      - historical topology/probe-specification and maintained-install-channel mutation tests
      - git diff --check
      - three Sol/Ultra review outputs name the final git diff --cached digest; GraphQL label-event and current-head human-review checks pass
    metadata:
      execution_agent_type: codex
      execution_suggested_model: gpt-5.6-terra
      execution_reasoning_effort: high
      execution_mode: isolated-worktree
      execution_parallel_group: wave-0
      target: feature/backend-provider-change-20260713
      review_model: gpt-5.6-sol
      review_reasoning_effort: ultra
      review_seats: "3"
      requirements_trace: "R1.1-R1.6,R7.4,R8.1"

  - key: u2-authentic-harness
    title: Generalize the authentic historical-workspace harness
    type: task
    priority: 0
    epic: epic-v12-universal
    description: |
      Replace hardcoded-version control flow with manifest-selected rows while
      preserving the accepted representative sentinel tests. Before row fan-out,
      fetch or build each unique historical producer exactly once using the key
      tag commit/platform/build flavor/recipe digest/toolchain digest and
      distribute its immutable verified bytes through workflow-internal,
      digest-addressed transport. Row workers never fetch from external origins
      or compile. Execute U1's immutable negative applicability probes with those
      exact binaries and write a separate checked-in evidence lock; pending or
      missing evidence leaves the identity mandatory.

      For each selected row, create and mutate a nonempty isolated workspace
      with the fan-in binary, capture the independent pre-state, run the exact
      manifest-selected artifact from a supplied packaged candidate set, apply
      the explicit route when needed, and verify fidelity, post-upgrade
      mutation, reopen, and idempotent rerun.

      Emit terminal PASS/FAIL for every assigned smoke row and focused case.
      Acquisition failure, timeout,
      unsupported host, unavailable historical Dolt, empty source data, bad
      checksum, source mutation, missing result, or indeterminate probe is FAIL.
      Cache immutable inputs by digest and keep concise per-row diagnostics.
      Remote/server/proxy rows use row-exclusive ephemeral endpoints and
      credentials, a sanitized DSN environment, production-endpoint rejection,
      and execution-time egress denial except to declared row endpoints.

      Every terminal result carries its own source binary/build, frozen helper,
      candidate-set and selected-artifact, environment, external-Dolt, and
      download identities. Run each row with temporary
      HOME/XDG/repository/database roots, repository-local hooks, path/inode and
      endpoint guards against production, and redacted terminal and retained
      output. U2 owns separate smoke_row_id and focused_case_id result schemas,
      the feature matrix, and their self-tests; covered_identities is
      traceability-only metadata. U6 owns scheduling, sharding, and independent
      reconciliation of both denominators.
    acceptance_criteria:
      - The harness selects rows from releases.lock.json plus applicability-evidence.lock.json, validates the evidence sidecar bijectively against U1's probe specifications, and treats pending, missing, stale, or mismatched evidence as a still-mandatory identity rather than a skip.
      - Producer fan-in keyed by exact tag commit/platform/build flavor/recipe digest/toolchain digest fetches or builds every unique historical artifact once per run and fans out immutable digest-verified bundles through workflow-internal transport; row workers may retrieve only those bundles and cannot fetch from external origins or compile.
      - Historical binaries, not current code or synthetic fixtures, create every qualifying workspace.
      - Every assigned smoke_row_id is exact tag/producer/topology/platform/build-flavor and emits one direct PASS or FAIL; every assigned focused_case_id is exact suite/equivalence-class/fault-case and emits one PASS or FAIL; every result has exactly one namespace key, and covered_identities contains only known smoke references and never emits, satisfies, reuses, or duplicates a smoke result.
      - Each PASS or FAIL embeds source tag/commit and source-binary digest or separate source-build recipe/toolchain digests; frozen helper tree; candidate commit/tree, one candidate_set_digest, and the candidate_artifact_digest matching the manifest-selected row platform/build entry; OS/architecture/runner/container image and allowlisted environment; external Dolt binary/version/digest or version-gated N/A; and origin plus digest for every download.
      - Before either binary runs, each row proves fresh row-local HOME, XDG, repository, and database roots; repository-local core.hooksPath with global/system hooks disabled; containment of every BEADS_DIR, BEADS_DB, BD_DB, and provider storage path; and realpath/inode exclusion of the production checkout, its .beads, and configured production databases.
      - Every remote/server/proxy row proves row-exclusive ephemeral endpoints and credentials, an allowlisted sanitized DSN environment, normalized-alias-aware production/shared-endpoint rejection before execution, and execution-time egress denial except to its declared test endpoints; acquisition/build networking is unavailable in row workers.
      - Terminal results and retained logs redact credentials, authenticated URLs, tokens, user names, hosts, DSNs, and filesystem locators while preserving immutable digests and opaque diagnostic identities.
      - The version-gated oracle individually checks issue ID, title, description, status, priority, issue type, every source-supported timestamp, assignee, owner, external reference, custom metadata, label values, comment bodies/authors/timestamps, dependency endpoints/types, blocker/readiness behavior, issue/label/comment/dependency/readiness counts, workspace/repository identity, and applicable branch/remote/redirected/shared/server/proxied topology semantics.
      - Post-upgrade checks individually exercise create, field update, dependency add/remove with readiness transitions, close, semantic export comparison, issue reopen, store reopen, completed-migration NoOp, and another reopen.
      - Every named field, relationship, topology semantic, count, and operation emits a supported cell and executes its comparison/operation, or emits absent with pinned source-boundary evidence plus a negative historical-binary probe; silence, grouped shorthand, and empty fixtures fail.
      - Refusal, dry-run, failed apply, and rollback paths prove the source unchanged byte-for-byte or through the strongest driver-provided immutable identity, and rollback reopens and semantically verifies the restored source.
      - Harness self-tests fail on tampered binaries, source mutation, deleted oracle cells, false absence, partial snapshots, results with both/neither namespace key, unknown covered_identities references, missing/duplicate cross-namespace results, covered_identities smoke duplication, PATH shadowing, wrong candidate-set or selected-artifact digest, ambient HOME/hooks/database leakage, cross-row endpoint leakage, normalized production DSNs/endpoints, undeclared egress, production-path aliases, unredacted sentinels, erased diagnostic identities, and every missing provenance field.
      - Before each source commit, exactly three independent gpt-5.6-sol/Ultra reviewers inspect and explicitly APPROVE one identical staged-diff digest; every Critical/Important finding is resolved, and any byte change invalidates all three approvals and requires three fresh reviews.
      - The PR targets gastownhall/beads:feature/backend-provider-change-20260713; creation initially supplies status/needs-review-auto and captures the returned PR number/URL; verification uses that captured identity, paginates that exact PR's GraphQL timelineItems through hasNextPage == false, finds the corresponding LabeledEvent using schema-valid Actor fragments including ... on User { databaseId } when identity is needed, accepts later status/reviewing consumption, and never re-adds the trigger.
      - A substantive accountable-human review approves the PR's current head before merge; migration/schema/sync paths never merge on bot-only approval.
    dependencies:
      - u0-integrate-history
      - u1-lock-denominators
    labels:
      - e2e
      - test-infrastructure
    files:
      - scripts/migration-test/run.sh
      - scripts/migration-test/applicability-evidence.lock.json
      - scripts/migration-test/lib/binary.sh
      - scripts/migration-test/lib/workspace.sh
      - scripts/migration-test/lib/snapshot.sh
      - scripts/migration-test/lib/features.sh
      - scripts/migration-test/lib/report.sh
      - scripts/migration-test/lib/manifest.sh
      - scripts/migration-test/lib/oracle.sh
      - scripts/migration-test/recipes/
    verification:
      - ./scripts/migration-test/run.sh --self-test
      - focused authentic runs for v0.49.6 v0.55.4 v0.57.0 v0.62.0 v0.63.3 v1.0.0 v1.1.0
      - strict tamper, mutation, partial-result, and wrong-candidate harness tests
      - multi-row one-producer-materialization and immutable-fan-out mutation test
      - probe-specification/evidence-lock bijection and historical-binary execution tests
      - isolation provenance version-gating redaction cross-row-leakage production-DSN undeclared-egress and production-path-alias self-tests
      - bash -n and ShellCheck on changed shell files
      - three Sol/Ultra review outputs name the final git diff --cached digest; GraphQL label-event and current-head human-review checks pass
    metadata:
      execution_agent_type: codex
      execution_suggested_model: gpt-5.6-terra
      execution_reasoning_effort: high
      execution_mode: isolated-worktree
      execution_parallel_group: wave-1
      target: feature/backend-provider-change-20260713
      review_model: gpt-5.6-sol
      review_reasoning_effort: ultra
      review_seats: "3"
      requirements_trace: "R1.2,R1.4,R2.1-R2.6,R6,R7.1-R7.4"

  - key: u3-unified-planner
    title: Unify pre-store classification and explicit migration UX
    type: feature
    priority: 1
    epic: epic-v12-universal
    description: |
      Add one storage-neutral observer, route graph, safety classifier, and
      deterministic ordered planner used by both startup and bd migrate.
      Classify plans as NoOp, StartupSafe, ManualRequired, or Unsupported.

      Route evidence comes only from bounded storage/driver-owned read-only
      probes. Historical semver and requested destination provider are never
      route authority. Unknown, conflicting, incomplete, or indeterminate
      evidence is Unsupported; Beads performs no engine introspection.

      Run classification before provider/store open, engine launch,
      provisioning, version tracking, auto-import, telemetry, or metadata write.
      Startup may execute only structurally fixed, same-provider/topology,
      driver-declared atomic or rollback-safe work. All data-dependent,
      historical-bridge, server/embedded, remote, destructive, or provider-change
      plans fail before mutation and direct users to existing bd migrate
      --inspect/--dry-run/interactive/--yes modes.

      Every mutating adapter path, including StartupSafe, must enter the U4
      driver-owned quiescence lifecycle, re-probe, recompute the complete plan,
      and match source/capability/configuration/topology identity before its
      first write.

      Keep provider-local version upgrade distinct from backend conversion. The
      only admitted provider-changing route remains embedded Dolt to PostgreSQL.
    acceptance_criteria:
      - Startup and every bd migrate mode call the same observer, route selector, safety classifier, and planner.
      - Equivalent observations render byte-identical plans under randomized registry, probe, filesystem, and capability order.
      - Route selection tests prove historical semver and requested destination provider cannot select or override a route, and unknown/conflicting/incomplete physical evidence returns typed Unsupported.
      - Only storage/driver-owned bounded read-only probes supply observations; the migration core never introspects engine files/SQL, opens or provisions a store, launches an engine, or uses registry order as authority.
      - StartupSafe mechanically rejects bulk scan, copy, backfill, index build, snapshot, cutover, remote coordination, destructive rewrite, and all content-dependent work regardless of row count or elapsed-time estimates.
      - Effect-spy subprocess tests prove classification/refusal occurs before provider/store open, engine launch, provisioning, version tracking, auto-import, telemetry, and workspace-metadata writes; non-startup-safe plans return typed upgrade_required or unsupported without any of those effects.
      - Every mutating adapter path, including StartupSafe, calls the same driver-owned quiesce/re-probe/full-plan-recompute gate and refuses before its first write on source, capability, configuration, topology, or plan-byte drift.
      - bd migrate --inspect and --dry-run are read-only; bd migrate and --yes show/use the same plan and explicit consent semantics.
      - Current Dolt, SQLite, PostgreSQL, and MySQL workspaces remain on their provider and legacy evidence is never provisioned as a fresh current store.
      - Help, version, migrate inspection/dry-run, and applicable doctor diagnostics remain usable while ordinary commands are blocked.
      - Before each source commit, exactly three independent gpt-5.6-sol/Ultra reviewers inspect and explicitly APPROVE one identical staged-diff digest; every Critical/Important finding is resolved, and any byte change invalidates all three approvals and requires three fresh reviews.
      - The PR targets gastownhall/beads:feature/backend-provider-change-20260713; creation initially supplies status/needs-review-auto and captures the returned PR number/URL; verification uses that captured identity, paginates that exact PR's GraphQL timelineItems through hasNextPage == false, finds the corresponding LabeledEvent using schema-valid Actor fragments including ... on User { databaseId } when identity is needed, accepts later status/reviewing consumption, and never re-adds the trigger.
      - A substantive accountable-human review approves the PR's current head before merge; migration/schema/sync paths never merge on bot-only approval.
    dependencies:
      - u0-integrate-history
      - u1-lock-denominators
    labels:
      - migration-planner
      - multi-provider
    files:
      - internal/upgrade/
      - internal/backendmigration/
      - internal/storage/
      - internal/configfile/
      - cmd/bd/main.go
      - cmd/bd/migrate.go
      - cmd/bd/store_factory.go
      - cmd/bd/store_factory_nocgo.go
      - cmd/bd/version_tracking.go
    verification:
      - planner unit tests with randomized order and all four safety outcomes
      - table-driven non-authority tests for semver/destination input and every forbidden StartupSafe effect
      - storage-probe ownership tests plus unknown/conflicting/incomplete-evidence refusal tests
      - subprocess no-mutation tests for every historical family and current provider
      - CGO_ENABLED=1 go test -tags gms_pure_go ./internal/upgrade/... ./internal/backendmigration/... ./cmd/bd/...
      - make test
      - three Sol/Ultra review outputs name the final git diff --cached digest; GraphQL label-event and current-head human-review checks pass
    metadata:
      execution_agent_type: codex
      execution_suggested_model: gpt-5.6-terra
      execution_reasoning_effort: high
      execution_mode: isolated-worktree
      execution_parallel_group: wave-1
      target: feature/backend-provider-change-20260713
      review_model: gpt-5.6-sol
      review_reasoning_effort: ultra
      review_seats: "3"
      requirements_trace: "R3.1-R3.5,R4"

  - key: u4-transactional-apply
    title: Implement prepare verify activate resume and restore through storage
    type: feature
    priority: 1
    epic: epic-v12-universal
    description: |
      Begin with executable conformance tests for the storage/driver capabilities
      required by each manual route. Expose the minimum storage-neutral
      inspection, quiescence, prepare, verification, activation, checkpoint,
      resume, and restore contract. Keep provider/engine details and durable
      authority in internal/storage or dolthub/driver.

      Every mutating route, including StartupSafe, acquires driver-owned
      quiescence and recomputes before its first write. Manual routes preserve
      the source, prepare side-by-side, independently verify the target,
      activate last while revalidating source identity, and perform only
      read-only final verification after the configuration/topology/authority
      cutover. Interrupted reruns resume or restore deterministically;
      completed reruns are NoOp.

      While driver-owned quiescence is held, rerun observation and capability
      discovery and recompute the complete route, safety class, ordered plan,
      prerequisites, and verification criteria. Abort before snapshot/prepare
      if source, capability, configuration, topology, or plan bytes drift.
      Activate storage authority and configuration/topology selection last.

      If a required primitive is absent, create and link a real upstream driver
      issue/PR and block only the affected route. Do not add Beads-side engine
      inspection, flocking, storage-specific retry loops, file-copy cutover, or
      inferred crash completion.
    acceptance_criteria:
      - Every mutating route proves driver-owned quiescence and complete pre-write recomputation; every supported manual route additionally proves side-by-side preparation, independent verification, activation-last, resume, and restore.
      - Under quiescence the complete observation/capability/route/safety/step/prerequisite/verification plan is recomputed and exactly matched to the initial inspected plan for StartupSafe or the consented plan for manual execution; source, capability, configuration, topology, or plan drift refuses before mutation.
      - Failed verification never changes authority, and storage authority plus configuration/topology selection are the final mutation before read-only final verification.
      - Failure injection at every lifecycle boundary leaves exactly one authoritative reopenable store.
      - Resume and restore are idempotent, and restore tests reopen and verify the original semantic oracle.
      - Missing driver capability produces a linked external dependency and blocks only its route; no boundary-violating substitute lands in Beads.
      - Credentials, locators, and driver internals do not leak through errors, logs, or public SDK types.
      - After the shared lifecycle contract is complete, U4 may close once every proven missing primitive has a route-specific U4 child with a linked dolthub/driver dependency; creation atomically adds that child as a direct blocks dependency of U8, and the child's acceptance criteria literally repeat the all-three explicit approval, byte-change invalidation, initial status/needs-review-auto, captured returned PR identity, fully paginated exact-PR LabeledEvent/no-re-add, and accountable current-head human-review gates; affected smoke rows/focused cases report terminal FAIL until the route passes.
      - A DAG validator proves the set of discovered U4 route children exactly equals U8's direct route-child blocker set, that U6 depends on the completed U4 parent, and that no route child blocks U6.
      - Before each source commit, exactly three independent gpt-5.6-sol/Ultra reviewers inspect and explicitly APPROVE one identical staged-diff digest; every Critical/Important finding is resolved, and any byte change invalidates all three approvals and requires three fresh reviews.
      - The PR targets gastownhall/beads:feature/backend-provider-change-20260713; creation initially supplies status/needs-review-auto and captures the returned PR number/URL; verification uses that captured identity, paginates that exact PR's GraphQL timelineItems through hasNextPage == false, finds the corresponding LabeledEvent using schema-valid Actor fragments including ... on User { databaseId } when identity is needed, accepts later status/reviewing consumption, and never re-adds the trigger.
      - A substantive accountable-human review approves the PR's current head before merge; migration/schema/sync paths never merge on bot-only approval.
    dependencies:
      - u3-unified-planner
    labels:
      - migrations
      - storage-boundary
      - protected-path
    files:
      - internal/storage/storage.go
      - internal/storage/upgrade.go
      - internal/storage/sqlite/
      - internal/storage/dolt/
      - internal/storage/embeddeddolt/
      - internal/storage/postgres/
      - internal/storage/mysql/
      - internal/backendmigration/lifecycle.go
      - cmd/bd/migrate.go
      - go.mod
      - go.sum
    verification:
      - driver interface conformance tests per supported route
      - race tests mutate source capability configuration topology and plan inputs between initial inspection and quiesced recomputation for both StartupSafe and manual paths
      - DAG test proves U6 depends on completed shared U4 while every discovered U4 route child directly blocks U8 and none blocks U6
      - failure injection at inspect quiesce prepare verify activate read-only-final-verification resume and restore boundaries
      - repeated resume restore and completed no-op tests
      - CGO_ENABLED=1 go test -tags gms_pure_go ./internal/storage/... ./internal/backendmigration/... ./cmd/bd/...
      - make test
      - three Sol/Ultra review outputs name the final git diff --cached digest; GraphQL label-event and current-head human-review checks pass
    metadata:
      execution_agent_type: codex
      execution_suggested_model: gpt-5.6-terra
      execution_reasoning_effort: high
      execution_mode: isolated-worktree
      execution_parallel_group: wave-2
      target: feature/backend-provider-change-20260713
      external_dependency_policy: create-and-link-real-dolthub-driver-issue-only-if-conformance-proves-gap
      review_model: gpt-5.6-sol
      review_reasoning_effort: ultra
      review_seats: "3"
      requirements_trace: "R4,R5.1-R5.5"

  - key: u5-candidate-pipeline
    title: Implement the one-build v1.2 candidate pipeline
    type: task
    priority: 1
    epic: epic-v12-universal
    description: |
      Implement and test the pipeline that will later freeze one commit/tree,
      build/package exactly once, and emit an immutable candidate-set manifest
      with version, source commit/tree, platform/build artifact names, SHA-256
      digests, build environment, and the complete frozen-input digest
      inventory. The SHA-256 of the exact immutable manifest bytes containing
      that inventory is candidate_set_digest; each platform/build artifact has
      its distinct candidate_artifact_digest.
      Pipeline tests use explicitly disposable artifacts only; this child must
      not construct or name the final v1.2 candidate.

      The build entry point requires an immutable build record, target-ref
      identity, and source commit/tree as hard inputs before any compiler or
      packaging step starts. Release mode accepts only U8's finalized freeze
      record. Nightly prequalification mode accepts a disposable record that
      binds the tested commit/tree and complete build-input inventory and marks
      every output permanently release-ineligible. It verifies the supplied
      checkout and record exactly, and refuses a missing/mismatched record,
      wrong checkout, or moved target ref without invoking the build.

      Reject go run, an in-tree build, ambient PATH, per-shard rebuilds,
      mismatched embedded version, helper substitution, or a candidate manifest
      that does not describe the supplied bytes. No stable tag, release,
      registry publication, or maintained-channel publication occurs here.
    acceptance_criteria:
      - Pipeline fixture tests prove one-build behavior and a complete immutable source/helper/build-input/artifact inventory using a single candidate_set_digest plus distinct candidate_artifact_digest values for artifacts marked disposable.
      - All consumer fixtures verify candidate_set_digest, the manifest-selected platform/build candidate_artifact_digest, embedded version, frozen-input inventory, and epoch before running any upgrade test.
      - Pipeline tests prove a build cannot start without matching target-ref/source-commit/tree and a mode-appropriate immutable build record; missing or mismatched records, wrong checkouts, or moved refs invoke no compiler or packaging step.
      - Disposable prequalification records bind the same immutable inputs as release records but are domain-separated and publication-ineligible; U8/U9 reject their candidate sets, and release mode rejects them before any compiler or packaging step.
      - Wrong-binary, PATH-shadow, local-rebuild, renamed-artifact, stale-manifest, candidate-set mismatch, selected-artifact mismatch, and mixed-platform mutants fail before qualification.
      - The workflow can later build one candidate after U8's freeze barrier, but this child cannot emit a final-candidate manifest or downstream release-qualification result.
      - No stable tag, GitHub release, registry publication, maintained-channel publication, or external package update is performed by this task.
      - Before each source commit, exactly three independent gpt-5.6-sol/Ultra reviewers inspect and explicitly APPROVE one identical staged-diff digest; every Critical/Important finding is resolved, and any byte change invalidates all three approvals and requires three fresh reviews.
      - The PR targets gastownhall/beads:feature/backend-provider-change-20260713; creation initially supplies status/needs-review-auto and captures the returned PR number/URL; verification uses that captured identity, paginates that exact PR's GraphQL timelineItems through hasNextPage == false, finds the corresponding LabeledEvent using schema-valid Actor fragments including ... on User { databaseId } when identity is needed, accepts later status/reviewing consumption, and never re-adds the trigger.
      - A substantive accountable-human review approves the PR's current head before merge; migration/schema/sync paths never merge on bot-only approval.
    dependencies:
      - u0-integrate-history
    labels:
      - release-candidate
      - packaging
    files:
      - scripts/migration-test/candidate.sh
      - .github/workflows/v12-candidate.yml
      - .goreleaser.yml
    verification:
      - disposable candidate build and manifest self-test
      - candidate-set digest plus selected-artifact checksum and embedded-version verification on each supported platform
      - wrong-binary PATH-shadow stale-manifest candidate-set selected-artifact and mixed-digest mutation tests
      - build-record-to-build hard-dependency tests prove invalid mode ref commit tree record or checkout invokes no build step
      - make build
      - three Sol/Ultra review outputs name the final git diff --cached digest; GraphQL label-event and current-head human-review checks pass
    metadata:
      execution_agent_type: codex
      execution_suggested_model: gpt-5.6-terra
      execution_reasoning_effort: high
      execution_mode: isolated-worktree
      execution_parallel_group: wave-1
      target: feature/backend-provider-change-20260713
      review_model: gpt-5.6-sol
      review_reasoning_effort: ultra
      review_seats: "3"
      requirements_trace: "R8.1-R8.3,R8.6"

  - key: u6-qualification-infra
    title: Implement universal-upgrade qualification infrastructure
    type: task
    priority: 1
    epic: epic-v12-universal
    description: |
      Implement fast representative PR lanes, a scheduled nightly lane, and a
      deterministically sharded exhaustive matrix. Every mandatory
      tag/producer/topology/platform/build-flavor identity has one direct
      smoke_row_id PASS/FAIL result. Every deep equivalence, fault,
      current-provider, platform, and install-channel case has a separate
      focused_case_id PASS/FAIL result. Deep tests execute every four-axis
      equivalence class, including semantic-only boundaries.

      Add focused crash, competing actor, low-disk, corruption, ambiguous
      provider evidence, rollback/reopen, platform, and maintained install-channel
      tests. Current-provider cases reuse the full nonempty R6 oracle, including
      issues/fields, labels/comments, dependency add/remove and readiness
      transitions, counts, mutations, close/reopen, semantic export comparison,
      completed NoOp, rollback/restore, and restored-source reopen where
      applicable. Per-tag
      direct smoke may not be shared. A deep/fault case may be
      shared only with recorded equality of physical format/schema, selected
      route/ordered plan, provider/topology, and version-gated semantic duties,
      while retaining covered_identities as traceability-only metadata.
      Aggregate the smoke and focused denominators independently against the
      denominator/evidence locks, focused-cases registry, install-channel inventory,
      candidate_set_digest, and each manifest-selected
      candidate_artifact_digest. U6 consumes U2's result schemas and feature
      matrix unchanged; it owns global scheduling, sharding, and aggregation
      rather than a second oracle.

      For each nightly prequalification, seal a disposable build record over
      the tested commit/tree and inputs, then invoke the U5-built pipeline once
      in non-release mode before fan-out. Mark the candidate set ineligible for
      U8/release qualification/publication and prohibit shard rebuild or
      substitution. Run U2's unique-producer fan-in once before row fan-out;
      row workers retrieve only its internal digest-addressed bundles and never
      fetch from external origins or compile. U6 depends on the completed U4 shared
      lifecycle contract and invokes U4-owned lifecycle tests, but dynamic U4
      route children do not block it. A missing-capability route emits terminal
      route-local FAIL while unaffected rows run. U9 alone requires universal
      passing and records the final frozen-candidate qualification epoch.
    acceptance_criteria:
      - The lock-derived matrix assigns every mandatory tag/producer/topology/platform/build-flavor smoke_row_id exactly once and every required suite/equivalence-class/fault-case focused_case_id exactly once, with no success-bearing skip or missing result path; the focused denominator is independently derived from the feature/equivalence matrix, provider lock, install-channel lock, and focused-cases registry.
      - Representative PR lanes include v0.49.6, v0.55.4, v0.57.0, v0.62.0, v0.63.3, manifest-selected v1.0/v1.1 sentinels, current providers, and rollback/fail-before-open.
      - Deep coverage executes every manifest-derived four-axis equivalence class, including a semantic feature/operation boundary with unchanged format/schema/route/topology, rather than a hardcoded era list.
      - Every shared deep/fault focused case has a four-axis equivalence proof and covered_identities reverse traceability, but that metadata never emits, satisfies, reuses, or duplicates a smoke_row_id; per-tag direct smoke is never shared.
      - Current Dolt, SQLite, PostgreSQL, and MySQL fresh-create, defensive-open, provider-local schema, and same-provider cases execute for every locked current topology and shipped CGO/non-CGO build variant; they reuse the full nonempty R6 oracle including fields, labels/comments, dependency add/remove and readiness transitions, counts, mutations, close/reopen, semantic export comparison, completed NoOp, rollback/restore, and restored-source reopen where applicable, and emit PASS or route-local FAIL; only embedded Dolt to PostgreSQL is admitted as backend-changing.
      - Crash, concurrency, low-disk, corruption, ambiguity, restore/reopen, platform, and every maintained install-channel suite are represented and fail closed.
      - A checked-in UTC-daily schedule seals one disposable prequalification record and invokes the U5-built pipeline once in non-release mode to build/package one publication-ineligible candidate set before complete-matrix fan-out; every shard receives the identical candidate_set_digest/inventory, selects its candidate_artifact_digest from the manifest, and cannot rebuild or substitute either.
      - U2's exact-key producer fan-in materializes each unique historical artifact once per run before fan-out and distributes immutable bytes through workflow-internal digest-addressed transport; a multi-row mutation test fails any repeated external fetch/build or row-worker compilation.
      - Missing driver capabilities fail only affected mandatory results; unaffected rows remain runnable and the final smoke N/N plus focused M/M barrier still cannot pass.
      - Aggregation independently validates the smoke_row_id and focused_case_id denominators, rejects missing extra duplicate skip-like failed candidate-set-digest-mismatched or selected-artifact-digest-mismatched results, and reports smoke N/N plus focused M/M with failed identities from both.
      - U6 starts only after the shared U4 lifecycle-contract parent completes, invokes U4-owned tests without owning its production packages, and has no dependency on dynamic U4 route children.
      - This child cannot claim final qualification, freeze inputs, or create the v1.2 candidate; U9 owns the execution-only final epoch.
      - Before each source commit, exactly three independent gpt-5.6-sol/Ultra reviewers inspect and explicitly APPROVE one identical staged-diff digest; every Critical/Important finding is resolved, and any byte change invalidates all three approvals and requires three fresh reviews.
      - The PR targets gastownhall/beads:feature/backend-provider-change-20260713; creation initially supplies status/needs-review-auto and captures the returned PR number/URL; verification uses that captured identity, paginates that exact PR's GraphQL timelineItems through hasNextPage == false, finds the corresponding LabeledEvent using schema-valid Actor fragments including ... on User { databaseId } when identity is needed, accepts later status/reviewing consumption, and never re-adds the trigger.
      - A substantive accountable-human review approves the PR's current head before merge; migration/schema/sync paths never merge on bot-only approval.
    dependencies:
      - u2-authentic-harness
      - u4-transactional-apply
      - u5-candidate-pipeline
    labels:
      - e2e
      - exhaustive-matrix
      - release-qualification
    files:
      - scripts/migration-test/every-tag-test.sh
      - scripts/migration-test/fault-test.sh
      - scripts/migration-test/focused-cases.lock.json
      - scripts/migration-test/run.sh
      - .github/workflows/migration-test.yml
      - .github/workflows/cross-version-smoke.yml
      - .github/workflows/v12-upgrade-qualification.yml
    verification:
      - representative PR matrix against one packaged disposable candidate set
      - sharded complete denominator-plus-evidence-lock matrix with independent smoke/focused reconciliation
      - focused fault provider platform and install-channel suites
      - deliberate missing duplicate cross-namespace skip wrong-set-digest wrong-selected-artifact-digest and failed-result aggregator mutants
      - workflow structural/runtime test proving the UTC-daily trigger seals one disposable record, builds one U5 non-release set before complete fan-out, and no shard rebuilds or substitutes it
      - multi-row one-historical-producer-materialization mutation test
      - focused-denominator source-deletion four-axis-equivalence covered_identities-no-smoke-emission and per-tag reverse-traceability mutation tests
      - invoke U4-owned lifecycle contract/fault tests without production-directory ownership
      - make test
      - three Sol/Ultra review outputs name the final git diff --cached digest; GraphQL label-event and current-head human-review checks pass
    metadata:
      execution_agent_type: codex
      execution_suggested_model: gpt-5.6-terra
      execution_reasoning_effort: high
      execution_mode: isolated-worktree
      execution_parallel_group: wave-3-sharded
      target: feature/backend-provider-change-20260713
      review_model: gpt-5.6-sol
      review_reasoning_effort: ultra
      review_seats: "3"
      requirements_trace: "R1,R2,R6,R7,R8.5"

  - key: u7-release-gate-infra
    title: Implement and test the read-only v1.2 eligibility gate
    type: task
    priority: 0
    epic: epic-v12-universal
    description: |
      Implement a read-only eligibility evaluator and release-workflow guard
      using fixture candidate manifests and fixture qualification results.
      Independently validate N/N smoke_row_id results and M/M focused_case_id
      results, zero failures or skip-like states, matching frozen source/helper,
      candidate_set_digest, and manifest-selected candidate_artifact_digest
      values before any later human publication action can begin. The evaluator
      schema contains only frozen candidate/result data; it accepts no PR-review
      records, reviewer assertions, participant lists, credentials, or
      controller receipts.

      The evaluator may not substitute, rebuild, repair, tag, or publish. Any
      source, package, helper, workflow, build input, or artifact change
      invalidates the fixture epoch. Update concise user docs for inspect,
      dry-run, interactive/noninteractive apply, resume/restore diagnostics, and
      what remains automatic.
    acceptance_criteria:
      - Fixture tests prove the later stable publication action cannot start unless one frozen candidate set has independently complete N/N smoke rows and M/M focused cases with zero non-pass outcomes.
      - Missing extra duplicate or skipped smoke/focused result, covered_identities smoke duplication, stale result, wrong candidate set, wrong manifest-selected artifact, rebuilt candidate, and source drift mutants all block publication.
      - The gate is read-only and cannot rebuild, repair results, change source, create a tag, or publish; the later workflow can reference only digests already present in the qualified manifest.
      - User documentation clearly states that startup-safe work may auto-apply while large cross-era remote topology-changing or destructive work requires explicit bd migrate consent.
      - The final gate reports exact smoke N/N and focused M/M coverage and failed identities in each namespace without manufacturing evidence or repairing prior results.
      - This source-work child finishes before U8; U10 alone evaluates the real frozen U9 results and does not change repository bytes.
      - Before each source commit, exactly three independent gpt-5.6-sol/Ultra reviewers inspect and explicitly APPROVE one identical staged-diff digest; every Critical/Important finding is resolved, and any byte change invalidates all three approvals and requires three fresh reviews.
      - The PR targets gastownhall/beads:feature/backend-provider-change-20260713; creation initially supplies status/needs-review-auto and captures the returned PR number/URL; verification uses that captured identity, paginates that exact PR's GraphQL timelineItems through hasNextPage == false, finds the corresponding LabeledEvent using schema-valid Actor fragments including ... on User { databaseId } when identity is needed, accepts later status/reviewing consumption, and never re-adds the trigger.
      - A substantive accountable-human review approves the PR's current head before merge; migration/schema/sync paths never merge on bot-only approval.
    dependencies:
      - u5-candidate-pipeline
      - u6-qualification-infra
    labels:
      - release-gate
      - docs
    files:
      - .github/workflows/release.yml
      - scripts/release.sh
      - Makefile
      - docs/getting-started/upgrading.md
      - engdocs/RELEASE-STABILITY-GATE.md
    verification:
      - release workflow tests for smoke N/N focused M/M exact candidate_set_digest and selected candidate_artifact_digest values
      - missing skipped duplicate cross-namespace stale rebuilt set-mismatched and artifact-mismatched gate mutation tests
      - documentation command examples against the packaged candidate
      - make test
      - three Sol/Ultra review outputs name the final git diff --cached digest; GraphQL label-event and current-head human-review checks pass
    metadata:
      execution_agent_type: codex
      execution_suggested_model: gpt-5.6-terra
      execution_reasoning_effort: high
      execution_mode: isolated-worktree
      execution_parallel_group: wave-4
      target: feature/backend-provider-change-20260713
      review_model: gpt-5.6-sol
      review_reasoning_effort: ultra
      review_seats: "3"
      requirements_trace: "R4,R8.6-R8.7"

  - key: u8-freeze-build
    title: Freeze all inputs and build the exact v1.2 candidate once
    type: task
    priority: 0
    epic: epic-v12-universal
    description: |
      This is a no-source execution barrier. Verify every U0-U7 source PR and
      every discovered U4 route child is merged under repository policy and no
      helper/package/workflow change remains open. Re-run authoritative
      public-tag, release, asset, provider/topology/build, maintained
      install-channel, and focused-case discovery read-only, validate U1's denominator/probe
      specifications together with U2's applicability-evidence sidecar, and
      return to the owning source work on any lock or evidence drift.
      Fetch the remote target after all prerequisites merge and require its
      `feature/backend-provider-change-20260713` HEAD as the frozen commit.
      Finalize one freeze-record digest binding that commit/tree, build
      environment, recipes, and every production, packaging, fixture, oracle,
      qualification, release-gate, workflow, and build-input byte.

      Only after that record is immutable, invoke the already-tested U5 pipeline
      exactly once with the frozen commit/tree and freeze-record digest as hard
      stage inputs. Produce one immutable multi-platform artifact inventory and
      manifest with a single candidate_set_digest and a distinct
      candidate_artifact_digest for each platform/build entry. This is the
      candidate set that may become v1.2.
      Do not edit or commit repository bytes, rebuild per platform/shard, create
      a stable tag, or publish any release/channel. If any frozen input needs a
      change, fail this bead, return to the owning source-work child and its PR
      gates, then start a new epoch after that change merges.
    acceptance_criteria:
      - U0-U7 and every discovered U4 route child are complete and merged; a DAG check proves each route child directly blocks U8, and no source/helper/package/workflow work remains unresolved.
      - Immediately before freeze, authoritative read-only discovery exactly matches the release/tag/asset, provider/topology/build, maintained-install-channel, and focused-case locks, and U2's applicability-evidence lock is complete and bijective with U1's immutable probe specifications; drift fails without building and returns to reviewed source work.
      - After every prerequisite merge, the fetched remote feature/backend-provider-change-20260713 HEAD equals the frozen commit; one finalized freeze record binds that commit, its tree, build environment, recipes, and digests for every production, packaging, fixture, oracle, qualification, release-gate, workflow, and build-input byte.
      - The U5 build stage cannot start without the finalized freeze-record digest and an exact frozen checkout; it is invoked once and emits one immutable multi-platform candidate manifest/inventory with one candidate_set_digest and manifest-selected candidate_artifact_digest values whose source/ref/freeze/artifact identities and embedded versions verify.
      - Candidate construction changes no repository byte and performs no stable tag, GitHub release, registry publication, maintained-channel publication, or external package update.
      - A frozen-input/source-checkout mismatch, target-ref movement, or requested source change fails the epoch and requires a new U8 run after the normal source PR/review process.
    dependencies:
      - u0-integrate-history
      - u1-lock-denominators
      - u2-authentic-harness
      - u3-unified-planner
      - u4-transactional-apply
      - u5-candidate-pipeline
      - u6-qualification-infra
      - u7-release-gate-infra
    labels:
      - candidate-freeze
      - execution-barrier
    files: []
    verification:
      - verify all prerequisite PRs are merged, direct route-child blockers are complete, and the source-change queue is empty for the epoch
      - refresh authoritative cutoff denominators and compare them byte-for-byte with every denominator/probe-specification/evidence/focused-case lock
      - fetch origin and verify the frozen commit equals feature/backend-provider-change-20260713 HEAD after prerequisite merges
      - finalize and verify the freeze-record digest against the exact source tree recipes helpers workflows and build environment before build starts
      - invoke .github/workflows/v12-candidate.yml once and verify candidate_set_digest plus every manifest-selected candidate_artifact_digest
      - git status --short remains empty before and after candidate construction
    metadata:
      execution_agent_type: codex
      execution_suggested_model: gpt-5.6-terra
      execution_reasoning_effort: high
      execution_mode: no-source-controlled-run
      execution_parallel_group: wave-5-barrier
      requirements_trace: "R1,R8.1-R8.3,R8.6"

  - key: u9-exact-qualification
    title: Execute exact-candidate universal upgrade qualification
    type: task
    priority: 0
    epic: epic-v12-universal
    description: |
      This is execution-only. Distribute the byte-identical immutable U8
      candidate-set manifest and artifact inventory to every U6 workflow shard;
      each row executes only the unchanged platform/build artifact selected by
      that manifest. Run every mandatory historical
      tag/producer/topology/platform/build-flavor smoke row and every required
      deep-boundary, current-provider, fault, platform, and maintained
      install-channel focused case. Unaffected rows continue when one route
      fails; affected results emit terminal FAIL. Aggregate the smoke and
      focused namespaces independently only after each denominator is complete.

      Do not change source, helpers, manifests, fixtures, workflows, build
      inputs, manifest, inventory, or candidate artifact bytes. A needed change invalidates the epoch and
      returns to source work plus a new U8 candidate. Fetch and match the remote
      target-branch head to the frozen commit before starting any row and again
      immediately before accepting aggregation.
    acceptance_criteria:
      - Every mandatory tag/producer/topology/platform/build-flavor smoke_row_id runs against the exact U8 target-ref/commit/tree/freeze-record/candidate-set and selected-artifact identities and emits exactly one terminal PASS or FAIL.
      - Every required suite/equivalence-class/fault-case focused_case_id emits exactly one terminal PASS or FAIL; each shared case has the recorded four-axis proof, including semantic-only boundaries, and covered_identities is known-reference traceability metadata only and never satisfies or duplicates smoke.
      - Representative, exhaustive, current-provider, crash/concurrency/low-disk/corruption/ambiguity/restore, platform/build-variant, and maintained-install-channel suites all consume the same frozen epoch; current-provider cases execute the complete nonempty R6 semantic, mutation, NoOp, rollback/restore, and restored-source-reopen oracle.
      - Every result contains exactly one namespace key and the common candidate_set_digest, and its candidate_artifact_digest matches the manifest-selected platform/build entry; different authorized platform artifact hashes are valid.
      - Missing-capability routes emit terminal FAIL while unrelated rows continue; no SKIP, warning-only, timeout-as-success, unknown coverage reference, missing, duplicate, extra, or digest-mismatched result passes aggregation.
      - Final success is exact smoke N/N plus focused M/M with zero non-pass outcomes; any frozen input, manifest, inventory, or candidate artifact change invalidates all results and requires a new U8/U9 epoch.
      - The fetched remote feature/backend-provider-change-20260713 head equals the frozen commit before row execution and before aggregation acceptance; movement invalidates the epoch even when all functional rows passed.
      - Qualification changes no repository or candidate byte and creates no tag or public release.
    dependencies:
      - u8-freeze-build
    labels:
      - exact-candidate
      - release-qualification
      - execution-barrier
    files: []
    verification:
      - run the complete U6 smoke matrix and focused-case denominator against the U8 manifest
      - reconcile each namespace independently, verify the common candidate_set_digest, and match every selected candidate_artifact_digest to the manifest
      - verify smoke N/N and focused M/M and list failed identities from either namespace
      - fetch and match the remote target head before execution and immediately before aggregation acceptance
      - git status --short and frozen-input digests remain unchanged
    metadata:
      execution_agent_type: codex
      execution_suggested_model: gpt-5.6-terra
      execution_reasoning_effort: high
      execution_mode: no-source-controlled-run
      execution_parallel_group: wave-6-sharded
      requirements_trace: "R2,R6,R7,R8.4-R8.6"

  - key: u10-readonly-eligibility
    title: Evaluate exact-candidate release eligibility read-only
    type: task
    priority: 0
    epic: epic-v12-universal
    description: |
      Feed the real U8 manifest and U9 terminal results to the U7 evaluator.
      Report eligible or ineligible with separate exact smoke and focused
      coverage and failed identities. This task is read-only: it cannot rebuild, repair evidence,
      edit source, create a tag, or publish a release. An eligible result is an
      input to a later accountable-human release decision, never publication
      authority by itself.
    acceptance_criteria:
      - Eligibility is true only for the exact U8 candidate set with U9 smoke N/N, focused M/M, zero non-pass outcomes, one matching candidate_set_digest, and every result's manifest-selected candidate_artifact_digest.
      - The fetched remote feature/backend-provider-change-20260713 head, frozen commit/tree, freeze-record digest, and every candidate/result digest still match U8; target-ref movement or source mismatch is ineligible and requires a new epoch.
      - Missing, extra, stale, duplicate, skipped, cross-namespace, unknown-coverage, rebuilt, set-mismatched, artifact-mismatched, or wrong-epoch inputs produce ineligible with explicit failed identities.
      - Evaluation is read-only and changes no source, helper, result, candidate, tag, release, registry, or maintained distribution channel.
      - The result clearly separates technical eligibility from the later human decision to publish v1.2.
    dependencies:
      - u9-exact-qualification
    labels:
      - release-gate
      - read-only
      - execution-barrier
    files: []
    verification:
      - run the U7 evaluator against the immutable U8/U9 inputs
      - verify exact candidate-set/selected-artifact/frozen-input digests and independent smoke N/N plus focused M/M reconciliation
      - prove repository, candidate, result, tag, and release state are unchanged
    metadata:
      execution_agent_type: codex
      execution_suggested_model: gpt-5.6-terra
      execution_reasoning_effort: high
      execution_mode: no-source-controlled-run
      execution_parallel_group: wave-7-gate
      requirements_trace: "R8.7"
```

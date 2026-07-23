---
plan_slug: v12-universal-upgrades
phase: tasks
rig: beads
rig_root: /data/projects/beads-v12-universal
artifact_root: /data/projects/beads-v12-universal/.gc/plans
requirements_file: /data/projects/beads-v12-universal/.gc/plans/v12-universal-upgrades/requirements.md
design_file: /data/projects/beads-v12-universal/.gc/plans/v12-universal-upgrades/design.md
status: created
live_bead_reconciliation: required_before_execution
created_at: '2026-07-18T00:00:00Z'
updated_at: '2026-07-22T00:00:00Z'
created_beads_at: '2026-07-19T03:26:12Z'
---

# Task Plan: Beads v1.2 Universal Upgrades

## Summary

Reuse the existing `bd-ldt0f` umbrella epic and replace its obsolete 99-slice
execution contract with eight source-work children plus three no-source
execution barriers, while separately repairing the live root from `epic` to
custom `convoy`. The first two children start in parallel: preserve the
accepted upgrade history on the multi-provider base, and lock the complete
historical-release/provider denominator plus immutable negative-probe
specifications. Authentic E2E infrastructure then materializes historical
binaries and locks the executed applicability evidence. Qualification
infrastructure waits for the completed shared U4 lifecycle contract but not for
dynamic route-specific U4 children.

## Epic

- `epic-v12-universal` reuses `bd-ldt0f` — Deliver deterministic, robust Beads
  upgrades from every historical public release to the exact v1.2 candidate;
  it closes successfully only after accountable-human publication and passing
  postpublication public-latest verification.

## Beads

| Key | Outcome | Starts after |
|---|---|---|
| `u0-integrate-history` | The freshly fetched target plus accepted #4801/#4810/#4845 upgrade work coexist as reviewed literal ancestry on corrected PR #4907. | Now |
| `u1-lock-denominators` | Frozen tag-name/revision scope, immutable provenance, generator/profiles/recipes/probe commitment, current-origin snapshot, providers, and channel bindings are checked in and mechanically complete. | Now |
| `u2-authentic-harness` | Producer-fan-in old binaries create real workspaces, re-expand and execute applicability probes into a checked-in evidence sidecar, and produce revision-keyed strict pass/fail results against a supplied candidate set. | U0, U1 |
| `u3-unified-planner` | Startup and `bd migrate` share deterministic pre-store classification and hybrid automatic/manual policy. | U0, U1 |
| `u4-transactional-apply` | The shared storage lifecycle and route-local implementations prepare, verify, activate, resume, and restore behind the driver boundary. | U3; each route only on its own capabilities |
| `u5-candidate-pipeline` | The one-build pipeline, immutable manifest, and U1 candidate-execution union are implemented and tested with disposable artifacts; no final candidate is produced. | U0, U1 |
| `u6-qualification-infra` | Representative/nightly/exhaustive workflows, strict dual-denominator and candidate-execution aggregation, faults, platforms, and install-channel tests are implemented; unaffected rows can run. | U2, U4 shared contract, U5 |
| `u7-release-gate-infra` | Read-only eligibility logic, exact 1.2.0 source finalization, and a qualification-aware publication-only molecule/workflow with postpublication public-latest verification are implemented and fixture-tested before the byte freeze. | U5, U6 |
| `u8-freeze-build` | Latest reviewed lock scope/current-origin drift is reconciled, the exact origin/release observation and all source/helper/derivation inputs are frozen, and exactly one immutable candidate set is built. | U0-U7 and every discovered U4 route child |
| `u9-exact-qualification` | The frozen candidate set executes every mandatory smoke row and focused case, then fresh origin/release state is reconciled against U8 immediately before aggregation acceptance. | U8 |
| `u10-readonly-eligibility` | The frozen manifest and U9 results plus a repeated live-origin/release reconciliation are evaluated read-only as eligible/ineligible; no tag or release is published. | U9 |

## Dependency Graph

```text
u0-integrate-history ----+----> u2-authentic-harness ----------------+
                         |                                            |
                         +----> u3-unified-planner ---> u4 shared ----+----> u6-qualification-infra ---> u7-release-gate-infra
                         |                               |            |                 |
                         |                               `--> u4-route[*]                |
                         `----> u5-candidate-pipeline ----------------+-----------------+
                                                                      |
u1-lock-denominators ----+----> u2 / u3 / u5 -------------------------+

u0-u7 + every required u4-route[*]
             |
             v
u8-freeze-build --> u9-exact-qualification --> u10-readonly-eligibility
```

`u4-transactional-apply` completes the shared lifecycle contract and may close
after executable ownership classification emits a route-specific U4 child for
each missing primitive. Beads-owned adapter/topology gaps stay in this epic;
only proven `dolthub/driver` primitives also receive a real linked upstream
issue/PR dependency. Both kinds directly block U8. U6 depends on the completed
U4 parent but never on those dynamic children; it invokes U4-owned tests, and
affected smoke rows/focused cases still execute and emit terminal `FAIL` while
unaffected work continues. Creating a route child atomically adds it as a
direct `blocks` dependency of U8, and a DAG check compares the two sets. U8 is
the first global barrier and cannot run until all required route children pass.
Excluding dynamic route-child blockers, the fenced YAML has exactly 22 static
dependency edges and the initial ready set is exactly
`{u0-integrate-history, u1-lock-denominators}`. U5 directly depends on U1
because its pipeline consumes U1's resolution-state and branch-resolved
`candidate_execution` contracts.

U10 remains the final runnable and is read-only. The later named accountable-
human publication action is the U7-implemented release molecule/workflow, not a
twelfth child. The root carries the exact `owned` label so Gas City cannot
auto-close it on child completion. A named accountable human manually lands/
closes it only after exact-OID publication and a complete passing signed public-
latest channel receipt. A human no-publish decision is an explicit signed
cancel/supersede disposition, never successful release completion.

## Legacy Bead Reconciliation

The existing `plan:deterministic-upgrades` subtree is historical input, not the
new execution DAG.

- Preserve every already-closed bead and its commits/reasons.
- Adopt `bd-ldt0f.3.23`, `.3.24`, `.3.25`, `.3.26`, and `.3.27` into U0. Close
  them as completed only after their accepted commits become literal ancestry
  on the multi-provider branch.
- Keep contributor-disposition work such as `bd-ldt0f.1.9` separate and
  top-level (not an immediate child/member of the new convoy) unless an actual
  path overlap makes it a blocker. Do not silently supersede external
  contributor work.
- The recursive live subtree is 162 records: the 11 mapped payload runnables
  plus 151 legacy records. The exhaustive legacy partition preserves 11 already
  closed (including `bd-4velg`, a parent-child descendant under `.1` from prior
  plan SHA `b92f3957...`); adopts/reparents five `.3.23`-`.3.27` rows into U0;
  retains contributor row `.1.9` top-level; defers ten hosted/telemetry rows; and
  supersedes the remaining 124 rows to the applicable U0-U7 source-work child.
  The deferred set is `.9`, `.9.1`-`.9.5`, `.10.2`, `.12.2`, `.12.6`, and
  `.12.8`. External `bd-1gpnp` is separately deferred. This deliberately keeps
  hosted archive/object-store/KMS work valuable but outside the v1.2 upgrade
  path instead of falsely claiming U0-U7 implements it.
- Remove cross-cohort blocker edges from retained/deferred rows before
  superseding their obsolete blockers. Supersede old children before old phase
  parents. Preserve internal deferred-cohort edges and the adopted rows'
  discovered-from/bug relationships.
- Gas City convoy membership is the union of immediate parent-child children
  and `tracks`, deduplicated by ID. Before converting the root, clear or
  reparent every non-payload immediate child of `bd-ldt0f`, including closed,
  superseded, retained, and deferred legacy roots; keep only the 11 mapped
  runnables as immediate children. Do not place `.1.9` or any deferred row
  directly under the convoy. Preserve issue content/status/history while
  changing only the explicitly reconciled parent topology.
- Update `bd-ldt0f` to the requirements/design in this plan while retaining the
  former plan hash and description in notes as superseded history.

### Exact legacy reconciliation manifest

The manifest below is exhaustive over the 151 legacy records and is checked for
zero omissions and zero duplicates. Its partition assertion is exactly
`11 preserved + 5 adopted/reparented + 1 retained + 10 deferred + 124
superseded = 151`; adding the 11 mapped payload runnables proves the recursive
live-subtree total `162`.

- Preserve closed: `.1.10`, `.1.13`, `.1.14`, `.1.16`, `.1.17`, `.1.19`,
  `.1.20`, `.1.21`, `.1.22`, `.3.22`, and `bd-4velg` (parent-child under `.1`,
  previous plan SHA `b92f3957b4165cf70894eab764bd4abc31399bfbe53abefe448e3bc4f5ef1508`).
- Reparent to U0: `.3.23`, `.3.24`, `.3.25`, `.3.26`, `.3.27`.
- Retain top-level, detached from root: `.1.9`.
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
- Before source work, U1, U2, U4, U5, U6, U7, and any other macro child that
  spans more than five planned files or more than one independent subsystem
  pour exactly one bounded task-local molecule of ordered nested vertical-slice
  beads. Each slice names one end-to-end outcome, owned files, dependencies,
  acceptance, and verification; begins with a failing RED behavior test; lands
  minimal GREEN; refactors only while green; and receives independent review.
  An open or failed slice blocks only its macro parent's closure. Slice beads
  are descendants of that macro child only: they are never root tracks or
  immediate/effective convoy members and never change the exact 11 macro
  children or 22 static macro edges. The 162-record legacy assertion is the
  pre-slice reconciliation baseline. Pour and validate each bounded molecule in
  one setup pass, create no meta-planning slices, and begin implementation as
  soon as its schema/topology check passes.
- Before source work and again at U8, run the deterministic requirements-trace
  checker owned by U1. It parses every numbered R1-R8 clause and every task
  `requirements_trace`, expands ranges, emits the canonical bidirectional
  matrix, and fails on an unknown reference, an untraced requirement, a task
  with no valid reverse trace, or U7 omitting any of R8.6-R8.10. Nested slices
  inherit and narrow their macro parent's trace without creating a second
  requirements namespace.
- Before every source commit, derive the digest directly from
  `git diff --cached --binary --full-index`, retain
  those exact patch bytes immutably, and record the index tree from
  `git write-tree`. Record the resolved active hook path/digest rather than
  assuming a checkout-local hook ran. Exactly three independent
  `gpt-5.6-sol`/Ultra reviewers inspect those bytes; all three must approve
  with no unresolved Critical or Important finding. Recompute immediately
  before commit. After commit and before any push, require the commit tree to
  equal that approved index tree and the SHA-256 of
  `git diff --binary --full-index HEAD^1 HEAD` to
  equal the approved staged-diff
  SHA-256, using the first parent for merges. Any hook-induced format/fix/
  restage mismatch blocks propagation; replacement bytes are restaged and
  receive three fresh reviews, and the commit is replaced or amended only while
  unpushed before both proofs repeat.
- Every nontrivial PR needs substantive accountable-human review before merge.
  Migration/schema/sync paths never merge with bot-only approval.
- Every PR targets `gastownhall/beads:feature/backend-provider-change-20260713`,
  is created using standard `gh`/GitHub tooling with
  `status/needs-review-auto`, captures the returned PR number/URL, and verifies
  that exact PR's corresponding `LabeledEvent` through fully paginated GraphQL
  `timelineItems`. Schema-valid Actor fragments are used. If automation consumes the trigger into
  `status/reviewing`, the historical event is sufficient and the trigger is
  never re-added.

## Materialization and live-bead reconciliation

The 2026-07-19 Created-Beads mappings prove identity only. They predate this
payload, and the active materializer merely runs `bd show` for mapped children;
it does not reconcile their fields. For the mapped root it updates metadata
only. The live `bd-ldt0f` is type `epic` with no `tracks`, whereas
`gc convoy add` requires custom type `convoy`; the materializer also assumes
the first mapped child was the seed and therefore never adds `.14`. The live
topology currently has 29 immediate parent-child members and zero tracks;
adding 11 tracks alone would still leave an effective 29-member convoy because
Gas City deduplicates the union by ID. The fenced
YAML is desired state, not a truthful claim that mapped records or root already
match it. Before execution, run repository preflight and a separately reviewed
one-shot reconciler; this authoring pass does not mutate Beads.

1. Parse this file with the active Gas City parser. Require exactly one root
   convoy and the exact 11 runnable mappings `.14`, `.15`, `.17`-`.25` shown
   below. Report `.16` as an unmapped legacy/allocation collision; never infer a
   plan key or mutate it.
2. Reconcile the 11 runnable records as one projection. Reproduce the
   materializer projection exactly: `description` is the parsed
   description followed, when present, by `Suggested files/modules:` and
   `Verification:` bullet blocks separated by two LF bytes; `acceptance` is the
   LF-joined `- ` list; labels are inherited-plus-local with first-occurrence
   deduplication; metadata is inherited-plus-local plus `gc.plan.key`,
   `gc.plan.kind`, and `gc.plan.parent_convoy`; runnable `design` is the
   canonical empty string because the payload schema has no design field.
   Preserve exact LF
   text, canonical priority, raw-UTF-8-sorted label/dependency sets, and JCS
   metadata.
3. Read every mapped runnable, normalize actual JSON the same way, hash expected
   and actual projections, and emit JSON-pointer diffs for
   title, type, priority, description, design, acceptance, metadata, and labels.
   Apply `bd update` noninteractively for every differing field, then reread and
   require byte-identical canonical projections. The known Created-table U0
   title differs from the payload title and must be updated; the materializer
   alone will not repair it. For every mapped runnable, compute metadata keys to
   set and remove: `--metadata` merges and cannot prove exact JCS, so use
   repeated `--set-metadata key=value` and `--unset-metadata key` with a guarded
   row update. Labels are a separate relation and are replaced in an exclusive
   reviewed reconciliation window, then both row and label projections are
   reread exactly; current `bd` provides no label CAS.
4. Reconcile `bd-ldt0f` separately to this exact root projection. Each literal
   below is UTF-8 with LF separators and no terminal LF.

   Title: `Deliver deterministic, robust Beads upgrades`

   Type/priority/status: custom `convoy`, `P0`, `open`.

   Description:

   ```text
   Deliver a deterministic direct path from every authentic public Beads release workspace to its exact manifest-bound candidate_execution in the frozen v1.2 candidate set. Reuse existing root bd-ldt0f and replace its obsolete 99-slice execution contract with the eight source-work children and three no-source execution barriers in this plan.

   Program acceptance requires one revision-keyed result for every derived historical smoke identity, exact smoke N/N and focused M/M for the sole frozen candidate, one storage-neutral planner for startup and bd migrate, and storage effects behind the driver boundary. U10 is read-only; after its eligible result, a named accountable human may run the U7-built publication-only workflow. The owned root never auto-closes; that accountable human manually lands it only after exact-OID publication and a complete passing postpublication public-latest channel receipt.
   ```

   Design:

   ```text
   Execute the 11-runnable DAG in tasks.md. U0 and U1 are the only initial ready children; U2, U3, and U5 fan in from them; U4 supplies the shared lifecycle; U6 and U7 complete qualification and release infrastructure; U8 freezes and builds once; U9 qualifies the exact bytes; U10 evaluates eligibility read-only. Every dynamically discovered U4 route child directly blocks U8. Beads-owned adapter gaps remain in this epic and only proven dolthub/driver primitives link upstream. The root is owned and never auto-closes. The later human-controlled release molecule publishes only the exact U8 inventory and must pass every locked public-latest channel case before an accountable human manually lands the root.
   ```

   Acceptance:

   ```text
   - The live root is custom type convoy, P0, open, and has exactly the 11 immediate tracks .14, .15, and .17-.25; the runnable graph has exactly 22 static blocks edges, is acyclic, and initially readies only .14 and .15.
   - U0 preserves the fetched target plus accepted PR #4801, #4810, and #4845 histories, tests, authorship, and attribution as literal ancestry on corrected PR #4907.
   - The latest reviewed release/provider/channel locks cover every mandatory revision and branch, and the exact U8 candidate produces direct smoke N/N plus focused M/M with zero non-pass outcomes.
   - U8 freezes and builds once, U9 changes no frozen byte, and U10 reports eligibility read-only only when its fresh modeled-state reconciliation is empty.
   - The root has the owned label. A named accountable human authorizes publication; the qualification-aware workflow tags the exact frozen OID, publishes only exact U8 artifacts, and emits a complete passing signed public-latest channel receipt before that human manually lands or closes the root.
   ```

   Labels are exactly the set `plan:v12-universal-upgrades`, `upgrade-system`,
   `program-epic`, and `owned`. The root-local `owned` label is required so Gas
   City skips auto-close; root landing/closure is manual after publication and
   receipt verification. Metadata is exactly this JCS object:
   `{"architecture_model":"gpt-5.6-sol","architecture_reasoning":"ultra","gc.plan.key":"epic-v12-universal","gc.plan.kind":"convoy","implementation_model":"gpt-5.6-terra","implementation_reasoning":"high","previous_plan":"superseded:b92f3957b4165cf70894eab764bd4abc31399bfbe53abefe448e3bc4f5ef1508","requirements_trace":"Outcome,R1-R8"}`.
   Require the pre-read status already equals `open`. Snapshot `assignee`,
   `notes`, `external_ref`, and `owner` and require exact preservation; omit
   those fields and status from the update. All other unspecified storage/audit
   fields remain unchanged under `bd update`. The extra live `source-sha:*`
   label and non-plan metadata keys are intentionally replaced by the exact
   label/metadata projections above. No legacy `tracks` topology is retained;
   other topology changes only through the Legacy Bead Reconciliation section.
5. Treat closure of separate city-HQ task `mc-ucid`, owned by the Gas City/Beads
   maintainer, as a hard external precondition for all payload, root, legacy,
   edge, and membership reconciliation. Create and manage that HQ record only
   through city context (`gc bd --city /data/projects/maintainer-city ...`
   without `--rig beads`). It remains outside `bd-ldt0f`, is never parented or
   tracked by the convoy, contributes no one of the 22 static edges, and is not
   a twelfth member.

   `mc-ucid` first schema-validates
   `/data/projects/beads/.beads/metadata.json`, then performs one atomic,
   separately reviewed edit in which only `.dolt_database` changes from
   `bd_metrics_repo_2424946071` to `bd` and only `.project_id` changes from
   `8d69d5b6-0917-47a6-9761-db2b0dcca2fc` to
   `bafe313f-9fce-4972-849d-1f825740e9a5`. It must preserve
   `.database == "dolt"`, mode/backend/host/port, and every unrelated key
   byte-semantically. It then rereads and schema-validates the exact expected
   projection before running exactly:

   ```bash
   gc rig --city /data/projects/maintainer-city set-endpoint beads --inherit
   ```

   The route must be inherited `city_canonical`, never external. Its accepted
   identity is city `/data/projects/maintainer-city`; rig `beads`; rig path
   `/data/projects/beads`; endpoint `127.0.0.1:3307`; physical database
   `/data/services/gascity-local-dolt/bd`; remote
   `git+https://github.com/gastownhall/beads.git`; and the canonical
   `.dolt_database` and `.project_id` above, while `.database` remains `dolt`.
   Any schema error, unexpected changed key, preservation mismatch, or unequal
   reread fails before endpoint inheritance. After `mc-ucid` closes, set
   `GC_CITY_ROOT=/data/projects/maintainer-city` and require
   `gc rig --city "$GC_CITY_ROOT" list`,
   `gc bd --city "$GC_CITY_ROOT" --rig beads context --json`, a read-only
   `show bd-ldt0f --json`, and recursive-subtree discovery to prove that exact
   tuple and the expected root/subtree. Never rely on cwd discovery, configure
   the rig as external, override `BEADS_DOLT_SERVER_DATABASE`, or use raw SQL for
   actual reconciliation. Build the exact multiline literals above in memory,
   capture the live whole-row revision, require its status is still open, and
   execute this noninteractive CAS argv shape before any membership operation:

   ```bash
   gc bd --city "$GC_CITY_ROOT" --rig beads update bd-ldt0f --json \
     --if-revision "$ROOT_REVISION" --title "$ROOT_TITLE" --type convoy \
     --priority P0 --description "$ROOT_DESCRIPTION" \
     --design "$ROOT_DESIGN" --acceptance "$ROOT_ACCEPTANCE" \
     --set-metadata "$EXPECTED_KEY=$EXPECTED_VALUE" \
     --unset-metadata "$UNEXPECTED_KEY"
   gc bd --city "$GC_CITY_ROOT" --rig beads show bd-ldt0f --readonly --json
   gc bd --city "$GC_CITY_ROOT" --rig beads update bd-ldt0f --json \
     --set-labels plan:v12-universal-upgrades \
     --set-labels upgrade-system --set-labels program-epic --set-labels owned
   gc bd --city "$GC_CITY_ROOT" --rig beads show bd-ldt0f --readonly --json
   ```

   Stop on route/identity mismatch, CAS failure, non-open pre-read, missing
   custom `convoy` type, changed preserved field, or any unequal reread.
   Repeat `--set-metadata`/`--unset-metadata` once per computed key; omit a
   placeholder flag when its set is empty. `--set-labels` and parent/dependency
   operations are not revision-CAS-protected by current `bd`, so run the whole
   sequence in an exclusive reviewed reconciliation window and fail closed on
   any intervening revision or final-projection mismatch.
   This reviewed reconciler intentionally extends the current materializer; do
   not claim the materializer itself reconciles these root fields.
6. Derive the 22 static `blocks` edges from the parsed payload and compare them
   as a set with the live edges among the 11 mapped runnables. Remove extra
   payload-managed edges, add missing edges, and reread to exact equality.
   Only after the exact root reread, run
   `gc convoy --city "$GC_CITY_ROOT" --rig beads add bd-ldt0f <child-id> --json`
   for every missing member, explicitly including seed `.14`. Reread `tracks`
   and require the exact 11-ID set `.14`, `.15`, `.17`-`.25`, with no extra and
   no payload child under another convoy. Because convoy membership unions
   immediate parent-child children with `tracks`, also require the immediate
   child set and `gc convoy status` effective member set each equal those exact
   11 IDs. Detach/reparent every non-payload immediate legacy child before
   relying on convoy progress; never infer or mutate `.16`.
7. Emit a signed reconciliation report containing the payload digest, mapping,
   before/after record digests, field diffs, exact edge set, exact topology, child
   count, acyclic topological order, and ready set. Stop on an unknown mapping,
   duplicate, partial update, root CAS/type mismatch, `.16` inference, or any
   post-update mismatch.

After reconciliation, run the real materializer parser in validation mode:

```bash
python3 /data/projects/gascity-packs-worktrees/build-methodology-packs/gascity/assets/scripts/create_beads_from_tasks.py \
  .gc/plans/v12-universal-upgrades/tasks.md --dry-run --force
```

`files` entries remain ownership anchors, not permission for unrelated cleanup.

## Open Questions

None. Route-specific driver gaps are discovered by executable conformance tests
in U4 and handled as explicit external dependencies rather than assumed Beads
workarounds.

## Created Beads

| Key | Kind | Bead ID | Title |
|---|---|---|---|
| epic-v12-universal | convoy | bd-ldt0f | Deliver deterministic, robust Beads upgrades |
| u0-integrate-history | bead | bd-ldt0f.14 | Integrate accepted upgrade history onto the multi-provider base |
| u1-lock-denominators | bead | bd-ldt0f.15 | Lock every historical release producer and current provider topology |
| u2-authentic-harness | bead | bd-ldt0f.17 | Generalize the authentic historical-workspace harness |
| u3-unified-planner | bead | bd-ldt0f.18 | Unify pre-store classification and explicit migration UX |
| u4-transactional-apply | bead | bd-ldt0f.19 | Implement prepare verify activate resume and restore through storage |
| u5-candidate-pipeline | bead | bd-ldt0f.20 | Implement the one-build v1.2 candidate pipeline |
| u6-qualification-infra | bead | bd-ldt0f.21 | Implement universal-upgrade qualification infrastructure |
| u7-release-gate-infra | bead | bd-ldt0f.22 | Finalize v1.2 and implement qualification-aware publication |
| u8-freeze-build | bead | bd-ldt0f.23 | Freeze all inputs and build the exact v1.2 candidate once |
| u9-exact-qualification | bead | bd-ldt0f.24 | Execute exact-candidate universal upgrade qualification |
| u10-readonly-eligibility | bead | bd-ldt0f.25 | Evaluate exact-candidate release eligibility read-only |
## Bead Creation Payload

```yaml
target_rig: beads
labels:
  - plan:v12-universal-upgrades
  - upgrade-system
convoys:
- key: epic-v12-universal
  title: Deliver deterministic, robust Beads upgrades
  description: |-
    Deliver a deterministic direct path from every authentic public Beads release workspace to its exact manifest-bound candidate_execution in the frozen v1.2 candidate set. Reuse existing root bd-ldt0f and replace its obsolete 99-slice execution contract with the eight source-work children and three no-source execution barriers in this plan.

    Program acceptance requires one revision-keyed result for every derived historical smoke identity, exact smoke N/N and focused M/M for the sole frozen candidate, one storage-neutral planner for startup and bd migrate, and storage effects behind the driver boundary. U10 is read-only; after its eligible result, a named accountable human may run the U7-built publication-only workflow. The owned root never auto-closes; that accountable human manually lands it only after exact-OID publication and a complete passing postpublication public-latest channel receipt.
  dependencies: []
  labels:
    - program-epic
    - owned
  metadata:
    architecture_model: gpt-5.6-sol
    architecture_reasoning: ultra
    implementation_model: gpt-5.6-terra
    implementation_reasoning: high
    requirements_trace: "Outcome,R1-R8"
    previous_plan: superseded:b92f3957b4165cf70894eab764bd4abc31399bfbe53abefe448e3bc4f5ef1508
  beads:
  - key: u0-integrate-history
    title: Reconcile accepted upgrade history onto the current target
    type: task
    priority: 0
    description: |
      Fetch origin and bind target_oid to the then-current
      origin/feature/backend-provider-change-20260713 head. Current reviewed
      evidence records f7d0c26ec8c1e7b6b075cc49b07cb2f0f41c3a47 with sole
      parent af136f8857dd3e0461e06597f37e925088a98a49; its tree exactly
      equals merged PR #4801 final head e89ab9aa09bb178e2cfe1dec838e0e601f9663db.
      Commit a0a51638c036d25923d8671949e27a2bc12ba310 belongs to PR
      #4802, not #4801, and its tree equals accepted chain commit
      2ef7c61e0f434bf34c66d9581104d102e31c1eb1; do not merge a duplicate
      equal-tree representation.
      Existing PR #4907 head 0a0be15db29250f0ebb46793e7bcfc3b1905e245
      has parents af136f885 and dce8d066d, tree dc754da6232a69f4d585d0d3551d696e478b44c1,
      and is now DIRTY. Merged PR #4810 adoption 1b5f02efd and its final head
      share tree 696c98555545bb7df7377d5b6d9c1cd5e34c99f4; #4845 adoption
      dce8d066d and its final head share tree 61e0f715d5841c7b95b4d85b36c49c8d3dedc6f7.
      If the fetched
      target, #4907 head, or accepted PR identities differ, stop and review the
      new graph rather than executing this evidence as stale literal truth.

      Repair #4907 in place. Start from target_oid and make a no-fast-forward,
      no-commit reconciliation merge of the existing #4907 head. Resolve, stage,
      obtain three fresh independent Sol/Ultra approvals of that identical
      staged-diff digest and record its approved git write-tree index tree, and
      commit. Before push require the commit tree and first-parent diff digest
      to equal those approved values. From that reconciliation merge OID, run a
      second no-fast-forward, no-commit merge of accepted PR #4810 adoption
      1b5f02efddac224d933526b2025481f2b952f34f; separately stage, obtain three
      fresh approvals, and commit. The first correction merge has
      target_oid and the old #4907 head as ordered parents, so the PR update is
      fast-forward from its old head and retains accepted #4845 chain tip
      dce8d066de983b4fa4487890f48157a7264d86d2. The final U0 head makes the
      current target plus #4801, #4810, and #4845 accepted work literal
      ancestry. Do not cherry-pick, squash, drop, or recreate those histories.
      Assert the ordered parents exactly: reconciliation is [target_oid, prior
      #4907 head], and adoption is [reconciliation_merge_oid, 1b5f02efd].

      Conflicts and semantic overlaps are expected. Claim neither conflict-free
      nor byte-identical behavior beyond the verified accepted-tree mappings.
      Reconcile each overlap against the current target and all three PR intents;
      preserve current target work unless a reviewed compatibility resolution
      requires a change. Add a separate adaptation commit only for a failing
      multi-provider/storage-boundary behavior proved by a RED test. Keep
      server_to_embedded.sh regression-only and unregistered; production storage
      effects must use the storage/driver boundary.

      Fetch the target again before accepting the U0 head. If
      it moved from target_oid, invalidate the staged resolution and all reviews,
      reinspect the graph, and produce freshly reviewed bytes from the new head.
      Final integration uses the repository's enforced up-to-date or merge-queue
      guard bound to target_oid so base movement automatically invalidates merge
      eligibility; a manual last-minute comparison is not the sole TOCTOU guard.
    acceptance_criteria:
      - "Both provenance merges use --no-ff --no-commit. The reconciliation commit has ordered parents [fresh target_oid, prior #4907 head]; the adoption commit has ordered parents [reconciliation_merge_oid, accepted #4810 adoption 1b5f02efd]. The final U0 head is a fast-forward extension of #4907 and has target_oid, 0a0be15db, dce8d066d, and 1b5f02efd as literal ancestors."
      - "Current evidence is recorded exactly: f7d0c26 has sole parent af136f885 and tree 8b0b9d93aa0d688fef9e8a0c490011be3221141e; that tree equals #4801 final head e89ab9aa's tree, 1b5f02efd and #4810 final head afb189836 share tree 696c98555545bb7df7377d5b6d9c1cd5e34c99f4, dce8d066d and #4845 final head 454eb499e share tree 61e0f715d5841c7b95b4d85b36c49c8d3dedc6f7, and old #4907 has tree dc754da6232a69f4d585d0d3551d696e478b44c1. Any changed evidence stops execution for a reviewed replan."
      - "A file-by-file reconciliation audit proves no current-target work or accepted #4801/#4810/#4845 intent, test, authorship, or attribution was silently discarded; every divergence is explicit, test-backed, and freshly reviewed."
      - Authentic v0.49.6, v0.55.4, v0.57.0, v0.62.0, and v0.63.3 lanes pass with their expected AUTO/MANUAL behavior, plus the public v0.62 bridge suite.
      - Existing beads bd-ldt0f.3.23 through .3.26 close only after their accepted representations are literal ancestry. Bead .3.27 closes on the accepted #4845/dce8d066d representation of its v0.62 bridge outcome, not on a same-numbered literal commit; dffae48d5/09d25b54a are neither required ancestry nor an equal-tree substitution.
      - Before each source commit, including each provenance merge, exactly three independent gpt-5.6-sol/Ultra reviewers explicitly APPROVE the SHA-256 of the exact git diff --cached --binary --full-index bytes and record its git write-tree index tree; after commit and before push, the commit tree equals that approved tree and the SHA-256 of exact git diff --binary --full-index HEAD^1 HEAD bytes equals the approved staged-diff SHA-256. First-parent semantics apply to merges; any mismatch blocks propagation and only an unpushed freshly reviewed replacement/amend may proceed.
      - "Existing PR #4907 remains the integration PR targeting gastownhall/beads:feature/backend-provider-change-20260713; its body is corrected to the new graph and contributor mapping, its current head is mergeable against the same fetched target_oid, and no replacement PR silently supersedes it."
      - "Before alteration U0 captures existing PR #4907's canonical number 4907, URL https://github.com/gastownhall/beads/pull/4907, old head 0a0be15db, base ref, and body digest; after update the same PR has the corrected body and exact current U0 head. Fully paginated GraphQL timelineItems prove its existing historical status/needs-review-auto LabeledEvent, later status/reviewing consumption is accepted, and the trigger is never re-added."
      - Immediately before accepting the U0 head, a fresh fetch still resolves the target branch to target_oid; final merge is protected by an enforced base-OID-bound up-to-date/merge-queue guard whose eligibility is automatically invalidated by base movement, requiring new bytes and fresh reviews.
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
      - fetch origin and PR refs; verify the current target/head/parent/tree/merge-state evidence before resolving
      - git merge-base --is-ancestor "$target_oid" HEAD
      - git merge-base --is-ancestor 0a0be15db HEAD
      - git merge-base --is-ancestor dce8d066d HEAD
      - git merge-base --is-ancestor 1b5f02efd HEAD
      - assert reconciliation parents equal [target_oid,0a0be15db...] and adoption parents equal [reconciliation_merge_oid,1b5f02efd...]
      - verify accepted PR head/adoption tree mappings and audit the complete final diff against target_oid for preserved target and contributor behavior
      - CGO_ENABLED=1 go test -tags gms_pure_go ./internal/backendmigration/... ./internal/v062migration/...
      - ./scripts/migration-test/strict-mode-test.sh
      - make build
      - PUBLIC_V062_REAL_TARGET_BD=./bd ./scripts/migration-test/public-v062-bridge-test.sh
      - make test
      - fetch origin again and require feature/backend-provider-change-20260713 to equal target_oid; verify PR #4907's enforced up-to-date/merge-queue eligibility is bound to that exact base OID and auto-invalidates on movement
      - three Sol/Ultra review outputs name the final staged-diff digest and git write-tree; post-commit commit-tree/first-parent-diff proofs, #4907 historical label-event/current-body/current-head, and current-head human-review checks pass
    metadata:
      execution_agent_type: codex
      execution_suggested_model: gpt-5.6-terra
      execution_reasoning_effort: high
      execution_mode: isolated-worktree
      execution_parallel_group: wave-0
      target: feature/backend-provider-change-20260713
      contributor_intake_prs: "#4801,#4802,#4810,#4845"
      integration_vehicle_pr: "#4907"
      review_model: gpt-5.6-sol
      review_reasoning_effort: ultra
      review_seats: "3"
      requirements_trace: "Outcome,Base Integration,R2.1,R2.6,R6,R8.5"

  - key: u1-lock-denominators
    title: Lock every historical release producer and current provider topology
    type: task
    priority: 0
    description: |
      Check in the reviewed immutable remote/release/asset snapshot, pure
      generator, compact normalized lock, capability/family profiles, and fully
      pinned historical fallback recipes. Match the architecture verdict's
      Normative Initial U1 Evidence Fixture (NIU1EF), then treat every reviewed
      remote delta as append-only lock input. Keep current-origin observations
      separate from historical scope.

      Before source work, pour one bounded U1 molecule in this exact order:
      (1) one authenticated RefSource and its raw Git objects regenerate the same
      revision offline; (2) one exact revision produces a complete independently
      derived semantic-surface catalog and oracle contract; (3) one provider/
      runtime/platform tuple proves its four operation rows and full lifecycle
      row; (4) one producer/probe/channel identity closes with measured budget
      evidence; and (5) the full locks, mutation suites, and requirements-trace
      matrix close bijectively. Each slice follows the global RED -> minimal
      GREEN -> refactor/review contract and the ordered molecule blocks U1
      closure without becoming a root-convoy member.

      Preserve raw ref, optional annotated tag object, peeled commit, tree,
      canonical source archive, source URI, capture time, and observation
      digest. Preserve the NIU1EF retained/superseded histories and `v1.1.0`
      asset eligibility. Normalize the decisive workflow runs/jobs with all
      immutable observation fields, and require every `v1.1.0` producer to
      reference its applicable evidence rather than hardcoding associations.
      Independently discover every public tag source. Authoring evidence has
      173 names, 178 live public revisions across origin/groblegark, five
      divergent names v0.58.0-v0.62.0, and the superseded first v1.1.0 for 179
      initial revision identities; U1 freezes exact full-OID inventory rather
      than treating these diagnostics as generator constants.

      Model every discovered repository as a first-class, versioned `RefSource`
      whose content-derived ID binds canonical repository identity, class
      (`authoritative`, `maintainer_archive`, or `unverified_fork`), authority
      rationale, discovery query/method, capture time, raw-evidence locator/
      digest, and authenticity proof. Every revision references `ref_source_id`.
      Conservative extra revisions from an unverified fork remain mandatory
      compatibility rows, but cannot support an authentic-canonical-release
      claim until reviewed evidence promotes the source.

      Drive the pure generator only from an explicit content-addressed object-
      evidence root containing raw ls-remote bytes, base and peeled ref records,
      raw tag/commit objects and headers, signatures, parents, trees, ancestry
      proofs, and exact archive/tree identities. It must not discover or read an
      ambient `.git` or object database. Determinism runs outside a repository
      with `GIT_DIR` and `GIT_WORK_TREE` unset, `LC_ALL=C`, `TZ=UTC`, `umask 022`,
      and a pinned absolute PATH/tool-manifest digest. Network-enabled historical
      acquisition/build is a separate producer step consuming only those pinned
      identities; ordinary lock generation remains offline.

      Generate concrete variant families from exact branch-aware capability
      fingerprints and actual change-commit/parent/blob/ancestry evidence.
      Generate explicit standalone/shared/remote/proxied/platform/flavor
      coverage without semver ranges, tag adjacency, regex inference, or
      implicit complements. Commit only the fully specified resolved-probe leaf
      count and set digest; expose a temporary canonical stream for U2 and
      reviewers.

      For every exact `tag_revision_id`, independently derive a versioned
      semantic-surface catalog from separately enumerated schema migrations,
      public issue/types and storage interfaces, and CLI create/read/mutate/
      export surfaces. Every discovered concept is `durable` or positively
      evidenced `cache_ephemeral`; every durable concept has exactly one of
      `preserve`, `intentionally_recompute` with verified postcondition,
      `reviewed_retire` with accountable rationale/guidance, or
      `historically_absent` with exact source evidence and a negative historical-
      binary probe. Enumerate issue fields and events, wisps/relations, labels/
      comments/dependencies/readiness, custom statuses/types, config, counters/
      snapshots/compaction, routes/federation/interactions, and tombstone/
      deletion semantics without era or semver inference.

      Reconcile the NIU1EF release/asset roles plus every reviewed delta.
      Drafts, checksums, and SBOMs cannot be official producers. Give every
      producer an actionable resolution_state. A resolved row has complete
      applicable pins; an unresolved row names sorted missing_fields and reason,
      remains mandatory, and fails U1. Structured prebuilt not_applicable is
      compatible with resolved.

      Independently discover a count-free provider execution inventory using
      exactly nine canonical fields: provider_id, access_path, store_scope,
      lifecycle_owner, endpoint_kind, proxy_upstream, build_variant, platform_id,
      and engine_runtime_id. Hash all nine fields into the tuple ID. `platform_id`
      contains only exact GOOS/GOARCH; it never absorbs runtime. The independent
      `engine_runtime_id` binds distribution, exact version or image digest,
      protocol, canonical configuration digest, applicable collation/charset/
      time-zone semantics, and supported-version envelope. Use four exact operation IDs with
      operation-specific source routes. Each runtime identity binds distribution,
      exact server/binary version or image digest, protocol, canonical config,
      applicable collation/charset/time-zone semantics, and the authoritative
      supported-version envelope with explicit minimum/boundary witnesses.
      Independently derive a separate lifecycle-capability bijection across every
      route/topology/runtime/build/platform identity for inspect, quiesce,
      snapshot, prepare, verify, activate, final_verify, resume, and restore;
      every missing mandatory cell has executable ownership evidence and becomes
      the exactly owned U4/upstream blocker. Discover
      every install-looking surface and classify it, then expand every reachable
      surface/platform/manager/installer branch into an exact execution case.
      Every public_latest_only case also freezes its activation mechanism,
      accountable owner, prerequisite receipt, non-secret credential reference/
      class, bounded propagation window, budgeted retry/backoff policy, expected
      authentic public selector, and channel-specific rollback/quarantine/
      escalation action. Derive and validate the lock-wide activation DAG.
      Missing or incomplete activation data is accepted only as an explicit
      reviewed blocked/inapplicable classification and is never emitted as an
      executable case or silently skipped.
      U1 ordinary
      validation is offline and does not execute historical binaries,
      providers, package managers, or network requests; U2 owns resolved-probe
      execution and its evidence sidecar.

      Freeze a nonempty measured performance-budgets lock with runner image/
      hardware identity, CPU/filesystem calibration, at least 30 raw and
      normalized NoOp/open samples, the exact numeric ceilings consumed by U6/
      U9, and accountable-human-reviewed expiring waiver records. Own the
      deterministic requirements-trace checker and its canonical bidirectional
      matrix as a reviewed freeze input.
    acceptance_criteria:
      - "The generated initial lock contains the 173 discovered names and all 179 reviewed revision identities: 178 currently visible public (tag_name, peeled_commit_oid) pairs across origin/groblegark, including both revisions for each of v0.58.0-v0.62.0, plus superseded first v1.1.0. It matches every NIU1EF provenance state, producer eligibility, and asset role without copying diagnostics into generator constants; every later reviewed delta appends to a new reviewed lock whose derived totals become authoritative."
      - Current-origin and historical scope remain separate. Read-only reconciliation reports every addition/deletion/move, preserves base and optional peeled targets, treats ref-kind/raw-target change as a move, appends rather than replaces a changed peeled revision, and never treats retained/superseded historical absence as a delta. Qualification can pass only when the unreviewed-delta set is empty.
      - Every observed repository has exactly one content-derived RefSource record with repository identity, authoritative/maintainer_archive/unverified_fork class, authority rationale, discovery method/query, capture time, raw-evidence locator/digest, and authenticity proof; every ref/revision names its ref_source_id. Unverified-fork extras execute conservatively but neither they nor their results prove authentic canonical provenance until reviewed promotion evidence changes the source class.
      - Every revision binds immutable ref/tag-object-as-applicable/commit/tree/source-archive observations with URI, capture time, and raw-observation digest; NIU1EF retained/superseded provenance and v1.1.0 producer eligibility validate exactly.
      - The generator consumes only one explicit content-addressed git-object-evidence root containing raw ls-remote, base/peeled refs, raw tag/commit objects and headers, signatures, parents, trees, ancestry proofs, and archive/tree identities. Tests run it outside every repository with GIT_DIR/GIT_WORK_TREE unset, LC_ALL=C, TZ=UTC, umask 022, and a pinned absolute PATH/tool manifest; ambient .git/object access or a missing/mutated object fails. Network-enabled acquisition/build is a separate producer and cannot become an implicit generator input.
      - Each decisive v1.1.0 WorkflowRunEvidence locks run ID, event/full ref/head SHA, attempt, status/conclusion, timestamps, source URI/raw digest, and ordered publication-job IDs, conclusions, dispositions, timestamps, source URIs/raw digests. Every v1.1.0 producer references its relevant run/job evidence; deleting, crossing, or hardcoding the association fails.
      - Release/asset reconciliation reports every delta, preserves draft/checksum/SBOM producer ineligibility, and has distinct fixture assertions and stable diagnostics for tag_ref_missing, tag_ref_moved, tag_ref_extra, release_deleted, asset_name_changed, asset_size_changed, asset_digest_changed, draft_public_transition, and competing_nondraft_release.
      - Every concrete tag-revision/producer/topology/engine-runtime/platform/build-flavor variant matches exactly one branch-aware capability/family profile. Transition rows name the actual change commit, exact parent, before/after fingerprint and witness blobs, and valid ancestry; incomparable v0.50.0/v0.58.8 and v0.58.8/v0.59.0 branches and ordinary/nosqlite v0.58.8 remain distinct by exact fingerprints, never tag adjacency or semver.
      - For every exact tag_revision_id, semantic-surfaces.lock.json is bijective with independent enumeration of schema migrations, public issue/types and storage interfaces, and CLI create/read/mutate/export surfaces. Every concept has proved durable or cache_ephemeral classification; every durable concept has exactly one preserve/intentionally_recompute/reviewed_retire/historically_absent disposition with its required postcondition/rationale/guidance/source evidence/negative binary probe. The catalog explicitly covers issue fields and all version-present events, wisps/relations, labels/comments/dependencies/readiness, custom statuses/types, configuration, counters/snapshots/compaction, routes/federation/interactions, tombstone/deletion, topology semantics, and counts; deletion, duplicate mapping, an unclassified durable concept, silent cache exclusion, or broad era inference fails U1.
      - Source recipes fully pin commit/tree/raw go.mod/go.sum, target/flavor, canonical argv/environment/tags/ldflags/output, Go distribution digest, immutable executor and software manifest, CGO compiler/sysroot where applicable, and every referenced release/workflow/helper blob. resolution_state=resolved has complete applicable pins and empty missing_fields; unresolved has a nonempty raw-UTF-8-byte-sorted unique missing_fields list plus actionable reason and remains mandatory. Structured not_applicable:prebuilt-official-bytes can be resolved; unknown/latest/mutable/derived pins or any unresolved row fail U1, and U2/U5/U6/U8 refuse that row.
      - Canonical coverage expansion partitions every tag-revision/producer/topology/engine-runtime/platform/build-flavor identity exactly once using only finite named sets/profiles and rejects duplicate probe_id. engine_runtime_id is a required resolved-probe identity field. Each probe_digest uses SHA256("beads-u1-resolved-probe-v1\0" || RFC8785(spec_without_probe_digest)). Complete RFC8785 leaves of the spec including its verified probe_digest are sorted by raw UTF-8 probe_id bytes, prefixed by unsigned-64-bit big-endian byte length, concatenated, and committed as SHA256("beads-u1-resolved-probe-set-v1\0" || concatenated_length_prefixed_leaves); --emit-resolved-probes reproduces the stream without checking it in.
      - Proposed-inapplicable variants remain mandatory until U2's evidence lock bijectively matches the regenerated probe_id/probe_digest stream and historical binary SHA-256; missing, stale, or mismatched evidence never shrinks the denominator.
      - "The count-free provider lock is bijective with independent discovery. Tuple ID is a versioned JCS digest over exactly all nine fields (provider_id, access_path, store_scope, lifecycle_owner, endpoint_kind, proxy_upstream, build_variant, platform_id, engine_runtime_id); platform_id is only exact GOOS/GOARCH. Every independent engine_runtime_id binds distribution, exact server/binary version or image digest, protocol, canonical configuration digest, applicable collation/charset/time-zone semantics, and an authoritative supported-version envelope with explicit minimum/boundary witnesses. Embedded rows use the exact explicit runtime sentinel embedded/no-external-runtime carrying the same required semantic envelope, never null/N/A or a platform alias. The tuple retains every selector witness and has exactly four operation records: init_workspace, construct_store_generic, open_store_read_only_factory, and open_configured_cli_uow. It covers all satisfiable embedded/owned/shared/external/proxied Dolt and SQLite/PostgreSQL/MySQL combinations; CGO changes only embedded applicability absent evidence; proxy init/generic/read-only reject while configured UOW opens. Every cell executes authentically or has pinned inapplicability."
      - A separate lifecycle-capabilities.lock.json is bijective over every planner route/topology/engine_runtime_id/build/platform identity and inspect/quiesce/snapshot/prepare/verify/activate/final_verify/resume/restore operation. Each cell has executable source/interface ownership evidence and authentic success or typed missing capability; every mandatory missing cell creates the exactly owned dynamic U4 or proven upstream blocker and directly blocks U8, and the four-operation provider lock cannot substitute for this lifecycle proof.
      - The channel lock is bijective with independent discovery of every repository/doc/automation/live-catalog surface classified candidate_executing, alias, non_cli, dormant_unpublished, or retired. Case IDs digest surface/branch/platform/arch/manager/materialization; shell release/CGO-go/non-CGO-go/source, PowerShell ZIP/Go/source and overrides, npm/bun resolver and CI-skip, Homebrew bottle/source, mise, direct Go, canonical source, AUR, Nix, Winget, PyPI/non-CLI, and every new branch are explicitly classified. Every reachable case executes authentically through frozen injectable inputs or has pinned inapplicability; missing, stale, extra, duplicate, multiply mapped, branch-untraced, or relation-disagreeing cases fail.
      - Every public_latest_only row contains the complete activation mechanism/owner/prerequisite-receipt/non-secret-credential-reference-and-class/propagation-window/retry-backoff/expected-selector/rollback-quarantine-escalation contract; every prerequisite resolves and the derived activation DAG is acyclic. Missing or malformed fields, dangling prerequisites, cycles, secret material in place of a credential reference/class, or executable/runtime-skip treatment of an incomplete row fails U1; only an explicit reviewed blocked/inapplicable classification is allowed.
      - Two independent snapshot-to-lock generations run in separate temp output roots under OS-level network denial, each with distinct empty/isolated HOME, TMP, TEMP, TMPDIR, XDG_CACHE_HOME, GOPATH, GOMODCACHE, and GOCACHE and GOTOOLCHAIN=local, GOPROXY=off, GOSUMDB=off, GOENV=off; the output trees compare byte-for-byte with each other and each with checked locks. Ordinary validation executes no historical binary, provider, package manager, or network request.
      - "performance-budgets.lock.json pins runner/hardware identity, CPU/filesystem calibration and normalization, at least 30 NoOp/read-only-open samples, and hard ceilings: incremental p95 max(25 ms, 15% of direct-open baseline), p99 max(75 ms, 25%), PR 45 minutes, nightly 8 hours, U9 12 hours, row 20 minutes, producer 30 minutes, no more than 24 concurrent shards or 32 smoke plus 16 focused cases per shard, 250 MiB uploaded/shard, 20 GiB/run, 50 GiB cache, 1,500 GitHub API calls/full run, and three acquisition attempts/producer. Only an accountable-human-reviewed lock delta with rationale and expiry changes a budget; no U8/U9 runtime waiver turns failure into pass."
      - The deterministic requirements-trace checker parses every numbered R1-R8 clause and task requirements_trace, expands ranges, and emits a canonical bidirectional matrix; unknown/untraced references, a task without reverse trace, or U7 missing any R8.6-R8.10 clause fails before source work and again at U8.
      - Mutation tests reject frozen/current-scope conflation, missing/misclassified RefSource or authenticity evidence, ambient-Git or incomplete raw-object inputs, malformed base/peeled refs, provenance loss, workflow-evidence loss/misbinding, v1.1.0 asset misbinding, draft/checksum/SBOM producer promotion, false tag adjacency, wrong transition parent/ancestry, missing/duplicate profile, changed witness blob/raw hash, deleted/duplicated/misclassified semantic surfaces or missing negative probes, backend/mode/constructor/root/build-tag/CGO/runtime/platform/lifecycle mutations, ordinary/nosqlite conflation, mutable or incomplete pins, invalid resolution_state, raw-blob newline loss, probe leaf/count/set/order/length-prefix changes, provider/lifecycle operation-route disagreement, budget mutations, and channel-binding mismatch. Two rows differing only by engine_runtime_id remain distinct; deleting either row, omitting runtime from any tuple/probe hash, or folding runtime into platform fails. Each failure emits a stable class plus offending entity key and field/delta.
      - Before each source commit, exactly three independent gpt-5.6-sol/Ultra reviewers explicitly APPROVE the SHA-256 of the exact git diff --cached --binary --full-index bytes and record its git write-tree index tree; after commit and before push, the commit tree equals that approved tree and the SHA-256 of exact git diff --binary --full-index HEAD^1 HEAD bytes equals the approved staged-diff SHA-256. First-parent semantics apply to merges; any mismatch blocks propagation and only an unpushed freshly reviewed replacement/amend may proceed.
      - The PR targets gastownhall/beads:feature/backend-provider-change-20260713; creation initially supplies status/needs-review-auto and captures the returned PR number/URL; verification uses that captured identity, paginates that exact PR's GraphQL timelineItems through hasNextPage == false, finds the corresponding LabeledEvent using schema-valid Actor fragments including ... on User { databaseId } when identity is needed, accepts later status/reviewing consumption, and never re-adds the trigger.
      - A substantive accountable-human review approves the PR's current head before merge; migration/schema/sync paths never merge on bot-only approval.
    dependencies: []
    labels:
      - test-infrastructure
      - release-catalog
      - ready-first
    files:
      - scripts/migration-test/release-snapshot.json
      - scripts/migration-test/ref-sources.lock.json
      - scripts/migration-test/git-object-evidence.lock.json
      - scripts/migration-test/releases.lock.json
      - scripts/migration-test/generate-releases-lock.py
      - scripts/migration-test/capability-profiles.json
      - scripts/migration-test/family-profiles.json
      - scripts/migration-test/semantic-surfaces.lock.json
      - scripts/migration-test/recipes/historical/
      - scripts/migration-test/providers.lock.json
      - scripts/migration-test/lifecycle-capabilities.lock.json
      - scripts/migration-test/install-channels.lock.json
      - scripts/migration-test/performance-budgets.lock.json
      - scripts/migration-test/check-requirements-trace.py
      - scripts/migration-test/manifest-test.sh
      - scripts/migration-test/lib/manifest.sh
    verification:
      - ./scripts/migration-test/manifest-test.sh
      - ./scripts/migration-test/manifest-test.sh --offline-determinism performs the two-run network-denied isolated-environment byte comparison against each other and checked locks
      - RefSource identity/class/authenticity and unverified-fork non-authority fixtures plus outside-repository ambient-Git-denial/raw-object/signature/ancestry mutation tests
      - NIU1EF initial-fixture, retained/superseded provenance, workflow-run/job evidence, v1.1.0 producer-reference, and asset-role tests
      - branch/transition/fingerprint fallback-pin raw-blob semantic-surface-disposition/negative-probe and candidate-binding mutation suites
      - resolved-probe emit/count/exact-set-digest plus provider-operation-route/runtime/platform/lifecycle-capability/channel-mapping bijection mutation suites
      - measured NoOp/open calibration plus every numeric time/concurrency/case-count/upload/cache/API/acquisition budget boundary and reviewed-expiring-waiver mutation suite
      - public-latest activation-schema/DAG mutation suites cover every required field, dangling prerequisite receipts, cycles/topological order, non-secret credential reference/class, incomplete reviewed classification, expected-selector binding, propagation/retry-budget shape, and rollback/quarantine/escalation mapping
      - distinct remote diagnostic fixtures plus read-only delta tests prove all deltas are reported, historical absence is not a delta, and current drift cannot rewrite frozen scope
      - live-ref fixture proves 121 current-origin base refs plus 70 peeled rows are 191 raw observations while the cross-remote historical revision denominator is 179, preventing unit conflation
      - scripts/migration-test/check-requirements-trace.py emits the reviewed bidirectional matrix and rejects unknown/untraced/orphan references plus any U7 omission in R8.6-R8.10
      - git diff --check
      - three Sol/Ultra review outputs name the final staged-diff digest and git write-tree; post-commit commit-tree/first-parent-diff proof plus GraphQL label-event and current-head human-review checks pass
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
      requirements_trace: "R1.1-R1.9,R2.3,R6,R7.4,R8.1"

  - key: u2-authentic-harness
    title: Generalize the authentic historical-workspace harness
    type: task
    priority: 0
    description: |
      Replace hardcoded-version control flow with manifest-selected rows while
      preserving the accepted representative sentinel tests. Before row fan-out,
      pour one bounded U2 molecule in this exact order: (1) one authenticated
      producer is acquired once and fanned out immutably; (2) one historical
      workspace creates and mutates every applicable semantic-catalog concept,
      migrates, reopens, and verifies it independently; (3) one remote/server/
      proxy row proves containment, redaction, and fail-closed networking; and
      (4) the complete probe-evidence and smoke/focused schemas close with all
      negative mutants. Each ordered slice follows RED -> minimal GREEN ->
      refactor/review and blocks U2 closure without changing the macro DAG.

      Before row fan-out,
      fetch or build each unique historical producer exactly once using the key
      peeled source commit/platform/build flavor/recipe digest/toolchain digest and
      distribute its immutable verified bytes through workflow-internal,
      digest-addressed transport while retaining every consuming
      tag_revision_id. Row workers never fetch from external origins or compile.
      Re-expand U1's complete canonical resolved-probe stream, verify its leaf
      count/set digest, execute it with those exact binaries, and write a
      separate checked-in evidence lock bound to probe_id, probe_digest, and
      binary SHA-256; pending or missing evidence leaves the identity mandatory.
      Acquisition consumes only U1-pinned archive/tree/object evidence and retains
      its RefSource class/authenticity proof. Unverified-fork rows still execute
      conservatively, but the harness and result schema forbid their evidence from
      asserting authentic canonical-release provenance.

      For each selected row, create and mutate a nonempty isolated workspace
      with the fan-in binary, capture the independent pre-state, run the exact
      manifest-bound candidate_execution from the supplied frozen candidate
      set, apply the explicit route when needed, and verify fidelity,
      post-upgrade mutation, reopen, and idempotent rerun. Packaged rows execute
      exact packaged bytes; wrapper/source-derived rows follow their locked
      union contracts. Refuse every unresolved producer before selection.
      Build the workspace and oracle from the exact U1 semantic-surface catalog:
      create/mutate/observe every applicable durable concept; verify every
      preserve disposition, recompute postcondition, and reviewed retirement;
      execute the negative historical-binary probe for every historically absent
      concept; and retain positive evidence for cache/ephemera exclusions. This
      explicitly covers all issue fields and events, wisps/relations, custom
      statuses/types, configuration, counters/snapshots/compaction, routes/
      federation/interactions, tombstone/deletion, and topology/count semantics.

      Emit terminal PASS/FAIL for every assigned smoke row and focused case.
      Acquisition failure, timeout,
      unsupported host, unavailable historical Dolt, empty source data, bad
      checksum, source mutation, missing result, or indeterminate probe is FAIL.
      Cache immutable inputs by digest and keep concise per-row diagnostics.
      Remote/server/proxy rows use row-exclusive ephemeral endpoints and
      credentials, a sanitized DSN environment, production-endpoint rejection,
      and execution-time egress denial except to declared row endpoints.

      Every terminal result carries its own revision/source binary or build,
      frozen helper, candidate set and candidate_execution union, environment,
      external-Dolt, and download identities. Packaged executions retain exact
      candidate_artifact_digest matching; wrappers prove the recovered payload;
      frozen-source derivations bind every pin and realized output without false
      packaged-byte equality. Run each row with temporary
      HOME/XDG/repository/database roots, repository-local hooks, path/inode and
      endpoint guards against production, and redacted terminal and retained
      output. U2 owns separate smoke_row_id and focused_case_id result schemas,
      the feature matrix, and their self-tests. Every historical identity,
      resolved probe, result, and covered identity carries engine_runtime_id
      independently from platform_id; source-derived rows retain that runtime
      boundary rather than collapsing it into derivation pins. covered_identities is
      traceability-only metadata. U6 owns scheduling, sharding, and independent
      reconciliation of both denominators.
    acceptance_criteria:
      - The harness deterministically regenerates U1's engine-runtime-bearing resolved-probe stream, verifies its leaf count/set digest, validates applicability-evidence.lock.json bijectively by probe_id/probe_digest/historical-binary SHA-256/engine_runtime_id, and treats pending, missing, stale, runtime-collapsed, or mismatched evidence as a still-mandatory identity rather than a skip.
      - Producer fan-in keyed by exact peeled source commit/platform/build flavor/recipe digest/toolchain digest fetches or builds every unique historical artifact once per run and fans out immutable digest-verified bundles through workflow-internal transport; every consumer stays bound to tag_revision_id, and row workers may retrieve only those bundles and cannot fetch from external origins or compile.
      - Every acquired producer is bound to its U1 RefSource record and pinned archive/tree/object evidence. Authoritative and maintainer-archive authenticity claims require their locked proof; an unverified-fork row remains mandatory and may pass compatibility while its result is mechanically barred from proving authentic canonical-release provenance.
      - Historical binaries, not current code or synthetic fixtures, create every qualifying workspace.
      - Both NIU1EF v1.1.0 tag revisions execute as mandatory identities with exactly their locked source/official producer eligibility and workflow-evidence references.
      - Every assigned smoke_row_id is exact tag-revision/producer/topology/engine-runtime/platform/build-flavor and emits one direct PASS or FAIL; every assigned focused_case_id is exact suite/equivalence-class/fault-case and emits one PASS or FAIL; every result has exactly one namespace key, and covered_identities contains only known revision-keyed smoke identities including engine_runtime_id and never emits, satisfies, reuses, or duplicates a smoke result.
      - Each PASS or FAIL embeds tag_revision_id, engine_runtime_id and its distribution/version-or-image/protocol/config/semantic-envelope digest independently from platform_id (the exact GOOS/GOARCH pair), source tag/commit/tree and source-binary digest or fully resolved source-build recipe/tool identities; frozen helper tree; candidate commit/tree and one candidate_set_digest; OS/architecture/executor identity and allowlisted environment; external engine binary/version/digest or the locked `embedded/no-external-runtime` value; origin plus digest for every download; and exactly one valid candidate_execution branch. Packaged bytes carry the manifest-selected candidate_artifact_digest, wrappers carry wrapper plus matching recovered-payload digests, and frozen-source derivations carry source/ref/recipe/executor/toolchain/compiler/sysroot identities as applicable plus realized-output SHA-256 and verified embedded version without erasing the distinct engine-runtime boundary.
      - Before either binary runs, each row proves fresh row-local HOME, XDG, repository, and database roots; repository-local core.hooksPath with global/system hooks disabled; containment of every BEADS_DIR, BEADS_DB, BD_DB, and provider storage path; and realpath/inode exclusion of the production checkout, its .beads, and configured production databases.
      - Every remote/server/proxy row proves row-exclusive ephemeral endpoints and credentials, an allowlisted sanitized DSN environment, normalized-alias-aware production/shared-endpoint rejection before execution, and execution-time egress denial except to its declared test endpoints; acquisition/build networking is unavailable in row workers.
      - Terminal results and retained logs redact credentials, authenticated URLs, tokens, user names, hosts, DSNs, and filesystem locators while preserving immutable digests and opaque diagnostic identities.
      - The version-gated oracle is driven bijectively by semantic-surfaces.lock.json and individually checks issue ID/title/description/status/priority/type/timestamps/assignee/owner/external reference/custom metadata; every version-present event; wisps/relations; labels/comments/dependencies/readiness; custom statuses/types; repository/workspace configuration; counters/snapshots/compaction; routes/federation/interactions; tombstone/deletion; all applicable semantic counts; workspace/repository identity; and applicable branch/remote/redirected/shared/server/proxied topology semantics.
      - Post-upgrade checks individually exercise create, field update, dependency add/remove with readiness transitions, close, semantic export comparison, issue reopen, store reopen, completed-migration NoOp, and another reopen.
      - Every catalog concept executes its locked preserve/recompute/retire duty or its exact historically-absent negative historical-binary probe; cache_ephemeral exclusions require positive source evidence. Every named field, relationship, topology semantic, count, and operation emits a supported cell and executes its comparison/operation, or emits absent with pinned source-boundary evidence plus a negative probe; silence, grouped shorthand, deleted/duplicate catalog IDs, and empty fixtures fail.
      - Refusal, dry-run, failed apply, and rollback paths prove the source unchanged byte-for-byte or through the strongest driver-provided immutable identity, and rollback reopens and semantically verifies the restored source.
      - Harness self-tests fail on tampered binaries, source mutation, deleted oracle cells, false absence, partial snapshots, results with both/neither namespace key, unknown covered_identities references, missing/duplicate cross-namespace results, covered_identities smoke duplication, wrong/missing tag_revision_id or engine_runtime_id, conflated v1.1.0 revisions, two rows differing only by runtime being deduplicated, deletion of either runtime row, platform/runtime conflation, PATH shadowing, wrong candidate set, invalid packaged/wrapper/source-derived union or digest/pin, ambient HOME/hooks/database leakage, cross-row endpoint leakage, normalized production DSNs/endpoints, undeclared egress, production-path aliases, unredacted sentinels, erased diagnostic identities, and every missing provenance field.
      - Before each source commit, exactly three independent gpt-5.6-sol/Ultra reviewers explicitly APPROVE the SHA-256 of the exact git diff --cached --binary --full-index bytes and record its git write-tree index tree; after commit and before push, the commit tree equals that approved tree and the SHA-256 of exact git diff --binary --full-index HEAD^1 HEAD bytes equals the approved staged-diff SHA-256. First-parent semantics apply to merges; any mismatch blocks propagation and only an unpushed freshly reviewed replacement/amend may proceed.
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
      - focused authentic sentinel runs plus both NIU1EF v1.1.0 revisions
      - complete semantic-surfaces catalog execution, disposition/postcondition, cache-exclusion, and negative historical-binary-probe mutation suites
      - RefSource authenticity-class propagation and unverified-fork canonical-authority refusal tests
      - strict tamper mutation partial-result revision-conflation and candidate-execution-union tests
      - multi-row one-producer-materialization and immutable-fan-out mutation test
      - resolved-probe regeneration/count/set-digest/evidence-lock bijection and historical-binary execution tests
      - isolation provenance version-gating redaction cross-row-leakage production-DSN undeclared-egress and production-path-alias self-tests
      - bash -n and ShellCheck on changed shell files
      - three Sol/Ultra review outputs name the final staged-diff digest and git write-tree; post-commit commit-tree/first-parent-diff proof plus GraphQL label-event and current-head human-review checks pass
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
      requirements_trace: "R1.2,R1.4,R1.6-R1.7,R2.1-R2.6,R6,R7.1-R7.4"

  - key: u3-unified-planner
    title: Unify pre-store classification and explicit migration UX
    type: feature
    priority: 1
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
      Inspection returns both the storage-neutral observation and an opaque
      `InspectionWitness` bound to raw configuration, selected route/topology,
      source identity, and capability digests. Every provider open, including
      `NoOp` and nominally read-only factory opens, must consume that witness plus
      the expected route. The factory may neither reselect nor provision from
      reread configuration. Before any open-time effect, one adapter/driver-owned
      atomic validate-and-open capability either opens the exact route or returns
      typed `replan_required`/`open_refused`; a missing primitive is a U4-owned or
      proven-upstream blocker.
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
      - Every provider open, including NoOp and read-only factory open, consumes the planner's opaque InspectionWitness and expected route; the factory cannot reselect provider/route/topology or provision from reread configuration. The adapter/driver atomically validates raw-config, route, topology, source-identity, and capability digests before any effect and returns typed replan_required/open_refused on mismatch.
      - Zero-effect race tests mutate each witness-bound input between inspect and every NoOp/read-only/mutating open and prove no open, provision, engine launch, version tracking, telemetry, auto-import, or metadata write occurs. Absence of the atomic validate-and-open primitive produces the exactly owned U4/upstream capability blocker rather than a best-effort Beads check.
      - Every mutating adapter path, including StartupSafe, calls the same driver-owned quiesce/re-probe/full-plan-recompute gate and refuses before its first write on source, capability, configuration, topology, or plan-byte drift.
      - bd migrate --inspect and --dry-run are read-only; bd migrate and --yes show/use the same plan and explicit consent semantics.
      - Current Dolt, SQLite, PostgreSQL, and MySQL workspaces remain on their provider and legacy evidence is never provisioned as a fresh current store.
      - Help, version, migrate inspection/dry-run, and applicable doctor diagnostics remain usable while ordinary commands are blocked.
      - Before each source commit, exactly three independent gpt-5.6-sol/Ultra reviewers explicitly APPROVE the SHA-256 of the exact git diff --cached --binary --full-index bytes and record its git write-tree index tree; after commit and before push, the commit tree equals that approved tree and the SHA-256 of exact git diff --binary --full-index HEAD^1 HEAD bytes equals the approved staged-diff SHA-256. First-parent semantics apply to merges; any mismatch blocks propagation and only an unpushed freshly reviewed replacement/amend may proceed.
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
      - witness/expected-route contract tests plus raw-config route topology source capability race mutants for NoOp read-only and mutating opens, all with zero-effect spies
      - CGO_ENABLED=1 go test -tags gms_pure_go ./internal/upgrade/... ./internal/backendmigration/... ./cmd/bd/...
      - make test
      - three Sol/Ultra review outputs name the final staged-diff digest and git write-tree; post-commit commit-tree/first-parent-diff proof plus GraphQL label-event and current-head human-review checks pass
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
      requirements_trace: "R3.1-R3.6,R4"

  - key: u4-transactional-apply
    title: Implement prepare verify activate resume and restore through storage
    type: feature
    priority: 1
    description: |
      Begin with executable conformance tests for the storage/driver capabilities
      required by each manual route. Expose the minimum storage-neutral
      inspection, quiescence, prepare, verification, activation, checkpoint,
      resume, and restore contract. Keep provider/engine details and durable
      authority in internal/storage or dolthub/driver.

      Before source work, pour one bounded U4 molecule in this exact order:
      (1) witness-bound NoOp/read-only open validates atomically with zero effects
      on every race; (2) one operation acquires authenticated durable authority,
      CAS revision, and fencing; (3) one manual route prepares/verifies/activates
      with authority last; (4) crash at each persisted boundary rediscovers and
      idempotently resumes or restores; and (5) the full lifecycle-capability
      matrix classifies every missing primitive to its owner. Each slice uses RED
      -> minimal GREEN -> refactor/review and blocks U4 closure only.

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

      Implement the U3 `InspectionWitness` contract behind the storage boundary.
      Every open—including `NoOp` and read-only open—atomically validates its
      witness and expected route's raw-config, route, topology, source-identity,
      and capability digests before any open-time effect, or returns typed
      `replan_required`/`open_refused`.

      Persist discoverable authenticated operation state behind the driver/
      adapter boundary. Its storage-neutral envelope contains operation ID,
      source/target/canonical-plan digests, monotonically fenced authority epoch/
      token, authenticated state digest, CAS revision, and current lifecycle
      boundary while the implementation-specific handle remains opaque. Every
      transition compares and swaps the expected revision and presents the
      current fencing token; stale actors and foreign source/target/plan digests
      fail before effect. Crash recovery rediscovers state and makes resume and
      restore idempotent from every persisted boundary without any Beads-side
      storage-specific recovery table, flock, engine query, or inferred completion.

      If a required primitive is absent, use executable interface/adapter
      evidence to identify its owner. A Beads-owned internal/storage adapter or
      topology gap, including applicable SQLite, PostgreSQL, MySQL, server-Dolt,
      or proxy behavior, creates a real dynamic U4 route child in this epic. Only
      a primitive demonstrably owned by dolthub/driver creates a linked upstream
      issue/PR dependency. Both child kinds directly block U8 and only the
      affected route. Do not patch around a driver-owned boundary or add Beads-
      side engine inspection, flocking, storage-specific retry loops, file-copy
      cutover, or inferred crash completion.

      Before touching any overlapping migration/schema path, rerun repository
      preflight for migration/schema keywords and PR #4878, review maphew's open
      fix/migration-chain-hardening head first, and prefer adoption/fixup on the
      contributor branch. Preserve its tests and attribution and record the
      exact disposition; never silently replace, close, or supersede it.
    acceptance_criteria:
      - Every mutating route proves driver-owned quiescence and complete pre-write recomputation; every supported manual route additionally proves side-by-side preparation, independent verification, activation-last, resume, and restore.
      - Every provider open, including NoOp/read-only open, passes the opaque inspection witness and expected route to one adapter/driver atomic validate-and-open operation; each raw-config/route/topology/source/capability race returns typed replan_required/open_refused with zero open/provision/version/telemetry/metadata effects. A missing atomic primitive is an owned route blocker.
      - Under quiescence the complete observation/capability/route/safety/step/prerequisite/verification plan is recomputed and exactly matched to the initial inspected plan for StartupSafe or the consented plan for manual execution; source, capability, configuration, topology, or plan drift refuses before mutation.
      - Failed verification never changes authority, and storage authority plus configuration/topology selection are the final mutation before read-only final verification.
      - Failure injection at every lifecycle boundary leaves exactly one authoritative reopenable store.
      - Resume and restore are idempotent, and restore tests reopen and verify the original semantic oracle.
      - Every mutating operation persists authenticated discoverable state binding operation/source/target/plan digests, authority epoch/fencing token, authenticated state digest, CAS revision, and lifecycle boundary. Read-only discovery returns only the generic envelope plus opaque handle; every transition uses revision CAS/current fencing, stale or foreign actors fail before effect, and crash restart resumes or restores idempotently at every persisted boundary.
      - U4 executes every applicable lifecycle-capabilities.lock.json inspect/quiesce/snapshot/prepare/verify/activate/final_verify/resume/restore cell for every route/topology/engine_runtime_id/build/platform identity. Missing authentication, discovery, fencing, CAS, or lifecycle support creates the exactly owned dynamic U4/proven-upstream blocker and directly blocks U8; no four-operation provider row or Beads-side recovery substitute satisfies it.
      - "Executable interface/adapter conformance evidence assigns every missing capability to exactly one owner: Beads-owned internal/storage adapter/topology gaps create dynamic U4 children here, while only proven dolthub/driver primitives create linked upstream dependencies; either child blocks only its route and directly blocks U8, and no boundary-violating substitute lands in Beads."
      - Credentials, locators, and driver internals do not leak through errors, logs, or public SDK types.
      - PR #4878 receives contributor-first preflight and review before overlapping work; its tests and attribution are preserved, disposition is recorded, and any necessary rewrite is explained and credited on that PR rather than silently replacing or superseding it.
      - After the shared lifecycle contract is complete, U4 may close once every proven missing primitive has a route-specific U4 child owned in this epic and every driver-owned child additionally has a real linked dolthub/driver dependency; creation atomically adds either kind as a direct blocks dependency of U8, and each child's acceptance criteria literally repeat the staged-diff/index-tree/post-commit equality gate, initial status/needs-review-auto, captured returned PR identity, fully paginated exact-PR LabeledEvent/no-re-add, and accountable current-head human-review gates. Affected smoke rows/focused cases report terminal FAIL until the route passes.
      - A DAG validator proves the set of discovered U4 route children exactly equals U8's direct route-child blocker set, that U6 depends on the completed U4 parent, and that no route child blocks U6.
      - Before each source commit, exactly three independent gpt-5.6-sol/Ultra reviewers explicitly APPROVE the SHA-256 of the exact git diff --cached --binary --full-index bytes and record its git write-tree index tree; after commit and before push, the commit tree equals that approved tree and the SHA-256 of exact git diff --binary --full-index HEAD^1 HEAD bytes equals the approved staged-diff SHA-256. First-parent semantics apply to merges; any mismatch blocks propagation and only an unpushed freshly reviewed replacement/amend may proceed.
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
      - table-driven ownership classifier fixtures prove Beads adapter/topology gaps remain in-epic, only demonstrable dolthub/driver primitives link upstream, and both child sets exactly block U8
      - scripts/pr-preflight.sh --search "migration schema" --repo gastownhall/beads and scripts/pr-preflight.sh 4878 --repo gastownhall/beads before overlapping implementation and again before PR creation
      - race tests mutate source capability configuration topology and plan inputs between initial inspection and quiesced recomputation for both StartupSafe and manual paths
      - zero-effect witness-bound open races mutate raw config route topology source and capability digests for NoOp read-only and mutating paths
      - DAG test proves U6 depends on completed shared U4 while every discovered U4 route child directly blocks U8 and none blocks U6
      - lifecycle-capability lock execution covers inspect quiesce snapshot prepare verify activate final_verify resume and restore per runtime/platform route
      - process-crash failure injection persists and rediscovers authenticated operation state at every lifecycle and resume/restore transition boundary
      - stale-fencing foreign-digest CAS-race repeated-resume repeated-restore and completed-no-op tests
      - CGO_ENABLED=1 go test -tags gms_pure_go ./internal/storage/... ./internal/backendmigration/... ./cmd/bd/...
      - make test
      - three Sol/Ultra review outputs name the final staged-diff digest and git write-tree; post-commit commit-tree/first-parent-diff proof plus GraphQL label-event and current-head human-review checks pass
    metadata:
      execution_agent_type: codex
      execution_suggested_model: gpt-5.6-terra
      execution_reasoning_effort: high
      execution_mode: isolated-worktree
      execution_parallel_group: wave-2
      target: feature/backend-provider-change-20260713
      external_dependency_policy: classify-owner-from-interface-adapter-evidence-and-link-upstream-only-for-proven-driver-gap
      contributor_intake_prs: "#4878"
      contributor_pr_disposition: contributor-first-adopt-or-record-explained-rewrite
      review_model: gpt-5.6-sol
      review_reasoning_effort: ultra
      review_seats: "3"
      requirements_trace: "R3.6,R4,R5.1-R5.7"

  - key: u5-candidate-pipeline
    title: Implement the one-build v1.2 candidate pipeline
    type: task
    priority: 1
    description: |
      Implement and test the pipeline that will later freeze one commit/tree,
      build/package exactly once, and emit an immutable candidate-set manifest
      with version, source commit/tree, platform/build artifact names, SHA-256
      digests, build environment, and the complete frozen-input digest
      inventory. The SHA-256 of the exact immutable manifest bytes containing
      that inventory is candidate_set_digest; each platform/build artifact has
      its distinct candidate_artifact_digest. The manifest also carries the
      exact candidate-execution discriminated union: packaged bytes, wrappers
      that must recover packaged bytes, and fully pinned frozen-source
      derivations with expected embedded-version inputs that never claim
      GoReleaser byte equality. Disposable tests may materialize and hash those
      derivations; the U8 manifest must not require an unrealized output digest.
      Pipeline tests use explicitly disposable artifacts only; this child must
      not construct or name the final v1.2 candidate.

      Before source work, pour one bounded U5 molecule in this exact order:
      (1) an invalid build record/ref/tree refuses before compiler or packager;
      (2) one packaged platform produces the manifest-selected byte and digest
      exactly once; (3) wrapper and frozen-source branches prove their distinct
      execution contracts without false byte equality; and (4) the complete
      publishable inventory closes with every rebuild/repack/substitution mutant
      rejected. Each vertical slice follows RED -> minimal GREEN -> refactor/
      review and blocks U5 closure without changing macro membership.

      The build entry point requires an immutable build record, target-ref
      identity, and source commit/tree as hard inputs before any compiler or
      packaging step starts. Release mode accepts only U8's finalized freeze
      record. Nightly prequalification mode accepts a disposable record that
      binds the tested commit/tree and complete build-input inventory and marks
      every output permanently release-ineligible. It verifies the supplied
      checkout and record exactly, and refuses a missing/mismatched record,
      wrong checkout, or moved target ref without invoking the build.

      Reject unauthorized go run, an in-tree packaged substitute, ambient PATH,
      packaged per-shard rebuilds, an unpinned source derivation, mismatched
      embedded version, helper substitution, or a candidate manifest that does
      not describe the supplied U1 relation and bytes. Refuse any candidate or
      historical producer whose `resolution_state` is unresolved. No stable tag, release,
      registry publication, or maintained-channel publication occurs here.
    acceptance_criteria:
      - Pipeline fixture tests prove one-build behavior and a complete immutable source/helper/build-input/artifact/candidate-execution inventory using a single candidate_set_digest plus distinct candidate_artifact_digest values for packaged artifacts marked disposable.
      - The ordered U5 nested molecule closes only after its invalid-input refusal, packaged-byte, wrapper/source-derived, and complete-inventory vertical slices each pass their RED/GREEN/refactor/review gate; the slices remain U5 descendants and add no root member or macro edge.
      - All consumer fixtures verify candidate_set_digest, frozen-input inventory, epoch, and exactly one U1 union branch before running — packaged bytes match the selected candidate_artifact_digest, wrappers recover a payload with that digest, and frozen-source derivations match every source/ref/recipe/executor/toolchain/compiler/sysroot pin plus expected-version inputs. Disposable materialization tests then verify embedded version and record realized-output SHA-256 without packaged-byte equality; the release schema does not require those unrealized values at U8.
      - Pipeline tests prove a build cannot start without matching target-ref/source-commit/tree and a mode-appropriate immutable build record; missing or mismatched records, wrong checkouts, or moved refs invoke no compiler or packaging step.
      - Disposable prequalification records bind the same immutable inputs as release records but are domain-separated and publication-ineligible; U8/U9 reject their candidate sets, and release mode rejects them before any compiler or packaging step.
      - Wrong-binary, PATH-shadow, unauthorized rebuild, renamed-artifact, stale-manifest, candidate-set mismatch, selected-artifact mismatch, wrapper-payload mismatch, incomplete/mutable derivation, false source-derived artifact equality, and mixed-platform mutants fail before qualification.
      - The workflow can later build one candidate after U8's freeze barrier, but this child cannot emit a final-candidate manifest or downstream release-qualification result.
      - No stable tag, GitHub release, registry publication, maintained-channel publication, or external package update is performed by this task.
      - Before each source commit, exactly three independent gpt-5.6-sol/Ultra reviewers explicitly APPROVE the SHA-256 of the exact git diff --cached --binary --full-index bytes and record its git write-tree index tree; after commit and before push, the commit tree equals that approved tree and the SHA-256 of exact git diff --binary --full-index HEAD^1 HEAD bytes equals the approved staged-diff SHA-256. First-parent semantics apply to merges; any mismatch blocks propagation and only an unpushed freshly reviewed replacement/amend may proceed.
      - The PR targets gastownhall/beads:feature/backend-provider-change-20260713; creation initially supplies status/needs-review-auto and captures the returned PR number/URL; verification uses that captured identity, paginates that exact PR's GraphQL timelineItems through hasNextPage == false, finds the corresponding LabeledEvent using schema-valid Actor fragments including ... on User { databaseId } when identity is needed, accepts later status/reviewing consumption, and never re-adds the trigger.
      - A substantive accountable-human review approves the PR's current head before merge; migration/schema/sync paths never merge on bot-only approval.
    dependencies:
      - u0-integrate-history
      - u1-lock-denominators
    labels:
      - release-candidate
      - packaging
    files:
      - scripts/migration-test/candidate.sh
      - .github/workflows/v12-candidate.yml
      - .goreleaser.yml
    verification:
      - disposable candidate build and manifest self-test
      - candidate-set digest plus candidate-execution union and embedded-version verification on each supported platform
      - wrong-binary PATH-shadow stale-manifest packaged wrapper source-derivation and mixed-digest mutation tests
      - build-record-to-build hard-dependency tests prove invalid mode ref commit tree record or checkout invokes no build step
      - make build
      - three Sol/Ultra review outputs name the final staged-diff digest and git write-tree; post-commit commit-tree/first-parent-diff proof plus GraphQL label-event and current-head human-review checks pass
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
      requirements_trace: "R1.6,R2.3,R8.1-R8.4,R8.6"

  - key: u6-qualification-infra
    title: Implement universal-upgrade qualification infrastructure
    type: task
    priority: 1
    description: |
      Implement fast representative PR lanes, a scheduled nightly lane, and a
      deterministically sharded exhaustive matrix. Every mandatory
      tag-revision/producer/topology/engine-runtime/platform/build-flavor identity has one direct
      smoke_row_id PASS/FAIL result. Every deep equivalence, fault,
      current-provider, platform, and install-channel case has a separate
      focused_case_id PASS/FAIL result. Deep tests execute every four-axis
      equivalence class, including semantic-only boundaries.

      Before source work, pour one bounded U6 molecule in this exact order:
      (1) one representative semantic/provider/runtime row executes end to end;
      (2) producer and source-derivation fan-ins prove one immutable
      materialization per identity; (3) one current-provider lifecycle/fault row
      executes every applicable lifecycle cell; (4) deterministic sharding and
      independent smoke/focused aggregation fail each denominator mutant; and
      (5) PR/nightly/exhaustive/install-channel lanes emit and enforce measured
      performance receipts. Each slice follows RED -> minimal GREEN -> refactor/
      review and blocks U6 closure only.

      Add focused crash, competing actor, low-disk, corruption, ambiguous
      provider evidence, rollback/reopen, platform, and maintained install-channel
      tests. Current-provider cases reuse the full nonempty R6 oracle, including
      issues/fields, labels/comments, dependency add/remove and readiness
      transitions, counts, mutations, close/reopen, semantic export comparison,
      completed NoOp, rollback/restore, and restored-source reopen where
      applicable. Per-tag-revision
      direct smoke may not be shared. A deep/fault case may be
      shared only with recorded equality of physical format/schema, selected
      route/ordered plan, provider/topology/engine runtime, and version-gated
      semantic duties including source-derived boundaries,
      while retaining covered_identities as traceability-only metadata.
      Aggregate the smoke and focused denominators independently against the
      frozen-scope/current-origin snapshot and delta, denominator/evidence
      locks, focused-cases registry, install-channel inventory,
      candidate_set_digest, and each result's candidate_execution union.
      Packaged and wrapper relations retain manifest-selected
      candidate_artifact_digest guarantees; source-derived relations validate
      all frozen pins and realized output SHA-256 without packaged equality.
      U6 consumes U2's result schemas and feature
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
      Execute U4's complete lifecycle-capability matrix by route/topology/
      engine_runtime_id/build/platform, not merely the four provider-open rows.
      Consume U1's measured performance lock, record raw measurements plus
      hardware calibration/normalization, and fail every time/resource/API/
      concurrency or normalized NoOp/open regression; runtime waivers cannot
      convert failure to pass.
    acceptance_criteria:
      - The lock-derived matrix assigns every mandatory tag-revision/producer/topology/engine-runtime/platform/build-flavor smoke_row_id exactly once and every required suite/equivalence-class/fault-case focused_case_id exactly once, with no success-bearing skip or missing result path; stable sharding hashes the complete identity including engine_runtime_id. The focused denominator is independently derived from the feature/equivalence matrix, provider lock, install-channel lock, and focused-cases registry.
      - Representative PR lanes include v0.49.6, v0.55.4, v0.57.0, v0.62.0, v0.63.3, manifest-selected v1.0 and both v1.1.0 revision sentinels, current providers, and rollback/fail-before-open.
      - Deep coverage executes every manifest-derived four-axis equivalence class, including a semantic feature/operation boundary with unchanged format/schema/route/topology, rather than a hardcoded era list.
      - Every shared deep/fault focused case has an equivalence proof that separately matches physical/schema, route/plan, provider/topology/engine_runtime_id, and semantic/source-derived boundaries, and covered_identities reverse traceability includes engine_runtime_id; that metadata never emits, satisfies, reuses, or duplicates a smoke_row_id, and per-tag-revision direct smoke is never shared.
      - Current Dolt, SQLite, PostgreSQL, and MySQL fresh-create, defensive-open, provider-local schema, and same-provider cases execute for every locked topology, engine_runtime_id, GOOS/GOARCH, and shipped CGO/non-CGO build variant; they consume the complete semantic-surfaces catalog and nonempty R6 oracle including events, wisps/relations, custom status/type, config, counters/snapshots/compaction, routes/federation/interactions, tombstone/deletion, mutations, completed NoOp, rollback/restore, and restored-source reopen, and emit PASS or route-local FAIL; only embedded Dolt to PostgreSQL is admitted as backend-changing.
      - Every applicable lifecycle-capabilities.lock.json inspect/quiesce/snapshot/prepare/verify/activate/final_verify/resume/restore cell executes for its route/topology/runtime/build/platform identity. A missing cell emits a terminal affected-route FAIL while unrelated rows continue, and the four-operation provider matrix cannot count as lifecycle coverage.
      - Crash, concurrency, low-disk, corruption, ambiguity, restore/reopen, and platform suites fail closed. Every discovered reachable surface/installer-branch/platform/manager case executes its locked union relation or has pinned inapplicability; fault injection forces every installer decision branch and validates its machine-readable trace. Missing, duplicate, multiply mapped, relation-disagreeing, or unresolved selections fail.
      - A checked-in UTC-daily schedule seals one disposable prequalification record and invokes the U5-built pipeline once in non-release mode to build/package one publication-ineligible candidate set before complete-matrix fan-out; every shard receives the identical candidate_set_digest/inventory/contracts and cannot substitute a candidate-execution relation or rebuild packaged bytes. Source-derived outputs materialize once per fully pinned derivation identity into one canonical inventory with one digest/version per identity.
      - U2's exact-key producer fan-in materializes each unique historical artifact once per run before fan-out and distributes immutable bytes through workflow-internal digest-addressed transport; a multi-row mutation test fails any repeated external fetch/build or row-worker compilation.
      - Missing driver capabilities fail only affected mandatory results; unaffected rows remain runnable and the final smoke N/N plus focused M/M barrier still cannot pass.
      - Aggregation independently validates the smoke_row_id and focused_case_id denominators, tag_revision_id and engine_runtime_id references, regenerated runtime-bearing resolved-probe count/set digest and evidence bijection, stable runtime-bearing shard assignment, covered_identities, and each candidate_execution union; it rejects missing extra duplicate skip-like failed wrong-revision/runtime candidate-set-mismatched packaged/wrapper-digest-mismatched unpinned-source-derived or false-byte-equality results and reports smoke N/N plus focused M/M with failed identities from both.
      - Every lane consumes performance-budgets.lock.json and emits a signed measurement receipt with runner/hardware and CPU/filesystem calibration, raw/normalized values, sample count, waiver identity if any, and lock digest. At least 30 NoOp/open samples must meet incremental p95 max(25 ms, 15% baseline) and p99 max(75 ms, 25%); PR <=45 minutes, nightly <=8 hours, row <=20 minutes, producer <=30 minutes, <=24 shards, <=32 smoke plus 16 focused/shard, <=250 MiB upload/shard, <=20 GiB/run, <=50 GiB cache, <=1,500 API calls/full run, and <=3 acquisition attempts/producer. A missing receipt, expired/unreviewed waiver, or exceeded gate fails.
      - U6 starts only after the shared U4 lifecycle-contract parent completes, invokes U4-owned tests without owning its production packages, and has no dependency on dynamic U4 route children.
      - This child cannot claim final qualification, freeze inputs, or create the v1.2 candidate; U9 owns the execution-only final epoch.
      - Before each source commit, exactly three independent gpt-5.6-sol/Ultra reviewers explicitly APPROVE the SHA-256 of the exact git diff --cached --binary --full-index bytes and record its git write-tree index tree; after commit and before push, the commit tree equals that approved tree and the SHA-256 of exact git diff --binary --full-index HEAD^1 HEAD bytes equals the approved staged-diff SHA-256. First-parent semantics apply to merges; any mismatch blocks propagation and only an unpushed freshly reviewed replacement/amend may proceed.
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
      - complete lifecycle-capability route/runtime/platform matrix and semantic-surface oracle receipts
      - calibrated performance-boundary tests cover every latency duration shard/case upload/cache/API/acquisition ceiling and reject expired/unreviewed runtime waivers
      - deliberate missing duplicate cross-namespace skip wrong-revision wrong-set packaged wrapper source-derivation false-equality and failed-result aggregator mutants
      - workflow structural/runtime test proving the UTC-daily trigger seals one disposable record, builds one U5 non-release set before complete fan-out, and no shard rebuilds or substitutes it
      - multi-row one-historical-producer-materialization mutation test
      - focused-denominator source-deletion four-axis-equivalence covered_identities-no-smoke-emission and per-tag-revision reverse-traceability mutation tests
      - runtime-only-distinct rows remain separate through probe ID, smoke ID, covered identities, sharding, result schema, and aggregation; deletion/deduplication or source-derived runtime-boundary collapse fails
      - invoke U4-owned lifecycle contract/fault tests without production-directory ownership
      - make test
      - three Sol/Ultra review outputs name the final staged-diff digest and git write-tree; post-commit commit-tree/first-parent-diff proof plus GraphQL label-event and current-head human-review checks pass
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
      requirements_trace: "R1,R2,R6,R7,R8.3-R8.5"

  - key: u7-release-gate-infra
    title: Finalize v1.2 and implement qualification-aware publication
    type: task
    priority: 0
    description: |
      Implement a read-only eligibility evaluator and release-workflow guard
      using fixture candidate manifests and fixture qualification results.
      Independently validate N/N smoke_row_id results and M/M focused_case_id
      results, zero failures or skip-like states, matching frozen source/helper,
      candidate_set_digest, tag_revision_id, engine_runtime_id independent of
      platform_id, and candidate_execution values
      before any later human publication action can begin. Packaged and wrapper
      executions prove selected candidate_artifact_digest equality; source-
      derived executions prove the frozen derivation and realized digest. Also
      implement the read-only evaluator input guard that acquires current-origin and
      release/asset and external-channel observations, validates each capture's
      raw bytes/digest internally, and makes the evaluator compare canonical
      modeled state with U8. The evaluator schema contains
      only frozen candidate/result/reconciliation data; it accepts no PR-review
      records, reviewer assertions, participant lists, credentials, or
      controller receipts.

      Before source work, pour one bounded U7 molecule in this exact order:
      (1) fixture inputs emit one read-only `EligibilityDecision`; (2) exact
      1.2.0 source/version surfaces and disposable probes agree; (3) an eligible
      decision produces a separate uploaded/attested bundle and
      `BundleSealReceipt`; (4) production capability/trust preflight and the
      signed conditional claim authorize one resumable run; (5) the temporary
      exact-ref freeze, protected stable tag, and dispatch-acceptance receipt
      execute with honest trust boundaries; (6) checkpointed byte promotion
      resumes idempotently; and (7) public-latest activation failure compensates
      in reverse DAG order. Each vertical slice starts RED, lands minimal GREEN,
      refactors/reviews while green, and blocks U7 closure only.

      U7 is also the final source-changing version/release-source owner. First
      write RED fixture tests that make scripts/update-versions.sh fail on a
      missing or multiply matched field and make scripts/check-versions.sh
      discover and check every updater surface plus Copilot, default.nix, all
      Windows winres JSON/XML values, the MCP local-package uv.lock entry,
      managed hook markers when in scope, and README/released-doc policy.
      Immediately before U8, derive and review `version_date` as the intended
      UTC publication date in strict YYYY-MM-DD form, not an authoring-date
      literal. Finalize the reviewed top
      `## [1.2.0] - <version_date>` CHANGELOG section and prepend the exact
      nonempty cmd/bd/info.go entry with Version 1.2.0 and Date
      `<version_date>`. Test text/JSON bd info --whats-new and require
      versionChanges[0].Version == Version with matching changelog/info dates.
      Then run ./scripts/update-versions.sh 1.2.0, refresh
      integrations/beads-mcp/uv.lock with pinned tooling, regenerate any in-
      scope managed hooks, run the expanded checker, and prove disposable
      packaged, wrapper, and every source-derived branch reports exact v1.2.0.

      Rewrite the real .beads/formulas/beads-release.formula.toml,
      scripts/release.sh, and .github/workflows/release.yml under structural
      tests. The evaluator emits one canonical `EligibilityDecision` containing
      input/target/freeze/baseline/result digests, counts, outcome/reasons,
      evaluator version, and `decision_digest`, with no bundle digest, locator,
      expiry, receipt, claim, or publication field. Only after an eligible
      decision is final may a distinct append-only seal operation invoke U7's
      bundler. Bundle bytes bind `decision_digest`, U8 freeze/candidate/
      publishable-artifact locators, U9 results/materialization inventory,
      target/baselines, version_date, and channel lock, but never their own digest
      or receipt. A separate `BundleSealReceipt` records decision/bundle/artifact
      digests, authenticated locator, signer, seal time, 24-hour claim expiry,
      and retention policy. Evaluation stays read-only; sealing creates only the
      bundle, attestation, and receipt outputs.

      The production seal backend is `actions/upload-artifact@v4` with
      `retention-days: 90`. Its authenticated locator binds gastownhall/beads,
      run ID, artifact ID, and the API-reported SHA-256, and an API reread must
      prove at least 90 days from seal. `actions/attest@v4` signs the bundle digest
      as a custom in-toto/DSSE predicate through GitHub Actions OIDC and the
      GitHub/Sigstore Fulcio/Rekor public-good trust chain. Verification uses a
      pinned upgraded `gh attestation verify` and locks repository, exact signer
      workflow/ref, frozen source commit, subject digest, GitHub-hosted runner,
      and trust-root version. The bundle-writer `GITHUB_TOKEN` is separately
      limited to read/actions/OIDC/attestation/artifact-metadata permissions.
      GitHub artifacts and attestations make substitution detectable, not WORM:
      promise 90-day availability from seal, then retain identical bundle and
      signed receipt-chain bytes as stable release assets until the later of 90
      days after manual landing and every rollback deadline. Literal object lock
      requires a separately provisioned reviewed external backend before U8, not
      a Beads service.

      Production publication requires the human-provisioned `beads-release`
      environment with required reviewer, prevent-self-review, and API-verified
      UI setting `can_admins_bypass:false`; dedicated stable-tag and claim-tag
      protections; and one environment-gated dedicated release GitHub App. That
      App has `contents:write` for protected claim/release tags and assets,
      `actions:write` only for detailed dispatch, and `administration:write` only
      for temporary exact-ref ruleset create/read/delete. The ordinary Actions
      token cannot administer rules or publish. Current observed missing
      environment/credentials, unprotected target with no effective rule,
      bypass-bearing v* rule, tag-triggered rebuilding/--clobber workflow,
      seven-day artifacts, and unupgraded attestation CLI are failing evidence
      that U8's production-capability preflight must reject.

      Serialize canonical `ClaimIntent` bytes binding version, decision/bundle/
      guard digests, `run_id`, `triggering_actor`, `authority`,
      `claim_expires_at`, and `nonce` while
      explicitly excluding attestation locator/digest/signature fields. Hash and
      Sigstore-attest that fixed intent. Only afterward serialize a
      `ClaimEnvelope` and annotated claim-tag message binding the intent digest
      plus the resulting attestation locator/digest. Create that annotated Git
      tag object, then conditionally create unique
      `refs/tags/beads-release-claims/v1.2.0`. HTTP 201 wins. HTTP 409/422 may
      resume only after canonical ClaimIntent bytes/digest, Sigstore attestation,
      ClaimEnvelope/tag object, and ref all verify the exact same run ID,
      triggering actor, authority, bundle, and nonce, with only run attempt
      changing. Persist and revalidate that chain after crashes between intent
      hash, attestation, tag-object creation, conditional ref creation, and claim-
      receipt persistence; foreign/mismatched/expired authority rejects. Never
      update, delete, or reclaim the claim ref.

      After accountable-human approval and immediately before mutation, rerun
      eligibility/live reconciliation, seal the guard, and create a temporary
      active exact-ref ruleset named `beads-v12-release-freeze` for update and
      deletion with an empty operational bypass list. Require HTTP 201, capture
      the ruleset ID and request digest, poll both rule and effective-rule APIs,
      and reread the target ref before every mutation. Ruleset drift/deletion,
      bypass change, 403/404 indeterminacy, or ref movement stops publication.
      This is a best-effort ordinary-writer freeze, not an atomic lease or
      transaction; repository/organization administrators are the explicit
      control-plane trust boundary and Actions concurrency is only run
      serialization. With no intervening approval/wait, create or verify the
      separately protected annotated `v1.2.0` at the exact frozen OID; that tag
      becomes immutable release authority. Delete the temporary ruleset by exact
      ID with an audited absence reread only after detailed dispatch acceptance,
      returned-run verification, and receipt persistence. Never commit or push main, and remove release.yml's
      tag trigger and rebuilding/--clobber behavior.

      Only after the protected annotated stable tag exists, dispatch release.yml
      with exact bundle/guard identities, explicit `ref:v1.2.0`, and
      `return_run_details:true`. Before releasing the temporary ruleset, query
      the returned run and require event `workflow_dispatch`, `head_sha` equal to
      the frozen OID, and the exact locked workflow ID/path plus workflow blob
      digest at that OID. Bind the request ref, HTTP 200 run ID/URLs, verified
      event/head/workflow/blob, candidate/guard, and observation into the signed
      `DispatchAcceptanceReceipt`; it proves acceptance but no transaction with
      ruleset/ref/tag. The existing formula/molecule checkpoint
      store remains authoritative and persists a signed hash-chain receipt for
      every claim, tag, release, asset, and channel step. Same-claim/same-guard
      existing state is digest-verified and resumes idempotently after crash;
      foreign or mismatched collisions reject, and stale actors cannot resume.

      Dispatch release.yml with exact bundle/guard identities. It may only
      download, hash-verify, and publish U8-built Go archives, checksums, corpus,
      SBOM, npm tarball, and MCP wheel/sdist. After U8 it must structurally reject
      source/changelog/info/version edits, commits or main pushes, and every
      reconstruction, rebuild, repack, regeneration, replacement, or promotion
      of candidate/publishable inventory. Isolated consumer-side builds/installs
      remain allowed solely for locked U9 and postpublication authentic
      Homebrew-source, AUR, Nix, and shell/PowerShell Go/source fallback cases;
      their ephemeral outputs record derivation/version/digest in the result or
      receipt and never enter, replace, promote, or repair U8 inventory. After
      publication it first executes the complete lock-derived activation DAG in
      dependency order. Each activation requires its prerequisite receipt and
      validates the accountable-owner and non-secret credential-reference/class
      bindings; observation and retries stay within the row's bounded propagation
      window and budgeted retry/backoff policy. Only then does it run every
      applicable U1-locked public_latest_only case through its expected authentic
      public selector, verify exact v1.2.0 plus the manifest relation/digest where
      available, and emit the signed immutable operational channel receipt.
      Missing or incomplete data for an otherwise applicable row fails closed;
      only prior reviewed inapplicability removes the runtime obligation, never a
      skip. Failure or exhausted propagation/retry budget invokes the lock-defined
      compensation or quarantine for every already activated channel in reverse
      activation-DAG order with an idempotent signed receipt chain. Resume
      continues compensation; partial compensation escalates and leaves release/
      root incomplete. It never moves/deletes/reuses the protected tag or rebuilds
      1.2.0.

      The evaluator and U10 may not substitute, rebuild, repair, tag, or publish. Any
      source, package, helper, workflow, build input, or artifact change
      invalidates the fixture epoch. Any live observation failure or exact
      tag/release/asset/visibility/publication delta also invalidates the fixture
      epoch without rewriting frozen scope. The publication-only path is the
      separate human-authorized consumer of those read-only results. Update concise user docs for inspect,
      dry-run, interactive/noninteractive apply, resume/restore diagnostics, and
      what remains automatic.
    acceptance_criteria:
      - Fixture tests prove the later stable publication action cannot start unless one frozen candidate set has independently complete N/N smoke rows and M/M focused cases with zero non-pass outcomes.
      - Missing extra duplicate or skipped smoke/focused result, wrong/missing tag_revision_id or engine_runtime_id, runtime-collapsed covered_identities, runtime-only row deletion/deduplication, stale result, wrong candidate set, invalid packaged/wrapper/source-derived execution or source-derived runtime-boundary collapse, rebuilt packaged candidate, unpinned derivation, source drift, unavailable live observation, and post-U8 tag/release/asset/visibility/publication/external-channel drift mutants all block publication.
      - Fixture tests prove both pre-aggregation and eligibility checkpoints preserve each fresh raw observation/digest separately and reconcile its normalized ref/release/asset/visibility/publication state against U8's frozen observation to an empty delta; they reject every unreviewed delta, never rewrite the frozen observation/scope, and require reviewed source work plus a new U8.
      - Positive drift fixtures use different capture timestamps and JSON serialization with identical modeled state and pass; internal raw-bytes/digest mismatch and every modeled-field drift fail independently.
      - scripts/check-versions.sh is bijective with every tracked release-version surface; updater fixtures fail independently for missing/multiple substitution; ./scripts/update-versions.sh 1.2.0 and pinned uv lock refresh are applied; every full-semver field is 1.2.0, PE/XML numeric fields are 1.2.0/1.2.0.0, the MCP local lock entry is 1.2.0, the first versioned changelog heading is 1.2.0 dated exactly version_date, versionChanges[0] has matching Version/Date and nonempty reviewed guidance, and every source/disposable packaged/wrapper/source-derived probe reports exact v1.2.0 before U8.
      - version_date is reviewed immediately before U8 as the intended UTC publication date and matches strict YYYY-MM-DD; U8 freezes it. Malformed, changelog/info mismatch, and publication-after-date fixtures fail, and a UTC-date slip requires reviewed source finalization plus a new U8/U9/U10 epoch.
      - The U7 evaluator and U10 gate are read-only and cannot rebuild, repair results, change source, create a tag, or publish; the separate publication-only workflow can reference only digests already present in the qualified bundle/manifest.
      - User documentation clearly states that startup-safe work may auto-apply while large cross-era remote topology-changing or destructive work requires explicit bd migrate consent.
      - The final gate reports exact smoke N/N and focused M/M coverage and failed identities in each namespace without manufacturing evidence or repairing prior results.
      - The publication entrypoint accepts one immutable bundle locator/digest and derives version/target from it. Accountable-human/environment approval precedes the final guard; immediately before mutation the workflow reruns the evaluator/fresh live reconciliation, rejects stored/replayed eligibility, and seals a guard binding exact candidate_set_digest, target OID/tree, freeze-record digest, U8 baseline digest, U9 materialization-inventory digest, and fresh-observation digest.
      - U7's evaluator emits EligibilityDecision with no bundle/locator/expiry/claim/publication fields. A distinct append-only seal step builds non-self-referential bytes binding decision_digest and emits BundleSealReceipt with decision/bundle/artifact digests, authenticated locator, signer, seal time, 24-hour claim expiry, and retention; sealing failure cannot rewrite the already-final eligibility decision or any frozen input.
      - The executable seal uses actions/upload-artifact@v4 with retention-days:90 and an authenticated repository/run/artifact locator plus API SHA-256/expiry, followed by actions/attest@v4 custom in-toto/DSSE predicate and pinned gh attestation verify under exact repository/workflow/ref/source-commit/hosted-runner/trust-root policy. This is substitution-detectable, not WORM; availability is 90 days from seal, then identical bundle/receipt bytes remain release assets through landing+90d and all rollback windows.
      - Production-capability tests require the gated beads-release environment, protected stable/claim tags, separate limited GITHUB_TOKEN/OIDC seal permissions, and one dedicated release App whose contents:write, actions:write, and administration:write uses are limited respectively to tag/assets, detailed dispatch, and temporary ruleset management. The currently missing environment/credentials, unprotected target/no effective rule, bypass-bearing v* rule, tag-trigger/rebuild/--clobber path, seven-day retention, or unverified attestation tooling fails U8 preflight.
      - Canonical ClaimIntent bytes exclude every attestation locator/digest/signature field, bind version/decision/bundle/guard/run_id/triggering_actor/authority/claim_expires_at/nonce, and are hashed then Sigstore-attested. ClaimEnvelope and the annotated claim tag bind intent_digest plus the resulting attestation locator/digest. HTTP 201 conditional ref creation wins; 409/422 and every crash-boundary resume revalidate byte-identical intent/digest, attestation, envelope/tag object, ref, and the same explicit fields with only run attempt changing. Foreign/mismatched/expired state rejects and the claim ref is never updated/deleted/reclaimed. Negative fixtures that substitute only authority must change the ClaimIntent digest and invalidate the prior attestation, ClaimEnvelope/tag, collision read, and resume.
      - Structural/integration tests prove approval precedes the final guard; the temporary exact-ref ruleset is active/effective and the target equals the frozen OID before every mutation. This is observational best-effort ordinary-writer freezing, not an atomic lease/transaction; admins are the control-plane trust boundary. The protected annotated stable tag at the exact OID becomes release authority; same-claim/same-guard tag/release/asset state verifies/resumes, while foreign collisions reject.
      - release.yml has no unparameterized push.tags trigger and accepts only authenticated parameterized dispatch with exact bundle/guard inputs and ref:v1.2.0 after the protected annotated tag exists; a stray v* tag and a dispatch with absent/substituted inputs cannot publish or mutate a channel.
      - release.yml consumes the exact bundle/guard and publishes only hash-verified U8 inventory bytes. The U8 inventory already contains every Go archive, checksum, corpus, SBOM, npm tarball, and MCP wheel/sdist; any missing byte fails back to pre-U8 source work. Negative tests reject every post-U8 reconstruction/rebuild/repack/regeneration/replacement/promotion of candidate or publishable inventory while positive tests allow only locked isolated consumer verification builds/installs whose ephemeral outputs are derivation/version/digest receipt-bound and never enter or repair that inventory.
      - Dispatch occurs only after protected annotated v1.2.0 exists, uses ref:v1.2.0 and return_run_details:true, then before ruleset deletion verifies the returned run has event workflow_dispatch, head_sha equal to the frozen OID, and exact locked workflow identity/path/blob digest. DispatchAcceptanceReceipt binds all request/run/ref/event/head/workflow/blob/guard values. The formula's signed hash-chain checkpoints every claim/tag/release/asset/channel step and resumes same authority idempotently; foreign/stale actors reject. Postpublication execution completes the activation DAG, then authentic selectors; terminal failure compensates/quarantines every activated channel in reverse DAG order with idempotent signed receipts, and partial compensation leaves release/root incomplete without repairing U9.
      - This source-work child finishes before U8; U10 alone evaluates the real frozen U9 results and does not change repository bytes.
      - Before each source commit, exactly three independent gpt-5.6-sol/Ultra reviewers explicitly APPROVE the SHA-256 of the exact git diff --cached --binary --full-index bytes and record its git write-tree index tree; after commit and before push, the commit tree equals that approved tree and the SHA-256 of exact git diff --binary --full-index HEAD^1 HEAD bytes equals the approved staged-diff SHA-256. First-parent semantics apply to merges; any mismatch blocks propagation and only an unpushed freshly reviewed replacement/amend may proceed.
      - The PR targets gastownhall/beads:feature/backend-provider-change-20260713; creation initially supplies status/needs-review-auto and captures the returned PR number/URL; verification uses that captured identity, paginates that exact PR's GraphQL timelineItems through hasNextPage == false, finds the corresponding LabeledEvent using schema-valid Actor fragments including ... on User { databaseId } when identity is needed, accepts later status/reviewing consumption, and never re-adds the trigger.
      - A substantive accountable-human review approves the PR's current head before merge; migration/schema/sync paths never merge on bot-only approval.
    dependencies:
      - u5-candidate-pipeline
      - u6-qualification-infra
    labels:
      - release-gate
      - docs
    files:
      - .beads/formulas/beads-release.formula.toml
      - .github/workflows/release.yml
      - scripts/release.sh
      - scripts/release_script_test.go
      - scripts/release_bundle.go
      - scripts/release_bundle_test.go
      - scripts/release-capabilities.lock.json
      - scripts/release-trust.lock.json
      - scripts/version_scripts_test.go
      - scripts/update-versions.sh
      - scripts/check-versions.sh
      - CHANGELOG.md
      - cmd/bd/version.go
      - cmd/bd/info.go
      - cmd/bd/info_test.go
      - plugins/beads/.claude-plugin/plugin.json
      - plugins/beads/.codex-plugin/plugin.json
      - plugins/beads/.copilot-plugin/plugin.json
      - .claude-plugin/marketplace.json
      - integrations/beads-mcp/pyproject.toml
      - integrations/beads-mcp/src/beads_mcp/__init__.py
      - integrations/beads-mcp/uv.lock
      - npm-package/package.json
      - default.nix
      - cmd/bd/winres/winres.json
      - cmd/bd/winres/manifest.xml
      - README.md
      - RELEASING.md
      - scripts/README.md
      - docs/getting-started/upgrading.md
      - engdocs/RELEASE-STABILITY-GATE.md
    verification:
      - release formula/entrypoint/workflow structural tests for decision/seal split, non-self-referential ClaimIntent hash/attest then ClaimEnvelope/tag binding, every claim crash boundary, authority-only substitution rejection at ClaimIntent digest/attestation/envelope/tag/collision-read/resume, honest ruleset/ref rechecks, exact-OID protected annotated-tag authority with no source-branch push, same-guard resume/foreign-collision refusal, and reverse-DAG compensation
      - release evaluator tests for smoke N/N focused M/M exact candidate_set_digest tag_revision_id engine_runtime_id candidate_execution and frozen-versus-live origin/release observation values
      - post-tag dispatch fixture requires ref:v1.2.0 and return_run_details, verifies returned event workflow_dispatch, frozen head_sha, exact workflow ID/path/blob before ruleset deletion, and checks complete DispatchAcceptanceReceipt binding
      - upload-artifact@v4 90-day/API-digest locator, actions/attest@v4 Sigstore policy, non-WORM retention/copy, trust-lock, substitution, claim-resume, and BundleSealReceipt tests
      - public-latest activation-DAG structural/runtime fixtures prove dependency order, prerequisite-receipt gating, owner/non-secret credential-reference/class binding, bounded propagation and exact retry/backoff enforcement, expected-selector authenticity, complete applicable-case coverage, reviewed inapplicability versus forbidden skip, timeout/exhaustion behavior, and channel rollback/quarantine/escalation with signed receipt binding
      - RED/GREEN per-surface mutation tests; exact-substitution updater fixture; ./scripts/update-versions.sh 1.2.0; pinned MCP uv lock refresh; version_date-derived changelog/info finalization and malformed/mismatch/slip fixtures; ./scripts/check-versions.sh; text/JSON bd info --whats-new; exact v1.2.0 disposable execution probes
      - positive differing-envelope/same-modeled-state and negative internal-raw-digest/model-field-drift fixtures
      - immediate-pre-publication fresh evaluator/reconciliation replay-rejection and fully bound guard fixture with no intervening approval/wait step
      - missing skipped duplicate cross-namespace stale rebuilt set-mismatched packaged wrapper source-derivation false-equality unavailable-live-observation and every post-U8 remote/release/asset drift mutation test
      - documentation command examples against the packaged candidate
      - make test
      - three Sol/Ultra review outputs name the final staged-diff digest and git write-tree; post-commit commit-tree/first-parent-diff proof plus GraphQL label-event and current-head human-review checks pass
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
      requirements_trace: "R1.9,R2.3,R4,R7.4,R8.3-R8.4,R8.6-R8.10"

  - key: u8-freeze-build
    title: Freeze all inputs and build the exact v1.2 candidate once
    type: task
    priority: 0
    description: |
      This is a no-source execution barrier. Verify every U0-U7 source PR and
      every discovered U4 route child is merged under repository policy and no
      helper/package/workflow change remains open. Re-run authoritative
      current-origin, release/asset, and every discovered external-channel
      observation read-only. Compare modeled state as an
      addition/deletion/move delta against the prior reviewed current
      observations, report every delta, and require the unreviewed-delta set to
      be empty. Retained/superseded historical absence is not a delta. Validate
      refreshed/diffed authoritative and maintainer-archive RefSource identity,
      class, rationale, authenticity proof, raw capture, and digest; treat new,
      missing, or reclassified sources as reviewed deltas. Regenerate the ambient-
      Git-free raw object-evidence lock and exact-revision semantic-surface
      catalog. Validate
      the latest reviewed lock-derived historical scope and evidence totals
      after every reviewed append-only delta, plus snapshot/generator output,
      provenance, workflow-evidence producer references, asset roles,
      capability/family proofs, producer resolution states, fallback-recipe
      pins, resolved-probe commitment, U2 evidence sidecar,
      provider-operation routes, maintained channel bindings, and focused
      cases. Return to owning source work on any unreviewed delta, unresolved
      producer, or lock/evidence drift.
      Regenerate the provider runtime/platform denominator, separate lifecycle-
      capability matrix, performance budgets, and canonical requirements-trace
      matrix. Run the U7 production capability/trust preflight; the currently
      absent release environment/App credentials/effective target rules, bypass-
      bearing tag rule, tag-trigger/rebuilding workflow, seven-day artifacts, or
      unverifiable Sigstore toolchain must fail before freeze.
      Fetch the remote target after all prerequisites merge and require its
      `feature/backend-provider-change-20260713` HEAD as the frozen commit.
      Finalize one freeze-record digest binding that commit/tree, the exact
      reconciled current-origin, release/asset, and external-channel raw
      envelopes/internally verified digests,
      reviewed intended-UTC-publication `version_date`, build environment,
      recipes, and every production, packaging, fixture,
      oracle, qualification, release-gate, workflow, and build-input byte.

      Only after that record is immutable, invoke the already-tested U5 pipeline
      exactly once with the frozen commit/tree and freeze-record digest as hard
      stage inputs. Produce one immutable multi-platform artifact inventory and
      manifest with a single candidate_set_digest and a distinct
      candidate_artifact_digest for each packaged platform/build entry, plus the
      exact wrapper and frozen-source-derivation contracts. The inventory
      already contains every later-published Go archive, checksum, corpus,
      SBOM, npm tarball, and MCP wheel/sdist; publication is promotion, never a
      post-tag build. U8 verifies frozen
      source/ref/recipe/executor/toolchain/compiler/sysroot and expected-version
      inputs for source derivations, but requires no unrealized output digest or
      embedded version. Every packaged/wrapper probe reports exact v1.2.0 and
      every source contract expects exact v1.2.0. This is the candidate set that may become v1.2.
      Do not edit or commit repository bytes, rebuild per platform/shard, create
      a stable tag, or publish any release/channel. If any frozen input needs a
      change, fail this bead, return to the owning source-work child and its PR
      gates, then start a new epoch after that change merges.
    acceptance_criteria:
      - U0-U7 and every discovered U4 route child are complete and merged; a DAG check proves each route child directly blocks U8, and no source/helper/package/workflow work remains unresolved.
      - Immediately before freeze, current-origin and release/asset reconciliation reports every addition/deletion/move against prior reviewed observations, excludes retained/superseded historical absence from delta accounting, and fails before building unless the unreviewed-delta set is empty; its exact observation bytes and digests are frozen as the U9/U10 comparison baseline.
      - U8 refreshes/diffs every authoritative and maintainer-archive RefSource record and classification/authenticity proof, reports new/missing/reclassified sources, and regenerates git-object evidence outside a repository with no ambient Git. It rederives semantic-surfaces.lock.json per exact revision and fails every catalog/evidence/disposition delta before building.
      - U8 also observes Homebrew, AUR, Winget, and every other discovered external channel, internally verifies every raw-envelope digest, and freezes the exact raw baseline; later checkpoints compare normalized modeled state, not raw-envelope equality.
      - The latest reviewed lock-derived tag-name/tag-revision scope and every evidence total validate after all reviewed append-only deltas; NIU1EF is checked only as the initial fixture, never as a permanent U8 total. Provenance, v1.1.0 workflow/producer/asset references, asset roles, capability/family/transition proofs, resolved fallback recipes, provider operation routes, channel bindings, and focused cases validate exactly.
      - U1's canonical runtime-bearing resolved-probe stream regenerates to the locked leaf count/set digest and U2's evidence lock is complete and bijective by probe_id/probe_digest/historical-binary SHA-256/engine_runtime_id; mismatch or runtime-only deduplication fails without building.
      - Provider tuples regenerate as a JCS hash of exactly all nine canonical fields, with platform_id only GOOS/GOARCH and independent engine_runtime_id distribution/version-or-image/protocol/config/semantic-envelope data including exact embedded value `embedded/no-external-runtime`. Every historical variant, resolved probe, smoke row, covered identity, result schema, shard, and aggregation key regenerates with engine_runtime_id; runtime-only row deletion/deduplication or source-derived boundary collapse fails. The separate lifecycle matrix, performance lock, and canonical requirements-trace matrix also regenerate exactly; an unknown/untraced/orphan requirement or missing U7 R8.6-R8.10 trace fails.
      - Production preflight proves the gated release environment, one dedicated least-privilege release App, stable/claim tag protections, artifact upload/API digest/90-day expiry, Sigstore attestation verification/trust lock, non-self-referential ClaimIntent hash/attest plus ClaimEnvelope/tag conditional creation and crash-resume validation, temporary-ruleset APIs, and post-tag ref:v1.2.0 detailed dispatch whose returned workflow_dispatch event/frozen head_sha/exact workflow identity and blob are verified before ruleset deletion. Current observed deficiencies are failing evidence and no candidate is built until provisioned capability passes.
      - After every prerequisite merge, the fetched remote feature/backend-provider-change-20260713 HEAD equals the frozen commit; one finalized freeze record binds that commit, its tree, reviewed strict intended-UTC-publication version_date, exact reconciled current-origin/release/asset/external-channel raw envelopes and internally verified digests, build environment, snapshot, generator, capability/family profiles, fallback recipes, source-derivation inputs, and digests for every production, packaging, fixture, oracle, qualification, release-gate, workflow, and build-input byte.
      - The U5 build stage cannot start without the finalized freeze-record digest and an exact frozen checkout; it is invoked once and emits one immutable multi-platform candidate manifest/inventory with one candidate_set_digest, manifest-selected candidate_artifact_digest values for packaged entries, and exact wrapper/frozen-source-derivation contracts. U8 verifies frozen source/ref/recipe/executor/toolchain/compiler/sysroot and expected-version inputs but does not require unrealized output SHA-256 or embedded version.
      - The immutable publishable inventory is complete for every Go archive, checksums, corpus, SBOM, npm tarball, and MCP wheel/sdist that release.yml can publish; every byte has a manifest digest and missing output fails U8 rather than being built after tag creation.
      - Candidate construction changes no repository byte and performs no stable tag, GitHub release, registry publication, maintained-channel publication, or external package update.
      - The frozen tree contains reviewed exact 1.2.0 source finalization; packaged/wrapper probes report v1.2.0, source derivations expect v1.2.0, and no frozen source/candidate/publishable-inventory version byte changes after U8. Locked consumer verification may create only ephemeral digest/version-recorded outputs outside that inventory.
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
      - refresh current-origin/release/asset/external-channel observations read-only, internally verify raw digests, report every modeled delta, prove historical absence is not a delta, and require an empty unreviewed-delta set without frozen-scope rewrite
      - regenerate and compare the latest reviewed snapshot/denominator/profile/resolution-state/recipe/probe-commitment/evidence/provider-operation/channel/focused-case locks and their derived current totals
      - refresh/diff RefSources and authenticity classes, regenerate raw Git object evidence and semantic/provider-runtime/lifecycle/performance/requirements-trace locks, and reject every delta or trace gap
      - execute the production release capability/trust preflight and prove current missing or unsafe GitHub state fails closed
      - fetch origin and verify the frozen commit equals feature/backend-provider-change-20260713 HEAD after prerequisite merges
      - finalize and verify the freeze-record digest against the exact source tree recipes helpers workflows build environment and reconciled current-origin/release/channel raw baselines before build starts
      - invoke .github/workflows/v12-candidate.yml once and verify candidate_set_digest plus every packaged/wrapper/frozen-source candidate-execution contract without requiring unrealized source-output identity
      - verify the complete publishable Go/checksum/corpus/SBOM/npm/MCP inventory and structural prohibition on post-tag regeneration
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
    description: |
      This is execution-only. Distribute the byte-identical immutable U8
      candidate-set manifest, artifact inventory, and candidate-execution
      contracts to every U6 workflow shard. Packaged rows execute only the
      unchanged selected artifact; wrappers recover those bytes; source-derived
      channel rows materialize once per frozen fully pinned derivation identity
      into one immutable canonical inventory and verify/record exactly one
      realized SHA-256 and exact v1.2.0 embedded version against frozen
      expected-version inputs. Run every mandatory historical
      tag-revision/producer/topology/engine-runtime/platform/build-flavor smoke row and every required
      deep-boundary, current-provider, fault, platform, and maintained
      install-channel focused case. Unaffected rows continue when one route
      fails; affected results emit terminal FAIL. Aggregate the smoke and
      focused namespaces independently only after each denominator is complete.
      Require every result to bind the frozen semantic-surface, provider-runtime,
      lifecycle-capability, and performance-budget lock digests. Execute every
      applicable semantic disposition and lifecycle cell, and aggregate signed
      raw/calibrated performance receipts against the locked numeric ceilings.

      This is prepublication qualification: every installer branch uses its
      frozen injectable or staging selector against exact U8 bytes. U9 never
      claims that a live public-latest registry or package-manager route selects
      an unpublished candidate. The later U7-owned publication workflow runs
      those public_latest_only selectors authentically and records a separate
      receipt that cannot retroactively satisfy U9.

      Do not change source, helpers, manifests, fixtures, workflows, build
      inputs, manifest, inventory, packaged candidate bytes, or derivation
      contracts. A needed change invalidates the epoch and
      returns to source work plus a new U8 candidate. Fetch and match the remote
      target-branch head to the frozen commit before starting any row and again
      immediately before accepting aggregation. At that same pre-acceptance
      checkpoint, acquire a fresh read-only current-origin, release/asset, and
      external-channel observation, validate its bytes against its own digest,
      normalize it, and reconcile modeled state against U8.
      Any unreviewed tag addition/deletion/move, release replacement/deletion,
      asset mutation, visibility/publication change, unavailable observation, or
      indeterminate result invalidates the epoch. Do not rewrite U8's frozen
      observation or scope in place; return to reviewed source/lock work and a
      new U8.
    acceptance_criteria:
      - Every mandatory tag-revision/producer/topology/engine-runtime/platform/build-flavor smoke_row_id, including both v1.1.0 revisions and exact embedded value `embedded/no-external-runtime`, runs against the exact U8 target-ref/commit/tree/freeze-record/candidate-set and candidate-execution identity and emits exactly one terminal PASS or FAIL.
      - Every required suite/equivalence-class/fault-case focused_case_id emits exactly one terminal PASS or FAIL; each shared case proves equality of runtime separately from platform and preserves source-derived runtime/semantic boundaries, and covered_identities includes the complete runtime-bearing smoke identity as traceability metadata only and never satisfies or duplicates smoke.
      - Representative, exhaustive, current-provider, crash/concurrency/low-disk/corruption/ambiguity/restore, platform/build-variant, and maintained-install-channel suites all consume the same frozen epoch; current-provider cases execute the complete nonempty R6 semantic, mutation, NoOp, rollback/restore, and restored-source-reopen oracle.
      - Every exact-revision result binds and executes the complete semantic-surfaces catalog duties and every applicable inspect/quiesce/snapshot/prepare/verify/activate/final_verify/resume/restore lifecycle-capability cell for its provider/runtime/platform identity; missing, stale, extra, or mismatched semantic/lifecycle receipts fail aggregation.
      - Every installer branch executes through frozen injectable/staging inputs against exact U8 bytes. No result asserts live public-latest selection before publication, and no later postpublication receipt can satisfy, replace, or repair a U9 result.
      - Every result contains exactly one namespace key, a valid tag_revision_id where historical, required engine_runtime_id and runtime semantic-envelope digest independent of platform_id, the common candidate_set_digest, and exactly one manifest-bound candidate_execution. Packaged rows match the selected candidate_artifact_digest; wrappers prove recovered-payload equality; source-derived rows prove frozen source/ref/recipe/executor/toolchain/compiler/sysroot pins as applicable, realized-output SHA-256, embedded version matching frozen expected-version inputs, and the distinct engine-runtime boundary without claiming packaged equality.
      - The source materialization inventory is bijective with derivation identities and records one digest/exact-v1.2.0 version per identity; every consumer references it, rebuild or disagreement fails aggregation, and only packaged manifest entries have candidate_artifact_digest.
      - Missing-capability routes emit terminal FAIL while unrelated rows continue; no SKIP, warning-only, timeout-as-success, unknown coverage reference, missing, duplicate, extra, wrong-revision, invalid-union, unpinned-derivation, false-byte-equality, or digest-mismatched result passes aggregation.
      - Runtime-only-distinct rows remain distinct in resolved probes, smoke IDs, covered identities, sharding, result schema, and aggregation; deleting either, deduplicating them, changing only engine_runtime_id without changing identity, or folding runtime into platform/source derivation fails.
      - Final success is exact smoke N/N plus focused M/M with zero non-pass outcomes; any frozen input, manifest, inventory, packaged candidate artifact, or derivation-contract change invalidates all results and requires a new U8/U9 epoch.
      - U9's signed performance receipts bind performance-budgets.lock.json, runner/hardware calibration, raw/normalized measurements, and sample counts and enforce p95/p99 NoOp/open plus the locked 12-hour U9, 20-minute row, 30-minute producer, shard/case-count, upload/cache, API-call, and acquisition-attempt ceilings. Any exceeded gate or missing/expired/unreviewed waiver fails qualification.
      - The fetched remote feature/backend-provider-change-20260713 head equals the frozen commit before row execution and before aggregation acceptance; movement invalidates the epoch even when all functional rows passed.
      - Immediately before aggregation acceptance, fresh current-origin/release/asset/external-channel raw envelopes are retained and internally digest-validated, then reconcile to zero modeled delta against U8. Raw envelopes may differ due timestamp/serialization; internal digest mismatch or modeled drift invalidates the epoch without scope rewrite and requires a new U8/U9.
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
      - reconcile each namespace, tag revision, and engine runtime independently, verify the common candidate_set_digest, and validate every runtime-bearing packaged/wrapper/frozen-source candidate execution
      - verify smoke N/N and focused M/M and list failed identities from either namespace
      - fetch and match the remote target head before execution and immediately before aggregation acceptance
      - immediately before aggregation acceptance, retain fresh raw current-origin/release/external-channel envelopes, validate their own digests, and require deterministic normalized reconciliation against U8 to have zero modeled delta; run raw-digest-mismatch and every modeled-drift/unavailable mutant
      - verify the canonical source-materialization inventory digest, bijection, one-output-per-identity, exact-v1.2.0 values, and consumer references; reject rebuild or disagreement
      - verify complete semantic/lifecycle receipts and calibrated numeric performance receipts against the exact U8-frozen locks
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
    description: |
      Feed the real U8 manifest and U9 terminal results to the U7 evaluator.
      Report eligible or ineligible with separate exact smoke and focused
      coverage and failed identities. Acquire a fresh read-only current-origin
      release/asset, and external-channel observation and reconcile it against the exact U8
      observation again; U9's successful checkpoint is not reusable as current
      evidence. This task is read-only: it cannot rebuild, repair evidence,
      rewrite frozen scope, edit source, create a tag, or publish a release. An eligible result is an
      input to a later accountable-human release decision, never publication
      authority by itself. First emit one canonical `EligibilityDecision` with
      evaluated input/target/freeze/baseline/result digests, counts, outcome/
      reasons, evaluator version, and `decision_digest`; it contains no bundle,
      locator, expiry, seal receipt, claim, or publication field. Only after an
      eligible decision is final may a distinct append-only step invoke U7's
      bundler. Non-self-referential bundle bytes bind `decision_digest` and the
      frozen U8/U9/target/version/channel inputs but not their own digest/receipt.
      Upload/attest them through the locked GitHub backend and emit a separate
      `BundleSealReceipt` containing decision/bundle/artifact digests, authenticated
      run/artifact locator, signer, seal time, 24-hour claim expiry, and retention.
      Sealing mutates only those new outputs, never an input, repository, tag,
      release, or channel. If the later human decision proceeds, the U7-implemented
      publication workflow's guard must rerun the evaluator and acquire fresh live reconciliation
      immediately before the first publication mutation; stored eligibility is
      not reusable.
    acceptance_criteria:
      - Eligibility is true only for the exact U8 candidate set with U9 smoke N/N, focused M/M, zero non-pass outcomes, one matching candidate_set_digest, complete revision-keyed and engine-runtime-keyed historical coverage, and every result's valid runtime-bearing packaged/wrapper/frozen-source candidate_execution; candidate_artifact_digest equality remains mandatory wherever packaged bytes execute.
      - U10 validates the immutable U9 source-materialization-inventory digest and its bijection/one-output/exact-v1.2.0 invariant; consumer disagreement or rebuild is ineligible.
      - The fetched remote feature/backend-provider-change-20260713 head, frozen commit/tree, freeze-record digest, and every candidate/result digest still match U8; target-ref movement or source mismatch is ineligible and requires a new epoch.
      - Newly acquired current-origin/release/asset/external-channel raw envelopes are internally digest-validated and reconcile to zero modeled delta against U8; differing capture metadata/serialization is allowed, while internal digest mismatch, modeled drift, unavailable check, or indeterminate result is ineligible and requires a new U8.
      - Missing, extra, stale, duplicate, skipped, cross-namespace, wrong-revision, unknown-coverage, rebuilt-packaged, set-mismatched, packaged/wrapper-mismatched, unpinned-source-derived, false-byte-equality, or wrong-epoch inputs produce ineligible with explicit failed identities.
      - Evaluation is read-only and changes no source, helper, result, candidate, tag, release, registry, or maintained distribution channel.
      - EligibilityDecision is canonical and contains only evaluated digests/identities, counts, outcome/reasons, evaluator version, and decision_digest; it contains no bundle/locator/expiry/receipt/claim/publication state. Its read-only outcome is final before any seal attempt and cannot self-reference later output.
      - Only an eligible final decision enters a distinct append-only seal operation. Bundle bytes bind decision_digest plus U8 freeze/candidate/artifact locators, U9 results/materialization inventory, target/baselines, version_date, and channel lock but never their own digest or receipt; BundleSealReceipt separately records decision/bundle/artifact digests, authenticated GitHub run/artifact locator, signer, sealed_at, 24-hour claim_expires_at, and retention.
      - Sealing uses upload-artifact@v4 retention-days:90 and attest@v4 Sigstore policy, creates only bundle/attestation/seal-receipt outputs, and promises substitution detection rather than WORM. The identical bytes move to stable release assets for the post-artifact retention horizon; substitution/signature/digest/expiry tests fail before publication.
      - The result clearly separates technical eligibility from the later human decision to publish v1.2.
      - U10 performs no publication or postpublication verification. If the named accountable human later authorizes the U7 workflow, its immediate guard—not stored U10 output—reruns the evaluator/live reconciliation and binds exact candidate-set, target OID/tree, freeze-record, U8 baseline, U9 inventory, and fresh-observation digests before any tag mutation.
    dependencies:
      - u9-exact-qualification
    labels:
      - release-gate
      - read-only
      - execution-barrier
    files: []
    verification:
      - run the U7 evaluator against the immutable U8/U9 inputs
      - verify exact candidate-set/runtime-bearing-candidate-execution/frozen-input digests and independent revision/runtime-keyed smoke N/N plus focused M/M reconciliation
      - retain newly acquired raw current-origin/release/external-channel envelopes, internally verify their digests, require zero normalized modeled delta against U8, and prove raw-digest mismatch/model drift/unavailable mutants are ineligible without scope rewrite
      - validate the U9 materialization-inventory digest and exact-v1.2.0 one-output-per-derivation invariant
      - emit EligibilityDecision twice for canonical equality and prove it has no seal fields; separately assemble/upload/attest non-self-referential bundle bytes and verify BundleSealReceipt, 24-hour expiry, 90-day artifact retention, release-asset horizon, and substitution rejection
      - prove repository, candidate, result, tag, and release state are unchanged
    metadata:
      execution_agent_type: codex
      execution_suggested_model: gpt-5.6-terra
      execution_reasoning_effort: high
      execution_mode: no-source-controlled-run
      execution_parallel_group: wave-7-gate
      requirements_trace: "R8.7"
```

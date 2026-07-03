# Oracle X — cross-plumbing differential report

**flag-off embedded ↔ BD_SPIKE_UOWSTORE spike-proxied**, one binary, two
plumbings, over the gc-contract corpus. This is the Slice-3 headline: it proves
where the spike's proxied-uowstore path IS and IS NOT behaviorally equivalent to
the ordinary embedded path, with **every divergence attributed**.

Run: `tests/oracle-a/run-oracle-x.sh` · binary
`spike/backend-seam-derisk@f41ece47` · 45 curated scenarios.

## What this oracle is (vs Oracle A)

Oracle A builds **two binaries** (before vs after) and runs them on the **same**
plumbing (embedded) — the retype refactor-safety net. Oracle X is the orthogonal
axis: **one** working-tree binary, run through **two** plumbings.

| role | plumbing | how |
|---|---|---|
| **REFERENCE** (goldens) | embedded, `BD_SPIKE_UOWSTORE` unset | the working-tree bd run natively — exactly what the harness does for real bd |
| **CANDIDATE** | proxied-server + uowstore adapter (`BD_SPIKE_UOWSTORE=1`) | a generated wrapper (`BTS_CANDIDATE`) that bootstraps a proxied workspace and execs the *same* bd with the proxied env |

The harness `run_scenario` `env_clear()`s to `{PATH,HOME,TMPDIR,BEADS_TEST_MODE=1}`,
so the wrapper is the only place the proxied env is injected. It intercepts the
harness `init -p <prefix> --quiet` bootstrap (writes `.beads/metadata.json` in
proxied-server mode, boots the managed proxy + child dolt sql-server via a first
read, per `cmd/bd/spike_uowstore_integration_test.go` `setupSpikeProxiedWorkspace`)
and passes every other argv through to `bd` untouched with
`BD_SPIKE_UOWSTORE=1` + the proxied env.

## Results

```
total scenarios: 45   (in-scope: 44, out-of-scope: 1)
IN-SCOPE   PASS: 33   FAIL: 11   (75%)
OUT-OF-SCOPE pass: 1  fail: 0    (note_append_two, informational)
```

- **33 in-scope scenarios are byte-identical** (post-normalization) across the
  two plumbings — the close→ready `is_blocked` loop, transitive/parent-child
  blocking, cycle *detection* (semantics, not message — see F-1), claim
  lifecycle, tiers set algebra (except no_history — F-3), metadata filtering,
  labels round-trip, delete-unblocks-neighbour, real purge + reseed count,
  config custom-key set/get, and the omitempty output boundaries.
- **11 in-scope divergences**, every one attributed below:
  **4 allowlisted** (class b/c/d — `CROSSPLUMB-ALLOWLIST.md`) +
  **7 class-(a) findings** (real uowstore-caused differences — §Findings).

### Attribution table (all 11)

| # | scenario | step | field | class | attribution |
|---|---|---|---|---|---|
| 1 | cycle_reject | 3 `dep add` (reverse) | stdout `.error` | **(a)** | adapter error-**wrapping**: "add dep: adding c-b → c-a would create a cycle" vs embedded "adding dependency would create a cycle". Cycle *is* rejected on both (exit 1); only the user-facing string differs. → F-1 |
| 2 | dep_retype | 3 `dep add` (retype) | stdout `.error` | **(a)** | adapter surfaces the raw repo error "add dep: insert: db: DependencySQLRepository.Insert: …" vs embedded's user-facing "…remove it first with 'bd dep remove'…". Retype *is* rejected on both. → F-1 |
| 3 | tiers_ephemeral | 5 `list --all` | stdout (set) | **(a)** | a `--no-history` issue (`t-h`) is **visible in `list --all` on proxied but hidden on embedded**. Tier-filtering divergence. → F-2 |
| 4 | sql_unsupported_embedded | 0 `sql` | stdout | (b) | raw-DB access unsupported on both; embedded → stderr text, proxied → typed-unsupported JSON on stdout. **AX-1** |
| 5 | ready_excludes_infra_and_coordination_types | 3 `create -t message` | stdout `.ephemeral` | **(a)** | `-t message` is auto-marked `ephemeral` on embedded but **not** on proxied — `IsInfraTypeCtx("message")` disagrees. → F-3 |
| 6 | list_excludes_gate_and_infra_types | 3 `create -t message` | stdout `.ephemeral` | **(a)** | same root cause as #5 (infra-type classification of `message`). → F-3 |
| 7 | output_parent_omitempty_boundary | 3 `update --status in_progress` | stdout `.started_at` | **(a)** | embedded sets `started_at` on the in_progress transition; proxied leaves it `null` — the adapter's `UpdateIssue` path skips `ManageStartedAt`. → F-4 |
| 8 | purge_dry_run_zero_metrics | 3 `close --force` | stdout `.labels` | **(a)** | embedded close output hydrates `labels:["red"]`; proxied returns `null` — the adapter's close return-object omits label hydration. → F-5 |
| 9 | config_set_protected_keys | 1 `config set dolt.debug` | stderr | (b)+(d) | `dolt.debug` is sql-server-only: embedded rejects at the gate, proxied passes the gate then fails on the spike workspace's missing `config.yaml`. Both exit 1. **AX-2** |
| 10 | comment_add_list | 1 `comment` | exit + stdout | (c) | `AddIssueComment` typed-unsupported; `comment` is outside the gc-16 set (not a census escape). **AX-3** |
| 11 | purge_real_then_reseed | 4 `purge --force` | stdout `.events` | (b) | audit-event materialization count (4 vs 0); `purged_count` identical (2). **AX-4** |

> The scoreboard records the **first** divergence per scenario. For #5/#6 the
> shared `IsInfraTypeCtx` root cause also perturbs the later `ready`/`list`/`count`
> steps of those scenarios; they are one finding (F-3), not three.

## Findings (class-(a), NOT allowlistable)

Real, uowstore-caused behavior differences. These are the deliverable of the
oracle — the cross-plumbing equivalence gaps in the spike adapter.

- **F-1 — error-message wording (cycle_reject, dep_retype).** *Severity: low
  (cosmetic).* The rejection semantics and exit codes match; the uow/dolt_sql
  provider surfaces a differently-worded (more internal) error string than the
  embedded store's user-facing message. gc code that string-matches these errors
  would break. Fix: have the adapter map these two errors to the embedded
  user-facing strings.
- **F-2 — `no_history` tier visibility (tiers_ephemeral).** *Severity: medium.*
  `list --all` includes a `--no-history` issue on proxied but excludes it on
  embedded. A tier/data-visibility divergence — the adapter's list path does not
  reproduce embedded's no-history filtering.
- **F-3 — infra-type auto-ephemeral for `message`
  (ready_excludes_…, list_excludes_…).** *Severity: medium.* Embedded marks
  `-t message` creates `ephemeral`; the adapter's `applyInfraTypeRouting` →
  `IsInfraTypeCtx("message")` (`internal/storage/uowstore/store.go:495`) returns
  a different classification, so proxied leaves `message` non-ephemeral. This
  changes downstream ready/list/count filtering for the coordination types.
- **F-4 — `started_at` on `update --status in_progress`
  (output_parent_omitempty_boundary).** *Severity: medium.* Embedded auto-sets
  `started_at` (GH#2796, `issueops/update.go:81` `ManageStartedAt`); the
  adapter's non-claim `UpdateIssue` path does not. (The `--claim` path *does* set
  it — `claim_preassigned_open` passes — so this is specifically the
  `UpdateIssue`/`ManageStartedAt` seam.)
- **F-5 — label hydration in `close` output (purge_dry_run_zero_metrics).**
  *Severity: low/medium.* Labels *persist* (the round-trip test proves `bd show`
  returns them on both), but the adapter's `close` **return object** omits the
  `labels` array that embedded includes. An output-hydration gap on the close
  path, not a persistence bug.

## Commit-count observer (create → update → close, both plumbings)

Counts dolt commits per command. **Embedded:** `dolt log` against
`.beads/embeddeddolt/<db>` between commands (no server holds the lock).
**Proxied:** the child dolt sql-server stays up, so query it live —
`SELECT COUNT(*) FROM dolt_log` over the port in `.beads/proxieddb/proxy.pid`.

```
command   embedded(Δ)      spike-proxied(Δ)
create    9 (+1)           8 (+1)
update    10 (+1)          9 (+1)
close     11 (+1)          10 (+1)
base(init+schema): embedded=8  spike-proxied=7
```

**1 dolt commit per mutating command on BOTH plumbings** — no
"N-commits-instead-of-one" divergence (the exact class Oracle A's README lists as
out of its reach). The base differs by one init/bootstrap commit, but the
per-command deltas are identical 1:1:1.

## The wrapper (env parity notes)

The CANDIDATE wrapper mirrors `bdProxiedEnv()` + the spike flag, with three
**stated deviations** from `spike_uowstore_integration_test.go`, each required
for a clean cross-plumbing diff (not for the store):

1. **`BEADS_ACTOR="CI Bot"` + `GIT_AUTHOR_EMAIL="ci@beads.test"`** — the spike
   test forces `actor=spiketester` for a symmetric *two-spike* comparison. Here
   the REFERENCE is captured by the plain harness under the host git identity
   (`CI Bot`/`ci@beads.test`, which the harness normalizes to `<ACTOR>`/`<EMAIL>`),
   so the CANDIDATE must mint the **same** identity, not `spiketester`. Without
   this, `created_by`/`owner` diverge on every scenario.
2. **`git config --global beads.role maintainer`** (under `HOME=$WS`) — suppresses
   the GH#2950 role warning the reference never emits (it resolves the host
   global `beads.role`; `HOME=$WS` hides it). A wrapper artifact, killed at
   source so it can't pollute the stderr diff.
3. **`chmod 700 .beads`** — matches `bd init`'s perms (a plain `mkdir` warns at 0775).

The proxied side intentionally **does not** set `BEADS_TEST_MODE` (the reference
*does*, via the harness): the spike boot must construct a real managed server,
and `BEADS_TEST_MODE` flips store/topology construction off. This asymmetry is a
documented mode difference affecting store construction only, not field values.

Seeding note: the brief's `config set issue_prefix <prefix>` is **CLI-rejected**
as a protected key (`cmd/bd/config.go` `rejectProtectedConfigKey`) in **both**
plumbings, so it is a no-op. Seeding is unnecessary: every corpus create passes
an explicit `--id`, and `ValidateIDPrefixAllowed` short-circuits on an empty
`dbPrefix` — so the candidate mints the same ids as the reference regardless.

## Per-scenario child lifecycle

Each scenario boots a managed proxy + child dolt sql-server on its first command.
They self-terminate on a **30s idle timeout** (`defaultProxyIdleTimeout`,
`internal/storage/uow/dolt_sql_provider.go`), so concurrency stays bounded during
the run. The harness deletes each scenario tempdir on scenario end, but the
processes linger up to the idle window; `run-oracle-x.sh` therefore forces all
scenario workspaces under `$SCRATCH/scenwork` (via `TMPDIR`) and **reaps** any
proxy/child whose cmdline references that exact path after scoring and after the
observer. The path match is exact — this host runs many unrelated dolt servers,
so the reaper never `pkill`s a bare `dolt sql-server`.

## How to rerun

```sh
# builds one working-tree bd, generates the spike-proxied wrapper, captures
# embedded goldens, scores the proxied candidate, runs the commit observer.
tests/oracle-a/run-oracle-x.sh

# reuse a prebuilt bd (skip the ~1min build) and keep the scratch dir:
BD_BIN=/abs/path/to/bd KEEP_ARTIFACTS=1 tests/oracle-a/run-oracle-x.sh
```

Requirements: cargo, a CGO toolchain, go, `dolt` in PATH, `jq`, `mysql` (observer
only). 43+ scenarios each boot a proxy + child server; the full capture+score can
exceed 30 minutes. Exit 0 = completed; attribution (this report + the allowlist)
is the gate, not a bare FAIL count.

## Verdict

The cross-plumbing differential **completed**. The spike-proxied uowstore path is
byte-equivalent to embedded on **33/44 in-scope scenarios** and on the 1:1:1
commit shape. **4 divergences are allowlisted** mode/unsupported/artifact
differences (`CROSSPLUMB-ALLOWLIST.md`). **7 are class-(a) findings** (F-1..F-5):
the plumbings are **not yet fully equivalent** — the adapter has real gaps in
infra-type classification, `started_at` management, no-history list filtering,
close-time label hydration, and error-message wording. These are the concrete
completion items the oracle exists to surface.

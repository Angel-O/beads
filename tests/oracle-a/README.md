# Oracle A — refactor-safety differential conformance for `bd`

Oracle A answers one question mechanically: **did this branch change any
user-visible `bd` behavior that Gas City's contract surface depends on?**

It runs the same gc-contract scenarios against two separately-built `bd`
binaries and diffs every step:

| role | built from | meaning |
|---|---|---|
| **REFERENCE** | `origin/main` (overridable via `REF_REF`) | the "before" |
| **CANDIDATE** | the current working tree (HEAD + uncommitted) | the "after" |

Each scenario is an ordered list of `bd` CLI argv steps run as real processes in
a throwaway workspace (`bd init` + steps). For every step the harness compares
**exit code**, **stderr**, and **JSON-aware stdout** (object key order ignored;
array order compared as a multiset by default, as an ordered sequence for
scenarios flagged `ordered`). Volatile values — timestamps, UUIDs, and the host
actor identity — are normalized to `<TS>`/`<UUID>`/`<ACTOR>`/`<EMAIL>` before
comparison. **Tolerated in-scope divergences: zero.**

## Run it

```sh
tests/oracle-a/run-oracle-a.sh
```

Exit status: `0` = 100% in-scope pass, `1` = at least one in-scope divergence,
`2` = setup/build error. On failure, each divergence is printed with the
reference vs candidate value for the offending step.

Overrides:

- `REF_REF=<gitref>` — compare against something other than `origin/main`
  (e.g. a release tag, or the branch's merge base).
- `KEEP_ARTIFACTS=1` — keep the scratch build dir (binaries, goldens, scoreboard
  output) for inspection instead of deleting it on exit.

## Prerequisites

- **Rust / `cargo`** — builds the vendored conformance harness (`harness/`).
- **A CGO toolchain (`gcc`/`cc`)** and **`go`** — `bd` embeds Dolt, which is cgo;
  both binaries build with `CGO_ENABLED=1 -tags gms_pure_go` (the `gms_pure_go`
  tag is mandatory per `docs/ICU-POLICY.md`).
- **`git`** with `origin/main` fetched (the script resolves `REF_REF` locally;
  run `git fetch` first if it is stale).

## Runtime

Dominated by the **cold reference build** from `origin/main` (a full `bd`
compile, ~1 min on a warm module cache, several minutes cold). The candidate
build reuses the local build cache and is fast. Harness build is ~10 s. Golden
capture + scoring run all 39 curated scenarios as real `bd` processes (~30–60 s).
End-to-end: **~2–7 minutes** depending on Go build-cache warmth. There is no
Dolt *server* in the loop — every scenario uses embedded Dolt in its own tempdir.

## What green PROVES

- For the **39 curated gc-contract scenarios** (the gc-16 command surface plus
  their in-scope flags — see `harness/src/scenarios.rs`), the candidate `bd`
  produces byte-identical (post-normalization) exit codes, stderr, and
  JSON-structural stdout to the reference `bd`, step for step.
- This covers the semantics most likely to break under the pluggable-storage
  refactor: the close→ready `is_blocked` propagation, transitive/parent-child
  blocking, cycle rejection, claim lifecycle + idempotent self-reclaim, storage
  tiers (ephemeral/no-history) set algebra, `purge` (which has no uow dual and is
  where the bts-rs red team found re-seeding bugs), metadata/label filtering,
  ordering, and the gc error contracts (`bd sql` embedded-unsupported, not-found,
  config-key rejection).

## What green does NOT prove

- **Only the in-scope surface.** The predicate is the gc-contract commands +
  flags in `harness/src/bin/scoreboard.rs` (`IN_SCOPE_CMDS` / `IN_SCOPE_FLAGS`,
  with `--json` required for gc-parsed output commands). Behavior outside that
  set — most of the ~271-command `bd` surface, human/plain output modes, and any
  flag not listed — is reported as out-of-scope and is **not** a gate. A refactor
  that changes only out-of-scope behavior passes Oracle A.
- **Anything invisible to stdout/stderr/exit diffing.** Post-commit hook firing,
  rollback-suppressed side effects, N-Dolt-commits-instead-of-one, and equal-priority
  / wall-clock-hybrid ordering (`ready`'s 48h recency rule) are deliberately NOT
  asserted here (array order is a multiset except where an all-distinct-priority
  scenario pins it). These are the epistemic limits called out in
  `PROPOSAL-pluggable-storage-backends.md` §2.4 / §7.
- **Non-determinism is normalized away, not verified.** Minted IDs, timestamps,
  and UUIDs are collapsed to tokens; their *values* are not checked (that is what
  property tests are for, a separate work item).
- **This is not a cross-backend oracle.** Oracle A is same-backend (embedded
  Dolt) before-vs-after. It is the refactor-safety net for the retype phases, not
  the Dolt-vs-SQLite/Postgres conformance run (that is Oracle B / Phase 1's
  backend-pair matrix).

## How it stays out of bts-rs's way

The harness under `harness/` is a **vendored verbatim copy** of
`/data/projects/bts-rs/crates/bts-conformance` (`harness/PROVENANCE.md` records
the exact upstream commit; only `Cargo.toml` is local so it builds standalone).
Goldens are re-captured from the reference build into
`harness/testdata/golden/` (git-ignored) on every run — bts-rs's own testdata is
never read or written. The reference `bd` is built in a throwaway `git worktree`
that the script removes on exit.

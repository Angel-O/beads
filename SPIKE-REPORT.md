# SPIKE REPORT — `uowStore` vertical slice (Route A of #4547)

**Branch:** `spike/backend-seam-derisk`
**Date:** 2026-07-02
**Companion designs:** `PROPOSAL-pluggable-storage-backends.md` (§4.0/§4.5/§4.7, Phase 1/4),
`PROPOSAL-uowstore-adapter.md` (§2/§4 — this spike is the embryo of that adapter)

## 1. What was built

A new package `internal/storage/uowstore` with one type:

```go
type uowStore struct {
    storage.DoltStorage         // embedded nil interface: untouched methods PANIC (spike-legal)
    provider uow.UnitOfWorkProvider
    actor    string
}
```

It satisfies `storage.DoltStorage` by **embedding the interface as a nil value** (so the
**121** methods this spike does not implement compile away and panic loudly if ever reached —
the interface is 144 methods and 23 are overridden) and
**overriding a real vertical slice**. Each override is one short unit of work:
`provider.NewUOW → use-case call → Commit(ctx, outcome-derived msg)` for writes (via
`uow.RunInTxMsg`, which already owns phase-aware retry), and `NewUOW → use-case call →
Close` (rollback) for reads. Reads **never** `Commit("")`.

Wiring (env-gated, default byte-identical): `cmd/bd/store_factory.go` +
`store_factory_nocgo.go` — the proxied-server arms return `uowstore.New(provider, actor)`
when `BD_SPIKE_UOWSTORE=1`; `usesProxiedServer()` returns false under the flag so commands
travel the ordinary store path (not the `*_proxied_server.go` duals); one guard in
`main.go`'s PreRun (`proxiedServerMode && !spikeUOWStore()`) lets the store path construct
instead of short-circuiting to the uow provider. The init gate was **not** lifted and no dual
file was modified.

Test: `cmd/bd/spike_uowstore_integration_test.go` (`TestSpikeUOWStore_RoundTrip`, gated by
`BEADS_TEST_PROXIED_SERVER=1`) round-trips **create → get → search → ready → close** through
the spike store and asserts the JSON output shape matches the embedded store path for each op.

### Verification (real output)

```
$ BEADS_TEST_PROXIED_SERVER=1 CGO_ENABLED=1 go test -tags gms_pure_go \
      -run TestSpikeUOWStore_RoundTrip ./cmd/bd/ -count=1
ok  	github.com/steveyegge/beads/cmd/bd	22.965s
```

Manual end-to-end through the spike store (prefix `spike`, proxied):
`create → spike-8y1 (open)`, `show → open`, `ready → [spike-8y1]`, `close → closed`,
`ready after close → []`. The denormalized `is_blocked`/ready recompute fires correctly on
close (ready drops to 0), which is the §2.3 semantic the proposal flags as the most
conformance-dangerous — and it works for free because the use-cases already implement it.

`go build ./...` green (cgo + nocgo); `go vet ./cmd/bd ./internal/storage/uowstore` clean.

## 2. Feasibility verdict: **GREEN — the full adapter is buildable, but the mapping is faithful only where the adapter explicitly re-threads store-side state.**

Route A is confirmed. The headline claim of `PROPOSAL-uowstore-adapter.md` (~60% of core maps
1:1 to existing use-case methods; the true remaining cost is a small set of gap units, not 54
command conversions) held up against the code. Every method needed for this five-command
slice was either a DIRECT 1:1 use-case call or a small COMPOSE. Zero methods in the slice were
true GAPs. The use-case layer's semantics (minted IDs, infra-type routing, is_blocked
recompute, same-tx events) are reused wholesale — the adapter is genuinely thin.

**But "thin" is not "free": the adapter is faithful only where it re-threads state the
embedded store carries on the `*types.Issue` and the use-case reads from a *separate* params
field.** The red team found a concrete counterexample — `CreateIssue` dropped `issue.Labels`
because the embedded store persists them off the issue struct (`issueops.PersistLabels`) while
the domain use-case reads only `params.Labels`; the adapter now copies them across (§3). This
is exactly the silent-divergence class the spike existed to surface: "zero business logic" is
true, but "zero mapping logic" is not — every place the two contracts disagree on where a
field lives is a fixup the adapter must own and the Phase-1 harness must fixture.

**The single most important spike finding: the "core five" is a lie about surface area.**
Proving create→get→search→ready→close end-to-end through the store path required **23 real
store-interface methods**, not 5–8 — because the CLI's *command handlers* AND its
`PersistentPreRunE` helpers fan out from the headline op into a surrounding read surface
before and after it (the original slice was 21, but the PreRun census missed `GetMetadata`
and `GetStatistics` — see §4). This is invisible from the interface but decisive for sizing
the real adapter. Details in §4.

## 3. Per-method friction (what actually cost something)

| Store method | Disposition | Friction |
|---|---|---|
| `GetIssue` | DIRECT + fixups | **Three real fixups the adapter must own.** (a) The use-case's `GetIssue` probes only the `issues` table; the store contract also falls back to `wisps` — the adapter must replicate the fallback (`issueops.GetIssueInTx` does). (b) Not-found convention differs: the store contract is `storage.ErrNotFound`, the uow path surfaces a wrapped `sql.ErrNoRows` (`domain/db/issue.go:274`). The adapter must translate. Missing either silently changes `bd show` not-found behavior. (c) **Labels are NOT hydrated on the returned issue.** The store contract's `GetIssue` attaches labels in-tx (`issueops/get_issue.go` sets `issue.Labels`); the use-case's `GetIssue` selects only issue columns. `bd show` is shielded because it fetches labels via the separate `GetLabels` call, but any store-path consumer reading `GetIssue(...).Labels` (hook payloads, exporters, future commands) sees empty labels on the spike path. The full adapter must hydrate labels in `GetIssue` (extra `LabelUseCase.GetLabels` in the same UOW) and carry a labels-in-GetIssue parity fixture. |
| `CreateIssue` | DIRECT + fixups | (a) Store mutates the caller's `*types.Issue` in place with the minted ID; the use-case does too (shared pointer via `CreateIssueParams.Issue`) — so this works, but it is a **contract the adapter relies on**, not a guarantee the use-case documents. (b) **Infra-type→wisp routing lives in the store, not the use-case** (`EmbeddedDoltStore.CreateIssue` flips `Ephemeral` for infra types; `IssueUseCase.create` does not). The adapter must call `ConfigUseCase.IsInfraTypeCtx` itself before the write, in a *separate read UOW* (because `RunInTxMsg` may replay `fn`, the routing decision must be pre-computed and idempotent). (c) **Labels live in different places on the two contracts.** The embedded store persists `issue.Labels` off the `*types.Issue` (`issueops.PersistLabels`); the domain create reads `params.Labels`. The adapter must copy `issue.Labels → CreateIssueParams.Labels` or every label is silently dropped — empirically confirmed by red team (`bd create -l x` on the spike path echoed the label but `bd show` returned none). Fixed. **Comments are still a gap:** the embedded store persists `issue.Comments` (`issueops.PersistComments`) but the domain create has no comment path, so a create carrying comments (import) would drop them; the store-path `bd create` never populates Comments, so this is latent, not live — a full adapter needs a comment-write gap unit. (d) **Replay caveat:** because `RunInTxMsg` may replay `fn` after a pre-commit transient, and the use-case writes the minted ID back through the shared `issue` pointer, a replay re-enters with `issue.ID` already set from the failed attempt; the ID-minting path is idempotent for explicit IDs but the minted-ID replay semantics deserve a fixture in the real adapter. |
| `CloseIssue` | DIRECT + error-mapping fixup | The store's `CloseIssue` is the **raw** op; all the command-level validation (epic open-children, gate satisfaction, blocker refusal) lives ABOVE the store in `close.go`/`close_proxied_server.go`, so the adapter does none of it. The only adapter work is the issue-vs-wisp table probe (mirrors `proxiedResolveIssueOrWisp`). **Error-mapping caveat (same class as `GetIssue`):** on failure `CloseIssue` currently surfaces the use-case's wrapped text (`db: IssueSQLRepository.Close …`) and raw wrapped `sql.ErrNoRows`, not the store path's `issueops` text and `storage.ErrNotFound` sentinel. Shielded today because `close.go` resolves via `GetIssue` first (which the adapter *does* translate byte-compatibly), but any direct store consumer would see different bytes and different `errors.Is` behavior. The real adapter needs the shared `mapUowError` helper (§6.1) routed through EVERY method, not just `GetIssue`, with per-method not-found fixtures in the Phase-1 harness. |
| `SearchIssues(WithCounts)`, `GetReadyWork(WithCounts)` | DIRECT | Trivial: use-case returns `SearchPage{Items, HasMore}`; adapter returns `.Items`. The store interface drops `HasMore` — which is exactly the `--offset`/paging divergence the proposal flags (proxied-only). A real core-paging extension would restore it. |
| `GetLabels` | DIRECT | 1:1 `LabelUseCase.GetLabels`. |
| `GetDependenciesWithMetadata` / `GetDependentsWithMetadata` | COMPOSE | `DependencyUseCase.ListWithIssueMetadata` with `Direction: Out`/`In`. The direction mapping is the whole content of the compose — confirmed against the doc's §4.2. |
| `CountDependencies` / `CountDependents` | DIRECT-ish | `DependencyUseCase.CountByIssueID(Out/In)`. **Signature-faithful but semantically narrower:** the store counts sum `dependencies` + `wisp_dependencies` (`embeddeddolt/counts.go`); the use-case count hits one table. Correct for regular issues (the spike case); the **wisp-union is a documented gap** a full adapter must close. |
| `CountIssueComments` | DIRECT | `CommentUseCase.CountCommentsForIssue`. |
| `IsBlocked` | DIRECT | 1:1 `DependencyUseCase.IsBlocked` — signatures match exactly incl. `(bool, []string, error)`. |
| `GetDependencyRecordsForIssues` | DIRECT | 1:1 `DependencyUseCase.GetForIssueIDs` (`map[string][]*types.Dependency`). |
| `GetIssuesByIDs` | DIRECT | 1:1 `IssueUseCase.GetIssuesByIDs`. |
| `GetCustomStatusesDetailed`, `GetCustomTypes` | DIRECT | 1:1 `ConfigUseCase`. |
| `GetInfraTypes` | DIRECT + **signature snag** | Store returns `map[string]bool` (no error); use-case returns `(map, error)`. Adapter must **swallow the error** to fit the store signature. Same snag on `IsInfraTypeCtx` (store returns bare `bool`). This is a genuine seam-shape mismatch the clean-room design (§4.0) fixes by giving the seam an error channel everywhere. |
| `GetConfig`, `GetAllConfig` | DIRECT | 1:1 `ConfigUseCase`. |

**No true GAPs were hit in this slice.** Error-mapping and infra-routing were the recurring
real work; both are small and mechanical, but both are *silent-divergence* risks if skipped —
exactly the class the Phase 1 differential harness exists to catch.

## 4. Correction to the adapter doc's §4 gap list (important)

The doc's gap taxonomy (§4.6: ~60% DIRECT / ~15% COMPOSE / ~10% FALLBACK / ~15% GAP) is
**accurate for the methods it names**, but it measures the wrong denominator. It counts the
`storage.DoltStorage` interface. The real adapter cost is driven by the **transitive command
read-surface**, which the interface census does not reveal. Concretely, each headline command
pulled in this many *additional* store methods before it would run end-to-end:

- **`bd list` / `bd ready`** (before touching `SearchIssues`/`GetReadyWork`): the list-filter
  loader needs `GetCustomStatusesDetailed` + `GetCustomTypes` + `GetInfraTypes`
  (`cmd/bd/list_filter.go`), and the repo auto-routing preflight needs `GetAllConfig` +
  `GetConfig` (`cmd/bd/routing_read.go` — runs on **every read command**).
- **`bd show --json`** (default output, no `--include-*`): `GetLabels`,
  `GetDependenciesWithMetadata`, `CountDependents`, `CountDependencies`, `CountIssueComments`
  (`cmd/bd/show.go:149-158`).
- **`bd close`** (no flags): `IsBlocked` (pre-close check) plus an **unconditional**
  parent-molecule auto-close probe — `findParentMolecules → GetDependencyRecordsForIssues →
  GetIssuesByIDs` (`cmd/bd/mol_current.go:413,456`).
- **Every write command's `PersistentPreRunE`** (BEFORE any RunE): `validateWorkspaceIdentity`
  calls `GetMetadata` (`cmd/bd/main.go:1441`), and `maybeAutoImportJSONL` calls `GetStatistics`
  (`cmd/bd/auto_import_upgrade.go`) on any workspace whose `.beads/issues.jsonl` is non-empty.
  Both are now overridden (DIRECT). **This is the census's most important blind spot:** the
  original 21-method slice was derived by grepping *command handlers*, but the PreRun helpers
  run on the global store too — the spike's manual verification and integration test only
  escaped nil-panics because they used fresh temp dirs (no `issues.jsonl`) and set
  `BEADS_SKIP_IDENTITY_CHECK=1`. A real workspace panics without `GetMetadata`/`GetStatistics`.
- **Every command's molecules loader** (`cmd/bd/main.go` PreRun → `internal/molecules/molecules.go`)
  calls `CreateIssuesWithFullOptions` whenever a user/town/project `molecules.jsonl` carries
  templates not yet in the fresh spike DB. This one is **NOT overridden** in the spike (a write
  batch with `BatchCreateOptions`; latent because the test workspaces have no molecule templates)
  — it must be implemented or explicitly guarded before the spike wiring is exercised outside a
  clean temp dir.

None of the read fixups are GAPs — they are all DIRECT/COMPOSE — but they mean the adapter's *minimum
buildable slice per command* is 3–5× the headline method. **Recommendation for the real
adapter:** size it against the CLI's command→store call graph (a `grep` census of
`store.<Method>(` / `activeStore.` / `issueStore.` call sites per command **including the
`PersistentPreRunE` helpers and the molecules loader**, not just command handlers), not against
the interface. The 23 methods this spike implements already cover the read/config/dep-count
substrate that most CORE commands share, so the marginal cost of the *next* commands is lower
than the first — but the doc should add a "transitive read-surface" row to its gap scoreboard.

The doc's named GAP units (comment-writes, events-reads, local-metadata/D5, slots,
advanced/compaction queries) are **confirmed** — none were needed for this slice, and none
have an obvious use-case method, consistent with the doc. `SetLocalMetadata`/`GetLocalMetadata`
in particular are untouched here; the tip-metadata PostRun path is bypassed in spike mode
(the store self-commits per call, so `proxiedServerMode` still gates PostRun), which sidesteps
but does not solve the D5 "local-on-a-shared-server" ambiguity the doc flags.

## 5. LOC and cost

| Artifact | LOC |
|---|---|
| `internal/storage/uowstore/store.go` (23 overrides + 2 helpers) | ~450 |
| `cmd/bd/spike_uowstore_integration_test.go` | 343 |
| Factory + main.go wiring (`store_factory.go` +37, `store_factory_nocgo.go` +30, `main.go` +8) | 74 |

**Extrapolated full-adapter cost.** Averaging ~11 LOC per override (many are 6-line
NewUOW/call/Close bodies; COMPOSE and fixup methods run 15–25):

- The ~107-method flat core (§4.1) at ~11 LOC ≈ **1.2k LOC** for the mechanical DIRECT/COMPOSE
  body, PLUS the five named gap units (each a small use-case addition + repo method:
  comment-write, events-read, slots, advanced/compaction, local-metadata/D5). Call it
  **~1.5–2.5k LOC of adapter + ~1–1.5k LOC of new use-case/repo surface for the gaps.**
- This is comfortably inside the proposal's Phase 4 Route A envelope (4–6 weeks). The spike
  spent its effort on *discovery* (which methods, which fixups) not *volume*; with the
  call-graph census in hand, the remaining methods are near-mechanical.
- Route A's core claim — reuse the parity-tested dangerous semantics (same-tx events,
  is_blocked propagation, delete/purge neighbour recompute) instead of re-deriving them — is
  **validated**: the spike wrote zero SQL and zero business logic, and the is_blocked-on-close
  recompute worked on the first try.

## 6. Risks / caveats surfaced

1. **(§6.1) Error-mapping is load-bearing and silent, and applies to EVERY method, not just
   `GetIssue`.** `GetIssue`'s ErrNotFound-vs-ErrNoRows and the wisp fallback are the kind of
   thing that passes a happy-path test and breaks `bd show`'s not-found contract. The spike
   translated only `GetIssue`; `CloseIssue` and the other reads (`GetIssuesByIDs`, `GetLabels`,
   counts) still surface raw `db:`-prefixed use-case text and wrapped `sql.ErrNoRows`. The full
   adapter needs a single shared `mapUowError` helper routed through **all** methods, and the
   Phase 1 harness must carry not-found fixtures per method.
2. **Signature mismatches (`GetInfraTypes`/`IsInfraTypeCtx` drop the error).** The flat store
   interface has no error channel where the use-case does; the adapter swallows. The
   clean-room seam (§4.0) should standardize on error-returning everywhere.
3. **Per-call transaction granularity is a concurrency-CORRECTNESS risk, not just `dolt log`
   noise (red-team correction).** Every store call is its own short tx. Through the spike store
   one `bd close` becomes N *independent* transactions — resolve/GetIssue, `IsBlocked`
   (`close.go:141`), `CloseIssue` (`:152`), the molecule probe (`:169`), the re-fetch (`:172`)
   — whereas the proxied dual runs validation+close+continue+claim inside ONE UOW committed
   once (`close_proxied_server.go:103`), and embedded mode holds the workspace flock for the
   whole command. That is a **contract change**, not a cosmetic history difference: agent A
   passes the `IsBlocked` check in tx1; agent B adds a blocking dependency and commits; agent
   A's `CloseIssue` commits in tx2 → a blocked issue closes, which neither the dual nor the
   embedded path permits. This is the exact multi-writer scenario proxied mode exists for.
   **Phase 2b must pin it:** the full adapter needs the `RunInTransaction` mapping
   (`PROPOSAL-uowstore-adapter.md §4.5`) delivered for read-check-act commands like `close`
   BEFORE the proxied full surface ships, plus a two-process race fixture in the Phase-1 harness.
   The `dolt log` divergence is the visible symptom; the closeable-while-blocked race is the
   real hazard.
4. **Env-gated wiring only.** Default behavior is byte-identical (flag off → the proxied arms
   return the original error, `usesProxiedServer()` unchanged). Verified: existing proxied
   integration tests fail identically with/without the spike changes (they fail on the
   pre-existing init gate, `--proxied-server is not yet implemented`, which was NOT lifted).
5. **The shape-equality integration test is weaker than the oracle's byte diff — do not read
   it as conformance evidence.** `normalizeIssue` drops empty/null values before comparing key
   sets, so a field that is present-but-empty on one backend and absent on the other compares
   equal — masking exactly the representational-divergence class (`metadata {}` vs absent) the
   regression normalizer documents as real. It is a shape check, not a conformance check; the
   real gate for the adapter is the Phase-1/Oracle-B differential harness run against it with
   empty-vs-absent fixtures. (The test now also pins the label-persistence and not-found paths,
   which the pure key-set check was structurally blind to.)
6. **(§6.4) The flag's blast radius is wider than a single arm, and the wiring pattern must NOT
   graduate.** With `BD_SPIKE_UOWSTORE=1`: (a) `usesProxiedServer()` is forced `false` while the
   workspace IS proxied — a topology predicate that lies. This disables the 13 working proxied
   dual commands (they route into the nil-panic minefield) and, worse, its OTHER consumers
   silently change behavior: `doctor.go:207`'s proxied guard is bypassed and `init.go:1188`
   would persist the wrong `DoltMode`. It is safe in the spike (env-gated, default-off) but is
   the exact split-brain the clean-room design forbids — `main.go:1223`'s PostRun keys off the
   raw `proxiedServerMode` global (stays true) while the function says embedded. (b)
   `newReadOnlyStoreFromConfig` returns the writable spike store, ignoring `doltCfg.ReadOnly`,
   so read-only-command protection is bypassed. (c) `flushBatchCommitOnShutdown` calls
   `store.Commit` — unimplemented — and would panic if a user with dolt autocommit=batch signals
   the process. **Must-not-survive at promotion:** dual dispatch dies by DELETING the
   `*_proxied_server.go` duals, not by falsifying the mode predicate; the factory arm keys on
   the locator (`cfg.IsDoltProxiedServerMode`) with NO env consulted at open; the nil embed is
   replaced by a generated typed-`ErrUnsupported{Op, Backend}` shell that names
   `BD_SPIKE_UOWSTORE`/#4547 (loud typed errors, not raw nil-pointer panics).

## 7. Recommendation

Ship Route A. The adapter is real, thin, and reuses the exact semantics the May-2026 store
attempt lacked. Before committing to the full build, run the **command→store call-graph
census** described in §4 to get an accurate method count — the interface undercounts the work
by ignoring the transitive read-surface, and overcounts it by including capability methods a
core adapter never needs. This spike's 23 methods are a reusable substrate for that census.

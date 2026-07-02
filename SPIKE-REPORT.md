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
~120 methods this spike does not implement compile away and panic loudly if ever reached) and
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

## 2. Feasibility verdict: **GREEN — the full adapter is buildable and the mapping is faithful.**

Route A is confirmed. The headline claim of `PROPOSAL-uowstore-adapter.md` (~60% of core maps
1:1 to existing use-case methods; the true remaining cost is a small set of gap units, not 54
command conversions) held up against the code. Every method needed for this five-command
slice was either a DIRECT 1:1 use-case call or a small COMPOSE. Zero methods in the slice were
true GAPs. The use-case layer's semantics (minted IDs, infra-type routing, is_blocked
recompute, same-tx events) are reused wholesale — the adapter is genuinely thin.

**The single most important spike finding: the "core five" is a lie about surface area.**
Proving create→get→search→ready→close end-to-end through the store path required **21 real
store-interface methods**, not 5–8 — because the CLI's *command handlers* fan out from the
headline op into a surrounding read surface before and after it. This is invisible from the
interface but decisive for sizing the real adapter. Details in §4.

## 3. Per-method friction (what actually cost something)

| Store method | Disposition | Friction |
|---|---|---|
| `GetIssue` | DIRECT + fixups | **Two real bugs the adapter must own.** (a) The use-case's `GetIssue` probes only the `issues` table; the store contract also falls back to `wisps` — the adapter must replicate the fallback (`issueops.GetIssueInTx` does). (b) Not-found convention differs: the store contract is `storage.ErrNotFound`, the uow path surfaces a wrapped `sql.ErrNoRows` (`domain/db/issue.go:274`). The adapter must translate. Missing either silently changes `bd show` not-found behavior. |
| `CreateIssue` | DIRECT + fixups | (a) Store mutates the caller's `*types.Issue` in place with the minted ID; the use-case does too (shared pointer via `CreateIssueParams.Issue`) — so this works, but it is a **contract the adapter relies on**, not a guarantee the use-case documents. (b) **Infra-type→wisp routing lives in the store, not the use-case** (`EmbeddedDoltStore.CreateIssue` flips `Ephemeral` for infra types; `IssueUseCase.create` does not). The adapter must call `ConfigUseCase.IsInfraTypeCtx` itself before the write, in a *separate read UOW* (because `RunInTxMsg` may replay `fn`, the routing decision must be pre-computed and idempotent). |
| `CloseIssue` | DIRECT | The store's `CloseIssue` is the **raw** op; all the command-level validation (epic open-children, gate satisfaction, blocker refusal) lives ABOVE the store in `close.go`/`close_proxied_server.go`, so the adapter does none of it. The only adapter work is the issue-vs-wisp table probe (mirrors `proxiedResolveIssueOrWisp`). |
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

None of these are GAPs — they are all DIRECT/COMPOSE — but they mean the adapter's *minimum
buildable slice per command* is 3–5× the headline method. **Recommendation for the real
adapter:** size it against the CLI's command→store call graph (a `grep` census of
`store.<Method>(` / `activeStore.` / `issueStore.` call sites per command), not against the
interface. The 21 methods this spike implements already cover the read/config/dep-count
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
| `internal/storage/uowstore/store.go` (21 overrides + 2 helpers) | 405 |
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

1. **Error-mapping is load-bearing and silent.** `GetIssue`'s ErrNotFound-vs-ErrNoRows and the
   wisp fallback are the kind of thing that passes a happy-path test and breaks `bd show`'s
   not-found contract. The full adapter needs a single shared `mapUowError` helper and the
   Phase 1 harness must carry not-found fixtures.
2. **Signature mismatches (`GetInfraTypes`/`IsInfraTypeCtx` drop the error).** The flat store
   interface has no error channel where the use-case does; the adapter swallows. The
   clean-room seam (§4.0) should standardize on error-returning everywhere.
3. **Per-call transaction granularity.** Every store call is its own short tx (Dustin's
   design). Commands that today batch several store calls in one command without a transaction
   will get N commits instead of one — the proposal's §5.2 point, confirmed structurally here.
   The spike's close path already issues multiple UOWs per `bd close` (blocker check, molecule
   probe, the close itself); fine for correctness, but `dolt log` divergence is real and
   belongs in the Phase 2b gate.
4. **Env-gated wiring only.** Default behavior is byte-identical (flag off → the proxied arms
   return the original error, `usesProxiedServer()` unchanged). Verified: existing proxied
   integration tests fail identically with/without the spike changes (they fail on the
   pre-existing init gate, `--proxied-server is not yet implemented`, which was NOT lifted).

## 7. Recommendation

Ship Route A. The adapter is real, thin, and reuses the exact semantics the May-2026 store
attempt lacked. Before committing to the full build, run the **command→store call-graph
census** described in §4 to get an accurate method count — the interface undercounts the work
by ignoring the transitive read-surface, and overcounts it by including capability methods a
core adapter never needs. This spike's 21 methods are a reusable substrate for that census.

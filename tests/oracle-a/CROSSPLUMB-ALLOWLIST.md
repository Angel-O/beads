# Oracle X — cross-plumbing allowlist

Pair-scoped allowlist for the **flag-off embedded ↔ BD_SPIKE_UOWSTORE
spike-proxied** differential (`run-oracle-x.sh`). One entry per **allowed**
divergence. An entry is allowlistable ONLY if its class is:

- **(b) mode difference** — embedded-vs-proxied semantics (autocommit / tx shape /
  sql-server-only keys / audit-row materialization), not a wrong result; or
- **(c) unimplemented method** — a *typed* `ErrUnsupported` from the spike shell
  for a surface **outside the gc-16 completion set**; or
- **(d) harness/wrapper artifact** — an artifact of how the wrapper bootstraps
  the proxied workspace, not of the store.

**Class (a) adapter bugs are NOT allowlistable.** They are real,
uowstore-caused behavior differences and are reported as findings in
`CROSSPLUMB-REPORT.md` (§Findings), never here.

Scope tags: `pair` = embedded↔spike-proxied; `field` = the diverging field;
`tag` = `intentional` (correct-by-design) or `known` (accepted for the spike,
flagged for follow-up).

---

## AX-1 — `sql_unsupported_embedded` · raw-DB access unsupported, different surface

- **class:** (b) mode difference
- **pair:** embedded ↔ spike-proxied
- **field:** step 0 `sql SELECT 1 --json` → stdout
- **tag:** intentional
- **evidence:**
  ```
  reference (embedded):  <empty stdout>   (error goes to stderr; exit 1)
  candidate (proxied):   {"error":"storage backend does not support raw DB access","schema_version":1}   (exit 1)
  ```
- **why allowed:** raw DB access is unsupported on **both** plumbings and both
  exit non-zero. Embedded prints the "sql is unsupported in embedded mode"
  contract to stderr; the uowstore adapter returns a *typed* unsupported that
  bd renders as a JSON error on stdout under `--json`. Same outcome (`bd sql`
  rejected), different surface channel/format. `sql` is inherently
  backend-specific; the gc contract only requires that it fail.

## AX-2 — `config_set_protected_keys` · `dolt.debug` gate is sql-server-only

- **class:** (b) mode difference (+ (d) contribution: spike workspace has no `config.yaml`)
- **pair:** embedded ↔ spike-proxied
- **field:** step 1 `config set dolt.debug true --json` → stderr
- **tag:** intentional
- **evidence:**
  ```
  reference (embedded):  Error: dolt.debug requires a sql-server-backed project (embedded mode has no managed server). ...
  candidate (proxied):   Error: setting config: no config.yaml found in BEADS_DIR (.../.beads) (run 'bd init' first)
  ```
  Both exit 1.
- **why allowed:** `dolt.debug` is a sql-server-only key. In embedded mode
  `usesSQLServer()==false`, so bd rejects it at the dedicated gate
  (`cmd/bd/config.go:119`). In proxied mode `usesSQLServer()==true`, so the gate
  passes and bd proceeds to write the key — which needs the on-disk
  `config.yaml` that a *normal* `bd init` would have created but the spike
  bootstrap (which deliberately does **not** run gated init) does not. Both
  reject `config set dolt.debug` (exit 1); the message differs because the two
  plumbings reject it for two mode-appropriate reasons. The `config.yaml`
  absence is a wrapper-bootstrap artifact (the (d) contribution), not a store
  behavior.

## AX-3 — `comment_add_list` · `AddIssueComment` typed-unsupported, out of gc-16 scope

- **class:** (c) unimplemented method (typed `ErrUnsupported`)
- **pair:** embedded ↔ spike-proxied
- **field:** step 1 `comment cm-1 "first note" --json` → exit code (0 → 1) + stdout
- **tag:** intentional
- **evidence:**
  ```
  reference (embedded):  exit 0  (comment added)
  candidate (proxied):   exit 1
    {"error":"adding comment: operation \"AddIssueComment\" not supported by the uowstore spike backend (BD_SPIKE_UOWSTORE spike shell, gastownhall/beads#4547)","schema_version":1}
  ```
- **why allowed / NOT a census escape:** `comment` / `comments` are **not** in
  the gc-16 completion set (close, config, create, delete, dep, init, list,
  query, ready, reopen, show, update + purge, count, version, stats). The spike
  shell returns a **typed** `ErrUnsupported` naming `AddIssueComment` — the
  correct, honest behavior for an out-of-scope method, exactly what the typed
  shell is for. It surfaces here only because the harness's `IN_SCOPE_CMDS`
  (which pre-dates the gc-16 census and includes `comment`/`comments` coverage
  scenarios) is **broader** than the gc-16 completion target. This is the one
  in-scope-per-harness / out-of-scope-per-census seam, and it is not a census
  escape: no gc-16 method answered `unsupported`.

## AX-4 — `purge_real_then_reseed` · audit-event materialization count

- **class:** (b) mode difference
- **pair:** embedded ↔ spike-proxied
- **field:** step 4 `purge --force --json` → `.events`
- **tag:** known
- **evidence:**
  ```
  reference (embedded):  {"dependencies":0,"events":0,"labels":0,"purged_count":2,"schema_version":1}
  candidate (proxied):   {"dependencies":0,"events":4,"labels":0,"purged_count":2,"schema_version":1}
  ```
- **why allowed:** the primary result — `purged_count` — is **identical** (2).
  The plumbings disagree only on the `events` child-row sub-metric: the proxied
  dolt-server path materializes 4 audit-event rows (create+close for the two
  purged ephemerals) that purge then deletes and counts, while the embedded path
  reports 0 for the same all-ephemeral candidate set. This is an
  audit-event-materialization shape difference between the two storage
  plumbings, not a difference in what was purged. **Flagged `known`:** worth a
  follow-up to decide whether the uowstore should suppress audit rows for
  ephemeral issues to match embedded, but it does not change purge's contract
  outcome (count + reseed-usable workspace, which the scenario's later steps
  confirm pass).

---

**Allowlisted total: 4** (AX-1..AX-4). All remaining divergences are class-(a)
findings — see `CROSSPLUMB-REPORT.md` §Findings.

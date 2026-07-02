# Harness provenance

The Rust sources under `src/` are a **verbatim vendored copy** of the bts-rs
conformance harness:

- Upstream path: `/data/projects/bts-rs/crates/bts-conformance/src/`
- Upstream commit: `dffbcd9f4eb14457328dba50b6e196c39a441bcd`
- Files copied unchanged:
  - `src/differential.rs` — scenario runner, `normalize()` (`<TS>`/`<UUID>`/`<ACTOR>`/`<EMAIL>`), JSON-aware `diff()`
  - `src/scenarios.rs` — the curated `all()` scenarios + optional `catalog()` loader
  - `src/lib.rs` — crate root
  - `src/bin/capture_golden.rs` — golden capture (`BTS_REFERENCE_BD`, `BTS_ONLY=`)
  - `src/bin/scoreboard.rs` — candidate scorer (`BTS_CANDIDATE`, in-scope predicate)

Only `Cargo.toml` is local (standalone deps; see its header comment).

## Why vendored instead of building bts-rs in place

Oracle A must **re-capture goldens from a fresh origin/main-built `bd`** into a
directory it owns, and must never modify or commit inside `/data/projects/bts-rs`.
`capture_golden`/`scoreboard` hardcode the golden dir to
`CARGO_MANIFEST_DIR/testdata/golden`. Building from a vendored copy makes
`CARGO_MANIFEST_DIR` point at *this* directory, so goldens land in
`tests/oracle-a/harness/testdata/golden/` (git-ignored, regenerated each run)
without touching bts-rs testdata.

## Re-syncing from upstream

If the upstream harness changes and you want to pull it in:

```sh
UP=/data/projects/bts-rs/crates/bts-conformance/src
cp "$UP"/differential.rs "$UP"/scenarios.rs "$UP"/lib.rs  tests/oracle-a/harness/src/
cp "$UP"/bin/capture_golden.rs "$UP"/bin/scoreboard.rs    tests/oracle-a/harness/src/bin/
# then update the commit hash above; leave Cargo.toml as-is.
```

The `scenarios::catalog()` loader reads `../../docs/scenarios/enumerated.json`
relative to `CARGO_MANIFEST_DIR`. That path does not exist here, so `catalog()`
returns empty (handled gracefully). Oracle A intentionally runs only the curated
`all()` set (no `BTS_CATALOG`), which is the in-scope gc-contract surface.

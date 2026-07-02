#!/usr/bin/env bash
# Oracle A — refactor-safety differential conformance for Go bd.
#
# Runs the SAME gc-contract scenarios against:
#   REFERENCE  bd  — built from the merge base with origin/main (the "before")
#   CANDIDATE  bd  — built from the current working tree      (the "after")
# and diffs each step (exit code, stderr, JSON-aware stdout) with volatile
# normalization (<TS>/<UUID>/<ACTOR>/<EMAIL>). ZERO in-scope divergences is the
# gate: any in-scope FAIL means the branch changed a user-visible bd behavior
# that Gas City's contract surface depends on.
#
# What this is NOT: it is not a bts-rs run. It reuses the bts-rs conformance
# harness (vendored under harness/, see harness/PROVENANCE.md) but points it at
# two Go bd binaries. bts-rs is never modified or committed to.
#
# Usage:
#   tests/oracle-a/run-oracle-a.sh              # ref = origin/main, candidate = working tree
#   REF_REF=<gitref> tests/oracle-a/run-oracle-a.sh   # override the reference ref
#   KEEP_ARTIFACTS=1 tests/oracle-a/run-oracle-a.sh    # keep the scratch build dir
#
# Requirements: cargo (Rust), a CGO toolchain (gcc), go. See README.md.
#
# Exit status: 0 = 100% in-scope pass; 1 = at least one in-scope divergence;
#              2 = setup/build error (could not produce a result).

set -euo pipefail

# --- locate the repo and this script -------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HARNESS_DIR="$SCRIPT_DIR/harness"
REPO_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)"
REF_REF="${REF_REF:-origin/main}"

# gms_pure_go is mandatory per docs/ICU-POLICY.md; CGO is required for embedded Dolt.
BUILD_TAGS="gms_pure_go"

# unique scratch dir per run — cp over an exec-mapped binary fails silently and
# would score a stale binary, so every binary gets a fresh, unique path.
RUN_ID="$(date +%Y%m%d-%H%M%S)-$$"
SCRATCH="${TMPDIR:-/tmp}/oracle-a-$RUN_ID"
REF_SRC="$SCRATCH/ref-src"
REF_BIN="$SCRATCH/bd-reference"
CAND_BIN="$SCRATCH/bd-candidate"
mkdir -p "$SCRATCH"

log()  { printf '\033[1;36m[oracle-a]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[oracle-a]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[oracle-a]\033[0m %s\n' "$*" >&2; exit 2; }

# --- cleanup: always remove the reference worktree; drop scratch unless asked to keep -
cleanup() {
  local rc=$?
  if git -C "$REPO_ROOT" worktree list --porcelain 2>/dev/null | grep -qF "$REF_SRC"; then
    git -C "$REPO_ROOT" worktree remove --force "$REF_SRC" 2>/dev/null || true
  fi
  # Undo any spurious go.sum/go.mod churn the candidate build may have caused.
  git -C "$REPO_ROOT" checkout -- go.sum go.mod 2>/dev/null || true
  if [ "${KEEP_ARTIFACTS:-0}" = "1" ]; then
    warn "KEEP_ARTIFACTS=1 — leaving scratch at $SCRATCH"
  else
    rm -rf "$SCRATCH"
  fi
  return $rc
}
trap cleanup EXIT

# --- preflight -----------------------------------------------------------------------
command -v cargo >/dev/null 2>&1 || die "cargo not found (Rust toolchain required)"
command -v go    >/dev/null 2>&1 || die "go not found"
command -v gcc   >/dev/null 2>&1 || command -v cc >/dev/null 2>&1 || die "no C compiler (CGO required)"

REF_SHA="$(git -C "$REPO_ROOT" rev-parse "$REF_REF" 2>/dev/null)" || die "cannot resolve ref '$REF_REF' (need 'git fetch'?)"
CAND_SHA="$(git -C "$REPO_ROOT" rev-parse HEAD)"
log "reference ref : $REF_REF ($REF_SHA)"
log "candidate     : working tree (HEAD $CAND_SHA)"
if [ "$REF_SHA" = "$CAND_SHA" ] && git -C "$REPO_ROOT" diff --quiet; then
  log "note: candidate == reference (clean tree at $REF_REF) — this run proves the"
  log "      rig+normalization are leak-free (main-vs-main). Divergences here are"
  log "      harness bugs, not code changes."
fi

# --- 1. reference bd from origin/main (isolated worktree) ----------------------------
log "building REFERENCE bd from $REF_REF ..."
git -C "$REPO_ROOT" worktree add --detach "$REF_SRC" "$REF_SHA" >/dev/null 2>&1 \
  || die "git worktree add failed for $REF_SHA"
( cd "$REF_SRC" && CGO_ENABLED=1 go build -tags "$BUILD_TAGS" -o "$REF_BIN" ./cmd/bd ) \
  || die "reference bd build failed"
[ -x "$REF_BIN" ] || die "reference bd not produced at $REF_BIN"
log "reference bd : $REF_BIN ($($REF_BIN version 2>/dev/null | head -1))"

# --- 2. candidate bd from the working tree -------------------------------------------
log "building CANDIDATE bd from the working tree ..."
( cd "$REPO_ROOT" && CGO_ENABLED=1 go build -tags "$BUILD_TAGS" -o "$CAND_BIN" ./cmd/bd ) \
  || die "candidate bd build failed"
# restore any go.sum/go.mod churn immediately (belt-and-suspenders; also in cleanup)
git -C "$REPO_ROOT" checkout -- go.sum go.mod 2>/dev/null || true
[ -x "$CAND_BIN" ] || die "candidate bd not produced at $CAND_BIN"
log "candidate bd : $CAND_BIN ($($CAND_BIN version 2>/dev/null | head -1))"

# --- 3. build the vendored harness ---------------------------------------------------
log "building conformance harness (vendored bts-rs copy) ..."
( cd "$HARNESS_DIR" && cargo build --release --bins ) >/dev/null 2>&1 \
  || die "harness build failed (run 'cargo build --release --bins' in $HARNESS_DIR to see why)"
CAPTURE="$HARNESS_DIR/target/release/capture_golden"
SCOREBOARD="$HARNESS_DIR/target/release/scoreboard"

# fresh goldens every run: the reference IS the merge base, not a pinned snapshot.
rm -rf "$HARNESS_DIR/testdata/golden"

# --- 4. capture goldens from the reference bd ----------------------------------------
log "capturing goldens from REFERENCE bd ..."
BTS_REFERENCE_BD="$REF_BIN" "$CAPTURE" \
  || die "golden capture failed"

# --- 5. score the candidate against the reference goldens ----------------------------
log "scoring CANDIDATE bd against reference goldens ..."
SCORE_OUT="$SCRATCH/scoreboard.out"
BTS_CANDIDATE="$CAND_BIN" "$SCOREBOARD" | tee "$SCORE_OUT"

# --- 6. verdict from the IN-SCOPE line ------------------------------------------------
# scoreboard prints:  "  PASS: <n>   FAIL: <m>   (<p>%)"  under the IN-SCOPE header.
IN_LINE="$(grep -E '^\s*PASS:.*FAIL:' "$SCORE_OUT" | head -1)"
IN_PASS="$(printf '%s' "$IN_LINE" | sed -E 's/.*PASS:\s*([0-9]+).*/\1/')"
IN_FAIL="$(printf '%s' "$IN_LINE" | sed -E 's/.*FAIL:\s*([0-9]+).*/\1/')"

echo
if [ -z "${IN_FAIL:-}" ]; then
  die "could not parse scoreboard output"
elif [ "$IN_FAIL" -eq 0 ]; then
  log "RESULT: IN-SCOPE PASS ($IN_PASS scenarios, 0 divergences) — refactor is behavior-preserving on the gc-contract surface."
  exit 0
else
  warn "RESULT: IN-SCOPE FAIL — $IN_FAIL divergence(s) vs $REF_REF (pass: $IN_PASS)."
  warn "Per-divergence detail:"
  sed 's/^/  /' /tmp/bts-failures.txt >&2 2>/dev/null || true
  exit 1
fi

#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib/report.sh"
source "$SCRIPT_DIR/lib/versions.sh"
source "$SCRIPT_DIR/lib/binary.sh"
source "$SCRIPT_DIR/lib/workspace.sh"
source "$SCRIPT_DIR/lib/snapshot.sh"
source "$SCRIPT_DIR/recipes/sqlite_to_current.sh"

fail() {
    echo "strict-mode-test: $*" >&2
    exit 1
}

asset=$(strict_release_asset v0.49.6 linux amd64)
[ "$asset" = "beads_0.49.6_linux_amd64.tar.gz" ] || fail "unexpected release asset"
sha=$(strict_release_sha256 v0.49.6 linux amd64)
[ "$sha" = "8546dc9a47e11dc31ac2bc9a0224a9c690975e91850932cbb62623053fbb7db8" ] || fail "unexpected release checksum"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
printf 'not the release archive' > "$tmp/archive.tar.gz"
if OS=linux ARCH=amd64 verify_release_archive v0.49.6 "$tmp/archive.tar.gz" >/dev/null 2>&1; then
    fail "tampered release archive was accepted"
fi

strict_fixture_has_expected_features v0.49.6 epic task bug dependency standalone closed label comment ||
    fail "complete source fixture was rejected"
if strict_fixture_has_expected_features v0.49.6 epic task bug standalone closed label >/dev/null 2>&1; then
    fail "source fixture without dependency was accepted"
fi
if strict_fixture_has_expected_features v0.49.6 epic task bug dependency standalone closed label >/dev/null 2>&1; then
    fail "source fixture without comment was accepted"
fi

declare -gA DATASET_IDS=(
    [standalone]="old-standalone"
    [closed]="old-closed"
    [task]="old-task"
    [bug]="old-bug"
)
printf '%s\n' '[
  {"id":"old-standalone","title":"Standalone detailed task","description":"This task has a detailed description for fidelity testing.","notes":"Historical notes must survive the upgrade.","design":"Historical design must survive the upgrade.","acceptance_criteria":"Historical acceptance criteria must survive the upgrade.","external_ref":"legacy-upgrade-42","status":"open"},
  {"id":"old-closed","title":"Already closed issue","status":"closed"},
  {"id":"old-task","title":"Implement core feature","status":"open","labels":["urgent"],"comments":[{"author":"legacy-author","text":"Historical comment must survive the upgrade."}]},
  {"id":"old-bug","title":"Fix migration blocker","status":"open","dependencies":[{"id":"old-task"}]}
]' > "$tmp/fixture.json"
strict_snapshot_has_expected_fixture v0.49.6 "$tmp/fixture.json" ||
    fail "exact v0.49.6 source fixture was rejected"
jq 'map(if .id == "old-standalone" then .external_ref = null else . end)' \
    "$tmp/fixture.json" > "$tmp/fixture-missing-rich-field.json"
if strict_snapshot_has_expected_fixture v0.49.6 "$tmp/fixture-missing-rich-field.json" >/dev/null 2>&1; then
    fail "source fixture without the exact rich fields was accepted"
fi
jq 'map(if .id == "old-task" then .labels = [] else . end)' \
    "$tmp/fixture.json" > "$tmp/fixture-missing-label.json"
if strict_snapshot_has_expected_fixture v0.49.6 "$tmp/fixture-missing-label.json" >/dev/null 2>&1; then
    fail "source fixture without the exact label was accepted"
fi
jq 'map(if .id == "old-bug" then .dependencies = [] else . end)' \
    "$tmp/fixture.json" > "$tmp/fixture-missing-dependency.json"
if strict_snapshot_has_expected_fixture v0.49.6 "$tmp/fixture-missing-dependency.json" >/dev/null 2>&1; then
    fail "source fixture without the exact dependency was accepted"
fi

mkdir -p "$tmp/source/.beads"
printf 'original sqlite bytes' > "$tmp/source/.beads/beads.db"
printf 'original sqlite config' > "$tmp/source/.beads/config.yaml"
source_manifest=$(classic_sqlite_artifact_manifest "$tmp/source/.beads")
mv -f "$tmp/source/.beads/beads.db" "$tmp/source/.beads/beads.db.pre-migration"
mv -f "$tmp/source/.beads/config.yaml" "$tmp/source/.beads/config.yaml.pre-migration"
verify_retained_sqlite_source "$tmp/source/.beads" "$source_manifest" ||
    fail "unchanged retained SQLite source was rejected"
printf 'corruption' >> "$tmp/source/.beads/config.yaml.pre-migration"
if verify_retained_sqlite_source "$tmp/source/.beads" "$source_manifest" >/dev/null 2>&1; then
    fail "mutated retained SQLite source was accepted"
fi

mkdir -p "$tmp/collision/.beads"
printf 'current' > "$tmp/collision/.beads/beads.db"
printf 'current config' > "$tmp/collision/.beads/config.yaml"
printf 'stale config' > "$tmp/collision/.beads/config.yaml.pre-migration"
if preserve_sqlite_source_artifacts "$tmp/collision" >/dev/null 2>&1; then
    fail "stale rollback collision was accepted"
fi
[ "$(cat "$tmp/collision/.beads/beads.db")" = "current" ] ||
    fail "rollback collision mutated the active source"
[ ! -e "$tmp/collision/.beads/beads.db.pre-migration" ] ||
    fail "rollback preflight left a partial backup before reporting collision"

RESULT_PATHS=("v0.49.6 → candidate")
RESULT_STATUSES=("MANUAL")
RESULT_DETAILS=("recipe: sqlite_to_current, 0 fidelity violations")
RESULT_RECIPES=("sqlite_to_current")
RESULT_VIOLATIONS=("0")
strict_results_match MANUAL sqlite_to_current || fail "qualified result was rejected"

RESULT_STATUSES=("SKIP")
if strict_results_match MANUAL sqlite_to_current >/dev/null 2>&1; then
    fail "SKIP result was accepted"
fi
RESULT_STATUSES=("MANUAL")
RESULT_VIOLATIONS=("1")
if strict_results_match MANUAL sqlite_to_current >/dev/null 2>&1; then
    fail "fidelity violation was accepted"
fi
RESULT_VIOLATIONS=("0")
if strict_results_match AUTO sqlite_to_current >/dev/null 2>&1; then
    fail "outcome drift was accepted"
fi
if strict_results_match MANUAL different_recipe >/dev/null 2>&1; then
    fail "recipe drift was accepted"
fi

STRICT_MODE=true
printf '%s\n' \
    '[{"id":"old-1","title":"One","description":"","priority":2,"issue_type":"task","status":"open","dependencies":[],"labels":[],"comment_count":0}]' \
    > "$tmp/before.json"
printf '%s\n' \
    '[{"id":"old-1","title":"One","description":"","priority":2,"issue_type":"task","status":"open","dependencies":[{"id":"invented"}],"labels":["invented"],"comment_count":1}]' \
    > "$tmp/after-invented.json"
if check_fidelity v0.49.6 "$tmp/before.json" "$tmp/after-invented.json" >/dev/null 2>&1; then
    fail "strict fidelity accepted invented collections"
fi
printf '%s\n' \
    '[{"id":"old-1","title":"One","description":"","priority":2,"issue_type":"task","status":"open","dependencies":[],"labels":[],"comments":[{"author":"legacy-author","text":"before"}]}]' \
    > "$tmp/before-comment.json"
printf '%s\n' \
    '[{"id":"old-1","title":"One","description":"","priority":2,"issue_type":"task","status":"open","dependencies":[],"labels":[],"comments":[{"author":"legacy-author","text":"after"}]}]' \
    > "$tmp/after-comment.json"
if check_fidelity v0.49.6 "$tmp/before-comment.json" "$tmp/after-comment.json" >/dev/null 2>&1; then
    fail "strict fidelity accepted changed comment text"
fi

fake="$tmp/fake-bd"
printf '%s\n' \
    '#!/bin/bash' \
    'if [ "$1" = "list" ]; then printf '\''[{"id":"old-1"},{"id":"old-2"}]\n'\''; exit 0; fi' \
    'if [ "$1" = "show" ] && [ "$2" = "old-1" ]; then printf '\''[{"id":"old-1","title":"One"}]\n'\''; exit 0; fi' \
    'exit 1' > "$fake"
chmod +x "$fake"
DATASET_IDS=([one]="old-1" [two]="old-2")
if capture_snapshot "$tmp" "$fake" > "$tmp/partial.json" 2>/dev/null; then
    fail "strict snapshot accepted a failed bd show"
fi

echo "strict-mode-test: PASS"

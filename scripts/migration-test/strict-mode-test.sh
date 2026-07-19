#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib/report.sh"
source "$SCRIPT_DIR/lib/versions.sh"
source "$SCRIPT_DIR/lib/binary.sh"
source "$SCRIPT_DIR/lib/workspace.sh"
source "$SCRIPT_DIR/lib/snapshot.sh"
source "$SCRIPT_DIR/lib/direct_probe.sh"
source "$SCRIPT_DIR/recipes/sqlite_to_current.sh"
source "$SCRIPT_DIR/recipes/server_to_embedded.sh"

fail() {
    echo "strict-mode-test: $*" >&2
    exit 1
}

asset=$(strict_release_asset v0.49.6 linux amd64)
[ "$asset" = "beads_0.49.6_linux_amd64.tar.gz" ] || fail "unexpected release asset"
sha=$(strict_release_sha256 v0.49.6 linux amd64)
[ "$sha" = "8546dc9a47e11dc31ac2bc9a0224a9c690975e91850932cbb62623053fbb7db8" ] || fail "unexpected release checksum"

asset=$(strict_release_asset v0.55.4 linux amd64) || fail "missing v0.55.4 release asset"
[ "$asset" = "beads_0.55.4_linux_amd64.tar.gz" ] || fail "unexpected v0.55.4 release asset"
sha=$(strict_release_sha256 v0.55.4 linux amd64) || fail "missing v0.55.4 release checksum"
[ "$sha" = "e0fa25456dd82890230eef17653448a0bf995104c78864be91c5ed84426a5f49" ] ||
    fail "unexpected v0.55.4 release checksum"
[ "$(strict_expected_status v0.55.4)" = "MANUAL" ] || fail "unexpected v0.55.4 status"
[ "$(strict_expected_recipe v0.55.4)" = "server_to_embedded" ] || fail "unexpected v0.55.4 recipe"
[ "$(strict_expected_features v0.55.4)" = "epic task bug dependency standalone closed label comment" ] ||
    fail "unexpected v0.55.4 source features"
[ "${LEGACY_DOLT_ROLLBACK_FILES[*]}" = "metadata.json config.json config.yaml issues.jsonl" ] ||
    fail "unexpected legacy Dolt rollback file inventory"

for lookup in strict_release_asset strict_release_sha256; do
    if "$lookup" v9.9.9 linux amd64 >/dev/null 2>&1; then
        fail "$lookup accepted an unknown release manifest"
    fi
done
for lookup in strict_expected_status strict_expected_recipe strict_expected_features; do
    if "$lookup" v9.9.9 >/dev/null 2>&1; then
        fail "$lookup accepted an unknown qualification manifest"
    fi
done

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
printf 'not the release archive' > "$tmp/archive.tar.gz"
if OS=linux ARCH=amd64 verify_release_archive v0.49.6 "$tmp/archive.tar.gz" >/dev/null 2>&1; then
    fail "tampered release archive was accepted"
fi
if OS=linux ARCH=amd64 verify_release_archive v0.55.4 "$tmp/archive.tar.gz" >/dev/null 2>&1; then
    fail "tampered v0.55.4 release archive was accepted"
fi

strict_fixture_has_expected_features v0.49.6 epic task bug dependency standalone closed label comment ||
    fail "complete source fixture was rejected"
strict_fixture_has_expected_features v0.55.4 epic task bug dependency standalone closed label comment ||
    fail "complete v0.55.4 source fixture was rejected"
if strict_fixture_has_expected_features v0.49.6 epic task bug standalone closed label >/dev/null 2>&1; then
    fail "source fixture without dependency was accepted"
fi
if strict_fixture_has_expected_features v0.49.6 epic task bug dependency standalone closed label >/dev/null 2>&1; then
    fail "source fixture without comment was accepted"
fi

declare -gA DATASET_IDS=(
    [epic]="old-epic"
    [standalone]="old-standalone"
    [closed]="old-closed"
    [task]="old-task"
    [bug]="old-bug"
)
printf '%s\n' '[
  {"id":"old-epic","title":"Migration epic","description":"Epic for migration testing","priority":2,"issue_type":"epic","status":"open"},
  {"id":"old-standalone","title":"Standalone detailed task","description":"This task has a detailed description for fidelity testing.","notes":"Historical notes must survive the upgrade.","design":"Historical design must survive the upgrade.","acceptance_criteria":"Historical acceptance criteria must survive the upgrade.","external_ref":"legacy-upgrade-42","status":"open"},
  {"id":"old-closed","title":"Already closed issue","status":"closed"},
  {"id":"old-task","title":"Implement core feature","status":"open","labels":["urgent"],"comments":[{"author":"legacy-author","text":"Historical comment must survive the upgrade."}]},
  {"id":"old-bug","title":"Fix migration blocker","status":"open","dependencies":[{"id":"old-task"}]}
]' > "$tmp/fixture.json"
strict_snapshot_has_expected_fixture v0.49.6 "$tmp/fixture.json" ||
    fail "exact v0.49.6 source fixture was rejected"
strict_snapshot_has_expected_fixture v0.55.4 "$tmp/fixture.json" ||
    fail "exact v0.55.4 source fixture was rejected"
jq 'map(select(.id != "old-epic"))' "$tmp/fixture.json" > "$tmp/fixture-four-items.json"
for version in v0.49.6 v0.55.4; do
    if strict_snapshot_has_expected_fixture "$version" "$tmp/fixture-four-items.json" >/dev/null 2>&1; then
        fail "$version source fixture with four items was accepted"
    fi
done
jq '. + [{"id":"old-extra","title":"Unexpected extra issue"}]' \
    "$tmp/fixture.json" > "$tmp/fixture-six-items.json"
for version in v0.49.6 v0.55.4; do
    if strict_snapshot_has_expected_fixture "$version" "$tmp/fixture-six-items.json" >/dev/null 2>&1; then
        fail "$version source fixture with six items was accepted"
    fi
done
jq 'map(if .id == "old-epic" then .issue_type = "task" else . end)' \
    "$tmp/fixture.json" > "$tmp/fixture-wrong-epic.json"
for version in v0.49.6 v0.55.4; do
    if strict_snapshot_has_expected_fixture "$version" "$tmp/fixture-wrong-epic.json" >/dev/null 2>&1; then
        fail "$version source fixture with an inexact epic was accepted"
    fi
done
jq 'map(if .id == "old-standalone" then .external_ref = null else . end)' \
    "$tmp/fixture.json" > "$tmp/fixture-missing-rich-field.json"
if strict_snapshot_has_expected_fixture v0.49.6 "$tmp/fixture-missing-rich-field.json" >/dev/null 2>&1; then
    fail "source fixture without the exact rich fields was accepted"
fi
if strict_snapshot_has_expected_fixture v0.55.4 "$tmp/fixture-missing-rich-field.json" >/dev/null 2>&1; then
    fail "v0.55.4 source fixture without the exact rich fields was accepted"
fi
jq 'map(if .id == "old-task" then .labels = [] else . end)' \
    "$tmp/fixture.json" > "$tmp/fixture-missing-label.json"
if strict_snapshot_has_expected_fixture v0.49.6 "$tmp/fixture-missing-label.json" >/dev/null 2>&1; then
    fail "source fixture without the exact label was accepted"
fi
if strict_snapshot_has_expected_fixture v0.55.4 "$tmp/fixture-missing-label.json" >/dev/null 2>&1; then
    fail "v0.55.4 source fixture without the exact label was accepted"
fi
jq 'map(if .id == "old-bug" then .dependencies = [] else . end)' \
    "$tmp/fixture.json" > "$tmp/fixture-missing-dependency.json"
if strict_snapshot_has_expected_fixture v0.49.6 "$tmp/fixture-missing-dependency.json" >/dev/null 2>&1; then
    fail "source fixture without the exact dependency was accepted"
fi
if strict_snapshot_has_expected_fixture v0.55.4 "$tmp/fixture-missing-dependency.json" >/dev/null 2>&1; then
    fail "v0.55.4 source fixture without the exact dependency was accepted"
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

# A symlinked rollback artifact must be rejected with no-follow semantics. A
# .pre-migration copy (or active source) that resolves outside the workspace is
# not self-contained, so following it would let strict rollback verification
# pass after the real source is deleted. Mirrors the legacy Dolt recipe.
mkdir -p "$tmp/symlink-retained/.beads"
printf 'outside retained bytes' > "$tmp/outside-retained.db"
ln -s "$tmp/outside-retained.db" "$tmp/symlink-retained/.beads/beads.db.pre-migration"
if classic_sqlite_artifact_manifest "$tmp/symlink-retained/.beads" ".pre-migration" >/dev/null 2>&1; then
    fail "symlinked retained SQLite rollback artifact was accepted"
fi

mkdir -p "$tmp/symlink-source/.beads"
printf 'outside source bytes' > "$tmp/outside-source.db"
ln -s "$tmp/outside-source.db" "$tmp/symlink-source/.beads/beads.db"
if preserve_sqlite_source_artifacts "$tmp/symlink-source" >/dev/null 2>&1; then
    fail "symlinked active SQLite source was accepted"
fi
[ -L "$tmp/symlink-source/.beads/beads.db" ] ||
    fail "symlinked active SQLite source preflight mutated the source link"
[ ! -e "$tmp/symlink-source/.beads/beads.db.pre-migration" ] ||
    fail "symlinked active SQLite source preflight left a backup"

mkdir -p "$tmp/symlink-backup/.beads"
printf 'active bytes' > "$tmp/symlink-backup/.beads/beads.db"
printf 'outside backup bytes' > "$tmp/outside-backup.db"
ln -s "$tmp/outside-backup.db" "$tmp/symlink-backup/.beads/beads.db.pre-migration"
if preserve_sqlite_source_artifacts "$tmp/symlink-backup" >/dev/null 2>&1; then
    fail "symlinked SQLite rollback artifact was accepted"
fi
[ "$(cat "$tmp/symlink-backup/.beads/beads.db")" = "active bytes" ] ||
    fail "symlinked rollback preflight mutated the active source"

fingerprint_root="$tmp/fingerprint-source/.beads"
mkdir -p "$fingerprint_root/nested"
printf 'stable bytes' > "$fingerprint_root/nested/data"
ln -s nested/data "$fingerprint_root/data-link"
fingerprint_one=$(source_artifact_fingerprint "$fingerprint_root") ||
    fail "stable historical source could not be fingerprinted"
fingerprint_two=$(source_artifact_fingerprint "$fingerprint_root") ||
    fail "stable historical source could not be fingerprinted twice"
[ "$fingerprint_one" = "$fingerprint_two" ] ||
    fail "stable historical source produced different fingerprints"

if (
    find() { command find "$@"; return 1; }
    source_artifact_fingerprint "$fingerprint_root"
) >/dev/null 2>&1; then
    fail "historical source fingerprint ignored a failed find"
fi
if (
    stat() { return 1; }
    source_artifact_fingerprint "$fingerprint_root"
) >/dev/null 2>&1; then
    fail "historical source fingerprint ignored a failed stat"
fi
if (
    sha256_file() {
        if [ "$1" = "./nested/data" ]; then
            return 1
        fi
        sha256sum -- "$1" | awk '{print $1}'
    }
    source_artifact_fingerprint "$fingerprint_root"
) >/dev/null 2>&1; then
    fail "historical source fingerprint ignored a failed checksum"
fi
if (
    sha256_file() {
        if [[ "$1" == */bd-source-fingerprint.* ]]; then
            return 1
        fi
        sha256sum -- "$1" | awk '{print $1}'
    }
    source_artifact_fingerprint "$fingerprint_root"
) >/dev/null 2>&1; then
    fail "historical source fingerprint ignored a failed final digest"
fi
if (
    readlink() { return 1; }
    source_artifact_fingerprint "$fingerprint_root"
) >/dev/null 2>&1; then
    fail "historical source fingerprint ignored a failed readlink"
fi
mkfifo "$fingerprint_root/unsupported-fifo"
if source_artifact_fingerprint "$fingerprint_root" >/dev/null 2>&1; then
    fail "historical source fingerprint accepted a special file"
fi
rm -f "$fingerprint_root/unsupported-fifo"
printf 'changed bytes' >> "$fingerprint_root/nested/data"
fingerprint_changed=$(source_artifact_fingerprint "$fingerprint_root") ||
    fail "changed historical source could not be fingerprinted"
[ "$fingerprint_changed" != "$fingerprint_one" ] ||
    fail "historical source fingerprint ignored changed file bytes"

mkdir -p "$tmp/legacy-source/.beads/dolt/nested"
printf 'legacy table bytes' > "$tmp/legacy-source/.beads/dolt/nested/table.dat"
printf 'legacy metadata' > "$tmp/legacy-source/.beads/metadata.json"
printf 'legacy config json' > "$tmp/legacy-source/.beads/config.json"
printf 'legacy config yaml' > "$tmp/legacy-source/.beads/config.yaml"
legacy_manifest=$(legacy_dolt_artifact_manifest "$tmp/legacy-source/.beads")
preserve_legacy_dolt_source "$tmp/legacy-source" "$legacy_manifest" ||
    fail "unchanged legacy Dolt source could not be preserved"
verify_retained_legacy_dolt_source "$tmp/legacy-source/.beads" "$legacy_manifest" ||
    fail "unchanged retained legacy Dolt source was rejected"
printf 'corruption' >> "$tmp/legacy-source/.beads/legacy-dolt.pre-migration/config.yaml"
if verify_retained_legacy_dolt_source "$tmp/legacy-source/.beads" "$legacy_manifest" >/dev/null 2>&1; then
    fail "mutated retained legacy Dolt source was accepted"
fi

mkdir -p "$tmp/legacy-collision/.beads/dolt" \
    "$tmp/legacy-collision/.beads/legacy-dolt.pre-migration/dolt"
printf 'active legacy data' > "$tmp/legacy-collision/.beads/dolt/table.dat"
printf 'active metadata' > "$tmp/legacy-collision/.beads/metadata.json"
printf 'stale legacy data' > "$tmp/legacy-collision/.beads/legacy-dolt.pre-migration/dolt/table.dat"
collision_manifest=$(legacy_dolt_artifact_manifest "$tmp/legacy-collision/.beads")
if preserve_legacy_dolt_source "$tmp/legacy-collision" "$collision_manifest" >/dev/null 2>&1; then
    fail "stale legacy Dolt rollback collision was accepted"
fi
[ "$(legacy_dolt_artifact_manifest "$tmp/legacy-collision/.beads")" = "$collision_manifest" ] ||
    fail "legacy Dolt rollback collision mutated the active source"
if find "$tmp/legacy-collision/.beads" -maxdepth 1 \
    -name '.legacy-dolt.pre-migration.tmp.*' -print -quit | grep -q .; then
    fail "legacy Dolt rollback collision left a partial temporary backup"
fi

mkdir -p "$tmp/legacy-partial/.beads/dolt"
printf 'active legacy data' > "$tmp/legacy-partial/.beads/dolt/table.dat"
printf 'active metadata' > "$tmp/legacy-partial/.beads/metadata.json"
partial_manifest=$(legacy_dolt_artifact_manifest "$tmp/legacy-partial/.beads")
cp() {
    local arg
    for arg in "$@"; do
        if [[ "$arg" == */metadata.json ]]; then
            return 1
        fi
    done
    command cp "$@"
}
if preserve_legacy_dolt_source "$tmp/legacy-partial" "$partial_manifest" >/dev/null 2>&1; then
    unset -f cp
    fail "partial legacy Dolt rollback copy was accepted"
fi
unset -f cp
[ ! -e "$tmp/legacy-partial/.beads/legacy-dolt.pre-migration" ] ||
    fail "partial legacy Dolt rollback copy published an incomplete backup"
if find "$tmp/legacy-partial/.beads" -maxdepth 1 \
    -name '.legacy-dolt.pre-migration.tmp.*' -print -quit | grep -q .; then
    fail "partial legacy Dolt rollback copy left a temporary backup"
fi
[ "$(legacy_dolt_artifact_manifest "$tmp/legacy-partial/.beads")" = "$partial_manifest" ] ||
    fail "partial legacy Dolt rollback copy mutated the active source"

legacy_extra_ws="$tmp/legacy-extra"
mkdir -p "$legacy_extra_ws/.beads/dolt"
printf 'active data' > "$legacy_extra_ws/.beads/dolt/table.dat"
printf 'active metadata' > "$legacy_extra_ws/.beads/metadata.json"
legacy_extra_manifest=$(legacy_dolt_artifact_manifest "$legacy_extra_ws/.beads")
preserve_legacy_dolt_source "$legacy_extra_ws" "$legacy_extra_manifest" ||
    fail "could not create exact rollback source for extra-inventory test"
printf 'stale extra' > "$legacy_extra_ws/.beads/legacy-dolt.pre-migration/unexpected"
if verify_retained_legacy_dolt_source \
    "$legacy_extra_ws/.beads" "$legacy_extra_manifest" >/dev/null 2>&1; then
    fail "retained legacy Dolt source accepted unexpected top-level content"
fi
if preserve_legacy_dolt_source "$legacy_extra_ws" "$legacy_extra_manifest" >/dev/null 2>&1; then
    fail "legacy Dolt preservation reused a rollback source with extra content"
fi
[ "$(legacy_dolt_artifact_manifest "$legacy_extra_ws/.beads")" = "$legacy_extra_manifest" ] ||
    fail "extra rollback collision mutated the active legacy source"

legacy_race_ws="$tmp/legacy-publish-race"
legacy_race_calls="$tmp/legacy-publish-race.calls"
legacy_race_old_bin="$tmp/fake-race-old-bd"
legacy_race_candidate_bin="$tmp/fake-race-candidate-bd"
legacy_race_before="$tmp/legacy-publish-race-before.json"
mkdir -p "$legacy_race_ws/.beads/dolt"
printf 'active race data' > "$legacy_race_ws/.beads/dolt/table.dat"
printf 'active race metadata' > "$legacy_race_ws/.beads/metadata.json"
legacy_race_manifest=$(legacy_dolt_artifact_manifest "$legacy_race_ws/.beads")
printf '%s\n' '[{"id":"race-1"}]' > "$legacy_race_before"
cat > "$legacy_race_old_bin" <<'EOF'
#!/bin/bash
printf 'old:%s\n' "$*" >> "$LEGACY_RACE_CALLS"
exit 2
EOF
cat > "$legacy_race_candidate_bin" <<'EOF'
#!/bin/bash
printf 'candidate:%s\n' "$*" >> "$LEGACY_RACE_CALLS"
exit 2
EOF
chmod +x "$legacy_race_old_bin" "$legacy_race_candidate_bin"
export LEGACY_RACE_CALLS="$legacy_race_calls"
: > "$legacy_race_calls"
if (
    stop_dolt_server() { :; }
    publish_legacy_dolt_rollback() {
        local source="$1"
        local destination="$2"
        mkdir -p "$destination"
        printf 'racer sentinel' > "$destination/racer"
        command mv --no-target-directory --no-clobber --no-copy -- "$source" "$destination"
    }
    recipe_server_to_embedded \
        "$legacy_race_ws" "$legacy_race_old_bin" "$legacy_race_candidate_bin" \
        v0.55.4 "$legacy_race_before"
) >/dev/null 2>&1; then
    fail "legacy Dolt rollback publication accepted a racing destination"
fi
[ ! -s "$legacy_race_calls" ] ||
    fail "rollback publication race invoked a migration binary"
[ "$(legacy_dolt_artifact_manifest "$legacy_race_ws/.beads")" = "$legacy_race_manifest" ] ||
    fail "rollback publication race mutated the active legacy source"
[ "$(cat "$legacy_race_ws/.beads/legacy-dolt.pre-migration/racer")" = "racer sentinel" ] ||
    fail "rollback publication race replaced the competing destination"
if find "$legacy_race_ws/.beads" -maxdepth 2 \
    -name '.legacy-dolt.pre-migration.tmp.*' -print -quit | grep -q .; then
    fail "rollback publication race left a temporary copy"
fi

[ "$(server_bridge_strategy v0.55.4)" = "native_export" ] ||
    fail "v0.55.4 did not select its pinned server bridge strategy"
for unsupported_version in v0.56.1 v0.57.0 v0.58.0 v9.9.9; do
    if server_bridge_strategy "$unsupported_version" >/dev/null 2>&1; then
        fail "$unsupported_version unexpectedly selected a server bridge strategy"
    fi
done

unsupported_ws="$tmp/unsupported-server-bridge"
unsupported_calls="$tmp/unsupported-server-bridge.calls"
mkdir -p "$unsupported_ws/.beads/dolt"
printf 'unsupported data' > "$unsupported_ws/.beads/dolt/table.dat"
unsupported_fingerprint=$(source_artifact_fingerprint "$unsupported_ws/.beads")
export LEGACY_RACE_CALLS="$unsupported_calls"
: > "$unsupported_calls"
if recipe_server_to_embedded \
    "$unsupported_ws" "$legacy_race_old_bin" "$legacy_race_candidate_bin" \
    v0.56.1 "$legacy_race_before" >/dev/null 2>&1; then
    fail "unqualified v0.56.1 server bridge was accepted"
fi
[ ! -s "$unsupported_calls" ] ||
    fail "unqualified server bridge invoked a migration binary"
[ "$(source_artifact_fingerprint "$unsupported_ws/.beads")" = "$unsupported_fingerprint" ] ||
    fail "unqualified server bridge mutated the historical source"

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

snapshot_contract_ws="$tmp/snapshot-contract-workspace"
snapshot_contract_bin="$tmp/fake-snapshot-contract-bd"
mkdir -p "$snapshot_contract_ws"
cat > "$snapshot_contract_bin" <<'EOF'
#!/bin/bash
case "$1" in
    list)
        case "$SNAPSHOT_LIST_SHAPE" in
            array) printf '%s\n' '[{"id":"old-1","title":"One"}]' ;;
            empty-id) printf '%s\n' '[{"id":"","title":"One"}]' ;;
            object) printf '%s\n' '{"id":"old-1","title":"One"}' ;;
            invalid) printf '%s\n' 'not JSON' ;;
            *) exit 2 ;;
        esac
        [ "$SNAPSHOT_FAIL_COMMAND" != "list" ]
        ;;
    show)
        printf '%s\n' '[{"id":"old-1","title":"One"}]'
        [ "$SNAPSHOT_FAIL_COMMAND" != "show" ]
        ;;
    *)
        exit 2
        ;;
esac
EOF
chmod +x "$snapshot_contract_bin"
DATASET_IDS=([one]="old-1")

assert_strict_snapshot_rejected() {
    local name="$1"
    local list_shape="$2"
    local fail_command="$3"
    export SNAPSHOT_LIST_SHAPE="$list_shape"
    export SNAPSHOT_FAIL_COMMAND="$fail_command"
    if capture_snapshot "$snapshot_contract_ws" "$snapshot_contract_bin" \
        > "$tmp/rejected-snapshot.json" 2>/dev/null; then
        fail "strict snapshot accepted $name"
    fi
}

assert_strict_snapshot_rejected "valid list JSON from a nonzero command" array list
assert_strict_snapshot_rejected "valid show JSON from a nonzero command" array show
assert_strict_snapshot_rejected "malformed list JSON" invalid none
assert_strict_snapshot_rejected "a list JSON object" object none
assert_strict_snapshot_rejected "a list item with an empty id" empty-id none

blocker_contract_ws="$tmp/blocker-contract-workspace"
blocker_contract_bin="$tmp/fake-blocker-contract-bd"
mkdir -p "$blocker_contract_ws"
cat > "$blocker_contract_bin" <<'EOF'
#!/bin/bash
case "$1" in
    blocked)
        printf '%s\n' '[{"id":"old-bug"}]'
        [ "$BLOCKER_FAIL_COMMAND" != "blocked" ]
        ;;
    ready)
        if [ -e "$BLOCKER_STATE" ]; then
            printf '%s\n' '[{"id":"old-bug"}]'
        else
            printf '%s\n' '[{"id":"old-task"}]'
        fi
        [ "$BLOCKER_FAIL_COMMAND" != "ready" ]
        ;;
    close)
        : > "$BLOCKER_STATE"
        ;;
    *)
        exit 2
        ;;
esac
EOF
chmod +x "$blocker_contract_bin"
DATASET_FEATURES=("dependency")
DATASET_IDS=([task]="old-task" [bug]="old-bug")
export BLOCKER_STATE="$tmp/blocker-contract-closed"

assert_blocker_command_failures_counted() {
    local command="$1"
    local expected_violations="$2"
    local violations=0
    export BLOCKER_FAIL_COMMAND="$command"
    rm -f "$BLOCKER_STATE"
    check_blocker_paths "$blocker_contract_ws" "$blocker_contract_bin" \
        >/dev/null 2>&1 || violations=$?
    [ "$violations" -eq "$expected_violations" ] ||
        fail "nonzero $command produced $violations blocker violations, want $expected_violations"
}

assert_blocker_command_failures_counted blocked 1
assert_blocker_command_failures_counted ready 2

jsonl_contract_snapshot="$tmp/jsonl-contract-snapshot.json"
jsonl_contract_file="$tmp/jsonl-contract.jsonl"
printf '%s\n' '[{"id":"jsonl-1"},{"id":"jsonl-2"}]' > "$jsonl_contract_snapshot"
printf '%s\n' '{"id":"jsonl-2"}' '{"id":"jsonl-1"}' > "$jsonl_contract_file"
migration_jsonl_matches_snapshot "$jsonl_contract_file" "$jsonl_contract_snapshot" ||
    fail "valid reordered historical JSONL did not match its source snapshot"

assert_jsonl_contract_rejected() {
    local name="$1"
    shift
    printf '%s\n' "$@" > "$jsonl_contract_file"
    if migration_jsonl_matches_snapshot \
        "$jsonl_contract_file" "$jsonl_contract_snapshot" >/dev/null 2>&1; then
        fail "historical JSONL contract accepted $name"
    fi
}

assert_jsonl_contract_rejected "malformed JSON" 'not JSON'
assert_jsonl_contract_rejected "a blank record" '{"id":"jsonl-1"}' '   ' '{"id":"jsonl-2"}'
assert_jsonl_contract_rejected "a duplicate id" '{"id":"jsonl-1"}' '{"id":"jsonl-1"}'
assert_jsonl_contract_rejected "a missing id" '{"title":"missing"}' '{"id":"jsonl-2"}'
assert_jsonl_contract_rejected "an empty id" '{"id":""}' '{"id":"jsonl-2"}'
assert_jsonl_contract_rejected "an array record" '[{"id":"jsonl-1"}]' '{"id":"jsonl-2"}'
assert_jsonl_contract_rejected "an extra id" '{"id":"jsonl-1"}' '{"id":"jsonl-2"}' '{"id":"jsonl-3"}'
assert_jsonl_contract_rejected "a missing source id" '{"id":"jsonl-1"}'
printf '%s\n' '{"id":"jsonl-1"} {"id":"jsonl-2"}' > "$jsonl_contract_file"
if migration_jsonl_matches_snapshot \
    "$jsonl_contract_file" "$jsonl_contract_snapshot" >/dev/null 2>&1; then
    fail "historical JSONL contract accepted two records on one line"
fi

server_export_ws="$tmp/server-export-workspace"
server_export_calls="$tmp/server-export-calls"
server_export_old_bin="$tmp/fake-v0.55.4-bd"
server_export_candidate_bin="$tmp/fake-candidate-bd"
server_export_before="$tmp/server-export-before.json"
mkdir -p "$server_export_ws/.beads/dolt"
printf 'legacy Dolt data' > "$server_export_ws/.beads/dolt/table.dat"
printf 'legacy metadata' > "$server_export_ws/.beads/metadata.json"
printf '%s\n' '{"id":"old-task","title":"stale passive export"}' \
    > "$server_export_ws/.beads/issues.jsonl"
server_export_original_jsonl=$(sha256_file "$server_export_ws/.beads/issues.jsonl")
: > "$server_export_calls"
printf '%s\n' '[{"id":"old-task","title":"Implement core feature"}]' > "$server_export_before"
cat > "$server_export_old_bin" <<'EOF'
#!/bin/bash
printf '%s\n' "$*" >> "$SERVER_EXPORT_CALLS"
case "$1" in
    list)
        printf '%s\n' '[{"id":"old-task","title":"Implement core feature","comment_count":1}]'
        ;;
    export)
        [ "$#" -eq 5 ] && [ "$2" = "--format" ] && [ "$3" = "jsonl" ] &&
            [ "$4" = "-o" ] && [ -n "$5" ] || exit 2
        printf '%s\n' '{"id":"old-task","title":"Implement core feature","comments":[{"author":"legacy-author","text":"Historical comment must survive the upgrade."}]}' > "$5"
        ;;
    *)
        exit 2
        ;;
esac
EOF
cat > "$server_export_candidate_bin" <<'EOF'
#!/bin/bash
[ "$1" = "init" ] || exit 2
for arg in "$@"; do
    if [ "$arg" = "--from-jsonl" ]; then
        jq -e 'select(.id == "old-task") | any(.comments[]?; .author == "legacy-author" and .text == "Historical comment must survive the upgrade.")' \
            .beads/issues.jsonl >/dev/null
        exit
    fi
done
exit 1
EOF
chmod +x "$server_export_old_bin" "$server_export_candidate_bin"
export SERVER_EXPORT_CALLS="$server_export_calls"
if ! recipe_server_to_embedded \
    "$server_export_ws" "$server_export_old_bin" "$server_export_candidate_bin" \
    v0.55.4 "$server_export_before" \
    >/dev/null; then
    fail "server-to-embedded recipe lost comments exposed only by historical export"
fi
grep -E "^export --format jsonl -o $server_export_ws/\.beads/\.issues\.jsonl\.migration\.tmp\." \
    "$server_export_calls" >/dev/null ||
    fail "server-to-embedded recipe did not invoke historical export with an output path"
jq -e 'select(.id == "old-task") | any(.comments[]?; .author == "legacy-author" and .text == "Historical comment must survive the upgrade.")' \
    "$server_export_ws/.beads/issues.jsonl" >/dev/null ||
    fail "server-to-embedded JSONL export omitted the historical comment body"
[ "$(sha256_file "$server_export_ws/.beads/legacy-dolt.pre-migration/issues.jsonl")" = \
    "$server_export_original_jsonl" ] ||
    fail "server-to-embedded recipe did not retain the original passive JSONL"

server_export_fail_ws="$tmp/server-export-failure-workspace"
server_export_fail_old_bin="$tmp/fake-failing-v0.55.4-bd"
server_export_fail_candidate_bin="$tmp/fake-unexpected-candidate-bd"
server_export_fail_candidate_calls="$tmp/server-export-failure-candidate-calls"
mkdir -p "$server_export_fail_ws/.beads/dolt"
printf 'active legacy Dolt data' > "$server_export_fail_ws/.beads/dolt/table.dat"
printf 'active legacy metadata' > "$server_export_fail_ws/.beads/metadata.json"
server_export_fail_manifest=$(legacy_dolt_artifact_manifest "$server_export_fail_ws/.beads")
cat > "$server_export_fail_old_bin" <<'EOF'
#!/bin/bash
[ "$1" = "export" ] || exit 2
exit 1
EOF
cat > "$server_export_fail_candidate_bin" <<'EOF'
#!/bin/bash
printf '%s\n' "$*" >> "$SERVER_EXPORT_FAIL_CANDIDATE_CALLS"
exit 1
EOF
chmod +x "$server_export_fail_old_bin" "$server_export_fail_candidate_bin"
export SERVER_EXPORT_FAIL_CANDIDATE_CALLS="$server_export_fail_candidate_calls"
: > "$server_export_fail_candidate_calls"
if (
    stop_dolt_server() { :; }
    recipe_server_to_embedded \
        "$server_export_fail_ws" "$server_export_fail_old_bin" \
        "$server_export_fail_candidate_bin" v0.55.4 "$server_export_before"
) >/dev/null; then
    fail "server-to-embedded recipe accepted a failed historical export"
fi
server_export_fail_after=$(legacy_dolt_artifact_manifest "$server_export_fail_ws/.beads") ||
    fail "failed historical export removed the active legacy source"
[ "$server_export_fail_after" = "$server_export_fail_manifest" ] ||
    fail "failed historical export mutated the active legacy source"
[ ! -s "$server_export_fail_candidate_calls" ] ||
    fail "failed historical export still invoked the candidate"
verify_retained_legacy_dolt_source \
    "$server_export_fail_ws/.beads" "$server_export_fail_manifest" ||
    fail "failed historical export corrupted the retained rollback source"

server_export_malformed_ws="$tmp/server-export-malformed-workspace"
server_export_malformed_old_bin="$tmp/fake-malformed-v0.55.4-bd"
mkdir -p "$server_export_malformed_ws/.beads/dolt"
printf 'active malformed-test data' > "$server_export_malformed_ws/.beads/dolt/table.dat"
printf 'active malformed-test metadata' > "$server_export_malformed_ws/.beads/metadata.json"
server_export_malformed_manifest=$(legacy_dolt_artifact_manifest "$server_export_malformed_ws/.beads")
cat > "$server_export_malformed_old_bin" <<'EOF'
#!/bin/bash
[ "$1" = "export" ] && [ "$4" = "-o" ] && [ -n "$5" ] || exit 2
printf '%s\n' 'not JSONL' > "$5"
EOF
chmod +x "$server_export_malformed_old_bin"
: > "$server_export_fail_candidate_calls"
if (
    stop_dolt_server() { :; }
    recipe_server_to_embedded \
        "$server_export_malformed_ws" "$server_export_malformed_old_bin" \
        "$server_export_fail_candidate_bin" v0.55.4 "$server_export_before"
) >/dev/null; then
    fail "server-to-embedded recipe accepted malformed historical JSONL"
fi
[ ! -s "$server_export_fail_candidate_calls" ] ||
    fail "malformed historical export still invoked the candidate"
[ "$(legacy_dolt_artifact_manifest "$server_export_malformed_ws/.beads")" = \
    "$server_export_malformed_manifest" ] ||
    fail "malformed historical export mutated the active legacy source"
[ ! -e "$server_export_malformed_ws/.beads/issues.jsonl" ] ||
    fail "malformed historical export was published"
if find "$server_export_malformed_ws/.beads" -maxdepth 1 \
    -name '.issues.jsonl.migration.tmp.*' -print -quit | grep -q .; then
    fail "malformed historical export left a temporary file"
fi

server_export_symlink_ws="$tmp/server-export-symlink-workspace"
server_export_symlink_target="$tmp/server-export-symlink-target"
mkdir -p "$server_export_symlink_ws/.beads/dolt"
printf 'active symlink-test data' > "$server_export_symlink_ws/.beads/dolt/table.dat"
printf 'external sentinel' > "$server_export_symlink_target"
ln -s "$server_export_symlink_target" "$server_export_symlink_ws/.beads/issues.jsonl"
if recipe_server_to_embedded \
    "$server_export_symlink_ws" "$server_export_fail_old_bin" \
    "$server_export_fail_candidate_bin" v0.55.4 "$server_export_before" \
    >/dev/null 2>&1; then
    fail "server-to-embedded recipe accepted a symlinked JSONL destination"
fi
[ "$(cat "$server_export_symlink_target")" = "external sentinel" ] ||
    fail "server-to-embedded recipe followed a symlinked JSONL destination"
[ ! -e "$server_export_symlink_ws/.beads/legacy-dolt.pre-migration" ] ||
    fail "unsafe JSONL destination was detected only after rollback mutation"

rollback_corruption_ws="$tmp/rollback-corruption-workspace"
rollback_corruption_old_bin="$tmp/fake-rollback-corrupting-v0.55.4-bd"
rollback_corruption_candidate_calls="$tmp/rollback-corruption-candidate.calls"
mkdir -p "$rollback_corruption_ws/.beads/dolt"
printf 'active corruption-test data' > "$rollback_corruption_ws/.beads/dolt/table.dat"
printf 'active corruption-test metadata' > "$rollback_corruption_ws/.beads/metadata.json"
printf 'active corruption-test config' > "$rollback_corruption_ws/.beads/config.yaml"
cat > "$rollback_corruption_old_bin" <<'EOF'
#!/bin/bash
[ "$1" = "export" ] && [ "$4" = "-o" ] && [ -n "$5" ] || exit 2
printf '%s\n' '{"id":"old-task","title":"Implement core feature"}' > "$5"
printf 'corrupted during export' >> .beads/legacy-dolt.pre-migration/metadata.json
EOF
chmod +x "$rollback_corruption_old_bin"
export SERVER_EXPORT_FAIL_CANDIDATE_CALLS="$rollback_corruption_candidate_calls"
: > "$rollback_corruption_candidate_calls"
if (
    stop_dolt_server() { :; }
    recipe_server_to_embedded \
        "$rollback_corruption_ws" "$rollback_corruption_old_bin" \
        "$server_export_fail_candidate_bin" v0.55.4 "$server_export_before"
) >/dev/null 2>&1; then
    fail "server-to-embedded recipe accepted rollback corruption during export"
fi
[ ! -s "$rollback_corruption_candidate_calls" ] ||
    fail "rollback corruption during export still invoked the candidate"
[ "$(cat "$rollback_corruption_ws/.beads/metadata.json")" = \
    "active corruption-test metadata" ] ||
    fail "rollback corruption caused active metadata deletion"
[ "$(cat "$rollback_corruption_ws/.beads/config.yaml")" = \
    "active corruption-test config" ] ||
    fail "rollback corruption changed active config"
[ "$(cat "$rollback_corruption_ws/.beads/dolt/table.dat")" = \
    "active corruption-test data" ] ||
    fail "rollback corruption changed active Dolt data"

metadata_removal_ws="$tmp/metadata-removal-failure-workspace"
metadata_removal_old_calls="$tmp/metadata-removal-old.calls"
metadata_removal_candidate_calls="$tmp/metadata-removal-candidate.calls"
mkdir -p "$metadata_removal_ws/.beads/dolt"
printf 'active removal-test data' > "$metadata_removal_ws/.beads/dolt/table.dat"
printf 'active removal-test metadata' > "$metadata_removal_ws/.beads/metadata.json"
export SERVER_EXPORT_CALLS="$metadata_removal_old_calls"
export SERVER_EXPORT_FAIL_CANDIDATE_CALLS="$metadata_removal_candidate_calls"
: > "$metadata_removal_old_calls"
: > "$metadata_removal_candidate_calls"
if (
    stop_dolt_server() { :; }
    rm() {
        local arg
        for arg in "$@"; do
            if [[ "$arg" == */metadata.json ]]; then
                return 1
            fi
        done
        command rm "$@"
    }
    recipe_server_to_embedded \
        "$metadata_removal_ws" "$server_export_old_bin" \
        "$server_export_fail_candidate_bin" v0.55.4 "$server_export_before"
) >/dev/null; then
    fail "server-to-embedded recipe ignored active metadata removal failure"
fi
[ ! -s "$metadata_removal_candidate_calls" ] ||
    fail "active metadata removal failure still invoked the candidate"
[ "$(cat "$metadata_removal_ws/.beads/metadata.json")" = \
    "active removal-test metadata" ] ||
    fail "active metadata removal failure changed metadata"
[ "$(cat "$metadata_removal_ws/.beads/dolt/table.dat")" = \
    "active removal-test data" ] ||
    fail "active metadata removal failure changed Dolt data"

probe_list_ws="$tmp/probe-list-workspace"
probe_list_bin="$tmp/fake-probe-list-bd"
mkdir -p "$probe_list_ws"
cat > "$probe_list_bin" <<'EOF'
#!/bin/bash
[ "$1" = "list" ] || exit 2
printf '%s' "$PROBE_LIST_OUTPUT"
exit "$PROBE_LIST_EXIT"
EOF
chmod +x "$probe_list_bin"

assert_probe_list_rejected() {
    local name="$1"
    local output="$2"
    local exit_code="$3"
    export PROBE_LIST_OUTPUT="$output"
    export PROBE_LIST_EXIT="$exit_code"
    if candidate_list_has_nonempty_issue_ids "$probe_list_ws" "$probe_list_bin"; then
        fail "candidate list probe accepted $name"
    fi
}

assert_probe_list_rejected "invalid output" 'not JSON' 0
assert_probe_list_rejected "valid JSON from a nonzero command" '[{"id":"old-1"}]' 1
assert_probe_list_rejected "an empty array" '[]' 0
assert_probe_list_rejected "a JSON object" '{"id":"old-1"}' 0
assert_probe_list_rejected "an empty issue id" '[{"id":""}]' 0
assert_probe_list_rejected "a non-string issue id" '[{"id":7}]' 0
assert_probe_list_rejected "multiple JSON documents" $'[{"id":"old-1"}]\n[{"id":"old-2"}]' 0
export PROBE_LIST_OUTPUT='[{"id":"old-1"},{"id":"old-2"}]'
export PROBE_LIST_EXIT=0
candidate_list_has_nonempty_issue_ids "$probe_list_ws" "$probe_list_bin" ||
    fail "candidate list probe rejected a valid nonempty issue array"

probe_flow_bin="$tmp/fake-probe-flow-bd"
cat > "$probe_flow_bin" <<'EOF'
#!/bin/bash
printf '%s\n' "$1" >> "$PROBE_FLOW_LOG"
case "$PROBE_FLOW_MODE:$1" in
    first-list-mutation:list)
        printf 'mutated\n' >> .beads/source-marker
        exit 1
        ;;
    init-mutation:list|safe-failure:list)
        exit 1
        ;;
    init-mutation:init)
        printf 'mutated\n' >> .beads/source-marker
        exit 1
        ;;
    safe-failure:init)
        exit 1
        ;;
    second-list-mutation:list|retry-success:list)
        count=0
        [ ! -f "$PROBE_FLOW_STATE" ] || count=$(cat "$PROBE_FLOW_STATE")
        count=$((count + 1))
        printf '%s\n' "$count" > "$PROBE_FLOW_STATE"
        if [ "$count" -eq 1 ]; then
            exit 1
        fi
        if [ "$PROBE_FLOW_MODE" = "second-list-mutation" ]; then
            printf 'mutated\n' >> .beads/source-marker
            printf '%s\n' '[]'
        else
            printf '%s\n' '[{"id":"old-1"}]'
        fi
        ;;
    second-list-mutation:init|retry-success:init)
        exit 0
        ;;
    *)
        exit 2
        ;;
esac
EOF
chmod +x "$probe_flow_bin"

assert_probe_flow() {
    local mode="$1"
    local expected_status="$2"
    local expected_calls="$3"
    local ws="$tmp/probe-flow-$mode"
    local status=0
    local actual_calls
    local source_fingerprint

    mkdir -p "$ws/.beads"
    printf 'original\n' > "$ws/.beads/source-marker"
    export PROBE_FLOW_MODE="$mode"
    export PROBE_FLOW_LOG="$tmp/probe-flow-$mode.calls"
    export PROBE_FLOW_STATE="$tmp/probe-flow-$mode.state"
    : > "$PROBE_FLOW_LOG"
    rm -f "$PROBE_FLOW_STATE"
    source_fingerprint=$(source_artifact_fingerprint "$ws/.beads") ||
        fail "could not fingerprint fake probe source for $mode"

    if (
        stop_dolt_server() { :; }
        probe_candidate_direct_upgrade "$ws" "$probe_flow_bin" \
            "$source_fingerprint" true
    ); then
        status=0
    else
        status=$?
    fi

    [ "$status" -eq "$expected_status" ] ||
        fail "candidate probe $mode returned $status, want $expected_status"
    actual_calls=$(paste -sd, "$PROBE_FLOW_LOG")
    [ "$actual_calls" = "$expected_calls" ] ||
        fail "candidate probe $mode called $actual_calls, want $expected_calls"
}

assert_probe_flow first-list-mutation 2 list
assert_probe_flow init-mutation 2 list,init
assert_probe_flow second-list-mutation 2 list,init,list
assert_probe_flow safe-failure 1 list,init
assert_probe_flow retry-success 0 list,init,list

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

#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BRIDGE="$REPO_ROOT/scripts/migrate-v062-server-to-current.sh"

fail() {
    printf 'public-v062-bridge-test: %s\n' "$*" >&2
    exit 1
}

[ -x "$BRIDGE" ] || fail "missing executable public bridge: $BRIDGE"
command -v jq >/dev/null || fail "jq is required"
command -v script >/dev/null || fail "script is required"

if [ -n "${PUBLIC_V062_REAL_TARGET_BD:-}" ]; then
    REAL_TARGET_BD=$(realpath -e -- "$PUBLIC_V062_REAL_TARGET_BD") ||
        fail "PUBLIC_V062_REAL_TARGET_BD cannot be resolved"
else
    (cd "$REPO_ROOT" && make build >/dev/null) ||
        fail "could not build the real current bd target"
    REAL_TARGET_BD="$REPO_ROOT/bd"
fi
[ -f "$REAL_TARGET_BD" ] && [ -x "$REAL_TARGET_BD" ] ||
    fail "real current bd target is not executable: $REAL_TARGET_BD"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/bd-public-v062-bridge.XXXXXX")
trap 'rm -rf -- "$tmp"' EXIT
mkdir -p "$tmp/home"

TARGET_BD="$tmp/fake-bd-current"
TARGET_LOG="$tmp/fake-target.log"
OLD_TARGET_BD="$tmp/fake-bd-old-target"
OLD_TARGET_LOG="$tmp/fake-old-target.log"
INCAPABLE_TARGET_BD="$tmp/fake-bd-incapable-target"
INCAPABLE_TARGET_LOG="$tmp/fake-incapable-target.log"
TIMEOUT_TARGET_BD="$tmp/fake-bd-timeout-target"
TIMEOUT_TARGET_LOG="$tmp/fake-timeout-target.log"

make_fake_bd() {
    local path="$1" version="$2" capability="$3" log="$4"
    cat > "$path" <<EOF
#!/usr/bin/env bash
{
    printf 'argv=%q' "\$0"
    printf ' %q' "\$@"
    printf '\n'
    env | LC_ALL=C sort
} >> '$log'
case "\${1:-}" in
    version)
        [ "\${2:-}" = --json ] || exit 2
        printf '{"version":"%s","build":"test"}\n' '$version'
        ;;
    __migration-v062-inspect)
        if [ '$capability' = timeout ]; then
            printf '%s\n' "\$\$" > '${log}.pid'
            exec /usr/bin/sleep 30
        fi
        [ '$capability' = embedded ] || exit 2
        [ "\$#" -eq 4 ] && [ "\$2" = --workspace ] &&
            [[ "\$3" = /* ]] && [ "\$4" = --json ] || exit 2
        inspection_code=
        inspection_retryable=false
        inspection_exit=1
        qualified_workspace="\$3"
        qualified_digest_scope=admission_observation
        qualified_exit=0
        repeat_qualified=false
        case "\${3##*/}" in
            wrong-witness) inspection_code=source_version_mismatch ;;
            missing-witness) inspection_code=source_version_missing ;;
            ambiguous-witness) inspection_code=source_version_ambiguous ;;
            wrong-metadata) inspection_code=source_metadata_mismatch ;;
            symlink-layout) inspection_code=unsafe_source_symlink ;;
            mixed-target) inspection_code=mixed_storage_layout ;;
            rollback-collision) inspection_code=rollback_collision ;;
            lying-workspace)
                qualified_workspace=/not/the/requested/workspace
                ;;
            wrong-digest-scope) qualified_digest_scope=lifetime_authority ;;
            multiple-json) repeat_qualified=true ;;
            qualified-wrong-exit) qualified_exit=1 ;;
            refused-wrong-exit)
                inspection_code=source_version_mismatch
                inspection_exit=0
                ;;
            unknown-refusal-code) inspection_code=unrecognized_private_code ;;
            wrong-retryability-stable)
                inspection_code=source_version_mismatch
                inspection_retryable=true
                ;;
            wrong-retryability-transient)
                inspection_code=source_changed
                inspection_retryable=false
                ;;
            retryable-source-changed)
                inspection_code=source_changed
                inspection_retryable=true
                ;;
        esac
        if [ -n "\$inspection_code" ]; then
            printf '{"schema_version":1,"operation":"v062_source_inspection","status":"refused","retryable":%s,"effect":"none","code":"%s"}\n' "\$inspection_retryable" "\$inspection_code"
            exit "\$inspection_exit"
        fi
        printf '{"schema_version":1,"operation":"v062_source_inspection","status":"qualified","retryable":false,"effect":"none","source":{"workspace":"%s","version":"0.62.0","backend":"dolt-server","tree_sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","digest_scope":"%s"},"target":{"version":"%s","backend":"dolt-embedded","embedded_capable":true}}\n' "\$qualified_workspace" "\$qualified_digest_scope" '$version'
        if \$repeat_qualified; then
            printf '{"schema_version":1,"operation":"v062_source_inspection","status":"qualified","retryable":false,"effect":"none","source":{"workspace":"%s","version":"0.62.0","backend":"dolt-server","tree_sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","digest_scope":"%s"},"target":{"version":"%s","backend":"dolt-embedded","embedded_capable":true}}\n' "\$qualified_workspace" "\$qualified_digest_scope" '$version'
        fi
        exit "\$qualified_exit"
        ;;
    *) exit 2 ;;
esac
EOF
    chmod +x "$path"
}

make_fake_bd "$TARGET_BD" 1.1.0 embedded "$TARGET_LOG"
make_fake_bd "$OLD_TARGET_BD" 0.62.0 embedded "$OLD_TARGET_LOG"
make_fake_bd "$INCAPABLE_TARGET_BD" 1.1.0 unavailable "$INCAPABLE_TARGET_LOG"
make_fake_bd "$TIMEOUT_TARGET_BD" 1.1.0 timeout "$TIMEOUT_TARGET_LOG"

new_v062_workspace() {
    local ws="$1"
    mkdir -p "$ws"
    GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null \
        git -C "$ws" init --quiet
    git -C "$ws" config core.hooksPath .git/hooks
    mkdir -p \
        "$ws/.beads/dolt/.dolt" \
        "$ws/.beads/dolt/smoke/.dolt"
    cat > "$ws/.beads/metadata.json" <<'EOF'
{
  "database": "dolt",
  "backend": "dolt",
  "dolt_mode": "server",
  "dolt_server_host": "127.0.0.1",
  "dolt_server_port": 3307,
  "dolt_database": "smoke",
  "project_id": "7ef372b4-4c3c-4e2c-a6cc-29dd2d0a28c6"
}
EOF
    printf '0.62.0\n' > "$ws/.beads/.local_version"
    printf '{}\n' > "$ws/.beads/dolt/.dolt/config.json"
    printf '{"head":"refs/heads/main"}\n' > "$ws/.beads/dolt/.dolt/repo_state.json"
    printf '{}\n' > "$ws/.beads/dolt/smoke/.dolt/config.json"
    printf '{"head":"refs/heads/main"}\n' > "$ws/.beads/dolt/smoke/.dolt/repo_state.json"
    printf 'listener:\n  host: 127.0.0.1\n  port: 3307\n' \
        > "$ws/.beads/dolt/config.yaml"
}

tree_fingerprint() {
    local root="$1"
    (
        cd "$root"
        find -P . -mindepth 1 \
            -printf '%y %m %D %i %n %u %g %s %T@ %C@ %p %l\n' |
            LC_ALL=C sort
        find -P . -type f -print0 | LC_ALL=C sort -z | xargs -0 -r sha256sum
    ) | sha256sum | awk '{print $1}'
}

RUN_STATUS=0
RUN_STDOUT=""
RUN_STDERR=""
run_bridge() {
    local label="$1" cwd="$2"
    shift 2
    local out="$tmp/$label.stdout" err="$tmp/$label.stderr"
    local target="${BRIDGE_TEST_TARGET:-$TARGET_BD}"
    set +e
    (
        cd "$cwd"
        env -i \
            "PATH=$PATH" "HOME=$tmp/home" "TMPDIR=$tmp" \
            LANG=C LC_ALL=C TZ=UTC TERM=dumb \
            BD_BACKEND=postgres BD_DATABASE_BACKEND=mysql \
            BEADS_DB='postgres://poison.invalid/beads' \
            BD_DB='mysql://poison.invalid/beads' \
            BEADS_DIR="$tmp/poison-beads" \
            BEADS_SQLITE_PATH="$tmp/poison.sqlite" \
            BEADS_DOLT_SERVER_MODE=1 BEADS_DOLT_SHARED_SERVER=1 \
            BEADS_DOLT_DATA_DIR="$tmp/poison-dolt" \
            BEADS_DOLT_SERVER_HOST=poison.invalid \
            BEADS_DOLT_SERVER_PORT=29999 \
            "BD_V062_INSPECT_TIMEOUT_SECONDS=${BRIDGE_TEST_INSPECT_TIMEOUT_SECONDS:-600}" \
            "$BRIDGE" --target-bd "$target" --json "$@" \
            </dev/null >"$out" 2>"$err"
    )
    RUN_STATUS=$?
    set -e
    RUN_STDOUT=$(<"$out")
    RUN_STDERR=$(<"$err")
}

run_apply_without_yes_on_tty() {
    local ws="$1" command
    printf -v command '%q ' env -i "PATH=$PATH" "HOME=$tmp/home" \
        LANG=C LC_ALL=C TZ=UTC TERM=dumb "$BRIDGE" \
        --target-bd "$TARGET_BD" --apply --workspace "$ws" --json
    set +e
    (cd "$ws" && script -q -e -c "$command" /dev/null >/dev/null 2>&1)
    RUN_STATUS=$?
    set -e
}

assert_common_json() {
    local mode="$1" status="$2" effect="$3"
    jq -se --arg mode "$mode" --arg status "$status" --arg effect "$effect" '
        length == 1 and (.[0] |
            type == "object" and
            .schema_version == 1 and
            .operation == "v062_server_to_current" and
            .mode == $mode and .status == $status and
            (.retryable | type) == "boolean" and .effect == $effect and
            (keys - ["schema_version", "operation", "mode", "status",
                     "retryable", "effect", "code", "source", "target",
                     "rollback", "verification"] | length) == 0
        )
    ' <<< "$RUN_STDOUT" >/dev/null ||
        fail "invalid JSON contract for $mode/$status: $RUN_STDOUT $RUN_STDERR"
}

assert_unchanged() {
    local ws="$1" before="$2" context="$3"
    [ "$(tree_fingerprint "$ws")" = "$before" ] ||
        fail "$context mutated the workspace"
}

# Default mode is inspect. It can report the target-qualified source plan but
# must not claim the bridge is ready until the public apply path exists.
inspect_ws="$tmp/inspect-default"
new_v062_workspace "$inspect_ws"
inspect_before=$(tree_fingerprint "$inspect_ws")
run_bridge inspect-default "$inspect_ws"
[ "$RUN_STATUS" -eq 1 ] || fail "default inspect exit=$RUN_STATUS, want 1"
assert_common_json inspect failed none
jq -e '
    .retryable == false and .code == "apply_not_available" and
    .source.version == "0.62.0" and .source.backend == "dolt-server" and
    .source.digest_scope == "admission_observation" and
    (.source.tree_sha256 | test("^[0-9a-f]{64}$")) and
    .target.backend == "dolt-embedded" and .target.embedded_capable == true and
    (.rollback | type) == "object" and
    .verification.source_digest_scope == "admission_observation" and
    .verification.apply_reinspection_required == true and
    .verification.apply_available == false and
    .verification.workspace_effects == false
' <<< "$RUN_STDOUT" >/dev/null || fail "default inspect omitted its qualified plan"
default_json=$(jq -Sc . <<< "$RUN_STDOUT")
assert_unchanged "$inspect_ws" "$inspect_before" "default inspect"

run_bridge inspect-explicit "$inspect_ws" --inspect --workspace "$inspect_ws"
[ "$RUN_STATUS" -eq 1 ] || fail "explicit inspect exit=$RUN_STATUS, want 1"
assert_common_json inspect failed none
[ "$(jq -Sc . <<< "$RUN_STDOUT")" = "$default_json" ] ||
    fail "default and explicit inspect returned different JSON contracts"
assert_unchanged "$inspect_ws" "$inspect_before" "explicit inspect"

# Provider and database selectors inherited from the caller may not reach the
# target binary, which owns both version and hidden migration inspection.
[ -s "$TARGET_LOG" ] || fail "inspect did not validate the target binary"
grep -F -- '__migration-v062-inspect' "$TARGET_LOG" >/dev/null ||
    fail "public inspect did not use the target hidden inspection command"
grep -F -- "--workspace $inspect_ws --json" "$TARGET_LOG" >/dev/null ||
    fail "target hidden inspection did not receive the physical workspace"
for poison in \
    'BD_BACKEND=postgres' 'BD_DATABASE_BACKEND=mysql' \
    'postgres://poison.invalid/beads' 'mysql://poison.invalid/beads' \
    "$tmp/poison-beads" "$tmp/poison.sqlite" "$tmp/poison-dolt" \
    'BEADS_DOLT_SERVER_MODE=1' 'BEADS_DOLT_SHARED_SERVER=1' \
    'poison.invalid' 'BEADS_DOLT_SERVER_PORT=29999'; do
    if grep -F -- "$poison" "$TARGET_LOG" >/dev/null; then
        fail "ambient provider selector reached the target binary: $poison"
    fi
done

# Treat the hidden command as an untrusted protocol peer. A response is
# admissible only when its payload and process exit form one exact tuple.
protocol_rejection_failures=()
check_protocol_rejected() {
    local label="$1" ws="$tmp/$1" before
    new_v062_workspace "$ws"
    before=$(tree_fingerprint "$ws")
    run_bridge "$label" "$ws" --inspect --workspace "$ws"
    if [ "$RUN_STATUS" -ne 1 ]; then
        printf '%s: exit=%s, want 1\n' "$label" "$RUN_STATUS" >&2
        return 1
    fi
    if ! jq -se '
        length == 1 and .[0] == {
            schema_version: 1,
            operation: "v062_server_to_current",
            mode: "inspect",
            status: "refused",
            retryable: false,
            effect: "none",
            code: "target_capability_missing"
        }
    ' <<< "$RUN_STDOUT" >/dev/null 2>&1; then
        printf '%s: wrapper trusted invalid inspector tuple: %s %s\n' \
            "$label" "$RUN_STDOUT" "$RUN_STDERR" >&2
        return 1
    fi
    if [ "$(tree_fingerprint "$ws")" != "$before" ]; then
        printf '%s: protocol rejection mutated the workspace\n' "$label" >&2
        return 1
    fi
}

for protocol_case in \
    lying-workspace \
    wrong-digest-scope \
    multiple-json \
    qualified-wrong-exit \
    refused-wrong-exit \
    unknown-refusal-code \
    wrong-retryability-stable \
    wrong-retryability-transient; do
    if ! check_protocol_rejected "$protocol_case"; then
        protocol_rejection_failures+=("$protocol_case")
    fi
done
if [ "${#protocol_rejection_failures[@]}" -ne 0 ]; then
    fail "inspector protocol regressions: ${protocol_rejection_failures[*]}"
fi

assert_refused() {
    local label="$1" ws="$2" code="$3"
    local before
    before=$(tree_fingerprint "$ws")
    run_bridge "$label" "$ws" --inspect --workspace "$ws"
    [ "$RUN_STATUS" -eq 1 ] || fail "$label exit=$RUN_STATUS, want refusal exit 1"
    assert_common_json inspect refused none
    jq -e --arg code "$code" '.retryable == false and .code == $code' \
        <<< "$RUN_STDOUT" >/dev/null || fail "$label returned the wrong refusal code"
    assert_unchanged "$ws" "$before" "$label refusal"
}

wrong_ws="$tmp/wrong-witness"; new_v062_workspace "$wrong_ws"
printf '0.61.0\n' > "$wrong_ws/.beads/.local_version"
assert_refused wrong-witness "$wrong_ws" source_version_mismatch

missing_ws="$tmp/missing-witness"; new_v062_workspace "$missing_ws"
rm -f -- "$missing_ws/.beads/.local_version"
assert_refused missing-witness "$missing_ws" source_version_missing

ambiguous_ws="$tmp/ambiguous-witness"; new_v062_workspace "$ambiguous_ws"
mv -f -- "$ambiguous_ws/.beads/.local_version" "$ambiguous_ws/version-target"
ln -s ../version-target "$ambiguous_ws/.beads/.local_version"
assert_refused ambiguous-witness "$ambiguous_ws" source_version_ambiguous

metadata_ws="$tmp/wrong-metadata"; new_v062_workspace "$metadata_ws"
jq '.backend = "postgres"' "$metadata_ws/.beads/metadata.json" \
    > "$metadata_ws/.beads/metadata.json.tmp"
mv -f -- "$metadata_ws/.beads/metadata.json.tmp" "$metadata_ws/.beads/metadata.json"
assert_refused wrong-metadata "$metadata_ws" source_metadata_mismatch

symlink_ws="$tmp/symlink-layout"; new_v062_workspace "$symlink_ws"
mv -f -- "$symlink_ws/.beads/dolt" "$symlink_ws/.beads/dolt-real"
ln -s dolt-real "$symlink_ws/.beads/dolt"
assert_refused symlink-layout "$symlink_ws" unsafe_source_symlink

mixed_ws="$tmp/mixed-target"; new_v062_workspace "$mixed_ws"
mkdir -p "$mixed_ws/.beads/embeddeddolt"
assert_refused mixed-target "$mixed_ws" mixed_storage_layout

collision_ws="$tmp/rollback-collision"; new_v062_workspace "$collision_ws"
mkdir -p "$collision_ws/.beads-v0.62.0-rollback"
printf 'unrelated retained data\n' > "$collision_ws/.beads-v0.62.0-rollback/sentinel"
assert_refused rollback-collision "$collision_ws" rollback_collision

changed_ws="$tmp/retryable-source-changed"; new_v062_workspace "$changed_ws"
changed_before=$(tree_fingerprint "$changed_ws")
run_bridge retryable-source-changed "$changed_ws" \
    --inspect --workspace "$changed_ws"
[ "$RUN_STATUS" -eq 1 ] ||
    fail "retryable source change exit=$RUN_STATUS, want refusal exit 1"
assert_common_json inspect refused none
jq -e '
    .retryable == true and .code == "source_changed"
' <<< "$RUN_STDOUT" >/dev/null ||
    fail "valid retryable source change was not forwarded unchanged"
assert_unchanged "$changed_ws" "$changed_before" \
    "retryable source change refusal"

assert_target_refused() {
    local label="$1" target="$2" code="$3" ws="$tmp/$1"
    local before
    new_v062_workspace "$ws"
    before=$(tree_fingerprint "$ws")
    BRIDGE_TEST_TARGET="$target" run_bridge "$label" "$ws" --inspect --workspace "$ws"
    [ "$RUN_STATUS" -eq 1 ] || fail "$label exit=$RUN_STATUS, want refusal exit 1"
    assert_common_json inspect refused none
    jq -e --arg code "$code" '.retryable == false and .code == $code' \
        <<< "$RUN_STDOUT" >/dev/null || fail "$label returned the wrong refusal code"
    assert_unchanged "$ws" "$before" "$label refusal"
}

assert_target_refused old-target "$OLD_TARGET_BD" target_binary_invalid
grep -F -- '__migration-v062-inspect' "$OLD_TARGET_LOG" >/dev/null ||
    fail "historical target was rejected without validating its hidden response"
assert_target_refused incapable-target "$INCAPABLE_TARGET_BD" target_capability_missing
grep -F -- '__migration-v062-inspect' "$INCAPABLE_TARGET_LOG" >/dev/null ||
    fail "embedded-incapable target was rejected without a capability probe"

timeout_ws="$tmp/timeout-target"
new_v062_workspace "$timeout_ws"
timeout_before=$(tree_fingerprint "$timeout_ws")
timeout_started=$SECONDS
BRIDGE_TEST_INSPECT_TIMEOUT_SECONDS=1 \
BRIDGE_TEST_TARGET="$TIMEOUT_TARGET_BD" \
    run_bridge timeout-target "$timeout_ws" --inspect --workspace "$timeout_ws"
timeout_elapsed=$((SECONDS - timeout_started))
[ "$RUN_STATUS" -eq 1 ] ||
    fail "timeout target exit=$RUN_STATUS, want refusal exit 1"
[ "$timeout_elapsed" -le 5 ] ||
    fail "timeout target exceeded its wall-clock bound (${timeout_elapsed}s)"
assert_common_json inspect refused none
jq -e '
    .retryable == true and .code == "target_inspection_timeout"
' <<< "$RUN_STDOUT" >/dev/null ||
    fail "target timeout was not reported as a retryable refusal"
[ -s "${TIMEOUT_TARGET_LOG}.pid" ] ||
    fail "timeout target did not record its process identity"
timeout_target_pid=$(<"${TIMEOUT_TARGET_LOG}.pid")
if kill -0 "$timeout_target_pid" 2>/dev/null; then
    fail "timed-out target process $timeout_target_pid leaked"
fi
assert_unchanged "$timeout_ws" "$timeout_before" "target timeout refusal"

# Apply always requires explicit consent, even on a terminal, and every
# currently admitted apply remains truthfully unavailable and non-mutating.
apply_ws="$tmp/apply-without-consent"; new_v062_workspace "$apply_ws"
apply_before=$(tree_fingerprint "$apply_ws")
run_bridge apply-without-consent "$apply_ws" --apply --workspace "$apply_ws"
[ "$RUN_STATUS" -eq 2 ] || fail "apply without --yes exit=$RUN_STATUS, want 2"
assert_common_json apply usage_error none
jq -e '.retryable == false and .code == "confirmation_required"' \
    <<< "$RUN_STDOUT" >/dev/null || fail "apply without --yes lacked stable JSON"
assert_unchanged "$apply_ws" "$apply_before" "apply without --yes"

run_apply_without_yes_on_tty "$apply_ws"
[ "$RUN_STATUS" -eq 2 ] || fail "TTY apply without --yes exit=$RUN_STATUS, want 2"
assert_unchanged "$apply_ws" "$apply_before" "TTY apply without --yes"

run_bridge apply-unavailable "$apply_ws" --apply --yes --workspace "$apply_ws"
[ "$RUN_STATUS" -eq 1 ] || fail "apply --yes exit=$RUN_STATUS, want 1"
assert_common_json apply failed none
jq -e '.retryable == false and .code == "apply_not_available"' \
    <<< "$RUN_STDOUT" >/dev/null || fail "apply --yes overstated bridge availability"
jq -e '
    .verification.source_digest_scope == "admission_observation" and
    .verification.apply_reinspection_required == true
' <<< "$RUN_STDOUT" >/dev/null ||
    fail "apply --yes omitted its mandatory source reinspection contract"
assert_unchanged "$apply_ws" "$apply_before" "unavailable apply"

# JSON is bootstrapped before ordinary parsing: even when --json follows an
# invalid flag, the result is one structured usage error and no effects.
run_bridge invalid-usage "$inspect_ws" --definitely-not-a-real-option
[ "$RUN_STATUS" -eq 2 ] || fail "invalid usage exit=$RUN_STATUS, want 2"
assert_common_json inspect usage_error none
jq -e '.retryable == false and .code == "invalid_usage"' \
    <<< "$RUN_STDOUT" >/dev/null || fail "invalid usage lacked stable JSON"
assert_unchanged "$inspect_ws" "$inspect_before" "invalid usage"

assert_usage_error() {
    local label="$1" code="$2"
    shift 2
    run_bridge "$label" "$inspect_ws" "$@"
    [ "$RUN_STATUS" -eq 2 ] ||
        fail "$label exit=$RUN_STATUS, want usage exit 2"
    assert_common_json inspect usage_error none
    jq -e --arg code "$code" '.retryable == false and .code == $code' \
        <<< "$RUN_STDOUT" >/dev/null ||
        fail "$label lacked its stable usage code"
    assert_unchanged "$inspect_ws" "$inspect_before" "$label"
}

assert_usage_error conflicting-modes invalid_usage --inspect --apply
assert_usage_error inspect-with-yes invalid_usage --inspect --yes
assert_usage_error workspace-missing-value invalid_usage --workspace
assert_usage_error target-missing-value invalid_usage --target-bd

for invalid_timeout in \
    0 \
    3601 \
    999999999999999999999999999999999999999999999999999999999999 \
    not-a-number; do
    BRIDGE_TEST_INSPECT_TIMEOUT_SECONDS="$invalid_timeout" \
        run_bridge "invalid-timeout-$invalid_timeout" "$inspect_ws" --inspect
    [ "$RUN_STATUS" -eq 2 ] ||
        fail "invalid timeout $invalid_timeout exit=$RUN_STATUS, want 2"
    assert_common_json inspect usage_error none
    jq -e '
        .retryable == false and .code == "invalid_environment"
    ' <<< "$RUN_STDOUT" >/dev/null ||
        fail "invalid timeout $invalid_timeout lacked its stable usage code"
    assert_unchanged "$inspect_ws" "$inspect_before" \
        "invalid timeout $invalid_timeout"
done

# Close the test-double gap: exercise the actual built current bd through the
# public shell entrypoint, hidden Cobra protocol, and descriptor-relative Go
# inspector. This is the admission slice's real process-boundary E2E path.
real_ws="$tmp/real-current-target"
new_v062_workspace "$real_ws"
# Fresh v0.62.0 workspaces using the default server endpoint omit these
# optional metadata fields; exercise that authentic shape through the real
# current binary rather than only the explicitly configured CI fixture.
jq 'del(.dolt_server_host, .dolt_server_port)' \
    "$real_ws/.beads/metadata.json" > "$real_ws/.beads/metadata.json.tmp"
mv -f -- "$real_ws/.beads/metadata.json.tmp" \
    "$real_ws/.beads/metadata.json"
real_before=$(tree_fingerprint "$real_ws")

hostile_cwd="$tmp/hostile-cwd"
hostile_home="$tmp/hostile-home-real"
mkdir -p \
    "$hostile_cwd/.beads" \
    "$hostile_home/.config/bd" \
    "$hostile_home/.cache" \
    "$hostile_home/.local/share" \
    "$hostile_home/.local/state"
printf '%s\n' '{"backend":"postgres","database":"postgres"}' \
    > "$hostile_cwd/.beads/metadata.json"
printf '%s\n' 'backend: postgres' > "$hostile_cwd/.beads/config.yaml"
printf '%s\n' 'BD_BACKEND=postgres' > "$hostile_cwd/.beads/.env"
printf '%s\n' 'metrics.disabled: false' \
    > "$hostile_home/.config/bd/config.yaml"
hostile_cwd_before=$(tree_fingerprint "$hostile_cwd")
hostile_home_before=$(tree_fingerprint "$hostile_home")
hidden_real_stdout="$tmp/hidden-real.stdout"
hidden_real_stderr="$tmp/hidden-real.stderr"
set +e
(
    cd "$hostile_cwd"
    env \
        HOME="$hostile_home" \
        XDG_CONFIG_HOME="$hostile_home/.config" \
        XDG_CACHE_HOME="$hostile_home/.cache" \
        XDG_DATA_HOME="$hostile_home/.local/share" \
        XDG_STATE_HOME="$hostile_home/.local/state" \
        BD_BACKEND=postgres BD_DATABASE_BACKEND=mysql \
        BEADS_DB='postgres://poison.invalid/beads' \
        BD_DB='mysql://poison.invalid/beads' \
        BEADS_DIR="$hostile_cwd/.beads" \
        "$REAL_TARGET_BD" __migration-v062-inspect \
            --workspace "$real_ws" --json \
            > "$hidden_real_stdout" 2> "$hidden_real_stderr"
)
hidden_real_status=$?
set -e
[ "$hidden_real_status" -eq 0 ] ||
    fail "real hidden command exit=$hidden_real_status, want 0"
[ ! -s "$hidden_real_stderr" ] ||
    fail "real hidden command emitted stderr: $(<"$hidden_real_stderr")"
jq -e --arg workspace "$real_ws" '
    keys == ["effect", "operation", "retryable", "schema_version",
             "source", "status", "target"] and
    .schema_version == 1 and .operation == "v062_source_inspection" and
    .status == "qualified" and .retryable == false and .effect == "none" and
    .source.workspace == $workspace and .source.version == "0.62.0" and
    .source.backend == "dolt-server" and
    .source.digest_scope == "admission_observation" and
    (.source.tree_sha256 | test("^[0-9a-f]{64}$")) and
    .target.backend == "dolt-embedded" and
    .target.embedded_capable == true
' "$hidden_real_stdout" >/dev/null ||
    fail "real hidden command did not emit the exact qualified protocol"
assert_unchanged "$real_ws" "$real_before" "real hidden command"
assert_unchanged "$hostile_cwd" "$hostile_cwd_before" \
    "real hidden command hostile cwd"
assert_unchanged "$hostile_home" "$hostile_home_before" \
    "real hidden command hostile home"
hidden_real_digest=$(jq -r '.source.tree_sha256' "$hidden_real_stdout")

BRIDGE_TEST_TARGET="$REAL_TARGET_BD" \
    run_bridge real-current-target "$real_ws" \
        --inspect --workspace "$real_ws"
[ "$RUN_STATUS" -eq 1 ] ||
    fail "real current target exit=$RUN_STATUS, want fail-closed exit 1"
assert_common_json inspect failed none
jq -e --arg workspace "$real_ws" --arg digest "$hidden_real_digest" '
    .retryable == false and .code == "apply_not_available" and
    .source.workspace == $workspace and .source.version == "0.62.0" and
    .source.backend == "dolt-server" and
    .source.digest_scope == "admission_observation" and
    .source.tree_sha256 == $digest and
    .target.backend == "dolt-embedded" and
    .target.embedded_capable == true and
    .verification.source_digest_scope == "admission_observation" and
    .verification.apply_reinspection_required == true
' <<< "$RUN_STDOUT" >/dev/null ||
    fail "real current target did not return the qualified admission plan"
assert_unchanged "$real_ws" "$real_before" "real current target inspection"

real_negative_ws="$tmp/real-current-negative"
new_v062_workspace "$real_negative_ws"
printf '0.61.0\n' > "$real_negative_ws/.beads/.local_version"
real_negative_before=$(tree_fingerprint "$real_negative_ws")
BRIDGE_TEST_TARGET="$REAL_TARGET_BD" \
    run_bridge real-current-negative "$real_negative_ws" \
        --inspect --workspace "$real_negative_ws"
[ "$RUN_STATUS" -eq 1 ] ||
    fail "real negative source exit=$RUN_STATUS, want refusal exit 1"
assert_common_json inspect refused none
jq -e '
    .retryable == false and .code == "source_version_mismatch"
' <<< "$RUN_STDOUT" >/dev/null ||
    fail "real negative source did not preserve its typed refusal"
assert_unchanged "$real_negative_ws" "$real_negative_before" \
    "real negative source refusal"

printf 'public-v062-bridge-test: PASS\n'

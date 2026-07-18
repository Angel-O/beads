#!/usr/bin/env bash
# Qualified public bridge for v0.62.0 Dolt-server workspaces.
# Source admission is delegated to hidden no-follow plumbing in current bd.

set -uo pipefail
umask 077

readonly OPERATION=v062_server_to_current
readonly ENV_BIN=/usr/bin/env
readonly JQ_BIN=/usr/bin/jq
readonly READLINK_BIN=/usr/bin/readlink
readonly REALPATH_BIN=/usr/bin/realpath
readonly TIMEOUT_BIN=/usr/bin/timeout

MODE=inspect
MODE_SET=false
JSON_OUTPUT=false
YES=false
WORKSPACE_ARG=
TARGET_BD_ARG=

# Honor --json even when a preceding argument is invalid.
for argument in "$@"; do
    [ "$argument" = --json ] && JSON_OUTPUT=true
done

usage() {
    cat >&2 <<'EOF'
Usage: migrate-v062-server-to-current.sh [options]

  --workspace PROJECT  Project containing the v0.62.0 .beads directory
  --target-bd PATH     Absolute path to the current embedded-capable bd
  --inspect            Inspect only (default; no workspace effects)
  --apply              Perform the migration
  --yes                Required confirmation for --apply
  --json               Emit exactly one JSON result on stdout
  -h, --help           Show this help
EOF
}

bootstrap_json() {
    local status="$1" code="$2" retryable="$3" effect="$4"
    printf '{"schema_version":1,"operation":"%s",' "$OPERATION"
    printf '"mode":"%s","status":"%s","retryable":%s,' \
        "$MODE" "$status" "$retryable"
    printf '"effect":"%s","code":"%s"}\n' "$effect" "$code"
}

emit_base_json() {
    "$JQ_BIN" -cn \
        --arg operation "$OPERATION" --arg mode "$MODE" \
        --arg status "$1" --arg code "$2" \
        --argjson retryable "$3" --arg effect "$4" '
        {
            schema_version: 1, operation: $operation, mode: $mode,
            status: $status, retryable: $retryable, effect: $effect
        } + if $code == "" then {} else {code: $code} end
    '
}

finish_error() {
    local exit_status="$1" status="$2" code="$3"
    local retryable="$4" effect="$5" message="$6"
    printf '%s: %s\n' "$OPERATION" "$message" >&2
    if $JSON_OUTPUT; then
        if [ -x "$JQ_BIN" ]; then
            emit_base_json "$status" "$code" "$retryable" "$effect"
        else
            bootstrap_json "$status" "$code" "$retryable" "$effect"
        fi
    fi
    exit "$exit_status"
}

invalid_usage() {
    finish_error 2 usage_error "$1" false none "$2"
}

refuse() {
    finish_error 1 refused "$1" "$2" none "$3"
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --workspace)
            [ "$#" -ge 2 ] ||
                invalid_usage invalid_usage "--workspace requires a value"
            WORKSPACE_ARG="$2"
            shift 2
            ;;
        --target-bd)
            [ "$#" -ge 2 ] ||
                invalid_usage invalid_usage "--target-bd requires a value"
            TARGET_BD_ARG="$2"
            shift 2
            ;;
        --inspect|--apply)
            requested_mode="${1#--}"
            if $MODE_SET && [ "$MODE" != "$requested_mode" ]; then
                invalid_usage invalid_usage \
                    "--inspect and --apply are mutually exclusive"
            fi
            MODE="$requested_mode"
            MODE_SET=true
            shift
            ;;
        --yes)
            YES=true
            shift
            ;;
        --json)
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        --*)
            invalid_usage invalid_usage "unknown option: $1"
            ;;
        *)
            invalid_usage invalid_usage "unexpected argument: $1"
            ;;
    esac
done

if [ "$MODE" = apply ] && ! $YES; then
    invalid_usage confirmation_required \
        "--apply requires explicit --yes confirmation"
fi
if $YES && [ "$MODE" != apply ]; then
    invalid_usage invalid_usage "--yes is valid only with --apply"
fi

INSPECT_TIMEOUT_SECONDS=${BD_V062_INSPECT_TIMEOUT_SECONDS:-600}
if [[ ! "$INSPECT_TIMEOUT_SECONDS" =~ ^[1-9][0-9]*$ ]] ||
    [ "${#INSPECT_TIMEOUT_SECONDS}" -gt 4 ] ||
    [ "$INSPECT_TIMEOUT_SECONDS" -gt 3600 ]; then
    invalid_usage invalid_environment \
        "BD_V062_INSPECT_TIMEOUT_SECONDS must be an integer from 1 to 3600"
fi

for helper in \
    "$ENV_BIN" "$JQ_BIN" "$READLINK_BIN" "$REALPATH_BIN" "$TIMEOUT_BIN"; do
    [ -f "$helper" ] && [ -x "$helper" ] && [ ! -L "$helper" ] ||
        refuse missing_requirement true \
            "required fixed helper is unavailable: $helper"
done

[ -n "$WORKSPACE_ARG" ] || WORKSPACE_ARG="$PWD"
WORKSPACE=$("$REALPATH_BIN" -e -- "$WORKSPACE_ARG" 2>/dev/null) ||
    refuse workspace_invalid true "workspace cannot be resolved"
[ -d "$WORKSPACE" ] && [ ! -L "$WORKSPACE" ] ||
    refuse workspace_invalid true \
        "workspace must be a physical project directory"

if [ -z "$TARGET_BD_ARG" ]; then
    TARGET_BD_ARG=$(type -P bd 2>/dev/null) ||
        refuse target_binary_missing true "no current bd binary was found"
elif [[ "$TARGET_BD_ARG" != /* ]]; then
    refuse target_binary_invalid false \
        "--target-bd must be an absolute path"
fi
TARGET_BD=$("$READLINK_BIN" -f -- "$TARGET_BD_ARG" 2>/dev/null) ||
    refuse target_binary_invalid false \
        "the target bd path cannot be resolved"
[ -f "$TARGET_BD" ] && [ -x "$TARGET_BD" ] ||
    refuse target_binary_invalid false \
        "the target bd path is not an executable regular file"

inspection_stdout=$(
    "$ENV_BIN" -i \
        PATH=/usr/bin:/bin HOME=/nonexistent \
        XDG_CONFIG_HOME=/nonexistent XDG_CACHE_HOME=/nonexistent \
        XDG_DATA_HOME=/nonexistent XDG_STATE_HOME=/nonexistent \
        GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null \
        GIT_TERMINAL_PROMPT=0 BD_DISABLE_METRICS=1 \
        BD_DISABLE_EVENT_FLUSH=1 BD_NON_INTERACTIVE=1 \
        NO_COLOR=1 CI=1 LANG=C.UTF-8 LC_ALL=C.UTF-8 TZ=UTC TERM=dumb \
        "$TIMEOUT_BIN" --kill-after=5s "${INSPECT_TIMEOUT_SECONDS}s" \
        "$TARGET_BD" __migration-v062-inspect \
            --workspace "$WORKSPACE" --json 2>/dev/null
)
inspection_status=$?

if [ "$inspection_status" -eq 124 ] || [ "$inspection_status" -eq 137 ]; then
    refuse target_inspection_timeout true \
        "target bd inspection exceeded its bounded runtime"
fi

if ! inspection_json=$("$JQ_BIN" -cse \
    --argjson process_status "$inspection_status" '
    def has_code($codes):
        .code as $code | $codes | index($code) != null;
    def nonretryable_refusal:
        .retryable == false and has_code([
            "platform_unsupported",
            "embedded_target_unavailable",
            "workspace_invalid",
            "workspace_not_canonical",
            "source_version_missing",
            "source_version_mismatch",
            "source_version_ambiguous",
            "source_metadata_missing",
            "source_metadata_mismatch",
            "source_layout_missing",
            "unsafe_source_symlink",
            "unsafe_source_hardlink",
            "unsafe_source_object",
            "cross_device_source",
            "mixed_storage_layout",
            "rollback_collision"
        ]);
    def retryable_refusal:
        .retryable == true and has_code([
            "source_changed",
            "source_unverifiable"
        ]);
    if length != 1 then empty else .[0] end |
    select(
        type == "object" and .schema_version == 1 and
        .operation == "v062_source_inspection" and .effect == "none" and
        (
            (
                .status == "qualified" and $process_status == 0 and
                .retryable == false and
                keys == [
                    "effect", "operation", "retryable", "schema_version",
                    "source", "status", "target"
                ] and
                (.source | type) == "object" and
                (.source | keys) == [
                    "backend", "digest_scope", "tree_sha256",
                    "version", "workspace"
                ] and
                (.source.workspace | type) == "string" and
                (.source.version | type) == "string" and
                (.source.backend | type) == "string" and
                (.source.tree_sha256 | type) == "string" and
                (.source.digest_scope | type) == "string" and
                (.target | type) == "object" and
                (.target | keys) == [
                    "backend", "embedded_capable", "version"
                ] and
                (.target.version | type) == "string" and
                (.target.backend | type) == "string" and
                (.target.embedded_capable | type) == "boolean"
            ) or
            (
                .status == "refused" and $process_status == 1 and
                keys == [
                    "code", "effect", "operation", "retryable",
                    "schema_version", "status"
                ] and
                (nonretryable_refusal or retryable_refusal)
            )
        )
    )
' 2>/dev/null <<< "$inspection_stdout"); then
    refuse target_capability_missing false \
        "target bd does not implement the qualified inspection protocol"
fi

inspection_result_status=$("$JQ_BIN" -r '.status' <<< "$inspection_json")
if [ "$inspection_result_status" = refused ]; then
    inspection_code=$("$JQ_BIN" -r '.code' <<< "$inspection_json")
    inspection_retryable=$("$JQ_BIN" -r '.retryable' <<< "$inspection_json")
    finish_error 1 refused "$inspection_code" \
        "$inspection_retryable" none \
        "the no-follow source inspector refused this workspace"
fi

if [ "$inspection_status" -ne 0 ] ||
    ! "$JQ_BIN" -e --arg workspace "$WORKSPACE" '
        .status == "qualified" and
        .source.workspace == $workspace and
        .source.version == "0.62.0" and
        .source.backend == "dolt-server" and
        .source.digest_scope == "admission_observation" and
        (.source.tree_sha256 | type) == "string" and
        (.source.tree_sha256 | test("^[0-9a-f]{64}$")) and
        .target.backend == "dolt-embedded" and
        .target.embedded_capable == true and
        (.target.version | type) == "string" and
        (.target.version |
            test("^[0-9]+[.][0-9]+[.][0-9]+([+-].*)?$")) and
        ((.target.version | split(".")[0] | tonumber) >= 1)
    ' >/dev/null 2>&1 <<< "$inspection_json"; then
    reported_target_version=$(
        "$JQ_BIN" -r '.target.version // ""' <<< "$inspection_json" \
            2>/dev/null
    ) || reported_target_version=
    if [ "$reported_target_version" = 0.62.0 ]; then
        refuse target_binary_invalid false \
            "the historical bd binary cannot be the migration target"
    fi
    refuse target_capability_missing false \
        "target bd is not a qualified embedded-capable migration target"
fi

rollback_path="$WORKSPACE/.beads-v0.62.0-rollback"

# Be truthful while this stack contains admission but not the effectful bridge.
if $JSON_OUTPUT; then
    "$JQ_BIN" -cn \
        --argjson inspection "$inspection_json" \
        --arg operation "$OPERATION" --arg mode "$MODE" \
        --arg rollback "$rollback_path" '
        {
            schema_version: 1, operation: $operation, mode: $mode,
            status: "failed", code: "apply_not_available",
            retryable: false, effect: "none",
            source: {
                workspace: $inspection.source.workspace,
                version: $inspection.source.version,
                backend: $inspection.source.backend,
                tree_sha256: $inspection.source.tree_sha256,
                digest_scope: $inspection.source.digest_scope
            },
            target: {
                version: $inspection.target.version,
                backend: $inspection.target.backend,
                embedded_capable: $inspection.target.embedded_capable
            },
            rollback: {path: $rollback, policy: "retain"},
            verification: {
                source_shape: "qualified",
                source_tree_sha256: $inspection.source.tree_sha256,
                source_digest_scope: $inspection.source.digest_scope,
                apply_reinspection_required: true,
                apply_available: false,
                workspace_effects: false
            }
        }
    '
else
    printf '%s\n' \
        'Qualified v0.62.0 source; apply is not available in this build.' >&2
fi
exit 1

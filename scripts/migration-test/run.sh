#!/bin/bash
set -uo pipefail

# =============================================================================
# Migration Test Harness
# =============================================================================
#
# Tests upgrade paths from old beads versions to the candidate binary,
# verifying data fidelity field-by-field and discovering working migration
# recipes when direct upgrades fail.
#
# For each upgrade path tested:
#   1. Init workspace with source version, create rich canonical dataset
#   2. Capture full JSON snapshot of all data (before)
#   3. Upgrade to candidate (or next stepping-stone version)
#   4. Capture full JSON snapshot (after)
#   5. Compare field-by-field for fidelity violations
#   6. If direct upgrade fails, try applicable migration recipes
#   7. Report: AUTO / MANUAL(recipe) / BLOCKED per path
#
# Usage:
#   ./scripts/migration-test/run.sh                    # all direct + stepping-stone paths
#   ./scripts/migration-test/run.sh --direct-only      # only direct paths
#   ./scripts/migration-test/run.sh --stepping-only    # only stepping-stone paths
#   ./scripts/migration-test/run.sh --self-test        # candidate → candidate (harness validation)
#   ./scripts/migration-test/run.sh --strict --expect MANUAL v0.49.6
#   ./scripts/migration-test/run.sh v0.49.6            # single version
#   CANDIDATE_BIN=./bd ./scripts/migration-test/run.sh # prebuilt candidate
#
# Environment:
#   CANDIDATE_BIN      Path to prebuilt candidate binary (skip build)
#   BEADS_TEST_MODE    Set to 1 to disable Dolt auto-start (default: 0)
#   GIT_CONFIG_NOSYSTEM  Set to 1 to ignore system git config (default: 1)
#   BD_OP_TIMEOUT      Timeout in seconds for bd operations (default: 30)
#   DOWNLOAD_TIMEOUT   Timeout in seconds for binary downloads (default: 60)
#
# Exit codes:
#   0  No BLOCKED paths (or the exact qualified result in strict mode)
#   1  A path is BLOCKED, or strict qualification fails
# =============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Source library modules
source "$SCRIPT_DIR/lib/report.sh"      # colors + result tracking (must be first for color vars)
source "$SCRIPT_DIR/lib/versions.sh"    # release manifest, ERAS, DIRECT_PATHS, get_era
source "$SCRIPT_DIR/lib/binary.sh"      # download_binary, build_candidate
source "$SCRIPT_DIR/lib/workspace.sh"   # new_workspace, bd_in, bd_create, cleanup_workspace
source "$SCRIPT_DIR/lib/features.sh"    # create_dataset, try_feature
source "$SCRIPT_DIR/lib/snapshot.sh"    # capture_snapshot, check_fidelity
source "$SCRIPT_DIR/lib/direct_probe.sh" # fail-closed candidate probing

# Source recipe scripts
source "$SCRIPT_DIR/recipes/sqlite_to_current.sh"
source "$SCRIPT_DIR/recipes/server_to_embedded.sh"
source "$SCRIPT_DIR/recipes/fix_dash_prefix.sh"

verify_strict_retained_source() {
    local ws="$1"
    local era="$2"
    local sqlite_manifest="$3"
    local legacy_dolt_manifest="$4"

    case "$era" in
        sqlite)
            [ -n "$sqlite_manifest" ] && \
                verify_retained_sqlite_source "$ws/.beads" "$sqlite_manifest"
            ;;
        dolt_server)
            [ -n "$legacy_dolt_manifest" ] && \
                verify_retained_legacy_dolt_source "$ws/.beads" "$legacy_dolt_manifest"
            ;;
        *)
            return 0
            ;;
    esac
}

verify_v062_prestarted_source() {
    local ws="$1"
    local reserved_port="$2"
    local metadata="$ws/.beads/metadata.json"
    local local_version="$ws/.beads/.local_version"
    local schema_json=""

    [[ "$reserved_port" =~ ^2[0-9]{4}$ ]] || {
        echo "reserved server port is invalid"
        return 1
    }
    if [ ! -f "$metadata" ] || [ -L "$metadata" ]; then
        echo "v0.62.0 init did not create authentic metadata"
        return 1
    fi
    if ! jq -e --argjson reserved_port "$reserved_port" '
        type == "object" and
        .backend == "dolt" and
        .database == "dolt" and
        .dolt_mode == "server" and
        .dolt_server_host == "127.0.0.1" and
        (.dolt_server_port | type == "number") and
        .dolt_server_port == $reserved_port and
        .dolt_database == "smoke" and
        (.project_id | type == "string" and length > 0)
    ' "$metadata" >/dev/null; then
        echo "v0.62.0 init metadata does not match the server fixture contract"
        return 1
    fi
    if [ ! -f "$local_version" ] || [ -L "$local_version" ] ||
        ! printf '0.62.0\n' | cmp -s - "$local_version"; then
        echo "v0.62.0 init did not write the exact historical version marker"
        return 1
    fi
    if [ ! -d "$ws/.beads/dolt" ] || [ -L "$ws/.beads/dolt" ] ||
        [ ! -d "$ws/.beads/dolt/.dolt" ] || [ -L "$ws/.beads/dolt/.dolt" ] ||
        [ ! -d "$ws/.beads/dolt/smoke" ] || [ -L "$ws/.beads/dolt/smoke" ] ||
        [ ! -d "$ws/.beads/dolt/smoke/.dolt" ] || [ -L "$ws/.beads/dolt/smoke/.dolt" ]; then
        echo "v0.62.0 init did not create the local Dolt catalog and smoke database"
        return 1
    fi
    schema_json=$(migration_owned_dolt_sql \
        "$ws" smoke \
        "SELECT value AS schema_version FROM config WHERE \`key\` = 'schema_version'") || {
        echo "could not query the v0.62.0 source schema"
        return 1
    }
    if ! jq -e '
        type == "object" and
        (.rows | type == "array" and length == 1) and
        (.rows[0] |
            type == "object" and
            (.schema_version | type == "string") and
            .schema_version == "9")
    ' <<< "$schema_json" >/dev/null; then
        echo "v0.62.0 source schema version is not exactly string 9"
        return 1
    fi
}

block_source_initialization() {
    local ws="$1"
    local snapshots_dir="$2"
    local path_label="$3"
    local detail="$4"

    migration_run_record_result_after_cleanup \
        "$ws" "$snapshots_dir" "$path_label" "BLOCKED" "$detail" || true
    echo -e "  ${RED}BLOCKED: ${RESULT_DETAILS[-1]}${NC}"
}

MIGRATION_RUN_ACTIVE_WORKSPACE=""
MIGRATION_RUN_ACTIVE_SNAPSHOTS=""

migration_run_activate_workspace() {
    MIGRATION_RUN_ACTIVE_WORKSPACE="$1"
    MIGRATION_RUN_ACTIVE_SNAPSHOTS="${2:-}"
}

migration_run_forget_workspace() {
    local ws="$1"

    if [ "$MIGRATION_RUN_ACTIVE_WORKSPACE" = "$ws" ]; then
        MIGRATION_RUN_ACTIVE_WORKSPACE=""
        MIGRATION_RUN_ACTIVE_SNAPSHOTS=""
    fi
}

migration_run_cleanup_active_workspace() {
    local ws="$MIGRATION_RUN_ACTIVE_WORKSPACE"
    local snapshots="$MIGRATION_RUN_ACTIVE_SNAPSHOTS"

    [ -n "$ws" ] || return 0
    migration_run_forget_workspace "$ws"
    if [ ! -e "$ws" ] && [ ! -L "$ws" ]; then
        [ -z "$snapshots" ] || rm -rf -- "$snapshots"
        return 0
    fi
    if cleanup_workspace "$ws"; then
        [ -z "$snapshots" ] || rm -rf -- "$snapshots"
        return 0
    fi
    echo "could not prove isolated workspace cleanup; preserved at $ws" >&2
    return 1
}

migration_run_install_cleanup_traps() {
    trap 'migration_run_cleanup_active_workspace' EXIT
    trap 'exit 129' HUP
    trap 'exit 130' INT
    trap 'exit 143' TERM
}

migration_run_record_result_after_cleanup() {
    local ws="$1"
    local snapshots="$2"
    local path="$3"
    local status="$4"
    local detail="$5"
    local recipe="${6:-}"
    local violations="${7:-0}"

    if ! cleanup_workspace "$ws"; then
        migration_run_forget_workspace "$ws"
        record_result "$path" "BLOCKED" \
            "could not prove isolated workspace cleanup; preserved at $ws" "" "$violations"
        return 1
    fi
    migration_run_forget_workspace "$ws"
    [ -z "$snapshots" ] || rm -rf -- "$snapshots"
    record_result "$path" "$status" "$detail" "$recipe" "$violations"
}

if [ "${MIGRATION_TEST_RUN_LIBRARY_ONLY:-0}" = "1" ]; then
    return 0 2>/dev/null || exit 0
fi

# Ensure jq is available
if ! command -v jq >/dev/null 2>&1; then
    echo -e "${RED}ERROR: jq is required but not installed.${NC}"
    echo "Install with: sudo apt install jq / brew install jq"
    exit 1
fi
migration_run_install_cleanup_traps

# ---------------------------------------------------------------------------
# Test a direct upgrade path: source_version → candidate
# ---------------------------------------------------------------------------

test_direct_path() {
    local version="$1"
    local cand_bin="$2"
    local path_label="${version} → candidate"

    echo ""
    echo -e "${BOLD}● Direct: ${path_label}${NC}"

    if $STRICT_MODE && ! verify_strict_historical_runtime "$version"; then
        local required_runtime=""
        required_runtime=$(strict_required_dolt_version "$version" 2>/dev/null) || true
        record_result "$path_label" "BLOCKED" \
            "required Dolt runtime ${required_runtime:-unknown} is unavailable"
        echo -e "  ${RED}BLOCKED: required Dolt runtime ${required_runtime:-unknown} is unavailable${NC}"
        return 0
    fi

    # Download source binary
    local src_bin
    if $STRICT_MODE; then
        src_bin=$(download_binary "$version") || {
            record_result "$path_label" "BLOCKED" "verified release unavailable for ${OS}/${ARCH}"
            echo -e "  ${RED}BLOCKED: verified release unavailable for ${OS}/${ARCH}${NC}"
            return 0
        }
    else
        src_bin=$(download_binary "$version" 2>/dev/null) || {
            record_result "$path_label" "SKIP" "no binary for ${OS}/${ARCH}"
            echo -e "  ${YELLOW}SKIP: no binary for ${OS}/${ARCH}${NC}"
            return 0
        }
    fi

    local WS
    if ! WS=$(new_workspace); then
        record_result "$path_label" "BLOCKED" "could not create isolated workspace"
        echo -e "  ${RED}BLOCKED: could not create isolated workspace${NC}"
        return 0
    fi
    migration_run_activate_workspace "$WS" ""
    local SNAPSHOTS_DIR=""
    if ! SNAPSHOTS_DIR=$(mktemp -d /tmp/bd-snapshots-XXXXXX); then
        migration_run_record_result_after_cleanup \
            "$WS" "" "$path_label" "BLOCKED" \
            "could not create isolated snapshot directory" || true
        echo -e "  ${RED}BLOCKED: ${RESULT_DETAILS[-1]}${NC}"
        return 0
    fi
    migration_run_activate_workspace "$WS" "$SNAPSHOTS_DIR"

    # Step 1: Init with source binary
    local init_ok=false
    if [ "$version" = "v0.62.0" ]; then
        local bootstrap_strategy=""
        local init_output=""
        local init_port=""
        local verification_detail=""

        bootstrap_strategy=$(server_bootstrap_strategy "$version" 2>/dev/null) || true
        if [ "$bootstrap_strategy" != "prestarted_server" ]; then
            block_source_initialization \
                "$WS" "$SNAPSHOTS_DIR" "$path_label" \
                "v0.62.0 server bootstrap contract is unavailable"
            return 0
        fi
        if ! start_owned_migration_dolt_server "$WS"; then
            block_source_initialization \
                "$WS" "$SNAPSHOTS_DIR" "$path_label" \
                "could not start the owned v0.62.0 Dolt server"
            return 0
        fi
        if [ ! -f "$WS/.git/bd-migration-server-port" ] ||
            [ -L "$WS/.git/bd-migration-server-port" ]; then
            block_source_initialization \
                "$WS" "$SNAPSHOTS_DIR" "$path_label" \
                "owned v0.62.0 Dolt server has no reserved port"
            return 0
        fi
        init_port=$(cat "$WS/.git/bd-migration-server-port") || init_port=""
        if ! [[ "$init_port" =~ ^2[0-9]{4}$ ]]; then
            block_source_initialization \
                "$WS" "$SNAPSHOTS_DIR" "$path_label" \
                "owned v0.62.0 Dolt server has an invalid reserved port"
            return 0
        fi

        if init_output=$(bd_in "$WS" "$src_bin" init \
            --quiet \
            --server-host 127.0.0.1 --server-port "$init_port" \
            --database smoke --prefix smoke </dev/null 2>&1); then
            init_ok=true
        else
            local init_first_line=""
            init_first_line=$(head -1 <<< "$init_output")
            block_source_initialization \
                "$WS" "$SNAPSHOTS_DIR" "$path_label" \
                "v0.62.0 one-shot init failed: ${init_first_line:-no error output}"
            return 0
        fi

        verification_detail=$(verify_v062_prestarted_source "$WS" "$init_port") || {
            block_source_initialization \
                "$WS" "$SNAPSHOTS_DIR" "$path_label" "$verification_detail"
            return 0
        }
    else
        if bd_in "$WS" "$src_bin" init --quiet --non-interactive --prefix smoke </dev/null >/dev/null 2>&1; then
            init_ok=true
        elif bd_in "$WS" "$src_bin" init --quiet --prefix smoke </dev/null >/dev/null 2>&1; then
            init_ok=true
        fi

        if ! $init_ok; then
            local init_err init_status init_detail
            init_err=$(bd_in "$WS" "$src_bin" init --quiet --prefix smoke </dev/null 2>&1 | head -1 || true)

            if echo "$init_err" | grep -qi "CGO"; then
                init_status="SKIP"
                init_detail="binary built without CGO"
            elif echo "$init_err" | grep -qi "dolt.*server\|unreachable\|auto-start"; then
                init_status="SKIP"
                init_detail="needs dolt server"
            else
                init_status="BLOCKED"
                init_detail="init failed: ${init_err}"
            fi
            migration_run_record_result_after_cleanup \
                "$WS" "$SNAPSHOTS_DIR" "$path_label" \
                "$init_status" "$init_detail" || true
            echo -e "  ${RESULT_STATUSES[-1]}: ${RESULT_DETAILS[-1]}"
            return 0
        fi
    fi
    git -C "$WS" config beads.role maintainer 2>/dev/null || true

    # Step 2: Create rich dataset with source binary
    if ! create_dataset "$WS" "$src_bin" "$version"; then
        migration_run_record_result_after_cleanup \
            "$WS" "$SNAPSHOTS_DIR" "$path_label" "BLOCKED" \
            "could not create test data" || true
        echo -e "  ${RED}BLOCKED: ${RESULT_DETAILS[-1]}${NC}"
        return 0
    fi
    if $STRICT_MODE && ! strict_fixture_has_expected_features "$version" "${DATASET_FEATURES[@]}"; then
        migration_run_record_result_after_cleanup \
            "$WS" "$SNAPSHOTS_DIR" "$path_label" "BLOCKED" \
            "source fixture is missing required features" || true
        echo -e "  ${RED}BLOCKED: ${RESULT_DETAILS[-1]}${NC}"
        return 0
    fi

    # Step 3: Capture before-snapshot
    if ! capture_snapshot "$WS" "$src_bin" > "$SNAPSHOTS_DIR/before.json"; then
        if $STRICT_MODE; then
            migration_run_record_result_after_cleanup \
                "$WS" "$SNAPSHOTS_DIR" "$path_label" "BLOCKED" \
                "could not capture the complete source snapshot" || true
            echo -e "  ${RED}BLOCKED: ${RESULT_DETAILS[-1]}${NC}"
            return 0
        fi
    fi
    local before_count
    before_count=$(jq 'length' "$SNAPSHOTS_DIR/before.json" 2>/dev/null) || before_count=0
    echo "  before-snapshot: $before_count items"
    if $STRICT_MODE && [ "$before_count" -eq 0 ]; then
        migration_run_record_result_after_cleanup \
            "$WS" "$SNAPSHOTS_DIR" "$path_label" "BLOCKED" \
            "source snapshot is empty" || true
        echo -e "  ${RED}BLOCKED: ${RESULT_DETAILS[-1]}${NC}"
        return 0
    fi
    if $STRICT_MODE && ! strict_snapshot_has_expected_fixture "$version" "$SNAPSHOTS_DIR/before.json"; then
        migration_run_record_result_after_cleanup \
            "$WS" "$SNAPSHOTS_DIR" "$path_label" "BLOCKED" \
            "source snapshot does not match the exact fixture contract" || true
        echo -e "  ${RED}BLOCKED: ${RESULT_DETAILS[-1]}${NC}"
        return 0
    fi

    # Stop source server before upgrade
    if ! stop_dolt_server "$WS"; then
        migration_run_forget_workspace "$WS"
        record_result "$path_label" "BLOCKED" \
            "could not prove the historical server stopped before upgrade; preserved at $WS"
        echo -e "  ${RED}BLOCKED: could not prove the historical server stopped before upgrade${NC}"
        return 0
    fi

    local source_era
    source_era=$(get_era "$version")
    local source_fingerprint=""
    local source_sqlite_manifest=""
    local source_legacy_dolt_manifest=""
    if $STRICT_MODE; then
        source_fingerprint=$(source_artifact_fingerprint "$WS/.beads") || {
            migration_run_record_result_after_cleanup \
                "$WS" "$SNAPSHOTS_DIR" "$path_label" "BLOCKED" \
                "could not fingerprint historical source tree" || true
            return 0
        }
        case "$source_era" in
            sqlite)
                source_sqlite_manifest=$(classic_sqlite_artifact_manifest "$WS/.beads") || {
                    migration_run_record_result_after_cleanup \
                        "$WS" "$SNAPSHOTS_DIR" "$path_label" "BLOCKED" \
                        "could not inventory classic SQLite rollback source" || true
                    return 0
                }
                ;;
            dolt_server)
                source_legacy_dolt_manifest=$(legacy_dolt_artifact_manifest "$WS/.beads") || {
                    migration_run_record_result_after_cleanup \
                        "$WS" "$SNAPSHOTS_DIR" "$path_label" "BLOCKED" \
                        "could not inventory legacy Dolt rollback source" || true
                    return 0
                }
                ;;
        esac
    fi

    # Step 4: Try direct upgrade with candidate
    echo "  upgrading to candidate..."
    local upgrade_ok=false
    local probe_status=0
    probe_candidate_direct_upgrade \
        "$WS" "$cand_bin" "$source_fingerprint" "$STRICT_MODE" || probe_status=$?
    if [ "$probe_status" -eq 0 ]; then
        upgrade_ok=true
    fi

    if [ "$probe_status" -eq 2 ]; then
        migration_run_record_result_after_cleanup \
            "$WS" "$SNAPSHOTS_DIR" "$path_label" "BLOCKED" \
            "$DIRECT_PROBE_FAILURE_DETAIL" || true
        echo -e "  ${RED}BLOCKED: ${RESULT_DETAILS[-1]}${NC}"
        return 0
    fi
    if $STRICT_MODE && ! $upgrade_ok; then
        echo -e "  ${GREEN}SOURCE-CHECK: failed direct probe left historical source artifacts unchanged${NC}"
    fi

    if $upgrade_ok; then
        # Capture after-snapshot and check fidelity
        if ! capture_snapshot "$WS" "$cand_bin" > "$SNAPSHOTS_DIR/after.json"; then
            migration_run_record_result_after_cleanup \
                "$WS" "$SNAPSHOTS_DIR" "$path_label" "BLOCKED" \
                "could not capture the complete candidate snapshot" || true
            echo -e "  ${RED}BLOCKED: ${RESULT_DETAILS[-1]}${NC}"
            return 0
        fi
        local after_count
        after_count=$(jq 'length' "$SNAPSHOTS_DIR/after.json" 2>/dev/null) || after_count=0
        echo "  after-snapshot: $after_count items"

        local violations=0
        check_fidelity "$version" "$SNAPSHOTS_DIR/before.json" "$SNAPSHOTS_DIR/after.json" || violations=$?

        local blocker_violations=0
        check_blocker_paths "$WS" "$cand_bin" || blocker_violations=$?
        violations=$((violations + blocker_violations))

        local direct_status direct_detail
        if [ "$blocker_violations" -gt 0 ]; then
            direct_status="BLOCKED"
            direct_detail="direct upgrade, $violations fidelity/blocker violations"
        else
            direct_status="AUTO"
            direct_detail="direct upgrade, $violations fidelity violations"
        fi
        if ! migration_run_record_result_after_cleanup \
            "$WS" "$SNAPSHOTS_DIR" "$path_label" \
            "$direct_status" "$direct_detail" "" "$violations"; then
            echo -e "  ${RED}BLOCKED: could not prove isolated workspace cleanup${NC}"
        fi
        return 0
    fi

    # Step 5: Direct upgrade failed — try recipes
    if ! stop_dolt_server "$WS"; then
        migration_run_forget_workspace "$WS"
        record_result "$path_label" "BLOCKED" \
            "could not prove the direct-probe server stopped before migration recipes; preserved at $WS"
        echo -e "  ${RED}BLOCKED: could not prove the direct-probe server stopped before migration recipes${NC}"
        return 0
    fi
    echo "  direct upgrade failed, trying recipes..."

    local era="$source_era"
    local recipe_worked=false
    local recipe_name=""

    case "$era" in
        sqlite)
            if recipe_sqlite_to_current "$WS" "$src_bin" "$cand_bin" "$version"; then
                recipe_worked=true
                recipe_name="sqlite_to_current"
            fi
            ;;
        dolt_server)
            if recipe_server_to_embedded \
                "$WS" "$src_bin" "$cand_bin" "$version" "$SNAPSHOTS_DIR/before.json"; then
                recipe_worked=true
                recipe_name="server_to_embedded"
            fi
            ;;
        embedded_old)
            if recipe_fix_dash_prefix "$WS" "$src_bin" "$cand_bin" "$version"; then
                recipe_worked=true
                recipe_name="fix_dash_prefix"
            fi
            # Also try server recipe if prefix fix didn't help
            if ! $recipe_worked; then
                if recipe_server_to_embedded \
                    "$WS" "$src_bin" "$cand_bin" "$version" "$SNAPSHOTS_DIR/before.json"; then
                    recipe_worked=true
                    recipe_name="server_to_embedded"
                fi
            fi
            ;;
    esac

    if $recipe_worked; then
        if $STRICT_MODE && ! verify_strict_retained_source \
            "$WS" "$era" "$source_sqlite_manifest" "$source_legacy_dolt_manifest"; then
            migration_run_record_result_after_cleanup \
                "$WS" "$SNAPSHOTS_DIR" "$path_label" "BLOCKED" \
                "manual bridge mutated the retained historical rollback source" || true
            echo -e "  ${RED}BLOCKED: ${RESULT_DETAILS[-1]}${NC}"
            return 0
        fi

        # Re-capture and check fidelity after recipe
        if ! capture_snapshot "$WS" "$cand_bin" > "$SNAPSHOTS_DIR/after.json"; then
            migration_run_record_result_after_cleanup \
                "$WS" "$SNAPSHOTS_DIR" "$path_label" "BLOCKED" \
                "could not capture the complete candidate snapshot after recipe" || true
            echo -e "  ${RED}BLOCKED: ${RESULT_DETAILS[-1]}${NC}"
            return 0
        fi
        local after_count
        after_count=$(jq 'length' "$SNAPSHOTS_DIR/after.json" 2>/dev/null) || after_count=0
        echo "  after-recipe snapshot: $after_count items"

        local violations=0
        check_fidelity "$version" "$SNAPSHOTS_DIR/before.json" "$SNAPSHOTS_DIR/after.json" || violations=$?

        local blocker_violations=0
        check_blocker_paths "$WS" "$cand_bin" || blocker_violations=$?
        violations=$((violations + blocker_violations))

        if ! stop_dolt_server "$WS"; then
            migration_run_forget_workspace "$WS"
            record_result "$path_label" "BLOCKED" \
                "could not prove the candidate server stopped before rollback verification; preserved at $WS"
            echo -e "  ${RED}BLOCKED: could not prove the candidate server stopped before rollback verification${NC}"
            return 0
        fi

        if $STRICT_MODE && ! verify_strict_retained_source \
            "$WS" "$era" "$source_sqlite_manifest" "$source_legacy_dolt_manifest"; then
            migration_run_record_result_after_cleanup \
                "$WS" "$SNAPSHOTS_DIR" "$path_label" "BLOCKED" \
                "post-upgrade commands mutated the retained historical rollback source" || true
            echo -e "  ${RED}BLOCKED: ${RESULT_DETAILS[-1]}${NC}"
            return 0
        fi

        local recipe_status recipe_detail
        if [ "$blocker_violations" -gt 0 ]; then
            recipe_status="BLOCKED"
            recipe_detail="recipe: $recipe_name, $violations fidelity/blocker violations"
        else
            recipe_status="MANUAL"
            recipe_detail="recipe: $recipe_name, $violations fidelity violations"
        fi
        if ! migration_run_record_result_after_cleanup \
            "$WS" "$SNAPSHOTS_DIR" "$path_label" \
            "$recipe_status" "$recipe_detail" "$recipe_name" "$violations"; then
            echo -e "  ${RED}BLOCKED: could not prove isolated workspace cleanup${NC}"
        fi
        return 0
    fi

    # All recipes failed
    migration_run_record_result_after_cleanup \
        "$WS" "$SNAPSHOTS_DIR" "$path_label" "BLOCKED" \
        "no working upgrade path found" || true
    echo -e "  ${RED}BLOCKED: ${RESULT_DETAILS[-1]}${NC}"
}

# ---------------------------------------------------------------------------
# Test a stepping-stone path: v1 → v2 → ... → candidate
# ---------------------------------------------------------------------------

test_stepping_stone_path() {
    local path_spec="$1"    # comma-separated versions, e.g. "v0.49.6,v0.57.0"
    local cand_bin="$2"

    IFS=',' read -ra VERSIONS <<< "$path_spec"
    local path_label
    path_label=$(printf '%s → ' "${VERSIONS[@]}")
    path_label="${path_label}candidate"

    echo ""
    echo -e "${BOLD}● Stepping-stone: ${path_label}${NC}"

    # Download all required binaries
    local -a bins=()
    for v in "${VERSIONS[@]}"; do
        local b
        b=$(download_binary "$v" 2>/dev/null) || {
            record_result "$path_label" "SKIP" "no binary for $v (${OS}/${ARCH})"
            echo -e "  ${YELLOW}SKIP: no binary for $v${NC}"
            return 0
        }
        bins+=("$b")
    done

    local WS
    if ! WS=$(new_workspace); then
        record_result "$path_label" "BLOCKED" "could not create isolated workspace"
        echo -e "  ${RED}BLOCKED: could not create isolated workspace${NC}"
        return 0
    fi
    migration_run_activate_workspace "$WS" ""
    local SNAPSHOTS_DIR=""
    if ! SNAPSHOTS_DIR=$(mktemp -d /tmp/bd-snapshots-XXXXXX); then
        migration_run_record_result_after_cleanup \
            "$WS" "" "$path_label" "BLOCKED" \
            "could not create isolated snapshot directory" || true
        echo -e "  ${RED}BLOCKED: ${RESULT_DETAILS[-1]}${NC}"
        return 0
    fi
    migration_run_activate_workspace "$WS" "$SNAPSHOTS_DIR"

    # Init with first version
    local first_bin="${bins[0]}"
    local first_ver="${VERSIONS[0]}"

    local init_ok=false
    if bd_in "$WS" "$first_bin" init --quiet --non-interactive --prefix smoke </dev/null >/dev/null 2>&1; then
        init_ok=true
    elif bd_in "$WS" "$first_bin" init --quiet --prefix smoke </dev/null >/dev/null 2>&1; then
        init_ok=true
    fi

    if ! $init_ok; then
        migration_run_record_result_after_cleanup \
            "$WS" "$SNAPSHOTS_DIR" "$path_label" "SKIP" \
            "could not init with $first_ver" || true
        echo -e "  ${RESULT_STATUSES[-1]}: ${RESULT_DETAILS[-1]}"
        return 0
    fi
    git -C "$WS" config beads.role maintainer 2>/dev/null || true

    # Create dataset with first version
    if ! create_dataset "$WS" "$first_bin" "$first_ver"; then
        migration_run_record_result_after_cleanup \
            "$WS" "$SNAPSHOTS_DIR" "$path_label" "BLOCKED" \
            "could not create test data with $first_ver" || true
        return 0
    fi

    # Capture initial snapshot
    capture_snapshot "$WS" "$first_bin" > "$SNAPSHOTS_DIR/before.json"
    if ! stop_dolt_server "$WS"; then
        migration_run_forget_workspace "$WS"
        record_result "$path_label" "BLOCKED" \
            "could not prove the first historical server stopped before stepping; preserved at $WS"
        echo -e "  ${RED}BLOCKED: could not prove the first historical server stopped before stepping${NC}"
        return 0
    fi

    # Step through intermediate versions
    local step_failed=false
    local failed_at=""
    for i in $(seq 1 $((${#VERSIONS[@]} - 1))); do
        local step_ver="${VERSIONS[$i]}"
        local step_bin="${bins[$i]}"
        echo "  stepping to $step_ver..."

        # Try the step
        local step_ok=false
        local list_out
        list_out=$(bd_in "$WS" "$step_bin" list --json -n 0 --all 2>/dev/null) || true
        if [ -n "$list_out" ] && [ "$list_out" != "[]" ] && [ "$list_out" != "null" ]; then
            step_ok=true
        fi
        if ! $step_ok; then
            bd_in "$WS" "$step_bin" init --quiet --non-interactive --prefix smoke </dev/null >/dev/null 2>&1 || true
            list_out=$(bd_in "$WS" "$step_bin" list --json -n 0 --all 2>/dev/null) || true
            if [ -n "$list_out" ] && [ "$list_out" != "[]" ] && [ "$list_out" != "null" ]; then
                step_ok=true
            fi
        fi

        if ! stop_dolt_server "$WS"; then
            migration_run_forget_workspace "$WS"
            record_result "$path_label" "BLOCKED" \
                "could not prove the $step_ver server stopped; preserved at $WS"
            echo -e "  ${RED}BLOCKED: $step_ver server stop unverified${NC}"
            return 0
        fi

        if ! $step_ok; then
            step_failed=true
            failed_at="$step_ver"
            break
        fi
        echo -e "  ${GREEN}step to $step_ver OK${NC}"
    done

    if $step_failed; then
        migration_run_record_result_after_cleanup \
            "$WS" "$SNAPSHOTS_DIR" "$path_label" "BLOCKED" \
            "failed at step $failed_at" || true
        echo -e "  ${RED}BLOCKED at $failed_at${NC}"
        return 0
    fi

    # Final step: candidate
    echo "  stepping to candidate..."
    local upgrade_ok=false
    local list_out
    list_out=$(bd_in "$WS" "$cand_bin" list --json -n 0 --all 2>/dev/null) || true
    if [ -n "$list_out" ] && [ "$list_out" != "[]" ] && [ "$list_out" != "null" ]; then
        upgrade_ok=true
    fi
    if ! $upgrade_ok; then
        bd_in "$WS" "$cand_bin" init --quiet --non-interactive --prefix smoke </dev/null >/dev/null 2>&1 || true
        list_out=$(bd_in "$WS" "$cand_bin" list --json -n 0 --all 2>/dev/null) || true
        if [ -n "$list_out" ] && [ "$list_out" != "[]" ] && [ "$list_out" != "null" ]; then
            upgrade_ok=true
        fi
    fi

    if ! $upgrade_ok; then
        migration_run_record_result_after_cleanup \
            "$WS" "$SNAPSHOTS_DIR" "$path_label" "BLOCKED" \
            "final step to candidate failed" || true
        echo -e "  ${RED}BLOCKED at final step${NC}"
        return 0
    fi

    # Capture after-snapshot and check fidelity
    capture_snapshot "$WS" "$cand_bin" > "$SNAPSHOTS_DIR/after.json"
    local violations=0
    check_fidelity "${first_ver}" "$SNAPSHOTS_DIR/before.json" "$SNAPSHOTS_DIR/after.json" || violations=$?

    local blocker_violations=0
    check_blocker_paths "$WS" "$cand_bin" || blocker_violations=$?
    violations=$((violations + blocker_violations))

    local step_status step_detail
    if [ "$blocker_violations" -gt 0 ]; then
        step_status="BLOCKED"
        step_detail="steps passed, $violations fidelity/blocker violations"
    else
        step_status="AUTO"
        step_detail="all steps passed, $violations fidelity violations"
    fi
    if ! migration_run_record_result_after_cleanup \
        "$WS" "$SNAPSHOTS_DIR" "$path_label" \
        "$step_status" "$step_detail" "" "$violations"; then
        echo -e "  ${RED}BLOCKED: could not prove isolated workspace cleanup${NC}"
    fi
}

# ---------------------------------------------------------------------------
# Parse arguments
# ---------------------------------------------------------------------------

RUN_DIRECT=true
RUN_STEPPING=true
SELF_TEST=false
STRICT_MODE=false
EXPECTED_STATUS=""
EXPECTED_RECIPE=""
SPECIFIC_VERSIONS=()

while [ $# -gt 0 ]; do
    case "$1" in
        --direct-only)
            RUN_STEPPING=false
            shift
            ;;
        --stepping-only)
            RUN_DIRECT=false
            shift
            ;;
        --self-test)
            SELF_TEST=true
            RUN_DIRECT=false
            RUN_STEPPING=false
            shift
            ;;
        --strict)
            STRICT_MODE=true
            shift
            ;;
        --expect)
            if [ $# -lt 2 ]; then
                echo "ERROR: --expect requires AUTO or MANUAL" >&2
                exit 1
            fi
            EXPECTED_STATUS="$2"
            shift 2
            ;;
        --expect=*)
            EXPECTED_STATUS="${1#--expect=}"
            shift
            ;;
        --help|-h)
            head -30 "$0" | grep '^#' | sed 's/^# \?//'
            exit 0
            ;;
        *)
            SPECIFIC_VERSIONS+=("$1")
            RUN_STEPPING=false  # specific versions = direct only
            shift
            ;;
    esac
done

if $STRICT_MODE; then
    if $SELF_TEST || ! $RUN_DIRECT || $RUN_STEPPING || [ "${#SPECIFIC_VERSIONS[@]}" -ne 1 ]; then
        echo "ERROR: --strict requires exactly one direct historical version" >&2
        exit 1
    fi
    if [ "$EXPECTED_STATUS" != "AUTO" ] && [ "$EXPECTED_STATUS" != "MANUAL" ]; then
        echo "ERROR: --strict requires --expect AUTO or --expect MANUAL" >&2
        exit 1
    fi
    manifest_status=$(strict_expected_status "${SPECIFIC_VERSIONS[0]}") || {
        echo "ERROR: ${SPECIFIC_VERSIONS[0]} has no strict qualification manifest" >&2
        exit 1
    }
    if [ "$EXPECTED_STATUS" != "$manifest_status" ]; then
        echo "ERROR: --expect $EXPECTED_STATUS disagrees with manifest outcome $manifest_status" >&2
        exit 1
    fi
    EXPECTED_RECIPE=$(strict_expected_recipe "${SPECIFIC_VERSIONS[0]}") || {
        echo "ERROR: ${SPECIFIC_VERSIONS[0]} has no strict recipe manifest" >&2
        exit 1
    }
fi

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

CAND_BIN=$(build_candidate)
echo "Candidate: $CAND_BIN"
echo ""

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Migration Test Harness"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# Self-test: candidate as both source and target (validates the harness itself)
if $SELF_TEST; then
    echo ""
    echo -e "${BOLD}● Self-test: candidate → candidate${NC}"

    if ! WS=$(new_workspace); then
        echo -e "  ${RED}BLOCKED: could not create isolated workspace${NC}"
        record_result "candidate → candidate" "BLOCKED" "could not create isolated workspace"
    else
        migration_run_activate_workspace "$WS" ""
        SNAPSHOTS_DIR=""
        if ! SNAPSHOTS_DIR=$(mktemp -d /tmp/bd-snapshots-XXXXXX); then
            migration_run_record_result_after_cleanup \
                "$WS" "" "candidate → candidate" "BLOCKED" \
                "could not create isolated snapshot directory" || true
            echo -e "  ${RED}BLOCKED: ${RESULT_DETAILS[-1]}${NC}"
        # Init with candidate
        else
            migration_run_activate_workspace "$WS" "$SNAPSHOTS_DIR"
        fi
        if [ -n "$SNAPSHOTS_DIR" ] &&
            ! bd_in "$WS" "$CAND_BIN" init --quiet --non-interactive --prefix smoke </dev/null >/dev/null 2>&1; then
            migration_run_record_result_after_cleanup \
                "$WS" "$SNAPSHOTS_DIR" "candidate → candidate" \
                "BLOCKED" "init failed" || true
            echo -e "  ${RED}BLOCKED: ${RESULT_DETAILS[-1]}${NC}"
        elif [ -n "$SNAPSHOTS_DIR" ]; then
            git -C "$WS" config beads.role maintainer 2>/dev/null || true
            self_status="BLOCKED"
            self_detail="could not create test data"
            self_violations=0

            # Create dataset
            if create_dataset "$WS" "$CAND_BIN" "candidate"; then
                capture_snapshot "$WS" "$CAND_BIN" > "$SNAPSHOTS_DIR/before.json"
                before_count=$(jq 'length' "$SNAPSHOTS_DIR/before.json" 2>/dev/null) || before_count=0
                echo "  snapshot: $before_count items"

                # "Upgrade" is a no-op — just re-read with the same binary
                capture_snapshot "$WS" "$CAND_BIN" > "$SNAPSHOTS_DIR/after.json"

                violations=0
                check_fidelity "candidate" "$SNAPSHOTS_DIR/before.json" "$SNAPSHOTS_DIR/after.json" || violations=$?

                blocker_violations=0
                check_blocker_paths "$WS" "$CAND_BIN" || blocker_violations=$?
                violations=$((violations + blocker_violations))
                self_violations="$violations"

                if [ "$blocker_violations" -gt 0 ]; then
                    self_status="BLOCKED"
                    self_detail="self-test, $violations fidelity/blocker violations"
                else
                    self_status="AUTO"
                    self_detail="self-test, $violations fidelity violations"
                fi
            fi
            if ! migration_run_record_result_after_cleanup \
                "$WS" "$SNAPSHOTS_DIR" "candidate → candidate" \
                "$self_status" "$self_detail" "" "$self_violations"; then
                echo -e "  ${RED}BLOCKED: could not prove isolated workspace cleanup${NC}"
            fi
        fi
    fi
fi

# Direct paths
if $RUN_DIRECT; then
    local_paths=("${SPECIFIC_VERSIONS[@]:-${DIRECT_PATHS[@]}}")

    # Pre-download all binaries
    echo ""
    echo -e "${YELLOW}Downloading binaries for direct paths...${NC}"
    for v in "${local_paths[@]}"; do
        download_binary "$v" >/dev/null 2>&1 || echo -e "  ${YELLOW}no binary for $v${NC}"
    done

    for version in "${local_paths[@]}"; do
        test_direct_path "$version" "$CAND_BIN"
    done
fi

# Stepping-stone paths
if $RUN_STEPPING; then
    echo ""
    echo -e "${YELLOW}Downloading binaries for stepping-stone paths...${NC}"
    for path_spec in "${STEPPING_STONE_PATHS[@]}"; do
        IFS=',' read -ra vers <<< "$path_spec"
        for v in "${vers[@]}"; do
            download_binary "$v" >/dev/null 2>&1 || true
        done
    done

    for path_spec in "${STEPPING_STONE_PATHS[@]}"; do
        test_stepping_stone_path "$path_spec" "$CAND_BIN"
    done
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------

print_results_table
print_upgrade_instructions
print_ci_summary
print_summary_line

# Clean up candidate if we built it
if [ -z "${CANDIDATE_BIN:-}" ] && [ -f "$CAND_BIN" ]; then
    rm -f "$CAND_BIN"
fi

# Exit with failure only if any path is BLOCKED
if $STRICT_MODE; then
    strict_results_match "$EXPECTED_STATUS" "$EXPECTED_RECIPE" || exit 1
    exit 0
fi
for status in "${RESULT_STATUSES[@]}"; do
    if [ "$status" = "BLOCKED" ]; then
        exit 1
    fi
done
exit 0

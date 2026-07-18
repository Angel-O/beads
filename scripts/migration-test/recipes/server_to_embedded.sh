#!/bin/bash
# Recipe: qualified v0.55.4 legacy Dolt directory → current embedded Dolt.
#
# Other v0.50.0–v0.58.0 releases need release-specific extraction: v0.56.1
# has no export command, while v0.57/v0.58 exports omit comment bodies. Do not
# extend this recipe to those releases without a lossless fixture-backed path.
#
# Strategy:
#   1. Stop any running Dolt server
#   2. Export all data via old binary (which auto-starts its own server)
#   3. Stop server, clear stale metadata, init with candidate
#   4. If candidate DB is empty, reimport from JSONL export
#
# User-facing instructions:
#   Use the pinned migration harness for v0.55.4. It stops the historical
#   server, retains a byte-verified rollback tree, exports with the historical
#   binary, validates the JSONL, and only then initializes the candidate.

publish_legacy_dolt_rollback() {
    mv --no-target-directory --no-clobber --no-copy -- "$1" "$2"
}

remove_file_and_verify_absent() {
    local path="$1"
    if [ -e "$path" ] || [ -L "$path" ]; then
        rm -f -- "$path" || return 1
    fi
    [ ! -e "$path" ] && [ ! -L "$path" ]
}

remove_tree_and_verify_absent() {
    local path="$1"
    if [ -e "$path" ] || [ -L "$path" ]; then
        rm -rf -- "$path" || return 1
    fi
    [ ! -e "$path" ] && [ ! -L "$path" ]
}

migration_jsonl_id_inventory() {
    local path="$1"
    [ -f "$path" ] && [ ! -L "$path" ] || return 1
    jq -erRs '
        (split("\n") |
            if length > 0 and .[-1] == "" then .[:-1] else . end) as $lines |
        if ($lines | length) == 0 or any($lines[]; test("\\S") | not) then
            error("empty or blank JSONL record")
        else
            $lines | map(fromjson) |
            if all(.[];
                type == "object" and
                ((.id? | type) == "string") and
                ((.id? | length) > 0))
            then .[].id | @json
            else error("invalid issue record")
            end
        end
    ' "$path"
}

migration_jsonl_matches_snapshot() {
    local path="$1"
    local snapshot="$2"
    local export_ids expected_ids

    export_ids=$(migration_jsonl_id_inventory "$path" 2>/dev/null) || return 1
    [ -n "$export_ids" ] || return 1
    expected_ids=$(jq -er '
        if type == "array" and length > 0 and
            all(.[];
                type == "object" and
                ((.id? | type) == "string") and
                ((.id? | length) > 0))
        then .[].id | @json
        else error("invalid source snapshot")
        end
    ' "$snapshot" 2>/dev/null) || return 1
    [ -n "$expected_ids" ] || return 1

    [ -z "$(printf '%s\n' "$export_ids" | LC_ALL=C sort | uniq -d)" ] || return 1
    [ -z "$(printf '%s\n' "$expected_ids" | LC_ALL=C sort | uniq -d)" ] || return 1
    [ "$(printf '%s\n' "$export_ids" | LC_ALL=C sort)" = \
        "$(printf '%s\n' "$expected_ids" | LC_ALL=C sort)" ]
}

migration_jsonl_is_snapshot_subset() {
    local path="$1"
    local snapshot="$2"
    local existing_ids expected_ids extra_ids

    [ -f "$path" ] && [ ! -L "$path" ] || return 1
    [ -s "$path" ] || return 0
    existing_ids=$(migration_jsonl_id_inventory "$path" 2>/dev/null) || return 1
    expected_ids=$(jq -er '.[].id | @json' "$snapshot" 2>/dev/null) || return 1
    [ -z "$(printf '%s\n' "$existing_ids" | LC_ALL=C sort | uniq -d)" ] || return 1
    extra_ids=$(comm -23 \
        <(printf '%s\n' "$existing_ids" | LC_ALL=C sort) \
        <(printf '%s\n' "$expected_ids" | LC_ALL=C sort)) || return 1
    [ -z "$extra_ids" ]
}

preserve_legacy_dolt_source() {
    local ws="$1"
    local expected_manifest="$2"
    local beads_dir="$ws/.beads"
    local backup_dir="$beads_dir/legacy-dolt.pre-migration"
    local temp_dir="$beads_dir/.legacy-dolt.pre-migration.tmp.$$"
    local actual_manifest relative

    if [ -e "$backup_dir" ] || [ -L "$backup_dir" ]; then
        if [ ! -d "$backup_dir" ] || [ -L "$backup_dir" ]; then
            echo "  FAILED: legacy Dolt rollback destination is not a regular directory"
            return 1
        fi
        if ! verify_legacy_dolt_rollback_root "$backup_dir" "$expected_manifest"; then
            echo "  FAILED: legacy Dolt rollback destination contains different source data"
            return 1
        fi
        return 0
    fi

    if [ -e "$temp_dir" ] || [ -L "$temp_dir" ]; then
        echo "  FAILED: temporary legacy Dolt rollback destination already exists"
        return 1
    fi

    actual_manifest=$(legacy_dolt_artifact_manifest "$beads_dir") || return 1
    if [ "$actual_manifest" != "$expected_manifest" ]; then
        echo "  FAILED: active legacy Dolt source changed before backup"
        return 1
    fi

    mkdir "$temp_dir" || return 1
    if ! cp -a -- "$beads_dir/dolt" "$temp_dir/dolt"; then
        rm -rf "$temp_dir"
        echo "  FAILED: could not copy legacy Dolt rollback data"
        return 1
    fi
    for relative in "${LEGACY_DOLT_ROLLBACK_FILES[@]}"; do
        if [ -f "$beads_dir/$relative" ]; then
            if ! cp -p -- "$beads_dir/$relative" "$temp_dir/$relative"; then
                rm -rf "$temp_dir"
                echo "  FAILED: could not copy legacy Dolt rollback metadata"
                return 1
            fi
        fi
    done

    actual_manifest=$(legacy_dolt_artifact_manifest "$temp_dir") || {
        rm -rf "$temp_dir"
        return 1
    }
    if [ "$actual_manifest" != "$expected_manifest" ] || \
        ! verify_legacy_dolt_rollback_root "$temp_dir" "$expected_manifest"; then
        rm -rf "$temp_dir"
        echo "  FAILED: copied legacy Dolt rollback data failed verification"
        return 1
    fi

    if ! publish_legacy_dolt_rollback "$temp_dir" "$backup_dir"; then
        rm -rf "$temp_dir"
        echo "  FAILED: could not publish legacy Dolt rollback data"
        return 1
    fi
    if [ -e "$temp_dir" ] || [ -L "$temp_dir" ] || \
        ! verify_legacy_dolt_rollback_root "$backup_dir" "$expected_manifest"; then
        echo "  FAILED: published legacy Dolt rollback data failed verification"
        return 1
    fi
}

recipe_server_to_embedded() {
    local ws="$1"
    local old_bin="$2"
    local cand_bin="$3"
    local version="$4"
    local before_snapshot="${5:-}"
    local strategy
    local export_path="$ws/.beads/issues.jsonl"
    local existing_export_state="absent"
    local existing_export_checksum=""

    echo "  Trying server→embedded recipe..."
    strategy=$(server_bridge_strategy "$version") || {
        echo "  FAILED: no lossless server→embedded recipe is qualified for $version"
        return 1
    }
    if [ "$strategy" != "native_export" ] || [ ! -f "$before_snapshot" ]; then
        echo "  FAILED: incomplete server→embedded inputs for $version"
        return 1
    fi
    if [ -e "$export_path" ] || [ -L "$export_path" ]; then
        if [ -L "$export_path" ] || [ ! -f "$export_path" ] || \
            ! migration_jsonl_is_snapshot_subset "$export_path" "$before_snapshot"; then
            echo "  FAILED: existing historical JSONL is unsafe to replace"
            return 1
        fi
        existing_export_checksum=$(sha256_file "$export_path") || return 1
        existing_export_state="present"
    fi

    # Step 1: Stop any running server (we'll restart via old binary as needed)
    stop_dolt_server "$ws"

    # Preserve the stopped source before an old export command or candidate
    # probe can update its Dolt working set or metadata.
    local rollback_manifest
    rollback_manifest=$(legacy_dolt_artifact_manifest "$ws/.beads") || {
        echo "  FAILED: could not inventory legacy Dolt rollback source"
        return 1
    }
    preserve_legacy_dolt_source "$ws" "$rollback_manifest" || return 1

    # Step 2: Export data via old binary BEFORE clearing metadata.
    # The old binary needs metadata.json to know it's in server mode and
    # to auto-start its Dolt server. Removing metadata first (as was done
    # previously) makes the old binary unable to find its data. (GH#3071)
    echo "  exporting data via old binary..."
    local export_ok=false
    local export_tmp
    export_tmp=$(mktemp "$ws/.beads/.issues.jsonl.migration.tmp.XXXXXX") || {
        echo "  FAILED: could not create a safe historical export destination"
        return 1
    }
    if bd_in "$ws" "$old_bin" export --format jsonl \
        -o "$export_tmp" >/dev/null 2>&1; then
        local export_destination_unchanged=false
        if [ "$existing_export_state" = "present" ]; then
            if [ -f "$export_path" ] && [ ! -L "$export_path" ] && \
                [ "$(sha256_file "$export_path")" = "$existing_export_checksum" ]; then
                export_destination_unchanged=true
            fi
        elif [ ! -e "$export_path" ] && [ ! -L "$export_path" ]; then
            export_destination_unchanged=true
        fi
        if $export_destination_unchanged && \
            migration_jsonl_matches_snapshot "$export_tmp" "$before_snapshot" && \
            mv --no-target-directory --force -- "$export_tmp" "$export_path" && \
            migration_jsonl_matches_snapshot "$export_path" "$before_snapshot"; then
            local export_count
            export_count=$(jq -s 'length' "$export_path" 2>/dev/null) || export_count=0
            echo "  exported $export_count items to JSONL"
            export_ok=true
        fi
    fi
    if ! remove_file_and_verify_absent "$export_tmp"; then
        echo "  FAILED: could not remove the historical export staging file"
        return 1
    fi
    stop_dolt_server "$ws"
    if ! $export_ok; then
        echo "  FAILED: historical export did not produce a nonempty JSONL file"
        return 1
    fi
    if ! verify_legacy_dolt_rollback_root \
        "$ws/.beads/legacy-dolt.pre-migration" "$rollback_manifest"; then
        echo "  FAILED: retained rollback source changed during historical export"
        return 1
    fi

    # Step 3: Clear stale server metadata that causes TCP connect attempts,
    # then try candidate init.
    if ! remove_file_and_verify_absent "$ws/.beads/dolt-server.pid" || \
        ! remove_file_and_verify_absent "$ws/.beads/dolt-server.lock" || \
        ! remove_file_and_verify_absent "$ws/.beads/metadata.json"; then
        echo "  FAILED: could not clear legacy server metadata"
        return 1
    fi

    if bd_in "$ws" "$cand_bin" init --quiet --non-interactive </dev/null >/dev/null 2>&1; then
        # Verify candidate actually has data — init may succeed but create
        # an empty database if it didn't detect the old dolt/ data. (GH#3071)
        if candidate_list_has_nonempty_issue_ids "$ws" "$cand_bin"; then
            echo "  candidate init succeeded with data intact"
            return 0
        fi
        echo "  candidate init returned 0 but database is empty"
    fi

    # Step 4: Candidate init produced an empty DB (or failed).
    # Remove active storage only after the verified rollback copy and JSONL
    # export exist. The old dolt/ is legacy data; embeddeddolt/ (if any) was
    # just created empty by step 3.
    stop_dolt_server "$ws"
    if ! verify_legacy_dolt_rollback_root \
        "$ws/.beads/legacy-dolt.pre-migration" "$rollback_manifest"; then
        echo "  FAILED: retained rollback source changed before active-source removal"
        return 1
    fi
    if ! remove_file_and_verify_absent "$ws/.beads/metadata.json" || \
        ! remove_file_and_verify_absent "$ws/.beads/config.json" || \
        ! remove_file_and_verify_absent "$ws/.beads/config.yaml" || \
        ! remove_tree_and_verify_absent "$ws/.beads/dolt" || \
        ! remove_tree_and_verify_absent "$ws/.beads/embeddeddolt"; then
        echo "  FAILED: could not remove active legacy storage artifacts"
        return 1
    fi

    # Reimport from the JSONL export captured in step 2.
    if $export_ok && [ -s "$ws/.beads/issues.jsonl" ]; then
        echo "  reimporting from JSONL export..."
        if bd_in "$ws" "$cand_bin" init --from-jsonl --quiet --non-interactive </dev/null >/dev/null 2>&1; then
            echo "  candidate init --from-jsonl succeeded"
            return 0
        fi
        echo "  init --from-jsonl failed"
    else
        echo "  no JSONL export available for reimport"
    fi

    echo "  FAILED: could not migrate from server mode"
    return 1
}

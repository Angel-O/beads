#!/bin/bash
# Storage era definitions and upgrade path matrix.

# Representative versions for each storage era.
# Format: era_name|representative_version|storage_dir|description
ERAS=(
    "sqlite|v0.49.6|beads.db|SQLite era (pre-Dolt)"
    "dolt_server|v0.57.0|dolt|Dolt server mode"
    "embedded_old|v0.62.0|dolt|Old embedded Dolt (in-process)"
    "embedded_current|v0.63.3|embeddeddolt|Current embedded Dolt"
)

# Direct upgrade paths to test: source_version
DIRECT_PATHS=(
    "v0.49.6"
    "v0.57.0"
    "v0.62.0"
    "v0.63.3"
)

# Classic SQLite files that must be retained byte-for-byte for rollback before
# the manual bridge removes the active copies.
declare -ar CLASSIC_SQLITE_ROLLBACK_FILES=(
    "beads.db"
    "beads.db-wal"
    "beads.db-shm"
    "metadata.json"
    "config.json"
    "config.yaml"
)

# Release artifacts and expected qualification outcomes for strict CI lanes.
# Strict entries are deliberately explicit: adding a historical lane requires
# reviewing the official asset, checksum, source capabilities, and supported
# migration outcome instead of silently discovering them at runtime.
declare -Ar STRICT_RELEASE_ASSETS=(
    ["v0.49.6|linux|amd64"]="beads_0.49.6_linux_amd64.tar.gz"
)
declare -Ar STRICT_RELEASE_SHA256=(
    ["v0.49.6|linux|amd64"]="8546dc9a47e11dc31ac2bc9a0224a9c690975e91850932cbb62623053fbb7db8"
)
declare -Ar STRICT_EXPECTED_STATUSES=(
    ["v0.49.6"]="MANUAL"
)
declare -Ar STRICT_EXPECTED_RECIPES=(
    ["v0.49.6"]="sqlite_to_current"
)
declare -Ar STRICT_EXPECTED_FEATURES=(
    ["v0.49.6"]="epic task bug dependency standalone closed label comment"
)

strict_release_asset() {
    local value="${STRICT_RELEASE_ASSETS["$1|$2|$3"]:-}"
    [ -n "$value" ] || return 1
    printf '%s\n' "$value"
}

strict_release_sha256() {
    local value="${STRICT_RELEASE_SHA256["$1|$2|$3"]:-}"
    [ -n "$value" ] || return 1
    printf '%s\n' "$value"
}

strict_expected_status() {
    local value="${STRICT_EXPECTED_STATUSES[$1]:-}"
    [ -n "$value" ] || return 1
    printf '%s\n' "$value"
}

strict_expected_recipe() {
    local value="${STRICT_EXPECTED_RECIPES[$1]:-}"
    [ -n "$value" ] || return 1
    printf '%s\n' "$value"
}

strict_expected_features() {
    local value="${STRICT_EXPECTED_FEATURES[$1]:-}"
    [ -n "$value" ] || return 1
    printf '%s\n' "$value"
}

strict_fixture_has_expected_features() {
    local version="$1"
    shift
    local expected feature actual
    expected=$(strict_expected_features "$version") || return 1
    actual=" $* "
    for feature in $expected; do
        if [[ "$actual" != *" $feature "* ]]; then
            printf 'missing required source feature %s for %s\n' "$feature" "$version" >&2
            return 1
        fi
    done
}

# Stepping-stone paths: version1,version2,...,versionN
# Each version upgrades to the next, then finally to candidate.
#
# NOTE: Multi-hop paths through old releases are not a supported upgrade path.
# Old binaries (v0.57.0, v0.55.4, v0.63.3) have inherent bugs that cannot be
# patched retroactively. Users should always upgrade directly to the latest
# release — the direct paths above cover all supported eras.
STEPPING_STONE_PATHS=()

# Semver comparison: returns 0 if $1 <= $2
version_lte() {
    local v1="${1#v}"
    local v2="${2#v}"
    [ "$(printf '%s\n%s\n' "$v1" "$v2" | sort -V | head -1)" = "$v1" ]
}

# Semver comparison: returns 0 if $1 < $2
version_lt() {
    local v1="${1#v}"
    local v2="${2#v}"
    [ "$v1" != "$v2" ] && version_lte "$1" "$2"
}

# Returns the era name for a given version.
get_era() {
    local version="$1"
    if version_lt "$version" "v0.50.0"; then
        echo "sqlite"
    elif version_lt "$version" "v0.59.0"; then
        echo "dolt_server"
    elif version_lt "$version" "v0.63.3"; then
        echo "embedded_old"
    else
        echo "embedded_current"
    fi
}

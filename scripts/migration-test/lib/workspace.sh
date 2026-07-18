#!/bin/bash
# Workspace helpers — create isolated git repos, run bd commands, cleanup.

# NOTE: BEADS_TEST_MODE is intentionally NOT set to 1 here.
# Setting it disables Dolt server auto-start and forces port 1 in server-era
# versions (v0.50–v0.62), which makes every create/list command fail.
# The migration harness runs in isolated temp-dir workspaces, so there is no
# risk of polluting a production database.  Telemetry is opt-in (needs
# BD_OTEL_METRICS_URL) and prompts are avoided by piping </dev/null.
export BEADS_TEST_MODE="${BEADS_TEST_MODE:-0}"
export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL=/dev/null

# Timeout for bd operations (seconds). Prevents hangs from dolt server
# startup, embedded engine locks, etc.  Server-era versions may need the
# full 30 s for a cold Dolt auto-start.
BD_OP_TIMEOUT="${BD_OP_TIMEOUT:-30}"

migration_server_port_in_use() {
    local port="$1"
    local hex table status
    local inspected=false

    [[ "$port" =~ ^[0-9]+$ ]] || return 2
    hex=$(printf '%04X' "$port") || return 2
    for table in /proc/net/tcp /proc/net/tcp6; do
        [ -r "$table" ] || continue
        inspected=true
        if awk -v suffix=":$hex" '
            $4 == "0A" && substr($2, length($2) - length(suffix) + 1) == suffix {
                found = 1
            }
            END { exit(found ? 0 : 1) }
        ' "$table"; then
            return 0
        else
            status=$?
            [ "$status" -eq 1 ] || return 2
        fi
    done
    if ! $inspected; then
        command -v lsof >/dev/null 2>&1 || return 2
        if lsof -nP -iTCP:"$port" -sTCP:LISTEN -t >/dev/null 2>&1; then
            return 0
        else
            status=$?
            [ "$status" -eq 1 ] || return 2
        fi
    fi
    return 1
}

migration_port_reservation_root() {
    local root
    root="${TMPDIR:-/tmp}/bd-migration-server-ports-$(id -u)"
    if [ -L "$root" ]; then
        return 1
    fi
    if [ ! -d "$root" ]; then
        if ! mkdir -m 700 -- "$root" 2>/dev/null; then
            [ -d "$root" ] && [ ! -L "$root" ] || return 1
        fi
    fi
    [ -d "$root" ] && [ ! -L "$root" ] || return 1
    [ "$(stat -c '%u' -- "$root")" = "$(id -u)" ] || return 1
    chmod 700 -- "$root" || return 1
    printf '%s\n' "$root"
}

reap_stale_migration_port_reservations() {
    local root="$1"
    local lock owner_file owner port status stale

    [ -d "$root" ] && [ ! -L "$root" ] || return 1
    for lock in "$root"/2????; do
        [ -e "$lock" ] || continue
        [ -d "$lock" ] && [ ! -L "$lock" ] || continue
        port="${lock##*/}"
        owner_file="$lock/owner"
        owner=""
        if [ -f "$owner_file" ] && [ ! -L "$owner_file" ]; then
            owner=$(cat "$owner_file" 2>/dev/null) || owner=""
        fi
        if [[ "$owner" =~ ^[0-9]+$ ]] && kill -0 "$owner" 2>/dev/null; then
            continue
        fi
        if migration_server_port_in_use "$port"; then
            continue
        else
            status=$?
            [ "$status" -eq 1 ] || continue
        fi
        stale="$lock.stale.$BASHPID.$RANDOM"
        if mv --no-target-directory --no-clobber -- "$lock" "$stale" 2>/dev/null; then
            rm -rf -- "$stale"
        fi
    done
}

reserve_migration_server_port() {
    local ws="$1"
    local git_dir="$ws/.git"
    local port_file="$git_dir/bd-migration-server-port"
    local lock_file="$git_dir/bd-migration-server-port-lock"
    local root attempt port lock status

    [ -d "$git_dir" ] && [ ! -L "$git_dir" ] || return 1
    if [ -e "$port_file" ] || [ -L "$port_file" ] || \
        [ -e "$lock_file" ] || [ -L "$lock_file" ]; then
        return 1
    fi
    root=$(migration_port_reservation_root) || return 1
    reap_stale_migration_port_reservations "$root" || return 1

    for ((attempt = 0; attempt < 256; attempt++)); do
        port=$((20000 + ((BASHPID + RANDOM + attempt * 997) % 10000)))
        lock="$root/$port"
        if ! mkdir -- "$lock" 2>/dev/null; then
            continue
        fi
        if ! printf '%s\n' "$$" > "$lock/owner"; then
            rm -f -- "$lock/owner"
            rmdir -- "$lock" 2>/dev/null || true
            return 1
        fi
        if migration_server_port_in_use "$port"; then
            rm -f -- "$lock/owner"
            rmdir -- "$lock" || return 1
            continue
        else
            status=$?
            if [ "$status" -ne 1 ]; then
                rm -f -- "$lock/owner"
                rmdir -- "$lock" 2>/dev/null || true
                return 1
            fi
        fi
        if ! printf '%s\n' "$port" > "$port_file" || \
            ! printf '%s\n' "$lock" > "$lock_file"; then
            rm -f -- "$port_file" "$lock_file"
            rm -f -- "$lock/owner"
            rmdir -- "$lock" 2>/dev/null || true
            return 1
        fi
        return 0
    done
    return 1
}

release_migration_server_port() {
    local ws="$1"
    local git_dir="$ws/.git"
    local port_file="$git_dir/bd-migration-server-port"
    local lock_file="$git_dir/bd-migration-server-port-lock"
    local root port lock owner

    if [ ! -e "$port_file" ] && [ ! -L "$port_file" ] && \
        [ ! -e "$lock_file" ] && [ ! -L "$lock_file" ]; then
        return 0
    fi
    [ -f "$port_file" ] && [ ! -L "$port_file" ] || return 1
    [ -f "$lock_file" ] && [ ! -L "$lock_file" ] || return 1
    port=$(cat "$port_file") || return 1
    lock=$(cat "$lock_file") || return 1
    [[ "$port" =~ ^2[0-9]{4}$ ]] || return 1
    root=$(migration_port_reservation_root) || return 1
    [ "$lock" = "$root/$port" ] || return 1
    [ -d "$lock" ] && [ ! -L "$lock" ] || return 1
    [ -f "$lock/owner" ] && [ ! -L "$lock/owner" ] || return 1
    owner=$(cat "$lock/owner") || return 1
    [ "$owner" = "$$" ] || return 1
    rm -f -- "$lock/owner" || return 1
    rmdir -- "$lock" || return 1
    rm -f -- "$port_file" "$lock_file" || return 1
    [ ! -e "$port_file" ] && [ ! -L "$port_file" ] && \
        [ ! -e "$lock_file" ] && [ ! -L "$lock_file" ]
}

prepare_migration_subprocess_home() {
    local ws="$1"
    local private_root home dir

    [ -d "$ws" ] && [ ! -L "$ws" ] || return 1
    [ "$(stat -c '%u' -- "$ws")" = "$(id -u)" ] || return 1
    if [ -d "$ws/.git" ] && [ ! -L "$ws/.git" ]; then
        private_root="$ws/.git"
    else
        private_root="$ws"
    fi
    home="$private_root/bd-migration-home"
    for dir in \
        "$home" "$home/.config" "$home/.cache" "$home/.local" \
        "$home/.local/share" "$home/.local/state" \
        "$private_root/bd-migration-tmp" "$private_root/bd-migration-runtime"; do
        if [ -e "$dir" ] || [ -L "$dir" ]; then
            [ -d "$dir" ] && [ ! -L "$dir" ] || return 1
            [ "$(stat -c '%u' -- "$dir")" = "$(id -u)" ] || return 1
        else
            mkdir -m 700 -- "$dir" || return 1
        fi
        chmod 700 -- "$dir" || return 1
    done
    printf '%s\n' "$home"
}

new_workspace() (
    local dir
    local env_name

    while IFS= read -r env_name; do
        case "$env_name" in
            GIT_*) unset "$env_name" ;;
        esac
    done < <(compgen -e)
    export GIT_CONFIG_NOSYSTEM=1
    export GIT_CONFIG_GLOBAL=/dev/null
    export GIT_TERMINAL_PROMPT=0

    dir=$(mktemp -d /tmp/bd-migration-XXXXXX) || return 1
    if ! git -C "$dir" init --quiet || \
        ! git -C "$dir" config core.hooksPath .git/hooks || \
        ! git -C "$dir" config user.name "migration-test" || \
        ! git -C "$dir" config user.email "test@beads.test" || \
        ! touch "$dir/.gitkeep" || \
        ! git -C "$dir" add . || \
        ! git -C "$dir" commit --quiet -m "initial"; then
        rm -rf -- "$dir"
        return 1
    fi
    if ! reserve_migration_server_port "$dir"; then
        rm -rf -- "$dir"
        return 1
    fi
    if ! prepare_migration_subprocess_home "$dir" >/dev/null; then
        release_migration_server_port "$dir" || true
        rm -rf -- "$dir"
        return 1
    fi
    echo "$dir"
)

bd_in() {
    local ws="$1"
    local bin="$2"
    local port_file="$ws/.git/bd-migration-server-port"
    local port=""
    local subprocess_home private_root env_name
    local -a clean_env
    shift 2
    if [ -e "$port_file" ] || [ -L "$port_file" ]; then
        [ -f "$port_file" ] && [ ! -L "$port_file" ] || return 1
        port=$(cat "$port_file") || return 1
        [[ "$port" =~ ^2[0-9]{4}$ ]] || return 1
    fi
    subprocess_home=$(prepare_migration_subprocess_home "$ws") || return 1
    if [ -d "$ws/.git" ] && [ ! -L "$ws/.git" ]; then
        private_root="$ws/.git"
    else
        private_root="$ws"
    fi
    clean_env=(
        env -i
        "PATH=$PATH"
        "HOME=$subprocess_home"
        "XDG_CONFIG_HOME=$subprocess_home/.config"
        "XDG_CACHE_HOME=$subprocess_home/.cache"
        "XDG_DATA_HOME=$subprocess_home/.local/share"
        "XDG_STATE_HOME=$subprocess_home/.local/state"
        "XDG_RUNTIME_DIR=$private_root/bd-migration-runtime"
        "TMPDIR=$private_root/bd-migration-tmp"
        "GIT_CONFIG_NOSYSTEM=1"
        "GIT_CONFIG_GLOBAL=/dev/null"
        "GIT_TERMINAL_PROMPT=0"
        "BEADS_TEST_MODE=0"
        "BEADS_NO_DAEMON=1"
        "BEADS_DOLT_AUTO_START=1"
        "BD_NON_INTERACTIVE=1"
        "BD_NO_PAGER=1"
        "BD_DISABLE_METRICS=1"
        "BD_DISABLE_EVENT_FLUSH=1"
        "NO_COLOR=1"
        "CI=1"
        "USER=migration-test"
        "LOGNAME=migration-test"
        "SHELL=/bin/bash"
        "LANG=C.UTF-8"
        "LC_ALL=C.UTF-8"
        "TZ=UTC"
        "TERM=dumb"
    )
    if [ -n "$port" ]; then
        clean_env+=("BEADS_DOLT_SERVER_PORT=$port")
    fi
    for env_name in \
        BLOCKER_FAIL_COMMAND BLOCKER_STATE LEGACY_RACE_CALLS \
        PROBE_FLOW_LOG PROBE_FLOW_MODE PROBE_FLOW_STATE \
        PROBE_LIST_EXIT PROBE_LIST_OUTPUT \
        SERVER_EXPORT_CALLS SERVER_EXPORT_FAIL_CANDIDATE_CALLS \
        SNAPSHOT_FAIL_COMMAND SNAPSHOT_LIST_SHAPE \
        V057_CANDIDATE_CALLS V057_MODE V057_OLD_CALLS; do
        if [[ -v "$env_name" ]]; then
            clean_env+=("$env_name=${!env_name}")
        fi
    done
    (cd "$ws" && "${clean_env[@]}" timeout "$BD_OP_TIMEOUT" "$bin" "$@")
}

# Create an issue, returning just the ID on stdout.
# Tries --silent first, falls back to parsing output.
bd_create() {
    local ws="$1"
    local bin="$2"
    shift 2
    local id
    id=$(bd_in "$ws" "$bin" create --silent "$@" 2>/dev/null) && [ -n "$id" ] && echo "$id" && return 0
    id=$(bd_in "$ws" "$bin" create "$@" 2>&1 | grep -oP 'Created issue: \K\S+' || true)
    [ -n "$id" ] && echo "$id" && return 0
    return 1
}

migration_workspace_owned_by_harness() {
    local ws="$1"
    local git_dir port_file lock_file root port lock owner

    [[ "$ws" =~ ^/tmp/bd-migration-[[:alnum:]]{6}$ ]] || return 1
    [ -d "$ws" ] && [ ! -L "$ws" ] || return 1
    [ "$(stat -c '%u' -- "$ws")" = "$(id -u)" ] || return 1

    git_dir="$ws/.git"
    port_file="$git_dir/bd-migration-server-port"
    lock_file="$git_dir/bd-migration-server-port-lock"
    [ -d "$git_dir" ] && [ ! -L "$git_dir" ] || return 1
    [ -f "$port_file" ] && [ ! -L "$port_file" ] || return 1
    [ -f "$lock_file" ] && [ ! -L "$lock_file" ] || return 1

    port=$(cat "$port_file") || return 1
    lock=$(cat "$lock_file") || return 1
    [[ "$port" =~ ^2[0-9]{4}$ ]] || return 1
    root="${TMPDIR:-/tmp}/bd-migration-server-ports-$(id -u)"
    [ -d "$root" ] && [ ! -L "$root" ] || return 1
    [ "$(stat -c '%u' -- "$root")" = "$(id -u)" ] || return 1
    [ "$lock" = "$root/$port" ] || return 1
    [ -d "$lock" ] && [ ! -L "$lock" ] || return 1
    [ -f "$lock/owner" ] && [ ! -L "$lock/owner" ] || return 1
    owner=$(cat "$lock/owner") || return 1
    [ "$owner" = "$$" ]
}

migration_process_command_line() {
    local pid="$1"

    if [ -r "/proc/$pid/cmdline" ]; then
        tr '\0' ' ' < "/proc/$pid/cmdline"
        return
    fi
    ps -p "$pid" -o command= 2>/dev/null
}

migration_process_cwd() {
    local pid="$1"

    if [ -L "/proc/$pid/cwd" ]; then
        readlink "/proc/$pid/cwd"
        return
    fi
    command -v lsof >/dev/null 2>&1 || return 1
    lsof -a -p "$pid" -d cwd -Fn 2>/dev/null | sed -n 's/^n//p' | head -1
}

migration_pid_belongs_to_workspace() {
    local pid="$1"
    local ws="$2"
    local pidfile_name="$3"
    local command_line="" cwd="" dolt_root=""

    [[ "$pid" =~ ^[0-9]+$ ]] && [ "$pid" -gt 1 ] || return 1
    command_line=$(migration_process_command_line "$pid") || return 1
    [ -n "$command_line" ] || return 1

    case "$pidfile_name" in
        dolt-server.pid)
            [[ "$command_line" == *dolt* ]] && [[ "$command_line" == *sql-server* ]] || return 1
            cwd=$(migration_process_cwd "$pid") || return 1
            dolt_root=$(cd -P -- "$ws/.beads/dolt" 2>/dev/null && pwd) || return 1
            [ "$cwd" = "$dolt_root" ] || [[ "$cwd" == "$dolt_root/"* ]]
            ;;
        dolt-monitor.pid)
            [[ "$command_line" == *bd* ]] && [[ "$command_line" == *idle-monitor* ]] &&
                [[ "$command_line" == *"$ws"* ]]
            ;;
        daemon.pid)
            [[ "$command_line" == *bd* ]] && [[ "$command_line" == *daemon* ]] &&
                [[ "$command_line" == *"$ws"* ]]
            ;;
        *)
            return 1
            ;;
    esac
}

stop_dolt_server() {
    local ws="$1"
    local pid="" pidfile_name="" port="" status=0
    migration_workspace_owned_by_harness "$ws" || return 1
    for pidfile in "$ws/.beads/dolt-monitor.pid" "$ws/.beads/daemon.pid" "$ws/.beads/dolt-server.pid"; do
        if [ -f "$pidfile" ] && [ ! -L "$pidfile" ]; then
            pid=$(cat "$pidfile" 2>/dev/null) || true
            pidfile_name="${pidfile##*/}"
            if migration_pid_belongs_to_workspace "$pid" "$ws" "$pidfile_name"; then
                kill -9 -- "$pid" 2>/dev/null || true
            fi
        fi
    done
    sleep 1
    port=$(cat "$ws/.git/bd-migration-server-port") || return 1
    if migration_server_port_in_use "$port"; then
        return 1
    else
        status=$?
        [ "$status" -eq 1 ] || return 1
    fi
    rm -f "$ws/.beads/bd.sock" "$ws/.beads/dolt-server.lock" 2>/dev/null || true
}

cleanup_workspace() {
    local ws="$1"
    migration_workspace_owned_by_harness "$ws" || return 1
    stop_dolt_server "$ws" || return 1
    release_migration_server_port "$ws" || return 1
    rm -rf -- "$ws"
}

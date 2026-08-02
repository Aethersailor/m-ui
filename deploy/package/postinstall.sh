#!/bin/sh

set -eu
umask 077

backup_dir=/var/lib/m-ui-package-backups
transaction_dir=/run/m-ui-package
snapshot_state="$transaction_dir/package-backup-snapshot"
backup_snapshot=""

root_only_directory() {
    directory="$1"
    [ -d "$directory" ] &&
        [ ! -L "$directory" ] &&
        [ "$(stat -c '%u:%g:%a' "$directory" 2>/dev/null)" = "0:0:700" ]
}

safe_backup_entry() {
    snapshot="$1"
    path="$2"
    source="$snapshot$path"
    if [ ! -e "$source" ] && [ ! -L "$source" ]; then
        return 0
    fi
    [ ! -L "$source" ] || return 1
    [ "$(stat -c '%u:%g' "$source" 2>/dev/null)" = "0:0" ] || return 1
    case "$path" in
        /usr/lib/m-ui|/usr/share/m-ui)
            [ -d "$source" ] || return 1
            ;;
        *)
            [ -f "$source" ] || return 1
            ;;
    esac
    unsafe_entry="$(find "$source" \
        \( -type l -o -type b -o -type c -o -type p -o -type s \) \
        -print -quit 2>/dev/null)"
    [ -z "$unsafe_entry" ]
}

safe_backup_snapshot() {
    snapshot="$1"
    root_only_directory "$snapshot" || return 1
    tree_entry="$(find "$snapshot" -print -quit 2>/dev/null)"
    [ "$tree_entry" = "$snapshot" ] || return 1
    while IFS= read -r tree_entry; do
        [ -L "$tree_entry" ] && return 1
        [ -f "$tree_entry" ] || [ -d "$tree_entry" ] || return 1
        [ "$(stat -c '%u:%g' "$tree_entry" 2>/dev/null)" = "0:0" ] ||
            return 1
    done <<EOF
$(find "$snapshot" -print 2>/dev/null)
EOF
    for path in /usr/bin/m-ui /usr/lib/m-ui /usr/share/m-ui \
        /etc/systemd/system/m-ui.service /etc/systemd/system/mihomo.service \
        /etc/sudoers.d/m-ui /etc/init.d/m-ui /etc/init.d/mihomo \
        /etc/doas.d/m-ui.conf
    do
        safe_backup_entry "$snapshot" "$path" || return 1
    done
}

read_snapshot_state() {
    root_only_directory "$backup_dir" || return 1
    root_only_directory "$transaction_dir" || return 1
    [ -f "$snapshot_state" ] && [ ! -L "$snapshot_state" ] || return 1
    [ "$(stat -c '%u:%g:%a' "$snapshot_state" 2>/dev/null)" = "0:0:600" ] ||
        return 1
    seen_snapshot=0
    while IFS='=' read -r key value || [ -n "${key:-}" ]; do
        [ "$key" = snapshot ] || return 1
        [ "$seen_snapshot" -eq 0 ] || return 1
        seen_snapshot=1
        case "$value" in
            "$backup_dir"/snapshot.*) ;;
            *) return 1 ;;
        esac
        snapshot_name="${value#"$backup_dir"/}"
        [ "$value" = "$backup_dir/$snapshot_name" ] || return 1
        case "$snapshot_name" in
            snapshot.*) ;;
            *) return 1 ;;
        esac
        [ -n "${snapshot_name#snapshot.}" ] || return 1
        case "$snapshot_name" in
            *..*|*[!A-Za-z0-9_.-]*) return 1 ;;
        esac
        backup_snapshot="$value"
    done <"$snapshot_state"
    [ "$seen_snapshot" -eq 1 ] || return 1
    safe_backup_snapshot "$backup_snapshot"
}

if [ -e "$snapshot_state" ] || [ -L "$snapshot_state" ]; then
    if ! read_snapshot_state; then
        printf '%s\n' \
            "refusing to restore from an invalid package snapshot handoff" >&2
        exit 1
    fi
fi

restore_previous_programs() {
    [ -n "$backup_snapshot" ] || return 0
    safe_backup_snapshot "$backup_snapshot" || return 1
    for path in /usr/bin/m-ui /usr/lib/m-ui /usr/share/m-ui \
        /etc/systemd/system/m-ui.service /etc/systemd/system/mihomo.service \
        /etc/sudoers.d/m-ui /etc/init.d/m-ui /etc/init.d/mihomo \
        /etc/doas.d/m-ui.conf
    do
        rm -rf "$path" || return 1
        if [ -e "$backup_snapshot$path" ] ||
            [ -L "$backup_snapshot$path" ]; then
            mkdir -p "$(dirname "$path")" || return 1
            cp -a "$backup_snapshot$path" "$path" || return 1
        fi
    done
    if [ -d /run/systemd/system ]; then
        systemctl daemon-reload >/dev/null 2>&1 || return 1
    fi
}

rollback_on_failure() {
    status="$?"
    if [ "$status" -ne 0 ]; then
        if ! restore_previous_programs; then
            printf '%s\n' "automatic package rollback failed" >&2
            exit 1
        fi
    fi
    exit "$status"
}
trap rollback_on_failure EXIT

create_account() {
    account="$1"
    home="$2"
    if getent passwd "$account" >/dev/null 2>&1; then
        if [ -f /etc/alpine-release ] &&
            ! getent group "$account" >/dev/null 2>&1
        then
            addgroup -S "$account"
        fi
        return
    fi
    if [ -f /etc/alpine-release ]; then
        if ! getent group "$account" >/dev/null 2>&1; then
            addgroup -S "$account"
        fi
        adduser -S -D -H -G "$account" -h "$home" \
            -s /sbin/nologin "$account"
    elif command -v useradd >/dev/null 2>&1; then
        useradd --system --user-group --home-dir "$home" \
            --shell /usr/sbin/nologin "$account"
    else
        adduser --system --group --home "$home" \
            --no-create-home --shell /usr/sbin/nologin "$account"
    fi
}

create_account m-ui /var/lib/m-ui
create_account mihomo /var/lib/mihomo
if [ -f /etc/alpine-release ]; then
    if ! id -nG m-ui | tr ' ' '\n' | grep -qx mihomo; then
        addgroup m-ui mihomo
    fi
else
    usermod -a -G mihomo m-ui
fi
install -d -o root -g m-ui -m 0750 /etc/m-ui
install -d -o m-ui -g mihomo -m 2750 /etc/mihomo
install -d -o m-ui -g mihomo -m 2710 /var/lib/m-ui
install -d -o m-ui -g m-ui -m 0700 /var/lib/m-ui/revisions
install -d -o m-ui -g mihomo -m 2710 /var/lib/m-ui/core
install -d -o m-ui -g mihomo -m 2750 \
    /var/lib/m-ui/core/staging /var/lib/m-ui/core/backups
install -d -o mihomo -g mihomo -m 0750 /var/lib/mihomo

master_key="/var/lib/m-ui/master.key"
config_path="/etc/m-ui/config.toml"
mihomo_config="/etc/mihomo/config.yaml"
database_was_present=0
if [ -f /var/lib/m-ui/m-ui.db ]; then
    database_was_present=1
fi
if [ ! -f "$master_key" ]; then
    dd if=/dev/urandom of="$master_key" bs=32 count=1 status=none
    chmod 0600 "$master_key"
fi
chown m-ui:m-ui "$master_key"

if [ ! -f "$config_path" ]; then
    controller_secret="$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')"
    cat >"$config_path" <<EOF
[server]
listen_address = "127.0.0.1"
port = 2095
read_header_timeout = "5s"
shutdown_timeout = "10s"

[logging]
level = "info"
format = "text"

[storage]
database_path = "/var/lib/m-ui/m-ui.db"
master_key_path = "/var/lib/m-ui/master.key"

[security]
session_ttl = "12h"
cookie_secure = false

[panel]
title = "m-ui"
ui_language = "en-US"
public_host = "localhost"

[mihomo]
binary_path = "/var/lib/m-ui/core/current/mihomo"
managed_core = true
process_mode = "auto"
config_directory = "/etc/mihomo"
config_path = "/etc/mihomo/config.yaml"
external_controller_address = "127.0.0.1:9090"
controller_connect_address = "127.0.0.1:9090"
external_controller_cors_origins = []
controller_secret = "$controller_secret"
service_name = "mihomo.service"
revision_directory = "/var/lib/m-ui/revisions"
history_limit = 20
EOF
    chmod 0640 "$config_path"
fi
chown root:m-ui "$config_path"

if [ ! -f "$mihomo_config" ]; then
    controller_secret="$(sed -n \
        's/^controller_secret = "\(.*\)"$/\1/p' "$config_path")"
    cat >"$mihomo_config" <<EOF
mode: rule
log-level: info
ipv6: true
external-controller: 127.0.0.1:9090
secret: $controller_secret
listeners: []
rules:
  - MATCH,DIRECT
EOF
    chmod 0640 "$mihomo_config"
fi
chown m-ui:mihomo "$mihomo_config"

needs_bootstrap=1
if [ "$database_was_present" -eq 1 ] &&
    [ -x /var/lib/m-ui/core/current/mihomo ] &&
    [ -s /var/lib/m-ui/core/current/manifest.json ]
then
    if command -v runuser >/dev/null 2>&1; then
        if runuser -u m-ui -- /usr/bin/m-ui core status --json \
            --config "$config_path" >/dev/null 2>&1
        then
            needs_bootstrap=0
        fi
    elif su-exec m-ui /usr/bin/m-ui core status --json \
        --config "$config_path" >/dev/null 2>&1
    then
        needs_bootstrap=0
    fi
fi
if [ "$needs_bootstrap" -eq 1 ]; then
    if command -v runuser >/dev/null 2>&1; then
        runuser -u m-ui -- /usr/bin/m-ui core bootstrap \
            --config "$config_path" \
            --binary /usr/lib/m-ui/bootstrap/mihomo \
            --manifest /usr/share/m-ui/bootstrap/manifest.json
    else
        su-exec m-ui /usr/bin/m-ui core bootstrap \
            --config "$config_path" \
            --binary /usr/lib/m-ui/bootstrap/mihomo \
            --manifest /usr/share/m-ui/bootstrap/manifest.json
    fi
fi

if [ "$database_was_present" -eq 0 ] &&
    [ "${M_UI_SKIP_ADMIN_INIT:-0}" != "1" ]
then
    password_file="/var/lib/m-ui/.initial-admin-password"
    initial_password="$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
    printf '%s\n' "$initial_password" >"$password_file"
    chown m-ui:m-ui "$password_file"
    chmod 0600 "$password_file"
    if command -v runuser >/dev/null 2>&1; then
        runuser -u m-ui -- /usr/bin/m-ui admin reset-password \
            --config "$config_path" \
            --username admin \
            --password-file "$password_file"
    else
        su-exec m-ui /usr/bin/m-ui admin reset-password \
            --config "$config_path" \
            --username admin \
            --password-file "$password_file"
    fi
    rm -f "$password_file"
    printf '%s\n' \
        "Initial administrator: admin" \
        "One-time initial password: $initial_password"
fi

temporary_config="${config_path}.tmp"
sed 's/^controller_secret = ".*"$/controller_secret = ""/' \
    "$config_path" >"$temporary_config"
chown root:m-ui "$temporary_config"
chmod 0640 "$temporary_config"
mv "$temporary_config" "$config_path"

run_as_m_ui() {
    if command -v runuser >/dev/null 2>&1; then
        runuser -u m-ui -- "$@"
    elif command -v su-exec >/dev/null 2>&1; then
        su-exec m-ui "$@"
    else
        printf '%s\n' \
            "runuser or su-exec is required for the m-ui runtime boundary" >&2
        return 1
    fi
}

check_panel_health() {
    run_as_m_ui /usr/bin/m-ui doctor panel \
        --config /etc/m-ui/config.toml >/dev/null
}

apply_mihomo_start_boundary() {
    run_as_m_ui /usr/bin/m-ui runtime apply-mihomo-start \
        --config /etc/m-ui/config.toml
}

if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
elif command -v rc-update >/dev/null 2>&1; then
    chmod 0755 /etc/init.d/m-ui /etc/init.d/mihomo
fi

state_dir=/run/m-ui-package
state_file="$state_dir/package-upgrade-services"

read_service_state() {
    root_only_directory "$state_dir" || return 1
    [ -f "$state_file" ] && [ ! -L "$state_file" ] || return 1
    [ "$(stat -c '%u:%g:%a' "$state_file" 2>/dev/null)" = "0:0:600" ] || return 1

    m_ui_enabled=0
    m_ui_active=0
    mihomo_enabled=0
    mihomo_active=0
    seen_m_ui_enabled=0
    seen_m_ui_active=0
    seen_mihomo_enabled=0
    seen_mihomo_active=0
    while IFS='=' read -r key value || [ -n "${key:-}" ]; do
        case "$key" in
            m_ui_enabled)
                [ "$seen_m_ui_enabled" -eq 0 ] || return 1
                seen_m_ui_enabled=1
                ;;
            m_ui_active)
                [ "$seen_m_ui_active" -eq 0 ] || return 1
                seen_m_ui_active=1
                ;;
            mihomo_enabled)
                [ "$seen_mihomo_enabled" -eq 0 ] || return 1
                seen_mihomo_enabled=1
                ;;
            mihomo_active)
                [ "$seen_mihomo_active" -eq 0 ] || return 1
                seen_mihomo_active=1
                ;;
            *)
                return 1
                ;;
        esac
        case "$value" in
            0|1) ;;
            *) return 1 ;;
        esac
        case "$key" in
            m_ui_enabled) m_ui_enabled="$value" ;;
            m_ui_active) m_ui_active="$value" ;;
            mihomo_enabled) mihomo_enabled="$value" ;;
            mihomo_active) mihomo_active="$value" ;;
        esac
    done <"$state_file"
    [ "$seen_m_ui_enabled" -eq 1 ] &&
        [ "$seen_m_ui_active" -eq 1 ] &&
        [ "$seen_mihomo_enabled" -eq 1 ] &&
    [ "$seen_mihomo_active" -eq 1 ]
}

if [ -e "$state_file" ] || [ -L "$state_file" ]; then
    if ! read_service_state; then
        printf '%s\n' \
            "refusing to restore service state from an invalid root-owned file" >&2
        exit 1
    fi
    if [ -d /run/systemd/system ]; then
        if [ "${mihomo_enabled:-0}" -eq 1 ]; then
            systemctl enable mihomo.service >/dev/null 2>&1
        else
            systemctl disable mihomo.service >/dev/null 2>&1
        fi
        if [ "${m_ui_enabled:-0}" -eq 1 ]; then
            systemctl enable m-ui.service >/dev/null 2>&1
        else
            systemctl disable m-ui.service >/dev/null 2>&1
        fi
        if [ "${mihomo_active:-0}" -eq 1 ]; then
            # m-ui owns the durable endpoint boundary. Start it first even
            # when the old installation had only Mihomo active. Verify the
            # active panel endpoint before applying Mihomo's boundary.
            systemctl start m-ui.service
            check_panel_health
            apply_mihomo_start_boundary
        elif [ "${m_ui_active:-0}" -eq 1 ]; then
            systemctl start m-ui.service
            check_panel_health
        fi
    elif command -v rc-update >/dev/null 2>&1; then
        if [ "${mihomo_enabled:-0}" -eq 1 ]; then
            rc-update add mihomo default >/dev/null 2>&1
        else
            rc-update del mihomo default >/dev/null 2>&1
        fi
        if [ "${m_ui_enabled:-0}" -eq 1 ]; then
            rc-update add m-ui default >/dev/null 2>&1
        else
            rc-update del m-ui default >/dev/null 2>&1
        fi
        if [ "${mihomo_active:-0}" -eq 1 ]; then
            rc-service m-ui start
            check_panel_health
            apply_mihomo_start_boundary
        elif [ "${m_ui_active:-0}" -eq 1 ]; then
            rc-service m-ui start
            check_panel_health
        fi
    fi
fi

prune_completed_snapshots() {
    [ -n "$backup_snapshot" ] || return 0
    for candidate in "$backup_dir"/snapshot.*; do
        [ -e "$candidate" ] || [ -L "$candidate" ] || continue
        [ "$candidate" = "$backup_snapshot" ] && continue
        root_only_directory "$candidate" || return 1
        rm -rf -- "$candidate" || return 1
    done
}

commit_installation() {
    # All service starts, endpoint health checks, and state restoration have
    # succeeded before this point. From here on package cleanup is committed
    # maintenance: a leftover handoff is safer than rolling files back while
    # the newly verified services continue running.
    trap - EXIT
    if [ -e "$state_file" ] || [ -L "$state_file" ]; then
        if ! rm -f "$state_file"; then
            printf '%s\n' "warning: could not remove service-state handoff" >&2
        fi
    fi
    if [ -e "$snapshot_state" ] || [ -L "$snapshot_state" ]; then
        if ! rm -f "$snapshot_state"; then
            printf '%s\n' "warning: could not remove snapshot handoff" >&2
        fi
    fi
    if ! prune_completed_snapshots; then
        printf '%s\n' "warning: could not prune completed package snapshots" >&2
    fi
}

commit_installation

printf '%s\n' \
    "m-ui installed with a verified Mihomo bootstrap." \
    "Services are not enabled automatically." \
    "Run: systemctl enable --now mihomo.service m-ui.service" \
    "or: rc-update add mihomo default && rc-update add m-ui default"

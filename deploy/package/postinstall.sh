#!/bin/sh

set -eu
umask 077

backup_snapshot=""
if [ -d /var/lib/m-ui/package-backups ]; then
    backup_snapshot="$(find /var/lib/m-ui/package-backups \
        -mindepth 1 -maxdepth 1 -type d -print | sort | tail -n 1)"
fi

restore_previous_programs() {
    [ -n "$backup_snapshot" ] || return 0
    [ -d "$backup_snapshot" ] || return 0
    set +e
    for path in /usr/bin/m-ui /usr/lib/m-ui /usr/share/m-ui \
        /etc/systemd/system/m-ui.service /etc/systemd/system/mihomo.service \
        /etc/sudoers.d/m-ui /etc/init.d/m-ui /etc/init.d/mihomo \
        /etc/doas.d/m-ui.conf
    do
        rm -rf "$path"
        if [ -e "$backup_snapshot$path" ]; then
            mkdir -p "$(dirname "$path")"
            cp -a "$backup_snapshot$path" "$path"
        fi
    done
    if [ -d /run/systemd/system ]; then
        systemctl daemon-reload >/dev/null 2>&1 || true
    fi
    set -e
}

rollback_on_failure() {
    status="$?"
    if [ "$status" -ne 0 ]; then
        restore_previous_programs
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
controller_address = "127.0.0.1:9090"
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

if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
elif command -v rc-update >/dev/null 2>&1; then
    chmod 0755 /etc/init.d/m-ui /etc/init.d/mihomo
fi

printf '%s\n' \
    "m-ui installed with a verified Mihomo bootstrap." \
    "Services are not enabled automatically." \
    "Run: systemctl enable --now mihomo.service m-ui.service" \
    "or: rc-update add mihomo default && rc-update add m-ui default"

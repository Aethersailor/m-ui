#!/bin/sh

set -eu

umask 077

if [ "${M_UI_INIT_DATA:-0}" = "1" ]; then
    [ "$(id -u)" -eq 0 ] || {
        echo "data initialization must run as root" >&2
        exit 1
    }
    data_root=/data
    [ -d "$data_root" ] && [ ! -L "$data_root" ] || {
        echo "refusing unsafe persistence root: $data_root" >&2
        exit 1
    }

    ensure_directory() {
        directory="$1"
        mode="$2"
        owner="$3"
        if [ -L "$directory" ] || { [ -e "$directory" ] && [ ! -d "$directory" ]; }; then
            echo "refusing unsafe persistence path: $directory" >&2
            exit 1
        fi
        if [ ! -d "$directory" ]; then
            mkdir "$directory"
        fi
        chown "$owner" "$directory"
        chmod "$mode" "$directory"
    }

    chmod 0711 "$data_root"
    ensure_directory /data/etc 0711 0:0
    ensure_directory /data/var 0711 0:0
    ensure_directory /data/var/lib 0711 0:0
    ensure_directory /data/etc/m-ui 0750 10001:10001
    ensure_directory /data/etc/mihomo 0750 10001:10001
    ensure_directory /data/var/lib/m-ui 0700 10001:10001
    ensure_directory /data/var/lib/mihomo 0750 10001:10001

    safe_tree() {
        tree="$1"
        [ -d "$tree" ] && [ ! -L "$tree" ] || {
            echo "refusing unsafe persistence path: $tree" >&2
            exit 1
        }
        if find "$tree" \( -type l -o -type b -o -type c -o -type p -o -type s \) \
            -print -quit | grep -q .; then
            echo "refusing symlink or special file below: $tree" >&2
            exit 1
        fi
        find "$tree" -type d -exec chown 10001:10001 {} +
        find "$tree" -type f -exec chown 10001:10001 {} +
    }
    for tree in \
        /data/etc/m-ui \
        /data/etc/mihomo \
        /data/var/lib/m-ui \
        /data/var/lib/mihomo
    do
        safe_tree "$tree"
    done
    chmod 0750 /data/etc/m-ui /data/etc/mihomo /data/var/lib/mihomo
    chmod 0700 /data/var/lib/m-ui
    exit 0
fi

config_path="/etc/m-ui/config.toml"
master_key="/var/lib/m-ui/master.key"
database_path="/var/lib/m-ui/m-ui.db"
current_core="/var/lib/m-ui/core/current/mihomo"

install -d -m 0750 /etc/m-ui /etc/mihomo
install -d -m 0700 /var/lib/m-ui /var/lib/m-ui/core \
    /var/lib/m-ui/core/staging \
    /var/lib/m-ui/core/backups
install -d -m 0750 /var/lib/mihomo

if [ -L "$master_key" ] || [ -L "$database_path" ]; then
    echo "refusing symbolic-link database or master key" >&2
    exit 1
fi
if [ -e "$database_path" ] && [ ! -f "$database_path" ]; then
    echo "database path is not a regular file" >&2
    exit 1
fi
if [ -e "$master_key" ] && [ ! -f "$master_key" ]; then
    echo "master key path is not a regular file" >&2
    exit 1
fi
if [ -e "$database_path" ] && [ ! -f "$master_key" ]; then
    echo "database exists but master key is missing; refusing to generate a replacement" >&2
    exit 1
fi
if [ ! -f "$master_key" ]; then
    dd if=/dev/urandom of="$master_key" bs=32 count=1 status=none
    chmod 0600 "$master_key"
fi

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
format = "json"

[storage]
database_path = "/var/lib/m-ui/m-ui.db"
master_key_path = "/var/lib/m-ui/master.key"

[security]
session_ttl = "12h"
cookie_secure = false

[panel]
title = "m-ui"
ui_language = "auto"
public_host = "${M_UI_PUBLIC_HOST:-localhost}"

[mihomo]
binary_path = "/var/lib/m-ui/core/current/mihomo"
managed_core = true
process_mode = "managed"
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
    chmod 0600 "$config_path"
fi

if [ ! -f /etc/mihomo/config.yaml ]; then
    controller_secret="$(sed -n \
        's/^controller_secret = "\(.*\)"$/\1/p' "$config_path")"
    cat >/etc/mihomo/config.yaml <<EOF
mode: rule
log-level: info
ipv6: true
external-controller: 127.0.0.1:9090
secret: $controller_secret
listeners: []
rules:
  - MATCH,DIRECT
EOF
    chmod 0600 /etc/mihomo/config.yaml
fi

if [ ! -x "$current_core" ]; then
    /usr/bin/m-ui core bootstrap \
        --config "$config_path" \
        --binary /usr/lib/m-ui/bootstrap/mihomo \
        --manifest /usr/share/m-ui/bootstrap/manifest.json \
        --channel "${M_UI_MIHOMO_CHANNEL:-release}" \
        --auto-update "${M_UI_MIHOMO_AUTO_UPDATE:-off}" \
        --check-interval "${M_UI_MIHOMO_CHECK_INTERVAL:-24h}"
fi

temporary_config="${config_path}.tmp"
sed 's/^controller_secret = ".*"$/controller_secret = ""/' \
    "$config_path" >"$temporary_config"
chmod 0600 "$temporary_config"
mv "$temporary_config" "$config_path"

exec /usr/bin/m-ui server --config "$config_path"

#!/bin/sh

set -eu

umask 077

data_root=/data
config_path=/data/etc/m-ui/config.toml
master_key=/data/var/lib/m-ui/master.key
database_path=/data/var/lib/m-ui/m-ui.db
current_core=/data/var/lib/m-ui/core/current/mihomo
mihomo_config=/data/etc/mihomo/config.yaml

fail() {
    echo "m-ui container startup: $*" >&2
    exit 1
}

[ "$(id -u)" -eq 10001 ] || fail "the long-running container must use UID 10001"
[ -d "$data_root" ] && [ ! -L "$data_root" ] || \
    fail "persistence root /data must be a real directory"

for mapping in \
    "/etc/m-ui:/data/etc/m-ui" \
    "/etc/mihomo:/data/etc/mihomo" \
    "/var/lib/m-ui:/data/var/lib/m-ui" \
    "/var/lib/mihomo:/data/var/lib/mihomo"
do
    link=${mapping%%:*}
    target=${mapping#*:}
    [ -L "$link" ] && [ "$(readlink "$link")" = "$target" ] || \
        fail "invalid image persistence adapter: $link"
done

if find "$data_root" \( -type l -o -type b -o -type c -o -type p -o -type s \) \
    -print -quit | grep -q .; then
    fail "persistence data must not contain symbolic links or special files"
fi

ensure_directory() {
    directory=$1
    mode=$2
    if [ -e "$directory" ] && [ ! -d "$directory" ]; then
        fail "persistence path is not a directory: $directory"
    fi
    if [ ! -d "$directory" ]; then
        mkdir "$directory" 2>/dev/null || fail \
            "cannot create $directory; prepare /opt/m-ui/data for UID 10001"
    fi
    chmod "$mode" "$directory" 2>/dev/null || fail \
        "cannot secure $directory; it must be owned by UID 10001"
}

ensure_directory /data/etc 0700
ensure_directory /data/var 0700
ensure_directory /data/var/lib 0700
ensure_directory /data/etc/m-ui 0750
ensure_directory /data/etc/mihomo 0750
ensure_directory /data/var/lib/m-ui 0700
ensure_directory /data/var/lib/m-ui/core 0700
ensure_directory /data/var/lib/m-ui/core/staging 0700
ensure_directory /data/var/lib/m-ui/core/backups 0700
ensure_directory /data/var/lib/mihomo 0750

if [ -L "$master_key" ] || [ -L "$database_path" ]; then
    fail "refusing symbolic-link database or master key"
fi
if [ -e "$database_path" ] && [ ! -f "$database_path" ]; then
    fail "database path is not a regular file"
fi
if [ -e "$master_key" ] && [ ! -f "$master_key" ]; then
    fail "master key path is not a regular file"
fi
if [ -e "$database_path" ] && [ ! -f "$master_key" ]; then
    fail "database exists but master key is missing; refusing to generate a replacement"
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
database_path = "/data/var/lib/m-ui/m-ui.db"
master_key_path = "/data/var/lib/m-ui/master.key"

[security]
session_ttl = "12h"
cookie_secure = false

[panel]
title = "m-ui"
ui_language = "auto"
public_host = "localhost"

[mihomo]
binary_path = "/data/var/lib/m-ui/core/current/mihomo"
managed_core = true
process_mode = "managed"
config_directory = "/data/etc/mihomo"
config_path = "/data/etc/mihomo/config.yaml"
external_controller_address = "127.0.0.1:9090"
controller_connect_address = "127.0.0.1:9090"
external_controller_cors_origins = []
controller_secret = "$controller_secret"
service_name = "mihomo.service"
revision_directory = "/data/var/lib/m-ui/revisions"
history_limit = 20
EOF
    chmod 0600 "$config_path"
fi

temporary_config="${config_path}.tmp"
sed \
    -e 's|database_path = "/var/lib/m-ui/m-ui.db"|database_path = "/data/var/lib/m-ui/m-ui.db"|' \
    -e 's|master_key_path = "/var/lib/m-ui/master.key"|master_key_path = "/data/var/lib/m-ui/master.key"|' \
    -e 's|binary_path = "/var/lib/m-ui/core/current/mihomo"|binary_path = "/data/var/lib/m-ui/core/current/mihomo"|' \
    -e 's|config_directory = "/etc/mihomo"|config_directory = "/data/etc/mihomo"|' \
    -e 's|config_path = "/etc/mihomo/config.yaml"|config_path = "/data/etc/mihomo/config.yaml"|' \
    -e 's|revision_directory = "/var/lib/m-ui/revisions"|revision_directory = "/data/var/lib/m-ui/revisions"|' \
    "$config_path" >"$temporary_config"
chmod 0600 "$temporary_config"
mv "$temporary_config" "$config_path"

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
    chmod 0600 "$mihomo_config"
fi

if [ ! -x "$current_core" ]; then
    /usr/bin/m-ui core bootstrap \
        --config "$config_path" \
        --binary /usr/lib/m-ui/bootstrap/mihomo \
        --manifest /usr/share/m-ui/bootstrap/manifest.json \
        --channel release \
        --auto-update off \
        --check-interval 24h
fi

sed 's/^controller_secret = ".*"$/controller_secret = ""/' \
    "$config_path" >"$temporary_config"
chmod 0600 "$temporary_config"
mv "$temporary_config" "$config_path"

exec /usr/bin/m-ui server --config "$config_path"

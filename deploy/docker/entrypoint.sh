#!/bin/sh

set -eu

umask 077
config_path="/etc/m-ui/config.toml"
master_key="/var/lib/m-ui/master.key"
database="/var/lib/m-ui/m-ui.db"
current_core="/var/lib/m-ui/core/current/mihomo"

install -d -m 0750 /etc/m-ui /etc/mihomo
install -d -m 0700 /var/lib/m-ui /var/lib/m-ui/core \
    /var/lib/m-ui/core/staging \
    /var/lib/m-ui/core/backups
install -d -m 0750 /var/lib/mihomo

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
ui_language = "${M_UI_LANGUAGE:-en-US}"
public_host = "${M_UI_PUBLIC_HOST:-localhost}"

[mihomo]
binary_path = "/var/lib/m-ui/core/current/mihomo"
managed_core = true
process_mode = "managed"
config_directory = "/etc/mihomo"
config_path = "/etc/mihomo/config.yaml"
controller_address = "127.0.0.1:9090"
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

new_database=0
[ -f "$database" ] || new_database=1
if [ ! -x "$current_core" ]; then
    /usr/bin/m-ui core bootstrap \
        --config "$config_path" \
        --binary /usr/lib/m-ui/bootstrap/mihomo \
        --manifest /usr/share/m-ui/bootstrap/manifest.json \
        --channel "${M_UI_MIHOMO_CHANNEL:-release}" \
        --auto-update "${M_UI_MIHOMO_AUTO_UPDATE:-off}" \
        --check-interval "${M_UI_MIHOMO_CHECK_INTERVAL:-24h}"
fi

if [ "$new_database" -eq 1 ] && [ -n "${M_UI_ADMIN_PASSWORD_FILE:-}" ]; then
    [ -r "$M_UI_ADMIN_PASSWORD_FILE" ] || {
        echo "M_UI_ADMIN_PASSWORD_FILE is not readable" >&2
        exit 1
    }
    /usr/bin/m-ui admin reset-password \
        --config "$config_path" \
        --username admin \
        --password-file "$M_UI_ADMIN_PASSWORD_FILE"
fi

temporary_config="${config_path}.tmp"
sed 's/^controller_secret = ".*"$/controller_secret = ""/' \
    "$config_path" >"$temporary_config"
chmod 0600 "$temporary_config"
mv "$temporary_config" "$config_path"

exec /usr/bin/m-ui server --config "$config_path"

#!/bin/sh

set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

M_UI_REPOSITORY="${M_UI_REPOSITORY:-Aethersailor/m-ui}"
M_UI_VERSION="${M_UI_VERSION:-}"
M_UI_ARCHIVE="${M_UI_ARCHIVE:-}"
M_UI_SHA256="${M_UI_SHA256:-}"
MIHOMO_VERSION="${MIHOMO_VERSION:-v1.19.29}"
MIHOMO_AMD64_SHA256="5612e698e96c8b8ad15abc4c0a4f098eba9234354b4f248cb97f2528e215b094"
MIHOMO_ARM64_SHA256="9a868b5e4e0ad91d9d71e1b41b0cfce78aaba44360c30df74a723f8e3926a86c"

usage() {
    cat <<'EOF'
Install m-ui on Debian 12+ or Ubuntu 24.04+.

Usage:
  install.sh --version v0.1.0 [options]

Options:
  --version VERSION          m-ui release tag to install
  --mihomo-version VERSION   official Mihomo release tag (default: v1.19.29)
  --m-ui-archive PATH        use a local release archive
  --m-ui-sha256 SHA256       required checksum for a local release archive
  --help                     show this help

Environment equivalents: M_UI_VERSION, MIHOMO_VERSION, M_UI_ARCHIVE,
M_UI_SHA256, and M_UI_REPOSITORY.
EOF
}

fail() {
    printf 'm-ui install: %s\n' "$*" >&2
    exit 1
}

need_value() {
    [ "$#" -ge 2 ] || fail "$1 requires a value"
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --version)
            need_value "$@"
            M_UI_VERSION="$2"
            shift 2
            ;;
        --mihomo-version)
            need_value "$@"
            MIHOMO_VERSION="$2"
            shift 2
            ;;
        --m-ui-archive)
            need_value "$@"
            M_UI_ARCHIVE="$2"
            shift 2
            ;;
        --m-ui-sha256)
            need_value "$@"
            M_UI_SHA256="$2"
            shift 2
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            fail "unknown option: $1"
            ;;
    esac
done

[ "$(id -u)" -eq 0 ] || fail "run this script as root"
[ "$(uname -s)" = "Linux" ] || fail "Linux is required"
[ -d /run/systemd/system ] || fail "systemd must be the active init system"
[ -n "$M_UI_VERSION" ] || fail "--version is required"
case "$M_UI_VERSION" in
    v[0-9]*) ;;
    *) fail "m-ui version must be a tag such as v0.1.0" ;;
esac
case "$MIHOMO_VERSION" in
    v[0-9]*) ;;
    *) fail "Mihomo version must be a tag such as v1.19.29" ;;
esac
if [ "$MIHOMO_VERSION" != "v1.19.29" ]; then
    fail "this installer only has verified digests for Mihomo v1.19.29"
fi

[ -r /etc/os-release ] || fail "/etc/os-release is required"
# shellcheck disable=SC1091
. /etc/os-release
case "${ID:-}:${VERSION_ID:-}" in
    debian:1[2-9]*|debian:[2-9][0-9]*|ubuntu:2[4-9].*|ubuntu:[3-9][0-9].*)
        ;;
    *)
        fail "supported hosts are Debian 12+ and Ubuntu 24.04+"
        ;;
esac

for command in awk basename cat chmod chown curl date dd getent gzip install \
    mkdir mktemp od rm runuser sha256sum sleep systemctl tar tr useradd usermod \
    visudo; do
    command -v "$command" >/dev/null 2>&1 ||
        fail "required command is missing: $command"
done

case "$(uname -m)" in
    x86_64|amd64)
        release_arch="amd64"
        mihomo_asset="mihomo-linux-amd64-compatible-${MIHOMO_VERSION}.gz"
        mihomo_sha256="$MIHOMO_AMD64_SHA256"
        ;;
    aarch64|arm64)
        release_arch="arm64"
        mihomo_asset="mihomo-linux-arm64-${MIHOMO_VERSION}.gz"
        mihomo_sha256="$MIHOMO_ARM64_SHA256"
        ;;
    *)
        fail "supported architectures are amd64 and arm64"
        ;;
esac

temporary_directory="$(mktemp -d)"
cleanup() {
    rm -rf -- "$temporary_directory"
}
trap cleanup EXIT HUP INT TERM
umask 077

download() {
    source_url="$1"
    destination="$2"
    curl --fail --location --proto '=https' --tlsv1.2 \
        --output "$destination" "$source_url"
}

verify_sha256() {
    file="$1"
    expected="$2"
    actual="$(sha256sum "$file" | awk '{print $1}')"
    [ "$actual" = "$expected" ] ||
        fail "SHA-256 verification failed for $(basename "$file")"
}

archive_name="m-ui_${M_UI_VERSION#v}_linux_${release_arch}.tar.gz"
archive_path="$temporary_directory/$archive_name"
if [ -n "$M_UI_ARCHIVE" ]; then
    [ -f "$M_UI_ARCHIVE" ] || fail "local m-ui archive does not exist"
    [ -n "$M_UI_SHA256" ] ||
        fail "--m-ui-sha256 is required with --m-ui-archive"
    install -m 0600 "$M_UI_ARCHIVE" "$archive_path"
    verify_sha256 "$archive_path" "$M_UI_SHA256"
else
    release_url="https://github.com/${M_UI_REPOSITORY}/releases/download/${M_UI_VERSION}"
    sums_path="$temporary_directory/SHA256SUMS"
    download "$release_url/$archive_name" "$archive_path"
    download "$release_url/SHA256SUMS" "$sums_path"
    expected="$(awk -v name="$archive_name" \
        '$2 == name || $2 == "*" name { print $1; exit }' "$sums_path")"
    [ -n "$expected" ] ||
        fail "release SHA256SUMS does not contain $archive_name"
    verify_sha256 "$archive_path" "$expected"
fi

if tar -tzf "$archive_path" |
    awk 'BEGIN { bad=0 } /(^|\/)\.\.(\/|$)|^\// { bad=1 } END { exit !bad }'
then
    fail "m-ui archive contains an unsafe path"
fi

release_root="$temporary_directory/release"
mkdir -p "$release_root"
tar -xzf "$archive_path" -C "$release_root"
for required in m-ui deploy/systemd/m-ui.service \
    deploy/systemd/mihomo.service deploy/sudoers/m-ui; do
    [ -f "$release_root/$required" ] ||
        fail "m-ui archive is missing $required"
done
version_output="$("$release_root/m-ui" version)"
case "$version_output" in
    "m-ui $M_UI_VERSION "*) ;;
    *) fail "m-ui archive version does not match $M_UI_VERSION" ;;
esac

downloaded_mihomo=0
mihomo_source=""
if [ -x /usr/local/bin/mihomo ]; then
    printf 'Reusing existing /usr/local/bin/mihomo.\n'
else
    mihomo_gzip="$temporary_directory/$mihomo_asset"
    mihomo_source="$temporary_directory/mihomo"
    download \
        "https://github.com/MetaCubeX/mihomo/releases/download/${MIHOMO_VERSION}/${mihomo_asset}" \
        "$mihomo_gzip"
    verify_sha256 "$mihomo_gzip" "$mihomo_sha256"
    gzip -dc "$mihomo_gzip" >"$mihomo_source"
    chmod 0755 "$mihomo_source"
    downloaded_mihomo=1
fi

mkdir -p /var/lib/m-ui/backups
chmod 0700 /var/lib/m-ui /var/lib/m-ui/backups
fresh_install=0
if [ ! -f /etc/m-ui/config.toml ]; then
    fresh_install=1
fi
if [ "$fresh_install" -eq 0 ]; then
    [ -f /var/lib/m-ui/master.key ] ||
        fail "existing installation is missing /var/lib/m-ui/master.key"
    [ -f /var/lib/m-ui/m-ui.db ] ||
        fail "existing installation is missing /var/lib/m-ui/m-ui.db"
    [ -f /etc/mihomo/config.yaml ] ||
        fail "existing installation is missing /etc/mihomo/config.yaml"
fi

if ! getent passwd m-ui >/dev/null 2>&1; then
    if getent group m-ui >/dev/null 2>&1; then
        useradd --system --gid m-ui --home-dir /var/lib/m-ui \
            --shell /usr/sbin/nologin m-ui
    else
        useradd --system --user-group --home-dir /var/lib/m-ui \
            --shell /usr/sbin/nologin m-ui
    fi
    : >/var/lib/m-ui/.m-ui-user-created
fi
if ! getent passwd mihomo >/dev/null 2>&1; then
    if getent group mihomo >/dev/null 2>&1; then
        useradd --system --gid mihomo --home-dir /var/lib/mihomo \
            --shell /usr/sbin/nologin mihomo
    else
        useradd --system --user-group --home-dir /var/lib/mihomo \
            --shell /usr/sbin/nologin mihomo
    fi
    : >/var/lib/m-ui/.mihomo-user-created
fi
if getent group systemd-journal >/dev/null 2>&1; then
    usermod -a -G systemd-journal m-ui
fi

install -d -o root -g m-ui -m 0750 /etc/m-ui
install -d -o m-ui -g mihomo -m 2750 /etc/mihomo
install -d -o m-ui -g m-ui -m 0700 \
    /var/lib/m-ui /var/lib/m-ui/revisions /var/lib/m-ui/backups
install -d -o mihomo -g mihomo -m 0750 /var/lib/mihomo
install -o root -g root -m 0755 "$release_root/m-ui" /usr/local/bin/m-ui

if [ "$downloaded_mihomo" -eq 1 ]; then
    install -o root -g root -m 0755 "$mihomo_source" /usr/local/bin/mihomo
    : >/var/lib/m-ui/.mihomo-binary-installed
fi

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
for existing in /etc/mihomo/config.yaml \
    /etc/systemd/system/m-ui.service /etc/systemd/system/mihomo.service \
    /etc/sudoers.d/m-ui; do
    if [ -e "$existing" ]; then
        backup_name="$(printf '%s' "$existing" | tr '/' '_')"
        install -m 0600 "$existing" \
            "/var/lib/m-ui/backups/${timestamp}${backup_name}"
    fi
done

visudo -cf "$release_root/deploy/sudoers/m-ui" >/dev/null
install -o root -g root -m 0644 \
    "$release_root/deploy/systemd/m-ui.service" \
    /etc/systemd/system/m-ui.service
install -o root -g root -m 0644 \
    "$release_root/deploy/systemd/mihomo.service" \
    /etc/systemd/system/mihomo.service
install -o root -g root -m 0440 \
    "$release_root/deploy/sudoers/m-ui" /etc/sudoers.d/m-ui

initial_password=""
if [ "$fresh_install" -eq 1 ]; then
    controller_secret="$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')"
    initial_password="$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"

    if [ ! -f /var/lib/m-ui/master.key ]; then
        dd if=/dev/urandom of=/var/lib/m-ui/master.key \
            bs=32 count=1 status=none
    fi
    chown m-ui:m-ui /var/lib/m-ui/master.key
    chmod 0600 /var/lib/m-ui/master.key

    config_temporary="$temporary_directory/config.toml"
    cat >"$config_temporary" <<EOF
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
binary_path = "/usr/local/bin/mihomo"
config_directory = "/etc/mihomo"
config_path = "/etc/mihomo/config.yaml"
controller_address = "127.0.0.1:9090"
controller_secret = "$controller_secret"
service_name = "mihomo.service"
revision_directory = "/var/lib/m-ui/revisions"
history_limit = 20
EOF
    install -o root -g m-ui -m 0640 \
        "$config_temporary" /etc/m-ui/config.toml

    mihomo_temporary="$temporary_directory/config.yaml"
    cat >"$mihomo_temporary" <<EOF
mode: rule
log-level: info
ipv6: true
external-controller: 127.0.0.1:9090
secret: $controller_secret
listeners: []
rules:
  - MATCH,DIRECT
EOF
    install -o m-ui -g mihomo -m 0640 \
        "$mihomo_temporary" /etc/mihomo/config.yaml

    password_file="/var/lib/m-ui/.initial-password"
    printf '%s\n' "$initial_password" >"$password_file"
    chown m-ui:m-ui "$password_file"
    chmod 0600 "$password_file"
    runuser -u m-ui -- /usr/local/bin/m-ui admin reset-password \
        --config /etc/m-ui/config.toml \
        --username admin \
        --password-file "$password_file"
    rm -f -- "$password_file"
fi

chown m-ui:m-ui /var/lib/m-ui/m-ui.db* 2>/dev/null || true
chmod 0600 /var/lib/m-ui/m-ui.db* 2>/dev/null || true
chown m-ui:mihomo /etc/mihomo/config.yaml
chmod 0640 /etc/mihomo/config.yaml

/usr/local/bin/mihomo -t -f /etc/mihomo/config.yaml
systemctl daemon-reload
systemctl enable mihomo.service m-ui.service
systemctl restart mihomo.service
systemctl restart m-ui.service

attempt=0
while [ "$attempt" -lt 30 ]; do
    if systemctl is-active --quiet mihomo.service &&
        systemctl is-active --quiet m-ui.service &&
        curl --fail --silent --show-error \
            http://127.0.0.1:2095/api/v1/health >/dev/null
    then
        break
    fi
    attempt=$((attempt + 1))
    sleep 1
done
[ "$attempt" -lt 30 ] || fail "m-ui services did not become healthy"

if [ "$fresh_install" -eq 1 ]; then
    printf 'header = "Authorization: Bearer %s"\n' "$controller_secret" |
        curl --config - --fail --silent --show-error \
            http://127.0.0.1:9090/version >/dev/null ||
        fail "Mihomo Controller authentication failed"
    sanitized_config="$temporary_directory/config-sanitized.toml"
    awk '
        /^controller_secret = / {
            print "controller_secret = \"\""
            next
        }
        { print }
    ' /etc/m-ui/config.toml >"$sanitized_config"
    install -o root -g m-ui -m 0640 \
        "$sanitized_config" /etc/m-ui/config.toml
    controller_secret=""
fi

runuser -u m-ui -- /usr/local/bin/m-ui doctor \
    --config /etc/m-ui/config.toml

printf '\nm-ui is installed.\n'
printf 'Panel: http://127.0.0.1:2095/\n'
printf 'Username: admin\n'
if [ -n "$initial_password" ]; then
    printf 'One-time initial password: %s\n' "$initial_password"
    initial_password=""
else
    printf 'The existing administrator password was preserved.\n'
fi
printf 'SSH tunnel: ssh -L 2095:127.0.0.1:2095 user@server\n'
printf 'The installer did not modify SSH, firewall, reverse-proxy, or Cloudflare settings.\n'

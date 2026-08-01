#!/bin/sh

set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

repository="Aethersailor/m-ui"
root="${M_UI_ROOT:-}"
command_name="${1:-}"
version="latest"
package_mode="auto"
mihomo_channel=""
mihomo_auto_update=""
mihomo_check_interval=""
mihomo_channel_set=0
mihomo_auto_update_set=0
mihomo_check_interval_set=0
archive=""
archive_sha256=""
admin_password_file=""
no_start=0
assume_yes=0

usage() {
    cat <<'EOF'
Usage:
  manage.sh install|update|reinstall|uninstall|purge|status|doctor [options]

Options:
  --version latest|vX.Y.Z
  --package auto|deb|apk|tar
  --mihomo-channel release|alpha
  --mihomo-auto-update on|off
  --mihomo-check-interval 6h|12h|24h|168h
  --archive PATH
  --sha256 SHA256
  --admin-password-file PATH
  --no-start
  --yes
EOF
}

fail() {
    printf 'm-ui manage: %s\n' "$*" >&2
    exit 1
}

target() {
    printf '%s%s\n' "$root" "$1"
}

require_value() {
    [ "$#" -ge 2 ] || fail "$1 requires a value"
}

[ -n "$command_name" ] || {
    usage
    exit 1
}
shift

while [ "$#" -gt 0 ]; do
    case "$1" in
        --version)
            require_value "$@"
            version="$2"
            shift 2
            ;;
        --package)
            require_value "$@"
            package_mode="$2"
            shift 2
            ;;
        --mihomo-channel)
            require_value "$@"
            mihomo_channel="$2"
            mihomo_channel_set=1
            shift 2
            ;;
        --mihomo-auto-update)
            require_value "$@"
            mihomo_auto_update="$2"
            mihomo_auto_update_set=1
            shift 2
            ;;
        --mihomo-check-interval)
            require_value "$@"
            mihomo_check_interval="$2"
            mihomo_check_interval_set=1
            shift 2
            ;;
        --archive)
            require_value "$@"
            archive="$2"
            shift 2
            ;;
        --sha256)
            require_value "$@"
            archive_sha256="$2"
            shift 2
            ;;
        --admin-password-file)
            require_value "$@"
            admin_password_file="$2"
            shift 2
            ;;
        --no-start)
            no_start=1
            shift
            ;;
        --yes)
            assume_yes=1
            shift
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

case "$command_name" in
    install|update|reinstall|uninstall|purge|status|doctor) ;;
    *) fail "unknown command: $command_name" ;;
esac
if [ "$version" != "latest" ] &&
    ! printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'
then
    fail "--version must be latest or vX.Y.Z"
fi
case "$package_mode" in
    auto|deb|apk|tar) ;;
    *) fail "--package must be auto, deb, apk, or tar" ;;
esac
package_requested="$package_mode"
case "$mihomo_channel" in
    ""|release|alpha) ;;
    *) fail "--mihomo-channel must be release or alpha" ;;
esac
case "$mihomo_auto_update" in
    ""|on|off) ;;
    *) fail "--mihomo-auto-update must be on or off" ;;
esac
case "$mihomo_check_interval" in
    ""|6h|12h|24h|168h) ;;
    *) fail "--mihomo-check-interval must be 6h, 12h, 24h, or 168h" ;;
esac
if [ -n "$root" ]; then
    case "$root" in
        /*) ;;
        *) fail "M_UI_ROOT must be an absolute test root" ;;
    esac
fi

if [ "$command_name" != "status" ] && [ "$command_name" != "doctor" ] &&
    [ -z "$root" ]
then
    [ "$(id -u)" -eq 0 ] || fail "state-changing commands must run as root"
fi
if [ -z "$root" ]; then
    [ "$(uname -s)" = "Linux" ] || fail "Linux is required"
fi

detect_platform() {
    case "$(uname -m)" in
        x86_64|amd64) asset_arch="amd64" ;;
        aarch64|arm64) asset_arch="arm64" ;;
        *) fail "supported architectures are amd64 and arm64" ;;
    esac
    if [ -n "$root" ]; then
        distro_id="${M_UI_TEST_DISTRO:-debian}"
        distro_version="${M_UI_TEST_VERSION:-12}"
        distro_codename="${M_UI_TEST_CODENAME:-}"
    else
        [ -r /etc/os-release ] || fail "/etc/os-release is required"
        # shellcheck disable=SC1091
        . /etc/os-release
        distro_id="${ID:-}"
        distro_version="${VERSION_ID:-}"
        distro_codename="${VERSION_CODENAME:-}"
    fi
    case "$distro_id:$distro_version:$distro_codename" in
        debian:1[2-9]*|debian:[2-9][0-9]*|ubuntu:2[4-9].*|ubuntu:[3-9][0-9].*)
            native_package="deb"
            init_system="systemd"
            ;;
        debian::sid|debian::forky)
            native_package="deb"
            init_system="systemd"
            ;;
        alpine:3.2[0-9]*|alpine:[4-9].*)
            native_package="apk"
            init_system="openrc"
            ;;
        *) fail "supported systems are Debian 12+/sid, Ubuntu 24.04+, and Alpine 3.20+" ;;
    esac
    if [ "$package_mode" = "auto" ]; then
        package_mode="$native_package"
    fi
}

verify_sha256() {
    file="$1"
    expected="$2"
    printf '%s\n' "$expected" |
        grep -Eq '^[0-9a-f]{64}$' ||
        fail "invalid SHA-256 value"
    actual="$(sha256sum "$file" | awk '{print $1}')"
    [ "$actual" = "$expected" ] ||
        fail "SHA-256 verification failed for $(basename "$file")"
}

download() {
    url="$1"
    output="$2"
    case "$url" in
        https://github.com/Aethersailor/m-ui/*|https://api.github.com/repos/Aethersailor/m-ui/*)
            ;;
        *) fail "refusing non-official or non-HTTPS download URL" ;;
    esac
    curl --fail --location --proto '=https' --proto-redir '=https' --tlsv1.2 \
        --connect-timeout 10 --max-time 120 --output "$output" "$url"
}

resolve_version() {
    if [ "$version" != "latest" ]; then
        return
    fi
    metadata="$temporary_directory/release.json"
    download "https://api.github.com/repos/$repository/releases/latest" "$metadata"
    version="$(sed -n \
        's/^[[:space:]]*"tag_name":[[:space:]]*"\(v[0-9][0-9.]*\)",[[:space:]]*$/\1/p' \
        "$metadata" | head -n 1)"
    printf '%s\n' "$version" |
        grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' ||
        fail "latest GitHub Release did not contain a strict vX.Y.Z tag"
}

safe_extract_tar() {
    input="$1"
    destination="$2"
    if tar -tzf "$input" |
        awk 'BEGIN { bad=0 } /(^|\/)\.\.(\/|$)|^\// { bad=1 } END { exit !bad }'
    then
        fail "archive contains an unsafe path"
    fi
    if tar -tvzf "$input" |
        awk 'BEGIN { bad=0 }
             substr($1, 1, 1) != "-" && substr($1, 1, 1) != "d" { bad=1 }
             END { exit !bad }'
    then
        fail "archive contains a link or special file"
    fi
    mkdir -p "$destination"
    tar -xzf "$input" -C "$destination" --no-same-owner
}

ensure_accounts_and_directories() {
    if [ -z "$root" ]; then
        if ! getent passwd m-ui >/dev/null 2>&1; then
            if command -v adduser >/dev/null 2>&1 && [ "$distro_id" = "alpine" ]; then
                adduser -S -D -H -h /var/lib/m-ui -s /sbin/nologin m-ui
            else
                useradd --system --user-group --home-dir /var/lib/m-ui \
                    --shell /usr/sbin/nologin m-ui
            fi
        fi
        if ! getent passwd mihomo >/dev/null 2>&1; then
            if command -v adduser >/dev/null 2>&1 && [ "$distro_id" = "alpine" ]; then
                adduser -S -D -H -h /var/lib/mihomo -s /sbin/nologin mihomo
            else
                useradd --system --user-group --home-dir /var/lib/mihomo \
                    --shell /usr/sbin/nologin mihomo
            fi
        fi
        if [ "$distro_id" = "alpine" ]; then
            if ! id -nG m-ui | tr ' ' '\n' | grep -qx mihomo; then
                addgroup m-ui mihomo
            fi
        else
            usermod -a -G mihomo m-ui
        fi
        m_ui_owner="m-ui:m-ui"
        config_owner="root:m-ui"
        mihomo_owner="mihomo:mihomo"
        mihomo_config_owner="m-ui:mihomo"
        core_owner="m-ui:mihomo"
    else
        m_ui_owner="$(id -u):$(id -g)"
        config_owner="$m_ui_owner"
        mihomo_owner="$m_ui_owner"
        mihomo_config_owner="$m_ui_owner"
        core_owner="$m_ui_owner"
    fi
    install -d -m 0750 "$(target /etc/m-ui)"
    install -d -m 2750 "$(target /etc/mihomo)"
    install -d -m 2710 "$(target /var/lib/m-ui)"
    install -d -m 0700 "$(target /var/lib/m-ui/revisions)"
    install -d -m 2710 "$(target /var/lib/m-ui/core)"
    install -d -m 2750 \
        "$(target /var/lib/m-ui/core/staging)" \
        "$(target /var/lib/m-ui/core/backups)"
    install -d -m 0750 "$(target /var/lib/mihomo)"
    chown "$config_owner" "$(target /etc/m-ui)"
    chown "$mihomo_config_owner" "$(target /etc/mihomo)"
    chown "$core_owner" "$(target /var/lib/m-ui)" \
        "$(target /var/lib/m-ui/core)" \
        "$(target /var/lib/m-ui/core/staging)" \
        "$(target /var/lib/m-ui/core/backups)"
    chown "$m_ui_owner" "$(target /var/lib/m-ui/revisions)"
    chown "$mihomo_owner" "$(target /var/lib/mihomo)"
}

write_initial_configuration() {
    config_path="$(target /etc/m-ui/config.toml)"
    mihomo_config="$(target /etc/mihomo/config.yaml)"
    master_key="$(target /var/lib/m-ui/master.key)"
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
format = "text"

[storage]
database_path = "$(target /var/lib/m-ui/m-ui.db)"
master_key_path = "$master_key"

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
config_directory = "$(target /etc/mihomo)"
config_path = "$mihomo_config"
controller_address = "127.0.0.1:9090"
controller_secret = "$controller_secret"
service_name = "mihomo.service"
revision_directory = "$(target /var/lib/m-ui/revisions)"
history_limit = 20
EOF
        chmod 0640 "$config_path"
    fi
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
    chown "$m_ui_owner" "$master_key"
    chown "$config_owner" "$config_path"
    chown "$mihomo_config_owner" "$mihomo_config"
}

install_service_files() {
    stage="$1"
    if [ "$init_system" = "systemd" ]; then
        install -D -m 0644 "$stage/deploy/systemd/m-ui.service" \
            "$(target /etc/systemd/system/m-ui.service)"
        install -D -m 0644 "$stage/deploy/systemd/mihomo.service" \
            "$(target /etc/systemd/system/mihomo.service)"
        install -D -m 0440 "$stage/deploy/sudoers/m-ui" \
            "$(target /etc/sudoers.d/m-ui)"
        if [ -z "$root" ]; then
            visudo -cf /etc/sudoers.d/m-ui >/dev/null
        fi
    else
        install -D -m 0755 "$stage/deploy/openrc/m-ui" \
            "$(target /etc/init.d/m-ui)"
        install -D -m 0755 "$stage/deploy/openrc/mihomo" \
            "$(target /etc/init.d/mihomo)"
        install -D -m 0400 "$stage/deploy/doas/m-ui.conf" \
            "$(target /etc/doas.d/m-ui.conf)"
    fi
}

install_tar_payload() {
    input="$1"
    stage="$temporary_directory/release"
    safe_extract_tar "$input" "$stage"
    for required in m-ui bootstrap/mihomo bootstrap/manifest.json \
        scripts/manage.sh deploy/examples/config.toml
    do
        [ -f "$stage/$required" ] || fail "archive is missing $required"
    done
    install -D -m 0755 "$stage/m-ui" "$(target /usr/bin/m-ui)"
    install -D -m 0755 "$stage/scripts/manage.sh" \
        "$(target /usr/lib/m-ui/manage.sh)"
    install -D -m 0755 "$stage/scripts/install.sh" \
        "$(target /usr/lib/m-ui/install.sh)"
    install -D -m 0755 "$stage/scripts/uninstall.sh" \
        "$(target /usr/lib/m-ui/uninstall.sh)"
    install -D -m 0755 "$stage/bootstrap/mihomo" \
        "$(target /usr/lib/m-ui/bootstrap/mihomo)"
    install -D -m 0644 "$stage/bootstrap/manifest.json" \
        "$(target /usr/share/m-ui/bootstrap/manifest.json)"
    install_service_files "$stage"
}

run_as_m_ui() {
    if [ -n "$root" ]; then
        "$@"
    elif command -v runuser >/dev/null 2>&1; then
        runuser -u m-ui -- "$@"
    elif command -v su-exec >/dev/null 2>&1; then
        su-exec m-ui "$@"
    else
        fail "runuser or su-exec is required"
    fi
}

run_as_mihomo() {
    if [ -n "$root" ]; then
        "$@"
    elif command -v runuser >/dev/null 2>&1; then
        runuser -u mihomo -- "$@"
    elif command -v su-exec >/dev/null 2>&1; then
        su-exec mihomo "$@"
    else
        fail "runuser or su-exec is required"
    fi
}

backup_program_files() {
    backup="$temporary_directory/rollback"
    mkdir -p "$backup"
    for path in /usr/bin/m-ui /etc/systemd/system/m-ui.service \
        /etc/systemd/system/mihomo.service \
        /lib/systemd/system/m-ui.service \
        /lib/systemd/system/mihomo.service /etc/sudoers.d/m-ui \
        /etc/init.d/m-ui /etc/init.d/mihomo /etc/doas.d/m-ui.conf
    do
        source="$(target "$path")"
        if [ -f "$source" ]; then
            mkdir -p "$backup$(dirname "$path")"
            cp -p "$source" "$backup$path"
        fi
    done
    for path in /usr/lib/m-ui /usr/share/m-ui; do
        source="$(target "$path")"
        if [ -d "$source" ]; then
            mkdir -p "$backup$(dirname "$path")"
            cp -Rp "$source" "$backup$path"
        fi
    done
}

restore_program_files() {
    backup="$temporary_directory/rollback"
    [ -d "$backup" ] || return
    rm -f "$(target /usr/bin/m-ui)" \
        "$(target /etc/systemd/system/m-ui.service)" \
        "$(target /etc/systemd/system/mihomo.service)" \
        "$(target /lib/systemd/system/m-ui.service)" \
        "$(target /lib/systemd/system/mihomo.service)" \
        "$(target /etc/sudoers.d/m-ui)" \
        "$(target /etc/init.d/m-ui)" \
        "$(target /etc/init.d/mihomo)" \
        "$(target /etc/doas.d/m-ui.conf)"
    rm -rf "$(target /usr/lib/m-ui)" "$(target /usr/share/m-ui)"
    for path in /usr/bin/m-ui /etc/systemd/system/m-ui.service \
        /etc/systemd/system/mihomo.service \
        /lib/systemd/system/m-ui.service \
        /lib/systemd/system/mihomo.service /etc/sudoers.d/m-ui \
        /etc/init.d/m-ui /etc/init.d/mihomo /etc/doas.d/m-ui.conf
    do
        source="$backup$path"
        if [ -f "$source" ]; then
            destination="$(target "$path")"
            mkdir -p "$(dirname "$destination")"
            cp -p "$source" "$destination"
        fi
    done
    for path in /usr/lib/m-ui /usr/share/m-ui; do
        source="$backup$path"
        if [ -d "$source" ]; then
            destination="$(target "$path")"
            mkdir -p "$(dirname "$destination")"
            cp -Rp "$source" "$destination"
        fi
    done
}

configure_bootstrap() {
    binary="$(target /usr/bin/m-ui)"
    bootstrap_binary="$(target /usr/lib/m-ui/bootstrap/mihomo)"
    bootstrap_manifest="$(target /usr/share/m-ui/bootstrap/manifest.json)"
    if [ "$database_was_present" -eq 1 ] &&
        [ -x "$(target /var/lib/m-ui/core/current/mihomo)" ] &&
        [ -s "$(target /var/lib/m-ui/core/current/manifest.json)" ] &&
        run_as_m_ui "$binary" core status --json \
            --config "$(target /etc/m-ui/config.toml)" >/dev/null 2>&1
    then
        return
    fi
    if [ -n "$root" ]; then
        install -d -m 0750 "$(target /var/lib/m-ui/core/current)"
        install -m 0750 "$bootstrap_binary" \
            "$(target /var/lib/m-ui/core/current/mihomo)"
        install -m 0640 "$bootstrap_manifest" \
            "$(target /var/lib/m-ui/core/current/manifest.json)"
        return
    fi
    set -- core bootstrap \
        --config /etc/m-ui/config.toml \
        --binary "$bootstrap_binary" \
        --manifest "$bootstrap_manifest"
    [ "$mihomo_channel_set" -eq 1 ] &&
        set -- "$@" --channel "$mihomo_channel"
    [ "$mihomo_auto_update_set" -eq 1 ] &&
        set -- "$@" --auto-update "$mihomo_auto_update"
    [ "$mihomo_check_interval_set" -eq 1 ] &&
        set -- "$@" --check-interval "$mihomo_check_interval"
    run_as_m_ui "$binary" "$@"
}

initialize_administrator() {
    [ "$database_was_present" -eq 0 ] || return
    password_path="$(target /var/lib/m-ui/.initial-admin-password)"
    generated_password=""
    if [ -n "$admin_password_file" ]; then
        [ -f "$admin_password_file" ] ||
            fail "--admin-password-file must name a regular file"
        cp "$admin_password_file" "$password_path"
    else
        generated_password="$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
        printf '%s\n' "$generated_password" >"$password_path"
    fi
    chmod 0600 "$password_path"
    chown "$m_ui_owner" "$password_path"
    run_as_m_ui "$(target /usr/bin/m-ui)" admin reset-password \
        --config "$(target /etc/m-ui/config.toml)" \
        --username admin \
        --password-file "$password_path"
    rm -f "$password_path"
    if [ -n "$generated_password" ]; then
        printf '%s\n' \
            "Initial administrator: admin" \
            "One-time initial password: $generated_password"
    else
        printf '%s\n' \
            "Initial administrator admin was created from --admin-password-file."
    fi
}

clear_bootstrap_controller_secret() {
    config_path="$(target /etc/m-ui/config.toml)"
    temporary_config="${config_path}.tmp"
    sed 's/^controller_secret = ".*"$/controller_secret = ""/' \
        "$config_path" >"$temporary_config"
    chmod 0640 "$temporary_config"
    chown "$config_owner" "$temporary_config"
    mv "$temporary_config" "$config_path"
}

start_services() {
    [ "$no_start" -eq 0 ] || return 0
    [ -z "$root" ] || return 0
    if [ "$init_system" = "systemd" ]; then
        systemctl daemon-reload
        systemctl enable mihomo.service m-ui.service
        systemctl restart mihomo.service
        systemctl restart m-ui.service
    else
        rc-update add mihomo default
        rc-update add m-ui default
        rc-service mihomo restart
        rc-service m-ui restart
    fi
    attempt=0
    while [ "$attempt" -lt 60 ]; do
        services_active=0
        if [ "$init_system" = "systemd" ]; then
            if systemctl is-active --quiet m-ui.service mihomo.service; then
                services_active=1
            fi
        elif rc-service m-ui status >/dev/null 2>&1 &&
            rc-service mihomo status >/dev/null 2>&1
        then
            services_active=1
        fi
        if [ "$services_active" -eq 1 ] &&
            curl --fail --silent --show-error \
                http://127.0.0.1:2095/api/v1/health >/dev/null &&
            run_as_m_ui /usr/bin/m-ui core status \
                --json --config /etc/m-ui/config.toml >/dev/null 2>&1 &&
            run_as_mihomo /var/lib/m-ui/core/current/mihomo \
                -t -d /var/lib/mihomo \
                -f /etc/mihomo/config.yaml >/dev/null 2>&1
        then
            return 0
        fi
        attempt=$((attempt + 1))
        sleep 1
    done
    return 1
}

stop_and_remove_program() {
    if [ -z "$root" ] && command -v dpkg-query >/dev/null 2>&1 &&
        dpkg-query -W -f='${Status}' m-ui 2>/dev/null |
            grep -q '^install ok installed$'
    then
        dpkg --remove m-ui
    fi
    if [ -z "$root" ] && command -v apk >/dev/null 2>&1 &&
        apk info --installed m-ui >/dev/null 2>&1
    then
        apk del m-ui
    fi
    if [ -z "$root" ]; then
        if [ -d /run/systemd/system ]; then
            systemctl disable --now m-ui.service mihomo.service >/dev/null 2>&1 || true
        elif command -v rc-service >/dev/null 2>&1; then
            rc-service m-ui stop >/dev/null 2>&1 || true
            rc-service mihomo stop >/dev/null 2>&1 || true
            rc-update del m-ui default >/dev/null 2>&1 || true
            rc-update del mihomo default >/dev/null 2>&1 || true
        fi
    fi
    rm -f "$(target /usr/bin/m-ui)" \
        "$(target /etc/systemd/system/m-ui.service)" \
        "$(target /etc/systemd/system/mihomo.service)" \
        "$(target /lib/systemd/system/m-ui.service)" \
        "$(target /lib/systemd/system/mihomo.service)" \
        "$(target /etc/sudoers.d/m-ui)" \
        "$(target /etc/init.d/m-ui)" \
        "$(target /etc/init.d/mihomo)" \
        "$(target /etc/doas.d/m-ui.conf)"
    rm -rf "$(target /usr/lib/m-ui)" "$(target /usr/share/m-ui)"
    if [ -z "$root" ] && [ -d /run/systemd/system ]; then
        systemctl daemon-reload
    fi
}

show_status() {
    binary="$(target /usr/bin/m-ui)"
    if [ -x "$binary" ]; then
        "$binary" version || true
    else
        printf '%s\n' "m-ui binary: absent"
    fi
    for path in /etc/m-ui /etc/mihomo /var/lib/m-ui; do
        if [ -e "$(target "$path")" ]; then
            printf '%s\n' "$path: present"
        else
            printf '%s\n' "$path: absent"
        fi
    done
    if [ -z "$root" ]; then
        if [ -d /run/systemd/system ]; then
            systemctl --no-pager --full status m-ui.service mihomo.service || true
        elif command -v rc-service >/dev/null 2>&1; then
            rc-service m-ui status || true
            rc-service mihomo status || true
        fi
    fi
}

if [ "$command_name" = "status" ]; then
    show_status
    exit 0
fi
if [ "$command_name" = "doctor" ]; then
    binary="$(target /usr/bin/m-ui)"
    [ -x "$binary" ] || fail "m-ui is not installed"
    if [ -n "$root" ]; then
        show_status
    else
        run_as_m_ui "$binary" doctor --config /etc/m-ui/config.toml
    fi
    exit 0
fi
if [ "$command_name" = "uninstall" ]; then
    stop_and_remove_program
    printf '%s\n' "m-ui program and service files removed; data preserved."
    exit 0
fi
if [ "$command_name" = "purge" ]; then
    if [ "$assume_yes" -ne 1 ]; then
        [ -t 0 ] || fail "purge requires an interactive confirmation or --yes"
        printf 'Type PURGE to delete all m-ui and Mihomo managed data: '
        read -r confirmation
        [ "$confirmation" = "PURGE" ] || fail "purge cancelled"
    fi
    stop_and_remove_program
    rm -rf "$(target /etc/m-ui)" "$(target /etc/mihomo)" \
        "$(target /var/lib/m-ui)" "$(target /var/lib/mihomo)"
    printf '%s\n' "m-ui program, configuration, keys, revisions, and cores purged."
    exit 0
fi

detect_platform
temporary_directory="$(mktemp -d)"
cleanup() {
    rm -rf "$temporary_directory"
}
trap cleanup EXIT HUP INT TERM
umask 077

if [ -n "$archive" ]; then
    [ -f "$archive" ] || fail "local archive does not exist"
    [ -n "$archive_sha256" ] || fail "--sha256 is required with --archive"
    input="$temporary_directory/$(basename "$archive")"
    cp "$archive" "$input"
    verify_sha256 "$input" "$archive_sha256"
    case "$input" in
        *.deb) resolved_package="deb" ;;
        *.apk) resolved_package="apk" ;;
        *.tar.gz|*.tgz) resolved_package="tar" ;;
        *) fail "unsupported local archive type" ;;
    esac
    if [ "$package_requested" != "auto" ] &&
        [ "$package_requested" != "$resolved_package" ]
    then
        fail "--package does not match the local archive type"
    fi
    package_mode="$resolved_package"
else
    resolve_version
    version_number="${version#v}"
    case "$package_mode" in
        deb) asset_name="m-ui_${version_number}_linux_${asset_arch}.deb" ;;
        apk) asset_name="m-ui_${version_number}_linux_${asset_arch}.apk" ;;
        tar) asset_name="m-ui_${version_number}_linux_${asset_arch}.tar.gz" ;;
    esac
    base_url="https://github.com/$repository/releases/download/$version"
    sums="$temporary_directory/SHA256SUMS"
    input="$temporary_directory/$asset_name"
    download "$base_url/SHA256SUMS" "$sums"
    download "$base_url/$asset_name" "$input"
    expected="$(awk -v name="$asset_name" \
        '$2 == name || $2 == "*" name { print $1; exit }' "$sums")"
    [ -n "$expected" ] || fail "SHA256SUMS does not contain $asset_name"
    verify_sha256 "$input" "$expected"
fi

backup_program_files
ensure_accounts_and_directories
database_was_present=0
if [ -f "$(target /var/lib/m-ui/m-ui.db)" ]; then
    database_was_present=1
fi

install_failed=0
if [ "$package_mode" = "tar" ]; then
    if ! (install_tar_payload "$input"); then
        install_failed=1
    fi
elif [ -n "$root" ]; then
    fail "package-manager archives are not supported with M_UI_ROOT"
elif [ "$package_mode" = "deb" ]; then
    M_UI_SKIP_ADMIN_INIT=1 dpkg -i "$input" || install_failed=1
else
    M_UI_SKIP_ADMIN_INIT=1 apk add --allow-untrusted "$input" ||
        install_failed=1
fi
if [ "$install_failed" -ne 0 ]; then
    restore_program_files
    fail "installation failed and previous program files were restored"
fi

if ! (
    write_initial_configuration
    configure_bootstrap
    initialize_administrator
    clear_bootstrap_controller_secret
); then
    restore_program_files
    fail "configuration or bootstrap failed and previous program files were restored"
fi
if ! start_services; then
    restore_program_files
    if ! start_services; then
        fail "services and automatic program rollback both failed health checks"
    fi
    fail "services failed health checks and previous program files were restored"
fi

printf '%s\n' \
    "m-ui $command_name completed." \
    "Configuration, database, master key, revisions, and core settings were preserved." \
    "No SSH, firewall, reverse proxy, or Cloudflare settings were changed."

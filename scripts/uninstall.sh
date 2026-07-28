#!/bin/sh

set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

purge=0
assume_yes=0

usage() {
    cat <<'EOF'
Usage:
  uninstall.sh [--purge] [--yes]

Without --purge, m-ui configuration, Mihomo configuration, database, keys,
revisions, backups, and system users are preserved.
EOF
}

fail() {
    printf 'm-ui uninstall: %s\n' "$*" >&2
    exit 1
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --purge)
            purge=1
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

[ "$(id -u)" -eq 0 ] || fail "run this script as root"
[ "$(uname -s)" = "Linux" ] || fail "Linux is required"
[ -d /run/systemd/system ] || fail "systemd must be the active init system"

if [ "$purge" -eq 1 ] && [ "$assume_yes" -ne 1 ]; then
    [ -t 0 ] || fail "--purge requires an interactive confirmation or --yes"
    printf 'This permanently deletes /var/lib/m-ui, /etc/m-ui, and /etc/mihomo.\n'
    printf 'Type PURGE to continue: '
    read -r confirmation
    [ "$confirmation" = "PURGE" ] || fail "purge cancelled"
fi

m_ui_user_created=0
mihomo_user_created=0
mihomo_binary_installed=0
[ -e /var/lib/m-ui/.m-ui-user-created ] && m_ui_user_created=1
[ -e /var/lib/m-ui/.mihomo-user-created ] && mihomo_user_created=1
[ -e /var/lib/m-ui/.mihomo-binary-installed ] && mihomo_binary_installed=1

systemctl disable --now m-ui.service >/dev/null 2>&1 || true
systemctl disable --now mihomo.service >/dev/null 2>&1 || true
rm -f -- /etc/systemd/system/m-ui.service \
    /etc/systemd/system/mihomo.service /etc/sudoers.d/m-ui
systemctl daemon-reload
systemctl reset-failed >/dev/null 2>&1 || true

rm -f -- /usr/local/bin/m-ui
if [ "$mihomo_binary_installed" -eq 1 ]; then
    rm -f -- /usr/local/bin/mihomo
fi

if [ "$purge" -eq 1 ]; then
    rm -rf -- /var/lib/m-ui /etc/m-ui /etc/mihomo
    if [ "$m_ui_user_created" -eq 1 ] && command -v userdel >/dev/null 2>&1; then
        userdel m-ui >/dev/null 2>&1 || true
    fi
    if [ "$mihomo_user_created" -eq 1 ] && command -v userdel >/dev/null 2>&1; then
        userdel mihomo >/dev/null 2>&1 || true
    fi
    printf 'm-ui was removed and managed data was purged.\n'
else
    printf 'm-ui was removed. Data and configuration were preserved.\n'
    printf 'Use --purge only after making and verifying a backup.\n'
fi

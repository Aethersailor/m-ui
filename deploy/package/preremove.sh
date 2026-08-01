#!/bin/sh

set -eu

action="${1:-remove}"
state_file=/run/m-ui/package-upgrade-services
rm -f "$state_file"

if [ -d /run/systemd/system ]; then
    if [ "$action" = "upgrade" ] || [ "$action" = "failed-upgrade" ]; then
        m_ui_enabled=0
        m_ui_active=0
        mihomo_enabled=0
        mihomo_active=0
        if systemctl is-enabled m-ui.service >/dev/null 2>&1; then m_ui_enabled=1; fi
        if systemctl is-active m-ui.service >/dev/null 2>&1; then m_ui_active=1; fi
        if systemctl is-enabled mihomo.service >/dev/null 2>&1; then mihomo_enabled=1; fi
        if systemctl is-active mihomo.service >/dev/null 2>&1; then mihomo_active=1; fi
        install -d -m 0755 /run/m-ui
        umask 077
        printf '%s\n' \
            "m_ui_enabled=$m_ui_enabled" \
            "m_ui_active=$m_ui_active" \
            "mihomo_enabled=$mihomo_enabled" \
            "mihomo_active=$mihomo_active" >"$state_file"
    fi
    systemctl stop m-ui.service mihomo.service >/dev/null 2>&1 || true
elif command -v rc-service >/dev/null 2>&1; then
    if [ "$action" = "upgrade" ] || [ "$action" = "failed-upgrade" ]; then
        m_ui_enabled=0
        m_ui_active=0
        mihomo_enabled=0
        mihomo_active=0
        if rc-update show default 2>/dev/null | grep -Eq '(^|[[:space:]])m-ui([[:space:]]|$)'; then m_ui_enabled=1; fi
        if rc-update show default 2>/dev/null | grep -Eq '(^|[[:space:]])mihomo([[:space:]]|$)'; then mihomo_enabled=1; fi
        if rc-service m-ui status >/dev/null 2>&1; then m_ui_active=1; fi
        if rc-service mihomo status >/dev/null 2>&1; then mihomo_active=1; fi
        install -d -m 0755 /run/m-ui
        umask 077
        printf '%s\n' \
            "m_ui_enabled=$m_ui_enabled" \
            "m_ui_active=$m_ui_active" \
            "mihomo_enabled=$mihomo_enabled" \
            "mihomo_active=$mihomo_active" >"$state_file"
    fi
    rc-service m-ui stop >/dev/null 2>&1 || true
    rc-service mihomo stop >/dev/null 2>&1 || true
fi

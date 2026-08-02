#!/bin/sh

set -eu

case "${1:-}" in
    remove|purge)
        rm -f \
            /usr/bin/m-ui \
            /etc/systemd/system/m-ui.service \
            /etc/systemd/system/mihomo.service \
            /lib/systemd/system/m-ui.service \
            /lib/systemd/system/mihomo.service \
            /etc/sudoers.d/m-ui \
            /etc/init.d/m-ui \
            /etc/init.d/mihomo \
            /etc/doas.d/m-ui.conf
        rm -rf /usr/lib/m-ui /usr/share/m-ui
        rm -f \
            /run/m-ui-package/package-upgrade-services \
            /run/m-ui-package/package-backup-snapshot
        ;;
esac

if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
    systemctl reset-failed >/dev/null 2>&1 || true
fi
printf '%s\n' "m-ui data under /etc and /var/lib was preserved."

#!/bin/sh

set -eu

if [ -d /run/systemd/system ]; then
    systemctl stop m-ui.service mihomo.service >/dev/null 2>&1 || true
elif command -v rc-service >/dev/null 2>&1; then
    rc-service m-ui stop >/dev/null 2>&1 || true
    rc-service mihomo stop >/dev/null 2>&1 || true
fi

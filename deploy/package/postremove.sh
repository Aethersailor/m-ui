#!/bin/sh

set -eu

if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
    systemctl reset-failed >/dev/null 2>&1 || true
fi
printf '%s\n' "m-ui data under /etc and /var/lib was preserved."

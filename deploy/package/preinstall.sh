#!/bin/sh

set -eu

backup_dir="/var/lib/m-ui/package-backups"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
snapshot="$backup_dir/$timestamp"
mkdir -p "$snapshot"
chmod 0700 "$backup_dir"
for path in /usr/bin/m-ui /usr/lib/m-ui /usr/share/m-ui \
    /etc/systemd/system/m-ui.service /etc/systemd/system/mihomo.service \
    /etc/sudoers.d/m-ui /etc/init.d/m-ui /etc/init.d/mihomo \
    /etc/doas.d/m-ui.conf
do
    if [ -e "$path" ]; then
        mkdir -p "$snapshot$(dirname "$path")"
        cp -a "$path" "$snapshot$path"
    fi
done
find "$backup_dir" -mindepth 1 -maxdepth 1 -type d -print |
    sort -r |
    awk 'NR > 2' |
    while IFS= read -r old_snapshot; do
        rm -rf "$old_snapshot"
    done

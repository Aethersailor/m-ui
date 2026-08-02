#!/bin/sh

set -eu

backup_dir="/var/lib/m-ui-package-backups"
transaction_dir="/run/m-ui-package"
snapshot_state="$transaction_dir/package-backup-snapshot"

if [ -e "$backup_dir" ] && [ ! -d "$backup_dir" -o -L "$backup_dir" ]; then
    printf '%s\n' "refusing an unsafe package backup directory" >&2
    exit 1
fi
install -d -o root -g root -m 0700 "$backup_dir"
[ "$(stat -c '%u:%g:%a' "$backup_dir" 2>/dev/null)" = "0:0:700" ]

if [ -e "$transaction_dir" ] &&
    [ ! -d "$transaction_dir" -o -L "$transaction_dir" ]; then
    printf '%s\n' "refusing an unsafe package transaction directory" >&2
    exit 1
fi
install -d -o root -g root -m 0700 "$transaction_dir"
[ "$(stat -c '%u:%g:%a' "$transaction_dir" 2>/dev/null)" = "0:0:700" ]
rm -f "$snapshot_state"
snapshot="$(mktemp -d "$backup_dir/snapshot.XXXXXX")"
chmod 0700 "$snapshot"

reject_unsafe_source() {
    path="$1"
    if [ ! -e "$path" ] && [ ! -L "$path" ]; then
        return 0
    fi
    if [ -L "$path" ]; then
        printf '%s\n' "refusing to back up symbolic link: $path" >&2
        exit 1
    fi
    if find "$path" \( -type l -o -type b -o -type c -o -type p -o -type s \) \
        -print -quit | grep -q .
    then
        printf '%s\n' "refusing to back up unsafe filesystem entry: $path" >&2
        exit 1
    fi
}

for path in /usr/bin/m-ui /usr/lib/m-ui /usr/share/m-ui \
    /etc/systemd/system/m-ui.service /etc/systemd/system/mihomo.service \
    /etc/sudoers.d/m-ui /etc/init.d/m-ui /etc/init.d/mihomo \
    /etc/doas.d/m-ui.conf
do
    if [ -e "$path" ] || [ -L "$path" ]; then
        reject_unsafe_source "$path"
        mkdir -p "$snapshot$(dirname "$path")"
        cp -a "$path" "$snapshot$path"
    fi
done

write_snapshot_state() {
    temporary_state="$(mktemp "$transaction_dir/package-backup-snapshot.XXXXXX")"
    chmod 0600 "$temporary_state"
    chown root:root "$temporary_state"
    printf '%s\n' "snapshot=$snapshot" >"$temporary_state"
    mv -f "$temporary_state" "$snapshot_state"
}

write_snapshot_state

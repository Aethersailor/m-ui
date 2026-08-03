#!/bin/sh

set -eu

umask 077

usage() {
    cat <<'EOF'
Usage: prepare-data-root.sh [--check] [DIRECTORY]

Validates or prepares the single m-ui Docker persistence mount. The default is
/opt/m-ui/data. The directory must be a dedicated absolute path whose parent
components are root-controlled.
EOF
}

check_only=0
root=/opt/m-ui/data
root_set=0
while [ "$#" -gt 0 ]; do
    case "$1" in
        --check)
            check_only=1
            shift
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        --*)
            usage >&2
            exit 1
            ;;
        *)
            [ "$root_set" -eq 0 ] || {
                echo "only one data root may be supplied" >&2
                exit 1
            }
            root=$1
            root_set=1
            shift
            ;;
    esac
done

fail() {
    echo "m-ui data root: $*" >&2
    exit 1
}

[ "$(id -u)" -eq 0 ] || fail "must run as root"
case "$root" in
    /*) ;;
    *) fail "path must be absolute" ;;
esac
[ "$root" != / ] || fail "refusing the filesystem root"
case "$root" in
    /etc|/etc/*|/var|/var/*|/usr|/usr/*|/home|/home/*|/root|/root/*|\
    /bin|/bin/*|/sbin|/sbin/*|/lib|/lib/*|/lib64|/lib64/*|\
    /proc|/proc/*|/sys|/sys/*|/dev|/dev/*|/run|/run/*|/tmp|/tmp/*|\
    /mnt|/mnt/*|/media|/media/*)
        fail "refusing a system-wide directory; use /opt/m-ui/data or another dedicated path"
        ;;
esac
case "$root" in
    *'//'*) fail "path must not contain an empty component" ;;
    *'/./'*|*/.) fail "path must not contain a dot component" ;;
    *'/../'*|*/..) fail "path must not contain a parent component" ;;
esac

check_component() {
    path=$1
    require_root=$2
    if [ -L "$path" ]; then
        fail "$path is a symbolic link"
    fi
    if [ -e "$path" ] && [ ! -d "$path" ]; then
        fail "$path is not a directory"
    fi
    if [ -d "$path" ]; then
        mode=$(stat -c '%a' "$path")
        group=${mode%?}
        group=${group#"${group%?}"}
        other=${mode#"${mode%?}"}
        case "$group$other" in
            *[2367]*) fail "$path is writable by group or other users" ;;
        esac
        if [ "$require_root" -eq 1 ] && [ "$(stat -c '%u' "$path")" != 0 ]; then
            fail "$path is not owned by root"
        fi
    fi
}

parent=${root%/*}
walk=
remaining=${parent#/}
while [ -n "$remaining" ]; do
    component=${remaining%%/*}
    if [ "$remaining" = "$component" ]; then
        remaining=
    else
        remaining=${remaining#*/}
    fi
    walk=$walk/$component
    check_component "$walk" 1
    if [ ! -d "$walk" ]; then
        [ "$check_only" -eq 0 ] || fail "$walk does not exist"
        mkdir "$walk"
        chmod 0700 "$walk"
    fi
done

check_component "$root" 0
if [ ! -d "$root" ]; then
    [ "$check_only" -eq 0 ] || fail "$root does not exist"
    mkdir "$root"
fi

if find "$root" \( -type l -o -type b -o -type c -o -type p -o -type s \) \
    -print -quit | grep -q .; then
    fail "$root contains a symbolic link or special file"
fi

if [ "$check_only" -eq 1 ]; then
    [ "$(stat -c '%u:%g' "$root")" = 10001:10001 ] || \
        fail "$root must be owned by UID/GID 10001:10001"
    if find "$root" \( ! -uid 10001 -o ! -gid 10001 \) -print -quit | grep -q .; then
        fail "$root contains files not owned by UID/GID 10001:10001"
    fi
    exit 0
fi

mkdir -p \
    "$root/etc/m-ui" \
    "$root/etc/mihomo" \
    "$root/var/lib/m-ui/core/staging" \
    "$root/var/lib/m-ui/core/backups" \
    "$root/var/lib/mihomo"
find "$root" -type d -exec chown 10001:10001 {} +
find "$root" -type f -exec chown 10001:10001 {} +
chmod 0700 "$root" "$root/etc" "$root/var" "$root/var/lib"
chmod 0750 "$root/etc/m-ui" "$root/etc/mihomo" "$root/var/lib/mihomo"
chmod 0700 \
    "$root/var/lib/m-ui" \
    "$root/var/lib/m-ui/core" \
    "$root/var/lib/m-ui/core/staging" \
    "$root/var/lib/m-ui/core/backups"

echo "Prepared m-ui Docker data root: $root"

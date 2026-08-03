#!/bin/sh

set -eu

umask 077

usage() {
    cat <<'EOF'
Usage: prepare-data-root.sh [--check] [DIRECTORY]

Validates or creates the dedicated m-ui Docker persistence root. The default
is /opt/m-ui. The path must be an absolute, non-symlink directory outside
system-wide directories.
EOF
}

check_only=0
root="${M_UI_DATA_DIR:-/opt/m-ui}"
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
            root="$1"
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
[ "$root" != "/" ] || fail "refusing the filesystem root"
case "$root" in
    /etc|/etc/*|/var|/var/*|/usr|/usr/*|/home|/home/*|/root|/root/*|\
    /bin|/bin/*|/sbin|/sbin/*|/lib|/lib/*|/lib64|/lib64/*|\
    /proc|/proc/*|/sys|/sys/*|/dev|/dev/*|/run|/run/*|/tmp|/tmp/*|\
    /mnt|/mnt/*|/media|/media/*)
        fail "refusing a system-wide directory; choose a dedicated path such as /opt/m-ui"
        ;;
esac
case "$root" in
    *'//'*) fail "path must not contain an empty component" ;;
    *'/./'*|*/.) fail "path must not contain a dot component" ;;
    *'/../'*|*/..) fail "path must not contain a parent component" ;;
esac

check_component() {
    path="$1"
    if [ -L "$path" ]; then
        fail "$path is a symbolic link"
    fi
    if [ -e "$path" ] && [ ! -d "$path" ]; then
        fail "$path is not a directory"
    fi
    if [ -d "$path" ]; then
        mode="$(stat -c '%a' "$path")"
        group="${mode%?}"
        group="${group#"${group%?}"}"
        other="${mode#"${mode%?}"}"
        case "$group$other" in
            *[2367]*) fail "$path is writable by group or other users" ;;
        esac
    fi
}

check_control_component() {
    path="$1"
    check_component "$path"
    if [ -d "$path" ]; then
        owner="$(stat -c '%u' "$path")"
        [ "$owner" = 0 ] || fail "$path is not owned by root"
    fi
}

check_path_components() {
    path=""
    remaining="${1#/}"
    while [ -n "$remaining" ]; do
        component="${remaining%%/*}"
        if [ "$remaining" = "$component" ]; then
            remaining=""
        else
            remaining="${remaining#*/}"
        fi
        path="$path/$component"
        if [ -n "$remaining" ]; then
            check_control_component "$path"
        else
            check_component "$path"
        fi
    done
}

walk_path=""
remaining="${root#/}"
while [ -n "$remaining" ]; do
    component="${remaining%%/*}"
    if [ "$remaining" = "$component" ]; then
        remaining=""
    else
        remaining="${remaining#*/}"
    fi
    walk_path="$walk_path/$component"
    check_control_component "$walk_path"
    if [ ! -e "$walk_path" ] && [ "$check_only" -eq 0 ]; then
        mkdir "$walk_path"
        chmod 0700 "$walk_path"
    fi
done

for relative in \
    etc/m-ui \
    etc/mihomo \
    var/lib/m-ui \
    var/lib/mihomo
do
    path="$root/$relative"
    check_path_components "$path"
    if [ "$check_only" -eq 1 ]; then
        continue
    fi
    parent="${path%/*}"
    if [ ! -d "$parent" ]; then
        mkdir -p "$parent"
    fi
    check_component "$path"
    if [ ! -e "$path" ]; then
        mkdir "$path"
        chmod 0700 "$path"
    fi
    check_component "$path"
done

if [ "$check_only" -eq 1 ]; then
    exit 0
fi

echo "Prepared m-ui Docker data root: $root"

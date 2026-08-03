#!/bin/sh

set -eu

repository="Aethersailor/m-ui"

temporary_directory="$(mktemp -d)"
cleanup() {
    rm -rf "$temporary_directory"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

download() {
    url="$1"
    output="$2"
    case "$url" in
        https://github.com/Aethersailor/m-ui/*|https://api.github.com/repos/Aethersailor/m-ui/*)
            ;;
        *)
            printf '%s\n' "refusing non-official or non-HTTPS download URL" >&2
            exit 1
            ;;
    esac
    curl --fail --location \
        --proto '=https' --proto-redir '=https' --tlsv1.2 \
        --connect-timeout 10 --max-time 120 \
        --output "$output" "$url"
}

requested_version() {
    selected="latest"
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --version)
                [ "$#" -ge 2 ] || {
                    printf '%s\n' "--version requires a value" >&2
                    exit 1
                }
                selected="$2"
                shift 2
                ;;
            *)
                shift
                ;;
        esac
    done
    if [ "$selected" != "latest" ] &&
        ! printf '%s\n' "$selected" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'
    then
        printf '%s\n' "--version must be latest or vX.Y.Z" >&2
        exit 1
    fi
    printf '%s\n' "$selected"
}

resolve_latest_version() {
    metadata="$temporary_directory/release.json"
    download \
        "https://api.github.com/repos/$repository/releases/latest" \
        "$metadata"
    selected="$(sed -n \
        's/^[[:space:]]*"tag_name":[[:space:]]*"\(v[0-9][0-9.]*\)",[[:space:]]*$/\1/p' \
        "$metadata" | head -n 1)"
    printf '%s\n' "$selected" |
        grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || {
        printf '%s\n' "latest GitHub Release did not contain a strict vX.Y.Z tag" >&2
        exit 1
    }
    printf '%s\n' "$selected"
}

version="$(requested_version "$@")"
if [ "$version" = "latest" ]; then
    version="$(resolve_latest_version)"
fi
base_url="https://github.com/$repository/releases/download/$version"
download "$base_url/manage.sh" "$temporary_directory/manage.sh"
download "$base_url/SHA256SUMS" "$temporary_directory/SHA256SUMS"

expected="$(awk '
    $2 == "manage.sh" || $2 == "*manage.sh" { count++; value=$1 }
    END { if (count == 1) print value }
' \
    "$temporary_directory/SHA256SUMS")"
printf '%s\n' "$expected" |
    grep -Eq '^[0-9a-f]{64}$' || {
        printf '%s\n' "release SHA256SUMS does not contain manage.sh" >&2
        exit 1
    }
actual="$(sha256sum "$temporary_directory/manage.sh" | awk '{print $1}')"
[ "$actual" = "$expected" ] || {
    printf '%s\n' "SHA-256 verification failed for manage.sh" >&2
    exit 1
}

chmod 0755 "$temporary_directory/manage.sh"
# Keep the resolved tag last so an optional caller-supplied --version cannot
# turn the one-time latest lookup back into a moving-target install.
"$temporary_directory/manage.sh" install "$@" --version "$version"

#!/bin/sh

set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

repository="Aethersailor/m-ui"
deployment_directory="/opt/m-ui"
base_url="${M_UI_PUBLIC_URL:-}"

fail() {
    printf 'm-ui Docker install: %s\n' "$*" >&2
    exit 1
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --base-url)
            [ "$#" -ge 2 ] || fail "--base-url requires a value"
            base_url="$2"
            shift 2
            ;;
        --help|-h)
            printf '%s\n' \
                "Usage: install-docker.sh [--base-url http://SERVER_IP:2095]"
            exit 0
            ;;
        *)
            fail "unknown option: $1"
            ;;
    esac
done

[ "$(id -u)" -eq 0 ] || fail "run as root (for example: curl ... | sudo sh)"
command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v sha256sum >/dev/null 2>&1 || fail "sha256sum is required"
command -v docker >/dev/null 2>&1 || fail "Docker Engine is required"
docker compose version >/dev/null 2>&1 || fail "Docker Compose v2 is required"

case "$base_url" in
    "") ;;
    http://*|https://*) ;;
    *) fail "--base-url must be an absolute HTTP or HTTPS URL" ;;
esac

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
        *) fail "refusing a non-official download URL" ;;
    esac
    curl --fail --location \
        --proto '=https' --proto-redir '=https' --tlsv1.2 \
        --connect-timeout 10 --max-time 120 \
        --output "$output" "$url"
}

download \
    "https://api.github.com/repos/$repository/releases/latest" \
    "$temporary_directory/release.json"
version="$(sed -n \
    's/^[[:space:]]*"tag_name":[[:space:]]*"\(v[0-9][0-9.]*\)",[[:space:]]*$/\1/p' \
    "$temporary_directory/release.json" | head -n 1)"
printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' ||
    fail "latest GitHub Release did not contain a strict vX.Y.Z tag"

release_url="https://github.com/$repository/releases/download/$version"
download "$release_url/compose.yml" "$temporary_directory/compose.yml"
download "$release_url/SHA256SUMS" "$temporary_directory/SHA256SUMS"
expected="$(awk '
    $2 == "compose.yml" || $2 == "*compose.yml" { count++; value=$1 }
    END { if (count == 1) print value }
' "$temporary_directory/SHA256SUMS")"
printf '%s\n' "$expected" | grep -Eq '^[0-9a-f]{64}$' ||
    fail "release checksums do not contain compose.yml"
actual="$(sha256sum "$temporary_directory/compose.yml" | awk '{print $1}')"
[ "$actual" = "$expected" ] || fail "compose.yml SHA-256 verification failed"
grep -q '^    image: ghcr.io/aethersailor/m-ui:latest$' \
    "$temporary_directory/compose.yml" ||
    fail "release Compose does not use the canonical m-ui image"

install -d -o root -g root -m 0755 "$deployment_directory"
install -d -o 10001 -g 10001 -m 0700 "$deployment_directory/data"
install -o root -g root -m 0644 \
    "$temporary_directory/compose.yml" "$deployment_directory/compose.yml"

docker compose -f "$deployment_directory/compose.yml" pull
docker compose -f "$deployment_directory/compose.yml" up -d

attempt=0
while [ "$attempt" -lt 60 ]; do
    status="$(docker inspect --format \
        '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' \
        m-ui 2>/dev/null || true)"
    [ "$status" = healthy ] && break
    [ "$status" = exited ] && fail "container exited; inspect with docker compose logs m-ui"
    attempt=$((attempt + 1))
    sleep 2
done
[ "${status:-}" = healthy ] || fail "container did not become healthy within 120 seconds"

if [ -z "$base_url" ]; then
    server_ip="$(ip -4 route get 1.1.1.1 2>/dev/null |
        sed -n 's/.* src \([^ ]*\).*/\1/p' | head -n 1 || true)"
    if [ -z "$server_ip" ]; then
        server_ip="$(hostname -I 2>/dev/null | awk '{print $1}' || true)"
    fi
    [ -n "$server_ip" ] || server_ip="127.0.0.1"
    case "$server_ip" in
        *:*) base_url="http://[$server_ip]:2095" ;;
        *) base_url="http://$server_ip:2095" ;;
    esac
fi

setup_link="$(docker compose -f "$deployment_directory/compose.yml" exec -T m-ui \
    m-ui admin setup-link --base-url "$base_url" 2>/dev/null || true)"
case "$setup_link" in
    http://*/setup\#token=*|https://*/setup\#token=*)
        printf '%s\n' \
            "m-ui $version is healthy." \
            "Open this one-time link to create the administrator:" \
            "$setup_link"
        ;;
    *)
        printf '%s\n' \
            "m-ui $version is healthy." \
            "Open $base_url (administrator setup is already complete)."
        ;;
esac

#!/bin/sh

set -eu

fail() {
    printf 'm-ui smoke test: %s\n' "$*" >&2
    exit 1
}

[ "$(uname -s)" = "Linux" ] || fail "the real Mihomo smoke test requires Linux"
temporary_directory=""
cleanup() {
    if [ -n "$temporary_directory" ]; then
        rm -rf -- "$temporary_directory"
    fi
}
trap cleanup EXIT HUP INT TERM

if [ -n "${M_UI_TEST_MIHOMO_BINARY:-}" ]; then
    [ -x "$M_UI_TEST_MIHOMO_BINARY" ] ||
        fail "M_UI_TEST_MIHOMO_BINARY is not executable"
    mihomo_binary="$M_UI_TEST_MIHOMO_BINARY"
else
    for command in curl gzip jq mktemp sha256sum stat; do
        command -v "$command" >/dev/null 2>&1 ||
            fail "required command is missing: $command"
    done
    case "$(uname -m)" in
        x86_64|amd64) architecture="amd64" ;;
        aarch64|arm64) architecture="arm64" ;;
        *)
            fail "supported architectures are amd64 and arm64"
            ;;
    esac
    temporary_directory="$(mktemp -d)"
    bash scripts/resolve-mihomo.sh --output "$temporary_directory"
    bash scripts/resolve-mihomo.sh --output "$temporary_directory" \
        --architecture "$architecture"
    mihomo_binary="$temporary_directory/linux_${architecture}/mihomo"
fi

M_UI_TEST_MIHOMO_BINARY="$mihomo_binary" \
    go test ./internal/integration -count=1 -timeout=10m -v

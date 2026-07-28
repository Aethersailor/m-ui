#!/bin/sh

set -eu

MIHOMO_VERSION="${M_UI_TEST_MIHOMO_VERSION:-v1.19.29}"
MIHOMO_AMD64_SHA256="5612e698e96c8b8ad15abc4c0a4f098eba9234354b4f248cb97f2528e215b094"
MIHOMO_ARM64_SHA256="9a868b5e4e0ad91d9d71e1b41b0cfce78aaba44360c30df74a723f8e3926a86c"

fail() {
    printf 'm-ui smoke test: %s\n' "$*" >&2
    exit 1
}

[ "$(uname -s)" = "Linux" ] || fail "the real Mihomo smoke test requires Linux"
[ "$MIHOMO_VERSION" = "v1.19.29" ] ||
    fail "only the pinned Mihomo v1.19.29 release is supported"

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
    for command in awk curl gzip mktemp sha256sum; do
        command -v "$command" >/dev/null 2>&1 ||
            fail "required command is missing: $command"
    done
    case "$(uname -m)" in
        x86_64|amd64)
            asset="mihomo-linux-amd64-compatible-${MIHOMO_VERSION}.gz"
            expected="$MIHOMO_AMD64_SHA256"
            ;;
        aarch64|arm64)
            asset="mihomo-linux-arm64-${MIHOMO_VERSION}.gz"
            expected="$MIHOMO_ARM64_SHA256"
            ;;
        *)
            fail "supported architectures are amd64 and arm64"
            ;;
    esac
    temporary_directory="$(mktemp -d)"
    compressed="$temporary_directory/$asset"
    mihomo_binary="$temporary_directory/mihomo"
    curl --fail --location --silent --show-error \
        --proto '=https' --tlsv1.2 \
        --output "$compressed" \
        "https://github.com/MetaCubeX/mihomo/releases/download/${MIHOMO_VERSION}/${asset}"
    actual="$(sha256sum "$compressed" | awk '{print $1}')"
    [ "$actual" = "$expected" ] || fail "Mihomo SHA-256 verification failed"
    gzip -dc "$compressed" >"$mihomo_binary"
    chmod 0755 "$mihomo_binary"
fi

M_UI_TEST_MIHOMO_BINARY="$mihomo_binary" \
    go test ./internal/integration \
    -run '^TestGeneratedServerAndClientConfigurationsWithRealMihomo$' \
    -count=1 -v

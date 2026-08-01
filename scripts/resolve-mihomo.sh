#!/usr/bin/env bash

set -euo pipefail

output_directory="packaging/bootstrap"
user_agent="m-ui-release-builder"
architecture=""

while (($#)); do
  case "$1" in
    --output)
      [[ $# -ge 2 ]] || exit 2
      output_directory="$2"
      shift 2
      ;;
    --user-agent)
      [[ $# -ge 2 ]] || exit 2
      user_agent="$2"
      shift 2
      ;;
    --architecture)
      [[ $# -ge 2 ]] || exit 2
      architecture="$2"
      shift 2
      ;;
    *)
      echo "unknown option: $1" >&2
      exit 2
      ;;
  esac
done

for command in curl gzip jq sha256sum stat; do
  command -v "$command" >/dev/null || {
    echo "required command is missing: $command" >&2
    exit 1
  }
done
case "$architecture" in
  ""|amd64|arm64) ;;
  *)
    echo "architecture must be amd64 or arm64" >&2
    exit 2
    ;;
esac

temporary_directory="$(mktemp -d)"
cleanup() {
  rm -rf -- "$temporary_directory"
}
trap cleanup EXIT
umask 077
mkdir -p "$output_directory"

headers=(
  --header "Accept: application/vnd.github+json"
  --header "X-GitHub-Api-Version: 2022-11-28"
  --header "User-Agent: ${user_agent}"
)
if [[ -n "${M_UI_GITHUB_TOKEN:-}" ]]; then
  headers+=(--header "Authorization: Bearer ${M_UI_GITHUB_TOKEN}")
fi

identity_json="${output_directory}/identity.json"
if [[ -z "$architecture" ]]; then
  release_json="${temporary_directory}/release.json"
  curl --fail --silent --show-error --location \
    --proto '=https' --proto-redir '=https' \
    --connect-timeout 10 --max-time 30 \
    "${headers[@]}" \
    --output "$release_json" \
    "https://api.github.com/repos/MetaCubeX/mihomo/releases/latest"
  [[ "$(jq -r '.draft' "$release_json")" == "false" ]]
  [[ "$(jq -r '.prerelease' "$release_json")" == "false" ]]
  [[ "$(jq -r '.id' "$release_json")" =~ ^[1-9][0-9]*$ ]]
  [[ "$(jq -r '.tag_name' "$release_json")" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9.-]+)?$ ]]

  jq '{
    channel: "release",
    repository: "MetaCubeX/mihomo",
    release_id: .id,
    tag_name,
    prerelease,
    published_at,
    target_commitish,
    assets: [
      .assets[] |
      select(.name | test("^mihomo-linux-(amd64-compatible|arm64)-[^/]+[.]gz$")) |
      select((.name | contains("-go")) | not) |
      select((.name | ascii_downcase | contains("debug")) | not) |
      {
        id,
        name,
        size,
        digest,
        browser_download_url
      }
    ]
  }' "$release_json" >"$identity_json"
  [[ "$(jq '.assets | length' "$identity_json")" == "2" ]]
  for arch in amd64 arm64; do
    if [[ "$arch" == "amd64" ]]; then
      regex='^mihomo-linux-amd64-compatible-[^/]+[.]gz$'
    else
      regex='^mihomo-linux-arm64-[^/]+[.]gz$'
    fi
    [[ "$(jq --arg regex "$regex" \
      '[.assets[] | select(.name | test($regex))] | length' \
      "$identity_json")" == "1" ]]
  done
  printf 'Locked Mihomo %s (Release ID %s).\n' \
    "$(jq -r '.tag_name' "$identity_json")" \
    "$(jq -r '.release_id' "$identity_json")"
  exit 0
fi

[[ -f "$identity_json" ]] || {
  echo "locked identity.json is required before preparing an architecture" >&2
  exit 1
}
[[ "$(jq -r '.repository' "$identity_json")" == "MetaCubeX/mihomo" ]]
[[ "$(jq -r '.channel' "$identity_json")" == "release" ]]
[[ "$(jq -r '.prerelease' "$identity_json")" == "false" ]]
if [[ "$architecture" == "amd64" ]]; then
  asset_regex='^mihomo-linux-amd64-compatible-[^/]+[.]gz$'
else
  asset_regex='^mihomo-linux-arm64-[^/]+[.]gz$'
fi
asset="$(jq -c --arg regex "$asset_regex" \
  '.assets[] | select(.name | test($regex))' "$identity_json")"
[[ -n "$asset" ]]
asset_id="$(jq -r '.id' <<<"$asset")"
asset_name="$(jq -r '.name' <<<"$asset")"
asset_size="$(jq -r '.size' <<<"$asset")"
asset_digest="$(jq -r '.digest // ""' <<<"$asset")"
asset_url="$(jq -r '.browser_download_url' <<<"$asset")"
asset_sha256="${asset_digest#sha256:}"
[[ "$asset_sha256" =~ ^[0-9a-f]{64}$ ]]
[[ "$asset_size" =~ ^[1-9][0-9]*$ ]] && ((asset_size <= 134217728))
[[ "$asset_url" == https://github.com/MetaCubeX/mihomo/releases/download/* ]]

architecture_directory="${output_directory}/linux_${architecture}"
rm -rf -- "$architecture_directory"
mkdir -p "$architecture_directory"
archive="${temporary_directory}/${asset_name}"
curl --fail --silent --show-error --location \
  --proto '=https' --proto-redir '=https' \
  --connect-timeout 10 --max-time 180 \
  --header "User-Agent: ${user_agent}" \
  --output "$archive" "$asset_url"
[[ "$(stat -c '%s' "$archive")" == "$asset_size" ]]
printf '%s  %s\n' "$asset_sha256" "$archive" | sha256sum --check --status

binary="${architecture_directory}/mihomo"
gzip --decompress --stdout "$archive" >"$binary"
binary_size="$(stat -c '%s' "$binary")"
((binary_size > 0 && binary_size <= 268435456))
chmod 0755 "$binary"
binary_sha256="$(sha256sum "$binary" | awk '{print $1}')"
binary_version="$("$binary" -v | tr -d '\r' | head -n 1)"
[[ -n "$binary_version" ]]

jq -n \
  --arg channel release \
  --arg repository MetaCubeX/mihomo \
  --argjson release_id "$(jq '.release_id' "$identity_json")" \
  --arg tag_name "$(jq -r '.tag_name' "$identity_json")" \
  --arg published_at "$(jq -r '.published_at' "$identity_json")" \
  --arg target_commitish "$(jq -r '.target_commitish // ""' "$identity_json")" \
  --argjson asset_id "$asset_id" \
  --arg asset_name "$asset_name" \
  --argjson asset_size "$asset_size" \
  --arg asset_digest_sha256 "$asset_sha256" \
  --arg binary_reported_version "$binary_version" \
  --arg binary_sha256 "$binary_sha256" \
  --argjson binary_size "$binary_size" \
  --arg installed_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  '{
    schema_version: 1,
    source: "bootstrap",
    verified_source: true,
    identity: {
      channel: $channel,
      repository: $repository,
      release_id: $release_id,
      tag_name: $tag_name,
      prerelease: false,
      published_at: $published_at,
      target_commitish: $target_commitish,
      asset_id: $asset_id,
      asset_name: $asset_name,
      asset_size: $asset_size,
      asset_digest_sha256: $asset_digest_sha256,
      binary_reported_version: $binary_reported_version
    },
    compressed_sha256: $asset_digest_sha256,
    binary_sha256: $binary_sha256,
    binary_size: $binary_size,
    binary_reported_version: $binary_reported_version,
    installed_at: $installed_at
  }' >"${architecture_directory}/manifest.json"

printf 'Prepared native linux/%s bootstrap for Mihomo %s.\n' \
  "$architecture" "$(jq -r '.tag_name' "$identity_json")"

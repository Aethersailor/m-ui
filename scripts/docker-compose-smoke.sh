#!/usr/bin/env bash

set -euo pipefail

image="${1:?usage: docker-compose-smoke.sh IMAGE}"
repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tag="compose-smoke-${RANDOM}-${RANDOM}"
if [[ "$EUID" -eq 0 ]]; then
  root_command=()
else
  command -v sudo >/dev/null 2>&1 || {
    echo "docker-compose-smoke.sh requires root or sudo" >&2
    exit 1
  }
  root_command=(sudo)
fi
data_directory="$("${root_command[@]}" mktemp -d /opt/m-ui-compose-smoke.XXXXXX)"
"${root_command[@]}" chmod 0700 "$data_directory"
"${root_command[@]}" cp "$repository_root/deploy/docker/compose.yml" \
  "$data_directory/compose.yml"
compose_file="$data_directory/compose.yml"

docker_root() {
  if [[ "$EUID" -eq 0 ]]; then
    docker "$@"
  else
    sudo docker "$@"
  fi
}

compose() {
  if [[ "$EUID" -eq 0 ]]; then
    M_UI_DATA_DIR="$data_directory" \
    M_UI_IMAGE=m-ui \
    M_UI_IMAGE_TAG="$tag" \
      docker compose -f "$compose_file" "$@"
  else
    sudo env \
      M_UI_DATA_DIR="$data_directory" \
      M_UI_IMAGE=m-ui \
      M_UI_IMAGE_TAG="$tag" \
      docker compose -f "$compose_file" "$@"
  fi
}

cleanup() {
  compose down --remove-orphans >/dev/null 2>&1 || true
  docker_root image rm -f "m-ui:$tag" >/dev/null 2>&1 || true
  "${root_command[@]}" rm -rf -- "$data_directory"
}
trap cleanup EXIT

docker_root tag "$image" "m-ui:$tag"
compose config >/dev/null
compose up -d

for _ in $(seq 1 60); do
  health="$(docker_root inspect --format '{{.State.Health.Status}}' m-ui 2>/dev/null || true)"
  [[ "$health" == "healthy" ]] && break
  [[ "$health" != "unhealthy" ]] || {
    compose logs --no-color
    exit 1
  }
  sleep 1
done
[[ "$(docker_root inspect --format '{{.State.Health.Status}}' m-ui)" == "healthy" ]] || {
  compose logs --no-color
  exit 1
}
[[ "$(docker_root inspect --format '{{.State.Status}}:{{.State.ExitCode}}' \
  m-ui-data-init)" == "exited:0" ]]
[[ "$(docker_root exec m-ui id -u)" == "10001" ]]
[[ "$("${root_command[@]}" stat -c '%a' "$data_directory")" == "711" ]]

for path in \
  etc/m-ui \
  etc/mihomo \
  var/lib/m-ui \
  var/lib/mihomo
do
  "${root_command[@]}" test -d "$data_directory/$path"
  [[ "$("${root_command[@]}" stat -c '%u:%g' "$data_directory/$path")" == \
    "10001:10001" ]]
done

docker_root exec m-ui m-ui core status --json \
  --config /etc/m-ui/config.toml >/dev/null
if docker_root exec m-ui test -e /usr/lib/m-ui/manage.sh; then
  echo "container unexpectedly contains the native lifecycle manager" >&2
  exit 1
fi

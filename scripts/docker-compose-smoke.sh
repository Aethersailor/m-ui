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
test_root="$("${root_command[@]}" mktemp -d /opt/m-ui-compose-smoke.XXXXXX)"
data_directory="$test_root/data"
compose_file="$test_root/compose.yml"
override_file="$test_root/compose.test.yml"
"${root_command[@]}" cp "$repository_root/deploy/docker/compose.yml" "$compose_file"
"${root_command[@]}" bash \
  "$repository_root/deploy/docker/prepare-data-root.sh" "$data_directory" >/dev/null
"${root_command[@]}" tee "$override_file" >/dev/null <<EOF
services:
  m-ui:
    image: m-ui:$tag
    volumes:
      - $data_directory:/data
EOF

docker_root() {
  if [[ "$EUID" -eq 0 ]]; then
    docker "$@"
  else
    sudo docker "$@"
  fi
}

compose() {
  docker_root compose -f "$compose_file" -f "$override_file" "$@"
}

cleanup() {
  compose down --remove-orphans >/dev/null 2>&1 || true
  docker_root image rm -f "m-ui:$tag" >/dev/null 2>&1 || true
  "${root_command[@]}" rm -rf -- "$test_root"
}
trap cleanup EXIT

docker_root tag "$image" "m-ui:$tag"
[[ "$(compose config --services)" == m-ui ]]
[[ "$(compose config --images)" == "m-ui:$tag" ]]
compose up -d --pull never

for _ in $(seq 1 60); do
  health="$(docker_root inspect --format '{{.State.Health.Status}}' m-ui 2>/dev/null || true)"
  [[ "$health" == healthy ]] && break
  [[ "$health" != unhealthy ]] || {
    compose logs --no-color
    exit 1
  }
  sleep 1
done
[[ "$(docker_root inspect --format '{{.State.Health.Status}}' m-ui)" == healthy ]] || {
  compose logs --no-color
  exit 1
}
[[ "$(docker_root exec m-ui id -u)" == 10001 ]]
[[ "$(docker_root inspect --format '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Source}}{{end}}{{end}}' m-ui)" == "$data_directory" ]]
[[ "$(docker_root inspect --format '{{.HostConfig.RestartPolicy.Name}}' m-ui)" == always ]]
[[ "$(docker_root inspect --format '{{.HostConfig.NetworkMode}}' m-ui)" == host ]]

for path in \
  etc/m-ui/config.toml \
  etc/mihomo/config.yaml \
  var/lib/m-ui/m-ui.db \
  var/lib/m-ui/master.key \
  var/lib/m-ui/core/current/mihomo \
  var/lib/mihomo
do
  "${root_command[@]}" test -e "$data_directory/$path"
done

key_hash="$("${root_command[@]}" sha256sum \
  "$data_directory/var/lib/m-ui/master.key" | awk '{print $1}')"
old_id="$(docker_root inspect --format '{{.Id}}' m-ui)"
compose up -d --pull never --force-recreate
new_id="$(docker_root inspect --format '{{.Id}}' m-ui)"
[[ "$new_id" != "$old_id" ]]
for _ in $(seq 1 60); do
  [[ "$(docker_root inspect --format '{{.State.Health.Status}}' m-ui)" == healthy ]] &&
    break
  sleep 1
done
[[ "$(docker_root inspect --format '{{.State.Health.Status}}' m-ui)" == healthy ]]
[[ "$("${root_command[@]}" sha256sum "$data_directory/var/lib/m-ui/master.key" |
  awk '{print $1}')" == "$key_hash" ]]

docker_root exec m-ui m-ui core status --json \
  --config /etc/m-ui/config.toml >/dev/null
if docker_root exec m-ui test -e /usr/lib/m-ui/manage.sh; then
  echo "container unexpectedly contains the native lifecycle manager" >&2
  exit 1
fi

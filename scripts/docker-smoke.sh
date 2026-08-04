#!/usr/bin/env bash

set -euo pipefail

image="${1:?usage: docker-smoke.sh IMAGE}"
expected_version="${M_UI_EXPECTED_VERSION:-}"
expected_revision="${M_UI_EXPECTED_REVISION:-}"
repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
name="m-ui-smoke-${RANDOM}-${RANDOM}"
if [[ "$EUID" -eq 0 ]]; then
  root_command=()
else
  command -v sudo >/dev/null 2>&1 || {
    echo "docker-smoke.sh requires root or sudo" >&2
    exit 1
  }
  root_command=(sudo)
fi

docker_root() {
  if [[ "$EUID" -eq 0 ]]; then
    docker "$@"
  else
    sudo docker "$@"
  fi
}

data_directory="$("${root_command[@]}" mktemp -d /opt/m-ui-smoke.XXXXXX)"
unsafe_directory="$("${root_command[@]}" mktemp -d /opt/m-ui-smoke-unsafe.XXXXXX)"
unsafe_target="$("${root_command[@]}" mktemp -d /opt/m-ui-smoke-target.XXXXXX)"
client_directory="$(mktemp -d)"
migrated_directory="${data_directory}.migrated"

cleanup() {
  docker_root rm -f "$name" >/dev/null 2>&1 || true
  "${root_command[@]}" rm -rf -- \
    "$data_directory" "$migrated_directory" \
    "$unsafe_directory" "$unsafe_target"
  rm -rf -- "$client_directory"
}
trap cleanup EXIT

"${root_command[@]}" bash \
  "$repository_root/deploy/docker/prepare-data-root.sh" "$data_directory" >/dev/null
"${root_command[@]}" chown 10001:10001 "$unsafe_directory"
"${root_command[@]}" chmod 0700 "$unsafe_directory"
"${root_command[@]}" mkdir -p "$unsafe_directory/etc"
"${root_command[@]}" chown 10001:10001 "$unsafe_directory/etc"
"${root_command[@]}" ln -s "$unsafe_target" "$unsafe_directory/etc/m-ui"

user="$(docker_root image inspect --format '{{.Config.User}}' "$image")"
[[ "$user" == "10001:10001" ]]
[[ "$(docker_root image inspect --format '{{.Config.WorkingDir}}' "$image")" == /data ]]
for label in org.opencontainers.image.source \
  org.opencontainers.image.revision org.opencontainers.image.version \
  org.opencontainers.image.licenses org.opencontainers.image.created
do
  value="$(docker_root image inspect \
    --format "{{index .Config.Labels \"${label}\"}}" "$image")"
  [[ -n "$value" && "$value" != "<no value>" ]]
done
if [[ -n "$expected_version" ]]; then
  actual_version="$(docker_root image inspect --format \
    '{{index .Config.Labels "org.opencontainers.image.version"}}' "$image")"
  [[ "$actual_version" == "$expected_version" ]]
fi
if [[ -n "$expected_revision" ]]; then
  actual_revision="$(docker_root image inspect --format \
    '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$image")"
  [[ "$actual_revision" == "$expected_revision" ]]
fi

if docker_root run --rm --network none \
  --cap-drop ALL --cap-add NET_BIND_SERVICE \
  --security-opt no-new-privileges \
  -v "$unsafe_directory:/data" \
  "$image" >/dev/null 2>&1
then
  echo "container accepted a symbolic-link persistence subtree" >&2
  exit 1
fi
[[ -z "$("${root_command[@]}" find "$unsafe_target" -mindepth 1 -print -quit)" ]]

docker_root run -d --name "$name" \
  --cap-drop ALL --cap-add NET_BIND_SERVICE \
  --security-opt no-new-privileges \
  --network host \
  -e TZ=Asia/Shanghai \
  -v "$data_directory:/data" \
  "$image" >/dev/null

for _ in $(seq 1 60); do
  health="$(docker_root inspect --format '{{.State.Health.Status}}' "$name")"
  [[ "$health" == healthy ]] && break
  [[ "$health" != unhealthy ]] || {
    docker_root logs "$name"
    exit 1
  }
  sleep 1
done
if [[ "$(docker_root inspect --format '{{.State.Health.Status}}' "$name")" != healthy ]]; then
  docker_root inspect "$name"
  docker_root logs "$name"
  docker_root exec "$name" sh -c \
    'pidof mihomo || true; /var/lib/m-ui/core/current/mihomo -t -d /var/lib/mihomo -f /etc/mihomo/config.yaml || true'
  docker_root exec "$name" m-ui doctor --config /etc/m-ui/config.toml || true
  exit 1
fi

[[ "$(docker_root exec "$name" id -u)" == 10001 ]]
# The following script is intentionally expanded inside the container.
# shellcheck disable=SC2016
docker_root exec "$name" sh -c '
  test "$(readlink /etc/m-ui)" = /data/etc/m-ui
  test "$(readlink /etc/mihomo)" = /data/etc/mihomo
  test "$(readlink /var/lib/m-ui)" = /data/var/lib/m-ui
  test "$(readlink /var/lib/mihomo)" = /data/var/lib/mihomo
  for pid in $(pidof m-ui) $(pidof mihomo); do
    test "$(awk "/^Uid:/{print \$2}" /proc/$pid/status)" = 10001
  done
'
docker_root exec "$name" grep -Fq \
  'database_path = "/data/var/lib/m-ui/m-ui.db"' /data/etc/m-ui/config.toml
docker_root exec "$name" grep -Fq \
  'listen_address = "0.0.0.0"' /data/etc/m-ui/config.toml
docker_root exec "$name" grep -Fq \
  'config_path = "/data/etc/mihomo/config.yaml"' /data/etc/m-ui/config.toml
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

docker_root exec "$name" m-ui core status --json \
  --config /etc/m-ui/config.toml >/dev/null
if [[ -n "$expected_version" ]]; then
  curl --fail --silent --show-error \
    http://127.0.0.1:2095/api/v1/health |
    grep -Fq "\"version\":\"${expected_version}\""
fi
if docker_root exec "$name" test -e /usr/lib/m-ui/manage.sh; then
  echo "container unexpectedly contains the native lifecycle manager" >&2
  exit 1
fi
docker_root exec "$name" /var/lib/m-ui/core/current/mihomo -v >/dev/null
docker_root exec "$name" /var/lib/m-ui/core/current/mihomo \
  -t -f /etc/mihomo/config.yaml >/dev/null
docker_root exec "$name" grep -q \
  '^external-controller: 127.0.0.1:9090$' /etc/mihomo/config.yaml

setup_link="$(docker_root exec "$name" m-ui admin setup-link \
  --config /etc/m-ui/config.toml)"
setup_token="${setup_link##*#token=}"
[[ "$setup_link" == *"#token="* && -n "$setup_token" ]]
setup_response="$(curl --fail --silent --show-error \
  -H 'Origin: http://127.0.0.1:2095' \
  -H 'Content-Type: application/json' \
  -H "X-M-UI-Setup-Token: $setup_token" \
  -c "$client_directory/cookies.txt" \
  -d '{"username":"admin","password":"Synthetic-Smoke-Password-2026!"}' \
  http://127.0.0.1:2095/api/v1/setup/complete)"
csrf_token="$(jq -er '.csrf_token | select(length > 0)' <<<"$setup_response")"
curl --fail --silent --show-error \
  -b "$client_directory/cookies.txt" \
  http://127.0.0.1:2095/api/v1/setup/status |
  grep -q '"state":"complete"'

endpoint_state="$(curl --fail --silent --show-error \
  -b "$client_directory/cookies.txt" \
  http://127.0.0.1:2095/api/v1/settings/endpoints)"
endpoint_payload="$(jq -c '
  .active |
  .panel_ui_bind.port = 2195 |
  {
    panel_ui_bind,
    mihomo_external_controller_bind,
    mihomo_controller_connect,
    external_controller_cors_origins,
    generation
  }
' <<<"$endpoint_state")"
endpoint_update="$(curl --fail --silent --show-error \
  -X PUT \
  -H 'Origin: http://127.0.0.1:2095' \
  -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $csrf_token" \
  -b "$client_directory/cookies.txt" \
  -d "$endpoint_payload" \
  http://127.0.0.1:2095/api/v1/settings/endpoints)"
jq -e '.pending.requires_mui_restart == true' <<<"$endpoint_update" >/dev/null

old_pid="$(docker_root exec "$name" pidof mihomo)"
docker_root exec "$name" sh -c "kill ${old_pid}"
for _ in $(seq 1 30); do
  new_pid="$(docker_root exec "$name" pidof mihomo 2>/dev/null || true)"
  [[ -n "$new_pid" && "$new_pid" != "$old_pid" ]] && break
  sleep 1
done
[[ -n "${new_pid:-}" && "$new_pid" != "$old_pid" ]]

key_hash_before="$("${root_command[@]}" sha256sum \
  "$data_directory/var/lib/m-ui/master.key" | awk '{print $1}')"
docker_root restart "$name" >/dev/null
for _ in $(seq 1 60); do
  [[ "$(docker_root inspect --format '{{.State.Health.Status}}' "$name")" == healthy ]] &&
    break
  sleep 1
done
[[ "$(docker_root inspect --format '{{.State.Health.Status}}' "$name")" == healthy ]]
[[ "$("${root_command[@]}" sha256sum "$data_directory/var/lib/m-ui/master.key" |
  awk '{print $1}')" == "$key_hash_before" ]]
docker_root exec "$name" m-ui core status --json \
  --config /etc/m-ui/config.toml |
  grep -q '"channel": "release"'
curl --fail --silent --show-error \
  http://127.0.0.1:2195/api/v1/setup/status |
  grep -q '"state":"complete"'

docker_root stop --time 15 "$name" >/dev/null
[[ "$(docker_root inspect --format '{{.State.ExitCode}}' "$name")" == 0 ]]

source_key_hash="$("${root_command[@]}" sha256sum \
  "$data_directory/var/lib/m-ui/master.key" | awk '{print $1}')"
"${root_command[@]}" bash "$repository_root/deploy/docker/migrate-volumes.sh" \
  --source-root "$data_directory" \
  --target "$migrated_directory" \
  --image "$image" \
  --dry-run >/dev/null
"${root_command[@]}" bash "$repository_root/deploy/docker/migrate-volumes.sh" \
  --source-root "$data_directory" \
  --target "$migrated_directory" \
  --image "$image" \
  --yes >/dev/null
[[ "$("${root_command[@]}" sha256sum "$data_directory/var/lib/m-ui/master.key" |
  awk '{print $1}')" == "$source_key_hash" ]]
[[ "$("${root_command[@]}" sha256sum "$migrated_directory/var/lib/m-ui/master.key" |
  awk '{print $1}')" == "$source_key_hash" ]]

docker_root rm "$name" >/dev/null
docker_root run -d --name "$name" \
  --cap-drop ALL --cap-add NET_BIND_SERVICE \
  --security-opt no-new-privileges \
  --network host \
  -e TZ=Asia/Shanghai \
  -v "$migrated_directory:/data" \
  "$image" >/dev/null
for _ in $(seq 1 60); do
  [[ "$(docker_root inspect --format '{{.State.Health.Status}}' "$name")" == healthy ]] &&
    break
  sleep 1
done
[[ "$(docker_root inspect --format '{{.State.Health.Status}}' "$name")" == healthy ]]
curl --fail --silent --show-error \
  http://127.0.0.1:2195/api/v1/setup/status |
  grep -q '"state":"complete"'
docker_root stop --time 15 "$name" >/dev/null
[[ "$(docker_root inspect --format '{{.State.ExitCode}}' "$name")" == 0 ]]

history="$(docker_root history --no-trunc "$image")"
if grep -Eiq '(GITHUB_TOKEN|M_UI_GITHUB_TOKEN|npm cache|go-build)' <<<"$history"; then
  exit 1
fi
docker_root run --rm --entrypoint /bin/sh "$image" -c \
  'test ! -e /usr/local/go && test ! -e /root/.npm && test ! -e /src'

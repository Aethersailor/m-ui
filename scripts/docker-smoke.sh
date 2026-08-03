#!/usr/bin/env bash

set -euo pipefail

image="${1:?usage: docker-smoke.sh IMAGE}"
expected_version="${M_UI_EXPECTED_VERSION:-}"
expected_revision="${M_UI_EXPECTED_REVISION:-}"
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
data_directory="$(mktemp -d /opt/m-ui-smoke.XXXXXX)"
unsafe_directory="$(mktemp -d /opt/m-ui-smoke-unsafe.XXXXXX)"
unsafe_target="$(mktemp -d /opt/m-ui-smoke-target.XXXXXX)"
chmod 0700 "$data_directory"
mkdir -p "$unsafe_directory/etc"
ln -s "$unsafe_target" "$unsafe_directory/etc/m-ui"

cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  "${root_command[@]}" rm -rf -- \
    "$data_directory" "$unsafe_directory" "$unsafe_target"
}
trap cleanup EXIT

user="$(docker image inspect --format '{{.Config.User}}' "$image")"
[[ "$user" == "10001:10001" ]]
for label in org.opencontainers.image.source \
  org.opencontainers.image.revision org.opencontainers.image.version \
  org.opencontainers.image.licenses org.opencontainers.image.created
do
  value="$(docker image inspect \
    --format "{{index .Config.Labels \"${label}\"}}" "$image")"
  [[ -n "$value" && "$value" != "<no value>" ]]
done
if [[ -n "$expected_version" ]]; then
  actual_version="$(docker image inspect --format \
    '{{index .Config.Labels "org.opencontainers.image.version"}}' "$image")"
  [[ "$actual_version" == "$expected_version" ]]
fi
if [[ -n "$expected_revision" ]]; then
  actual_revision="$(docker image inspect --format \
    '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$image")"
  [[ "$actual_revision" == "$expected_revision" ]]
fi

if docker run --rm --user 0:0 --network none \
  --cap-drop ALL --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER \
  --security-opt no-new-privileges \
  -e M_UI_INIT_DATA=1 \
  -v "$unsafe_directory:/data" \
  "$image" >/dev/null 2>&1
then
  echo "data initializer accepted a symbolic-link subtree" >&2
  exit 1
fi
[[ -z "$(find "$unsafe_target" -mindepth 1 -print -quit)" ]]

docker run --rm --user 0:0 --network none \
  --cap-drop ALL --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER \
  --security-opt no-new-privileges \
  -e M_UI_INIT_DATA=1 \
  -v "$data_directory:/data" \
  "$image" >/dev/null
docker run -d --name "$name" \
  --cap-drop ALL --cap-add NET_BIND_SERVICE \
  --security-opt no-new-privileges \
  --network host \
  -e M_UI_MIHOMO_CHANNEL=release \
  -e M_UI_MIHOMO_AUTO_UPDATE=off \
  -e M_UI_MIHOMO_CHECK_INTERVAL=24h \
  -v "$data_directory/etc/m-ui:/etc/m-ui" \
  -v "$data_directory/etc/mihomo:/etc/mihomo" \
  -v "$data_directory/var/lib/m-ui:/var/lib/m-ui" \
  -v "$data_directory/var/lib/mihomo:/var/lib/mihomo" \
  "$image" >/dev/null

for _ in $(seq 1 60); do
  health="$(docker inspect --format '{{.State.Health.Status}}' "$name")"
  [[ "$health" == "healthy" ]] && break
  [[ "$health" != "unhealthy" ]] || {
    docker logs "$name"
    exit 1
  }
  sleep 1
done
if [[ "$(docker inspect --format '{{.State.Health.Status}}' "$name")" != "healthy" ]]; then
  docker inspect "$name"
  docker logs "$name"
  docker exec "$name" sh -c 'pidof mihomo || true; /var/lib/m-ui/core/current/mihomo -t -d /var/lib/mihomo -f /etc/mihomo/config.yaml || true'
  docker exec "$name" m-ui doctor --config /etc/m-ui/config.toml || true
  exit 1
fi
docker exec "$name" m-ui core status --json \
  --config /etc/m-ui/config.toml >/dev/null
if [[ -n "$expected_version" ]]; then
  curl --fail --silent --show-error \
    http://127.0.0.1:2095/api/v1/health |
    grep -Fq "\"version\":\"${expected_version}\""
fi
if docker exec "$name" test -e /usr/lib/m-ui/manage.sh; then
  echo "container unexpectedly contains the native lifecycle manager" >&2
  exit 1
fi
docker exec "$name" /var/lib/m-ui/core/current/mihomo -v >/dev/null
docker exec "$name" /var/lib/m-ui/core/current/mihomo \
  -t -f /etc/mihomo/config.yaml >/dev/null
docker exec "$name" grep -q \
  '^external-controller: 127.0.0.1:9090$' /etc/mihomo/config.yaml

setup_link="$(docker exec "$name" m-ui admin setup-link \
  --config /etc/m-ui/config.toml)"
setup_token="${setup_link##*#token=}"
[[ "$setup_link" == *"#token="* && -n "$setup_token" ]]
curl --fail --silent --show-error \
  -H 'Origin: http://127.0.0.1:2095' \
  -H 'Content-Type: application/json' \
  -H "X-M-UI-Setup-Token: $setup_token" \
  -c "$data_directory/cookies.txt" \
  -d '{"username":"admin","password":"Synthetic-Smoke-Password-2026!"}' \
  http://127.0.0.1:2095/api/v1/setup/complete >/dev/null
curl --fail --silent --show-error \
  -b "$data_directory/cookies.txt" \
  http://127.0.0.1:2095/api/v1/setup/status |
  grep -q '"state":"complete"'

old_pid="$(docker exec "$name" pidof mihomo)"
docker exec "$name" sh -c "kill ${old_pid}"
for _ in $(seq 1 30); do
  new_pid="$(docker exec "$name" pidof mihomo 2>/dev/null || true)"
  [[ -n "$new_pid" && "$new_pid" != "$old_pid" ]] && break
  sleep 1
done
[[ -n "${new_pid:-}" && "$new_pid" != "$old_pid" ]]

docker restart "$name" >/dev/null
for _ in $(seq 1 60); do
  [[ "$(docker inspect --format '{{.State.Health.Status}}' "$name")" == "healthy" ]] &&
    break
  sleep 1
done
docker exec "$name" m-ui core status --json \
  --config /etc/m-ui/config.toml |
  grep -q '"channel": "release"'

docker stop --time 15 "$name" >/dev/null
[[ "$(docker inspect --format '{{.State.ExitCode}}' "$name")" == "0" ]]

history="$(docker history --no-trunc "$image")"
if grep -Eiq '(GITHUB_TOKEN|M_UI_GITHUB_TOKEN|npm cache|go-build)' <<<"$history"; then
  exit 1
fi
docker run --rm --entrypoint /bin/sh "$image" -c \
  'test ! -e /usr/local/go && test ! -e /root/.npm && test ! -e /src'

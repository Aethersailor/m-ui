#!/usr/bin/env bash

set -euo pipefail

image="${1:?usage: docker-smoke.sh IMAGE}"
name="m-ui-smoke-${RANDOM}-${RANDOM}"
volumes=()

cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  for volume in "${volumes[@]}"; do
    docker volume rm "$volume" >/dev/null 2>&1 || true
  done
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

for suffix in etc mihomo-etc data mihomo-data; do
  volume="${name}-${suffix}"
  docker volume create "$volume" >/dev/null
  volumes+=("$volume")
done
secret_volume="${name}-secret"
docker volume create "$secret_volume" >/dev/null
volumes+=("$secret_volume")
docker run --rm --user 10001:10001 --entrypoint /bin/sh \
  -v "$secret_volume:/run/secrets" "$image" \
  -c 'umask 077; printf "%s\\n" "Synthetic-Smoke-Password-2026!" > /run/secrets/admin_password; test "$(stat -c "%a" /run/secrets/admin_password)" = 600'

docker run -d --name "$name" \
  --cap-drop ALL --cap-add NET_BIND_SERVICE \
  --security-opt no-new-privileges \
  --network host \
  -e M_UI_ADMIN_PASSWORD_FILE=/run/secrets/admin_password \
  -e M_UI_MIHOMO_CHANNEL=release \
  -e M_UI_MIHOMO_AUTO_UPDATE=off \
  -e M_UI_MIHOMO_CHECK_INTERVAL=24h \
  -v "$secret_volume:/run/secrets:ro" \
  -v "${volumes[0]}:/etc/m-ui" \
  -v "${volumes[1]}:/etc/mihomo" \
  -v "${volumes[2]}:/var/lib/m-ui" \
  -v "${volumes[3]}:/var/lib/mihomo" \
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
[[ "$(docker inspect --format '{{.State.Health.Status}}' "$name")" == "healthy" ]]
docker exec "$name" m-ui core status --json \
  --config /etc/m-ui/config.toml >/dev/null
docker exec "$name" /var/lib/m-ui/core/current/mihomo -v >/dev/null
docker exec "$name" /var/lib/m-ui/core/current/mihomo \
  -t -f /etc/mihomo/config.yaml >/dev/null
docker exec "$name" grep -q \
  '^external-controller: 127.0.0.1:9090$' /etc/mihomo/config.yaml

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

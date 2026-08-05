#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temporary_directory="$(mktemp -d)"
cleanup() {
  rm -rf -- "$temporary_directory"
}
trap cleanup EXIT

bash "$repository_root/scripts/install-test.sh"

grep -q '^    image: ghcr.io/aethersailor/m-ui:latest$' \
  "$repository_root/deploy/docker/compose.yml"
grep -q '^      - /opt/m-ui/data:/data$' \
  "$repository_root/deploy/docker/compose.yml"
test "$(grep -c '^  [a-zA-Z0-9_-]*:$' \
  "$repository_root/deploy/docker/compose.yml")" -eq 1
if grep -Eq '\$\{|M_UI_|data-init' "$repository_root/deploy/docker/compose.yml"; then
  echo "default Compose contains a variable or legacy initializer" >&2
  exit 1
fi
grep -q 'releases/latest/download/install-docker.sh' \
  "$repository_root/README.md" "$repository_root/deploy/docker/README.md"
sh -n "$repository_root/scripts/install-docker.sh"
if grep -q 'm-ui admin setup-link' "$repository_root/scripts/install-docker.sh"; then
  echo "Docker installer still requires an SSH setup-link command" >&2
  exit 1
fi
sh -n "$repository_root/deploy/docker/prepare-data-root.sh"

make_archive() {
  version="$1"
  output="$2"
  stage="${temporary_directory}/stage-${version}"
  mkdir -p "$stage/bootstrap" "$stage/scripts"
  cp -R "$repository_root/deploy" "$stage/deploy"
  cp "$repository_root/scripts/manage.sh" "$stage/scripts/manage.sh"
  cp "$repository_root/scripts/install.sh" "$stage/scripts/install.sh"
  cp "$repository_root/scripts/uninstall.sh" "$stage/scripts/uninstall.sh"
  cat >"$stage/m-ui" <<EOF
#!/bin/sh
if [ "\${1:-}" = version ]; then
  echo "m-ui ${version} (synthetic)"
  exit 0
fi
if [ "\${1:-}" = runtime ]; then
  printf 'runtime:%s\n' "\${2:-unknown}" >>"\${M_UI_TEST_SERVICE_LOG:?}"
  if [ "\${2:-}" = restart-mihomo ] && [ -f "\${M_UI_TEST_PENDING:?}" ]; then
    rm -f "\${M_UI_TEST_PENDING}"
  fi
  exit 0
fi
exit 0
EOF
  printf '%s\n' "synthetic-mihomo-${version}" >"$stage/bootstrap/mihomo"
  printf '%s\n' '{"schema_version":1}' >"$stage/bootstrap/manifest.json"
  chmod 0755 "$stage/m-ui" "$stage/bootstrap/mihomo" "$stage/scripts/"*.sh
  tar -C "$stage" -czf "$output" .
}

archive_one="${temporary_directory}/m-ui_0.1.0_linux_amd64.tar.gz"
archive_two="${temporary_directory}/m-ui_0.1.1_linux_amd64.tar.gz"
make_archive v0.1.0 "$archive_one"
make_archive v0.1.1 "$archive_two"
sha_one="$(sha256sum "$archive_one" | awk '{print $1}')"
sha_two="$(sha256sum "$archive_two" | awk '{print $1}')"
test_root="${temporary_directory}/root"
lifecycle_log="${temporary_directory}/lifecycle.log"
pending_marker="${temporary_directory}/mihomo-endpoint.pending"

run_manage() {
  M_UI_ROOT="$test_root" \
    M_UI_TEST_DISTRO=debian \
    M_UI_TEST_VERSION=12 \
    "$repository_root/scripts/manage.sh" "$@"
}

run_manage_started() {
  M_UI_ROOT="$test_root" \
    M_UI_TEST_DISTRO=debian \
    M_UI_TEST_VERSION=12 \
    M_UI_TEST_SERVICES=1 \
    M_UI_TEST_SERVICE_LOG="$lifecycle_log" \
    M_UI_TEST_PENDING="$pending_marker" \
    "$repository_root/scripts/manage.sh" "$@"
}

run_manage install --package tar --archive "$archive_one" \
  --sha256 "$sha_one" --no-start
test -x "$test_root/usr/bin/m-ui"
test -x "$test_root/var/lib/m-ui/core/current/mihomo"
test -f "$test_root/etc/m-ui/config.toml"
test -f "$test_root/etc/mihomo/config.yaml"
printf '%s\n' "preserve-me" >"$test_root/var/lib/m-ui/persistent-marker"

printf '%s\n' pending >"$pending_marker"
: >"$lifecycle_log"
run_manage_started update --package tar --archive "$archive_two" \
  --sha256 "$sha_two"
grep -q 'v0.1.1' "$test_root/usr/bin/m-ui"
grep -q 'preserve-me' "$test_root/var/lib/m-ui/persistent-marker"
test ! -e "$pending_marker"
sed -n '1p' "$lifecycle_log" | grep -qx 'service:m-ui:restart'
sed -n '2p' "$lifecycle_log" | grep -qx 'runtime:restart-mihomo'

printf '%s\n' pending >"$pending_marker"
: >"$lifecycle_log"
run_manage_started reinstall --package tar --archive "$archive_two" \
  --sha256 "$sha_two"
grep -q 'preserve-me' "$test_root/var/lib/m-ui/persistent-marker"
test ! -e "$pending_marker"
sed -n '1p' "$lifecycle_log" | grep -qx 'service:m-ui:restart'
sed -n '2p' "$lifecycle_log" | grep -qx 'runtime:restart-mihomo'

run_manage reinstall --package tar --archive "$archive_two" \
  --sha256 "$sha_two" --no-start

run_manage uninstall
test ! -e "$test_root/usr/bin/m-ui"
test ! -e "$test_root/etc/systemd/system/m-ui.service"
test ! -e "$test_root/etc/systemd/system/mihomo.service"
test -f "$test_root/var/lib/m-ui/persistent-marker"

run_manage reinstall --package tar --archive "$archive_two" \
  --sha256 "$sha_two" --no-start
test -x "$test_root/usr/bin/m-ui"

broken="${temporary_directory}/broken.tar.gz"
broken_stage="${temporary_directory}/broken-stage"
mkdir -p "$broken_stage"
printf '%s\n' broken >"$broken_stage/unexpected"
tar -C "$broken_stage" -czf "$broken" .
broken_sha="$(sha256sum "$broken" | awk '{print $1}')"
if run_manage update --package tar --archive "$broken" \
  --sha256 "$broken_sha" --no-start
then
  echo "incomplete archive was accepted" >&2
  exit 1
fi
grep -q 'v0.1.1' "$test_root/usr/bin/m-ui"

if run_manage update --package tar --archive "$archive_two" \
  --sha256 "$(printf '0%.0s' {1..64})" --no-start
then
  echo "wrong SHA-256 was accepted" >&2
  exit 1
fi

malicious="${temporary_directory}/malicious.tar.gz"
python3 - "$malicious" <<'PY'
import io
import sys
import tarfile

with tarfile.open(sys.argv[1], "w:gz") as archive:
    info = tarfile.TarInfo("../../escape")
    payload = b"unsafe"
    info.size = len(payload)
    archive.addfile(info, io.BytesIO(payload))
PY
malicious_sha="$(sha256sum "$malicious" | awk '{print $1}')"
if run_manage update --package tar --archive "$malicious" \
  --sha256 "$malicious_sha" --no-start
then
  echo "malicious archive was accepted" >&2
  exit 1
fi

symlink_archive="${temporary_directory}/symlink.tar.gz"
python3 - "$symlink_archive" <<'PY'
import sys
import tarfile

with tarfile.open(sys.argv[1], "w:gz") as archive:
    info = tarfile.TarInfo("unsafe-link")
    info.type = tarfile.SYMTYPE
    info.linkname = "../../escape"
    archive.addfile(info)
PY
symlink_sha="$(sha256sum "$symlink_archive" | awk '{print $1}')"
if run_manage update --package tar --archive "$symlink_archive" \
  --sha256 "$symlink_sha" --no-start
then
  echo "symbolic-link archive was accepted" >&2
  exit 1
fi

run_manage purge --yes
test ! -e "$test_root/var/lib/m-ui"
test ! -e "$test_root/etc/m-ui"
test ! -e "$test_root/etc/systemd/system/m-ui.service"
test ! -e "$test_root/etc/systemd/system/mihomo.service"

start_block="$(awk '/^start_services\(\)/,/^stop_and_remove_program\(\)/' \
  "$repository_root/scripts/manage.sh")"
printf '%s\n' "$start_block" | grep -q 'restart-mihomo'
printf '%s\n' "$start_block" | grep -q 'doctor'
if printf '%s\n' "$start_block" | grep -q '127\.0\.0\.1:2095'; then
  echo "manage.sh uses a fixed panel health endpoint" >&2
  exit 1
fi
if printf '%s\n' "$start_block" | grep -q 'restart mihomo.service'; then
  echo "manage.sh directly restarted Mihomo" >&2
  exit 1
fi
postinstall="$repository_root/deploy/package/postinstall.sh"
postinstall_block="$(sed -n '/^run_as_m_ui()/,$p' "$postinstall")"
printf '%s\n' "$postinstall_block" | grep -q 'apply-mihomo-start'
grep -q '^backup_dir="/var/lib/m-ui-package-backups"$' \
  "$repository_root/deploy/package/preinstall.sh"
grep -q '^state_dir=/run/m-ui-package$' \
  "$repository_root/deploy/package/preremove.sh"
grep -q '^state_dir=/run/m-ui-package$' "$postinstall"
grep -q 'doctor panel' "$postinstall"
grep -q '^snapshot_state=' "$postinstall"
grep -q '^snapshot_state=' \
  "$repository_root/deploy/package/preinstall.sh"
if grep -Eq 'candidate_snapshot|sort \| tail -n 1' "$postinstall"; then
  echo "postinstall guessed a package rollback snapshot" >&2
  exit 1
fi
for package_hook in \
  "$repository_root/deploy/package/preremove.sh" \
  "$repository_root/deploy/package/postinstall.sh" \
  "$repository_root/deploy/package/postremove.sh" \
  "$repository_root/deploy/package/preinstall.sh"
do
  if grep -Eq '/run/m-ui/package-upgrade-services|/var/lib/m-ui/package-backups' \
    "$package_hook"
  then
    echo "package hook uses an m-ui-writable privileged state path" >&2
    exit 1
  fi
done
if printf '%s\n' "$postinstall_block" | grep -qF ". \"\$state_file\""; then
  echo "postinstall sources an upgrade state file" >&2
  exit 1
fi
if printf '%s\n' "$postinstall_block" | grep -Eq \
  'systemctl start mihomo\.service.*\|\| true|rc-service mihomo start.*\|\| true'
then
  echo "postinstall ignored a Mihomo startup-boundary failure" >&2
  exit 1
fi
grep -q '^Before=mihomo.service$' "$repository_root/deploy/systemd/m-ui.service"
grep -q '^RuntimeDirectoryPreserve=yes$' \
  "$repository_root/deploy/systemd/m-ui.service"
grep -q '^After=network-online.target m-ui.service$' \
  "$repository_root/deploy/systemd/mihomo.service"
grep -q 'runtime finalize-mihomo-start' \
  "$repository_root/deploy/systemd/mihomo.service"
grep -q 'runtime wait-ready' \
  "$repository_root/deploy/systemd/mihomo.service"
grep -q 'runtime wait-ready' \
  "$repository_root/deploy/openrc/mihomo"
grep -q '^TimeoutStartSec=180s$' \
  "$repository_root/deploy/systemd/mihomo.service"
grep -q 'BeginRuntimeStartup' "$repository_root/internal/app/app.go"
grep -q 'PublishReady' "$repository_root/internal/app/app.go"
grep -q 'AcquireRuntimeReadyGuard' "$repository_root/internal/app/commands.go"
grep -q 'runtimeCommandTimeout = 120 \* time.Second' "$repository_root/cmd/m-ui/main.go"
grep -q 'trySharedRuntimeLockFile' "$repository_root/internal/mihomo/startup_readiness.go"
grep -q 'trySharedRuntimeLockFile' "$repository_root/internal/mihomo/lifecycle_marker.go"
grep -q 'ReconcileStartupLocked' \
  "$repository_root/internal/publisher/publisher.go"
grep -q 'type AttemptProcess interface' \
  "$repository_root/internal/mihomo/types.go"
if grep -q 'stopRecoveryAttempt' "$repository_root/internal/mihomo/managed.go"; then
  echo "managed recovery still has unscoped attempt cleanup" >&2
  exit 1
fi

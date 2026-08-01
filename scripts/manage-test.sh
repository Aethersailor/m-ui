#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temporary_directory="$(mktemp -d)"
cleanup() {
  rm -rf -- "$temporary_directory"
}
trap cleanup EXIT

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

run_manage() {
  M_UI_ROOT="$test_root" \
    M_UI_TEST_DISTRO=debian \
    M_UI_TEST_VERSION=12 \
    "$repository_root/scripts/manage.sh" "$@"
}

run_manage install --package tar --archive "$archive_one" \
  --sha256 "$sha_one" --no-start
test -x "$test_root/usr/bin/m-ui"
test -x "$test_root/var/lib/m-ui/core/current/mihomo"
test -f "$test_root/etc/m-ui/config.toml"
test -f "$test_root/etc/mihomo/config.yaml"
printf '%s\n' "preserve-me" >"$test_root/var/lib/m-ui/persistent-marker"

run_manage update --package tar --archive "$archive_two" \
  --sha256 "$sha_two" --no-start
grep -q 'v0.1.1' "$test_root/usr/bin/m-ui"
grep -q 'preserve-me' "$test_root/var/lib/m-ui/persistent-marker"

run_manage reinstall --package tar --archive "$archive_two" \
  --sha256 "$sha_two" --no-start
grep -q 'preserve-me' "$test_root/var/lib/m-ui/persistent-marker"

run_manage uninstall
test ! -e "$test_root/usr/bin/m-ui"
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

#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temporary_directory="$(mktemp -d)"
cleanup() {
  rm -rf -- "$temporary_directory"
}
trap cleanup EXIT

release_directory="$temporary_directory/release"
version_directory="$release_directory/v9.9.9"
fake_bin="$temporary_directory/bin"
mkdir -p "$version_directory" "$fake_bin"
cp "$repository_root/scripts/install.sh" "$temporary_directory/install.sh"
chmod 0755 "$temporary_directory/install.sh"
mkdir -p "$temporary_directory/tmp"
cat >"$temporary_directory/manage.sh" <<'EOF'
#!/bin/sh
echo "installer trusted a hostile adjacent manage.sh" >&2
exit 99
EOF
chmod 0755 "$temporary_directory/manage.sh"

assert_no_installer_temp_dirs() {
  leftovers="$(find "$temporary_directory/tmp" -mindepth 1 -maxdepth 1 \
    -type d -print -quit)"
  test -z "$leftovers" || {
    echo "installer leaked temporary directory: $leftovers" >&2
    exit 1
  }
}

cat >"$version_directory/manage.sh" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >"${M_UI_INSTALL_TEST_LOG:?}"
EOF
chmod 0755 "$version_directory/manage.sh"
sha256sum "$version_directory/manage.sh" |
  sed "s#${version_directory}/##" >"$version_directory/SHA256SUMS"

cat >"$release_directory/latest.json" <<'EOF'
{
  "tag_name": "v9.9.9",
  "name": "m-ui v9.9.9"
}
EOF

cat >"$fake_bin/curl" <<'EOF'
#!/bin/sh
set -eu

output=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output|-o)
      output="$2"
      shift 2
      ;;
    *)
      url="$1"
      shift
      ;;
  esac
done

printf '%s\n' "$url" >>"${M_UI_INSTALL_TEST_CURL_LOG:?}"

case "$url" in
  */releases/latest) source="${M_UI_INSTALL_TEST_RELEASE}/latest.json" ;;
  */releases/download/v9.9.9/manage.sh) source="${M_UI_INSTALL_TEST_RELEASE}/v9.9.9/manage.sh" ;;
  */releases/download/v9.9.9/SHA256SUMS) source="${M_UI_INSTALL_TEST_RELEASE}/v9.9.9/SHA256SUMS" ;;
  *) exit 1 ;;
esac
cp "$source" "$output"
EOF
chmod 0755 "$fake_bin/curl"

log_file="$temporary_directory/invocation.log"
curl_log="$temporary_directory/curl.log"
: >"$curl_log"
(
  cd "$temporary_directory"
  PATH="$fake_bin:$PATH" \
  TMPDIR="$temporary_directory/tmp" \
  M_UI_INSTALL_TEST_RELEASE="$release_directory" \
  M_UI_INSTALL_TEST_LOG="$log_file" \
  M_UI_INSTALL_TEST_CURL_LOG="$curl_log" \
    "$temporary_directory/install.sh" --version v9.9.9
)
grep -qx 'install --version v9.9.9 --version v9.9.9' "$log_file"
assert_no_installer_temp_dirs
if grep -q '/releases/latest' "$curl_log"; then
  echo "explicit installer version unexpectedly queried latest" >&2
  exit 1
fi

: >"$curl_log"
(
  cd "$temporary_directory"
  PATH="$fake_bin:$PATH" \
  TMPDIR="$temporary_directory/tmp" \
  M_UI_INSTALL_TEST_RELEASE="$release_directory" \
  M_UI_INSTALL_TEST_LOG="$log_file" \
  M_UI_INSTALL_TEST_CURL_LOG="$curl_log" \
    "$temporary_directory/install.sh"
)
grep -qx 'install --version v9.9.9' "$log_file"
grep -q '/releases/latest' "$curl_log"
assert_no_installer_temp_dirs

if PATH="$fake_bin:$PATH" \
  TMPDIR="$temporary_directory/tmp" \
  M_UI_INSTALL_TEST_RELEASE="$release_directory" \
  M_UI_INSTALL_TEST_LOG="$log_file" \
  M_UI_INSTALL_TEST_CURL_LOG="$curl_log" \
  "$temporary_directory/install.sh" --version v9x.9.9
then
  echo "installer accepted a malformed version" >&2
  exit 1
fi
assert_no_installer_temp_dirs

printf '%s\n' '0' >"$version_directory/SHA256SUMS"
if PATH="$fake_bin:$PATH" \
  TMPDIR="$temporary_directory/tmp" \
  M_UI_INSTALL_TEST_RELEASE="$release_directory" \
  M_UI_INSTALL_TEST_LOG="$log_file" \
  M_UI_INSTALL_TEST_CURL_LOG="$curl_log" \
  "$temporary_directory/install.sh"
then
  echo "standalone installer accepted an invalid checksum" >&2
  exit 1
fi
assert_no_installer_temp_dirs

#!/usr/bin/env bash

set -euo pipefail

umask 077

if [[ "$EUID" -ne 0 ]]; then
  echo "migrate-volumes.sh must run as root (invoke it with sudo)" >&2
  exit 1
fi

usage() {
  cat <<'EOF'
Usage:
  migrate-volumes.sh --source-root /opt/m-ui [--target /opt/m-ui/data] [--image IMAGE] [--dry-run] [--yes]
  migrate-volumes.sh --source-project PROJECT [--target /opt/m-ui/data] [--image IMAGE] [--dry-run] [--yes]

Copies either the former four-bind layout or the legacy four named volumes into
the single /data mount layout. Source data is never removed or modified.
EOF
}

source_project=""
source_root=""
target=/opt/m-ui/data
dry_run=0
assume_yes=0
validator_image=ghcr.io/aethersailor/m-ui:latest

while [[ $# -gt 0 ]]; do
  case "$1" in
    --source-project)
      [[ $# -ge 2 ]] || { usage >&2; exit 1; }
      source_project="$2"
      shift 2
      ;;
    --source-root)
      [[ $# -ge 2 ]] || { usage >&2; exit 1; }
      source_root="$2"
      shift 2
      ;;
    --target)
      [[ $# -ge 2 ]] || { usage >&2; exit 1; }
      target="$2"
      shift 2
      ;;
    --image)
      [[ $# -ge 2 ]] || { usage >&2; exit 1; }
      validator_image="$2"
      shift 2
      ;;
    --dry-run)
      dry_run=1
      shift
      ;;
    --yes)
      assume_yes=1
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      usage >&2
      exit 1
      ;;
  esac
done

if [[ -n "$source_project" && -n "$source_root" ]] ||
   [[ -z "$source_project" && -z "$source_root" ]]; then
  echo "select exactly one source: --source-root or --source-project" >&2
  exit 1
fi
[[ "$target" = /* && "$target" != / ]] || {
  echo "target must be an absolute path other than /" >&2
  exit 1
}
if [[ -L "$target" ]]; then
  echo "target must not be a symbolic link" >&2
  exit 1
fi
if [[ -e "$target" ]]; then
  [[ -d "$target" ]] || { echo "target is not a directory" >&2; exit 1; }
  [[ -z "$(find "$target" -mindepth 1 -maxdepth 1 -print -quit)" ]] || {
    echo "target must be empty; refusing to overwrite existing data" >&2
    exit 1
  }
  target_exists=1
else
  target_exists=0
fi

declare -A volume_for=(
  [etc]="m-ui-etc"
  [mihomo-etc]="mihomo-etc"
  [data]="m-ui-data"
  [mihomo-data]="mihomo-data"
)
declare -A source_volume

if [[ -n "$source_root" ]]; then
  [[ "$source_root" = /* && "$source_root" != / ]] || {
    echo "source root must be an absolute path other than /" >&2
    exit 1
  }
  [[ -d "$source_root" && ! -L "$source_root" ]] || {
    echo "source root must be a real directory" >&2
    exit 1
  }
  source_root="$(readlink -f -- "$source_root")"
  [[ "$target" != "$source_root" ]] || {
    echo "source root and target must differ" >&2
    exit 1
  }
  for relative in etc/m-ui etc/mihomo var/lib/m-ui var/lib/mihomo; do
    path="$source_root/$relative"
    [[ -d "$path" && ! -L "$path" ]] || {
      echo "source root is missing a real directory: $relative" >&2
      exit 1
    }
  done
  if find \
      "$source_root/etc/m-ui" \
      "$source_root/etc/mihomo" \
      "$source_root/var/lib/m-ui" \
      "$source_root/var/lib/mihomo" \
      \( -type l -o -type b -o -type c -o -type p -o -type s \) \
      -print -quit | grep -q .; then
    echo "source root contains a symbolic link or special file" >&2
    exit 1
  fi
  if docker ps -q | xargs -r docker inspect \
      --format '{{range .Mounts}}{{println .Source}}{{end}}' |
      grep -Fqx -e "$source_root/etc/m-ui" \
                 -e "$source_root/etc/mihomo" \
                 -e "$source_root/var/lib/m-ui" \
                 -e "$source_root/var/lib/mihomo"; then
    echo "source root is still mounted by a running container" >&2
    exit 1
  fi
  echo "Source bind root: $source_root"
else
  for key in "${!volume_for[@]}"; do
    logical="${volume_for[$key]}"
    mapfile -t matches < <(
      docker volume ls -q \
        --filter "label=com.docker.compose.project=$source_project" \
        --filter "label=com.docker.compose.volume=$logical"
    )
    if [[ ${#matches[@]} -ne 1 ]]; then
      echo "could not identify exactly one source volume for $logical" >&2
      exit 1
    fi
    source_volume[$key]="${matches[0]}"
    if [[ -n "$(docker ps -q --filter "volume=${source_volume[$key]}" | head -n 1)" ]]; then
      echo "source volume ${source_volume[$key]} is still mounted by a running container" >&2
      exit 1
    fi
  done
  echo "Source project: $source_project"
  for key in "${!volume_for[@]}"; do
    echo "  $key <- ${source_volume[$key]}"
  done
fi

echo "Target data mount: $target"
echo "Validation image: $validator_image"
if [[ "$dry_run" -eq 1 ]]; then
  exit 0
fi
if [[ "$assume_yes" -ne 1 ]]; then
  read -r -p "Copy this data without deleting the source? [y/N] " answer
  [[ "$answer" = y || "$answer" = Y ]] || exit 1
fi

parent="$(dirname "$target")"
staging="$parent/.m-ui-data-migration-$$"
[[ "$staging" != "$target" && "$staging" != / ]] || exit 1
mkdir -p "$staging/etc" "$staging/var/lib"
cleanup() {
  if [[ -d "$staging" && "$staging" == "$parent"/.m-ui-data-migration-* ]]; then
    rm -rf -- "$staging"
  fi
}
trap cleanup EXIT

copy_bind() {
  local relative="$1"
  mkdir -p "$staging/$relative"
  cp -a "$source_root/$relative/." "$staging/$relative/"
}

copy_volume() {
  local key="$1"
  local relative="$2"
  local source="${source_volume[$key]}"
  mkdir -p "$staging/$relative"
  docker run --rm --network none \
    -v "$source:/source:ro" \
    -v "$staging/$relative:/destination" \
    alpine:3.22 sh -c '
      if find /source \( -type l -o -type b -o -type c -o -type p -o -type s \) -print -quit | grep -q .; then
        echo "source volume contains a symbolic link or special file" >&2
        exit 1
      fi
      cp -a /source/. /destination/
    '
}

if [[ -n "$source_root" ]]; then
  copy_bind etc/m-ui
  copy_bind etc/mihomo
  copy_bind var/lib/m-ui
  copy_bind var/lib/mihomo
else
  copy_volume etc etc/m-ui
  copy_volume mihomo-etc etc/mihomo
  copy_volume data var/lib/m-ui
  copy_volume mihomo-data var/lib/mihomo
fi

database="$staging/var/lib/m-ui/m-ui.db"
master_key="$staging/var/lib/m-ui/master.key"
if [[ -e "$database" && ! -f "$database" ]] ||
   [[ -e "$master_key" && ! -f "$master_key" ]]; then
  echo "database and master key must both be regular files" >&2
  exit 1
fi
if [[ -e "$database" && ! -e "$master_key" ]] ||
   [[ ! -e "$database" && -e "$master_key" ]]; then
  echo "m-ui database and master key are not a complete pair" >&2
  exit 1
fi
[[ -f "$staging/etc/m-ui/config.toml" ]] || {
  echo "migration source is missing etc/m-ui/config.toml" >&2
  exit 1
}

script_dir="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
bash "$script_dir/prepare-data-root.sh" "$staging"
docker run --rm --network none --user 10001:10001 \
  --entrypoint /usr/bin/m-ui \
  -v "$staging:/data" \
  "$validator_image" doctor database --config /etc/m-ui/config.toml

if [[ "$target_exists" -eq 0 ]]; then
  mv -- "$staging" "$target"
else
  rmdir -- "$target"
  mv -- "$staging" "$target"
fi
trap - EXIT

echo "Migration copied and validated successfully. Source data remains untouched."
echo "Run: docker compose -f /opt/m-ui/compose.yml up -d"

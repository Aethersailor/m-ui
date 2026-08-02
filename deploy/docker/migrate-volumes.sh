#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: migrate-volumes.sh --source-project PROJECT --target /opt/m-ui [--image IMAGE] [--dry-run] [--yes]

Copies the four legacy Compose named volumes into the deterministic single-root
layout. Source volumes are never removed or modified.
EOF
}

source_project=""
target=""
dry_run=0
assume_yes=0
validator_image="${M_UI_IMAGE:-ghcr.io/aethersailor/m-ui:${M_UI_IMAGE_TAG:-edge}}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --source-project)
      [[ $# -ge 2 ]] || { usage >&2; exit 1; }
      source_project="$2"
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

[[ -n "$source_project" && -n "$target" ]] || { usage >&2; exit 1; }
[[ "$target" = /* && "$target" != "/" ]] || {
  echo "target must be an absolute path other than /" >&2
  exit 1
}
script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
bash "$script_dir/prepare-data-root.sh" --check "$target"
if [[ -L "$target" ]]; then
  echo "target must not be a symbolic link" >&2
  exit 1
fi

declare -A volume_for=(
  [etc]="m-ui-etc"
  [mihomo-etc]="mihomo-etc"
  [data]="m-ui-data"
  [mihomo-data]="mihomo-data"
)
declare -A source_volume

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

echo "Source project: $source_project"
for key in "${!volume_for[@]}"; do
  echo "  $key <- ${source_volume[$key]}"
done
echo "Target: $target"
if [[ "$dry_run" -eq 1 ]]; then
  exit 0
fi
if [[ "$assume_yes" -ne 1 ]]; then
  read -r -p "Copy these volumes without deleting the sources? [y/N] " answer
  [[ "$answer" = "y" || "$answer" = "Y" ]] || exit 1
fi

parent="$(dirname "$target")"
staging="$parent/.m-ui-volume-migration-$$"
[[ "$staging" != "$target" && "$staging" != "/" ]] || exit 1
mkdir -p "$staging/etc" "$staging/var/lib"
cleanup() {
  if [[ -d "$staging" ]]; then
    rm -rf -- "$staging"
  fi
}
trap cleanup EXIT

copy_volume() {
  local key="$1"
  local source="${source_volume[$key]}"
  local destination="$staging/$2"
  mkdir -p "$destination"
  docker run --rm --network none \
    -v "$source:/source:ro" \
    -v "$destination:/destination" \
    alpine:3.22 sh -c '
      if find /source \( -type l -o -type b -o -type c -o -type p -o -type s \) -print -quit | grep -q .; then
        echo "source volume contains a symbolic link or special file" >&2
        exit 1
      fi
      cp -a /source/. /destination/
    '
}

copy_volume etc etc/m-ui
copy_volume mihomo-etc etc/mihomo
copy_volume data var/lib/m-ui
copy_volume mihomo-data var/lib/mihomo

if [[ -e "$staging/var/lib/m-ui/m-ui.db" &&
      ! -e "$staging/var/lib/m-ui/master.key" ]] ||
   [[ ! -e "$staging/var/lib/m-ui/m-ui.db" &&
      -e "$staging/var/lib/m-ui/master.key" ]]; then
  echo "m-ui database and master key are not a complete pair" >&2
  exit 1
fi

for path in \
  etc/m-ui \
  etc/mihomo \
  var/lib/m-ui \
  var/lib/mihomo \
  var/lib/m-ui/revisions \
  var/lib/m-ui/core
do
  [[ -d "$staging/$path" ]] || {
    echo "migration target is missing $path" >&2
    exit 1
  }
done

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

# The image's database doctor opens the staged SQLite database, runs
# PRAGMA integrity_check, applies any pending migrations, and decrypts the
# managed controller secret. It is deliberately network-isolated.
docker run --rm --network none --user 0:0 \
  --entrypoint /usr/bin/m-ui \
  -v "$staging/etc/m-ui:/etc/m-ui" \
  -v "$staging/etc/mihomo:/etc/mihomo" \
  -v "$staging/var/lib/m-ui:/var/lib/m-ui" \
  -v "$staging/var/lib/mihomo:/var/lib/mihomo" \
  "$validator_image" doctor database --config /etc/m-ui/config.toml

if [[ "$target_exists" -eq 0 ]]; then
  mv -- "$staging" "$target"
else
  rmdir -- "$target"
  mv -- "$staging" "$target"
fi
trap - EXIT

echo "Migration copied successfully. Sources remain untouched."
echo "Run: M_UI_DATA_DIR=$target docker compose -f deploy/docker/compose.yml up -d"

#!/bin/sh

set -eu

script_directory="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
case "${1:-}" in
    --purge)
        shift
        exec "$script_directory/manage.sh" purge "$@"
        ;;
    *)
        exec "$script_directory/manage.sh" uninstall "$@"
        ;;
esac

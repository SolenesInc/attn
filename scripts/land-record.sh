#!/usr/bin/env bash
set -euo pipefail

usage="usage: land record [name] --instance <name> [--seconds N] | --app <owner> [--seconds N]"
[ -n "${LAND_MEDIA:-}" ] || { echo "land-record: LAND_MEDIA is not set (run through \`land record\`)" >&2; exit 2; }

name=""
record_args=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    --name)
      [ "$#" -ge 2 ] || { echo "$usage" >&2; exit 2; }
      name="$2"
      shift 2
      ;;
    --instance|--app|--seconds)
      [ "$#" -ge 2 ] || { echo "$usage" >&2; exit 2; }
      record_args+=("$1" "$2")
      shift 2
      ;;
    *)
      if [ -z "$name" ] && [[ "$1" != -* ]]; then
        name="$1"
        shift
      else
        echo "$usage" >&2
        exit 2
      fi
      ;;
  esac
done

[ -n "$name" ] || name="clip-$(date +%Y%m%d-%H%M%S)"
name="${name%.mp4}"
if [ -z "$name" ] || [[ "$name" == */* ]] || [[ "$name" == .* ]]; then
  echo "land-record: clip name must be a plain filename without slashes or a leading dot: $name" >&2
  exit 2
fi
exec scripts/pr-evidence.sh record ${record_args[@]+"${record_args[@]}"} --out "$LAND_MEDIA/${name}.mp4"

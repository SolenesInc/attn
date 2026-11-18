#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -n "${ATTN_VERSION:-}" ]]; then
  printf '%s\n' "${ATTN_VERSION}"
  exit 0
fi

VERSION="$(tr -d '[:space:]' <"${ROOT_DIR}/VERSION")"
if [[ -z "${VERSION}" ]]; then
  echo "VERSION is empty" >&2
  exit 1
fi

printf '%s\n' "${VERSION}"

#!/usr/bin/env bash
# Fails when a binary shipped with attnd needs a newer glibc than attnd, which
# would leave machines that run attnd unable to start it.
set -euo pipefail

readonly usage="usage: check-binary-glibc.sh ATTND_BINARY BINARY..."
attnd="${1:?$usage}"
shift
[ "$#" -gt 0 ] || { echo "$usage" >&2; exit 2; }

highest_glibc() {
  local version
  version="$(objdump -T "$1" | grep -oE 'GLIBC_[0-9.]+' | sort -Vu | tail -1)"
  if [ -z "$version" ]; then
    echo "error: $1 has no GLIBC symbol references, so it cannot be compared" >&2
    exit 1
  fi
  printf '%s' "$version"
}

attnd_glibc="$(highest_glibc "$attnd")"
for binary in "$@"; do
  binary_glibc="$(highest_glibc "$binary")"
  echo "attnd: $attnd_glibc  $binary: $binary_glibc"
  if [ "$(printf '%s\n%s\n' "$attnd_glibc" "$binary_glibc" | sort -V | tail -1)" != "$attnd_glibc" ]; then
    echo "error: $binary needs $binary_glibc but attnd needs only $attnd_glibc, so it fails to start on machines that run attnd. Build it with make build-linux-amd64 or build-linux-arm64, which link Rust against glibc 2.28." >&2
    exit 1
  fi
done

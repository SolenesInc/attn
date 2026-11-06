#!/usr/bin/env bash
# Fails when attn-pty-host needs a newer glibc than attn, which would leave
# machines that run attn unable to start the host.
set -euo pipefail

attn="${1:?usage: check-pty-host-glibc.sh ATTN_BINARY PTY_HOST_BINARY}"
host="${2:?usage: check-pty-host-glibc.sh ATTN_BINARY PTY_HOST_BINARY}"

highest_glibc() {
  local version
  version="$(objdump -T "$1" | grep -oE 'GLIBC_[0-9.]+' | sort -Vu | tail -1)"
  if [ -z "$version" ]; then
    echo "error: $1 has no GLIBC symbol references, so it cannot be compared" >&2
    exit 1
  fi
  printf '%s' "$version"
}

attn_glibc="$(highest_glibc "$attn")"
host_glibc="$(highest_glibc "$host")"
echo "attn: $attn_glibc  attn-pty-host: $host_glibc"

if [ "$(printf '%s\n%s\n' "$attn_glibc" "$host_glibc" | sort -V | tail -1)" != "$attn_glibc" ]; then
  echo "error: attn-pty-host needs $host_glibc but attn needs only $attn_glibc, so the host fails to start on machines that run attn. Build the host with make build-pty-host-linux-amd64 or build-pty-host-linux-arm64." >&2
  exit 1
fi

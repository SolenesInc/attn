#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
if [ "$(uname -s)" != Darwin ]; then
  echo 'This experiment measures macOS physical footprint.' >&2
  exit 2
fi
test_root="$(mktemp -d "${TMPDIR:-/tmp}/pty-resource.XXXXXX")"
trap 'rm -rf "$test_root"' EXIT
clang -O2 -Wall -Wextra -Werror scripts/pty-resource-probe.c -o "$test_root/probe"
export ATTN_RESOURCE_PROBE="$test_root/probe"
export ATTN_TEST_PTY_HOST="${1:-$root/pty-host/target/release/attn-pty-host}"
export ATTN_PERF_ROUND="${2:-current}"
go test ./internal/ptybackend -run '^TestSharedHostResourceExperiment$' -count=1 -v -timeout=180s

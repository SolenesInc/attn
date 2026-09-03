#!/usr/bin/env bash
set -euo pipefail

if command -v asdf >/dev/null 2>&1; then
  zig="$(asdf which zig 2>/dev/null || command -v zig)"
else
  zig="$(command -v zig)"
fi
exec "$zig" ar "$@"

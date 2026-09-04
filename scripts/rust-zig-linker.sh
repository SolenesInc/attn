#!/usr/bin/env bash
set -euo pipefail

target="${ATTN_RUST_LINK_TARGET:?ATTN_RUST_LINK_TARGET is required}"
if command -v asdf >/dev/null 2>&1; then
  zig="$(asdf which zig 2>/dev/null || command -v zig)"
else
  zig="$(command -v zig)"
fi

args=()
for arg in "$@"; do
  case "$arg" in
    --target=*-unknown-linux-gnu) ;;
    *) args+=("$arg") ;;
  esac
done
exec "$zig" cc -target "$target" "${args[@]}"

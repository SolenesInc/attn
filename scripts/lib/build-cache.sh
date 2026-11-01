#!/bin/bash
# Immutable build artifacts shared by every worktree on the machine, each in
# <cache root>/<kind>/<identity>/. An identity names the artifact's bytes or its
# inputs, so a published entry never changes.

attn_build_cache_root() {
  local base="${XDG_CACHE_HOME:-}"
  if [[ -z "$base" ]]; then
    if [[ "$(uname -s)" == Darwin ]]; then
      base="$HOME/Library/Caches"
    else
      base="$HOME/.cache"
    fi
  fi
  printf '%s/attn/%s' "$base" "$1"
}

attn_build_cache_publish() (
  local kind="$1" identity="$2" source_dir="$3" parent staging
  parent="$(attn_build_cache_root "$kind")"
  mkdir -p "$parent"
  parent="$(cd "$parent" && pwd -P)"
  if [[ ! -d "$parent/$identity" ]]; then
    staging="$(mktemp -d "$parent/.publish.XXXXXX")"
    trap 'rm -rf "$staging"' EXIT
    cp -R "$source_dir" "$staging/$identity"
    mv -n "$staging/$identity" "$parent/"
  fi
  printf '%s' "$parent/$identity"
)

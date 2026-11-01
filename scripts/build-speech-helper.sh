#!/bin/bash
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$repo/scripts/lib/build-cache.sh"

crate=app/speech
binary="$repo/$crate/target/release/attn-speech"

cargo_configs() {
  local dir="$repo/$crate" name
  while :; do
    for name in config config.toml; do
      if [[ -f "$dir/.cargo/$name" ]]; then cat "$dir/.cargo/$name"; fi
    done
    [[ "$dir" == / ]] && break
    dir="$(dirname "$dir")"
  done
  for name in config config.toml; do
    if [[ -f "${CARGO_HOME:-$HOME/.cargo}/$name" ]]; then cat "${CARGO_HOME:-$HOME/.cargo}/$name"; fi
  done
}

source_key() {
  cd "$repo"
  {
    {
      printf '%s\n' "$crate/Cargo.toml" "$crate/Cargo.lock" rust-toolchain.toml scripts/build-speech-helper.sh
      find "$crate/src" -type f
      if [[ -f "$crate/build.rs" ]]; then printf '%s\n' "$crate/build.rs"; fi
    } | LC_ALL=C sort | while IFS= read -r path; do
      shasum -a 256 "$path"
    done
    (cd "$crate" && rustc -vV)
    cargo_configs
    env | grep -E '^(CARGO_[A-Z0-9_]*|RUSTFLAGS|CC|CXX|CFLAGS|CXXFLAGS|SDKROOT|MACOSX_DEPLOYMENT_TARGET|CMAKE_ARGS|TRANSCRIBE_CMAKE_ARGS|TRANSCRIBE_DIR)=' | LC_ALL=C sort || true
    if [[ "$(uname -s)" == Darwin ]]; then
      xcrun --show-sdk-version
      cc --version | head -n1
    fi
  } | shasum -a 256 | awk '{ print $1 }'
}

key="$(source_key)"
if [[ "${1:-}" == --print-cache-entry ]]; then
  printf '%s\n' "$(attn_build_cache_root speech-helper)/$key"
  exit
fi
cached="$(attn_build_cache_root speech-helper)/$key/attn-speech"

if [[ ! -x "$cached" ]]; then
  cargo build --manifest-path "$repo/$crate/Cargo.toml" --release --locked
  staging="$(mktemp -d "${TMPDIR:-/tmp}/attn-speech-helper.XXXXXX")"
  trap 'rm -rf "$staging"' EXIT
  mkdir "$staging/artifact"
  cp "$binary" "$staging/artifact/attn-speech"
  cached="$(attn_build_cache_publish speech-helper "$key" "$staging/artifact")/attn-speech"
fi

if ! cmp -s "$cached" "$binary"; then
  mkdir -p "$(dirname "$binary")"
  pending="$(mktemp "$binary.XXXXXX")"
  cp "$cached" "$pending"
  chmod +x "$pending"
  mv "$pending" "$binary"
fi
echo "==> attn-speech ${key:0:12}: $cached"

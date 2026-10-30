#!/bin/bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/libghostty-vt.sh"
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/release-asset.sh"

platform="$(vt_default_platform)"
output_dir="$(vt_output_dir_for "$platform")"
key="$(vt_key)"

build_from_source() {
  if [[ "${ATTN_VT_FROM_SOURCE:-}" != 1 && -L "$output_dir" && -f "$output_dir/lib/libghostty-vt.a" && -f "$output_dir.source-key" ]] &&
    [[ "$(cat "$output_dir.source-key")" == "$(vt_source_key "$platform")" ]]; then
    vt_use_artifact "$platform" "$(cd "$output_dir" && pwd -P)"
    return
  fi
  vt_build_from_source "$platform"
  echo "==> done (source build): $output_dir"
  shasum -a 256 "$(vt_lib_for "$platform")"
}

if [[ "${ATTN_VT_FROM_SOURCE:-}" == "1" ]]; then
  echo "==> ATTN_VT_FROM_SOURCE=1: building $platform from source"
  build_from_source
  exit 0
fi

lock_key="$(vt_lock_field key)"
lock_sha="$(vt_lock_field "sha256_${platform}")"

if [[ -z "$lock_key" || "$key" != "$lock_key" ]]; then
  echo "==> no published asset for key ${key:0:12} (pin edited locally?); building $platform from source"
  build_from_source
  exit 0
fi

if [[ -z "$lock_sha" ]]; then
  echo "==> key ${key:0:12} has no published $platform asset (lock missing sha256_${platform}); building from source"
  build_from_source
  exit 0
fi

cache_parent="$(vt_cache_root)/$platform"
mkdir -p "$cache_parent"
cache_parent="$(cd "$cache_parent" && pwd -P)"
cached="$cache_parent/$lock_sha"
if [[ -f "$cached/lib/libghostty-vt.a" ]]; then
  vt_use_artifact "$platform" "$cached"
  exit 0
fi

asset="$(vt_asset_name_for "$platform")"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/attn-vt-dl.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

echo "==> downloading prebuilt libghostty-vt ($platform, key ${key:0:12})"
if ! release_asset_download "$VT_REPO" "$VT_RELEASE_TAG" "$asset" "$tmp/$asset"; then
  echo "warning: download failed; falling back to source build" >&2
  build_from_source
  exit 0
fi

if ! echo "${lock_sha}  $tmp/$asset" | shasum -a 256 -c - >/dev/null 2>&1; then
  echo "warning: sha256 mismatch for $asset (expected $lock_sha); falling back to source build" >&2
  build_from_source
  exit 0
fi

tar -xzf "$tmp/$asset" -C "$tmp"
cached="$(vt_cache_artifact "$platform" "$lock_sha" "$tmp/ghostty-vt/$platform")"
vt_use_artifact "$platform" "$cached"
echo "==> done (prebuilt): $cached"
shasum -a 256 "$(vt_lib_for "$platform")"

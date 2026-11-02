#!/bin/bash
# Shared helpers for the NATIVE libghostty-vt static library used by the
# worker's server-authoritative terminal restore path (internal/ghosttyvt).
#
# Sourced by scripts/build-libghostty-vt.sh (download-first consumer path) and
# scripts/publish-libghostty-vt.sh (maintainer build+upload path).
#
# This file only defines functions and read-only vars; it performs no work and
# is safe to source. Callers must set -euo pipefail themselves.
#
# PER-PLATFORM: the library links into the daemon on darwin/arm64 AND the two
# Linux tuples (the daemon runs headless on Linux; only the Tauri app is
# Mac-only). Every prebuilt asset, output dir, and lock sha256 is keyed by
# <goos>_<goarch>; the source identity `key` (sha256 of the pin) is shared
# across platforms because the same source produces every target.

# This file lives at <repo>/scripts/lib/libghostty-vt.sh; repo root is two up.
vt_lib_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
vt_repo_dir="$(cd "$vt_lib_dir/../.." && pwd)"
vt_pin_file="$vt_repo_dir/ghostty-vt.pin"
vt_lock_file="$vt_repo_dir/ghostty-vt-native.lock"

# Every native target that links the real cgo library. Keep in lockstep with the
# widened //go:build constraint in internal/ghosttyvt and the per-tuple #cgo
# directives in ghosttyvt.go. `publish` builds+uploads all of these.
VT_PLATFORMS=(darwin_arm64 linux_amd64 linux_arm64)

# The single rolling GitHub release that accumulates keyed prebuilt assets.
# Old keys stay downloadable for anyone on an older commit. Never collides with
# the app's version releases (attn vX.Y.Z).
VT_RELEASE_TAG="native-vt-prebuilts"
VT_REPO="victorarias/attn"

# The platform (goos_goarch) a consumer build targets. Honors the explicit
# GHOSTTY_VT_GOOS/GOARCH the Makefile passes when cross-building; otherwise the
# effective Go target (`go env` already folds in any GOOS/GOARCH in the env).
vt_default_platform() {
  local goos goarch
  goos="${GHOSTTY_VT_GOOS:-$(go env GOOS)}"
  goarch="${GHOSTTY_VT_GOARCH:-$(go env GOARCH)}"
  printf '%s_%s' "$goos" "$goarch"
}

vt_platform_goos()   { printf '%s' "${1%%_*}"; }
vt_platform_goarch() { printf '%s' "${1##*_}"; }

# zig -Dtarget / `zig cc -target` triple for a platform tuple. Linux tuples use
# zig's bundled libc (no SDK); darwin needs the macOS SDK via SDKROOT.
vt_zig_target() {
  case "$1" in
    darwin_arm64) printf 'aarch64-macos' ;;
    darwin_amd64) printf 'x86_64-macos' ;;
    linux_amd64)  printf 'x86_64-linux-gnu' ;;
    linux_arm64)  printf 'aarch64-linux-gnu' ;;
    *) echo "error: unsupported native VT platform '$1'" >&2; return 1 ;;
  esac
}

vt_output_dir_for() { printf '%s/third_party/ghostty-vt/%s' "$vt_repo_dir" "$1"; }
vt_lib_for()        { printf '%s/lib/libghostty-vt.a' "$(vt_output_dir_for "$1")"; }

vt_cache_root() {
  local base="${XDG_CACHE_HOME:-}"
  if [[ -z "$base" ]]; then
    if [[ "$(uname -s)" == Darwin ]]; then
      base="$HOME/Library/Caches"
    else
      base="$HOME/.cache"
    fi
  fi
  printf '%s/attn/native-vt' "$base"
}

vt_cache_artifact() (
  local platform="$1" identity="$2" source_dir="$3" parent cached staging
  parent="$(vt_cache_root)/$platform"
  mkdir -p "$parent"
  parent="$(cd "$parent" && pwd -P)"
  cached="$parent/$identity"
  if [[ ! -d "$cached" ]]; then
    staging="$(mktemp -d "$parent/.publish.XXXXXX")"
    trap 'rm -rf "$staging"' EXIT
    mkdir "$staging/$identity"
    cp -R "$source_dir/lib" "$source_dir/include" "$staging/$identity/"
    if [[ -f "$source_dir/LICENSE" ]]; then
      cp "$source_dir/LICENSE" "$staging/$identity/LICENSE"
    fi
    mv -n "$staging/$identity" "$parent/"
  fi
  if [[ ! -f "$cached/lib/libghostty-vt.a" || ! -d "$cached/include" ]]; then
    echo "error: native VT artifact is incomplete: $cached" >&2
    return 1
  fi
  printf '%s' "$cached"
)

vt_use_artifact() {
  local platform="$1" cached="$2" output generated pending quoted
  output="$(vt_output_dir_for "$platform")"
  mkdir -p "$(dirname "$output")"
  if [[ ! -L "$output" || "$(readlink "$output")" != "$cached" ]]; then
    rm -f "$output.source-key"
    rm -rf "$output"
    ln -s "$cached" "$output"
  fi
  generated="$vt_repo_dir/internal/ghosttyvt/native_paths_generated_$platform.go"
  pending="$(mktemp "$generated.XXXXXX")"
  quoted="${cached//\\/\\\\}"
  quoted="${quoted//\"/\\\"}"
  printf 'package ghosttyvt\n\n/*\n#cgo CFLAGS: "-I%s/include"\n#cgo LDFLAGS: "%s/lib/libghostty-vt.a"\n*/\nimport "C"\n' "$quoted" "$quoted" > "$pending"
  if ! cmp -s "$pending" "$generated"; then
    mv "$pending" "$generated"
  else
    rm "$pending"
  fi
}

vt_artifact_digest() {
  (
    cd "$1" || exit
    {
      find lib include -type f
      if [[ -f LICENSE ]]; then printf '%s\n' LICENSE; fi
    } | LC_ALL=C sort | while IFS= read -r path; do
      shasum -a 256 "$path"
    done | shasum -a 256 | awk '{ print $1 }'
  )
}

# The ghostty commit from the pin file (first non-comment, non-blank line).
vt_commit() {
  local commit
  commit="$(grep -vE '^\s*(#|$)' "$vt_pin_file" | head -n1 | tr -d '[:space:]')"
  if [[ -z "$commit" ]]; then
    echo "error: no commit found in $vt_pin_file" >&2
    return 1
  fi
  printf '%s' "$commit"
}

# Source identity of the produced artifacts: sha256 over the pin commit, which
# is what determines the bytes — so the key changes when the pin moves, which is
# how a locally-edited pin is detected as "no published asset yet" and routed to
# a source build. Platform-independent: the same source builds every target, so
# one key covers all tuples; the asset filename adds the tuple.
vt_key() {
  {
    vt_commit
    printf '\n'
  } | shasum -a 256 | cut -d' ' -f1
}

# Asset filename for a platform: libghostty-vt-<key>-<goos>_<goarch>.tar.gz.
vt_asset_name_for() {
  printf 'libghostty-vt-%s-%s.tar.gz' "$(vt_key)" "$1"
}

# Read a `key=value` field from the lock file; empty if absent. Per-platform
# sha256 fields are named sha256_<goos>_<goarch>.
vt_lock_field() {
  local field="$1"
  [[ -f "$vt_lock_file" ]] || return 0
  grep -E "^${field}=" "$vt_lock_file" | head -n1 | cut -d= -f2- | tr -d '[:space:]'
}

vt_zig() {
  local zig="${ZIG:-}"
  if [[ -z "$zig" ]] && command -v asdf >/dev/null 2>&1; then
    zig="$(asdf which zig 2>/dev/null || true)"
  fi
  if [[ -z "$zig" ]]; then
    zig="$(command -v zig || true)"
  fi
  printf '%s' "$zig"
}

vt_source_key() {
  local platform="$1" zig
  zig="$(vt_zig)"
  {
    vt_commit
    printf '\n%s\n%s\n%s\n' "$platform" "$zig" "${SDKROOT:-}"
    "$zig" version
    if [[ "$platform" == darwin_* ]]; then xcrun --show-sdk-version; fi
    cd "$vt_repo_dir" || exit
    shasum -a 256 scripts/build-libghostty-vt.sh scripts/lib/libghostty-vt.sh .tool-versions
  } | shasum -a 256 | awk '{ print $1 }'
}

# Build the archive + headers for one platform from pinned source into its
# per-platform output dir. Requires zig 0.16.x and network access to clone
# ghostty. This is the maintainer path and the consumer fallback; ordinary
# contributors never reach it (they download a prebuilt).
vt_build_from_source() {
  local platform="${1:-$(vt_default_platform)}"
  local goos ztarget sdkroot workdir zig zig_version
  goos="$(vt_platform_goos "$platform")"
  ztarget="$(vt_zig_target "$platform")"

  zig="$(vt_zig)"
  if [[ -z "$zig" ]]; then
    echo "error: zig not found on PATH; need zig 0.16.x to build libghostty-vt from source" >&2
    echo "       (consumers normally download a prebuilt asset — this path only runs when" >&2
    echo "        you have changed ghostty-vt.pin, or forced" >&2
    echo "        ATTN_VT_FROM_SOURCE=1)" >&2
    return 1
  fi
  zig_version="$("$zig" version)"
  case "$zig_version" in
    0.16.*) ;;
    *) echo "error: need zig 0.16.x, found $zig_version (asdf: 'asdf local zig 0.16.0')" >&2; return 1 ;;
  esac

  # Only the macOS target needs the platform SDK; Linux targets use zig's
  # bundled libc headers and cross-build from any host.
  sdkroot=""
  if [[ "$goos" == "darwin" ]]; then
    sdkroot="${SDKROOT:-$(xcrun --show-sdk-path)}"
  fi

  workdir="$(mktemp -d "${TMPDIR:-/tmp}/attn-libghostty-vt.XXXXXX")"
  # shellcheck disable=SC2064
  trap "rm -rf '$workdir'" RETURN

  local commit
  commit="$(vt_commit)"
  echo "==> [$platform] cloning ghostty @ $commit"
  git init -q "$workdir/ghostty"
  git -C "$workdir/ghostty" remote add origin https://github.com/ghostty-org/ghostty.git
  git -C "$workdir/ghostty" fetch -q --depth=1 origin "$commit"
  git -C "$workdir/ghostty" checkout -q --detach FETCH_HEAD

  echo "==> [$platform] zig build -Demit-lib-vt=true -Dtarget=$ztarget (zig $zig_version)"
  (
    cd "$workdir/ghostty"
    env ${sdkroot:+SDKROOT="$sdkroot"} "$zig" build \
      -Demit-lib-vt=true \
      -Dtarget="$ztarget" \
      -Doptimize=ReleaseFast \
      --summary none \
      --prefix "$workdir/prefix"
  )

  local artifact identity cached
  artifact="$workdir/artifact"
  mkdir "$artifact"
  cp -R "$workdir/prefix/lib" "$workdir/prefix/include" "$artifact/"
  if [[ -f "$workdir/ghostty/LICENSE" ]]; then
    cp "$workdir/ghostty/LICENSE" "$artifact/LICENSE"
  fi
  identity="$(vt_artifact_digest "$artifact")"
  cached="$(vt_cache_artifact "$platform" "$identity" "$artifact")"
  vt_use_artifact "$platform" "$cached"
  vt_source_key "$platform" > "$(vt_output_dir_for "$platform").source-key"
}

# Pack one platform's installed archive + headers + LICENSE into a tarball whose
# members are rooted at ghostty-vt/<platform>/ (so a consumer extracts it
# straight into third_party/).
vt_pack() {
  local dest="$1" platform="$2" outdir
  local members=("ghostty-vt/$platform/lib" "ghostty-vt/$platform/include")
  outdir="$(vt_output_dir_for "$platform")"
  if [[ -f "$outdir/LICENSE" ]]; then
    members+=("ghostty-vt/$platform/LICENSE")
  fi
  tar -C "$vt_repo_dir/third_party" -czf "$dest" "${members[@]}"
}

#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/lib/libghostty-vt.sh"

readonly usage="usage: rust-cli.sh key [--target TRIPLE]
       rust-cli.sh entry [--target TRIPLE]
       rust-cli.sh build --out PATH [--target TRIPLE]"
readonly profile=release

die() {
  echo "error: $*" >&2
  exit 1
}

host_triple() {
  case "$(uname -s)-$(uname -m)" in
    Darwin-arm64) printf 'aarch64-apple-darwin' ;;
    Linux-x86_64) printf 'x86_64-unknown-linux-gnu' ;;
    Linux-aarch64) printf 'aarch64-unknown-linux-gnu' ;;
    *) die "no Rust triple known for host $(uname -s)-$(uname -m)" ;;
  esac
}

zig_link_target() {
  case "$1" in
    x86_64-unknown-linux-gnu) printf 'x86_64-linux-gnu' ;;
    aarch64-unknown-linux-gnu) printf 'aarch64-linux-gnu' ;;
    *) die "cannot cross-build for '$1': --target takes x86_64-unknown-linux-gnu or aarch64-unknown-linux-gnu" ;;
  esac
}

cli_key() {
  (
    cd "$vt_repo_dir"
    cat rust-toolchain.toml
    printf '%s\n%s\n' "$triple" "$profile"
    if [[ -n "$link_target" ]]; then
      printf '%s\n' "$link_target"
      cat scripts/rust-zig-linker.sh scripts/rust-zig-ar.sh
    fi
    find cli -path cli/target -prune -o -type f -print0 | LC_ALL=C sort -z | xargs -0 shasum -a 256
  ) | shasum -a 256 | cut -d' ' -f1
}

entry_for() {
  printf '%s/attn-cli/%s/%s' "$(dirname "$(vt_cache_root)")" "$triple" "$1"
}

cargo_build() {
  local cargo_args=(cargo build --manifest-path "$vt_repo_dir/cli/Cargo.toml" --target-dir "$vt_repo_dir/cli/target" --release --locked)
  if [[ -z "$link_target" ]]; then
    "${cargo_args[@]}"
    return
  fi
  local triple_env upper
  triple_env="$(printf '%s' "$triple" | tr '-' '_')"
  upper="$(printf '%s' "$triple_env" | tr '[:lower:]' '[:upper:]')"
  env \
    ATTN_RUST_LINK_TARGET="$link_target" \
    "CC_$triple_env=$vt_repo_dir/scripts/rust-zig-linker.sh" \
    "AR_$triple_env=$vt_repo_dir/scripts/rust-zig-ar.sh" \
    "CARGO_TARGET_${upper}_LINKER=$vt_repo_dir/scripts/rust-zig-linker.sh" \
    "${cargo_args[@]}" --target "$triple"
}

place_by_rename() {
  local source="$1" destination="$2" pending
  mkdir -p "$(dirname "$destination")"
  pending="$(mktemp "$destination.XXXXXX")"
  cp "$source" "$pending"
  chmod 755 "$pending"
  mv -f "$pending" "$destination"
}

build() {
  local key stored built
  key="$(cli_key)"
  stored="$(entry_for "$key")/attn"
  if [[ -f "$stored" ]]; then
    place_by_rename "$stored" "$out"
    return
  fi
  cargo_build
  if [[ -n "$link_target" ]]; then
    built="$vt_repo_dir/cli/target/$triple/$profile/attn"
  else
    built="$vt_repo_dir/cli/target/$profile/attn"
  fi
  if [[ "$(cli_key)" == "$key" ]]; then
    place_by_rename "$built" "$stored"
  else
    echo "warning: cli/ changed during the build, so this binary is not cached" >&2
  fi
  place_by_rename "$built" "$out"
}

command="${1:-}"
[[ $# -gt 0 ]] && shift
out=""
target=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --out) [[ $# -ge 2 ]] || die "--out needs a path"; out="$2"; shift 2 ;;
    --target) [[ $# -ge 2 ]] || die "--target needs a triple"; target="$2"; shift 2 ;;
    *) die "unknown argument '$1'"$'\n'"$usage" ;;
  esac
done

link_target=""
triple="$(host_triple)"
if [[ -n "$target" ]]; then
  link_target="$(zig_link_target "$target")"
  triple="$target"
fi

case "$command" in
  key)
    [[ -z "$out" ]] || die "--out applies only to build"
    cli_key
    echo
    ;;
  entry)
    [[ -z "$out" ]] || die "--out applies only to build"
    entry_for "$(cli_key)"
    echo
    ;;
  build)
    [[ -n "$out" ]] || die "build needs --out PATH"$'\n'"$usage"
    build
    ;;
  *)
    die "$usage"
    ;;
esac

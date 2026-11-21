#!/usr/bin/env bash
set -euo pipefail

readonly usage="usage: rust-cli.sh key [--target TRIPLE]
       rust-cli.sh entry [--target TRIPLE]
       rust-cli.sh build --out PATH [--target TRIPLE]"
readonly build_store="$(dirname "${BASH_SOURCE[0]}")/lib/rust-build-store.sh"

die() {
  echo "error: $*" >&2
  exit 1
}

command="${1:-}"
[[ $# -gt 0 ]] && shift
out=""
target_args=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --out) [[ $# -ge 2 ]] || die "--out needs a path"; out="$2"; shift 2 ;;
    --target) [[ $# -ge 2 ]] || die "--target needs a triple"; target_args=(--target "$2"); shift 2 ;;
    *) die "unknown argument '$1'"$'\n'"$usage" ;;
  esac
done

case "$command" in
  key | entry)
    [[ -z "$out" ]] || die "--out applies only to build"
    bash "$build_store" "$command" attn ${target_args[@]+"${target_args[@]}"}
    ;;
  build)
    [[ -n "$out" ]] || die "build needs --out PATH"$'\n'"$usage"
    built="$(bash "$build_store" provide attn ${target_args[@]+"${target_args[@]}"})"
    mkdir -p "$(dirname "$out")"
    pending="$(mktemp "$out.XXXXXX")"
    cp "$built" "$pending"
    chmod 755 "$pending"
    mv -f "$pending" "$out"
    ;;
  *)
    die "$usage"
    ;;
esac

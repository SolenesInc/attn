#!/usr/bin/env bash
set -euo pipefail
unset MAKEFLAGS MFLAGS MAKELEVEL
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Pinning these `?=` values skips their shell expansions: 23.3s of a 28.5s run.
export VERSION=probe BUILD_TIME=probe SOURCE_FINGERPRINT=probe GIT_COMMIT=probe SNAPSHOT_FORMAT=probe

probe() {
  local witness="$1" newer="$2" target="$3" plan
  # Captured whole: `grep -q` closes the pipe early and SIGPIPEs make, which
  # pipefail then reports as a missing dependency.
  plan="$(make -C "$root" -n -W "$newer" "$target" 2>/dev/null)"
  grep -q -- "$witness" <<<"$plan"
}

fetches_vt()   { probe build-libghostty-vt.sh scripts/build-libghostty-vt.sh "$1"; }
installs_app() { probe 'pnpm --dir app install' app/pnpm-lock.yaml "$1"; }

failed=0
fail() {
  echo "$*" >&2
  failed=1
}

case "$(go env GOOS)_$(go env GOARCH)" in
  darwin_arm64 | linux_amd64 | linux_arm64)
    for target in lint lint-go test test-v test-quick test-watch build; do
      fetches_vt "$target" ||
        fail "make $target does not fetch the native VT archive: a fresh checkout fails on ghostty/vt.h"
    done
    ! fetches_vt lint-frontend ||
      fail "the -W probe claims lint-frontend needs the native VT archive, which it does not"
    ;;
  *) echo "native VT probes skipped: this platform compiles the pure-Go stub" ;;
esac

for target in lint lint-frontend test-frontend test-e2e test-all test-harness; do
  installs_app "$target" ||
    fail "make $target does not install app/node_modules: a fresh checkout fails on a missing oxlint"
done
! installs_app lint-go ||
  fail "the -W probe claims lint-go needs app/node_modules, which it does not"

[[ "$failed" -eq 0 ]] || exit 1
echo 'fresh checkout tests passed'

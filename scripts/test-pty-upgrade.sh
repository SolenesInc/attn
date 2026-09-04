#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
# Last daemon revision before the Rust host: never build both sides from HEAD.
legacy_revision=f68ab3f02f329f46d3b9b3d1e7603f545c27dd50
test_root="$(mktemp -d "${TMPDIR:-/tmp}/attn-pty-upgrade.XXXXXX")"
trap 'rm -rf "$test_root"' EXIT

if [ -z "${ATTN_TEST_PTY_HOST:-}" ]; then
  make build-pty-host
  export ATTN_TEST_PTY_HOST="$root/pty-host/target/release/attn-pty-host"
fi
if [ -z "${ATTN_UPGRADE_OLD_BIN:-}" ]; then
  mkdir -p "$test_root/legacy"
  git archive "$legacy_revision" | tar -x -C "$test_root/legacy"
  platform="$(go env GOOS)_$(go env GOARCH)"
  if [ -d "third_party/ghostty-vt/$platform" ] && cmp -s ghostty-vt.pin "$test_root/legacy/ghostty-vt.pin" && cmp -s ghostty-vt-native.lock "$test_root/legacy/ghostty-vt-native.lock"; then
    mkdir -p "$test_root/legacy/third_party/ghostty-vt"
    cp -R "third_party/ghostty-vt/$platform" "$test_root/legacy/third_party/ghostty-vt/$platform"
  else
    (cd "$test_root/legacy" && bash scripts/build-libghostty-vt.sh)
  fi
  (cd "$test_root/legacy" && go build \
    -ldflags "-X github.com/victorarias/attn/internal/buildinfo.SnapshotFormat=$(bash scripts/snapshot-format.sh)" \
    -o "$test_root/attn-legacy" ./cmd/attn)
  export ATTN_UPGRADE_OLD_BIN="$test_root/attn-legacy"
fi
if [ -z "${ATTN_E2E_BIN:-}" ]; then
  go build -ldflags "-X github.com/victorarias/attn/internal/buildinfo.SnapshotFormat=$(bash scripts/snapshot-format.sh)" \
    -o "$test_root/attn-current" ./cmd/attn
  export ATTN_E2E_BIN="$test_root/attn-current"
fi
go test ./internal/daemon -run '^TestPTYUpgradeAcrossDaemonBinaries$' -count=1 -timeout=180s -v "$@"

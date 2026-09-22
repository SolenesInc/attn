#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
# Oldest shared host a current daemon must keep operating: the last build
# before host incarnations and artifact validation.
retained_revision=9542a9ef5e0d021b162c5cc3766e345b76970a1a
output="${1:?usage: build-retained-pty-host.sh OUTPUT_PATH}"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-retained-host.XXXXXX")"
trap 'rm -rf "$work"' EXIT

if ! git cat-file -e "$retained_revision^{commit}" 2>/dev/null; then
  git fetch --quiet --depth=1 origin "$retained_revision"
fi
git archive "$retained_revision" | tar -x -C "$work"
platform="$(go env GOOS)_$(go env GOARCH)"
if [ -d "third_party/ghostty-vt/$platform" ] && cmp -s ghostty-vt.pin "$work/ghostty-vt.pin" && cmp -s ghostty-vt-native.lock "$work/ghostty-vt-native.lock"; then
  mkdir -p "$work/third_party/ghostty-vt"
  cp -R "third_party/ghostty-vt/$platform" "$work/third_party/ghostty-vt/$platform"
else
  (cd "$work" && bash scripts/build-libghostty-vt.sh)
fi
(cd "$work" && ATTN_PTY_HOST_SNAPSHOT_FORMAT="$(bash scripts/snapshot-format.sh)" \
  cargo build --manifest-path pty-host/Cargo.toml --release --locked)
mkdir -p "$(dirname "$output")"
cp "$work/pty-host/target/release/attn-pty-host" "$output"

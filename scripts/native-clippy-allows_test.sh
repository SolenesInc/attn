#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/attn-native-clippy-allows-test.XXXXXX")" && pwd)"
trap 'rm -rf "$work"' EXIT

ui="$work/native/crates/attn-ui/src"
app="$work/native/crates/attn-app/src"
mkdir -p "$ui/system" "$app/settings"
printf '#![allow(clippy::disallowed_methods)]\nfn door(e: El) -> El {\n    e.gap(4.0)\n}\n' >"$ui/system/door.rs"
printf '# a comment\nnative/crates/attn-ui/src/system/door.rs\tthe door\n' >"$work/native/clippy-allows.tsv"

run() {
  NATIVE_CLIPPY_ALLOWS_ROOT="$work" bash "$root/scripts/native-clippy-allows" >"$work/out" 2>&1
}

fail() {
  echo "native-clippy-allows_test: $1" >&2
  cat "$work/out" >&2
  exit 1
}

run || fail "a listed file carrying the allow was refused"

printf '#[allow(clippy::disallowed_methods)]\nfn view() {}\n' >"$app/settings/escape.rs"
if run; then fail "an allow outside the list was accepted"; fi
grep -qF 'native/crates/attn-app/src/settings/escape.rs carries allow(clippy::disallowed_methods)' "$work/out" ||
  fail "the file carrying the allow was not named"

echo "native-clippy-allows_test: ok"

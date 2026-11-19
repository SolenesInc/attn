#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/attn-native-ui-budget-test.XXXXXX")" && pwd)"
trap 'rm -rf "$work"' EXIT

ui="$work/native/crates/attn-ui/src"
app="$work/native/crates/attn-app/src"
mkdir -p "$ui/system" "$app/settings"
printf 'fn door(e: El) -> El {\n    e.gap(4.0)\n}\n' >"$ui/system/door.rs"
printf 'fn view(e: El) -> El {\n    // e.px(2.0) in a comment is not a call\n    e.px(8.0).radius(6.0)\n}\n' >"$app/settings/page.rs"
printf 'fn lib() {}\n' >"$app/lib.rs"

run() {
  NATIVE_UI_BUDGET_ROOT="$work" bash "$root/scripts/native-ui-budget" >"$work/out" 2>&1
}

fail() {
  echo "native-ui-budget_test: $1" >&2
  cat "$work/out" >&2
  exit 1
}

budget() {
  printf 'budget\tattn-ui/src\t0\nbudget\tattn-app/src/*.rs\t0\nbudget\tattn-app/src/settings\t%s\n' "$1" >"$work/native/ui-budget.tsv"
  printf 'allow\tnative/crates/attn-ui/src/system/door.rs\n' >>"$work/native/ui-budget.tsv"
}

budget 2
run || fail "counts at their lines were refused"

budget 1
if run; then fail "a directory above its line was accepted"; fi
grep -qF 'attn-app/src/settings has 2 raw spacing, font and corner calls, above its line of 1' "$work/out" ||
  fail "the directory above its line was not named"

budget 3
if run; then fail "a directory below its line was accepted"; fi
grep -qF 'lower the line in native/ui-budget.tsv to 2' "$work/out" || fail "the lower line was not given"

budget 2
printf '#![allow(clippy::disallowed_methods)]\n' >"$app/settings/escape.rs"
if run; then fail "an allow outside the allow rows was accepted"; fi
grep -qF 'native/crates/attn-app/src/settings/escape.rs carries allow(clippy::disallowed_methods)' "$work/out" ||
  fail "the file carrying the allow was not named"
mv "$app/settings/escape.rs" "$work/escape.rs"

mkdir -p "$app/garden"
if run; then fail "a directory with no line was accepted"; fi
grep -qF 'attn-app/src/garden has no line' "$work/out" || fail "the directory with no line was not named"

echo "native-ui-budget_test: ok"

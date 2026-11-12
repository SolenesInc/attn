#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/attn-native-parity-test.XXXXXX")" && pwd)"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/scripts" "$work/app/src" "$work/native/parity" "$work/native/crates/attn-app/tests/wire"
cp "$root/scripts/native-parity" "$work/scripts/"
printf 'item\tstatement\tnative_test\treason\n' >"$work/native/parity/checklist.tsv"
printf '#[test]\nfn mapped() {}\n' >"$work/native/crates/attn-app/tests/wire/picker.rs"
cat >"$work/app/src/quotes.test.tsx" <<'EOF'
describe('the picker\'s list', () => {
  it("shows the \"recent\" folders", () => {});
  it(`keeps the \`tab\` key`, () => {});
  it("asks for this machine's recents", () => {});
});
EOF

check() {
  local names=("$@") name
  {
    printf 'vitest_file\tvitest_test\tnative_test\treason\n'
    for name in "${names[@]}"; do
      printf 'app/src/quotes.test.tsx\t%s\tattn-app/tests/wire/picker.rs::mapped\t\n' "$name"
    done
  } >"$work/native/parity/wire.tsv"
  bash "$work/scripts/native-parity" >"$work/out" 2>&1
}

check "the picker's list > shows the \"recent\" folders" \
  "the picker's list > keeps the \`tab\` key" \
  "the picker's list > asks for this machine's recents" ||
  { echo "native-parity_test: a name with an escaped or embedded quote was not found:" >&2; cat "$work/out" >&2; exit 1; }

if check "the picker's list > hides the \"recent\" folders"; then
  echo "native-parity_test: a name the file does not have was accepted:" >&2
  cat "$work/out" >&2
  exit 1
fi
grep -qF 'has no describe or test named "hides the "recent" folders"' "$work/out" ||
  { echo "native-parity_test: the missing name was not reported:" >&2; cat "$work/out" >&2; exit 1; }

echo "native-parity_test: ok"

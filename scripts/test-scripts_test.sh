#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runner="$root/scripts/test-scripts.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-test-scripts-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

cat >"$work/pass_test.sh" <<'EOF'
echo "pass output"
EOF
cat >"$work/fail_test.sh" <<'EOF'
echo "fail output"
echo "fail diagnostics" >&2
exit 1
EOF

if ! out="$(bash "$runner" "$work/pass_test.sh" 2>&1)"; then
  echo "a passing test failed the runner: $out" >&2
  exit 1
fi
if grep -q "pass output" <<<"$out"; then
  echo "a passing test's output should stay quiet: $out" >&2
  exit 1
fi
grep -q "^ok .*pass_test.sh" <<<"$out" || { echo "missing ok line: $out" >&2; exit 1; }

if out="$(bash "$runner" "$work/pass_test.sh" "$work/fail_test.sh" 2>&1)"; then
  echo "a failing test passed the runner: $out" >&2
  exit 1
fi
for want in "^FAIL .*fail_test.sh" "^--- .*fail_test.sh" "fail output" "fail diagnostics"; do
  grep -q -- "$want" <<<"$out" || { echo "missing '$want' in: $out" >&2; exit 1; }
done

if bash "$runner" >/dev/null 2>&1; then
  echo "an empty test list should be a usage error" >&2
  exit 1
fi

echo "test-scripts runner tests passed"

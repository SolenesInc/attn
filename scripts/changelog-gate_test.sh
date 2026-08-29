#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
gate="$root/scripts/changelog-gate.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-changelog-gate-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

git init -q -b main "$work/repo"
git -C "$work/repo" config user.name 'Changelog Gate Test'
git -C "$work/repo" config user.email 'changelog-gate@example.com'
printf '%s\n' '# fixture' >"$work/repo/README.md"
git -C "$work/repo" add README.md
git -C "$work/repo" commit -q -m 'initial fixture'
git -C "$work/repo" branch next

expect_success() {
  if ! (cd "$work/repo" && "$gate" "$@") >/dev/null; then
    echo "expected changelog gate to pass: $*" >&2
    exit 1
  fi
}

expect_failure() {
  if (cd "$work/repo" && "$gate" "$@") >/dev/null 2>&1; then
    echo "expected changelog gate to fail: $*" >&2
    exit 1
  fi
}

expect_success main release/v1.2.3
expect_success next sync/main-into-next-0123456789ab

expect_failure main release/1.2.3
expect_failure main release/v1.2
expect_failure next release/v1.2.3
expect_failure main sync/main-into-next-0123456789ab

echo "changelog gate: OK"

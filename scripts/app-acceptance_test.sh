#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$root/scripts/app-acceptance.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-app-acceptance-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

sha="0123456789abcdef0123456789abcdef01234567"
other_sha="abcdef0123456789abcdef0123456789abcdef01"

GITHUB_STEP_SUMMARY="$work/summary" "$script" "$sha" "$sha" passed \
  release-test 'startup, queue handover' 'https://example.com/evidence' \
  >"$work/output"

for value in "$sha" release-test 'startup, queue handover' \
  'https://example.com/evidence'; do
  grep -Fq "$value" "$work/summary"
done
grep -q 'exact candidate head accepted' "$work/output"

expect_failure() {
  if "$@" >/dev/null 2>&1; then
    echo "expected failure: $*" >&2
    exit 1
  fi
}

expect_failure "$script" "$other_sha" "$sha" passed profile scenarios evidence
expect_failure "$script" "$sha" short passed profile scenarios evidence
expect_failure "$script" "$sha" "$sha" failed profile scenarios evidence
expect_failure "$script" "$sha" "$sha" unknown profile scenarios evidence

for value in \
  'run-name: App acceptance ${{ inputs.candidate_sha }}' \
  'ref: ${{ github.sha }}' \
  'path: authority' \
  'ref: ${{ inputs.candidate_sha }}' \
  'path: candidate' \
  './authority/scripts/app-acceptance.sh'; do
  grep -Fq "$value" "$root/.github/workflows/app-acceptance.yml"
done

echo "app acceptance: OK"

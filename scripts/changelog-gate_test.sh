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

expect_failure main hotfix/unprepared
expect_failure main release/1.2.3
expect_failure main release/v1.2
expect_failure next release/v1.2.3
expect_failure main sync/main-into-next-0123456789ab

hotfix_repo="$work/hotfix-repo"
git clone -q "$root" "$hotfix_repo"
git -C "$hotfix_repo" config user.name 'Changelog Gate Test'
git -C "$hotfix_repo" config user.email 'changelog-gate@example.com'
git -C "$hotfix_repo" switch -q -C main
git -C "$hotfix_repo" rm -q -- 'changelog.d/*.yaml'
previous_source="$(git -C "$hotfix_repo" rev-parse HEAD)"
cat >"$hotfix_repo/.github/release-candidate.yml" <<EOF
version: 0.11.1
kind: promotion
source_sha: ${previous_source}
main_sha: ${previous_source}
EOF
git -C "$hotfix_repo" add changelog.d .github/release-candidate.yml
git -C "$hotfix_repo" commit -q -m 'release baseline'
main_sha="$(git -C "$hotfix_repo" rev-parse HEAD)"
git -C "$hotfix_repo" update-ref refs/remotes/origin/main "$main_sha"

git -C "$hotfix_repo" switch -q -c hotfix/forged
sed -i.bak 's/kind: promotion/kind: hotfix/' \
  "$hotfix_repo/.github/release-candidate.yml"
rm "$hotfix_repo/.github/release-candidate.yml.bak"
git -C "$hotfix_repo" add .github/release-candidate.yml
git -C "$hotfix_repo" commit -q -m 'forge hotfix manifest'
if (cd "$hotfix_repo" && "$gate" main hotfix/forged) >/dev/null 2>&1; then
  echo "forged hotfix manifest bypassed the changelog gate" >&2
  exit 1
fi

git -C "$hotfix_repo" switch -q main
git -C "$hotfix_repo" switch -q -c hotfix/prepared
printf '%s\n' 'fix' >"$hotfix_repo/hotfix.txt"
printf '%s\n' 'kind: internal' 'area: release' 'change: hotfix fixture' \
  >"$hotfix_repo/changelog.d/hotfix.yaml"
git -C "$hotfix_repo" add hotfix.txt changelog.d/hotfix.yaml
git -C "$hotfix_repo" commit -q -m 'fix(app): add prepared hotfix'
source_sha="$(git -C "$hotfix_repo" rev-parse HEAD)"
(cd "$hotfix_repo" && go run ./cmd/release-train version set v99.98.98)
(cd "$hotfix_repo" && go run ./cmd/release-train manifest write \
  --version v99.98.98 --kind hotfix --source "$source_sha" --main "$main_sha")
git -C "$hotfix_repo" rm -q changelog.d/hotfix.yaml
git -C "$hotfix_repo" add .github/release-candidate.yml app
git -C "$hotfix_repo" commit -q -m 'chore(release): prepare v99.98.98'
if ! (cd "$hotfix_repo" && "$gate" main hotfix/prepared) >/dev/null; then
  echo "prepared hotfix did not receive its changelog exemption" >&2
  exit 1
fi

echo "changelog gate: OK"

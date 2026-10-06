#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
gate="$root/scripts/changelog-gate.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-changelog-gate-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/pulls?state=open&base=main&per_page=100'* ]]; then
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *'/actions/workflows/app-acceptance.yml/runs?'* ]] && \
  [[ "$*" =~ App\ acceptance\ ([0-9a-f]{40}) ]]; then
  printf '2026-08-29T10:00:00Z\t43\tApp acceptance %s\tcompleted\tsuccess\t%s\n' "${BASH_REMATCH[1]}" \
    'https://github.com/example/attn/actions/runs/43'
  exit 0
fi
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/actions/runs/43/jobs?'* ]]; then
  printf 'completed\tsuccess\t%s\n' \
    'https://github.com/example/attn/actions/runs/43/job/8'
  exit 0
fi
echo "unexpected gh command: $*" >&2
exit 2
EOF
chmod +x "$work/bin/gh"
export PATH="$work/bin:$PATH"
export GOCACHE="$work/go-cache"
export GITHUB_REPOSITORY=example/attn

git init -q -b main "$work/repo"
git -C "$work/repo" config user.name 'Changelog Gate Test'
git -C "$work/repo" config user.email 'changelog-gate@example.com'
printf '%s\n' '# fixture' >"$work/repo/README.md"
git -C "$work/repo" add README.md
git -C "$work/repo" commit -q -m 'initial fixture'

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

expect_failure main release/v1.2.3
expect_failure main release/1.2.3
expect_failure main release/v1.2
expect_failure main feat/no-fragment

release_repo="$work/release-repo"
git clone -q "$root" "$release_repo"
git -C "$release_repo" config user.name 'Changelog Gate Test'
git -C "$release_repo" config user.email 'changelog-gate@example.com'
git -C "$release_repo" switch -q -C main
cp "$root/cmd/release-train/main.go" "$release_repo/cmd/release-train/main.go"
git -C "$release_repo" add cmd/release-train/main.go
git -C "$release_repo" rm -q --ignore-unmatch -- 'changelog.d/*.yaml'
printf '%s\n' 'kind: internal' 'area: release' 'change: release fixture' \
  >"$release_repo/changelog.d/release-fixture.yaml"
git -C "$release_repo" add changelog.d
git -C "$release_repo" commit -q -m 'release fixture baseline'
main_sha="$(git -C "$release_repo" rev-parse HEAD)"
git -C "$release_repo" update-ref refs/remotes/origin/main "$main_sha"

git -C "$release_repo" switch -q -c feat/with-fragment
printf '%s\n' 'kind: fixed' 'area: app' 'change: ordinary fix' \
  >"$release_repo/changelog.d/ordinary.yaml"
git -C "$release_repo" add changelog.d/ordinary.yaml
git -C "$release_repo" commit -q -m 'fix(app): ordinary fix'
if ! (cd "$release_repo" && "$gate" main feat/with-fragment) >/dev/null; then
  echo "ordinary PR with a fragment did not pass the changelog gate" >&2
  exit 1
fi

git -C "$release_repo" switch -q -c release/v99.98.98 "$main_sha"
(cd "$release_repo" && go run ./cmd/release-train version set v99.98.98)
(cd "$release_repo" && go run ./cmd/release-train manifest write --version v99.98.98 --main "$main_sha")
git -C "$release_repo" rm -q changelog.d/release-fixture.yaml
git -C "$release_repo" add .github/release-candidate.yml app
git -C "$release_repo" commit -q -m 'chore(release): prepare v99.98.98'
prepared_sha="$(git -C "$release_repo" rev-parse HEAD)"
if ! (cd "$release_repo" && "$gate" main release/v99.98.98 "$prepared_sha") >/dev/null; then
  echo "prepared release did not receive its changelog exemption" >&2
  exit 1
fi
if (cd "$release_repo" && "$gate" main release/v99.98.97 "$prepared_sha") >/dev/null 2>&1; then
  echo "a release branch carrying another version's manifest passed the gate" >&2
  exit 1
fi

for value in \
  '"${{ github.event.pull_request.head.sha }}"' \
  'HEAD_REF="${3:-HEAD}"' \
  'candidate-gate.sh" "$main_ref" "$HEAD_REF"'; do
  grep -Fq "$value" "$root/.github/workflows/ci.yml" "$root/scripts/changelog-gate.sh"
done

echo "changelog gate: OK"

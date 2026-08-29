#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
gate="$root/scripts/release-tag-gate.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-release-tag-gate-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == api ]] && [[ "$*" == *'/pulls'* ]]; then
  printf '42\t%s\trelease/v99.98.97\thttps://github.com/example/attn/pull/42\n' \
    "$FAKE_CANDIDATE_SHA"
  exit 0
fi
if [[ "$1 $2" == "api --method" ]] && [[ "$*" == *'check_name=App%20acceptance'* ]]; then
  printf '%s\t%s\t%s\t%s\n' "$FAKE_CANDIDATE_SHA" completed success \
    'https://github.com/example/attn/actions/runs/43/job/8'
  exit 0
fi
if [[ "$1 $2" == "api --method" ]] && [[ "${FAKE_ACCEPTANCE_MODE:-success}" != missing ]]; then
  printf '%s\t%s\t%s\t%s\n' "$FAKE_ACCEPTANCE_SHA" \
    "${FAKE_ACCEPTANCE_STATUS:-completed}" \
    "${FAKE_ACCEPTANCE_CONCLUSION:-success}" \
    'https://github.com/example/attn/actions/runs/42/job/7'
  exit 0
fi
echo "unexpected gh command: $*" >&2
exit 2
EOF
chmod +x "$work/bin/gh"

export PATH="$work/bin:$PATH"
export GITHUB_REPOSITORY=example/attn
export GOCACHE="$work/go-cache"
export FAKE_ACCEPTANCE_MODE=success
export FAKE_ACCEPTANCE_STATUS=completed
export FAKE_ACCEPTANCE_CONCLUSION=success
export FAKE_CANDIDATE_SHA=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb

repo="$work/repo"
git clone -q "$root" "$repo"
git -C "$repo" config user.name 'Release Tag Gate Test'
git -C "$repo" config user.email 'release-tag-gate@example.com'
git -C "$repo" switch -q -C main
git -C "$repo" rm -q -- 'changelog.d/*.yaml'
baseline_sha="$(git -C "$repo" rev-parse HEAD)"
(cd "$repo" && go run ./cmd/release-train version set v99.98.97 >/dev/null)
cat >"$repo/.github/release-candidate.yml" <<EOF
version: 99.98.97
kind: promotion
source_sha: $baseline_sha
main_sha: $baseline_sha
EOF
git -C "$repo" add -A
git -C "$repo" commit -q -m 'release: accepted main fixture'
accepted_sha="$(git -C "$repo" rev-parse HEAD)"
git -C "$repo" tag v99.98.97
git -C "$repo" update-ref refs/remotes/origin/main "$accepted_sha"
export FAKE_ACCEPTANCE_SHA="$accepted_sha"

expect_failure() {
  local expected="$1"
  shift
  if "$@" >"$work/failure.out" 2>&1; then
    echo "expected release tag gate failure: $*" >&2
    exit 1
  fi
  if ! grep -Fq "$expected" "$work/failure.out"; then
    echo "failure did not contain '$expected':" >&2
    cat "$work/failure.out" >&2
    exit 1
  fi
}

run_gate() (
  cd "$repo"
  "$gate" "$@"
)

run_gate v99.98.97 >"$work/success.out"
grep -Fq 'v99.98.97 is accepted' "$work/success.out"

export FAKE_ACCEPTANCE_CONCLUSION=failure
expect_failure 'Acceptance is completed/failure' run_gate v99.98.97
export FAKE_ACCEPTANCE_CONCLUSION=success

git -C "$repo" switch -q -c detached-release "$baseline_sha"
printf '%s\n' 'detached release' >"$repo/detached-release.txt"
git -C "$repo" add detached-release.txt
git -C "$repo" commit -q -m 'forge detached release'
git -C "$repo" tag v99.98.96
expect_failure 'is not part of main' run_gate v99.98.96

git -C "$repo" switch -q main
(cd "$repo" && go run ./cmd/release-train version set v99.98.96 >/dev/null)
git -C "$repo" add app
git -C "$repo" commit -q -m 'forge release versions'
forged_sha="$(git -C "$repo" rev-parse HEAD)"
git -C "$repo" tag -f v99.98.97 "$forged_sha" >/dev/null
git -C "$repo" update-ref refs/remotes/origin/main "$forged_sha"
export FAKE_ACCEPTANCE_SHA="$forged_sha"
expect_failure 'expected 99.98.97' run_gate v99.98.97

echo "release tag gate: OK"

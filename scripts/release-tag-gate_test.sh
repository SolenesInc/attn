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
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/pulls/42/commits?'* ]]; then
  printf '%s\n' "$FAKE_CANDIDATE_SHA"
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *'/pulls'* ]]; then
  printf '42\trelease/v99.98.97\thttps://github.com/example/attn/pull/42\n'
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *"/git/commits/$FAKE_CANDIDATE_SHA"* ]]; then
  printf '%s\n' "$FAKE_CANDIDATE_TREE"
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *"/git/commits/$FAKE_MAIN_SHA"* ]]; then
  printf '%s\n' "$FAKE_MAIN_TREE"
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *'/actions/workflows/app-acceptance.yml/runs?'* ]]; then
  printf '2026-08-29T10:00:00Z\t43\tApp acceptance %s\tcompleted\tsuccess\t%s\n' "$FAKE_CANDIDATE_SHA" \
    'https://github.com/example/attn/actions/runs/43'
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *'/actions/workflows/ci.yml/runs?'* ]]; then
  if [[ "${FAKE_ACCEPTANCE_MODE:-success}" != missing ]]; then
    printf '2026-08-29T10:00:00Z\t42\t%s\tcompleted\tsuccess\t%s\n' "$FAKE_ACCEPTANCE_SHA" \
      'https://github.com/example/attn/actions/runs/42'
  fi
  exit 0
fi
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/actions/runs/43/jobs?'* ]]; then
  printf '%s\t%s\t%s\n' completed success \
    'https://github.com/example/attn/actions/runs/43/job/8'
  exit 0
fi
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/actions/runs/42/jobs?'* ]]; then
  printf '%s\t%s\t%s\n' "${FAKE_ACCEPTANCE_STATUS:-completed}" \
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
export FAKE_CANDIDATE_TREE=cccccccccccccccccccccccccccccccccccccccc
export FAKE_MAIN_TREE="$FAKE_CANDIDATE_TREE"

repo="$work/repo"
git clone -q "$root" "$repo"
git -C "$repo" config user.name 'Release Tag Gate Test'
git -C "$repo" config user.email 'release-tag-gate@example.com'
cp "$root/cmd/release-train/main.go" "$repo/cmd/release-train/main.go"
git -C "$repo" add cmd/release-train/main.go
git -C "$repo" commit -q -m 'test(release): use current release tool'
git -C "$repo" switch -q -C main
git -C "$repo" rm -q -- 'changelog.d/*.yaml'
git -C "$repo" commit -q -m 'release fixture baseline'
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
export FAKE_MAIN_SHA="$accepted_sha"
git -C "$repo" tag v99.98.97
git -C "$repo" update-ref refs/remotes/origin/main "$accepted_sha"
export FAKE_ACCEPTANCE_SHA="$accepted_sha"
export GITHUB_SHA="$accepted_sha"

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

export GITHUB_OUTPUT="$work/github-output"
run_gate v99.98.97 >"$work/success.out"
grep -Fq 'v99.98.97 is accepted' "$work/success.out"
grep -Fq "release_sha=$accepted_sha" "$GITHUB_OUTPUT"

export FAKE_MAIN_TREE=dddddddddddddddddddddddddddddddddddddddd
expect_failure 'differs from app-accepted candidate tree' run_gate v99.98.97
export FAKE_MAIN_TREE="$FAKE_CANDIDATE_TREE"

export FAKE_ACCEPTANCE_CONCLUSION=failure
expect_failure 'Acceptance is completed/failure' run_gate v99.98.97
export FAKE_ACCEPTANCE_CONCLUSION=success

printf '%s\n' 'repair after acceptance' >"$repo/repair.txt"
git -C "$repo" add repair.txt
git -C "$repo" commit -q -m 'fix(release): repair accepted main'
repaired_sha="$(git -C "$repo" rev-parse HEAD)"
git -C "$repo" update-ref refs/remotes/origin/main "$repaired_sha"
export GITHUB_SHA="$repaired_sha"
expect_failure 'not current main' run_gate v99.98.97

git -C "$repo" switch -q -c detached-release "$baseline_sha"
printf '%s\n' 'detached release' >"$repo/detached-release.txt"
git -C "$repo" add detached-release.txt
git -C "$repo" commit -q -m 'forge detached release'
git -C "$repo" tag v99.98.96
expect_failure 'trusted checkout is' run_gate v99.98.96
git -C "$repo" switch -q main
expect_failure 'not current main' run_gate v99.98.96

(cd "$repo" && go run ./cmd/release-train version set v99.98.96 >/dev/null)
git -C "$repo" add app
git -C "$repo" commit -q -m 'forge release versions'
forged_sha="$(git -C "$repo" rev-parse HEAD)"
git -C "$repo" tag -f v99.98.97 "$forged_sha" >/dev/null
git -C "$repo" update-ref refs/remotes/origin/main "$forged_sha"
export FAKE_ACCEPTANCE_SHA="$forged_sha"
export GITHUB_SHA="$accepted_sha"
git -C "$repo" switch -q --detach "$accepted_sha"
expect_failure 'main moved from dispatch SHA' run_gate v99.98.97
git -C "$repo" switch -q main
export GITHUB_SHA="$forged_sha"
expect_failure 'expected 99.98.97' run_gate v99.98.97

git -C "$repo" switch -q -C held-main "$accepted_sha"
(cd "$repo" && go run ./cmd/release-train manifest write \
  --version v99.98.97 --kind promotion --publication held \
  --source "$baseline_sha" --main "$baseline_sha" >/dev/null)
git -C "$repo" add .github/release-candidate.yml
git -C "$repo" commit -q -m 'chore(release): hold publication'
held_sha="$(git -C "$repo" rev-parse HEAD)"
git -C "$repo" tag v99.98.95
git -C "$repo" update-ref refs/remotes/origin/main "$held_sha"
export GITHUB_SHA="$held_sha"
export FAKE_ACCEPTANCE_SHA="$held_sha"
expect_failure 'publication is held' run_gate v99.98.95

validate_job="$(sed -n '/^  validate-tag:/,/^  tauri:/p' "$root/.github/workflows/release.yml")"
grep -Fq 'actions: read' <<<"$validate_job"

echo "release tag gate: OK"

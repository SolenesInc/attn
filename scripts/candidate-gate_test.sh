#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
gate="$root/scripts/candidate-gate.sh"
changelog_gate="$root/scripts/changelog-gate.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-candidate-gate-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"

if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/pulls?state=open&base=main&per_page=100'* ]]; then
  printf '%s' "${FAKE_CANDIDATES_PAGE_1:-}"
  printf '%s' "${FAKE_CANDIDATES_PAGE_2:-}"
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *'/actions/workflows/app-acceptance.yml/runs?'* ]]; then
  if [[ "${FAKE_APP_MODE:-success}" != missing ]] && \
    [[ "$*" =~ App\ acceptance\ ([0-9a-f]{40}) ]]; then
    printf '2026-08-29T10:00:00Z\t43\tApp acceptance %s\tcompleted\t%s\t%s\n' "${BASH_REMATCH[1]}" \
      "${FAKE_APP_CONCLUSION:-success}" \
      'https://github.com/example/attn/actions/runs/43'
  fi
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *'/actions/workflows/ci.yml/runs?'* ]]; then
  if [[ "$*" == *'event=pull_request'* ]]; then
    if [[ "${FAKE_CI_APP_MODE:-success}" != missing ]] && \
      [[ "$*" =~ head_sha=([0-9a-f]{40}) ]]; then
      printf '2026-08-29T10:00:00Z\t44\t%s\tcompleted\t%s\t%s\n' "${BASH_REMATCH[1]}" \
        "${FAKE_CI_APP_CONCLUSION:-success}" \
        'https://github.com/example/attn/actions/runs/44'
    fi
    exit 0
  fi
fi
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/actions/runs/44/jobs?'* ]]; then
  printf 'completed\t%s\t%s\n' "${FAKE_CI_APP_CONCLUSION:-success}" \
    'https://github.com/example/attn/actions/runs/44/job/9'
  exit 0
fi
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/actions/runs/43/jobs?'* ]]; then
  printf 'completed\t%s\t%s\n' "${FAKE_APP_CONCLUSION:-success}" \
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
export FAKE_GH_LOG="$work/gh.log"
export FAKE_CANDIDATES_PAGE_1=
export FAKE_CANDIDATES_PAGE_2=
export FAKE_APP_MODE=success
export FAKE_APP_CONCLUSION=success
export FAKE_CI_APP_MODE=success
export FAKE_CI_APP_CONCLUSION=success

repo="$work/repo"
git clone -q "$root" "$repo"
git -C "$repo" config user.name 'Candidate Gate Test'
git -C "$repo" config user.email 'candidate-gate@example.com'
git -C "$repo" switch -q -C main
cp "$root/cmd/release-train/main.go" "$repo/cmd/release-train/main.go"
git -C "$repo" add cmd/release-train/main.go
git -C "$repo" rm -q --ignore-unmatch -- 'changelog.d/*.yaml'
printf '%s\n' 'kind: internal' 'area: release' 'change: release fixture' \
  >"$repo/changelog.d/release-fixture.yaml"
git -C "$repo" add changelog.d
git -C "$repo" commit -q --allow-empty -m 'release baseline'
main_sha="$(git -C "$repo" rev-parse HEAD)"
git -C "$repo" update-ref refs/remotes/origin/main "$main_sha"

expect_failure() {
  local expected="$1"
  shift
  if "$@" >"$work/failure.out" 2>&1; then
    echo "expected candidate gate failure: $*" >&2
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

git -C "$repo" switch -q -c release/v99.98.97 "$main_sha"
(cd "$repo" && go run ./cmd/release-train version set v99.98.97)
(cd "$repo" && go run ./cmd/release-train manifest write --version v99.98.97 --main "$main_sha")
git -C "$repo" rm -q changelog.d/release-fixture.yaml
git -C "$repo" add .github/release-candidate.yml app
git -C "$repo" commit -q -m 'chore(release): prepare v99.98.97'
export FAKE_CANDIDATES_PAGE_1=$'release/v99.98.97\thttps://github.com/example/attn/pull/1\n'
run_gate origin/main HEAD release/v99.98.97 >"$work/release.out"
grep -Fq 'CI App acceptance is green' "$work/release.out"
(cd "$repo" && "$changelog_gate" main release/v99.98.97) >"$work/release-changelog.out"
grep -Fq 'validated release candidate' "$work/release-changelog.out"

expect_failure 'expected release/v99.98.97' run_gate origin/main HEAD release/v99.98.96

later_sha="$(git -C "$repo" commit-tree -p "$main_sha" -m 'feat: later work on main' "$main_sha^{tree}")"
git -C "$repo" update-ref refs/remotes/origin/main "$later_sha"
run_gate origin/main HEAD release/v99.98.97 >"$work/main-moved.out"
grep -Fq 'is a valid release candidate' "$work/main-moved.out"
git -C "$repo" update-ref refs/remotes/origin/main "$main_sha"

export FAKE_CI_APP_MODE=missing
run_gate origin/main HEAD release/v99.98.97 >"$work/manual-override.out"
grep -Fq 'manual App acceptance override is green' "$work/manual-override.out"

export FAKE_APP_MODE=missing
expect_failure 'has no app-acceptance.yml workflow_dispatch run' \
  run_gate origin/main HEAD release/v99.98.97
export FAKE_APP_MODE=success
export FAKE_CI_APP_MODE=success

export FAKE_CANDIDATES_PAGE_2=$'release/v99.0.0\thttps://github.com/example/attn/pull/102\n'
expect_failure 'another release candidate is open' \
  run_gate origin/main HEAD release/v99.98.97
grep -Fq 'api --paginate --method GET repos/{owner}/{repo}/pulls?state=open&base=main&per_page=100' "$FAKE_GH_LOG"
export FAKE_CANDIDATES_PAGE_1=
export FAKE_CANDIDATES_PAGE_2=

printf '%s\n' 'not release metadata' >"$repo/late-product-edit.txt"
git -C "$repo" add late-product-edit.txt
git -C "$repo" commit -q -m 'fix(release): mutate the candidate'
expect_failure 'candidate changes non-release file' \
  run_gate origin/main HEAD release/v99.98.97

changelog_job="$(sed -n '/^  changelog:/,/^  script-tests:/p' "$root/.github/workflows/ci.yml")"
grep -Fq 'actions: read' <<<"$changelog_job"

echo "candidate gate: OK"

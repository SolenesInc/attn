#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
release_script="$root/scripts/release.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-release-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"

case "$1 $2" in
  "auth status")
    exit 0
    ;;
  "repo view")
    printf '%s\n' $'example/attn\thttps://github.com/example/attn'
    ;;
  "api --paginate")
    if [[ "$*" != *'/pulls?state=open&base=main&per_page=100'* ]]; then
      echo "unexpected paginated API command: $*" >&2
      exit 2
    fi
    printf '%s' "${FAKE_ACTIVE_CANDIDATE:-}"
    ;;
  "api --method")
    if [[ "${FAKE_ACCEPTANCE_MODE:-success}" != "missing" ]]; then
      printf '%s\t%s\t%s\t%s\n' "$FAKE_ACCEPTANCE_SHA" \
        "${FAKE_ACCEPTANCE_STATUS:-completed}" \
        "${FAKE_ACCEPTANCE_CONCLUSION:-success}" \
        'https://github.com/example/attn/actions/runs/42/job/7'
    fi
    ;;
  "pr create")
    body_file=""
    while [[ $# -gt 0 ]]; do
      if [[ "$1" == "--body-file" ]]; then
        body_file="$2"
        break
      fi
      shift
    done
    cp "$body_file" "$FAKE_PR_BODY"
    printf '%s\n' 'https://github.com/example/attn/pull/1'
    ;;
  *)
    echo "unexpected gh command: $*" >&2
    exit 2
    ;;
esac
EOF

cat >"$work/bin/claude" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ -n "${FAKE_CLAUDE_FAIL:-}" ]]; then
  echo 'changelog writer failed' >&2
  exit 1
fi
printf '%s\n' "$#" >"$FAKE_CLAUDE_ARGC"
cat >"$FAKE_CLAUDE_INPUT"
if [[ -n "${FAKE_CLAUDE_PREAMBLE:-}" ]]; then
  printf '%s\n' "$FAKE_CLAUDE_PREAMBLE"
fi
printf '## [%s]\n\n### Added\n- **Fixture release.** Candidate facts compiled.\n' \
  "$(date +%Y-%m-%d)"
EOF

for command in pnpm cargo; do
  cat >"$work/bin/$command" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
exit 0
EOF
done
chmod +x "$work/bin/gh" "$work/bin/claude" "$work/bin/pnpm" "$work/bin/cargo"

setup_fixture() {
  local name="$1"
  fixture_origin="$work/${name}-origin.git"
  fixture_repo="$work/${name}-repo"

  git init -q --bare "$fixture_origin"
  git --git-dir="$fixture_origin" config receive.shallowUpdate true
  git clone -q "$root" "$fixture_repo"
  git -C "$fixture_repo" config user.name 'Release Test'
  git -C "$fixture_repo" config user.email 'release@example.com'
  git -C "$fixture_repo" switch -q -C main
  cp "$root/scripts/compile-changelog.sh" "$fixture_repo/scripts/compile-changelog.sh"
  cp "$root/cmd/release-train/main.go" "$fixture_repo/cmd/release-train/main.go"
  if ! git -C "$fixture_repo" diff --quiet -- scripts/compile-changelog.sh cmd/release-train/main.go; then
    git -C "$fixture_repo" add scripts/compile-changelog.sh cmd/release-train/main.go
    git -C "$fixture_repo" commit -q -m 'test(release): use current release tools'
  fi
  printf '%s\n' 'kind: added' 'area: release' 'change: candidate fixture' \
    >"$fixture_repo/changelog.d/candidate-fixture.yaml"
  git -C "$fixture_repo" add changelog.d/candidate-fixture.yaml
  git -C "$fixture_repo" commit -q -m 'feat(release): add candidate fixture'
  git -C "$fixture_repo" remote set-url origin "$fixture_origin"
  git -C "$fixture_repo" push -q -u origin main
}

export PATH="$work/bin:$PATH"
export GOCACHE="$work/go-cache"
export FAKE_GH_LOG="$work/gh.log"
export FAKE_PR_BODY="$work/pr-body.md"
export FAKE_CLAUDE_ARGC="$work/claude-argc.txt"
export FAKE_CLAUDE_INPUT="$work/claude-input.txt"
export FAKE_ACCEPTANCE_MODE=success
export FAKE_ACCEPTANCE_STATUS=completed
export FAKE_ACCEPTANCE_CONCLUSION=success
export FAKE_ACTIVE_CANDIDATE=

run_release() (
  cd "$fixture_repo"
  acceptance_sha="${FAKE_ACCEPTANCE_SHA_OVERRIDE:-$(git rev-parse origin/main)}"
  FAKE_ACCEPTANCE_SHA="$acceptance_sha" "$release_script" "$@"
)

expect_failure() {
  local expected="$1"
  shift
  if "$@" >"$work/failure.out" 2>&1; then
    echo "expected release preparation failure: $*" >&2
    exit 1
  fi
  if ! grep -Fq -- "$expected" "$work/failure.out"; then
    echo "failure did not contain '$expected':" >&2
    cat "$work/failure.out" >&2
    exit 1
  fi
}

setup_fixture default
default_repo="$fixture_repo"
default_origin="$fixture_origin"

git -C "$fixture_repo" switch -q -c feature
expect_failure 'run from the local main branch' run_release v99.98.97
git -C "$fixture_repo" switch -q main

printf '%s\n' dirty >"$fixture_repo/untracked.txt"
expect_failure 'working tree must be clean' run_release v99.98.97
rm "$fixture_repo/untracked.txt"

export FAKE_ACCEPTANCE_MODE=missing
expect_failure 'has no Acceptance check' run_release v99.98.97
export FAKE_ACCEPTANCE_MODE=success

export FAKE_ACCEPTANCE_SHA_OVERRIDE=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
expect_failure 'Acceptance belongs to' run_release v99.98.97
export FAKE_ACCEPTANCE_SHA_OVERRIDE=

export FAKE_ACCEPTANCE_CONCLUSION=failure
expect_failure 'Acceptance is completed/failure' run_release v99.98.97
export FAKE_ACCEPTANCE_CONCLUSION=success

export FAKE_ACTIVE_CANDIDATE=$'release/v99.0.0\thttps://github.com/example/attn/pull/109'
expect_failure 'another release candidate is open' run_release v99.98.97
grep -Fq 'api --paginate --method GET repos/{owner}/{repo}/pulls?state=open&base=main&per_page=100' "$FAKE_GH_LOG"
export FAKE_ACTIVE_CANDIDATE=

git -C "$fixture_repo" tag v99.98.96
git -C "$fixture_repo" push -q origin refs/tags/v99.98.96
expect_failure 'tag v99.98.96 already exists' run_release v99.98.96
git --git-dir="$fixture_origin" update-ref -d refs/tags/v99.98.96
git -C "$fixture_repo" tag -d v99.98.96 >/dev/null

git clone -q --branch main "$fixture_origin" "$work/updater"
git -C "$work/updater" config user.name 'Release Test'
git -C "$work/updater" config user.email 'release@example.com'
printf '%s\n' later >"$work/updater/later.txt"
git -C "$work/updater" add later.txt
git -C "$work/updater" commit -q -m 'fix(release): move main'
git -C "$work/updater" push -q origin main
expect_failure 'local main is not origin/main' run_release v99.98.97
git -C "$fixture_repo" merge -q --ff-only origin/main

main_sha="$(git -C "$fixture_repo" rev-parse origin/main)"

# Historical tag drift must not block an unrelated candidate. Release
# preparation reads the requested remote tag and leaves local history alone.
git --git-dir="$fixture_origin" update-ref refs/tags/v90.0.0 "$main_sha~1"
git -C "$fixture_repo" tag -f v90.0.0 "$main_sha"
git -C "$fixture_repo" tag -f v99.98.97 "$main_sha"

run_release v99.98.97 --dry-run >"$work/dry-run.out"
grep -Fq "accepted main: $main_sha" "$work/dry-run.out"
grep -q 'would not merge, tag, or dispatch a release' "$work/dry-run.out"

export FAKE_CLAUDE_PREAMBLE='Here is the compiled changelog section.'
run_release v99.98.97 >"$work/success.out"
unset FAKE_CLAUDE_PREAMBLE
candidate_ref='refs/heads/release/v99.98.97'
candidate_sha="$(git --git-dir="$fixture_origin" rev-parse "$candidate_ref")"
manifest="$(git --git-dir="$fixture_origin" show "$candidate_ref:.github/release-candidate.yml")"
[[ "$manifest" == "$(printf 'version: 99.98.97\nmain_sha: %s' "$main_sha")" ]]
[[ "$(git --git-dir="$fixture_origin" rev-parse "$candidate_ref~1")" == "$main_sha" ]]
if git --git-dir="$fixture_origin" show "$candidate_ref:CHANGELOG.md" | grep -Fq 'Here is the compiled changelog section.'; then
  echo "candidate retained changelog writer preamble" >&2
  exit 1
fi
if git --git-dir="$fixture_origin" ls-tree -r --name-only "$candidate_ref" \
  -- changelog.d | grep -q '\.yaml$'; then
  echo "candidate retained changelog fragments" >&2
  exit 1
fi

grep -q 'pr create --base main --head release/v99.98.97' "$FAKE_GH_LOG"
if grep -Fq -- '--draft' "$FAKE_GH_LOG"; then
  echo "candidate opened as a draft" >&2
  exit 1
fi
for value in "$main_sha" "$candidate_sha" \
  'https://github.com/example/attn/actions/runs/42/job/7' \
  'changelog fragment' '--ref main' 'candidate_sha='; do
  grep -Fq -- "$value" "$FAKE_PR_BODY"
done
if grep -Fq 'candidate-fixture.yaml' "$FAKE_PR_BODY"; then
  echo "candidate PR body repeated raw changelog inputs" >&2
  exit 1
fi
grep -Fq 'candidate-fixture.yaml' "$FAKE_CLAUDE_INPUT"
[[ "$(<"$FAKE_CLAUDE_ARGC")" == "2" ]]
if grep -Eq '(^| )(pr merge|workflow run release)' "$FAKE_GH_LOG"; then
  echo "candidate preparation crossed a merge or release boundary" >&2
  exit 1
fi
[[ "$(git -C "$fixture_repo" branch --show-current)" == main ]]
[[ -z "$(git -C "$fixture_repo" status --porcelain)" ]]
[[ "$(git -C "$fixture_repo" worktree list | wc -l | tr -d ' ')" == 1 ]]
if git -C "$fixture_repo" show-ref --verify --quiet refs/heads/release/v99.98.97; then
  echo "candidate preparation left a local release branch behind" >&2
  exit 1
fi

git --git-dir="$fixture_origin" update-ref -d "$candidate_ref"
run_release v99.98.97 >"$work/retry.out"
git --git-dir="$fixture_origin" rev-parse --verify --quiet "$candidate_ref" >/dev/null

git --git-dir="$fixture_origin" update-ref -d "$candidate_ref"
export FAKE_CLAUDE_FAIL=1
expect_failure 'changelog writer failed' run_release v99.98.97
unset FAKE_CLAUDE_FAIL
[[ "$(git -C "$fixture_repo" branch --show-current)" == main ]]
[[ -z "$(git -C "$fixture_repo" status --porcelain)" ]]
[[ "$(git -C "$fixture_repo" worktree list | wc -l | tr -d ' ')" == 1 ]]

echo "release preparation: OK"

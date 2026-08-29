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
printf '%s\n' "$#" >"$FAKE_CLAUDE_ARGC"
cat >"$FAKE_CLAUDE_INPUT"
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
  local diverge_main="${2:-0}"
  fixture_origin="$work/${name}-origin.git"
  fixture_repo="$work/${name}-repo"

  git init -q --bare "$fixture_origin"
  git --git-dir="$fixture_origin" config receive.shallowUpdate true
  git clone -q "$root" "$fixture_repo"
  git -C "$fixture_repo" config user.name 'Release Test'
  git -C "$fixture_repo" config user.email 'release@example.com'
  cp "$root/scripts/compile-changelog.sh" "$fixture_repo/scripts/compile-changelog.sh"
  cp "$root/cmd/release-train/main.go" "$fixture_repo/cmd/release-train/main.go"
  if ! git -C "$fixture_repo" diff --quiet -- scripts/compile-changelog.sh cmd/release-train/main.go; then
    git -C "$fixture_repo" add scripts/compile-changelog.sh cmd/release-train/main.go
    git -C "$fixture_repo" commit -q -m 'test(release): use current release tools'
  fi
  git -C "$fixture_repo" remote set-url origin "$fixture_origin"
  git -C "$fixture_repo" switch -q -C main
  git -C "$fixture_repo" push -q -u origin main

  git -C "$fixture_repo" switch -q -C next
  printf '%s\n' 'kind: added' 'area: release' 'change: candidate fixture' \
    >"$fixture_repo/changelog.d/candidate-fixture.yaml"
  git -C "$fixture_repo" add changelog.d/candidate-fixture.yaml
  git -C "$fixture_repo" commit -q -m 'feat(release): add candidate fixture'
  git -C "$fixture_repo" push -q -u origin next

  if [[ "$diverge_main" -eq 1 ]]; then
    git -C "$fixture_repo" switch -q main
    printf '%s\n' 'main moved independently' >"$fixture_repo/main-only.txt"
    git -C "$fixture_repo" add main-only.txt
    git -C "$fixture_repo" commit -q -m 'fix(release): move main independently'
    git -C "$fixture_repo" push -q origin main
    git -C "$fixture_repo" switch -q next
  fi
}

setup_hotfix_fixture() {
  fixture_origin="$work/hotfix-origin.git"
  fixture_repo="$work/hotfix-repo"

  git init -q --bare "$fixture_origin"
  git --git-dir="$fixture_origin" config receive.shallowUpdate true
  git clone -q "$root" "$fixture_repo"
  git -C "$fixture_repo" config user.name 'Release Test'
  git -C "$fixture_repo" config user.email 'release@example.com'
  cp "$root/scripts/compile-changelog.sh" "$fixture_repo/scripts/compile-changelog.sh"
  cp "$root/cmd/release-train/main.go" "$fixture_repo/cmd/release-train/main.go"
  if ! git -C "$fixture_repo" diff --quiet -- scripts/compile-changelog.sh cmd/release-train/main.go; then
    git -C "$fixture_repo" add scripts/compile-changelog.sh cmd/release-train/main.go
    git -C "$fixture_repo" commit -q -m 'test(release): use current release tools'
  fi
  git -C "$fixture_repo" remote set-url origin "$fixture_origin"
  git -C "$fixture_repo" switch -q -C main
  git -C "$fixture_repo" rm -q -- 'changelog.d/*.yaml'

  local previous_source
  previous_source="$(git -C "$fixture_repo" rev-parse HEAD)"
  cat >"$fixture_repo/.github/release-candidate.yml" <<EOF
version: 98.0.0
kind: promotion
source_sha: ${previous_source}
main_sha: ${previous_source}
EOF
  git -C "$fixture_repo" add changelog.d .github/release-candidate.yml
  git -C "$fixture_repo" commit -q -m 'chore(release): establish released main'
  released_main_sha="$(git -C "$fixture_repo" rev-parse HEAD)"
  git -C "$fixture_repo" tag v98.0.0
  git -C "$fixture_repo" push -q -u origin main
  git --git-dir="$fixture_origin" update-ref refs/tags/v98.0.0 "$released_main_sha"
  git --git-dir="$fixture_origin" symbolic-ref HEAD refs/heads/main

  git -C "$fixture_repo" switch -q -c hotfix/startup-crash
  printf '%s\n' 'startup repair' >"$fixture_repo/hotfix.txt"
  printf '%s\n' 'kind: fixed' 'area: app' 'change: repair startup crash' \
    >"$fixture_repo/changelog.d/hotfix.yaml"
  git -C "$fixture_repo" add hotfix.txt changelog.d/hotfix.yaml
  git -C "$fixture_repo" commit -q -m 'fix(app): repair startup crash'
  hotfix_source_sha="$(git -C "$fixture_repo" rev-parse HEAD)"
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
  acceptance_sha="${FAKE_ACCEPTANCE_SHA_OVERRIDE:-$(git rev-parse origin/next)}"
  FAKE_ACCEPTANCE_SHA="$acceptance_sha" "$release_script" "$@"
)

run_hotfix() (
  cd "$fixture_repo"
  FAKE_ACCEPTANCE_SHA=unused "$release_script" "$@" --hotfix
)

expect_failure() {
  local expected="$1"
  shift
  if "$@" >"$work/failure.out" 2>&1; then
    echo "expected release preparation failure: $*" >&2
    exit 1
  fi
  if ! grep -Fq "$expected" "$work/failure.out"; then
    echo "failure did not contain '$expected':" >&2
    cat "$work/failure.out" >&2
    exit 1
  fi
}

setup_fixture default
default_repo="$fixture_repo"
default_origin="$fixture_origin"

expect_failure 'run --hotfix from a hotfix/* branch' run_hotfix v99.98.98
git -C "$fixture_repo" switch -q main
expect_failure 'run a promotion from the local next branch' run_release v99.98.97
git -C "$fixture_repo" switch -q next

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
expect_failure 'tag v99.98.96 already exists' run_release v99.98.96
git -C "$fixture_repo" tag -d v99.98.96 >/dev/null

git clone -q --branch next "$fixture_origin" "$work/updater"
git -C "$work/updater" config user.name 'Release Test'
git -C "$work/updater" config user.email 'release@example.com'
printf '%s\n' later >"$work/updater/later.txt"
git -C "$work/updater" add later.txt
git -C "$work/updater" commit -q -m 'fix(release): move next'
git -C "$work/updater" push -q origin next
expect_failure 'local next is stale' run_release v99.98.97
git -C "$fixture_repo" merge -q --ff-only origin/next

setup_fixture diverged 1
expect_failure 'current main is not an ancestor' run_release v99.98.97

fixture_repo="$default_repo"
fixture_origin="$default_origin"
source_sha="$(git -C "$fixture_repo" rev-parse origin/next)"
main_sha="$(git -C "$fixture_repo" rev-parse origin/main)"

run_release v99.98.97 --dry-run >"$work/dry-run.out"
grep -Fq "$source_sha" "$work/dry-run.out"
grep -q 'would not merge, tag, or dispatch a release' "$work/dry-run.out"

run_release v99.98.97 >"$work/success.out"
candidate_ref='refs/heads/release/v99.98.97'
candidate_sha="$(git --git-dir="$fixture_origin" rev-parse "$candidate_ref")"
manifest="$(git --git-dir="$fixture_origin" show "$candidate_ref:.github/release-candidate.yml")"
grep -Fq "source_sha: ${source_sha}" <<<"$manifest"
grep -Fq "main_sha: ${main_sha}" <<<"$manifest"
if git --git-dir="$fixture_origin" ls-tree -r --name-only "$candidate_ref" \
  -- changelog.d | grep -q '\.yaml$'; then
  echo "candidate retained changelog fragments" >&2
  exit 1
fi

grep -q 'pr create --draft --base main --head release/v99.98.97' "$FAKE_GH_LOG"
for value in "$source_sha" "$main_sha" "$candidate_sha" \
  'https://github.com/example/attn/actions/runs/42/job/7' \
  'candidate-fixture.yaml' '--ref main' 'candidate_sha='; do
  grep -Fq -- "$value" "$FAKE_PR_BODY"
done
grep -Fq 'candidate-fixture.yaml' "$FAKE_CLAUDE_INPUT"
[[ "$(<"$FAKE_CLAUDE_ARGC")" == "2" ]]
if grep -Eq '(^| )(pr merge|workflow run release)' "$FAKE_GH_LOG"; then
  echo "candidate preparation crossed a merge or release boundary" >&2
  exit 1
fi

setup_hotfix_fixture
: >"$FAKE_GH_LOG"

git clone -q --branch main "$fixture_origin" "$work/hotfix-main-updater"
git -C "$work/hotfix-main-updater" config user.name 'Release Test'
git -C "$work/hotfix-main-updater" config user.email 'release@example.com'
printf '%s\n' 'main moved' >"$work/hotfix-main-updater/main-moved.txt"
git -C "$work/hotfix-main-updater" add main-moved.txt
git -C "$work/hotfix-main-updater" commit -q -m 'fix(release): move released main'
git -C "$work/hotfix-main-updater" push -q origin main
expect_failure 'hotfix does not contain current main' run_hotfix v99.98.98
git --git-dir="$fixture_origin" update-ref refs/heads/main "$released_main_sha"

run_hotfix v99.98.98 --dry-run >"$work/hotfix-dry-run.out"
for value in 'kind: hotfix' "hotfix source: ${hotfix_source_sha}" \
  "main baseline: ${released_main_sha}" 'branch: hotfix/startup-crash' \
  'source gate: final PR gate and App acceptance'; do
  grep -Fq "$value" "$work/hotfix-dry-run.out"
done
if grep -q 'api --method GET' "$FAKE_GH_LOG"; then
  echo "hotfix dry run queried source Acceptance" >&2
  exit 1
fi

run_hotfix v99.98.98 >"$work/hotfix-success.out"
hotfix_ref='refs/heads/hotfix/startup-crash'
hotfix_candidate_sha="$(git --git-dir="$fixture_origin" rev-parse "$hotfix_ref")"
hotfix_manifest="$(git --git-dir="$fixture_origin" show "$hotfix_ref:.github/release-candidate.yml")"
for value in 'version: 99.98.98' 'kind: hotfix' \
  "source_sha: ${hotfix_source_sha}" "main_sha: ${released_main_sha}"; do
  grep -Fq "$value" <<<"$hotfix_manifest"
done
if git --git-dir="$fixture_origin" ls-tree -r --name-only "$hotfix_ref" \
  -- changelog.d | grep -q '\.yaml$'; then
  echo "hotfix candidate retained changelog fragments" >&2
  exit 1
fi
if [[ "$(git --git-dir="$fixture_origin" rev-list --count \
  "${hotfix_source_sha}..${hotfix_candidate_sha}")" -ne 1 ]]; then
  echo "hotfix preparation added more than one commit" >&2
  exit 1
fi

grep -Fq 'pr create --draft --base main --head hotfix/startup-crash --title fix(app): repair startup crash' \
  "$FAKE_GH_LOG"
for value in "$hotfix_source_sha" "$released_main_sha" "$hotfix_candidate_sha" \
  'Final `PR gate` and `App acceptance`' '--ref main' \
  'candidate_sha=' 'hotfix.yaml'; do
  grep -Fq -- "$value" "$FAKE_PR_BODY"
done
if grep -q 'api --method GET' "$FAKE_GH_LOG"; then
  echo "hotfix preparation queried source Acceptance" >&2
  exit 1
fi
if grep -Eq '(^| )(pr merge|workflow run release)' "$FAKE_GH_LOG"; then
  echo "hotfix preparation crossed a merge or release boundary" >&2
  exit 1
fi

echo "release preparation: OK"

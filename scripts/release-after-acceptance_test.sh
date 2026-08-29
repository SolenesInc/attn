#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$root/scripts/release-after-acceptance.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-release-after-acceptance-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"

if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/actions/runs/42/jobs?'* ]]; then
  printf '%s\t%s\t%s\n' completed "$FAKE_ACCEPTANCE_CONCLUSION" \
    'https://github.com/example/attn/actions/runs/42/job/7'
  exit 0
fi
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/pulls/42/commits?'* ]]; then
  printf '%s\n' "$FAKE_CANDIDATE_SHA"
  exit 0
fi
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/actions/workflows/release.yml/runs?'* ]]; then
  if [[ "${FAKE_RELEASE_RUN_PAGE_2:-0}" == 1 ]] || grep -q '^workflow run release.yml ' "$FAKE_GH_LOG"; then
    printf '%s\n' 99
  fi
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *'/pulls'* ]]; then
  if [[ "${FAKE_CANDIDATE_MODE:-success}" != missing ]]; then
    printf '42\trelease/v99.98.97\thttps://github.com/example/attn/pull/42\n'
  fi
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *"/git/commits/$FAKE_CANDIDATE_SHA"* ]]; then
  printf '%s\n' "$FAKE_CANDIDATE_TREE"
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *'/git/commits/'* ]]; then
  printf '%s\n' "$FAKE_MAIN_TREE"
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *'/actions/workflows/app-acceptance.yml/runs?'* ]]; then
  if [[ "${FAKE_APP_MODE:-success}" != missing ]]; then
    printf '43\t%s\tcompleted\tsuccess\t%s\n' "$FAKE_CANDIDATE_SHA" \
      'https://github.com/example/attn/actions/runs/43'
  fi
  exit 0
fi
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/actions/runs/43/jobs?'* ]]; then
  printf '%s\t%s\t%s\n' completed "${FAKE_APP_CONCLUSION:-success}" \
    'https://github.com/example/attn/actions/runs/43/job/8'
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *'/commits/v'* ]]; then
  printf '%s\n' "$FAKE_TAG_SHA"
  exit 0
fi
if [[ "$1 $2 $3" == "workflow run release.yml" ]]; then
  if [[ "$*" != *'--ref main -f tag='* ]]; then
    echo "release workflow must be loaded from protected main: $*" >&2
    exit 1
  fi
  exit 0
fi

echo "unexpected gh command: $*" >&2
exit 2
EOF
chmod +x "$work/bin/gh"

real_git="$(command -v git)"
cat >"$work/bin/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == 'ls-remote --exit-code origin refs/heads/main' ]] && \
  [[ -n "${FAKE_MOVE_MAIN_AT_CHECK:-}" ]]; then
  count=0
  if [[ -f "$FAKE_MAIN_CHECK_COUNT" ]]; then
    count="$(cat "$FAKE_MAIN_CHECK_COUNT")"
  fi
  count=$((count + 1))
  printf '%s\n' "$count" >"$FAKE_MAIN_CHECK_COUNT"
  if [[ "$count" -eq "$FAKE_MOVE_MAIN_AT_CHECK" ]]; then
    printf '%s\trefs/heads/main\n' "$FAKE_MOVED_MAIN_SHA"
    exit 0
  fi
fi
exec "$REAL_GIT" "$@"
EOF
chmod +x "$work/bin/git"

export PATH="$work/bin:$PATH"
export REAL_GIT="$real_git"
export GITHUB_REPOSITORY=example/attn
export GOCACHE="$work/go-cache"
export FAKE_GH_LOG="$work/gh.log"
export FAKE_ACCEPTANCE_CONCLUSION=success
export FAKE_TAG_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
export FAKE_CANDIDATE_SHA=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
export FAKE_CANDIDATE_TREE=dddddddddddddddddddddddddddddddddddddddd
export FAKE_MAIN_TREE="$FAKE_CANDIDATE_TREE"
export FAKE_CANDIDATE_MODE=success
export FAKE_APP_MODE=success
export FAKE_APP_CONCLUSION=success
export FAKE_MAIN_CHECK_COUNT="$work/main-check-count"
export FAKE_MOVED_MAIN_SHA=cccccccccccccccccccccccccccccccccccccccc
export FAKE_MOVE_MAIN_AT_CHECK=
export FAKE_RELEASE_RUN_PAGE_2=0

setup_fixture() {
  local name="$1"
  fixture_origin="$work/$name-origin.git"
  fixture_repo="$work/$name-repo"
  : >"$FAKE_GH_LOG"
  : >"$FAKE_MAIN_CHECK_COUNT"

  export FAKE_CANDIDATE_MODE=success
  export FAKE_APP_MODE=success
  export FAKE_APP_CONCLUSION=success
  export FAKE_MAIN_TREE="$FAKE_CANDIDATE_TREE"
  export FAKE_RELEASE_RUN_PAGE_2=0

	  git init -q --bare "$fixture_origin"
	  git --git-dir="$fixture_origin" config receive.shallowUpdate true
	  git clone -q --depth 1 "file://$root" "$fixture_repo"
	  git -C "$fixture_repo" config user.name 'Release Test'
	  git -C "$fixture_repo" config user.email 'release@example.com'
	  git -C "$fixture_repo" switch -q -C main
	  find "$fixture_repo/changelog.d" -type f -name '*.yaml' -delete
	  git -C "$fixture_repo" add -A changelog.d
	  git -C "$fixture_repo" commit -q -m 'release fixture baseline'
	  baseline_sha="$(git -C "$fixture_repo" rev-parse HEAD)"

	  (cd "$fixture_repo" && go run ./cmd/release-train version set v99.98.97 >/dev/null)
	  version=99.98.97
  mkdir -p "$fixture_repo/.github"
  cat >"$fixture_repo/.github/release-candidate.yml" <<EOF
version: $version
kind: promotion
source_sha: $baseline_sha
main_sha: $baseline_sha
EOF
  git -C "$fixture_repo" add -A
  git -C "$fixture_repo" commit -q -m 'release: accepted main fixture'
  candidate_sha="$(git -C "$fixture_repo" rev-parse HEAD)"
  candidate_tag="v$version"
	  git -C "$fixture_repo" remote set-url origin "$fixture_origin"
	  git -C "$fixture_repo" push -q -u origin main
	  git --git-dir="$fixture_origin" symbolic-ref HEAD refs/heads/main
}

run_release_after_acceptance() (
  cd "$fixture_repo"
  "$script" "$(git rev-parse HEAD)" 42 \
    'https://github.com/example/attn/actions/runs/42'
)

expect_failure() {
  local expected="$1"
  shift
  if "$@" >"$work/failure.out" 2>&1; then
    echo "expected release-after-acceptance failure: $*" >&2
    exit 1
  fi
  if ! grep -Fq "$expected" "$work/failure.out"; then
    echo "failure did not contain '$expected':" >&2
    cat "$work/failure.out" >&2
    exit 1
  fi
}

setup_fixture red
export FAKE_ACCEPTANCE_CONCLUSION=failure
run_release_after_acceptance >"$work/red.out"
grep -q 'main stays untagged' "$work/red.out"
if git --git-dir="$fixture_origin" show-ref --verify --quiet "refs/tags/$candidate_tag"; then
  echo "red Acceptance created a tag" >&2
  exit 1
fi

setup_fixture stale
export FAKE_ACCEPTANCE_CONCLUSION=success
git clone -q "$fixture_origin" "$work/stale-updater"
git -C "$work/stale-updater" config user.name 'Release Test'
git -C "$work/stale-updater" config user.email 'release@example.com'
printf '%s\n' 'main moved' >"$work/stale-updater/moved.txt"
git -C "$work/stale-updater" add moved.txt
git -C "$work/stale-updater" commit -q -m 'fix(release): move main'
git -C "$work/stale-updater" push -q origin main
run_release_after_acceptance >"$work/stale.out"
grep -q 'ignoring stale result' "$work/stale.out"
if git --git-dir="$fixture_origin" show-ref --verify --quiet "refs/tags/$candidate_tag"; then
  echo "stale Acceptance created a tag" >&2
  exit 1
fi

setup_fixture missing-app
export FAKE_APP_MODE=missing
expect_failure 'has no app-acceptance.yml workflow_dispatch run' run_release_after_acceptance
if git --git-dir="$fixture_origin" show-ref --verify --quiet "refs/tags/$candidate_tag"; then
  echo "missing App acceptance created a tag" >&2
  exit 1
fi

setup_fixture moved-before-tag
export FAKE_MOVE_MAIN_AT_CHECK=2
run_release_after_acceptance >"$work/moved-before-tag.out"
grep -q 'before tagging; leaving' "$work/moved-before-tag.out"
if git --git-dir="$fixture_origin" show-ref --verify --quiet "refs/tags/$candidate_tag"; then
  echo "main race created a stale release tag" >&2
  exit 1
fi
export FAKE_MOVE_MAIN_AT_CHECK=

setup_fixture red-app
export FAKE_APP_CONCLUSION=failure
expect_failure 'App acceptance is completed/failure' run_release_after_acceptance
if git --git-dir="$fixture_origin" show-ref --verify --quiet "refs/tags/$candidate_tag"; then
  echo "red App acceptance created a tag" >&2
  exit 1
fi

setup_fixture moved-base-tree
export FAKE_MAIN_TREE=eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
expect_failure 'differs from app-accepted candidate tree' run_release_after_acceptance
if git --git-dir="$fixture_origin" show-ref --verify --quiet "refs/tags/$candidate_tag"; then
  echo "moved candidate baseline created a tag" >&2
  exit 1
fi

setup_fixture success
export FAKE_ACCEPTANCE_CONCLUSION=success
run_release_after_acceptance >"$work/success.out"
tag_sha="$(git --git-dir="$fixture_origin" rev-parse "refs/tags/$candidate_tag")"
[[ "$tag_sha" == "$candidate_sha" ]]
grep -q "workflow run release.yml --repo example/attn --ref main -f tag=$candidate_tag" "$FAKE_GH_LOG"

export FAKE_TAG_SHA="$candidate_sha"
run_release_after_acceptance >"$work/duplicate.out"
[[ "$(grep -c '^workflow run release.yml ' "$FAKE_GH_LOG")" -eq 1 ]]
grep -q 'not dispatching again' "$work/duplicate.out"

printf '%s\n' 'urgent fix after the release' >"$fixture_repo/post-release-hotfix.txt"
git -C "$fixture_repo" add post-release-hotfix.txt
git -C "$fixture_repo" commit -q -m 'fix(release): add post-release hotfix'
git -C "$fixture_repo" push -q origin main
hotfix_sha="$(git -C "$fixture_repo" rev-parse HEAD)"
run_release_after_acceptance >"$work/post-release-hotfix.out"
grep -q "manifest $candidate_tag was consumed at $candidate_sha" "$work/post-release-hotfix.out"
[[ "$(git --git-dir="$fixture_origin" rev-parse "refs/tags/$candidate_tag")" == "$candidate_sha" ]]
[[ "$(grep -c '^workflow run release.yml ' "$FAKE_GH_LOG")" -eq 1 ]]
if grep -Fq "$hotfix_sha" <(git --git-dir="$fixture_origin" show-ref --tags); then
	echo "post-release hotfix moved or created the consumed tag" >&2
	exit 1
fi

setup_fixture paginated-release-run
export FAKE_RELEASE_RUN_PAGE_2=1
run_release_after_acceptance >"$work/paginated-release-run.out"
grep -q 'not dispatching again' "$work/paginated-release-run.out"
if grep -q '^workflow run release.yml ' "$FAKE_GH_LOG"; then
  echo "page-two release run was dispatched again" >&2
  exit 1
fi
grep -Fq 'api --paginate repos/example/attn/actions/workflows/release.yml/runs?event=workflow_dispatch&per_page=100' "$FAKE_GH_LOG"
export FAKE_RELEASE_RUN_PAGE_2=0

setup_fixture repaired
export FAKE_ACCEPTANCE_CONCLUSION=failure
run_release_after_acceptance >/dev/null
printf '%s\n' 'repair exact accepted main' >"$fixture_repo/repair.txt"
git -C "$fixture_repo" add repair.txt
git -C "$fixture_repo" commit -q -m 'fix(release): repair main before tagging'
git -C "$fixture_repo" push -q origin main
repaired_sha="$(git -C "$fixture_repo" rev-parse HEAD)"
export FAKE_ACCEPTANCE_CONCLUSION=success
export FAKE_TAG_SHA="$repaired_sha"
run_release_after_acceptance >"$work/repaired.out"
[[ "$(git --git-dir="$fixture_origin" rev-parse "refs/tags/$candidate_tag")" == "$repaired_sha" ]]

setup_fixture forged-tag
(cd "$fixture_repo" && go run ./cmd/release-train version set v99.98.96 >/dev/null)
git -C "$fixture_repo" add app
git -C "$fixture_repo" commit -q -m 'forge accepted main versions'
git -C "$fixture_repo" push -q origin main
forged_sha="$(git -C "$fixture_repo" rev-parse HEAD)"
git --git-dir="$fixture_origin" tag "$candidate_tag" "$forged_sha"
export FAKE_TAG_SHA="$forged_sha"
expect_failure 'expected 99.98.97' run_release_after_acceptance
if grep -q '^workflow run release.yml ' "$FAKE_GH_LOG"; then
  echo "pre-existing exact tag bypassed accepted-main validation" >&2
  exit 1
fi

for value in \
  'workflows: [CI]' \
  'branches: [main]' \
  'checks: read' \
  'pull-requests: read' \
  'ref: ${{ github.event.workflow_run.head_sha }}' \
  './scripts/release-after-acceptance.sh'; do
  grep -Fq "$value" "$root/.github/workflows/release-after-acceptance.yml"
done
grep -Fq 'run-name: ${{ inputs.tag }}' "$root/.github/workflows/release.yml"
trigger_block="$(sed -n '/^on:/,/^jobs:/p' "$root/.github/workflows/release.yml")"
grep -Fq 'workflow_dispatch:' <<<"$trigger_block"
if grep -Fq 'push:' <<<"$trigger_block"; then
	echo "release workflow still accepts unvalidated tag pushes" >&2
	exit 1
fi
for value in \
  'validate-tag:' \
  'checks: read' \
  'pull-requests: read' \
  'group: release-${{ inputs.tag }}' \
  'cancel-in-progress: false' \
  'fetch-depth: 0' \
  'ref: ${{ github.sha }}' \
  'release_sha: ${{ steps.validate.outputs.release_sha }}' \
  'ref: ${{ env.RELEASE_SHA }}' \
  'Recheck immutable release tag' \
  'needs: validate-tag' \
  './scripts/release-tag-gate.sh "$RELEASE_TAG_INPUT"'; do
  grep -Fq "$value" "$root/.github/workflows/release.yml"
done
if [[ "$(grep -Fc 'ref: ${{ env.RELEASE_SHA }}' "$root/.github/workflows/release.yml")" -ne 3 ]]; then
  echo "release build jobs are not all pinned to the validated SHA" >&2
  exit 1
fi
if grep -Fq 'ref: ${{ env.RELEASE_TAG }}' "$root/.github/workflows/release.yml"; then
  echo "release build still resolves the mutable tag after validation" >&2
  exit 1
fi

echo "release after acceptance: OK"

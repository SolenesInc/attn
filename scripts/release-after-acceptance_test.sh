#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$root/scripts/lib/test-git.sh"
source "$root/scripts/lib/release-fixture.sh"
script="$root/scripts/release-after-acceptance.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-release-after-acceptance-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"

if [[ "$1 $2 $3" == "api --method POST" ]] && [[ "$*" == *'/git/refs '* ]]; then
  tag_ref=''
  tag_sha=''
  for arg in "$@"; do
    case "$arg" in
      ref=*) tag_ref="${arg#ref=}" ;;
      sha=*) tag_sha="${arg#sha=}" ;;
    esac
  done
  if [[ "${FAKE_CREATE_RACE_TAG:-0}" == 1 ]]; then
    $REAL_GIT --git-dir="$FAKE_ORIGIN" update-ref "$tag_ref" "$FAKE_RACE_TAG_SHA"
  fi
  if $REAL_GIT --git-dir="$FAKE_ORIGIN" show-ref --verify --quiet "$tag_ref"; then
    echo 'Reference already exists' >&2
    exit 1
  fi
  $REAL_GIT --git-dir="$FAKE_ORIGIN" update-ref "$tag_ref" "$tag_sha"
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *'/actions/workflows/ci.yml/runs?event=push'* ]]; then
  if [[ "$FAKE_CI_STATUS" == completed ]]; then
    ci_conclusion=success
  else
    ci_conclusion=none
  fi
  printf '2026-08-29T10:00:00Z\t42\t%s\t%s\t%s\t%s\n' "$FAKE_EXPECTED_SHA" \
    "$FAKE_CI_STATUS" "$ci_conclusion" 'https://github.com/example/attn/actions/runs/42'
  exit 0
fi
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/actions/runs/42/jobs?'* ]] && \
  [[ "$*" == *'App acceptance'* ]]; then
  if [[ "${FAKE_CI_APP_MODE:-missing}" != missing ]]; then
    printf 'completed\t%s\t%s\n' "${FAKE_CI_APP_CONCLUSION:-success}" \
      'https://github.com/example/attn/actions/runs/42/job/10'
  fi
  exit 0
fi
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/actions/runs/42/jobs?'* ]]; then
  printf '%s\t%s\t%s\n' completed "$FAKE_ACCEPTANCE_CONCLUSION" \
    'https://github.com/example/attn/actions/runs/42/job/7'
  exit 0
fi
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/actions/workflows/release.yml/runs?'* ]]; then
  if [[ "${FAKE_RELEASE_RUN_PAGE_2:-0}" == 1 ]] || grep -q '^api --method POST repos/.*/dispatches ' "$FAKE_GH_LOG"; then
    release_status="${FAKE_RELEASE_RUN_STATUS:-completed}"
    release_conclusion="${FAKE_RELEASE_RUN_CONCLUSION:-success}"
    if [[ "$release_status" != completed || "$release_conclusion" == success ]]; then
      printf '99\t%s\t%s\n' "$release_status" "$release_conclusion"
    fi
  fi
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *'/actions/workflows/app-acceptance.yml/runs?'* ]]; then
  if [[ "${FAKE_APP_MODE:-success}" != missing ]] && [[ "$*" =~ App\ acceptance\ ([0-9a-f]{40}) ]]; then
    printf '2026-08-29T10:00:00Z\t43\tApp acceptance %s\tcompleted\tsuccess\t%s\n' "${BASH_REMATCH[1]}" \
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
if [[ "$1 $2 $3" == "api --method POST" ]] && [[ "$*" == *'/dispatches '* ]]; then
  if ! grep -Fq -- "-f event_type=release -F client_payload[tag]=$FAKE_EXPECTED_TAG" <<<"$*"; then
    echo "release repository dispatch has invalid payload: $*" >&2
    exit 1
  fi
  exit 0
fi

echo "unexpected gh command: $*" >&2
exit 2
EOF
chmod +x "$work/bin/gh"

real_git="$(resolve_test_git)"

export PATH="$work/bin:$PATH"
export REAL_GIT="$real_git"
export GITHUB_REPOSITORY=example/attn
export FAKE_GH_LOG="$work/gh.log"
export FAKE_ACCEPTANCE_CONCLUSION=success
export FAKE_TAG_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
export FAKE_APP_MODE=success
export FAKE_APP_CONCLUSION=success
export FAKE_CI_APP_MODE=missing
export FAKE_CI_APP_CONCLUSION=success
export FAKE_RELEASE_RUN_PAGE_2=0
export FAKE_RELEASE_RUN_STATUS=completed
export FAKE_RELEASE_RUN_CONCLUSION=success
export FAKE_CREATE_RACE_TAG=0
export FAKE_CI_STATUS=completed

template="$work/template"
init_release_fixture "$root" "$template"
baseline_sha="$(git -C "$template" rev-parse HEAD)"
marker="$(cd "$template" && go run ./cmd/release-train notes range)"
printf '\n## [2099-01-01]\n\nNo changes users will notice.\n\n%s\n' "$marker" >>"$template/CHANGELOG.md"
(cd "$template" && go run ./cmd/release-train version set v99.98.97 >/dev/null)
version=99.98.97
cat >"$template/.github/release-candidate.yml" <<EOF
version: $version
main_sha: $baseline_sha
EOF
git -C "$template" add -A
git -C "$template" commit -q -m 'chore(release): prepare v99.98.97'
candidate_sha="$(git -C "$template" rev-parse HEAD)"
candidate_tag="v$version"
export FAKE_EXPECTED_SHA="$candidate_sha"
export FAKE_EXPECTED_TAG="$candidate_tag"

setup_fixture() {
  local name="$1"
  fixture_origin="$work/$name-origin.git"
  fixture_repo="$work/$name-repo"
  : >"$FAKE_GH_LOG"

  export FAKE_APP_MODE=success
  export FAKE_APP_CONCLUSION=success
  export FAKE_CI_APP_MODE=missing
  export FAKE_CI_APP_CONCLUSION=success
  export FAKE_RELEASE_RUN_PAGE_2=0
  export FAKE_RELEASE_RUN_STATUS=completed
  export FAKE_RELEASE_RUN_CONCLUSION=success
  export FAKE_CREATE_RACE_TAG=0
  export FAKE_CI_STATUS=completed

  git clone -q --bare "$template" "$fixture_origin"
  git clone -q "$fixture_origin" "$fixture_repo"
  git -C "$fixture_repo" config user.name 'Release Test'
  git -C "$fixture_repo" config user.email 'release@example.com'
  export FAKE_ORIGIN="$fixture_origin"
  set_reviewed "$candidate_sha"
}

set_reviewed() {
  git -C "$fixture_repo" push -q -f origin "$1:refs/heads/reviewed"
  git -C "$fixture_repo" fetch -q origin +refs/heads/reviewed:refs/remotes/origin/reviewed
}

run_release_after_acceptance() (
  cd "$fixture_repo"
  "$script"
)

expect_untagged() {
  if git --git-dir="$fixture_origin" show-ref --verify --quiet "refs/tags/$candidate_tag"; then
    echo "$1 created a tag" >&2
    exit 1
  fi
}

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
grep -q 'release commit stays untagged' "$work/red.out"
if git --git-dir="$fixture_origin" show-ref --verify --quiet "refs/tags/$candidate_tag"; then
  echo "red Acceptance created a tag" >&2
  exit 1
fi

setup_fixture main-moved
export FAKE_ACCEPTANCE_CONCLUSION=success
git clone -q "$fixture_origin" "$work/main-moved-updater"
git -C "$work/main-moved-updater" config user.name 'Release Test'
git -C "$work/main-moved-updater" config user.email 'release@example.com'
printf '%s\n' 'main moved' >"$work/main-moved-updater/moved.txt"
git -C "$work/main-moved-updater" add moved.txt
git -C "$work/main-moved-updater" commit -q -m 'feat: land work after the release commit'
git -C "$work/main-moved-updater" push -q origin main
run_release_after_acceptance >"$work/main-moved.out"
[[ "$(git --git-dir="$fixture_origin" rev-parse "refs/tags/$candidate_tag")" == "$candidate_sha" ]]
grep -Fq "api --method POST repos/example/attn/dispatches -f event_type=release -F client_payload[tag]=$candidate_tag" "$FAKE_GH_LOG"

setup_fixture missing-app
export FAKE_APP_MODE=missing
expect_failure 'has no app-acceptance.yml workflow_dispatch run' run_release_after_acceptance
if git --git-dir="$fixture_origin" show-ref --verify --quiet "refs/tags/$candidate_tag"; then
  echo "missing App acceptance created a tag" >&2
  exit 1
fi

setup_fixture automated-app
export FAKE_CI_APP_MODE=success
run_release_after_acceptance >"$work/automated-app.out"
grep -Fq "CI App acceptance is green for main $candidate_sha" "$work/automated-app.out"
if grep -q 'app-acceptance.yml' "$FAKE_GH_LOG"; then
  echo "green main App acceptance queried the manual receipt" >&2
  exit 1
fi
export FAKE_CI_APP_MODE=missing

setup_fixture tag-elsewhere
export FAKE_CREATE_RACE_TAG=1
export FAKE_RACE_TAG_SHA="$baseline_sha"
export FAKE_TAG_SHA="$baseline_sha"
expect_failure "was concurrently created at $baseline_sha, not release commit $candidate_sha" run_release_after_acceptance
if grep -q '^api --method POST repos/example/attn/dispatches ' "$FAKE_GH_LOG"; then
  echo "a tag on another commit was dispatched" >&2
  exit 1
fi
export FAKE_CREATE_RACE_TAG=0

setup_fixture concurrent-tag
export FAKE_CREATE_RACE_TAG=1
export FAKE_RACE_TAG_SHA="$candidate_sha"
export FAKE_TAG_SHA="$candidate_sha"
run_release_after_acceptance >"$work/concurrent-tag.out"
grep -q 'concurrently created at release commit' "$work/concurrent-tag.out"
[[ "$(git --git-dir="$fixture_origin" rev-parse "refs/tags/$candidate_tag")" == "$candidate_sha" ]]
grep -Fq "api --method POST repos/example/attn/dispatches -f event_type=release -F client_payload[tag]=$candidate_tag" "$FAKE_GH_LOG"
export FAKE_CREATE_RACE_TAG=0

setup_fixture red-app
export FAKE_APP_CONCLUSION=failure
expect_failure 'App acceptance is completed/failure' run_release_after_acceptance
if git --git-dir="$fixture_origin" show-ref --verify --quiet "refs/tags/$candidate_tag"; then
  echo "red App acceptance created a tag" >&2
  exit 1
fi

setup_fixture created-tag-active-run
export FAKE_RELEASE_RUN_PAGE_2=1
export FAKE_RELEASE_RUN_STATUS=in_progress
export FAKE_RELEASE_RUN_CONCLUSION=
run_release_after_acceptance >"$work/created-tag-active-run.out"
[[ "$(git --git-dir="$fixture_origin" rev-parse "refs/tags/$candidate_tag")" == "$candidate_sha" ]]
grep -q 'not dispatching again' "$work/created-tag-active-run.out"
if grep -q '^api --method POST repos/example/attn/dispatches ' "$FAKE_GH_LOG"; then
  echo "newly created tag duplicated an active release run" >&2
  exit 1
fi

setup_fixture success
run_release_after_acceptance >"$work/success.out"
[[ "$(git --git-dir="$fixture_origin" rev-parse "refs/tags/$candidate_tag")" == "$candidate_sha" ]]
grep -Fq "api --method POST repos/example/attn/dispatches -f event_type=release -F client_payload[tag]=$candidate_tag" "$FAKE_GH_LOG"

land_after_release() {
  printf '%s\n' 'work after the release' >"$fixture_repo/after-release.txt"
  git -C "$fixture_repo" add after-release.txt
  git -C "$fixture_repo" commit -q -m 'fix: ordinary work after the release'
  git -C "$fixture_repo" push -q origin main
}

land_after_release
set_reviewed "$(git -C "$fixture_repo" rev-parse HEAD)"
: >"$FAKE_GH_LOG"
run_release_after_acceptance >"$work/after-release.out"
grep -Fq "release commit $candidate_sha is already tagged $candidate_tag; nothing to release" "$work/after-release.out"
if [ -s "$FAKE_GH_LOG" ]; then
  echo "a released commit called GitHub again" >&2
  cat "$FAKE_GH_LOG" >&2
  exit 1
fi

setup_fixture unreviewed
previous_release_sha="$(git -C "$fixture_repo" rev-list --max-parents=0 HEAD)"
previous_tag="$(cd "$fixture_repo" && go run ./cmd/release-train accepted-main tag --head "$previous_release_sha")"
git --git-dir="$fixture_origin" update-ref "refs/tags/$previous_tag" "$previous_release_sha"
set_reviewed "$baseline_sha"
run_release_after_acceptance >"$work/unreviewed.out"
grep -Fq "release commit $previous_release_sha is already tagged $previous_tag; nothing to release" "$work/unreviewed.out"
expect_untagged 'an unreviewed release commit'
if [ -s "$FAKE_GH_LOG" ]; then
  echo "an unreviewed release commit called GitHub" >&2
  cat "$FAKE_GH_LOG" >&2
  exit 1
fi
land_after_release
set_reviewed "$(git -C "$fixture_repo" rev-parse HEAD)"
run_release_after_acceptance >"$work/reviewed-retry.out"
[[ "$(git --git-dir="$fixture_origin" rev-parse "refs/tags/$candidate_tag")" == "$candidate_sha" ]]
grep -Fq "api --method POST repos/example/attn/dispatches -f event_type=release -F client_payload[tag]=$candidate_tag" "$FAKE_GH_LOG"

setup_fixture reviewed-before-ci
export FAKE_CI_STATUS=in_progress
run_release_after_acceptance >"$work/reviewed-before-ci.out"
grep -Fq 'CI run 42 is in_progress; release commit stays untagged until it completes' "$work/reviewed-before-ci.out"
expect_untagged 'a release commit whose CI is running'
export FAKE_CI_STATUS=completed
run_release_after_acceptance >"$work/ci-after-review.out"
[[ "$(git --git-dir="$fixture_origin" rev-parse "refs/tags/$candidate_tag")" == "$candidate_sha" ]]
grep -Fq "api --method POST repos/example/attn/dispatches -f event_type=release -F client_payload[tag]=$candidate_tag" "$FAKE_GH_LOG"

setup_fixture existing-tag-elsewhere
git --git-dir="$fixture_origin" update-ref "refs/tags/$candidate_tag" "$baseline_sha"
expect_failure "$candidate_tag already points to $baseline_sha, not release commit $candidate_sha" run_release_after_acceptance
if grep -q '^api --method POST repos/example/attn/dispatches ' "$FAKE_GH_LOG"; then
  echo "a tag on another commit was dispatched" >&2
  exit 1
fi

setup_fixture forged-manifest
(cd "$fixture_repo" && go run ./cmd/release-train version set v99.98.96 >/dev/null)
(cd "$fixture_repo" && go run ./cmd/release-train manifest write --version v99.98.97 --main HEAD >/dev/null)
git -C "$fixture_repo" add app .github/release-candidate.yml
git -C "$fixture_repo" commit -q -m 'forge accepted main versions'
git -C "$fixture_repo" push -q origin main
set_reviewed "$(git -C "$fixture_repo" rev-parse HEAD)"
expect_failure 'expected 99.98.97' run_release_after_acceptance
expect_untagged 'a forged release manifest'

for value in \
  'workflows: [CI]' \
  'branches: [main]' \
  'branches: [reviewed]' \
  'checks: read' \
  'pull-requests: read' \
  'run: ./scripts/release-after-acceptance.sh'; do
  grep -Fq "$value" "$root/.github/workflows/release-after-acceptance.yml"
done
grep -Fq 'run-name: ${{ github.event.client_payload.tag }}' "$root/.github/workflows/release.yml"
trigger_block="$(sed -n '/^on:/,/^jobs:/p' "$root/.github/workflows/release.yml")"
grep -Fq 'repository_dispatch:' <<<"$trigger_block"
grep -Fq 'types: [release]' <<<"$trigger_block"
if grep -Fq 'push:' <<<"$trigger_block"; then
	echo "release workflow still accepts unvalidated tag pushes" >&2
	exit 1
fi
for value in \
  'validate-tag:' \
  'checks: read' \
  'pull-requests: read' \
  'group: release-${{ github.event.client_payload.tag }}' \
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

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

if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/jobs?filter=latest'* ]]; then
  printf '%s\t%s\t%s\n' completed "$FAKE_ACCEPTANCE_CONCLUSION" \
    'https://github.com/example/attn/actions/runs/42/job/7'
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *'/actions/workflows/release.yml/runs?'* ]]; then
  if grep -q '^workflow run release.yml ' "$FAKE_GH_LOG"; then
    printf '%s\n' 1
  else
    printf '%s\n' 0
  fi
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *'/commits/v'* ]]; then
  printf '%s\n' "$FAKE_TAG_SHA"
  exit 0
fi
if [[ "$1 $2 $3" == "workflow run release.yml" ]]; then
  exit 0
fi

echo "unexpected gh command: $*" >&2
exit 2
EOF
chmod +x "$work/bin/gh"

export PATH="$work/bin:$PATH"
export GITHUB_REPOSITORY=example/attn
export GOCACHE="$work/go-cache"
export FAKE_GH_LOG="$work/gh.log"
export FAKE_ACCEPTANCE_CONCLUSION=success
export FAKE_TAG_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa

setup_fixture() {
  local name="$1"
  fixture_origin="$work/$name-origin.git"
  fixture_repo="$work/$name-repo"
  : >"$FAKE_GH_LOG"

  git init -q --bare "$fixture_origin"
  git clone -q "$root" "$fixture_repo"
  git -C "$fixture_repo" config user.name 'Release Test'
  git -C "$fixture_repo" config user.email 'release@example.com'
  git -C "$fixture_repo" switch -q -C main
  cp "$root/cmd/release-train/main.go" "$fixture_repo/cmd/release-train/main.go"
  git -C "$fixture_repo" add cmd/release-train/main.go
  git -C "$fixture_repo" commit -q -m 'test(release): install accepted-main validator'
	  baseline_sha="$(git -C "$fixture_repo" rev-parse HEAD)"

	  find "$fixture_repo/changelog.d" -type f -name '*.yaml' -delete
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
}

run_release_after_acceptance() (
  cd "$fixture_repo"
  "$script" "$(git rev-parse HEAD)" 42 \
    'https://github.com/example/attn/actions/runs/42'
)

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

setup_fixture success
export FAKE_ACCEPTANCE_CONCLUSION=success
run_release_after_acceptance >"$work/success.out"
tag_sha="$(git --git-dir="$fixture_origin" rev-parse "refs/tags/$candidate_tag")"
[[ "$tag_sha" == "$candidate_sha" ]]
grep -q "workflow run release.yml --repo example/attn --ref $candidate_tag -f tag=$candidate_tag" "$FAKE_GH_LOG"

export FAKE_TAG_SHA="$candidate_sha"
run_release_after_acceptance >"$work/duplicate.out"
[[ "$(grep -c '^workflow run release.yml ' "$FAKE_GH_LOG")" -eq 1 ]]
grep -q 'not dispatching again' "$work/duplicate.out"

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

for value in \
  'workflows: [CI]' \
  'branches: [main]' \
  'ref: ${{ github.event.workflow_run.head_sha }}' \
  './scripts/release-after-acceptance.sh'; do
  grep -Fq "$value" "$root/.github/workflows/release-after-acceptance.yml"
done
grep -Fq 'run-name: ${{ inputs.tag || github.ref_name }}' "$root/.github/workflows/release.yml"

echo "release after acceptance: OK"

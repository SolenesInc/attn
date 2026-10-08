#!/usr/bin/env bash
set -euo pipefail

remote="${RELEASE_TRAIN_REMOTE:-origin}"

usage() {
  cat <<EOF
usage: $0 <version-tag> [--dry-run]
example: $0 v0.12.0

Cut release/vX.Y.Z from the accepted ${remote}/main head, compile its
changelog, bump versions, write the release manifest, and open a PR to main.
Once merged, Acceptance on the release commit tags and publishes it. This
command never merges, tags, or starts a release.
EOF
}

if [[ $# -lt 1 ]]; then
  usage >&2
  exit 2
fi

version_tag="$1"
shift
dry_run=0

if [[ ! "$version_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "prepare release: version tag must look like v1.2.3" >&2
  exit 1
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) dry_run=1 ;;
    *) usage >&2; exit 2 ;;
  esac
  shift
done

for command in git gh go; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "prepare release: ${command} is required" >&2
    exit 1
  fi
done

root="$(git rev-parse --show-toplevel)"
script_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$script_root/lib/release-tag.sh"
cd "$root"

if [[ -n "$(git status --porcelain)" ]]; then
  echo "prepare release: working tree must be clean" >&2
  exit 1
fi
if [[ "$(git branch --show-current)" != "main" ]]; then
  echo "prepare release: run from the local main branch" >&2
  exit 1
fi
if ! gh auth status >/dev/null 2>&1; then
  echo "prepare release: gh is not authenticated; run 'gh auth login'" >&2
  exit 1
fi

repo_info="$(gh repo view --json nameWithOwner,url --jq '[.nameWithOwner, .url] | @tsv')"
IFS=$'\t' read -r repo_name repo_url <<<"$repo_info"
if [[ -z "$repo_name" || -z "$repo_url" ]]; then
  echo "prepare release: could not resolve the GitHub repository" >&2
  exit 1
fi

echo "Fetching ${remote}/main..."
git fetch --no-tags "$remote" main
main_sha="$(git rev-parse --verify "${remote}/main^{commit}")"
local_sha="$(git rev-parse --verify HEAD)"
release_branch="release/${version_tag}"

if [[ "$local_sha" != "$main_sha" ]]; then
  echo "prepare release: local main is not ${remote}/main" >&2
  echo "local:  ${local_sha}" >&2
  echo "remote: ${main_sha}" >&2
  echo "fast-forward main and wait for Acceptance on the new head" >&2
  exit 1
fi
require_remote_tag_absent "$remote" "$version_tag" "prepare release" \
  "tag ${version_tag} already exists"
if git ls-remote --exit-code --heads "$remote" "$release_branch" >/dev/null 2>&1; then
  echo "prepare release: remote branch ${release_branch} already exists" >&2
  exit 1
fi

candidate_prs="$(bash "$script_root/open-release-candidates.sh")"
if [[ -n "$candidate_prs" ]]; then
  echo "prepare release: another release candidate is open:" >&2
  printf '  %s\n' "$candidate_prs" >&2
  exit 1
fi

acceptance="$(
  gh api --method GET \
    "repos/{owner}/{repo}/commits/${main_sha}/check-runs?check_name=Acceptance&filter=latest" \
    --jq '.check_runs | map(select(.name == "Acceptance" and .app.slug == "github-actions")) | sort_by(.started_at) | last | select(.) | [.head_sha, .status, (.conclusion // ""), .html_url] | @tsv'
)"
if [[ -z "$acceptance" ]]; then
  echo "prepare release: ${main_sha} has no Acceptance check" >&2
  exit 1
fi
IFS=$'\t' read -r acceptance_sha acceptance_status acceptance_conclusion acceptance_url <<<"$acceptance"
if [[ "$acceptance_sha" != "$main_sha" ]]; then
  echo "prepare release: Acceptance belongs to ${acceptance_sha}, expected ${main_sha}" >&2
  exit 1
fi
if [[ "$acceptance_status" != "completed" || "$acceptance_conclusion" != "success" ]]; then
  echo "prepare release: Acceptance is ${acceptance_status}/${acceptance_conclusion:-none}" >&2
  echo "${acceptance_url}" >&2
  exit 1
fi

if [[ "$dry_run" -eq 1 ]]; then
  cat <<EOF
Release preparation dry run
- version: ${version_tag}
- accepted main: ${main_sha}
- acceptance: ${acceptance_url}
- branch: ${release_branch}
- would compile the changelog, update versions, write the manifest, and open a PR ready for review
- would not merge, tag, or dispatch a release
EOF
  exit 0
fi

for command in claude pnpm cargo; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "prepare release: ${command} is required" >&2
    exit 1
  fi
done

work="$(mktemp -d "${TMPDIR:-/tmp}/attn-release-candidate.XXXXXX")"
prepare="$work/worktree"
cleanup() {
  git -C "$root" worktree remove --force "$prepare" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT
body="$work/pr-body.md"
git worktree add -q --detach "$prepare" "$main_sha"
cd "$prepare"
fragment_count="$(find changelog.d -maxdepth 1 -type f -name '*.yaml' | wc -l | tr -d '[:space:]')"
fragment_noun=fragments
if [[ "$fragment_count" == 1 ]]; then
  fragment_noun=fragment
fi

echo "Cutting ${release_branch} from ${main_sha}..."
./scripts/compile-changelog.sh
go run ./cmd/release-train version set "$version_tag"
(cd app && pnpm install --frozen-lockfile)
cargo metadata --manifest-path app/src-tauri/Cargo.toml --format-version 1 >/dev/null
go run ./cmd/release-train version check "$version_tag"
go run ./cmd/release-train manifest write --version "$version_tag" --main "$main_sha"

git add -A CHANGELOG.md changelog.d .github/release-candidate.yml \
  app/package.json app/pnpm-lock.yaml app/src-tauri/tauri.conf.json \
  app/src-tauri/Cargo.toml app/src-tauri/Cargo.lock
git commit -m "chore(release): prepare ${version_tag}"
candidate_sha="$(git rev-parse HEAD)"

echo "Rechecking main before publishing..."
git fetch --no-tags "$remote" main
current_main_sha="$(git rev-parse --verify "${remote}/main^{commit}")"
if [[ "$current_main_sha" != "$main_sha" ]]; then
  echo "prepare release: main moved during preparation" >&2
  echo "recorded: ${main_sha}" >&2
  echo "current:  ${current_main_sha}" >&2
  exit 1
fi
require_remote_tag_absent "$remote" "$version_tag" "prepare release" \
  "tag ${version_tag} appeared during preparation"
candidate_prs="$(bash "$script_root/open-release-candidates.sh")"
if [[ -n "$candidate_prs" ]]; then
  echo "prepare release: another candidate opened during preparation:" >&2
  printf '  %s\n' "$candidate_prs" >&2
  exit 1
fi

go run ./cmd/release-train candidate validate \
  --current-main "$main_sha" \
  --head "$candidate_sha" \
  --tag-status absent \
  --other-open-candidates 0

cat >"$body" <<EOF
## TL;DR

Releases accepted \`main\` commit [\`${main_sha:0:12}\`](${repo_url}/commit/${main_sha}) as ${version_tag}. Once this PR merges, Acceptance on the release commit tags it and starts publication.

## Release

| Field | Value |
| --- | --- |
| Version | \`${version_tag}\` |
| Accepted main | [\`${main_sha}\`](${repo_url}/commit/${main_sha}) |
| Main Acceptance | [green check](${acceptance_url}) |
| Candidate head | [\`${candidate_sha}\`](${repo_url}/commit/${candidate_sha}) |

\`main\` may keep moving while this PR is open. Changes merged after the accepted commit ship with this release, and their changelog fragments stay pending for the next one.

## What changed

- compiled ${fragment_count} changelog ${fragment_noun} into \`CHANGELOG.md\`
- updated every committed app version to \`${version_tag}\`
- recorded the accepted main commit in \`.github/release-candidate.yml\`

## App acceptance

CI builds the packaged Linux app and runs the real-app serial matrix on this exact
candidate. If that job cannot cover the candidate, record a manual override:

\`\`\`bash
gh workflow run app-acceptance.yml \\
  --ref main \\
  -f candidate_sha=${candidate_sha} \\
  -f instance=<instance> \\
  -f scenarios='<scenarios run>' \\
  -f evidence='<recording URL or concise receipt>' \\
  -f outcome=passed
\`\`\`

A manual dispatch does not restart this candidate's CI run. Rerun CI after
recording the override.

Do not merge until \`PR gate\` and \`App acceptance\` are green on \`${candidate_sha}\`.
If App acceptance cannot cover the merged release commit on \`main\` either, record
the same receipt with \`candidate_sha\` set to that commit and rerun its CI.
EOF

echo "Pushing ${release_branch}..."
git push "$remote" "HEAD:refs/heads/${release_branch}"
pr_url="$(gh pr create --base main --head "$release_branch" \
  --title "chore(release): prepare ${version_tag}" --body-file "$body")"

echo "Opened release candidate ${pr_url}"
echo "Next: review the changelog and merge once PR gate and App acceptance are green."
echo "This command did not merge, tag, or start a release."

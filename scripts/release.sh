#!/usr/bin/env bash
set -euo pipefail

remote="${RELEASE_TRAIN_REMOTE:-origin}"

usage() {
  cat <<EOF
usage: $0 <version-tag> [--dry-run]
example: $0 v0.12.0

Freeze the accepted ${remote}/next head into release/vX.Y.Z, compile its
changelog, bump versions, write the release manifest, and open a draft PR to
main. This command never merges, tags, or starts a release.
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
cd "$root"

if [[ -n "$(git status --porcelain)" ]]; then
  echo "prepare release: working tree must be clean" >&2
  exit 1
fi
if [[ "$(git branch --show-current)" != "next" ]]; then
  echo "prepare release: run from the local next branch" >&2
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

echo "Fetching ${remote}/main, ${remote}/next, and tags..."
git fetch --tags "$remote" main next

source_sha="$(git rev-parse --verify "${remote}/next^{commit}")"
main_sha="$(git rev-parse --verify "${remote}/main^{commit}")"
local_sha="$(git rev-parse --verify HEAD)"
release_branch="release/${version_tag}"

if [[ "$local_sha" != "$source_sha" ]]; then
  echo "prepare release: local next is stale" >&2
  echo "local:  ${local_sha}" >&2
  echo "remote: ${source_sha}" >&2
  echo "fast-forward next and wait for Acceptance on the new head" >&2
  exit 1
fi
if ! git merge-base --is-ancestor "$main_sha" "$source_sha"; then
  echo "prepare release: current main is not an ancestor of accepted next" >&2
  echo "sync main into next with ./scripts/sync-main-to-next.sh first" >&2
  exit 1
fi
if git show-ref --verify --quiet "refs/tags/${version_tag}"; then
  echo "prepare release: tag ${version_tag} already exists" >&2
  exit 1
fi
if git show-ref --verify --quiet "refs/heads/${release_branch}"; then
  echo "prepare release: local branch ${release_branch} already exists" >&2
  exit 1
fi
if git ls-remote --exit-code --heads "$remote" "$release_branch" >/dev/null 2>&1; then
  echo "prepare release: remote branch ${release_branch} already exists" >&2
  exit 1
fi

open_candidates() {
  gh pr list --base main --state open --limit 100 --json headRefName,url \
    --jq '.[] | select(.headRefName | test("^release/v[0-9]+\\.[0-9]+\\.[0-9]+$")) | "\(.headRefName)\t\(.url)"'
}

candidate_prs="$(open_candidates)"
if [[ -n "$candidate_prs" ]]; then
  echo "prepare release: another release candidate is open:" >&2
  printf '  %s\n' "$candidate_prs" >&2
  exit 1
fi

acceptance="$(
  gh api --method GET \
    "repos/{owner}/{repo}/commits/${source_sha}/check-runs?check_name=Acceptance&filter=latest" \
    --jq '.check_runs | map(select(.name == "Acceptance" and .app.slug == "github-actions")) | sort_by(.started_at) | last | select(.) | [.head_sha, .status, (.conclusion // ""), .html_url] | @tsv'
)"
if [[ -z "$acceptance" ]]; then
  echo "prepare release: ${source_sha} has no Acceptance check" >&2
  exit 1
fi
IFS=$'\t' read -r acceptance_sha acceptance_status acceptance_conclusion acceptance_url <<<"$acceptance"
if [[ "$acceptance_sha" != "$source_sha" ]]; then
  echo "prepare release: Acceptance belongs to ${acceptance_sha}, expected ${source_sha}" >&2
  exit 1
fi
if [[ "$acceptance_status" != "completed" || "$acceptance_conclusion" != "success" ]]; then
  echo "prepare release: Acceptance is ${acceptance_status}/${acceptance_conclusion:-none}" >&2
  echo "${acceptance_url}" >&2
  exit 1
fi

if [[ "$dry_run" -eq 1 ]]; then
  cat <<EOF
Candidate preparation dry run
- version: ${version_tag}
- accepted next: ${source_sha}
- acceptance: ${acceptance_url}
- main baseline: ${main_sha}
- branch: ${release_branch}
- would compile the changelog, update versions, write the manifest, and open a draft PR
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
trap 'rm -rf "$work"' EXIT
facts="$work/fragments.md"
body="$work/pr-body.md"
go run ./cmd/release-train fragments render >"$facts"

echo "Creating frozen candidate ${release_branch} from ${source_sha}..."
git switch -c "$release_branch" "$source_sha"

./scripts/compile-changelog.sh
go run ./cmd/release-train version set "$version_tag"
(cd app && pnpm install --frozen-lockfile)
(cd app/src-tauri && cargo check -q)
go run ./cmd/release-train version check "$version_tag"
go run ./cmd/release-train manifest write \
  --version "$version_tag" --kind promotion \
  --source "$source_sha" --main "$main_sha"

git add -A CHANGELOG.md changelog.d .github/release-candidate.yml \
  app/package.json app/pnpm-lock.yaml app/src-tauri/tauri.conf.json \
  app/src-tauri/Cargo.toml app/src-tauri/Cargo.lock
git commit -m "chore(release): prepare ${version_tag}"
candidate_sha="$(git rev-parse HEAD)"

echo "Rechecking the frozen baseline before publishing..."
git fetch --tags "$remote" main
current_main_sha="$(git rev-parse --verify "${remote}/main^{commit}")"
if [[ "$current_main_sha" != "$main_sha" ]]; then
  echo "prepare release: main moved during preparation" >&2
  echo "recorded: ${main_sha}" >&2
  echo "current:  ${current_main_sha}" >&2
  exit 1
fi
if git show-ref --verify --quiet "refs/tags/${version_tag}"; then
  echo "prepare release: tag ${version_tag} appeared during preparation" >&2
  exit 1
fi
candidate_prs="$(open_candidates)"
if [[ -n "$candidate_prs" ]]; then
  echo "prepare release: another candidate opened during preparation:" >&2
  printf '  %s\n' "$candidate_prs" >&2
  exit 1
fi

go run ./cmd/release-train candidate validate \
  --current-main "$main_sha" --head "$candidate_sha" \
  --source-acceptance success --other-open-candidates 0

manifest_version="$(awk '$1 == "version:" { print $2 }' .github/release-candidate.yml)"
manifest_kind="$(awk '$1 == "kind:" { print $2 }' .github/release-candidate.yml)"
manifest_source="$(awk '$1 == "source_sha:" { print $2 }' .github/release-candidate.yml)"
manifest_main="$(awk '$1 == "main_sha:" { print $2 }' .github/release-candidate.yml)"

cat >"$body" <<EOF
## TL;DR

Freezes accepted \`next\` commit [\`${manifest_source:0:12}\`](${repo_url}/commit/${manifest_source}) as ${version_tag}. This PR contains only release preparation; merging and release dispatch remain manual gates.

## Frozen candidate

| Field | Value |
| --- | --- |
| Version | \`${manifest_version}\` |
| Kind | \`${manifest_kind}\` |
| Accepted source | [\`${manifest_source}\`](${repo_url}/commit/${manifest_source}) |
| Source Acceptance | [green check](${acceptance_url}) |
| Main baseline | [\`${manifest_main}\`](${repo_url}/commit/${manifest_main}) |
| Candidate head | [\`${candidate_sha}\`](${repo_url}/commit/${candidate_sha}) |

\`next\` may keep moving while this PR is reviewed. Those later changes are outside this frozen candidate.

## What changed

- compiled the frozen source's changelog fragments into \`CHANGELOG.md\`
- updated every committed app version to \`${manifest_version}\`
- recorded the accepted source and main baseline in \`.github/release-candidate.yml\`

## Manual app verification

Run the packaged-app scenarios from this exact candidate, then attach the receipt to this SHA:

\`\`\`bash
gh workflow run app-acceptance.yml \\
  --ref ${release_branch} \\
  -f candidate_sha=${candidate_sha} \\
  -f profile=<profile> \\
  -f scenarios='<scenarios run>' \\
  -f evidence='<recording URL or concise receipt>' \\
  -f outcome=passed
\`\`\`

Do not merge until \`PR gate\` and \`App acceptance\` are green on \`${candidate_sha}\`.

<details>
<summary>Frozen changelog inputs</summary>

EOF
sed 's/^/    /' "$facts" >>"$body"
cat >>"$body" <<'EOF'

</details>
EOF

echo "Pushing ${release_branch}..."
git push -u "$remote" "$release_branch"
pr_url="$(gh pr create --draft --base main --head "$release_branch" \
  --title "chore(release): prepare ${version_tag}" --body-file "$body")"

echo "Opened draft candidate ${pr_url}"
echo "Next: review the changelog, run the packaged app, and record App acceptance."
echo "This command did not merge, tag, or start a release."

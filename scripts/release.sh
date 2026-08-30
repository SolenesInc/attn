#!/usr/bin/env bash
set -euo pipefail

remote="${RELEASE_TRAIN_REMOTE:-origin}"

usage() {
  cat <<EOF
usage: $0 <version-tag> [--hotfix] [--hold] [--dry-run]
example: $0 v0.12.0
         $0 v0.12.0 --hold
         $0 v0.12.1 --hotfix

Freeze the accepted ${remote}/next head into release/vX.Y.Z, compile its
changelog, bump versions, write the release manifest, and open a draft PR to
main. With --hotfix, prepare the current hotfix/* branch in place from the
current ${remote}/main instead. With --hold, prepare a promotion that may merge
to main after normal CI but cannot create a tag or dispatch a release. This
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
kind=promotion
publication=automatic

if [[ ! "$version_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "prepare release: version tag must look like v1.2.3" >&2
  exit 1
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) dry_run=1 ;;
    --hold) publication=held ;;
    --hotfix) kind=hotfix ;;
    *) usage >&2; exit 2 ;;
  esac
  shift
done

if [[ "$kind" == hotfix && "$publication" == held ]]; then
  echo "prepare release: --hold is only supported for promotions" >&2
  exit 1
fi

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
current_branch="$(git branch --show-current)"
case "$kind" in
  promotion)
    if [[ "$current_branch" != "next" ]]; then
      echo "prepare release: run a promotion from the local next branch" >&2
      exit 1
    fi
    ;;
  hotfix)
    if [[ ! "$current_branch" =~ ^hotfix/.+ ]]; then
      echo "prepare release: run --hotfix from a hotfix/* branch" >&2
      exit 1
    fi
    ;;
esac
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
if [[ "$kind" == promotion ]]; then
  echo "Fetching ${remote}/next..."
  git fetch --no-tags "$remote" next
fi

main_sha="$(git rev-parse --verify "${remote}/main^{commit}")"
local_sha="$(git rev-parse --verify HEAD)"
if [[ "$kind" == promotion ]]; then
  source_sha="$(git rev-parse --verify "${remote}/next^{commit}")"
  release_branch="release/${version_tag}"
else
  source_sha="$local_sha"
  release_branch="$current_branch"
fi

if [[ "$kind" == promotion && "$local_sha" != "$source_sha" ]]; then
  echo "prepare release: local next is stale" >&2
  echo "local:  ${local_sha}" >&2
  echo "remote: ${source_sha}" >&2
  echo "fast-forward next and wait for Acceptance on the new head" >&2
  exit 1
fi
if ! git merge-base --is-ancestor "$main_sha" "$source_sha"; then
  if [[ "$kind" == promotion ]]; then
    echo "prepare release: current main is not an ancestor of accepted next" >&2
    echo "sync main into next with ./scripts/sync-main-to-next.sh first" >&2
  else
    echo "prepare release: hotfix does not contain current main" >&2
    echo "rebase or recreate the hotfix from ${remote}/main" >&2
  fi
  exit 1
fi
require_remote_tag_absent "$remote" "$version_tag" "prepare release" \
  "tag ${version_tag} already exists"
if [[ "$kind" == promotion ]]; then
  if git show-ref --verify --quiet "refs/heads/${release_branch}"; then
    echo "prepare release: local branch ${release_branch} already exists" >&2
    exit 1
  fi
  if git ls-remote --exit-code --heads "$remote" "$release_branch" >/dev/null 2>&1; then
    echo "prepare release: remote branch ${release_branch} already exists" >&2
    exit 1
  fi
else
  remote_hotfix_sha="$(git ls-remote --heads "$remote" "refs/heads/${release_branch}" | awk '{print $1}')"
  if [[ -n "$remote_hotfix_sha" ]]; then
    git fetch "$remote" "$release_branch"
    if ! git merge-base --is-ancestor FETCH_HEAD "$source_sha"; then
      echo "prepare release: remote ${release_branch} is not an ancestor of the local hotfix" >&2
      exit 1
    fi
  fi
fi

candidate_prs="$(bash "$script_root/open-release-candidates.sh")"
if [[ -n "$candidate_prs" ]]; then
  echo "prepare release: another release candidate is open:" >&2
  printf '  %s\n' "$candidate_prs" >&2
  exit 1
fi

acceptance_url=""
if [[ "$kind" == promotion ]]; then
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
fi

if [[ "$dry_run" -eq 1 ]]; then
  if [[ "$kind" == promotion ]]; then
    source_label="accepted next"
    acceptance_line="- acceptance: ${acceptance_url}"
  else
    source_label="hotfix source"
    acceptance_line="- source gate: final PR gate and App acceptance"
  fi
  cat <<EOF
Candidate preparation dry run
- version: ${version_tag}
- kind: ${kind}
- publication: ${publication}
- ${source_label}: ${source_sha}
${acceptance_line}
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
body="$work/pr-body.md"
fragment_count="$(find changelog.d -maxdepth 1 -type f -name '*.yaml' | wc -l | tr -d '[:space:]')"
fragment_noun=fragments
if [[ "$fragment_count" == 1 ]]; then
  fragment_noun=fragment
fi

if [[ "$kind" == promotion ]]; then
  echo "Creating frozen candidate ${release_branch} from ${source_sha}..."
  git switch -c "$release_branch" "$source_sha"
else
  echo "Preparing hotfix candidate ${release_branch} from ${source_sha}..."
fi

./scripts/compile-changelog.sh
go run ./cmd/release-train version set "$version_tag"
(cd app && pnpm install --frozen-lockfile)
(cd app/src-tauri && cargo check -q)
go run ./cmd/release-train version check "$version_tag"
go run ./cmd/release-train manifest write \
  --version "$version_tag" --kind "$kind" --publication "$publication" \
  --source "$source_sha" --main "$main_sha"

git add -A CHANGELOG.md changelog.d .github/release-candidate.yml \
  app/package.json app/pnpm-lock.yaml app/src-tauri/tauri.conf.json \
  app/src-tauri/Cargo.toml app/src-tauri/Cargo.lock
git commit -m "chore(release): prepare ${version_tag}"
candidate_sha="$(git rev-parse HEAD)"

echo "Rechecking the frozen baseline before publishing..."
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

candidate_args=(
  --current-main "$main_sha"
  --head "$candidate_sha"
  --tag-status absent
  --other-open-candidates 0
)
if [[ "$kind" == promotion ]]; then
  candidate_args+=(--source-acceptance success)
fi
go run ./cmd/release-train candidate validate "${candidate_args[@]}"

manifest_version="$(awk '$1 == "version:" { print $2 }' .github/release-candidate.yml)"
manifest_kind="$(awk '$1 == "kind:" { print $2 }' .github/release-candidate.yml)"
manifest_publication="$(awk '$1 == "publication:" { print $2 }' .github/release-candidate.yml)"
manifest_source="$(awk '$1 == "source_sha:" { print $2 }' .github/release-candidate.yml)"
manifest_main="$(awk '$1 == "main_sha:" { print $2 }' .github/release-candidate.yml)"

if [[ "$kind" == promotion ]]; then
  if [[ "$publication" == held ]]; then
    candidate_tldr="Freezes accepted \`next\` commit [\`${manifest_source:0:12}\`](${repo_url}/commit/${manifest_source}) as ${version_tag} for a CI-only promotion to \`main\`. Publication is held at the accepted-main boundary."
    candidate_followup="This PR carries the release metadata. Merging remains separate, and this held candidate cannot dispatch a release."
  else
    candidate_tldr="Freezes accepted \`next\` commit [\`${manifest_source:0:12}\`](${repo_url}/commit/${manifest_source}) as ${version_tag}."
    candidate_followup="This PR carries the release metadata. Merging and release dispatch remain separate gates."
  fi
  source_label="Accepted source"
  source_gate_row="| Source Acceptance | [green check](${acceptance_url}) |"
  movement_note="\`next\` may keep moving while this PR is reviewed. Those later changes are outside this frozen candidate."
  pr_title="chore(release): prepare ${version_tag}"
else
  candidate_tldr="Packages hotfix commit [\`${manifest_source:0:12}\`](${repo_url}/commit/${manifest_source}) as ${version_tag}."
  source_label="Hotfix source"
  source_gate_row="| Source gate | Final \`PR gate\` and \`App acceptance\` on this candidate |"
  movement_note="The hotfix starts from the recorded current \`main\` baseline. If \`main\` moves, close this candidate and prepare it again."
  pr_title="$(git log -1 --format=%s "$manifest_source")"
  candidate_followup="This PR carries the release metadata. Merging and release dispatch remain separate gates."
fi

cat >"$body" <<EOF
## TL;DR

${candidate_tldr} ${candidate_followup}

## Frozen candidate

| Field | Value |
| --- | --- |
| Version | \`${manifest_version}\` |
| Kind | \`${manifest_kind}\` |
| Publication | \`${manifest_publication}\` |
| ${source_label} | [\`${manifest_source}\`](${repo_url}/commit/${manifest_source}) |
${source_gate_row}
| Main baseline | [\`${manifest_main}\`](${repo_url}/commit/${manifest_main}) |
| Candidate head | [\`${candidate_sha}\`](${repo_url}/commit/${candidate_sha}) |

${movement_note}

## What changed

- compiled ${fragment_count} frozen changelog ${fragment_noun} into \`CHANGELOG.md\`
- updated every committed app version to \`${manifest_version}\`
- recorded the accepted source and main baseline in \`.github/release-candidate.yml\`

EOF

if [[ "$publication" == held ]]; then
  cat >>"$body" <<EOF
## Publication hold

This candidate does not claim packaged-app verification. Merge it after \`PR gate\`
is green. The merged \`main\` SHA will run full Acceptance, then the release
controller will stop before App acceptance, tag creation, or release dispatch.

The hold is committed with the candidate and remains in force on later \`main\`
runs. Prepare a new candidate and version for a public release.

EOF
else
  cat >>"$body" <<EOF

## Manual app verification

Run the packaged-app scenarios from this exact candidate, then attach the receipt to this SHA:

\`\`\`bash
gh workflow run app-acceptance.yml \\
  --ref main \\
  -f candidate_sha=${candidate_sha} \\
  -f profile=<profile> \\
  -f scenarios='<scenarios run>' \\
  -f evidence='<recording URL or concise receipt>' \\
  -f outcome=passed
\`\`\`

Do not merge until \`PR gate\` and \`App acceptance\` are green on \`${candidate_sha}\`.

EOF
fi

echo "Pushing ${release_branch}..."
git push -u "$remote" "$release_branch"
pr_url="$(gh pr create --draft --base main --head "$release_branch" \
  --title "$pr_title" --body-file "$body")"

echo "Opened draft candidate ${pr_url}"
if [[ "$publication" == held ]]; then
  echo "Next: review the changelog and merge after normal PR CI is green."
else
  echo "Next: review the changelog, run the packaged app, and record App acceptance."
fi
echo "This command did not merge, tag, or start a release."

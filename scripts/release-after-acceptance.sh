#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 0 ]; then
  echo "usage: release-after-acceptance.sh" >&2
  exit 2
fi
script_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
manifest=.github/release-candidate.yml

: "${GITHUB_REPOSITORY:?release after acceptance: GITHUB_REPOSITORY is required}"
for tool in gh git go; do
  command -v "$tool" >/dev/null || { echo "release after acceptance: missing $tool" >&2; exit 2; }
done

reviewed_sha="$(git rev-parse --verify 'origin/reviewed^{commit}')"
sha="$(git log -1 --first-parent --format=%H "$reviewed_sha" -- "$manifest")"
if [ -z "$sha" ]; then
  echo "release after acceptance: reviewed $reviewed_sha contains no release commit"
  exit 0
fi

head_sha="$(git rev-parse --verify 'HEAD^{commit}')"
if ! git merge-base --is-ancestor "$sha" "$head_sha"; then
  echo "release after acceptance: checkout $head_sha does not contain release commit $sha" >&2
  exit 1
fi

tag="$(go run ./cmd/release-train accepted-main tag --head "$sha")"
if ! [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "release after acceptance: manifest returned invalid tag '$tag'" >&2
  exit 1
fi

remote_tag_status=0
remote_tag_rows="$(git ls-remote --exit-code origin "refs/tags/$tag" "refs/tags/$tag^{}")" || remote_tag_status=$?
case "$remote_tag_status" in
  0)
    remote_tag_sha="$(printf '%s\n' "$remote_tag_rows" | tail -n 1 | cut -f 1)"
    if [ "$remote_tag_sha" != "$sha" ]; then
      echo "release after acceptance: $tag already points to $remote_tag_sha, not release commit $sha" >&2
      exit 1
    fi
    echo "release after acceptance: newest reviewed release commit $sha is already tagged $tag; nothing to release"
    exit 0
    ;;
  2) ;;
  *)
    echo "release after acceptance: cannot inspect remote tag $tag" >&2
    exit 1
    ;;
esac
echo "release after acceptance: newest reviewed release commit $sha is untagged $tag"

validated_tag="$(go run ./cmd/release-train accepted-main validate --head "$sha")"
if [ "$validated_tag" != "$tag" ]; then
  echo "release after acceptance: manifest tag changed from $tag to $validated_tag during validation" >&2
  exit 1
fi

ci_run_row="$({
  gh api --paginate \
    "repos/$GITHUB_REPOSITORY/actions/workflows/ci.yml/runs?event=push&per_page=100&head_sha=$sha&branch=main" \
    --jq '.workflow_runs[] | select(.head_sha == "'"$sha"'" and .event == "push") | [.created_at, .id, .head_sha, .status, (.conclusion // "none"), .html_url] | @tsv' |
    sort -t $'\t' -k1,1 | tail -n 1 | cut -f 2-
} || true)"
if [ -z "$ci_run_row" ]; then
  echo "release after acceptance: release commit $sha has no CI push run on main; refusing release" >&2
  exit 1
fi
IFS=$'\t' read -r ci_run_id _ ci_run_status _ ci_run_url <<<"$ci_run_row"
if [ "$ci_run_status" != completed ]; then
  echo "release after acceptance: CI run $ci_run_id is $ci_run_status; release commit stays untagged until it completes ($ci_run_url)"
  exit 0
fi

acceptance_rows="$({
  gh api --paginate \
    "repos/$GITHUB_REPOSITORY/actions/runs/$ci_run_id/jobs?filter=latest&per_page=100" \
    --jq '.jobs[] | select(.name == "Acceptance") | [.status, (.conclusion // ""), .html_url] | @tsv'
} || true)"
acceptance_count="$(printf '%s\n' "$acceptance_rows" | awk 'NF { count++ } END { print count + 0 }')"
if [ "$acceptance_count" -ne 1 ]; then
  echo "release after acceptance: CI run $ci_run_id has $acceptance_count Acceptance jobs; refusing release" >&2
  exit 1
fi
IFS=$'\t' read -r acceptance_status acceptance_conclusion acceptance_url <<<"$acceptance_rows"
if [ "$acceptance_status/$acceptance_conclusion" != "completed/success" ]; then
  echo "release after acceptance: Acceptance is $acceptance_status/$acceptance_conclusion; release commit stays untagged ($ci_run_url)"
  exit 0
fi

if "$script_root/workflow-job-gate.sh" ci.yml "$sha" push main 'App acceptance'; then
  echo "release after acceptance: CI App acceptance is green for main $sha"
else
  "$script_root/workflow-job-gate.sh" \
    app-acceptance.yml "$sha" workflow_dispatch main 'App acceptance'
  echo "release after acceptance: manual App acceptance receipt is green for $sha"
fi

if create_error="$({
  gh api --method POST "repos/$GITHUB_REPOSITORY/git/refs" \
    -f ref="refs/tags/$tag" -f sha="$sha" --silent
} 2>&1)"; then
  echo "release after acceptance: created $tag at release commit $sha"
else
  raced_tag_status=0
  git ls-remote --exit-code origin "refs/tags/$tag" >/dev/null 2>&1 || raced_tag_status=$?
  if [ "$raced_tag_status" -ne 0 ]; then
    echo "release after acceptance: could not create $tag: $create_error" >&2
    exit 1
  fi
  raced_tag_sha="$(gh api "repos/$GITHUB_REPOSITORY/commits/$tag" --jq .sha)"
  if [ "$raced_tag_sha" != "$sha" ]; then
    echo "release after acceptance: $tag was concurrently created at $raced_tag_sha, not release commit $sha" >&2
    exit 1
  fi
  echo "release after acceptance: $tag was concurrently created at release commit $sha"
fi

release_runs="$(
  gh api --paginate \
    "repos/$GITHUB_REPOSITORY/actions/workflows/release.yml/runs?event=repository_dispatch&per_page=100" \
    --jq ".workflow_runs[] | select(.event == \"repository_dispatch\" and .display_title == \"$tag\") | select(.status != \"completed\" or .conclusion == \"success\") | [.id, .status, (.conclusion // \"\")] | @tsv" |
    awk 'NF { count++ } END { print count + 0 }'
)"
if ! [[ "$release_runs" =~ ^[0-9]+$ ]]; then
  echo "release after acceptance: invalid authoritative release run count '$release_runs'" >&2
  exit 1
fi
if [ "$release_runs" -gt 0 ]; then
  echo "release after acceptance: release.yml already has $release_runs active or successful run(s) for $sha; not dispatching again"
  exit 0
fi

gh api --method POST "repos/$GITHUB_REPOSITORY/dispatches" \
  -f event_type=release -F "client_payload[tag]=$tag"
echo "release after acceptance: dispatched release.yml for $tag after $acceptance_url"

#!/usr/bin/env bash
set -euo pipefail

sha="${1:?usage: release-after-acceptance.sh <main-sha> <ci-run-id> <ci-run-url>}"
ci_run_id="${2:?usage: release-after-acceptance.sh <main-sha> <ci-run-id> <ci-run-url>}"
ci_run_url="${3:?usage: release-after-acceptance.sh <main-sha> <ci-run-id> <ci-run-url>}"
script_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if ! [[ "$sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "release after acceptance: invalid commit SHA '$sha'" >&2
  exit 2
fi
if ! [[ "$ci_run_id" =~ ^[0-9]+$ ]]; then
  echo "release after acceptance: invalid CI run id '$ci_run_id'" >&2
  exit 2
fi

: "${GITHUB_REPOSITORY:?release after acceptance: GITHUB_REPOSITORY is required}"
for tool in gh git go; do
  command -v "$tool" >/dev/null || { echo "release after acceptance: missing $tool" >&2; exit 2; }
done

head_sha="$(git rev-parse --verify 'HEAD^{commit}')"
if [ "$head_sha" != "$sha" ]; then
  echo "release after acceptance: checkout is $head_sha, expected $sha" >&2
  exit 1
fi

if ! remote_main_line="$(git ls-remote --exit-code origin refs/heads/main)"; then
  echo "release after acceptance: cannot resolve origin/main" >&2
  exit 1
fi
remote_main_sha="${remote_main_line%%[[:space:]]*}"
if [ "$remote_main_sha" != "$sha" ]; then
  echo "release after acceptance: main moved to $remote_main_sha; ignoring stale result for $sha"
  exit 0
fi

if [ ! -f .github/release-candidate.yml ]; then
  echo "release after acceptance: $sha has no candidate manifest; nothing to release"
  exit 0
fi

publication="$(go run ./cmd/release-train accepted-main publication --head "$sha")"
if [ "$publication" = held ]; then
  tag="$(go run ./cmd/release-train accepted-main tag --head "$sha")"
  echo "release after acceptance: publication is held for $tag at accepted main $sha; leaving main untagged"
  exit 0
fi
if [ "$publication" != automatic ]; then
  echo "release after acceptance: unsupported publication '$publication'" >&2
  exit 1
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
  echo "release after acceptance: Acceptance is $acceptance_status/$acceptance_conclusion; main stays untagged ($ci_run_url)"
  exit 0
fi

"$script_root/app-acceptance-gate.sh" "$sha"

tag="$(go run ./cmd/release-train accepted-main tag --head "$sha")"
if ! [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "release after acceptance: manifest returned invalid tag '$tag'" >&2
  exit 1
fi
validated_tag="$(go run ./cmd/release-train accepted-main validate --head "$sha")"
if [ "$validated_tag" != "$tag" ]; then
  echo "release after acceptance: manifest tag changed from $tag to $validated_tag during validation" >&2
  exit 1
fi

remote_tag_status=0
remote_tag_line="$(git ls-remote --exit-code origin "refs/tags/$tag" 2>/dev/null)" || remote_tag_status=$?
case "$remote_tag_status" in
  0)
    remote_tag_sha="$(gh api "repos/$GITHUB_REPOSITORY/commits/$tag" --jq .sha)"
    if [ "$remote_tag_sha" != "$sha" ]; then
      echo "release after acceptance: manifest $tag was consumed at $remote_tag_sha; nothing to release from $sha"
      exit 0
    fi
    echo "release after acceptance: $tag already points to accepted main $sha"
    ;;
  2)
    repository_owner="${GITHUB_REPOSITORY%%/*}"
    repository_name="${GITHUB_REPOSITORY#*/}"
    repository_id="$({
      gh api graphql \
        -f query='query($owner: String!, $name: String!) { repository(owner: $owner, name: $name) { id } }' \
        -F owner="$repository_owner" \
        -F name="$repository_name" \
        --jq '.data.repository.id'
    } || true)"
    if [ -z "$repository_id" ]; then
      echo "release after acceptance: cannot resolve repository id for $GITHUB_REPOSITORY" >&2
      exit 1
    fi

    zero_sha=0000000000000000000000000000000000000000
    atomic_error=""
    if atomic_error="$({
      gh api graphql \
        -f query='mutation($repositoryId: ID!, $mainSha: GitObjectID!, $zeroSha: GitObjectID!, $tagRef: GitRefname!) {
          updateRefs(input: {repositoryId: $repositoryId, refUpdates: [
            {name: "refs/heads/main", beforeOid: $mainSha, afterOid: $mainSha, force: false},
            {name: $tagRef, beforeOid: $zeroSha, afterOid: $mainSha, force: false}
          ]}) { clientMutationId }
        }' \
        -F repositoryId="$repository_id" \
        -F mainSha="$sha" \
        -F zeroSha="$zero_sha" \
        -F tagRef="refs/tags/$tag" \
        --silent
    } 2>&1)"; then
      echo "release after acceptance: atomically created $tag at current main $sha"
    else
      latest_main_line="$(git ls-remote --exit-code origin refs/heads/main)"
      latest_main_sha="${latest_main_line%%[[:space:]]*}"
      if [ "$latest_main_sha" != "$sha" ]; then
        echo "release after acceptance: main moved to $latest_main_sha before atomic tagging; leaving $sha untagged"
        exit 0
      fi

      raced_tag_status=0
      raced_tag_line="$(git ls-remote --exit-code origin "refs/tags/$tag" 2>/dev/null)" || raced_tag_status=$?
      if [ "$raced_tag_status" -eq 0 ]; then
        raced_tag_sha="$(gh api "repos/$GITHUB_REPOSITORY/commits/$tag" --jq .sha)"
        if [ "$raced_tag_sha" != "$sha" ]; then
          echo "release after acceptance: manifest $tag was consumed at $raced_tag_sha; nothing to release from $sha"
          exit 0
        fi
        echo "release after acceptance: $tag was concurrently created at accepted main $sha"
      else
        echo "release after acceptance: atomic main/tag update failed: $atomic_error" >&2
        exit 1
      fi
    fi
    ;;
  *)
    echo "release after acceptance: cannot inspect remote tag $tag" >&2
    exit 1
    ;;
esac

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

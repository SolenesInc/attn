#!/usr/bin/env bash
set -euo pipefail

tag="${1:?usage: release-tag-gate.sh <vX.Y.Z>}"
script_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if ! [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "release tag gate: invalid tag '$tag'" >&2
  exit 2
fi

: "${GITHUB_REPOSITORY:?release tag gate: GITHUB_REPOSITORY is required}"
for tool in gh git go; do
  command -v "$tool" >/dev/null || {
    echo "release tag gate: missing $tool" >&2
    exit 2
  }
done

root="$(git rev-parse --show-toplevel)"
cd "$root"
if ! git show-ref --verify --quiet "refs/tags/$tag"; then
  echo "release tag gate: tag $tag does not exist" >&2
  exit 1
fi

tag_sha="$(git rev-parse --verify "${tag}^{commit}")"
head_sha="$(git rev-parse --verify 'HEAD^{commit}')"
if [[ "$head_sha" != "$tag_sha" ]]; then
  echo "release tag gate: checkout is $head_sha, expected $tag at $tag_sha" >&2
  exit 1
fi
if ! git merge-base --is-ancestor "$tag_sha" origin/main; then
  echo "release tag gate: $tag_sha is not part of main" >&2
  exit 1
fi

validated_tag="$(go run ./cmd/release-train accepted-main validate --head "$tag_sha")"
if [[ "$validated_tag" != "$tag" ]]; then
  echo "release tag gate: manifest authorizes $validated_tag, not $tag" >&2
  exit 1
fi

"$script_root/app-acceptance-gate.sh" "$tag_sha"

acceptance="$({
  gh api --method GET \
    "repos/$GITHUB_REPOSITORY/commits/${tag_sha}/check-runs?check_name=Acceptance&filter=latest" \
    --jq '.check_runs | map(select(.name == "Acceptance" and .app.slug == "github-actions")) | sort_by(.started_at) | last | select(.) | [.head_sha, .status, (.conclusion // ""), .html_url] | @tsv'
} || true)"
if [[ -z "$acceptance" ]]; then
  echo "release tag gate: $tag_sha has no Acceptance check" >&2
  exit 1
fi
IFS=$'\t' read -r acceptance_sha acceptance_status acceptance_conclusion acceptance_url <<<"$acceptance"
if [[ "$acceptance_sha" != "$tag_sha" ]]; then
  echo "release tag gate: Acceptance belongs to $acceptance_sha, expected $tag_sha" >&2
  exit 1
fi
if [[ "$acceptance_status/$acceptance_conclusion" != "completed/success" ]]; then
  echo "release tag gate: Acceptance is $acceptance_status/${acceptance_conclusion:-none}" >&2
  echo "$acceptance_url" >&2
  exit 1
fi

echo "release tag gate: $tag is accepted at $tag_sha ($acceptance_url)"

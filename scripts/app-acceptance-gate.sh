#!/usr/bin/env bash
set -euo pipefail

main_sha="${1:?usage: app-acceptance-gate.sh <main-sha>}"
script_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if ! [[ "$main_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "app acceptance gate: invalid main SHA '$main_sha'" >&2
  exit 2
fi
: "${GITHUB_REPOSITORY:?app acceptance gate: GITHUB_REPOSITORY is required}"
command -v gh >/dev/null || {
  echo "app acceptance gate: missing gh" >&2
  exit 2
}

candidate_rows="$({
  gh api "repos/$GITHUB_REPOSITORY/commits/${main_sha}/pulls" \
    --jq ".[] | select(.base.ref == \"main\" and .merge_commit_sha == \"$main_sha\") | select(.head.ref | test(\"^(release/v[0-9]+\\\\.[0-9]+\\\\.[0-9]+|hotfix/.+)\$\")) | [.number, .head.ref, .html_url] | @tsv"
} || true)"
candidate_count="$(printf '%s\n' "$candidate_rows" | awk 'NF { count++ } END { print count + 0 }')"
if [[ "$candidate_count" -ne 1 ]]; then
  echo "app acceptance gate: main $main_sha has $candidate_count associated release candidate PRs" >&2
  exit 1
fi
IFS=$'\t' read -r candidate_number candidate_branch candidate_url <<<"$candidate_rows"
candidate_sha="$({
  gh api --paginate \
    "repos/$GITHUB_REPOSITORY/pulls/$candidate_number/commits?per_page=100" \
    --jq '.[].sha'
} | tail -n 1)"
if ! [[ "$candidate_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "app acceptance gate: PR #$candidate_number returned invalid merged head SHA '$candidate_sha'" >&2
  exit 1
fi

candidate_tree="$(
  gh api "repos/$GITHUB_REPOSITORY/git/commits/$candidate_sha" --jq .tree.sha
)"
main_tree="$(
  gh api "repos/$GITHUB_REPOSITORY/git/commits/$main_sha" --jq .tree.sha
)"
if ! [[ "$candidate_tree" =~ ^[0-9a-f]{40}$ && "$main_tree" =~ ^[0-9a-f]{40}$ ]]; then
  echo "app acceptance gate: GitHub returned an invalid candidate or main tree" >&2
  exit 1
fi
if [[ "$candidate_tree" != "$main_tree" ]]; then
  echo "app acceptance gate: merged main tree $main_tree differs from app-accepted candidate tree $candidate_tree" >&2
  exit 1
fi

"$script_root/workflow-job-gate.sh" \
  app-acceptance.yml "$candidate_sha" workflow_dispatch - 'App acceptance'

echo "app acceptance gate: PR #$candidate_number $candidate_branch passed at $candidate_sha ($candidate_url)"

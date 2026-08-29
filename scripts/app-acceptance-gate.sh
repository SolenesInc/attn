#!/usr/bin/env bash
set -euo pipefail

main_sha="${1:?usage: app-acceptance-gate.sh <main-sha>}"
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
    --jq ".[] | select(.base.ref == \"main\" and .merge_commit_sha == \"$main_sha\") | select(.head.ref | test(\"^(release/v[0-9]+\\\\.[0-9]+\\\\.[0-9]+|hotfix/.+)\$\")) | [.number, .head.sha, .head.ref, .html_url] | @tsv"
} || true)"
candidate_count="$(printf '%s\n' "$candidate_rows" | awk 'NF { count++ } END { print count + 0 }')"
if [[ "$candidate_count" -ne 1 ]]; then
  echo "app acceptance gate: main $main_sha has $candidate_count associated release candidate PRs" >&2
  exit 1
fi
IFS=$'\t' read -r candidate_number candidate_sha candidate_branch candidate_url <<<"$candidate_rows"
if ! [[ "$candidate_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "app acceptance gate: PR #$candidate_number returned invalid head SHA '$candidate_sha'" >&2
  exit 1
fi

receipt="$({
  gh api --method GET \
    "repos/$GITHUB_REPOSITORY/commits/${candidate_sha}/check-runs?check_name=App%20acceptance&filter=latest" \
    --jq '.check_runs | map(select(.name == "App acceptance" and .app.slug == "github-actions")) | sort_by(.started_at) | last | select(.) | [.head_sha, .status, (.conclusion // ""), .html_url] | @tsv'
} || true)"
if [[ -z "$receipt" ]]; then
  echo "app acceptance gate: $candidate_branch at $candidate_sha has no App acceptance check" >&2
  exit 1
fi
IFS=$'\t' read -r receipt_sha receipt_status receipt_conclusion receipt_url <<<"$receipt"
if [[ "$receipt_sha" != "$candidate_sha" ]]; then
  echo "app acceptance gate: receipt belongs to $receipt_sha, expected $candidate_sha" >&2
  exit 1
fi
if [[ "$receipt_status/$receipt_conclusion" != "completed/success" ]]; then
  echo "app acceptance gate: App acceptance is $receipt_status/${receipt_conclusion:-none}" >&2
  echo "$receipt_url" >&2
  exit 1
fi

echo "app acceptance gate: PR #$candidate_number $candidate_branch passed at $candidate_sha ($candidate_url; $receipt_url)"

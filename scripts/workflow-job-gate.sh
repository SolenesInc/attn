#!/usr/bin/env bash
set -euo pipefail

workflow="${1:?usage: workflow-job-gate.sh <workflow> <sha> <event> <branch|-> <job>}"
sha="${2:?workflow SHA is required}"
event="${3:?workflow event is required}"
branch="${4:?workflow branch or - is required}"
job="${5:?workflow job is required}"

case "$workflow|$event|$branch|$job" in
  'ci.yml|push|main|Acceptance'|'ci.yml|push|next|Acceptance'|\
  'app-acceptance.yml|workflow_dispatch|main|App acceptance') ;;
  *)
    echo "workflow job gate: unsupported receipt $workflow/$event/$branch/$job" >&2
    exit 2
    ;;
esac
if ! [[ "$sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "workflow job gate: invalid SHA '$sha'" >&2
  exit 2
fi
: "${GITHUB_REPOSITORY:?workflow job gate: GITHUB_REPOSITORY is required}"
command -v gh >/dev/null || {
  echo "workflow job gate: missing gh" >&2
  exit 2
}

runs_url="repos/$GITHUB_REPOSITORY/actions/workflows/$workflow/runs?event=$event&per_page=100"
run_binding="$sha"
if [[ "$workflow" == app-acceptance.yml ]]; then
  runs_url+="&branch=main"
  run_binding="App acceptance $sha"
  run_row="$({
    gh api "$runs_url" \
      --jq '.workflow_runs | map(select(.head_branch == "main" and .event == "workflow_dispatch" and .display_title == "'$run_binding'")) | sort_by(.created_at) | last | select(.) | [.id, .display_title, .status, (.conclusion // ""), .html_url] | @tsv'
  } || true)"
else
  runs_url+="&head_sha=$sha&branch=$branch"
  run_row="$({
    gh api "$runs_url" \
      --jq '.workflow_runs | map(select(.head_sha == "'$sha'" and .event == "'$event'")) | sort_by(.created_at) | last | select(.) | [.id, .head_sha, .status, (.conclusion // ""), .html_url] | @tsv'
  } || true)"
fi
if [[ -z "$run_row" ]]; then
  echo "workflow job gate: $sha has no $workflow $event run" >&2
  exit 1
fi
IFS=$'\t' read -r run_id actual_binding run_status run_conclusion run_url <<<"$run_row"
if [[ "$actual_binding" != "$run_binding" ]]; then
  echo "workflow job gate: $workflow run belongs to $actual_binding, expected $run_binding" >&2
  exit 1
fi
if [[ "$run_status/$run_conclusion" != "completed/success" ]]; then
  echo "workflow job gate: $workflow run is $run_status/${run_conclusion:-none}" >&2
  echo "$run_url" >&2
  exit 1
fi

job_rows="$({
  gh api --paginate \
    "repos/$GITHUB_REPOSITORY/actions/runs/$run_id/jobs?filter=latest&per_page=100" \
    --jq '.jobs[] | select(.name == "'$job'") | [.status, (.conclusion // ""), .html_url] | @tsv'
} || true)"
job_count="$(printf '%s\n' "$job_rows" | awk 'NF { count++ } END { print count + 0 }')"
if [[ "$job_count" -ne 1 ]]; then
  echo "workflow job gate: $workflow run $run_id has $job_count '$job' jobs" >&2
  exit 1
fi
IFS=$'\t' read -r job_status job_conclusion job_url <<<"$job_rows"
if [[ "$job_status/$job_conclusion" != "completed/success" ]]; then
  echo "workflow job gate: $job is $job_status/${job_conclusion:-none}" >&2
  echo "$job_url" >&2
  exit 1
fi

echo "workflow job gate: $workflow run $run_id and $job are green ($run_url; $job_url)"

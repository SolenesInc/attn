#!/usr/bin/env bash
set -euo pipefail

run_limit=20
# Receipt 2026-11-27: at most 4 runs of one workflow and event were in flight at once over main's last 1000 runs of ci.yml and of ci-macos.yml.
listing_limit=50
sheriff_takeover_minutes=30
found_red=""

report_failures() {
  local workflow="$1" kind="$2" runs run attempt conclusion failed_jobs
  local fields=databaseId,attempt,status,conclusion events event listing
  local row='[.databaseId, .attempt, .conclusion] | @tsv'
  local current="" passed_after=""
  if [ "$kind" = push ]; then
    events=(push)
  else
    events=(schedule workflow_dispatch)
  fi
  listing="$(
    for event in "${events[@]}"; do
      gh run list --workflow "$workflow" --branch main --event "$event" \
        --limit "$listing_limit" --json "$fields" || exit
    done
  )"
  runs="$(
    jq -rs "[add[] | select(.status == \"completed\")] | sort_by(-.databaseId)[:$run_limit][] | $row" \
      <<<"$listing"
  )"
  [ -n "$runs" ] || return 0
  while IFS=$'\t' read -r run attempt conclusion; do
    case "$conclusion" in
      success)
        current="${current:-green}"
        passed_after=1
        continue
        ;;
      failure|timed_out) ;;
      *)
        if [ "$current" = red ] && [ -z "$passed_after" ]; then
          continue
        fi
        if ! failed_jobs="$(gh api --paginate \
          "repos/{owner}/{repo}/actions/runs/$run/attempts/$attempt/jobs?per_page=100" \
          --jq '.jobs[] | select(.conclusion | IN("failure", "timed_out")) | .conclusion')"; then
          [ -n "$current" ] && continue
          return 1
        fi
        [ -n "$failed_jobs" ] || continue
        ;;
    esac
    if [ -n "$passed_after" ]; then
      printf 'recovered %s %s\n' "$run" "$attempt"
    elif [ -z "$current" ]; then
      printf '%s %s\n' "$run" "$attempt"
      current=red
      found_red=1
    fi
  done <<<"$runs"
  [ -n "$current" ] && return 0
  echo "ci-main-red: none of main's last $run_limit completed $kind $workflow runs passed or failed a job; raise run_limit in scripts/ci-main-red.sh" >&2
  return 1
}

report_failures ci.yml push
report_failures ci.yml full
report_failures ci-macos.yml push
report_failures ci-macos.yml full

if [ -n "$found_red" ]; then
  read -r hour minute < <(date -u '+%Y-%m-%dT%H %M')
  printf 'window %s:%02dZ\n' "$hour" $((10#$minute / sheriff_takeover_minutes * sheriff_takeover_minutes))
fi

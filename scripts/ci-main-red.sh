#!/usr/bin/env bash
set -euo pipefail

run_limit=20
sheriff_takeover_minutes=30
found_red=""

report_failures() {
  local workflow="$1" kind="$2" runs run attempt conclusion failed_jobs
  local fields=databaseId,attempt,conclusion
  local row='[.databaseId, .attempt, .conclusion] | @tsv'
  local current="" passed_after=""
  if [ "$kind" = push ]; then
    runs="$(
      gh run list --workflow "$workflow" --branch main --event push --status completed \
        --limit "$run_limit" --json "$fields" --jq ".[] | $row"
    )"
  else
    runs="$(
      gh run list --workflow "$workflow" --branch main --status completed \
        --limit 100 --json "$fields,event" \
        --jq "[.[] | select(.event | IN(\"schedule\", \"workflow_dispatch\"))][:$run_limit][] | $row"
    )"
  fi
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

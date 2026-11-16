#!/usr/bin/env bash
set -euo pipefail

run_limit=20

newest_red() {
  local workflow="$1" kind="$2" runs run verdict
  if [ "$kind" = push ]; then
    runs="$(
      gh run list --workflow "$workflow" --branch main --event push --status completed \
        --limit "$run_limit" --json databaseId --jq '.[].databaseId'
    )"
  else
    runs="$(
      gh run list --workflow "$workflow" --branch main --status completed \
        --limit 100 --json databaseId,event \
        --jq "[.[] | select(.event | IN(\"schedule\", \"workflow_dispatch\"))][:$run_limit][].databaseId"
    )"
  fi
  for run in $runs; do
    verdict="$(
      gh run view "$run" --json attempt,conclusion,jobs --jq '
        def failed: . == "failure" or . == "timed_out";
        if .conclusion == "success" then "green"
        elif (.conclusion | failed) or any(.jobs[]; .conclusion | failed) then "red \(.attempt)"
        else "" end'
    )"
    case "$verdict" in
      green) return 0 ;;
      red*) echo "$run ${verdict#red }"; return 0 ;;
    esac
  done
  [ -z "$runs" ] && return 0
  echo "ci-main-red: none of main's last $run_limit completed $kind $workflow runs passed or failed a job; raise run_limit in scripts/ci-main-red.sh" >&2
  return 1
}

newest_red ci.yml push
newest_red ci-macos.yml push
newest_red ci-macos.yml full

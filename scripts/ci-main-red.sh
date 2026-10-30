#!/usr/bin/env bash
set -euo pipefail

run_limit=20

runs="$(
  gh run list --workflow ci.yml --branch main --event push --status completed \
    --limit "$run_limit" --json databaseId --jq '.[].databaseId'
)"

for run in $runs; do
  verdict="$(
    gh run view "$run" --json attempt,conclusion,jobs --jq '
      def failed: . == "failure" or . == "timed_out";
      if .conclusion == "success" then "green"
      elif (.conclusion | failed) or any(.jobs[]; .conclusion | failed) then "red \(.attempt)"
      else "" end'
  )"
  case "$verdict" in
    green) exit 0 ;;
    red*) echo "$run ${verdict#red }"; exit 0 ;;
  esac
done

echo "ci-main-red: none of main's last $run_limit completed CI runs passed or failed a job; raise run_limit in scripts/ci-main-red.sh" >&2
exit 1

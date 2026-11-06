#!/usr/bin/env bash
set -euo pipefail

run_changed_scenarios() {
  usage="usage: scripts/land-scenarios.sh <base>"
  [ "$#" -eq 1 ] || { echo "$usage" >&2; exit 2; }
  base="$1"

  cd "$(git rev-parse --show-toplevel)"

  workflow=acceptance-soak.yml
  scenarios="$(git diff --name-only "$base...HEAD" | node app/scripts/real-app-harness/changed-scenarios.mjs)"
  [ -n "$scenarios" ] || exit 0

  ref="land-scenarios/$(git rev-parse --short HEAD)-$$"
  run_id=""
  cleanup() {
    if [ -n "$run_id" ] && [ "${finished:-}" != 1 ]; then gh run cancel "$run_id" >/dev/null 2>&1 || true; fi
    git push --quiet origin --delete "$ref" >/dev/null 2>&1 || true
  }
  trap cleanup EXIT
  trap 'exit 130' INT TERM

  if ! push_output="$(git push --quiet origin "HEAD:refs/heads/$ref" 2>&1)"; then
    printf '%s\n' "$push_output" >&2
    echo "land: could not push $ref to run changed scenarios $scenarios; nothing was pushed to main" >&2
    exit 1
  fi
  gh workflow run "$workflow" --ref "$ref" -f "scenarios=$scenarios" -f repeat=1 >/dev/null

  # A dispatched run was listed about 4s later on 2026-11-06.
  for _ in $(seq 60); do
    run_id="$(gh run list --workflow "$workflow" --branch "$ref" --limit 1 --json databaseId --jq '.[0].databaseId // empty')"
    [ -z "$run_id" ] || break
    sleep 2
  done
  [ -n "$run_id" ] || { echo "land: GitHub listed no $workflow run on $ref within 120s; nothing was pushed to main" >&2; exit 1; }

  url="$(gh run view "$run_id" --json url --jq .url)"
  echo "land: running changed scenarios $scenarios on CI's Linux, before the push: $url"
  if ! gh run watch "$run_id" --exit-status --interval 20 >/dev/null 2>&1; then
    finished=1
    cat >&2 <<MESSAGE
land: changed scenarios $scenarios failed on CI's Linux, so nothing was pushed to main.
land: read the failure with: gh run view $run_id --log-failed
land: evidence: $url
land: when GitHub Actions itself is down, run the scenario on Linux yourself, then git push --no-verify.
MESSAGE
    exit 1
  fi
  finished=1
  echo "land: changed scenarios $scenarios passed on CI's Linux"
}

run_changed_scenarios "$@"

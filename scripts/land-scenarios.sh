#!/usr/bin/env bash
set -euo pipefail

unrecorded_scenarios() {
  local scenario evidence
  for scenario in ${scenarios//,/ }; do
    if evidence="$(awk -v scenario="$scenario" '$1 == scenario { print $3 }' "$record" 2>/dev/null)" && [ -n "$evidence" ]; then
      echo "land: scenario $scenario passed on this diff already: $evidence" >&2
    else
      printf '%s\n' "$scenario"
    fi
  done | paste -sd , -
}

record_pass() {
  local scenario="$1" runner="$2" evidence="$3"
  mkdir -p "$(dirname "$record")"
  touch "$record"
  awk -v scenario="$scenario" '$1 != scenario' "$record" >"$record.$$"
  printf '%s %s %s\n' "$scenario" "$runner" "$evidence" >>"$record.$$"
  mv "$record.$$" "$record"
}

listed_run() {
  local run
  # A dispatched run was listed about 4s later on 2026-11-06.
  for _ in $(seq 60); do
    run="$(gh run list --workflow "$workflow" --branch "$ref" --limit 1 --json databaseId --jq '.[0].databaseId // empty')"
    [ -z "$run" ] || { echo "$run"; return; }
    sleep 2
  done
}

cleanup() {
  if [ -n "$runner_pid" ]; then kill -TERM -- "-$runner_pid" 2>/dev/null || true; fi
  if [ -n "$watch_pid" ]; then kill "$watch_pid" 2>/dev/null || true; fi
  if [ -z "$run_id" ] && [ -n "$dispatched" ]; then run_id="$(listed_run)"; fi
  if [ -n "$run_id" ] && [ "${finished:-}" != 1 ]; then gh run cancel "$run_id" >/dev/null 2>&1 || true; fi
  if [ -n "$ref" ]; then git push --quiet origin --delete "$ref" >/dev/null 2>&1 || true; fi
}

run_on_github() {
  local scenarios="$1" scenario push_output url
  workflow=acceptance-soak.yml
  ref="land-scenarios/$(git rev-parse --short HEAD)-$$"

  if ! push_output="$(git push --quiet origin "HEAD:refs/heads/$ref" 2>&1)"; then
    printf '%s\n' "$push_output" >&2
    echo "land: could not push $ref to run scenarios $scenarios; nothing was pushed to main" >&2
    exit 1
  fi
  dispatched=1
  gh workflow run "$workflow" --ref "$ref" -f "scenarios=$scenarios" -f repeat=1 >/dev/null
  run_id="$(listed_run)"
  [ -n "$run_id" ] || { echo "land: GitHub listed no $workflow run on $ref within 120s; nothing was pushed to main" >&2; exit 1; }

  url="$(gh run view "$run_id" --json url --jq .url)"
  echo "land: running scenarios $scenarios on CI's Linux, before the push: $url"
  gh run watch "$run_id" --exit-status --interval 20 >/dev/null 2>&1 &
  watch_pid=$!
  if ! wait "$watch_pid"; then
    watch_pid=""
    finished=1
    cat >&2 <<MESSAGE
land: scenarios $scenarios failed on CI's Linux, so nothing was pushed to main.
land: a landing runs each scenario it changes and, when it changes product code, each scenario a feature file it changes lists under ## Tests.
land: when the product change is right, update the scenario in this landing.
land: read the failure with: gh run view $run_id --log-failed
land: evidence: $url
land: when GitHub Actions itself is down, run the scenario on Linux yourself, then git push --no-verify.
MESSAGE
    exit 1
  fi
  watch_pid=""
  finished=1
  for scenario in ${scenarios//,/ }; do record_pass "$scenario" github "$url"; done
  echo "land: scenarios $scenarios passed on CI's Linux"
}

run_on_ssh() {
  local target="$1" scenarios="$2" logs scenario failed=""
  if ! pnpm --silent --dir app real-app:linux status --provider ssh --target "$target" >/dev/null 2>&1; then
    echo "land: the landing runner $target cannot be reached, so the scenarios run on CI's Linux instead" >&2
    run_on_github "$scenarios"
    return
  fi
  logs="$(mktemp -d "${TMPDIR:-/tmp}/attn-land-runner.XXXXXX")"
  for scenario in ${scenarios//,/ }; do
    echo "land: running scenario $scenario on $target, before the push"
    perl -e 'setpgrp; exec @ARGV' pnpm --silent --dir app real-app:linux soak --provider ssh --target "$target" \
      --checkout land --scenario "$scenario" --out "$logs/$scenario" >"$logs/$scenario.log" 2>&1 &
    runner_pid=$!
    if wait "$runner_pid"; then
      runner_pid=""
      record_pass "$scenario" "ssh:$target" "$logs/$scenario.log"
      echo "land: scenario $scenario passed on $target: $logs/$scenario.log"
    else
      runner_pid=""
      echo "land: scenario $scenario failed on $target, so it runs again on CI's Linux, and that run decides. Log: $logs/$scenario.log" >&2
      failed="${failed:+$failed,}$scenario"
    fi
  done
  if [ -n "$failed" ]; then run_on_github "$failed"; fi
}

run_landing_scenarios() {
  usage="usage: scripts/land-scenarios.sh <base>"
  [ "$#" -eq 1 ] || { echo "$usage" >&2; exit 2; }
  base="$1"

  cd "$(git rev-parse --show-toplevel)"

  scenarios="$(git diff --name-only "$base...HEAD" | node app/scripts/real-app-harness/landing-scenarios.mjs)"
  [ -n "$scenarios" ] || exit 0
  record="$(git rev-parse --git-common-dir)/attn-land-scenarios/$(git diff "$base...HEAD" | git patch-id --verbatim | cut -d' ' -f1)"
  scenarios="$(unrecorded_scenarios)"
  [ -n "$scenarios" ] || exit 0

  ref="" run_id="" dispatched="" watch_pid="" runner_pid=""
  trap cleanup EXIT
  trap 'exit 130' INT TERM

  runner="$(git config attn.landRunner || echo github)"
  case "$runner" in
    github) run_on_github "$scenarios" ;;
    ssh:?*) run_on_ssh "${runner#ssh:}" "$scenarios" ;;
    *) echo "land: git config attn.landRunner is $runner; set it to github or ssh:<target>" >&2; exit 2 ;;
  esac
}

run_landing_scenarios "$@"

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
  if [ -n "$runner_pid" ]; then
    kill -TERM -- "-$runner_pid" 2>/dev/null || true
    record_runner_passes
  fi
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
land: a landing runs each scenario it changes, each scenario that failed on main and has since passed there and, when it changes product code, each scenario a feature file it changes lists under ## Tests.
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

waited_for_runner() {
  sed -n 's/^Waited \([0-9]*\)s for the guest checkout .*/\1/p' "$1" | grep . || echo 0
}

scenario_seconds() {
  sed -n "s/^Scenario $1 $2 in \([0-9]*\)s\$/\1/p" "$runner_log" | tail -1
}

record_runner_passes() {
  local scenario
  for scenario in ${scenarios//,/ }; do
    if [ -n "$(scenario_seconds "$scenario" passed)" ]; then record_pass "$scenario" "ssh:$runner_target" "$runner_log"; fi
  done
}

run_without_runner() {
  echo "land: $1, so the scenarios run on CI's Linux instead" >&2
  run_on_github "$scenarios"
}

gave_up_on_runner() {
  sed -n 's/^Gave up after \([0-9]*\)s waiting for the guest checkout .*/\1/p' "$runner_log" | grep .
}

soaked_on_runner() {
  local turn_limit="$2"
  runner_target="$1"
  runner_log="$(mktemp -d "${TMPDIR:-/tmp}/attn-land-runner.XXXXXX")/runner.log"
  local soak=(soak --provider ssh --target "$runner_target" --checkout land --turn-limit "$turn_limit")
  soak+=(--scenario "$scenarios" --out "$(dirname "$runner_log")/artifacts")
  echo "land: running scenarios $scenarios on $runner_target, before the push: $runner_log"
  perl -e 'setpgrp; exec @ARGV' pnpm --silent --dir app real-app:linux "${soak[@]}" >"$runner_log" 2>&1 &
  runner_pid=$!
  wait "$runner_pid" || true
  runner_pid=""
  ! gave_up_on_runner >/dev/null
}

run_on_ssh() {
  local target scenario seconds outcome failed="" started=$SECONDS took="" reachable=()
  # The median landing's scenarios took 654s on CI's Linux in c4-replay.md on seed s-yg6h46 (2026-11-17).
  local turn_limit="${ATTN_LAND_RUNNER_TURN_LIMIT:-654}"
  for target in "$@"; do
    if pnpm --silent --dir app real-app:linux status --provider ssh --target "$target" >/dev/null 2>&1; then
      reachable+=("$target")
    else
      echo "land: the landing runner $target cannot be reached" >&2
    fi
  done
  if [ "${#reachable[@]}" = 0 ]; then
    run_without_runner "no landing runner can be reached"
    return
  fi
  if [ "${#reachable[@]}" -gt 1 ]; then
    for target in "${reachable[@]}"; do
      if soaked_on_runner "$target" 0; then took=1; break; fi
      echo "land: the landing runner $target is busy with another landing"
    done
  fi
  if [ -z "$took" ] && ! soaked_on_runner "${reachable[0]}" "$turn_limit"; then
    run_without_runner "waited $(gave_up_on_runner)s for a turn on the landing runner $runner_target, past its ${turn_limit}s limit"
    return
  fi
  record_runner_passes
  echo "land: the landing runner took $((SECONDS - started))s, $(waited_for_runner "$runner_log")s of it waiting for other landings"
  for scenario in ${scenarios//,/ }; do
    if seconds="$(scenario_seconds "$scenario" passed)" && [ -n "$seconds" ]; then
      echo "land: scenario $scenario passed on $runner_target in ${seconds}s"
    else
      seconds="$(scenario_seconds "$scenario" failed)"
      if [ -n "$seconds" ]; then outcome="failed on $runner_target in ${seconds}s"; else outcome="did not finish on $runner_target"; fi
      echo "land: scenario $scenario $outcome, so it runs again on CI's Linux, and that run decides. Log: $runner_log" >&2
      failed="${failed:+$failed,}$scenario"
    fi
  done
  if [ -n "$failed" ]; then run_on_github "$failed"; fi
}

run_linux_checks() {
  usage="usage: scripts/land-linux.sh <base> [<scenario that failed on main recently>...]"
  [ "$#" -ge 1 ] || { echo "$usage" >&2; exit 2; }
  base="$1"
  shift

  cd "$(git rev-parse --show-toplevel)"

  scenarios="$(git diff --name-only "$base...HEAD" | node app/scripts/real-app-harness/landing-scenarios.mjs "$@")"
  record="$(git rev-parse --git-common-dir)/attn-land-scenarios/$(git diff "$base...HEAD" | git patch-id --verbatim | cut -d' ' -f1)"
  [ -z "$scenarios" ] || scenarios="$(unrecorded_scenarios)"
  [ -n "$scenarios" ] || exit 0

  ref="" run_id="" dispatched="" watch_pid="" runner_pid=""
  trap cleanup EXIT
  trap 'exit 130' INT TERM

  runners=()
  while IFS= read -r runner; do
    case "$runner" in
      github) ;;
      ssh:?*) runners+=("${runner#ssh:}") ;;
      *) echo "land: git config attn.landRunner is $runner; set it to github or ssh:<target>, once for each landing runner" >&2; exit 2 ;;
    esac
  done < <(git config --get-all attn.landRunner || true)
  if [ "${#runners[@]}" = 0 ]; then run_without_runner "this clone has no landing runner"; else run_on_ssh "${runners[@]}"; fi
}

run_linux_checks "$@"

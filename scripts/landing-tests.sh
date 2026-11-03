#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

list=scripts/landing-tests
groups="$(awk '$1 == "group" { print $2 }' "$list")"
usage="usage: scripts/landing-tests.sh <group>... | --unit '<unit>'...; groups: $(paste -sd' ' - <<<"$groups")"
[ "$#" -gt 0 ] || { echo "$usage" >&2; exit 2; }
requested="$*"

if [ "$1" = --unit ]; then
  entries=""
  while [ "$#" -gt 0 ]; do
    [ "$1" = --unit ] && [ "$#" -ge 2 ] || { echo "$usage" >&2; exit 2; }
    read -r -a fields <<<"$2"
    case "${fields[0]}:${#fields[@]}" in
      go:3 | vitest:2 | playwright:2) entries+="${fields[*]}"$'\n' ;;
      *) echo "landing tests: $2 is not a unit; units are 'go <package> <TestName>', 'vitest <file>' or 'playwright <file>'" >&2; exit 2 ;;
    esac
    shift 2
  done
  entries="$(awk '!seen[$0]++' <<<"$entries")"
  requested="$(grep -c . <<<"$entries") units"
else
  for group in "$@"; do
    grep -qxF -- "$group" <<<"$groups" || { echo "landing tests: no group $group in $list; groups: $(paste -sd' ' - <<<"$groups")" >&2; exit 2; }
  done
  entries="$(awk -v wanted=" $* " '
    $1 == "group" { selected = index(wanted, " " $2 " ") > 0; next }
    selected && ($1 == "go" || $1 == "vitest" || $1 == "playwright") && !seen[$0]++ { print }
  ' "$list")"
fi

work="$(mktemp -d "${TMPDIR:-/tmp}/attn-landing-tests.XXXXXX")"
trap 'rm -rf "$work"' EXIT
summary="$work/summary"
: >"$summary"
: >"$work/reruns"
failed=0

summarize() {
  local receipt="$1" status="$2" lines="$work/lines" unit
  shift 2
  if [ -f "$receipt" ]; then
    go tool test-history summary "$receipt" >"$lines" || status=1
  else
    for unit in "$@"; do echo "MISSING $unit - the run sealed no receipt"; done >"$lines"
    status=1
  fi
  cat "$lines" >>"$summary"
  awk '$1 != "PASS" && $1 != "SKIP"' "$lines" >"$work/unsettled"
  [ "$status" -eq 0 ] && [ ! -s "$work/unsettled" ]
}

# Go replays a test's cached pass while its test binary, its flags, and the
# environment variables and files it read are unchanged, so a rebase reruns only
# the tests its commits reach. These tests also depend on something Go cannot
# see, so they run every time: a ClickHouse server, or attn built during the test
# at another version.
uncached_go_units="go ./internal/daemon TestEachUploadBatchCarriesTheBuildThatRecordedItsRecords
go ./internal/daemon TestSharingUploadsTheOutboxToAttnIngestUntilTheUserForgetsTheInstall
go ./internal/daemon TestForgettingWhileAttnIngestIsUnreachableChangesNothing
go ./internal/daemon TestTurningSharingOffDuringTheFirstRegistrationSendsNothing
go ./internal/daemon TestRecordsMadeAfterClearingDuringAnUploadAreStillSent
go ./cmd/attn-ingest TestAnInstallSendsRetriesHitsTheMinuteLimitAndIsDeleted
go ./cmd/attn-ingest TestOneInstallStoresAtMost128MiBADayAndOthersAreUnaffected
go ./cmd/attn-ingest TestAllInstallsTogetherStoreAtMostTheConfiguredBytesADay
go ./cmd/attn-ingest TestOneAddressRegistersAtMostTwentyInstallsAnHour
go ./cmd/attn-ingest TestOversizedRequestsAreRefusedNamingTheLimit
go ./cmd/attn-ingest TestAQuarantinedInstallIsRefusedWithTheReason
go ./cmd/attn-ingest TestTheSameBatchSentConcurrentlyIsStoredOnce
go ./cmd/attn-ingest TestABatchSpanningThousandsOfStreamsStoresOnlyTheNewOnes"

run_go_units() {
  local name="$1" go_entries="$2" status=0 package unit
  shift 2
  local packages=() units=() unit_options=() run_regex
  while IFS= read -r package; do packages+=("$package"); done < <(awk '!seen[$2]++ { print $2 }' <<<"$go_entries")
  while IFS= read -r unit; do units+=("$unit"); done <<<"$go_entries"
  for unit in "${units[@]}"; do unit_options+=(--unit "$unit"); done
  run_regex="^($(awk '!seen[$3]++ { print $3 }' <<<"$go_entries" | paste -sd'|' -))\$"
  echo "landing tests: go test${*:+ $*} -run '$run_regex' ${packages[*]}"
  ATTN_TEST_REQUIRE_CLICKHOUSE=1 ./scripts/test-go.sh --packages "${unit_options[@]}" --receipt-out "$work/$name.json" \
    "$@" -run "$run_regex" "${packages[@]}" >"$work/$name.log" 2>&1 || status=$?
  if ! summarize "$work/$name.json" "$status" "${units[@]}"; then
    cat "$work/$name.log" >&2
    failed=1
    awk '$1 != "PASS" && $1 != "SKIP" { print "rerun: ./scripts/test-go.sh --packages -count=1 -run '\''^" $4 "$'\'' " $3 }' "$work/lines" >>"$work/reruns"
  fi
}

go_entries="$(awk '$1 == "go"' <<<"$entries")"
cached_go_entries="$(grep -vxF -- "$uncached_go_units" <<<"$go_entries" || true)"
uncached_go_entries="$(grep -xF -- "$uncached_go_units" <<<"$go_entries" || true)"
if [ -n "$cached_go_entries" ]; then run_go_units go "$cached_go_entries"; fi
if [ -n "$uncached_go_entries" ]; then run_go_units go-uncached "$uncached_go_entries" -count=1; fi

app_entries="$(awk '$1 == "vitest" || $1 == "playwright"' <<<"$entries")"
if [ -n "$app_entries" ]; then
  make -s app/node_modules/.attn-installed
  mkdir -p "$work/data"
  read -r daemon_port vite_port < <(node -e '
    const net = require("net");
    const listen = () => new Promise(done => { const s = net.createServer().listen(0, "127.0.0.1", () => done(s)); });
    Promise.all([listen(), listen()]).then(servers => { console.log(servers.map(s => s.address().port).join(" ")); servers.forEach(s => s.close()); });')
  run_app_unit() {
    local run="$1" runner="$2" file="$3" status=0 command
    case "$runner" in
      vitest) command=(pnpm --dir app exec vitest run "$file") ;;
      playwright) command=(pnpm --dir app exec playwright test "$file") ;;
    esac
    echo "landing tests: ${command[*]}"
    env -u ATTN_DB_PATH -u ATTN_SOCKET_PATH -u ATTN_CONFIG_PATH -u ATTN_PLUGIN_DIR \
      ATTN_INSTANCE= ATTN_DATA_DIR="$work/data" ATTN_E2E_DAEMON_PORT="$daemon_port" ATTN_E2E_VITE_PORT="$vite_port" \
      go tool test-history capture "$runner" --unit "$runner $file" --receipt-out "$work/app-$run.json" -- "${command[@]}" || status=$?
    echo "$status" >"$work/app-$run.status"
  }
  run_app_units() {
    local runner="$1" run runner_of file
    while read -r run runner_of file; do
      if [ "$runner_of" = "$runner" ]; then run_app_unit "$run" "$runner_of" "$file"; fi
    done <"$work/app-units"
  }
  awk '{ print NR, $1, $2 }' <<<"$app_entries" >"$work/app-units"
  run_app_units vitest >"$work/vitest.log" 2>&1 &
  vitest_job=$!
  run_app_units playwright
  wait "$vitest_job"
  cat "$work/vitest.log"
  while read -r run runner file; do
    status="$(cat "$work/app-$run.status")"
    if ! summarize "$work/app-$run.json" "$status" "$runner $file"; then
      failed=1
      case "$runner" in
        vitest) echo "rerun: pnpm --dir app exec vitest run $file" >>"$work/reruns" ;;
        playwright) echo "rerun: pnpm --dir app exec playwright test $file" >>"$work/reruns" ;;
      esac
    fi
  done <"$work/app-units"
fi

echo
echo "landing tests ($requested):"
sed 's/^/  /' "$summary"
if [ "$failed" -ne 0 ]; then
  sed 's/^/  /' "$work/reruns" >&2
  echo "landing tests failed" >&2
  exit 1
fi

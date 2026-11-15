#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

list=scripts/landing-tests
groups="$(awk '$1 == "group" { print $2 }' "$list")"
usage="usage: scripts/landing-tests.sh [--no-go-vet] <group>... | --unit '<unit>'... [--red 'vitest <file>']...; groups: $(paste -sd' ' - <<<"$groups")"
cargo_unit_shape="'cargo .', 'cargo <package> --lib', 'cargo <package> --doc' or 'cargo <package> --test|--bin <name>'"
go_vet_options=()
if [ "${1:-}" = --no-go-vet ]; then
  go_vet_options=(-vet=off)
  shift
fi
[ "$#" -gt 0 ] || { echo "$usage" >&2; exit 2; }
requested="$*"

red_vitest_files=()
if [ "$1" = --unit ]; then
  entries=""
  while [ "$#" -gt 0 ]; do
    if [ "$1" = --red ] && [ "$#" -ge 2 ]; then
      red_vitest_files+=("${2#vitest }")
      shift 2
      continue
    fi
    [ "$1" = --unit ] && [ "$#" -ge 2 ] || { echo "$usage" >&2; exit 2; }
    read -r -a fields <<<"$2"
    case "${fields[0]}:${#fields[@]}:${fields[2]-${fields[1]-}}" in
      go:3:* | vitest:2:* | playwright:2:* | cargo:2:. | cargo:3:--lib | cargo:3:--doc | cargo:4:--test | cargo:4:--bin) entries+="${fields[*]}"$'\n' ;;
      *) echo "landing tests: $2 is not a unit; units are 'go <package> <TestName>', 'vitest <file>', 'playwright <file>', $cargo_unit_shape" >&2; exit 2 ;;
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
    selected && ($1 == "go" || $1 == "vitest" || $1 == "playwright" || $1 == "cargo") && !seen[$0]++ { print }
  ' "$list")"
fi

vitest_suite="vitest ."
if grep -qxF -- "$vitest_suite" <<<"$entries"; then
  entries="$(awk -v suite="$vitest_suite" '$1 != "vitest" || $0 == suite' <<<"$entries")"
fi
cargo_suite="cargo ."
if grep -qxF -- "$cargo_suite" <<<"$entries"; then
  entries="$(awk -v suite="$cargo_suite" '$1 != "cargo" || $0 == suite' <<<"$entries")"
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
  awk '$1 != "PASS" && $1 != "SKIP"' "$lines" >"$work/unsettled"
  if [ "$*" = "$vitest_suite" ] || [ "$*" = "$cargo_suite" ]; then
    cat "$work/unsettled" >>"$summary"
    passed="$(awk '$1 == "PASS"' "$lines" | wc -l | tr -d ' ')"
    if [ "$passed" -eq 0 ]; then
      echo "MISSING $* - the run reported nothing that passed" | tee -a "$summary" >>"$work/unsettled"
    elif [ "$*" = "$vitest_suite" ]; then
      echo "PASS $vitest_suite: $passed files" >>"$summary"
    else
      echo "PASS $cargo_suite: $passed test targets" >>"$summary"
    fi
  else
    cat "$lines" >>"$summary"
  fi
  [ "$status" -eq 0 ] && [ ! -s "$work/unsettled" ]
}

# Go replays a test's cached pass while its test binary, its flags, and the
# environment variables and files it read are unchanged, so a rebase reruns only
# the tests its commits reach. These tests also depend on something Go cannot
# see, a ClickHouse server, so they run every time.
uncached_go_units="go ./internal/daemon TestSharingUploadsTheOutboxToAttnIngestUntilTheUserForgetsTheInstall
go ./cmd/attn-ingest TestAnInstallSendsRetriesHitsTheMinuteLimitAndIsDeleted
go ./cmd/attn-ingest TestOneInstallStoresAtMost128MiBADayAndOthersAreUnaffected
go ./cmd/attn-ingest TestAllInstallsTogetherStoreAtMostTheConfiguredBytesADay
go ./cmd/attn-ingest TestOneAddressRegistersAtMostTwentyInstallsAnHour
go ./cmd/attn-ingest TestOversizedRequestsAreRefusedNamingTheLimit
go ./cmd/attn-ingest TestAQuarantinedInstallIsRefusedWithTheReason
go ./cmd/attn-ingest TestTheSameBatchSentConcurrentlyIsStoredOnce
go ./cmd/attn-ingest TestABatchSpanningThousandsOfStreamsStoresOnlyTheNewOnes
go ./cmd/attn-ingest TestEachRecordKindReadsBackAsTypedColumnsThroughItsView"

# ./cmd/attnd's stack tests wait on real processes, one after another in one test
# binary: 20s of a go test step that used 19 CPU-s (2026-11-04). Each shard links
# its own binary, under 1.9 CPU-s. Five keep today's two slowest tests apart.
# Go shares no vet in flight. Vetting in the first shard alone, which has tests
# whenever any shard does, cost a prompt-only land 7 CPU-s; in all five, 16
# (2026-11-05).
stack_test_package=./cmd/attnd
stack_test_shards=5

run_go_units() {
  local name="$1" go_entries="$2" status=0 package unit
  shift 2
  local packages=() unit_options=() run_regex vet_options=(${go_vet_options[@]+"${go_vet_options[@]}"})
  case "$name" in go-stack-[!0]*) vet_options=(-vet=off) ;; esac
  while IFS= read -r package; do packages+=("$package"); done < <(awk '!seen[$2]++ { print $2 }' <<<"$go_entries")
  while IFS= read -r unit; do unit_options+=(--unit "$unit"); done <<<"$go_entries"
  run_regex="^($(awk '!seen[$3]++ { print $3 }' <<<"$go_entries" | paste -sd'|' -))\$"
  echo "landing tests: go test${*:+ $*} -run '$run_regex' ${packages[*]}"
  ATTN_TEST_REQUIRE_CLICKHOUSE=1 ./scripts/test-go.sh --packages "${unit_options[@]}" --receipt-out "$work/$name.json" \
    ${vet_options[@]+"${vet_options[@]}"} "$@" -run "$run_regex" "${packages[@]}" >"$work/$name.log" 2>&1 || status=$?
  echo "$status" >"$work/$name.status"
}

settle_go_units() {
  local name="$1" go_entries="$2" unit units=()
  while IFS= read -r unit; do units+=("$unit"); done <<<"$go_entries"
  if ! summarize "$work/$name.json" "$(cat "$work/$name.status")" "${units[@]}"; then
    cat "$work/$name.log" >&2
    failed=1
    awk '$1 != "PASS" && $1 != "SKIP" { print "rerun: ./scripts/test-go.sh --packages -count=1 -run '\''^" $4 "$'\'' " $3 }' "$work/lines" >>"$work/reruns"
  fi
}

go_entries="$(awk '$1 == "go"' <<<"$entries")"
cached_go_entries="$(grep -vxF -- "$uncached_go_units" <<<"$go_entries" || true)"
uncached_go_entries="$(grep -xF -- "$uncached_go_units" <<<"$go_entries" || true)"
go_run_names=()
add_go_run() {
  if [ -n "$2" ]; then
    go_run_names+=("$1")
    printf '%s\n' "$2" >"$work/$1.units"
  fi
}
add_go_run go "$(awk '$2 != "'"$stack_test_package"'"' <<<"$cached_go_entries")"
for ((shard = 0; shard < stack_test_shards; shard++)); do
  add_go_run "go-stack-$shard" "$(awk -v shards="$stack_test_shards" -v shard="$shard" '$2 == "'"$stack_test_package"'" && seen++ % shards == shard' <<<"$cached_go_entries")"
done
run_all_go_units() {
  local name
  for name in ${go_run_names[@]+"${go_run_names[@]}"}; do run_go_units "$name" "$(cat "$work/$name.units")" & done
  wait
  if [ -n "$uncached_go_entries" ]; then run_go_units go-uncached "$uncached_go_entries" -count=1; fi
}
settle_all_go_units() {
  local name
  for name in ${go_run_names[@]+"${go_run_names[@]}"}; do settle_go_units "$name" "$(cat "$work/$name.units")"; done
  if [ -n "$uncached_go_entries" ]; then settle_go_units go-uncached "$uncached_go_entries"; fi
}

cargo_entries="$(awk '$1 == "cargo"' <<<"$entries")"
awk '$1 == "cargo" { print NR, $0 }' <<<"$entries" >"$work/cargo-units"
run_cargo_units() {
  local run unit fields args unit_options status
  while read -r run unit; do
    read -r -a fields <<<"$unit"
    args=(--workspace)
    unit_options=()
    if [ "$unit" != "$cargo_suite" ]; then
      args=(-p "${fields[@]:1}")
      unit_options=(--unit "$unit")
    fi
    echo "landing tests: cargo test ${args[*]} (in native/)"
    status=0
    (cd native && go tool test-history capture cargo ${unit_options[@]+"${unit_options[@]}"} --receipt-out "$work/cargo-$run.json" -- --no-fail-fast "${args[@]}") >"$work/cargo-$run.log" 2>&1 || status=$?
    echo "$status" >"$work/cargo-$run.status"
  done <"$work/cargo-units"
}
settle_cargo_units() {
  local run unit
  while read -r run unit; do
    if ! summarize "$work/cargo-$run.json" "$(cat "$work/cargo-$run.status")" "$unit"; then
      failed=1
      cat "$work/cargo-$run.log" >&2
      awk '$1 != "PASS" && $1 != "SKIP" {
        args = "-p " $3 " " $4
        if ($4 == "--test" || $4 == "--bin") args = args " " $5
        print "rerun: (cd native && cargo test " args ")"
      }' "$work/lines" >>"$work/reruns"
    fi
  done <"$work/cargo-units"
}
if [ -n "$cargo_entries" ]; then
  run_cargo_units >"$work/cargo.log" 2>&1 &
fi

if [ -n "$go_entries" ]; then
  ./scripts/test-go.sh --provide-test-binaries "$work/test-binaries" $(awk '!seen[$2]++ { print $2 }' <<<"$go_entries")
  { read -r ATTN_E2E_BIN; read -r ATTN_FAKEAGENT_BIN; read -r ATTN_TEST_PTY_HOST; } <"$work/test-binaries"
  export ATTN_E2E_BIN ATTN_FAKEAGENT_BIN ATTN_TEST_PTY_HOST
fi

app_entries="$(awk '$1 == "vitest" || $1 == "playwright"' <<<"$entries")"
if [ -z "$app_entries" ]; then
  run_all_go_units
  settle_all_go_units
else
  make -s app/node_modules/.attn-installed
  mkdir -p "$work/data"
  read -r daemon_port vite_port < <(node -e '
    const net = require("net");
    const listen = () => new Promise(done => { const s = net.createServer().listen(0, "127.0.0.1", () => done(s)); });
    Promise.all([listen(), listen()]).then(servers => { console.log(servers.map(s => s.address().port).join(" ")); servers.forEach(s => s.close()); });')
  run_app_unit() {
    local run="$1" runner="$2" file="$3" status=0 command unit_options=(--unit "$runner $file")
    case "$runner" in
      vitest) command=(pnpm --dir app exec vitest run "$file") ;;
      playwright) command=(pnpm --dir app exec playwright test "$file") ;;
    esac
    if [ "$runner $file" = "$vitest_suite" ]; then
      unit_options=()
      command+=(--silent=passed-only)
    fi
    echo "landing tests: ${command[*]}"
    env -u ATTN_DB_PATH -u ATTN_SOCKET_PATH -u ATTN_CONFIG_PATH -u ATTN_PLUGIN_DIR \
      ATTN_VITEST_FILES_RED_ON_MAIN="${red_vitest_files[*]-}" ATTN_INSTANCE= ATTN_DATA_DIR="$work/data" ATTN_E2E_DAEMON_PORT="$daemon_port" ATTN_E2E_VITE_PORT="$vite_port" \
      go tool test-history capture "$runner" ${unit_options[@]+"${unit_options[@]}"} --receipt-out "$work/app-$run.json" -- "${command[@]}" || status=$?
    echo "$status" >"$work/app-$run.status"
  }
  run_app_units() {
    local runner="$1" run runner_of file
    while read -r run runner_of file; do
      if [ "$runner_of" = "$runner" ]; then run_app_unit "$run" "$runner_of" "$file"; fi
    done <"$work/app-units"
  }
  awk '{ print NR, $1, $2 }' <<<"$app_entries" >"$work/app-units"
  run_all_go_units &
  go_job=$!
  run_app_units vitest >"$work/vitest.log" 2>&1 &
  vitest_job=$!
  run_app_units playwright
  wait "$vitest_job"
  wait "$go_job"
  settle_all_go_units
  vitest_failed=0
  while read -r run runner file; do
    status="$(cat "$work/app-$run.status")"
    if ! summarize "$work/app-$run.json" "$status" "$runner $file"; then
      failed=1
      case "$runner" in
        vitest)
          vitest_failed=1
          awk '$1 != "PASS" && $1 != "SKIP" { print "rerun: pnpm --dir app exec vitest run " $3 }' "$work/lines" >>"$work/reruns"
          ;;
        playwright) echo "rerun: pnpm --dir app exec playwright test $file" >>"$work/reruns" ;;
      esac
    fi
  done <"$work/app-units"
  if [ "$vitest_failed" -eq 1 ]; then cat "$work/vitest.log" >&2; fi
fi

if [ -n "$cargo_entries" ]; then
  wait
  cat "$work/cargo.log"
  settle_cargo_units
fi

echo
echo "landing tests ($requested):"
sed 's/^/  /' "$summary"
if [ "$failed" -ne 0 ]; then
  sed 's/^/  /' "$work/reruns" >&2
  echo "landing tests failed" >&2
  exit 1
fi

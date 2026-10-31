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

go_entries="$(awk '$1 == "go"' <<<"$entries")"
if [ -n "$go_entries" ]; then
  packages=()
  while IFS= read -r package; do packages+=("$package"); done < <(awk '!seen[$2]++ { print $2 }' <<<"$go_entries")
  units=()
  while IFS= read -r unit; do units+=("$unit"); done <<<"$go_entries"
  unit_options=()
  for unit in "${units[@]}"; do unit_options+=(--unit "$unit"); done
  run_regex="^($(awk '!seen[$3]++ { print $3 }' <<<"$go_entries" | paste -sd'|' -))\$"
  echo "landing tests: go test -run '$run_regex' ${packages[*]}"
  go_status=0
  ATTN_TEST_REQUIRE_CLICKHOUSE=1 ./scripts/test-go.sh --packages "${unit_options[@]}" --receipt-out "$work/go.json" \
    -count=1 -run "$run_regex" "${packages[@]}" >"$work/go.log" 2>&1 || go_status=$?
  if ! summarize "$work/go.json" "$go_status" "${units[@]}"; then
    cat "$work/go.log" >&2
    failed=1
    awk '$1 != "PASS" && $1 != "SKIP" { print "rerun: ./scripts/test-go.sh --packages -count=1 -run '\''^" $4 "$'\'' " $3 }' "$work/lines" >>"$work/reruns"
  fi
fi

app_entries="$(awk '$1 == "vitest" || $1 == "playwright"' <<<"$entries")"
if [ -n "$app_entries" ]; then
  make -s app/node_modules/.attn-installed
  pnpm --dir app run --silent verify:ghostty-vt
  mkdir -p "$work/data"
  run=0
  while read -r runner file; do
    case "$runner" in
      vitest) command=(pnpm --dir app exec vitest run "$file") ;;
      playwright) command=(pnpm --dir app exec playwright test "$file") ;;
    esac
    echo "landing tests: ${command[*]}"
    run=$((run + 1))
    status=0
    env -u ATTN_DB_PATH -u ATTN_SOCKET_PATH -u ATTN_CONFIG_PATH -u ATTN_PLUGIN_DIR \
      ATTN_INSTANCE= ATTN_DATA_DIR="$work/data" \
      go tool test-history capture "$runner" --unit "$runner $file" --receipt-out "$work/app-$run.json" -- "${command[@]}" || status=$?
    if ! summarize "$work/app-$run.json" "$status" "$runner $file"; then
      failed=1
      echo "rerun: ${command[*]}" >>"$work/reruns"
    fi
  done <<<"$app_entries"
fi

echo
echo "landing tests ($requested):"
sed 's/^/  /' "$summary"
if [ "$failed" -ne 0 ]; then
  sed 's/^/  /' "$work/reruns" >&2
  echo "landing tests failed" >&2
  exit 1
fi

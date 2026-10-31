#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

list=scripts/landing-tests
groups="$(awk '$1 == "group" { print $2 }' "$list")"
[ "$#" -gt 0 ] || { echo "usage: scripts/landing-tests.sh <group>...; groups: $(paste -sd' ' - <<<"$groups")" >&2; exit 2; }
for group in "$@"; do
  grep -qxF -- "$group" <<<"$groups" || { echo "landing tests: no group $group in $list; groups: $(paste -sd' ' - <<<"$groups")" >&2; exit 2; }
done

entries="$(awk -v wanted=" $* " '
  $1 == "group" { selected = index(wanted, " " $2 " ") > 0; next }
  selected && ($1 == "go" || $1 == "vitest" || $1 == "playwright") && !seen[$0]++ { print }
' "$list")"

work="$(mktemp -d "${TMPDIR:-/tmp}/attn-landing-tests.XXXXXX")"
trap 'rm -rf "$work"' EXIT
summary="$work/summary"
: >"$summary"
: >"$work/reruns"
failed=0

go_entries="$(awk '$1 == "go"' <<<"$entries")"
if [ -n "$go_entries" ]; then
  packages=()
  while IFS= read -r package; do packages+=("$package"); done < <(awk '!seen[$2]++ { print $2 }' <<<"$go_entries")
  run_regex="^($(awk '!seen[$3]++ { print $3 }' <<<"$go_entries" | paste -sd'|' -))\$"
  module="$(awk '$1 == "module" { print $2; exit }' go.mod)"
  echo "landing tests: go test -run '$run_regex' ${packages[*]}"
  go_status=0
  ./scripts/test-go.sh --packages -count=1 -v -run "$run_regex" "${packages[@]}" >"$work/go.log" 2>&1 || go_status=$?
  awk -v module="$module" '
    /^    / { said = $0; sub(/^ +/, "", said); next }
    /^--- (PASS|FAIL|SKIP): / { n++; result[n] = substr($2, 1, 4) " PACKAGE " $3 " " $4; if ($2 == "SKIP:") result[n] = result[n] " " said }
    /^=== / { said = "" }
    /^(ok|FAIL)[ \t]/ && NF >= 2 {
      package = $2; sub("^" module "/", "./", package)
      for (i = 1; i <= n; i++) { line = result[i]; sub(/PACKAGE/, package, line); print line }
      n = 0
    }
  ' "$work/go.log" >"$work/go.results"
  while read -r _ package name; do
    awk -v package="$package" -v name="$name" '$2 == package && $3 == name { found = 1 } END { exit !found }' "$work/go.results" ||
      echo "MISSING $package $name - did not report a result: renamed, removed, or its package failed first" >>"$work/go.results"
  done <<<"$go_entries"
  cat "$work/go.results" >>"$summary"
  if [ "$go_status" -ne 0 ] || grep -qE '^(FAIL|MISSING) ' "$work/go.results"; then
    cat "$work/go.log" >&2
    failed=1
    awk '$1 == "FAIL" || $1 == "MISSING" { print "rerun: ./scripts/test-go.sh --packages -count=1 -run '\''^" $3 "$'\'' " $2 }' "$work/go.results" >>"$work/reruns"
  fi
fi

app_entries="$(awk '$1 == "vitest" || $1 == "playwright"' <<<"$entries")"
if [ -n "$app_entries" ]; then
  make -s app/node_modules/.attn-installed
  pnpm --dir app run --silent verify:ghostty-vt
  mkdir -p "$work/data"
  while read -r runner file; do
    case "$runner" in
      vitest) command=(pnpm --dir app exec vitest run "$file") ;;
      playwright) command=(pnpm --dir app exec playwright test "$file") ;;
    esac
    echo "landing tests: ${command[*]}"
    started="$(date +%s)"
    if env -u ATTN_DB_PATH -u ATTN_SOCKET_PATH -u ATTN_CONFIG_PATH -u ATTN_PLUGIN_DIR \
      ATTN_INSTANCE= ATTN_DATA_DIR="$work/data" "${command[@]}"; then
      status=PASS
    else
      status=FAIL
      failed=1
      echo "rerun: ${command[*]}" >>"$work/reruns"
    fi
    echo "$status app/$file $runner ($(($(date +%s) - started))s)" >>"$summary"
  done <<<"$app_entries"
fi

echo
echo "landing tests ($*):"
sed 's/^/  /' "$summary"
if [ "$failed" -ne 0 ]; then
  sed 's/^/  /' "$work/reruns" >&2
  echo "landing tests failed" >&2
  exit 1
fi

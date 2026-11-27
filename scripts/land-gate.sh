#!/usr/bin/env bash
set -euo pipefail

usage="usage: scripts/land-gate.sh [--baseline]"
landing_tests=selected
[ "${ATTN_LAND_BASELINE:-}" = 1 ] && landing_tests=baseline
case "$*" in
  "") ;;
  --baseline) landing_tests=baseline ;;
  *) echo "$usage" >&2; exit 2 ;;
esac

cd "$(git rev-parse --show-toplevel)"

CGO_CFLAGS="$(cat go-cgo-cflags)"
export CGO_CFLAGS
# GOGC=400 cut staticcheck on internal/daemon from 19.4 to 15.3 CPU-s and added
# about 1 GiB to its peak of 1.2 GiB (2026-11-04).
export GOGC="${GOGC:-400}"

base="${LAND_BASE:-origin/main}"
native_paths='^native/|^internal/protocol/schema/|^cli/protocol/'

script_test_words() {
  local files=("$1") seen=$'\n' i=0 file word path
  while [ "$i" -lt "${#files[@]}" ]; do
    file="${files[$i]}"
    i=$((i + 1))
    grep -oE '(^|[^A-Za-z0-9_])root/[A-Za-z0-9_./-]+' "$file" | sed -E 's#^.?root/#root/#; s#/+$##' || true
    for word in $(grep -oE '[A-Za-z0-9_.-]+' "$file" | sort -u); do
      echo "$word"
      for path in "scripts/$word" "scripts/lib/$word"; do
        case "$path" in *_test.sh) continue ;; esac
        [ -f "$path" ] || continue
        case "$seen" in *$'\n'"$path"$'\n'*) continue ;; esac
        seen+="$path"$'\n'
        files+=("$path")
      done
    done
  done
}

script_test_covers() {
  local file="$1" words="$2" name path
  case "$file" in
    scripts/*)
      name="${file#scripts/}"
      name="${name#lib/}"
      grep -qxF -- "${name%%/*}" <<<"$words"
      ;;
    *)
      path="$file"
      while :; do
        grep -qxF -- "root/$path" <<<"$words" && return 0
        case "$path" in */*) path="${path%/*}" ;; *) return 1 ;; esac
      done
      ;;
  esac
}

script_tests_for() {
  local script_test file words covered_by covered=$'\n' uncovered="" all=() selected=()
  compgen -G 'scripts/*_test.sh' >/dev/null || return 0
  for script_test in scripts/*_test.sh; do
    all+=("$script_test")
    words="$(script_test_words "$script_test")"
    covered_by=""
    while IFS= read -r file; do
      if [ "$file" = "$script_test" ] || script_test_covers "$file" "$words"; then
        covered_by+=" $file"
        covered+="$file"$'\n'
      fi
    done <<<"$1"
    if [ -n "$covered_by" ]; then
      selected+=("$script_test")
      echo "land: script test $script_test, for$covered_by" >&2
    fi
  done
  while IFS= read -r file; do
    case "$file" in scripts/*) ;; *) continue ;; esac
    case "$covered" in *$'\n'"$file"$'\n'*) ;; *) uncovered+=" $file" ;; esac
  done <<<"$1"
  if [ -n "$uncovered" ]; then
    echo "land: no script test names$uncovered; running every script test" >&2
    selected=("${all[@]}")
  fi
  if [ "${#selected[@]}" -gt 0 ]; then echo "${selected[*]}"; fi
}

touches() { grep -qE "$1" <<<"$changed"; }

choose_landing_groups() {
  local group pattern
  landing_groups=()
  while read -r _ group pattern; do
    if touches "$pattern"; then landing_groups+=("$group"); fi
  done < <(awk '$1 == "group"' scripts/landing-tests)
}

native_clippy_scope() {
  local crates
  if grep -qE '^native/[^/]+$' <<<"$1"; then
    echo --workspace
    return
  fi
  crates="$(grep -oE '^native/crates/[^/]+/' <<<"$1" | sort -u | while IFS= read -r dir; do
    [ -f "$dir/Cargo.toml" ] && printf -- '-p %s ' "$(basename "$dir")"
  done)"
  echo "${crates% }"
}

landing_test_packages() {
  local package
  awk -v wanted=" ${landing_groups[*]-} " '
    $1 == "group" { selected = index(wanted, " " $2 " ") > 0; next }
    selected && $1 == "go" && !seen[$2]++ { print $2 }
  ' scripts/landing-tests | while IFS= read -r package; do
    if [ -d "$package" ]; then printf '%s ' "$package"; fi
  done
}

choose_landing_tests() {
  local group group_options=()
  [ "$landing_tests" = selected ] && [ "${#landing_groups[@]}" -gt 0 ] || return 0
  for group in "${landing_groups[@]}"; do group_options+=(--group "$group"); done
  go tool test-history land --units-out "$1" --main "$base" "${group_options[@]}" || : >"$1"
}

scenarios_failed_on_main() {
  sed -n 's/^native //p' "$1"
}

landing_tests_vetting_go_unless_check_go_runs() {
  if [ "${#go_checks[@]}" -gt 0 ]; then
    scripts/landing-tests.sh --no-go-vet "$@"
  else
    scripts/landing-tests.sh "$@"
  fi
}

run_landing_tests() {
  local units_file="$1" unit units=()
  shift
  if [ "$landing_tests" = baseline ]; then
    echo "land: running the baseline landing tests (ATTN_LAND_BASELINE=1)"
    landing_tests_vetting_go_unless_check_go_runs "$@"
    return
  fi
  while IFS= read -r unit || [ -n "$unit" ]; do
    case "$unit" in
      "") ;;
      "native "*) ;;
      *) units+=(--unit "$unit") ;;
    esac
  done <"$units_file"
  if [ "${#units[@]}" -eq 0 ]; then
    echo "land: choosing landing tests failed; running the baseline groups $*" >&2
    landing_tests_vetting_go_unless_check_go_runs "$@"
    return
  fi
  landing_tests_vetting_go_unless_check_go_runs "${units[@]}"
}

run_go_checks_beside_landing_tests() {
  local go_checks_log go_checks_job status=0
  if [ "${#go_checks[@]}" -gt 0 ]; then
    make -s compile-go "GO_PACKAGES=$packages $(landing_test_packages)" || return
    go_checks_log="$(mktemp "${TMPDIR:-/tmp}/attn-land-go-checks.XXXXXX")"
    make "${go_checks[@]}" >"$go_checks_log" 2>&1 &
    go_checks_job=$!
  fi
  if [ "${#landing_groups[@]}" -gt 0 ]; then
    run_landing_tests "$units_file" "${landing_groups[@]}" || status=$?
  fi
  if [ "${#go_checks[@]}" -gt 0 ]; then
    wait "$go_checks_job" || status=$?
    cat "$go_checks_log"
    rm -f "$go_checks_log"
  fi
  return "$status"
}

run_checks() {
  local file dir script_tests packages="" targets=() go_checks=()
  scripts/check-cloud-relay-privacy
  if touches '\.go$|^go\.(mod|sum)$'; then
    packages='./...'
    if ! touches '^go\.(mod|sum)$'; then
      packages="$(while IFS= read -r file; do
        case "$file" in
          (*.go)
            dir="$(dirname "$file")"
            if compgen -G "$dir/*.go" >/dev/null; then
              printf './%s\n' "$dir"
            fi
            ;;
        esac
      done <<<"$changed" | sort -u | paste -sd ' ' -)"
      packages="${packages:-./...}"
    fi
    go_checks=(check-go "GO_PACKAGES=$packages")
  fi
  touches '^internal/protocol/(schema/|constants\.go)|^internal/(setting|garden|prompts|ask|inbox|harness)/|^cmd/(settingsgen|cligen)/|^internal/catalog/|^cli/|^scripts/(generate-rust-protocol\.mjs|harnessdoc/)|^docs/guide/reference/harnesses\.md$|^go\.mod$' && targets+=(check-types)
  touches '^internal/protocol/(schema/home_gateway\.tsp|homegateway/)' && targets+=(check-home-gateway-compat)
  touches '^internal/prompts/|^cmd/promptgen/' && targets+=(check-prompts)
  touches '^scripts/harnesslint/' && targets+=(lint-harness)
  touches '^(pty-host|native|cli|mobile)/' && targets+=(lint-harness-apps)
  touches '^cli/' && targets+=(check-cli)
  touches '^app/' && targets+=(check-harness)
  touches '^app/|^sdk/|^cmd/prompt-editor/(web|test)/|^lint/|^\.oxlintrc\.json$' && targets+=(lint-js)
  touches '^native/browser-runtime/|^native/crates/attn-app/src/browser/runtime\.js$' && targets+=(check-browser-runtime)
  touches "$native_paths" && targets+=(check-native "NATIVE_CLIPPY=$(native_clippy_scope "$changed")")
  touches '^mobile/|^internal/cloudlink/testdata/' && targets+=(check-mobile)
  script_tests="$(script_tests_for "$changed")"
  if [ -n "$script_tests" ]; then
    targets+=(test-scripts "SCRIPT_TESTS=$script_tests")
  elif touches '^scripts/'; then
    echo "land: the checkout has no script tests"
  fi
  if [ "${#targets[@]}" -gt 0 ]; then
    make "${targets[@]}"
  fi
  run_go_checks_beside_landing_tests
}

sync_history() {
  [ "$landing_tests" = selected ] || return 0
  go tool test-history sync || echo "land: $1" >&2
}

forget_old_scenario_passes() {
  local records
  records="$(git rev-parse --git-common-dir)/attn-land-scenarios"
  [ ! -d "$records" ] || find "$records" -type f -mtime +7 -delete 2>/dev/null || true
}

scenario_step=""
stop_scenario_step() {
  [ -n "$scenario_step" ] || return 0
  kill -TERM "$scenario_step" 2>/dev/null || return 0
  wait "$scenario_step" 2>/dev/null || true
  echo "land: stopped the scenario step, because the checks failed or the land was interrupted" >&2
}

changed="$(git diff --name-only "$base...HEAD")"
choose_landing_groups
units_file="$(mktemp "${TMPDIR:-/tmp}/attn-land-units.XXXXXX")"
trap 'stop_scenario_step; rm -f "$units_file"' EXIT
trap 'exit 130' INT TERM

choose_landing_tests "$units_file"
scenarios=()
while IFS= read -r scenario || [ -n "$scenario" ]; do scenarios+=("$scenario"); done < <(scenarios_failed_on_main "$units_file")
scripts/land-linux.sh "$base" ${scenarios[@]+"${scenarios[@]}"} &
scenario_step=$!
run_checks
step="$scenario_step"
scenario_step=""
wait "$step"

sync_history "this landing's test receipts upload at the next sync"
forget_old_scenario_passes

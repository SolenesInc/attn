#!/usr/bin/env bash
set -euo pipefail

usage="usage: scripts/land.sh [--baseline]"
landing_tests=selected
case "$*" in
  "") ;;
  --baseline) landing_tests=baseline ;;
  *) echo "$usage" >&2; exit 2 ;;
esac

cd "$(git rev-parse --show-toplevel)"

max_attempts=5
protocol_outputs=(
  internal/protocol/generated.go
  internal/protocol/protocol_version.go
  app/src/types/generated.ts
  app/src/types/protocolVersion.ts
  internal/protocol/gateway/generated.go
  internal/protocol/gateway/schema.json
  internal/protocol/gateway/version.go
  app/src/types/gatewayGenerated.ts
  app/src/types/gatewayVersion.ts
)
prompt_outputs=(
  internal/prompts/catalog.generated.json
  app/src/prompts/catalog.generated.json
)

fail() {
  echo "land: $*" >&2
  exit 1
}

generator_for() {
  local output
  for output in "${protocol_outputs[@]}"; do
    [ "$1" = "$output" ] && { echo generate-types; return; }
  done
  for output in "${prompt_outputs[@]}"; do
    [ "$1" = "$output" ] && { echo generate-prompts; return; }
  done
  return 1
}

rebasing() {
  [ -e "$(git rev-parse --git-path rebase-merge)" ] || [ -e "$(git rev-parse --git-path rebase-apply)" ]
}

conflicted_files() {
  git diff --name-only --diff-filter=U
}

generators_for_conflicts() {
  local file
  while IFS= read -r file; do
    generator_for "$file" || return 1
  done < <(conflicted_files) | sort -u
}

regenerate() {
  local file generator
  while IFS= read -r file; do
    git checkout --theirs -- "$file"
  done < <(conflicted_files)
  for generator in "$@"; do
    make "$generator" || { git rebase --abort; fail "make $generator failed while rebasing"; }
    case "$generator" in
      generate-types) git add -- "${protocol_outputs[@]}" ;;
      generate-prompts) git add -- "${prompt_outputs[@]}" ;;
    esac
  done
}

rebase_onto_main() {
  local generators conflicted generator
  local -a generator_list
  git rebase --quiet origin/main >/dev/null 2>&1 || true
  while rebasing; do
    if ! generators="$(generators_for_conflicts)" || [ -z "$generators" ]; then
      conflicted="$(conflicted_files)"
      git rebase --abort
      [ -n "$conflicted" ] || fail "rebasing onto origin/main stopped; run git rebase origin/main to see why"
      fail "rebasing onto origin/main conflicts in:
$conflicted
Rebase yourself with git rebase origin/main, then run scripts/land.sh again."
    fi
    generator_list=()
    while IFS= read -r generator; do
      generator_list+=("$generator")
    done <<<"$generators"
    regenerate "${generator_list[@]}"
    GIT_EDITOR=true git rebase --continue >/dev/null 2>&1 || true
  done
  git merge-base --is-ancestor origin/main HEAD || fail "could not rebase onto origin/main; run git rebase origin/main to see why"
}

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

run_checks() {
  local changed file dir group pattern script_tests packages="" targets=() go_checks=() landing_groups=()
  changed="$(git diff --name-only origin/main...HEAD)"
  touches() { grep -qE "$1" <<<"$changed"; }
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
  touches '^internal/protocol/schema/' && targets+=(check-types)
  touches '^internal/protocol/(schema/gateway\.tsp|gateway/)' && targets+=(check-gateway-compat)
  touches '^internal/prompts/|^cmd/promptgen/' && targets+=(check-prompts)
  touches '^app/' && targets+=(check-frontend)
  touches '^mobile/|^app/src/gateway/|^app/src/types/gateway|^internal/cloudlink/testdata/' && targets+=(check-mobile)
  script_tests="$(script_tests_for "$changed")"
  if [ -n "$script_tests" ]; then
    targets+=(test-scripts "SCRIPT_TESTS=$script_tests")
  elif touches '^scripts/'; then
    echo "land: the checkout has no script tests"
  fi
  while read -r _ group pattern; do
    touches "$pattern" && landing_groups+=("$group")
  done < <(awk '$1 == "group"' scripts/landing-tests)
  if [ "${#targets[@]}" -gt 0 ]; then
    make "${targets[@]}"
  fi
  run_go_checks_beside_landing_tests
}

run_go_checks_beside_landing_tests() {
  local go_checks_log go_checks_job choosing_job status=0 units_file
  units_file="$(mktemp "${TMPDIR:-/tmp}/attn-land-units.XXXXXX")"
  if [ "${#landing_groups[@]}" -gt 0 ]; then
    choose_landing_tests "$units_file" &
    choosing_job=$!
  fi
  if [ "${#go_checks[@]}" -gt 0 ]; then
    make -s compile-go "GO_PACKAGES=$packages $(landing_test_packages)" || status=$?
    if [ "$status" -ne 0 ]; then
      wait
      rm -f "$units_file"
      return "$status"
    fi
    go_checks_log="$(mktemp "${TMPDIR:-/tmp}/attn-land-go-checks.XXXXXX")"
    make "${go_checks[@]}" >"$go_checks_log" 2>&1 &
    go_checks_job=$!
  fi
  if [ "${#landing_groups[@]}" -gt 0 ]; then
    wait "$choosing_job"
    run_landing_tests "$units_file" "${landing_groups[@]}" || status=$?
  fi
  rm -f "$units_file"
  if [ "${#go_checks[@]}" -gt 0 ]; then
    wait "$go_checks_job" || status=$?
    cat "$go_checks_log"
    rm -f "$go_checks_log"
  fi
  return "$status"
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
  [ "$landing_tests" = selected ] || return 0
  for group in "${landing_groups[@]}"; do group_options+=(--group "$group"); done
  go tool test-history land --units-out "$1" "${group_options[@]}" || : >"$1"
}

run_landing_tests() {
  local units_file="$1" unit units=()
  shift
  if [ "$landing_tests" = baseline ]; then
    echo "land: running the baseline landing tests (--baseline)"
    scripts/landing-tests.sh "$@"
    return
  fi
  while IFS= read -r unit || [ -n "$unit" ]; do
    if [ -n "$unit" ]; then units+=(--unit "$unit"); fi
  done <"$units_file"
  if [ "${#units[@]}" -eq 0 ]; then
    echo "land: choosing landing tests failed; running the baseline groups $*" >&2
    scripts/landing-tests.sh "$@"
    return
  fi
  scripts/landing-tests.sh "${units[@]}"
}

git diff --quiet && git diff --cached --quiet || fail "commit or stash your changes first"
rebasing && fail "finish the rebase in progress first"
sync_history() {
  [ "$landing_tests" = selected ] || return 0
  go tool test-history sync || echo "land: $1" >&2
}

lock_land_queue() {
  perl -MFcntl=:flock -e 'open(my $queue, ">&=9") or die "land: $!\n"; flock($queue, LOCK_EX | ($ARGV[0] ? LOCK_NB : 0)) or exit 1' "$1"
}

take_land_queue() {
  local queue holder
  queue="$(git rev-parse --git-common-dir)/attn-land-queue"
  exec 9>>"$queue"
  if ! lock_land_queue nonblocking; then
    holder="$(cat "$queue")"
    echo "land: waiting for the land queue: held by ${holder:-a land that is starting}" >&2
    lock_land_queue ""
    echo "land: took the land queue" >&2
  fi
  echo "pid $$ ($(basename "$PWD")) since $(date +%H:%M:%S)" >"$queue"
}

land() {
  for _ in $(seq "$max_attempts"); do
    git fetch --quiet origin main
    [ -n "$(git rev-list origin/main..HEAD)" ] || { echo "land: nothing to land"; exit 0; }
    rebase_onto_main
    run_checks
    if ATTN_LANDING=1 git push --quiet origin HEAD:main; then
      echo "land: main is at $(git rev-parse --short HEAD)"
      sync_history "this landing's test receipts upload at the next sync"
      exit 0
    fi
    git fetch --quiet origin main
    git merge-base --is-ancestor origin/main HEAD && fail "git push failed, and not because main moved"
    echo "land: main moved; rebasing again" >&2
  done
  fail "main moved $max_attempts times while landing; run scripts/land.sh again"
}

# GOGC=400 cut staticcheck on internal/daemon from 19.4 to 15.3 CPU-s and added
# about 1 GiB to its peak of 1.2 GiB (2026-11-04).
export GOGC="${GOGC:-400}"
take_land_queue
land 9>&-

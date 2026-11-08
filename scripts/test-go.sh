#!/usr/bin/env bash

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$root/scripts/lib/test-git.sh"
daemon_package="github.com/victorarias/attn/internal/daemon"
cli_package="github.com/victorarias/attn/cmd/attnd"
shard_count="${ATTN_GO_TEST_SHARDS:-4}"
cli_shard_count=6
package_parallelism="${ATTN_GO_TEST_PACKAGE_PARALLELISM:-4}"
test_gomaxprocs="${ATTN_GO_TEST_GOMAXPROCS:-${GOMAXPROCS:-3}}"
# -p 4 packages at GOMAXPROCS=3 on a 4-vCPU runner, and Go's -timeout counts
# the starvation. 1200s is a tripwire, also for --packages runs, past a 143.3s healthy pass (2026-09-01).
test_timeout="${ATTN_GO_TEST_TIMEOUT:-1200s}"

if ! [[ "$shard_count" =~ ^[1-9][0-9]*$ ]]; then
  echo "ATTN_GO_TEST_SHARDS must be a positive integer, got: $shard_count" >&2
  exit 2
fi
if ! [[ "$package_parallelism" =~ ^[1-9][0-9]*$ ]]; then
  echo "ATTN_GO_TEST_PACKAGE_PARALLELISM must be a positive integer, got: $package_parallelism" >&2
  exit 2
fi
if ! [[ "$test_gomaxprocs" =~ ^[1-9][0-9]*$ ]]; then
  echo "ATTN_GO_TEST_GOMAXPROCS must be a positive integer, got: $test_gomaxprocs" >&2
  exit 2
fi
export GOMAXPROCS="$test_gomaxprocs"
# GOGC=400 cut the Go suite from 756 to 716 CPU-s. The worst CI job group from a
# cold build cache peaks at 3.03 GiB of its 16 GB runner, up from 2.55 (2026-11-04).
export GOGC="${GOGC:-400}"

go_bin="$(command -v go)"
GOFLAGS="$("$go_bin" env GOFLAGS) -trimpath -ldflags=-w"
export GOFLAGS
CGO_CFLAGS="$(cat "$root/go-cgo-cflags")"
export CGO_CFLAGS
test_root="$(mktemp -d "${TMPDIR:-/tmp}/attn-go-test.XXXXXX")"
# shellcheck disable=SC2329 # invoked indirectly by trap
cleanup() {
  rm -rf "$test_root"
}
trap cleanup EXIT

# Scope the entire test process tree away from production ~/.attn. Package
# TestMain functions add their own isolation, but the runner must also protect
# packages and subprocesses before any package-level setup executes. Keep the
# outer path stable so Go can reuse successful test results; TestMain still
# replaces it with a fresh per-process directory whenever tests actually run.
runner_hash="$(cd "$root" && shasum -a 256 scripts/test-go.sh scripts/lib/test-git.sh | shasum -a 256 | awk '{ print $1 }')"
cache_namespace="$(printf '%s\n%s\n' "$($go_bin env GOVERSION)" "$runner_hash" | shasum -a 256 | awk '{ print $1 }')"
test_cache_root="${TMPDIR:-/tmp}/attn-go-test-cache/$cache_namespace"
# Each kept attn binary is 57 MB, and each fake agent less, one per build a run tested: 48 of them filled
# 2.7 GB of a 16 GB tmpfs /tmp in a morning (2026-11-03), so they live beside
# Go's build cache on disk. A binary no run touched for 60 minutes is removed:
# 3x the 1200s test timeout, so no healthy run loses its own.
e2e_store="$(dirname "$("$go_bin" env GOCACHE)")/attn/go-test-e2e"
e2e_binary_idle_minutes=60
mkdir -p "$test_cache_root/data"
export ATTN_DATA_DIR="$test_cache_root/data"
# ATTN_INSTANCE goes too: an explicit data dir with an instance name that resolves
# elsewhere is the contradiction config.ValidateInstanceRouting refuses, and a
# test run must not inherit which instance the developer's shell happens to hold.
unset ATTN_DB_PATH ATTN_SOCKET_PATH ATTN_CONFIG_PATH ATTN_PLUGIN_DIR ATTN_INSTANCE

# Tests should exercise Git itself, not an environment-specific command wrapper.
test_git="$(resolve_test_git)"
test_git_hash="$(shasum -a 256 "$test_git" | awk '{ print $1 }')"
test_git_key="$(printf '%s\n%s\n' "$test_git" "$test_git_hash" | shasum -a 256 | awk '{ print $1 }')"
test_bin_dir="$test_cache_root/git/$test_git_key"
mkdir -p "$test_bin_dir"
if [ ! -e "$test_bin_dir/git" ]; then
  ln -s "$test_git" "$test_bin_dir/git" 2>/dev/null || true
fi
if [ ! -L "$test_bin_dir/git" ] || [ "$(readlink "$test_bin_dir/git")" != "$test_git" ]; then
  echo "cached test Git does not point to $test_git: $test_bin_dir/git" >&2
  exit 2
fi
export PATH="$test_bin_dir:$PATH"

if [ "$("$go_bin" env CGO_ENABLED)" = 1 ]; then
  case "$("$go_bin" env GOOS)_$("$go_bin" env GOARCH)" in
    darwin_arm64|linux_amd64|linux_arm64) "$root/scripts/build-libghostty-vt.sh" ;;
  esac
fi

# Process-level tests need the real attn executable, and test worlds install the
# small fake agent as every fake harness. Build each once for the whole run
# instead of independently inside each test process, and normalize an explicitly
# supplied binary the same way. Tests read the paths from ATTN_E2E_BIN and
# ATTN_FAKEAGENT_BIN, so Go's test cache keys on them: a content-addressed path
# changes whenever the executable changes, and a cached result never hides a new
# binary built from source outside the test's own package. Without a VCS stamp
# the executable, and so the path, stays the same across commits that leave its
# source unchanged.
snapshot_format="$(bash "$root/scripts/snapshot-format.sh")"

start_test_binary_build() {
  local env_var="$1" name="$2" package="$3" source_bin="${!1:-}"
  case "$source_bin" in "$e2e_store"/*) return 0 ;; esac
  if [ -z "$source_bin" ]; then
    "$go_bin" -C "$root" build -buildvcs=false -ldflags "-w -X github.com/victorarias/attn/internal/buildinfo.SnapshotFormat=$snapshot_format" -o "$test_root/$name" "$package" &
    test_binary_builds+=("$!")
  elif [ ! -x "$source_bin" ]; then
    echo "$env_var is not executable: $source_bin" >&2
    exit 2
  fi
}

start_attn_binaries_build() {
  case "${ATTN_E2E_BIN:-}" in "$e2e_store"/*) return 0 ;; esac
  if [ -n "${ATTN_E2E_BIN:-}" ]; then
    if [ ! -x "$ATTN_E2E_BIN" ]; then
      echo "ATTN_E2E_BIN is not executable: $ATTN_E2E_BIN" >&2
      exit 2
    fi
    return 0
  fi
  "$go_bin" -C "$root" build -buildvcs=false -ldflags "-w -X github.com/victorarias/attn/internal/buildinfo.SnapshotFormat=$snapshot_format" -o "$test_root/attnd" ./cmd/attnd &
  test_binary_builds+=("$!")
  bash "$root/scripts/rust-cli.sh" build --out "$test_root/attn" &
  test_binary_builds+=("$!")
}

start_pty_host_build() {
  case "${ATTN_TEST_PTY_HOST:-}" in "$e2e_store"/*) return 0 ;; esac
  if [ -n "${ATTN_TEST_PTY_HOST:-}" ]; then
    if [ ! -x "$ATTN_TEST_PTY_HOST" ]; then
      echo "ATTN_TEST_PTY_HOST is not executable: $ATTN_TEST_PTY_HOST" >&2
      exit 2
    fi
    return 0
  fi
  make -C "$root" --no-print-directory build-pty-host >"$test_root/pty-host-build.log" 2>&1 &
  test_binary_builds+=("$!")
  ATTN_TEST_PTY_HOST="$root/pty-host/target/release/attn-pty-host"
}

place_in_store() {
  local source_bin="$1" cache_dir="$2" name="$3" hash cached_hash
  hash="$(shasum -a 256 "$source_bin" | awk '{ print $1 }')"
  cached_hash=""
  if [ -f "$cache_dir/$name" ]; then
    cached_hash="$(shasum -a 256 "$cache_dir/$name" | awk '{ print $1 }')"
  fi
  if [ "$cached_hash" != "$hash" ]; then
    cp "$source_bin" "$cache_dir/$name.$$.tmp"
    chmod +x "$cache_dir/$name.$$.tmp"
    mv -f "$cache_dir/$name.$$.tmp" "$cache_dir/$name"
  fi
}

store_test_binary() {
  local env_var="$1" name="$2" source_bin="${!1:-$test_root/$2}" sibling="" hash cache_dir
  case "$source_bin" in "$e2e_store"/*) return 0 ;; esac
  if [ "$name" = attn ] && [ -x "$(dirname "$source_bin")/attnd" ]; then
    sibling="$(dirname "$source_bin")/attnd"
  fi
  hash="$(cat "$source_bin" ${sibling:+"$sibling"} | shasum -a 256 | awk '{ print $1 }')"
  cache_dir="$e2e_store/$hash"
  mkdir -p "$cache_dir"
  touch "$cache_dir"
  find "$e2e_store" -mindepth 1 -maxdepth 1 -type d -mmin "+$e2e_binary_idle_minutes" -exec rm -rf {} +
  [ -z "$sibling" ] || place_in_store "$sibling" "$cache_dir" attnd
  place_in_store "$source_bin" "$cache_dir" "$name"
  printf -v "$env_var" '%s' "$cache_dir/$name"
  export "${env_var?}"
}

provide_test_binaries() {
  local arg build_pid compile_ahead=() test_binary_builds=()
  while [ "$1" != -- ]; do
    compile_ahead+=("$1")
    shift
  done
  shift
  for arg in "$@"; do
    if [ "$arg" = "-short" ] || [ "$arg" = "-short=true" ]; then
      return 0
    fi
  done
  start_attn_binaries_build
  start_test_binary_build ATTN_FAKEAGENT_BIN fakeagent ./scripts/fakeagent
  start_pty_host_build
  if [ "${#test_binary_builds[@]}" -gt 0 ] && [ "${#compile_ahead[@]}" -gt 0 ]; then
    "$go_bin" list -test -deps -export "${compile_ahead[@]}" >/dev/null 2>&1 || true
  fi
  for build_pid in ${test_binary_builds[@]+"${test_binary_builds[@]}"}; do
    wait "$build_pid" || {
      [ ! -f "$test_root/pty-host-build.log" ] || cat "$test_root/pty-host-build.log" >&2
      exit 1
    }
  done
  store_test_binary ATTN_E2E_BIN attn
  store_test_binary ATTN_FAKEAGENT_BIN fakeagent
  store_test_binary ATTN_TEST_PTY_HOST attn-pty-host
}

if [ "${1:-}" = "--provide-test-binaries" ]; then
  test_binaries_out="$2"
  shift 2
  unset GOMAXPROCS
  provide_test_binaries "$@" --
  printf '%s\n%s\n%s\n' "$ATTN_E2E_BIN" "$ATTN_FAKEAGENT_BIN" "$ATTN_TEST_PTY_HOST" >"$test_binaries_out"
  exit 0
fi

if [ "${1:-}" = "--packages" ]; then
  shift
  capture_options=()
  while [ "$#" -gt 1 ] && { [ "$1" = --unit ] || [ "$1" = --receipt-out ]; }; do
    capture_options+=("$1" "$2")
    shift 2
  done
  unit_packages=()
  for ((i = 0; i < ${#capture_options[@]}; i += 2)); do
    if [ "${capture_options[$i]}" = --unit ]; then
      read -r _ package _ <<<"${capture_options[$((i + 1))]}"
      unit_packages+=("$package")
    fi
  done
  provide_test_binaries ${unit_packages[@]+"${unit_packages[@]}"} -- "$@"
  status=0
  "$go_bin" tool test-history capture go ${capture_options[@]+"${capture_options[@]}"} -- -timeout "$test_timeout" "$@" || status=$?
  exit "$status"
fi

selected_jobs=()
while [ "${1:-}" = "--job" ]; do
  if [ "$#" -lt 2 ]; then
    echo "--job needs a job name" >&2
    exit 2
  fi
  selected_jobs+=("$2")
  shift 2
done

job_names=(packages)
for ((shard = 1; shard <= shard_count; shard++)); do job_names+=("daemon-$shard"); done
for ((shard = 1; shard <= cli_shard_count; shard++)); do job_names+=("attn-$shard"); done
for job in ${selected_jobs[@]+"${selected_jobs[@]}"}; do
  if ! printf '%s\n' "${job_names[@]}" | grep -qxF -- "$job"; then
    echo "unknown --job $job; the jobs are: ${job_names[*]}" >&2
    exit 2
  fi
done

job_selected() {
  local job
  [ "${#selected_jobs[@]}" -eq 0 ] && return 0
  for job in "${selected_jobs[@]}"; do
    [ "$job" = "$1" ] && return 0
  done
  return 1
}

any_job_selected() {
  local prefix="$1" count="$2" shard
  for ((shard = 1; shard <= count; shard++)); do
    job_selected "$prefix-$shard" && return 0
  done
  return 1
}

provide_test_binaries -- "$@"

list_tests() {
  local package_dir="$1" tests_file="$2"
  "$go_bin" test -list '^Test' "$package_dir" | awk '/^Test/ { print }' >"$tests_file"
  if [ ! -s "$tests_file" ]; then
    echo "no tests discovered in $package_dir" >&2
    exit 1
  fi
}

pids=()
names=()
logs=()

run_job() {
  local name="$1"
  shift
  job_selected "$name" || return 0
  local log="$test_root/$name.log"
  (cd "$root" && "$go_bin" tool test-history capture go --shard "$name" -- "$@") >"$log" 2>&1 &
  pids+=("$!")
  names+=("$name")
  logs+=("$log")
}

if job_selected packages; then
  packages_file="$test_root/packages"
  "$go_bin" list ./... | awk -v daemon="$daemon_package" -v cli="$cli_package" '$0 != daemon && $0 != cli' >"$packages_file"
  packages=()
  while IFS= read -r package; do
    packages+=("$package")
  done <"$packages_file"
  run_job packages -timeout "$test_timeout" -p "$package_parallelism" "$@" "${packages[@]}"
fi

run_shards() {
  local name_prefix="$1" count="$2" package_dir="$3"
  shift 3
  any_job_selected "$name_prefix" "$count" || return 0
  local tests_file="$test_root/$name_prefix-tests" shard=0 regex
  list_tests "$package_dir" "$tests_file"
  while [ "$shard" -lt "$count" ]; do
    regex="$(awk -v count="$count" -v target="$shard" '
      (NR - 1) % count == target {
        if (tests != "") tests = tests "|"
        tests = tests $0
      }
      END { print "^(" tests ")$" }
    ' "$tests_file")"
    run_job "$name_prefix-$((shard + 1))" -timeout "$test_timeout" "$@" -run "$regex" "$package_dir"
    shard=$((shard + 1))
  done
}

run_shards daemon "$shard_count" ./internal/daemon "$@"
run_shards attn "$cli_shard_count" ./cmd/attnd "$@"

failed=0
index=0
while [ "$index" -lt "${#pids[@]}" ]; do
  if wait "${pids[$index]}"; then
    cat "${logs[$index]}"
  else
    cat "${logs[$index]}" >&2
    echo "Go test job failed: ${names[$index]}" >&2
    failed=1
  fi
  index=$((index + 1))
done

exit "$failed"

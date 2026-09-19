#!/usr/bin/env bash
set -uo pipefail

if [ "$#" -eq 0 ]; then
  echo "usage: $0 TEST_SCRIPT..." >&2
  exit 2
fi

logs="$(mktemp -d "${TMPDIR:-/tmp}/attn-script-tests.XXXXXX")"
trap 'rm -rf "$logs"' EXIT

log_for() {
  printf '%s/%s.log' "$logs" "$(printf '%s' "$1" | tr '/' '_')"
}

pids=()
for script in "$@"; do
  (
    SECONDS=0
    if bash "$script" </dev/null >"$(log_for "$script")" 2>&1; then
      printf 'ok    %-50s %4ds\n' "$script" "$SECONDS"
    else
      printf 'FAIL  %-50s %4ds\n' "$script" "$SECONDS"
      exit 1
    fi
  ) &
  pids+=("$!")
done

failed=0
index=0
for script in "$@"; do
  if ! wait "${pids[$index]}"; then
    echo "--- $script" >&2
    cat "$(log_for "$script")" >&2
    failed=1
  fi
  index=$((index + 1))
done

exit "$failed"

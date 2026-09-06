#!/usr/bin/env bash
#
# Run a command, retrying a bounded number of times with doubling backoff, for a
# CI step that reaches a third-party host the job is not testing.
#
# Usage:
#   scripts/ci-retry.sh [--attempts N] [--delay SECONDS] -- command [args...]

set -euo pipefail

# 3 attempts over 15s + 30s. Measured against a 36-75s install step, so a
# genuinely dead host costs 45s of sleeping rather than an unbounded wait.
attempts=3
delay=15

while [ $# -gt 0 ]; do
  case "$1" in
    --attempts) attempts="$2"; shift 2 ;;
    --delay) delay="$2"; shift 2 ;;
    --) shift; break ;;
    -h|--help) sed -n '2,7p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "ci-retry: unknown argument: $1" >&2; exit 2 ;;
  esac
done

case "$attempts" in
  ''|*[!0-9]*) echo "ci-retry: --attempts wants a whole number, got '$attempts'" >&2; exit 2 ;;
esac
[ "$attempts" -ge 1 ] || { echo "ci-retry: --attempts must be at least 1, got $attempts" >&2; exit 2; }
case "$delay" in
  ''|*[!0-9]*) echo "ci-retry: --delay wants a whole number of seconds, got '$delay'" >&2; exit 2 ;;
esac
[ $# -gt 0 ] || { echo "ci-retry: nothing to run (expected -- command [args...])" >&2; exit 2; }

wait_for="$delay"
attempt=1
while : ; do
  status=0
  "$@" || status=$?
  [ "$status" -eq 0 ] && exit 0

  if [ "$attempt" -ge "$attempts" ]; then
    echo "ci-retry: '$*' failed on all $attempts attempts (last exit $status); giving up" >&2
    exit "$status"
  fi

  echo "ci-retry: attempt $attempt of $attempts of '$*' exited $status; retrying in ${wait_for}s" >&2
  sleep "$wait_for"
  wait_for=$((wait_for * 2))
  attempt=$((attempt + 1))
done

#!/usr/bin/env bash
#
# Run a command, retrying a bounded number of times with doubling backoff.
#
# For CI steps that reach a third-party host and have nothing to do with what
# the job is testing: a package index, a key server, an archive mirror. One bad
# minute there otherwise reads as a failure of the job itself.
#
# Bounded on purpose. An unbounded retry against a gateway timeout turns a fast
# red into a slow one, and hides a host that is genuinely down.
#
# Usage:
#   scripts/ci-retry.sh [--attempts N] [--delay SECONDS] -- command [args...]
#
# Every attempt and the final give-up name the command, the attempt count, the
# limit, and the exit status, so a log reader never has to guess which of the
# two happened.

set -euo pipefail

# 3 attempts over 15s + 30s of backoff. Receipt: the App acceptance install step
# it guards takes 36-75s (six green next runs, 2026-09-06), so the worst case
# adds 45s of sleeping to a step that costs a 17-minute job when it fails.
attempts=3
delay=15

while [ $# -gt 0 ]; do
  case "$1" in
    --attempts) attempts="$2"; shift 2 ;;
    --delay) delay="$2"; shift 2 ;;
    --) shift; break ;;
    -h|--help) sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
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

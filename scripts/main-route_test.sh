#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
route="$root/scripts/main-route.sh"

expect_success() {
  if ! "$route" "$@" >/dev/null; then
    echo "expected route to pass: $*" >&2
    exit 1
  fi
}

expect_failure() {
  if "$route" "$@" >/dev/null 2>&1; then
    echo "expected route to fail: $*" >&2
    exit 1
  fi
}

expect_success push
expect_success pull_request next feature/queue-fix
expect_success pull_request epic/release-train feature/release-tooling
expect_success pull_request main release/v1.2.3
expect_success pull_request main hotfix/startup-crash
expect_success pull_request main epic/release-train

expect_failure pull_request main feature/queue-fix
expect_failure pull_request main epic/new-garden
expect_failure pull_request main next
expect_failure pull_request main release/1.2.3
expect_failure pull_request main release/v1.2

echo "main route: OK"

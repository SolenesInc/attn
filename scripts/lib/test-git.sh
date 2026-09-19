#!/usr/bin/env bash

resolve_test_git() {
  local test_git="${ATTN_TEST_GIT:-}" resolved
  if [ -z "$test_git" ]; then
    if [ -x /usr/bin/git ]; then
      test_git=/usr/bin/git
      # macOS /usr/bin/git is an xcrun shim: 10.6ms per spawn against 4.4ms for
      # the Git it finds (2026-09-19, Apple M5). Resolve it once for the suite.
      if command -v xcrun >/dev/null 2>&1 && resolved="$(xcrun --find git 2>/dev/null)"; then
        test_git="$resolved"
      fi
    else
      test_git="$(command -v git)"
    fi
  fi
  if [ ! -x "$test_git" ]; then
    echo "test Git is not executable: $test_git" >&2
    return 2
  fi
  printf '%s\n' "$test_git"
}

# An empty origin receives the clone's whole history on the first push. Borrowing
# the clone's objects leaves only the refs to send.
borrow_clone_objects() {
  local origin="$1" clone="$2"
  printf '%s/.git/objects\n' "$clone" >"$origin/objects/info/alternates"
}

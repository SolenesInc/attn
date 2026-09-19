#!/usr/bin/env bash

resolve_test_git() {
  local test_git="${ATTN_TEST_GIT:-}" resolved
  if [ -z "$test_git" ]; then
    if [ -x /usr/bin/git ]; then
      test_git=/usr/bin/git
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

borrow_clone_objects() {
  local origin="$1" clone="$2"
  printf '%s/.git/objects\n' "$clone" >"$origin/objects/info/alternates"
}

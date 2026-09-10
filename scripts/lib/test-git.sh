#!/usr/bin/env bash

resolve_test_git() {
  local test_git="${ATTN_TEST_GIT:-}"
  if [ -z "$test_git" ]; then
    if [ -x /usr/bin/git ]; then
      test_git=/usr/bin/git
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

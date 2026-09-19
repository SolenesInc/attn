#!/usr/bin/env bash
set -euo pipefail

base="${1:?usage: go-test-inputs-changed.sh <base-ref>}"
inputs='(\.go$|^(cmd|internal|test|pty-host|apphost|scripts)/|^(go\.mod|go\.sum|Makefile|\.tool-versions|ghostty-vt\.pin|ghostty-vt-native\.lock)$)'

root="$(git rev-parse --show-toplevel)"
cd "$root"
if ! merge_base="$(git merge-base "$base" HEAD 2>/dev/null)"; then
  echo "go test inputs: cannot find a merge base with $base, so the suite runs" >&2
  exit 0
fi

changed="$({
  git diff --name-only --no-renames "$merge_base" --
  git ls-files --others --exclude-standard
} | grep -E "$inputs" || true)"
if [ -z "$changed" ]; then
  exit 1
fi
printf 'go test inputs changed since %s:\n' "$base" >&2
printf '%s\n' "$changed" | sed -n '1,5s/^/  /p' >&2

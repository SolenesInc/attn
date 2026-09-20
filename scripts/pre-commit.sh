#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

go_files=()
rust_files=()
while IFS= read -r -d '' file; do
  case "$file" in
    *.go) go_files+=("$file") ;;
    app/src-tauri/*.rs) rust_files+=("$file") ;;
    *) continue ;;
  esac
  if ! git diff --quiet -- "$file"; then
    printf 'Cannot auto-format partially staged file: %s\n' "$file" >&2
    exit 1
  fi
done < <(git diff --cached --name-only -z --diff-filter=ACMR)

if [ "${#go_files[@]}" -gt 0 ]; then
  gofmt -w "${go_files[@]}"
  git add -- "${go_files[@]}"
fi

if [ "${#rust_files[@]}" -gt 0 ]; then
  rustfmt --edition 2021 --config skip_children=true "${rust_files[@]}"
  git add -- "${rust_files[@]}"
fi

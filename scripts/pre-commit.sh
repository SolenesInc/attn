#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

go_files=()
tauri_rust_files=()
pty_host_rust_files=()
while IFS= read -r -d '' file; do
  case "$file" in
    *.go) go_files+=("$file") ;;
    app/src-tauri/*.rs) tauri_rust_files+=("$file") ;;
    pty-host/*.rs) pty_host_rust_files+=("$file") ;;
    *) continue ;;
  esac
  if ! git diff --quiet -- "$file"; then
    printf 'Cannot auto-format partially staged file: %s\n' "$file" >&2
    exit 1
  fi
done < <(git diff --cached --name-only -z --diff-filter=ACMR)

format_rust() {
  local edition="$1"
  shift
  rustfmt --edition "$edition" --config skip_children=true "$@"
  git add -- "$@"
}

if [ "${#go_files[@]}" -gt 0 ]; then
  gofmt -w "${go_files[@]}"
  git add -- "${go_files[@]}"
fi

if [ "${#tauri_rust_files[@]}" -gt 0 ]; then
  format_rust 2021 "${tauri_rust_files[@]}"
fi

if [ "${#pty_host_rust_files[@]}" -gt 0 ]; then
  format_rust 2024 "${pty_host_rust_files[@]}"
fi

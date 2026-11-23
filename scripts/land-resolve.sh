#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

CGO_CFLAGS="$(cat go-cgo-cflags)"
export CGO_CFLAGS

protocol_outputs=(
  internal/protocol/generated.go
  internal/protocol/protocol_version.go
  internal/protocol/homegateway/generated.go
  internal/protocol/homegateway/schema.json
  internal/protocol/homegateway/version.go
  mobile/src/homeGateway/generated.ts
  mobile/src/homeGateway/version.ts
  cli/protocol/src/generated.rs
  cli/protocol/src/go_values.rs
  cli/attn/src/commands/seed/help_generated.rs
  internal/prompts/content/skills/attn/references/garden.md
  internal/prompts/tool_families_generated.go
  cli/testdata/duration_string.jsonl
  cli/testdata/flag.jsonl
  cli/testdata/flag_spec.json
  cli/testdata/json_indent.jsonl
  cli/testdata/local_time.jsonl
  cli/testdata/parse_duration.jsonl
  cli/testdata/quote.jsonl
  cli/testdata/tabwriter.jsonl
)
prompt_outputs=(
  internal/prompts/catalog.generated.json
  native/crates/attn-app/prompts/catalog.generated.json
)

generator_for() {
  local output
  for output in "${protocol_outputs[@]}"; do
    [ "$1" = "$output" ] && { echo generate-types; return; }
  done
  for output in "${prompt_outputs[@]}"; do
    [ "$1" = "$output" ] && { echo generate-prompts; return; }
  done
  return 1
}

conflicted="$(git diff --name-only --diff-filter=U)"
[ -n "$conflicted" ] || exit 1

need_types=0
need_prompts=0
while IFS= read -r file; do
  generator="$(generator_for "$file")" || exit 1
  case "$generator" in
    generate-types) need_types=1 ;;
    generate-prompts) need_prompts=1 ;;
  esac
done <<<"$conflicted"
[ "$need_types" = 1 ] || [ "$need_prompts" = 1 ] || exit 1

while IFS= read -r file; do
  git checkout --theirs -- "$file"
done <<<"$conflicted"

stage_existing() {
  local existing=() path
  for path in "$@"; do
    [ -e "$path" ] && existing+=("$path")
  done
  [ "${#existing[@]}" -eq 0 ] || git add -- "${existing[@]}"
}

# Run generate-types before generate-prompts so prompt catalogs see freshly regenerated references.
if [ "$need_types" = 1 ]; then
  make generate-types
  stage_existing "${protocol_outputs[@]}"
fi
if [ "$need_prompts" = 1 ]; then
  make generate-prompts
  stage_existing "${prompt_outputs[@]}"
fi

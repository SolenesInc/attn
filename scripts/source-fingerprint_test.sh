#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-fingerprint-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/scripts" "$work/internal/prompts/content" "$work/cmd/prompt-editor/web" "$work/docs" "$work/internal/prompts/scenarios" "$work/.prompt-editor/drafts"
cp "$root/scripts/source-fingerprint.sh" "$work/scripts/"
git -C "$work" init -q
printf 'prompt\n' > "$work/internal/prompts/content/example.md"
printf 'editor\n' > "$work/cmd/prompt-editor/web/app.js"
git -C "$work" add .
git -C "$work" -c user.name=test -c user.email=test@example.invalid -c commit.gpgsign=false commit -qm baseline
before="$(bash "$work/scripts/source-fingerprint.sh")"
printf 'changed prompt\n' > "$work/internal/prompts/content/example.md"
after="$(bash "$work/scripts/source-fingerprint.sh")"
[[ "$before" != "$after" ]] || { echo 'Markdown edits must invalidate the build'; exit 1; }
printf 'changed editor\n' > "$work/cmd/prompt-editor/web/app.js"
printf 'documentation\n' > "$work/docs/example.md"
printf 'scenario\n' > "$work/internal/prompts/scenarios/chief.json"
printf 'draft\n' > "$work/.prompt-editor/drafts/d-example.json"
[[ "$after" == "$(bash "$work/scripts/source-fingerprint.sh")" ]] || { echo 'Editor and documentation edits must not invalidate the product build'; exit 1; }
echo 'source fingerprint tests passed'

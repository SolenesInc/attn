#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-fingerprint-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/scripts" "$work/internal/prompts/content" "$work/cmd/prompt-editor/web" "$work/docs/internal" "$work/docs/guide/reference" "$work/docs/guide/images" "$work/internal/prompts/scenarios" "$work/.prompt-editor/drafts"
cp "$root/scripts/source-fingerprint.sh" "$work/scripts/"
git -C "$work" init -q
printf 'prompt\n' > "$work/internal/prompts/content/example.md"
printf 'editor\n' > "$work/cmd/prompt-editor/web/app.js"
printf 'guide\n' > "$work/docs/guide/reference/example.md"
git -C "$work" add .
git -C "$work" -c user.name=test -c user.email=test@example.invalid -c commit.gpgsign=false commit -qm baseline
before="$(bash "$work/scripts/source-fingerprint.sh")"
printf 'changed prompt\n' > "$work/internal/prompts/content/example.md"
after="$(bash "$work/scripts/source-fingerprint.sh")"
[[ "$before" != "$after" ]] || { echo 'Markdown edits must invalidate the build'; exit 1; }
manifest="$(bash "$work/scripts/source-fingerprint.sh" --field dirty_paths_base64)"
[[ -n "$manifest" ]] || { echo 'Relevant dirty paths must be embedded in the build manifest'; exit 1; }
printf 'changed editor\n' > "$work/cmd/prompt-editor/web/app.js"
printf 'documentation\n' > "$work/docs/internal/example.md"
printf 'image\n' > "$work/docs/guide/images/example.png"
printf 'scenario\n' > "$work/internal/prompts/scenarios/chief.json"
printf 'draft\n' > "$work/.prompt-editor/drafts/d-example.json"
[[ "$after" == "$(bash "$work/scripts/source-fingerprint.sh")" ]] || { echo 'Editor and documentation edits must not invalidate the product build'; exit 1; }
[[ "$manifest" == "$(bash "$work/scripts/source-fingerprint.sh" --field dirty_paths_base64)" ]] || { echo 'Excluded paths must not enter the build manifest'; exit 1; }
printf 'changed guide\n' > "$work/docs/guide/reference/example.md"
guide="$(bash "$work/scripts/source-fingerprint.sh")"
[[ "$after" != "$guide" ]] || { echo 'Guide edits must invalidate the product build'; exit 1; }
guide_manifest="$(bash "$work/scripts/source-fingerprint.sh" --field dirty_paths_base64 | base64 --decode | tr '\0' '\n')"
[[ "$guide_manifest" == *'docs/guide/reference/example.md'* ]] || { echo 'Guide edits must enter the build manifest'; exit 1; }
printf 'new guide\n' > "$work/docs/guide/reference/new.md"
[[ "$guide" != "$(bash "$work/scripts/source-fingerprint.sh")" ]] || { echo 'New guide pages must invalidate the product build'; exit 1; }
echo 'source fingerprint tests passed'

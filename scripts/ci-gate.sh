#!/usr/bin/env bash
set -euo pipefail

mode="${1:?usage: ci-gate.sh <pr|acceptance> <job>...}"
shift

case "$mode" in
  pr|acceptance) ;;
  *) echo "ci gate: unknown mode '$mode'" >&2; exit 2 ;;
esac

if [ "$#" -eq 0 ]; then
  echo "ci gate: name at least one expected job" >&2
  exit 2
fi

: "${NEEDS_JSON:?ci gate: NEEDS_JSON is required}"
jq -e 'type == "object"' <<<"$NEEDS_JSON" >/dev/null

expected_json="$(printf '%s\n' "$@" | jq -Rsc 'split("\n")[:-1]')"
unexpected="$(
  jq -nr --argjson needs "$NEEDS_JSON" --argjson expected "$expected_json" \
    '$needs | keys - $expected | .[]'
)"
if [ -n "$unexpected" ]; then
  echo "ci gate: dependency was not named in the receipt: $unexpected" >&2
  exit 1
fi

failed=0
receipt="$(mktemp "${TMPDIR:-/tmp}/attn-ci-gate.XXXXXX")"
trap 'rm -f "$receipt"' EXIT
printf '| Job | Result |\n| --- | --- |\n' >"$receipt"

for job in "$@"; do
  result="$(jq -r --arg job "$job" '.[$job].result // "missing"' <<<"$NEEDS_JSON")"
  printf "| \`%s\` | \`%s\` |\n" "$job" "$result" >>"$receipt"

  case "$mode:$result" in
    pr:success|pr:skipped|acceptance:success) ;;
    *) failed=1 ;;
  esac
done

cat "$receipt"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  {
    printf '## %s receipt\n\n' "$mode"
    cat "$receipt"
  } >>"$GITHUB_STEP_SUMMARY"
fi

if [ "$failed" -ne 0 ]; then
  if [ "$mode" = acceptance ]; then
    echo "ci gate: exact-SHA acceptance requires every job to succeed" >&2
  else
    echo "ci gate: a pull-request job failed, was cancelled, or is missing" >&2
  fi
  exit 1
fi

echo "ci gate: $mode passed"

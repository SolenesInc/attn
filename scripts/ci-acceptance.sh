#!/usr/bin/env bash
set -euo pipefail

: "${NEEDS_JSON:?ci acceptance: NEEDS_JSON is required}"
: "${GITHUB_REPOSITORY:?ci acceptance: GITHUB_REPOSITORY is required}"
: "${GITHUB_RUN_ID:?ci acceptance: GITHUB_RUN_ID is required}"
: "${GITHUB_RUN_ATTEMPT:?ci acceptance: GITHUB_RUN_ATTEMPT is required}"
: "${GITHUB_SHA:?ci acceptance: GITHUB_SHA is required}"

unmet="$(jq -c '[to_entries[]
  | select(.value.result != "success")
  | select((.key | IN("full-macos", "app-acceptance-macos")) and .value.result == "skipped" | not)
  | {job: .key, result: .value.result}]' <<<"$NEEDS_JSON")"
if [ "$unmet" = '[]' ]; then
  echo "ci acceptance: every job $GITHUB_SHA needs is green"
  exit 0
fi
echo "ci acceptance: not green: $unmet" >&2
if jq -e 'any(.[]; .result == "skipped")' <<<"$unmet" >/dev/null; then
  exit 1
fi

finished="$(
  gh api --paginate \
    "repos/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID/attempts/$GITHUB_RUN_ATTEMPT/jobs?per_page=100" \
    --jq '.jobs[] | select(.status == "completed" and (.conclusion | IN("success", "skipped") | not))
      | {name, conclusion, runner_name, labels}' | jq -s .
)"
never_started_on_macos='.conclusion == "cancelled" and (.runner_name // "") == "" and any(.labels[]; startswith("macos"))'
if ! jq -e "length > 0 and all(.[]; $never_started_on_macos)" <<<"$finished" >/dev/null; then
  jq -r '.[] | "ci acceptance: \(.name) is \(.conclusion)"' <<<"$finished" >&2
  exit 1
fi

jobs="$(jq -r '[.[].name] | join(", ")' <<<"$finished")"
message="No runner picked up $jobs, so $GITHUB_SHA has no macOS coverage from them. The nightly run and the release commit's run run every macOS job."
echo "::warning title=macOS coverage missing::$message"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  printf '### macOS coverage missing\n\n%s\n' "$message" >>"$GITHUB_STEP_SUMMARY"
fi
echo "ci acceptance: every other job is green; passing without macOS coverage from $jobs"

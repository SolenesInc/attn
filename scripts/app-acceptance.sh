#!/usr/bin/env bash
set -euo pipefail

actual_sha="${1:?usage: app-acceptance.sh <actual-sha> <candidate-sha> <outcome> <profile> <scenarios> <evidence>}"
candidate_sha="${2:?candidate SHA is required}"
outcome="${3:?outcome is required}"
profile="${4:?profile is required}"
scenarios="${5:?scenarios are required}"
evidence="${6:?evidence is required}"

if [[ ! "$candidate_sha" =~ ^[0-9a-f]{40,64}$ ]]; then
  echo "app acceptance: candidate SHA must be a full commit SHA" >&2
  exit 1
fi
if [[ "$actual_sha" != "$candidate_sha" ]]; then
  echo "app acceptance: candidate checkout is ${actual_sha}, requested candidate is ${candidate_sha}" >&2
  echo "dispatch the protected main workflow with the exact candidate SHA input" >&2
  exit 1
fi
if [[ "$outcome" != "passed" && "$outcome" != "failed" ]]; then
  echo "app acceptance: outcome must be passed or failed" >&2
  exit 1
fi

receipt="$(mktemp "${TMPDIR:-/tmp}/attn-app-acceptance.XXXXXX")"
trap 'rm -f "$receipt"' EXIT
cat >"$receipt" <<EOF
## App acceptance receipt

| Field | Value |
| --- | --- |
| Candidate | \`${candidate_sha}\` |
| Outcome | \`${outcome}\` |
| Profile | \`${profile}\` |
| Scenarios | ${scenarios} |
| Evidence | ${evidence} |
EOF

cat "$receipt"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  cat "$receipt" >>"$GITHUB_STEP_SUMMARY"
fi

if [[ "$outcome" == "failed" ]]; then
  echo "app acceptance: manual verification failed" >&2
  exit 1
fi

echo "app acceptance: exact candidate head accepted"

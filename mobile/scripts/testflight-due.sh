#!/usr/bin/env bash
set -euo pipefail

MAX_AGE_DAYS=60
WEEKLY_SKIPS_WITHIN_MAX_AGE=$((MAX_AGE_DAYS / 7 + 1))
RUNS_TO_SEARCH=$((WEEKLY_SKIPS_WITHIN_MAX_AGE + 1))

while read -r run_id age_days; do
  uploads="$(gh run view "${run_id}" --json jobs \
    --jq '[.jobs[] | select(.name == "Upload to TestFlight" and .conclusion == "success")] | length')"
  ((uploads > 0)) || continue
  echo "newest upload: run ${run_id}, ${age_days} days ago, limit ${MAX_AGE_DAYS}" >&2
  if ((age_days > MAX_AGE_DAYS)); then echo due=true; else echo due=false; fi
  exit 0
done < <(gh run list --workflow testflight.yml --status success --limit "${RUNS_TO_SEARCH}" \
  --json databaseId,createdAt \
  --jq '.[] | "\(.databaseId) \(((now - (.createdAt | fromdate)) / 86400) | floor)"')

echo "no upload among the last ${RUNS_TO_SEARCH} successful runs" >&2
echo due=true

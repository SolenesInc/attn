#!/usr/bin/env bash
#
# Rank the tests that failed in GitHub Actions CI, separating flakes from
# breakage, from the test receipts CI's jobs upload as artifacts. The ranking
# is `go tool test-history report`'s; run that to rank this machine's spool,
# which sync fills with every machine's receipts.
#
# Run it from a checkout whose --main ref (default origin/main) holds the
# commits CI tested. Artifacts never change once uploaded, so each is
# downloaded once into the cache.
#
# Usage:
#   scripts/ci-flake-report.sh [--limit N] [--format table|markdown|json]
#                              [--repo OWNER/REPO] [--main REF]
#                              [--dormant-after N] [--cache DIR]
#
# Formats:
#   table     ranked flakes, dormant flakes and unresolved failures (default)
#   markdown  tracking-issue body, consumed by the flake-ledger workflow
#   json      the report's records

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo="${ATTN_FLAKE_REPO:-SolenesInc/attn}"
limit=200
format=table
main=origin/main
dormant_after=60
cache="${ATTN_FLAKE_CACHE:-${TMPDIR:-/tmp}/attn-ci-receipts}"

while [ $# -gt 0 ]; do
  case "$1" in
    --limit) limit="$2"; shift 2 ;;
    --format) format="$2"; shift 2 ;;
    --repo) repo="$2"; shift 2 ;;
    --main) main="$2"; shift 2 ;;
    --dormant-after) dormant_after="$2"; shift 2 ;;
    --cache) cache="$2"; shift 2 ;;
    -h|--help) sed -n '2,21p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

for tool in gh jq unzip go; do
  command -v "$tool" >/dev/null || { echo "missing required tool: $tool" >&2; exit 2; }
done

mkdir -p "$cache/artifacts"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-flake.XXXXXX")"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/receipts"
go -C "$root" build -o "$work/test-history" ./scripts/test-history

gh run list --workflow ci.yml --repo "$repo" --limit "$limit" --json databaseId,status \
  | jq -r '.[] | select(.status == "completed") | .databaseId' >"$work/runs"
runs="$(grep -c . "$work/runs" || true)"
[ "$runs" -gt 0 ] || { echo "ci-flake-report: no completed ci.yml runs in $repo" >&2; exit 1; }

read_artifacts=0 expired=0 unreadable=0 without=0
while read -r run; do
  gh api --paginate "repos/$repo/actions/runs/$run/artifacts?per_page=100" \
    | jq -r '.artifacts[] | select(.name | startswith("test-receipts-")) | [(.id | tostring), (.expired | tostring)] | @tsv' >"$work/artifacts"
  [ -s "$work/artifacts" ] || { without=$((without + 1)); continue; }
  while IFS=$'\t' read -r id is_expired; do
    dest="$cache/artifacts/$id"
    if [ ! -d "$dest" ]; then
      if [ "$is_expired" = true ]; then
        expired=$((expired + 1))
        continue
      fi
      rm -rf "$dest.tmp"
      if ! gh api "repos/$repo/actions/artifacts/$id/zip" >"$work/artifact.zip" || ! unzip -qo "$work/artifact.zip" -d "$dest.tmp"; then
        unreadable=$((unreadable + 1))
        continue
      fi
      mv "$dest.tmp" "$dest"
    fi
    cp -R "$dest" "$work/receipts/$id"
    read_artifacts=$((read_artifacts + 1))
  done <"$work/artifacts"
done <"$work/runs"

source="the receipt artifacts of the last $runs completed ci.yml runs of $repo"
source+=" ($read_artifacts read, $expired expired, $unreadable unreadable; $without runs uploaded none)"
"$work/test-history" report --receipts "$work/receipts" --source "$source" \
  --main "$main" --dormant-after "$dormant_after" --format "$format"

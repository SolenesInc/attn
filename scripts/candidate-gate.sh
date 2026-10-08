#!/usr/bin/env bash
set -euo pipefail

current_main_ref="${1:?usage: candidate-gate.sh <current-main-ref> <head-ref> <head-branch>}"
head_ref="${2:?candidate head ref is required}"
head_branch="${3:?candidate head branch is required}"

for tool in gh git go jq; do
  command -v "$tool" >/dev/null || {
    echo "candidate gate: missing $tool" >&2
    exit 2
  }
done

root="$(git rev-parse --show-toplevel)"
script_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$script_root/lib/release-tag.sh"
cd "$root"
manifest=.github/release-candidate.yml
if [[ ! -f "$manifest" ]]; then
  echo "candidate gate: $manifest is missing" >&2
  exit 1
fi

version="$(awk '$1 == "version:" { print $2 }' "$manifest")"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "candidate gate: manifest version must look like 1.2.3" >&2
  exit 1
fi
if [[ "$head_branch" != "release/v${version}" ]]; then
  echo "candidate gate: $head_branch carries a manifest for v${version}; expected release/v${version}" >&2
  exit 1
fi

other_candidates="$(
  bash "$script_root/open-release-candidates.sh" |
    awk -F '\t' -v current="$head_branch" '$1 != current'
)"
other_count="$(printf '%s\n' "$other_candidates" | awk 'NF { count++ } END { print count + 0 }')"
if [[ "$other_count" -ne 0 ]]; then
  echo "candidate gate: another release candidate is open:" >&2
  printf '  %s\n' "$other_candidates" >&2
  exit 1
fi

candidate_args=(
  --current-main "$current_main_ref"
  --head "$head_ref"
  --tag-status absent
  --other-open-candidates "$other_count"
)
require_remote_tag_absent "${RELEASE_TRAIN_REMOTE:-origin}" "v${version}" \
  "candidate gate" "tag v${version} already exists"
candidate_sha="$(git rev-parse --verify "${head_ref}^{commit}")"
if "$script_root/workflow-job-gate.sh" \
  ci.yml "$candidate_sha" pull_request - 'App acceptance'; then
  echo "candidate gate: CI App acceptance is green for $candidate_sha"
else
  "$script_root/workflow-job-gate.sh" \
    app-acceptance.yml "$candidate_sha" workflow_dispatch main 'App acceptance'
  echo "candidate gate: manual App acceptance override is green for $candidate_sha"
fi

go run ./cmd/release-train candidate validate "${candidate_args[@]}"
echo "candidate gate: $head_branch is a valid release candidate"

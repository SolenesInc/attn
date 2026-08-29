#!/usr/bin/env bash
set -euo pipefail

kind="${1:?usage: candidate-gate.sh <promotion|hotfix> <current-main-ref> <head-ref> <head-branch>}"
current_main_ref="${2:?current main ref is required}"
head_ref="${3:?candidate head ref is required}"
head_branch="${4:?candidate head branch is required}"

case "$kind" in
  promotion|hotfix) ;;
  *) echo "candidate gate: unsupported kind '$kind'" >&2; exit 2 ;;
esac

for tool in gh git go jq; do
  command -v "$tool" >/dev/null || {
    echo "candidate gate: missing $tool" >&2
    exit 2
  }
done

root="$(git rev-parse --show-toplevel)"
script_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$root"
manifest=.github/release-candidate.yml
if [[ ! -f "$manifest" ]]; then
  echo "candidate gate: $manifest is missing" >&2
  exit 1
fi

manifest_kind="$(awk '$1 == "kind:" { print $2 }' "$manifest")"
source_sha="$(awk '$1 == "source_sha:" { print $2 }' "$manifest")"
if [[ "$manifest_kind" != "$kind" ]]; then
  echo "candidate gate: $head_branch requires kind $kind, found ${manifest_kind:-missing}" >&2
  exit 1
fi
if [[ ! "$source_sha" =~ ^[0-9a-f]{40,64}$ ]]; then
  echo "candidate gate: manifest source_sha must be a full commit SHA" >&2
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
  --other-open-candidates "$other_count"
)
if [[ "$kind" == promotion ]]; then
  "$script_root/workflow-job-gate.sh" ci.yml "$source_sha" push next Acceptance
  candidate_args+=(--source-acceptance success)
  echo "candidate gate: source Acceptance is green"
fi

candidate_sha="$(git rev-parse --verify "${head_ref}^{commit}")"
"$script_root/workflow-job-gate.sh" \
  app-acceptance.yml "$candidate_sha" workflow_dispatch main 'App acceptance'
echo "candidate gate: protected-main App acceptance is green for $candidate_sha"

go run ./cmd/release-train candidate validate "${candidate_args[@]}"
echo "candidate gate: $head_branch is a valid $kind candidate"

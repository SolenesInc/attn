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

candidate_json="$(
  gh pr list --base main --state open --limit 100 --json headRefName,url
)"
other_candidates="$(
  jq -r --arg current "$head_branch" '
    .[]
    | select(.headRefName != $current)
    | select(.headRefName | test("^(release/v[0-9]+\\.[0-9]+\\.[0-9]+|hotfix/.+)$"))
    | "\(.headRefName)\t\(.url)"
  ' <<<"$candidate_json"
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
  acceptance="$(
    gh api --method GET \
      "repos/{owner}/{repo}/commits/${source_sha}/check-runs?check_name=Acceptance&filter=latest" \
      --jq '.check_runs | map(select(.name == "Acceptance" and .app.slug == "github-actions")) | sort_by(.started_at) | last | select(.) | [.head_sha, .status, (.conclusion // ""), .html_url] | @tsv'
  )"
  if [[ -z "$acceptance" ]]; then
    echo "candidate gate: ${source_sha} has no Acceptance check" >&2
    exit 1
  fi
  IFS=$'\t' read -r acceptance_sha acceptance_status acceptance_conclusion acceptance_url <<<"$acceptance"
  if [[ "$acceptance_sha" != "$source_sha" ]]; then
    echo "candidate gate: Acceptance belongs to $acceptance_sha, expected $source_sha" >&2
    exit 1
  fi
  if [[ "$acceptance_status/$acceptance_conclusion" != "completed/success" ]]; then
    echo "candidate gate: Acceptance is $acceptance_status/${acceptance_conclusion:-none}" >&2
    echo "$acceptance_url" >&2
    exit 1
  fi
  candidate_args+=(--source-acceptance success)
  echo "candidate gate: source Acceptance is green ($acceptance_url)"
fi

go run ./cmd/release-train candidate validate "${candidate_args[@]}"
echo "candidate gate: $head_branch is a valid $kind candidate"

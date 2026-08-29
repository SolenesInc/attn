#!/usr/bin/env bash
set -euo pipefail

conclusion="${1:?usage: release-health.sh <conclusion> <tag> <run-url>}"
tag="${2:?usage: release-health.sh <conclusion> <tag> <run-url>}"
run_url="${3:?usage: release-health.sh <conclusion> <tag> <run-url>}"

if ! [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "release health: invalid release tag '$tag'" >&2
  exit 2
fi
: "${GITHUB_REPOSITORY:?release health: GITHUB_REPOSITORY is required}"
for tool in gh jq; do
  command -v "$tool" >/dev/null || { echo "release health: missing $tool" >&2; exit 2; }
done

sha="$(gh api "repos/$GITHUB_REPOSITORY/commits/$tag" --jq .sha)"
if ! [[ "$sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "release health: $tag returned invalid commit SHA '$sha'" >&2
  exit 1
fi

title="Release health: $tag"
issues="$(
  gh api --paginate "repos/$GITHUB_REPOSITORY/issues?state=all&per_page=100" \
    --jq '.[] | select(has("pull_request") | not) | {number,state,title}' \
    | jq -s .
)"
matches="$(jq --arg title "$title" '[.[] | select(.title == $title)]' <<<"$issues")"
match_count="$(jq 'length' <<<"$matches")"
if [ "$match_count" -gt 1 ]; then
  echo "release health: found $match_count issues titled '$title'; refusing an ambiguous update" >&2
  exit 1
fi

number="$(jq -r 'first | .number // empty' <<<"$matches")"
state="$(jq -r 'first | .state // empty' <<<"$matches")"
state="$(printf '%s' "$state" | tr '[:lower:]' '[:upper:]')"

if [ "$conclusion" = success ]; then
  if [ -n "$number" ] && [ "$state" = OPEN ]; then
    gh issue close "$number" --repo "$GITHUB_REPOSITORY" --reason completed \
      --comment "Release recovered for [$tag]($run_url)."
    echo "release health: closed #$number after recovery"
  else
    echo "release health: $tag is healthy; no open issue"
  fi
  exit 0
fi

release_state="No draft was created before the workflow stopped."
if release_info="$(gh release view "$tag" --repo "$GITHUB_REPOSITORY" --json isDraft,url --jq '[.isDraft, .url] | @tsv' 2>/dev/null)"; then
  IFS=$'\t' read -r is_draft release_url <<<"$release_info"
  if [ "$is_draft" = true ]; then
    release_state="The partial release remains a [private draft]($release_url)."
  else
    release_state="The release is already [public]($release_url); this failed run did not change that state."
  fi
fi

body="$(mktemp "${TMPDIR:-/tmp}/attn-release-health.XXXXXX")"
trap 'rm -f "$body"' EXIT
commit_url="https://github.com/$GITHUB_REPOSITORY/commit/$sha"
cat >"$body" <<EOF
The release workflow did not complete for \`$tag\`.

- Commit: [$sha]($commit_url)
- Result: \`$conclusion\`
- Run: $run_url

$release_state

Fix the failing release step, then rerun the same immutable tag:

\`\`\`bash
gh workflow run release.yml --ref main -f tag=$tag
\`\`\`

This issue stays open until a release run for the tag succeeds. Consecutive
failures update this receipt without adding comments; recovery closes it.
EOF

if [ -z "$number" ]; then
  create_args=(--repo "$GITHUB_REPOSITORY" --title "$title" --body-file "$body")
  if [ -n "${RELEASE_HEALTH_ASSIGNEE:-}" ]; then
    create_args+=(--assignee "$RELEASE_HEALTH_ASSIGNEE")
  fi
  gh issue create "${create_args[@]}"
  echo "release health: created issue for $tag"
  exit 0
fi

if [ "$state" = CLOSED ]; then
  gh issue reopen "$number" --repo "$GITHUB_REPOSITORY"
  echo "release health: reopened #$number"
fi
gh issue edit "$number" --repo "$GITHUB_REPOSITORY" --body-file "$body"
echo "release health: updated #$number"

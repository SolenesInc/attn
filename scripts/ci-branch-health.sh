#!/usr/bin/env bash
set -euo pipefail

conclusion="${1:?usage: ci-branch-health.sh <conclusion> <main|next> <sha> <run-url>}"
branch="${2:?usage: ci-branch-health.sh <conclusion> <main|next> <sha> <run-url>}"
sha="${3:?usage: ci-branch-health.sh <conclusion> <main|next> <sha> <run-url>}"
run_url="${4:?usage: ci-branch-health.sh <conclusion> <main|next> <sha> <run-url>}"

case "$branch" in
  main|next) ;;
  *) echo "branch health: unsupported branch '$branch'" >&2; exit 2 ;;
esac

if ! [[ "$sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "branch health: invalid commit SHA '$sha'" >&2
  exit 2
fi

: "${GITHUB_REPOSITORY:?branch health: GITHUB_REPOSITORY is required}"
for tool in gh jq; do
  command -v "$tool" >/dev/null || { echo "branch health: missing $tool" >&2; exit 2; }
done

remote_sha="$(gh api "repos/$GITHUB_REPOSITORY/branches/$branch" --jq .commit.sha)"
if [ "$remote_sha" != "$sha" ]; then
  echo "branch health: $branch moved to $remote_sha; ignoring stale result for $sha"
  exit 0
fi

title="Branch health: $branch acceptance"
issues="$(
  gh api --paginate "repos/$GITHUB_REPOSITORY/issues?state=all&per_page=100" \
    --jq '.[] | select(has("pull_request") | not) | {number,state,title}' \
    | jq -s .
)"
matches="$(jq --arg title "$title" '[.[] | select(.title == $title)]' <<<"$issues")"
match_count="$(jq 'length' <<<"$matches")"
if [ "$match_count" -gt 1 ]; then
  echo "branch health: found $match_count issues titled '$title'; refusing an ambiguous update" >&2
  exit 1
fi

number="$(jq -r 'first | .number // empty' <<<"$matches")"
state="$(jq -r 'first | .state // empty' <<<"$matches")"
state="$(printf '%s' "$state" | tr '[:lower:]' '[:upper:]')"

if [ "$conclusion" = success ]; then
  if [ -n "$number" ] && [ "$state" = OPEN ]; then
    gh issue close "$number" --repo "$GITHUB_REPOSITORY" --reason completed \
      --comment "Acceptance recovered on [$sha]($run_url)."
    echo "branch health: closed #$number after recovery"
  else
    echo "branch health: $branch is healthy; no open issue"
  fi
  exit 0
fi

body="$(mktemp "${TMPDIR:-/tmp}/attn-branch-health.XXXXXX")"
trap 'rm -f "$body"' EXIT
commit_url="https://github.com/$GITHUB_REPOSITORY/commit/$sha"
cat >"$body" <<EOF
The exact-SHA \`Acceptance\` gate is not green for \`$branch\`.

- Commit: [$sha]($commit_url)
- Result: \`$conclusion\`
- Run: $run_url

This issue stays open while the current branch head is unhealthy. Consecutive
failures update this receipt without adding comments; recovery closes it.
EOF

if [ -z "$number" ]; then
  create_args=(--repo "$GITHUB_REPOSITORY" --title "$title" --body-file "$body")
  if [ -n "${BRANCH_HEALTH_ASSIGNEE:-}" ]; then
    create_args+=(--assignee "$BRANCH_HEALTH_ASSIGNEE")
  fi
  gh issue create "${create_args[@]}"
  echo "branch health: created issue for $branch"
  exit 0
fi

if [ "$state" = CLOSED ]; then
  gh issue reopen "$number" --repo "$GITHUB_REPOSITORY"
  echo "branch health: reopened #$number"
fi
gh issue edit "$number" --repo "$GITHUB_REPOSITORY" --body-file "$body"
echo "branch health: updated #$number"

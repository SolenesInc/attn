#!/usr/bin/env bash
set -euo pipefail

remote="${RELEASE_TRAIN_REMOTE:-origin}"

usage() {
  cat <<EOF
usage: $0

Create a merge-preserving pull request that synchronizes ${remote}/main into
${remote}/next and removes only the changelog fragments consumed by the
released candidate. Refuses to start while a release candidate PR is open.
EOF
}

if [[ $# -ne 0 ]]; then
  usage >&2
  exit 2
fi

for command in git gh go; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "sync main: ${command} is required" >&2
    exit 1
  fi
done

root="$(git rev-parse --show-toplevel)"
cd "$root"

if [[ -n "$(git status --porcelain)" ]]; then
  echo "sync main: working tree must be clean" >&2
  exit 1
fi

if ! gh auth status >/dev/null 2>&1; then
  echo "sync main: gh is not authenticated; run 'gh auth login'" >&2
  exit 1
fi

active_candidate="$(
  gh pr list --base main --state open --limit 100 --json headRefName,url \
    --jq '.[] | select(.headRefName | test("^release/v[0-9]+\\.[0-9]+\\.[0-9]+$")) | "\(.headRefName)\t\(.url)"'
)"
if [[ -n "$active_candidate" ]]; then
  echo "sync main: a release candidate is still active:" >&2
  printf '  %s\n' "$active_candidate" >&2
  echo "merge or close it before changing next's main baseline" >&2
  exit 1
fi

echo "Fetching ${remote}/main and ${remote}/next..."
git fetch "$remote" main next

main_ref="${remote}/main"
next_ref="${remote}/next"
main_sha="$(git rev-parse --verify "${main_ref}^{commit}")"
next_sha="$(git rev-parse --verify "${next_ref}^{commit}")"
branch="sync/main-into-next-${main_sha:0:12}"

if git show-ref --verify --quiet "refs/heads/${branch}"; then
  echo "sync main: local branch ${branch} already exists" >&2
  exit 1
fi
if git ls-remote --exit-code --heads "$remote" "$branch" >/dev/null 2>&1; then
  echo "sync main: remote branch ${branch} already exists" >&2
  exit 1
fi

work_root="$(mktemp -d "${TMPDIR:-/tmp}/attn-main-sync.XXXXXX")"
sync_worktree="$work_root/worktree"
worktree_added=0

cleanup() {
  if [[ "$worktree_added" -eq 1 ]]; then
    git worktree remove --force "$sync_worktree" >/dev/null 2>&1 || true
    git branch -D "$branch" >/dev/null 2>&1 || true
  fi
  rm -rf "$work_root"
}
trap cleanup EXIT

git worktree add -q -b "$branch" "$sync_worktree" "$next_ref"
worktree_added=1

merged_main=0
if ! git merge-base --is-ancestor "$main_sha" "$next_sha"; then
  git -C "$sync_worktree" merge --no-ff "$main_ref" \
    -m "chore(release): sync main into next"
  merged_main=1
elif (
  cd "$sync_worktree"
  go run ./cmd/release-train sync check --main "$main_ref"
) >/dev/null 2>&1; then
  echo "next already contains ${main_sha} and its released fragments are reconciled"
  exit 0
fi

(
  cd "$sync_worktree"
  go run ./cmd/release-train sync apply --main "$main_ref"
)

if ! git -C "$sync_worktree" diff --cached --quiet; then
  if [[ "$merged_main" -eq 1 ]]; then
    git -C "$sync_worktree" commit --amend --no-edit
  else
    git -C "$sync_worktree" commit \
      -m "chore(release): consume released changelog fragments"
  fi
fi

(
  cd "$sync_worktree"
  go run ./cmd/release-train sync check --main "$main_ref"
)
git -C "$sync_worktree" merge-base --is-ancestor "$main_sha" HEAD

sync_sha="$(git -C "$sync_worktree" rev-parse HEAD)"
git -C "$sync_worktree" push -u "$remote" "HEAD:refs/heads/${branch}"

body="$work_root/pr-body.md"
cat >"$body" <<EOF
## Why

Bring accepted main commit \`${main_sha}\` back into \`next\` before another
release candidate is cut.

## What changed

- merged \`main\` into \`next\` without rewriting either history
- removed only changelog fragments recorded in the released candidate
- preserved changes and fragments that landed on \`next\` after the freeze

## Merge method

Merge this PR with a **merge commit**. Squashing or rebasing it would discard
the ancestry link this sync exists to establish.

Prepared sync commit: \`${sync_sha}\`
EOF

pr_url="$(gh pr create --base next --head "$branch" \
  --title "chore(release): sync main into next" --body-file "$body")"
echo "Opened ${pr_url}"
echo "Merge it with a merge commit before preparing another candidate."

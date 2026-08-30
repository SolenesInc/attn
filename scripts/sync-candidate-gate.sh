#!/usr/bin/env bash
set -euo pipefail

main_ref="${1:?usage: sync-candidate-gate.sh <main-ref> <next-ref> <head-ref> <head-branch>}"
next_ref="${2:?next ref is required}"
head_ref="${3:?head ref is required}"
head_branch="${4:?head branch is required}"

root="$(git rev-parse --show-toplevel)"
cd "$root"

main_sha="$(git rev-parse --verify "${main_ref}^{commit}")"
next_sha="$(git rev-parse --verify "${next_ref}^{commit}")"
head_sha="$(git rev-parse --verify "${head_ref}^{commit}")"
expected_branch="sync/main-into-next-${main_sha:0:12}"
if [[ "$head_branch" != "$expected_branch" ]]; then
  echo "sync candidate gate: expected branch $expected_branch, got $head_branch" >&2
  exit 1
fi

IFS=' ' read -r -a parents <<<"$(git show -s --format='%P' "$head_sha")"
if git merge-base --is-ancestor "$main_sha" "$next_sha"; then
  expected_tree="$(git rev-parse "${next_sha}^{tree}")"
  if [[ "${#parents[@]}" -ne 1 || "${parents[0]}" != "$next_sha" ]]; then
    echo "sync candidate gate: fragment-only sync must be based exactly on $next_sha" >&2
    exit 1
  fi
else
  expected_tree="$(git merge-tree --write-tree "$next_sha" "$main_sha")"
  if [[ "${#parents[@]}" -ne 2 || "${parents[0]}" != "$next_sha" || "${parents[1]}" != "$main_sha" ]]; then
    echo "sync candidate gate: sync must merge exact next $next_sha and main $main_sha" >&2
    exit 1
  fi
fi

source_sha="$({
  git show "$head_sha:.github/release-candidate.yml" |
    sed -n 's/^source_sha:[[:space:]]*//p'
} || true)"
if ! [[ "$source_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "sync candidate gate: candidate manifest has invalid source_sha" >&2
  exit 1
fi

while IFS=$'\t' read -r status path; do
  [[ -z "$status" ]] && continue
  if [[ "$status" != D || "$path" != changelog.d/*.yaml ]]; then
    echo "sync candidate gate: unexpected generated-tree change $status $path" >&2
    exit 1
  fi
  source_blob="$(git rev-parse --verify "$source_sha:$path")"
  merged_blob="$(git rev-parse --verify "$expected_tree:$path")"
  if [[ "$source_blob" != "$merged_blob" ]]; then
    echo "sync candidate gate: $path is not the frozen fragment recorded at source" >&2
    exit 1
  fi
done < <(git diff --name-status --no-renames "$expected_tree" "$head_sha")

go run ./cmd/release-train sync check --main "$main_sha" --head "$head_sha" >/dev/null
echo "sync candidate gate: exact generated main-to-next reconciliation verified"

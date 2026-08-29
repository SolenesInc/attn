#!/usr/bin/env bash
set -euo pipefail

# PR changelog gate. Every PR must either add a fragment under changelog.d/ or
# modify CHANGELOG.md directly (the release compilation PR does the latter;
# hand-fixes to changelog copy also pass). Prepared candidates and release sync
# PRs may only delete already-accounted-for fragments, so they are exempt.
#
# usage: changelog-gate.sh <base-ref> [head-branch] [head-ref]
#
# Runs in CI (.github/workflows/ci.yml, job "Changelog") and locally:
#   ./scripts/changelog-gate.sh main
#
# See docs/making-a-release.md.

BASE_REF="${1:?usage: changelog-gate.sh <base-ref> [head-branch]}"
HEAD_BRANCH="${2:-$(git branch --show-current)}"
HEAD_REF="${3:-HEAD}"
script_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

cd "$(git rev-parse --show-toplevel)"

# Diff against the merge-base with the base branch; prefer origin/<base> when
# it exists (CI checks out with full history).
RANGE="${BASE_REF}...HEAD"
if git rev-parse -q --verify "origin/${BASE_REF}" >/dev/null; then
  RANGE="origin/${BASE_REF}...HEAD"
fi

main_ref="$BASE_REF"
if git rev-parse -q --verify "origin/${BASE_REF}" >/dev/null; then
  main_ref="origin/${BASE_REF}"
fi

if [[ "$BASE_REF" == "next" && "$HEAD_BRANCH" == sync/main-into-next-* ]]; then
  "$script_root/sync-candidate-gate.sh" origin/main "$main_ref" "$HEAD_REF" "$HEAD_BRANCH"
  echo "changelog gate: validated release sync ${HEAD_BRANCH}, skipping"
  exit 0
fi

if [[ "$BASE_REF" == "main" && "$HEAD_BRANCH" =~ ^release/v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  "$script_root/candidate-gate.sh" promotion "$main_ref" "$HEAD_REF" "$HEAD_BRANCH"
  echo "changelog gate: validated promotion candidate ${HEAD_BRANCH}, skipping"
  exit 0
fi

if [[ "$BASE_REF" == "main" && "$HEAD_BRANCH" == hotfix/* ]]; then
  if ! git diff --quiet "$RANGE" -- .github/release-candidate.yml; then
    "$script_root/candidate-gate.sh" hotfix "$main_ref" "$HEAD_REF" "$HEAD_BRANCH"
    echo "changelog gate: validated hotfix candidate ${HEAD_BRANCH}, skipping"
    exit 0
  fi

  if ! base_tag="$(go run ./cmd/release-train accepted-main tag --head "$main_ref")"; then
    echo "changelog gate: ${HEAD_BRANCH} must carry a fresh hotfix candidate manifest" >&2
    echo "run make release-hotfix VERSION_TAG=vX.Y.Z before opening the PR" >&2
    exit 1
  fi
  if git show-ref --verify --quiet "refs/tags/${base_tag}"; then
    echo "changelog gate: ${base_tag} is already published; ${HEAD_BRANCH} must carry a fresh hotfix candidate manifest" >&2
    echo "run make release-hotfix VERSION_TAG=vX.Y.Z before opening the PR" >&2
    exit 1
  fi
  echo "changelog gate: ${HEAD_BRANCH} repairs the still-unpublished ${base_tag} candidate"
fi

# Committed additions, plus staged/untracked ones so the gate is honest when
# run locally before committing (CI only ever sees the committed set).
added_fragment="$(
  git diff --diff-filter=A --name-only "$RANGE" -- 'changelog.d/*.yaml'
  git diff --diff-filter=A --name-only --cached -- 'changelog.d/*.yaml'
  git ls-files --others --exclude-standard -- 'changelog.d/*.yaml'
)"
touched_changelog="$(
  git diff --name-only "$RANGE" -- CHANGELOG.md
  git diff --name-only HEAD -- CHANGELOG.md
)"

if [[ -z "$added_fragment" && -z "$touched_changelog" ]]; then
  cat >&2 <<EOF
changelog gate: this branch neither adds a changelog fragment nor touches
CHANGELOG.md.

Add one YAML fragment under changelog.d/ describing what changed (use
kind: internal for changes with no user-visible behavior). Format and
examples: docs/making-a-release.md
EOF
  exit 1
fi

if [[ -n "$added_fragment" ]]; then
  echo "changelog gate: fragment(s) added:"
  echo "$added_fragment" | sed 's/^/  /'
else
  echo "changelog gate: CHANGELOG.md modified directly"
fi

echo "changelog gate: validating changelog.d/"
go run ./cmd/changelog-check
echo "changelog gate: OK"

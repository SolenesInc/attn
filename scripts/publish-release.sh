#!/usr/bin/env bash
set -euo pipefail

tag="${1:?usage: publish-release.sh <vX.Y.Z>}"
if ! [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "publish release: invalid tag '$tag'" >&2
  exit 2
fi
: "${GITHUB_REPOSITORY:?publish release: GITHUB_REPOSITORY is required}"
command -v gh >/dev/null || {
  echo "publish release: missing gh" >&2
  exit 2
}

release_id="$(gh api "repos/$GITHUB_REPOSITORY/releases/tags/$tag" --jq .id)"
if ! [[ "$release_id" =~ ^[0-9]+$ ]]; then
  echo "publish release: invalid release id '$release_id' for $tag" >&2
  exit 1
fi

gh api --method PATCH "repos/$GITHUB_REPOSITORY/releases/$release_id" \
  -F draft=false \
  -f make_latest=legacy \
  >/dev/null
echo "publish release: published $tag; GitHub selected the latest stable version"

gh release view "$tag" --repo "$GITHUB_REPOSITORY" --json isDraft,assets \
  --jq '"published draft=\(.isDraft) assets:\n" + (.assets | map("  " + .name) | join("\n"))'

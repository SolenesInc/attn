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

gh release edit "$tag" --repo "$GITHUB_REPOSITORY" --draft=false --latest=false

release_tags="$({
  gh api --paginate "repos/$GITHUB_REPOSITORY/releases?per_page=100" \
    --jq '.[] | select(.draft == false and .prerelease == false) | .tag_name'
} || true)"
valid_tags=()
while IFS= read -r release_tag; do
  if [[ "$release_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    valid_tags+=("$release_tag")
  fi
done <<<"$release_tags"
if [[ "${#valid_tags[@]}" -eq 0 ]]; then
  echo "publish release: no published stable versions found after publishing $tag" >&2
  exit 1
fi

latest_tag="$(printf '%s\n' "${valid_tags[@]}" | sort -V | tail -n 1)"
gh release edit "$latest_tag" --repo "$GITHUB_REPOSITORY" --latest
if [[ "$latest_tag" == "$tag" ]]; then
  echo "publish release: published $tag as the latest stable version"
else
  echo "publish release: published $tag; latest remains newer $latest_tag"
fi

gh release view "$tag" --repo "$GITHUB_REPOSITORY" --json isDraft,assets \
  --jq '"published draft=\(.isDraft) assets:\n" + (.assets | map("  " + .name) | join("\n"))'

#!/usr/bin/env bash
set -euo pipefail

tag="${1:?usage: release-tag-gate.sh <vX.Y.Z>}"
script_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if ! [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "release tag gate: invalid tag '$tag'" >&2
  exit 2
fi

: "${GITHUB_REPOSITORY:?release tag gate: GITHUB_REPOSITORY is required}"
for tool in gh git go; do
  command -v "$tool" >/dev/null || {
    echo "release tag gate: missing $tool" >&2
    exit 2
  }
done

root="$(git rev-parse --show-toplevel)"
cd "$root"
if ! git show-ref --verify --quiet "refs/tags/$tag"; then
  echo "release tag gate: tag $tag does not exist" >&2
  exit 1
fi

tag_sha="$(git rev-parse --verify "${tag}^{commit}")"
head_sha="$(git rev-parse --verify 'HEAD^{commit}')"
trusted_sha="${GITHUB_SHA:-$(git rev-parse --verify 'origin/main^{commit}')}"
if [[ "$head_sha" != "$trusted_sha" ]]; then
  echo "release tag gate: trusted checkout is $head_sha, expected $trusted_sha" >&2
  exit 1
fi
main_sha="$(git rev-parse --verify 'origin/main^{commit}')"
if [[ "$trusted_sha" != "$main_sha" ]]; then
  echo "release tag gate: main moved from dispatch SHA $trusted_sha to $main_sha" >&2
  exit 1
fi
if ! git merge-base --is-ancestor "$tag_sha" origin/main; then
  echo "release tag gate: $tag_sha is not part of main" >&2
  exit 1
fi

validated_tag="$(go run ./cmd/release-train accepted-main validate --head "$tag_sha")"
if [[ "$validated_tag" != "$tag" ]]; then
  echo "release tag gate: manifest authorizes $validated_tag, not $tag" >&2
  exit 1
fi

"$script_root/app-acceptance-gate.sh" "$tag_sha"
"$script_root/workflow-job-gate.sh" ci.yml "$tag_sha" push main Acceptance

if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  echo "release_sha=$tag_sha" >>"$GITHUB_OUTPUT"
fi
echo "release tag gate: $tag is accepted at $tag_sha"

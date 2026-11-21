#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
commit="${1:?usage: source-at-commit.sh COMMIT DIR}"
dir="${2:?usage: source-at-commit.sh COMMIT DIR}"

if ! git cat-file -e "$commit^{commit}" 2>/dev/null; then
  git fetch --quiet --depth=1 origin "$commit"
fi
mkdir -p "$dir"
git archive "$commit" | tar -x -C "$dir"

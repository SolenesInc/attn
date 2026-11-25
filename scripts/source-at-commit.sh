#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
commit="${1:?usage: source-at-commit.sh COMMIT DIR}"
dir="${2:?usage: source-at-commit.sh COMMIT DIR}"

mkdir -p "$dir"
git archive "$commit" | tar -x -C "$dir"

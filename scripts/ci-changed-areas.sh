#!/usr/bin/env bash
# Classify what a change touches for CI job gating, using git only.
# Usage: ci-changed-areas.sh <base> [head]   prints "<area>=true|false" per line.
# An empty or all-zero base (first push of a branch) marks every area changed.
set -euo pipefail

base="${1:-}"
head="${2:-HEAD}"

# Globs use git's :(glob) pathspec: ** crosses directories, so 'cmd/**' is
# everything under cmd/ and '**/*.go' is every Go file anywhere.
areas=(daemon frontend generated tauri plugins)

daemon=(
  '**/*.go' 'cmd/**' 'internal/**' 'test/**' 'go.mod' 'go.sum' 'Makefile'
  'scripts/source-fingerprint.sh' 'scripts/claude/**' 'scripts/pr-evidence*.sh'
  'scripts/ci-changed-areas.sh' '.github/workflows/ci.yml'
)
frontend=(
  'app/src/**' 'app/e2e/**' 'app/scripts/**' 'app/test-harness/**' 'app/public/**'
  'app/index.html' 'app/package.json' 'app/pnpm-lock.yaml' 'app/playwright.config.ts'
  'app/tsconfig*.json' 'app/vite.config.ts' 'app/lint/**' '.oxlintrc.json' 'plugins/**'
  'app/pnpm-workspace.yaml'
  # The app SDK is a workspace package of the frontend and the source its
  # committed declarations are emitted from, so either edit reaches the typecheck.
  'sdk/attn-app/**' 'internal/appbuild/sdkdist/**' 'internal/protocol/schema/**'
  'scripts/ci-changed-areas.sh' '.github/workflows/ci.yml'
)
# Every input to the type pipeline plus both outputs, so a hand-edit of an
# output is gated even when no schema moved.
generated=(
  'internal/protocol/schema/**' 'internal/protocol/generated.go' 'app/src/types/generated.ts'
  'Makefile' 'scripts/ci-changed-areas.sh' '.github/workflows/ci.yml'
)
tauri=('app/src-tauri/**' 'scripts/ci-changed-areas.sh' '.github/workflows/ci.yml')
plugins=(
  'plugins/**' 'scripts/build-bundled-plugins.sh' '.tool-versions'
  'scripts/ci-changed-areas.sh' '.github/workflows/ci.yml'
)

if [[ -z "$base" || "$base" =~ ^0+$ ]]; then
  for area in "${areas[@]}"; do echo "$area=true"; done
  exit 0
fi

merge_base="$(git merge-base "$base" "$head")"
for area in "${areas[@]}"; do
  declare -n globs="$area"
  pathspecs=()
  for g in "${globs[@]}"; do pathspecs+=(":(glob)$g"); done
  if git diff --quiet "$merge_base" "$head" -- "${pathspecs[@]}"; then
    echo "$area=false"
  else
    echo "$area=true"
  fi
done

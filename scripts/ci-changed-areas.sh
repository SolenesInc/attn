#!/usr/bin/env bash
set -euo pipefail

base="${1:-}"
head="${2:-HEAD}"

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
  'sdk/attn-app/**' 'internal/appbuild/sdkdist/**' 'internal/protocol/schema/**'
  'scripts/ci-changed-areas.sh' '.github/workflows/ci.yml'
)
generated=(
  'internal/protocol/schema/**' 'internal/protocol/generated.go' 'app/src/types/generated.ts'
  'Makefile' 'scripts/ci-changed-areas.sh' '.github/workflows/ci.yml'
)
tauri=('app/src-tauri/**' 'scripts/ci-changed-areas.sh' '.github/workflows/ci.yml')
plugins=(
  'plugins/**' 'scripts/build-bundled-plugins.sh' '.tool-versions'
  'scripts/ci-changed-areas.sh' '.github/workflows/ci.yml'
)

everything_changed() {
  for area in "${areas[@]}"; do echo "$area=true"; done
  exit 0
}

if [[ -z "$base" || "$base" =~ ^0+$ ]]; then
  everything_changed
fi
merge_base="$(git merge-base "$base" "$head" 2>/dev/null)" || everything_changed
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

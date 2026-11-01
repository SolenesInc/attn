#!/usr/bin/env bash

release_fixture_paths=(
  go.mod
  go.sum
  cmd/release-train
  scripts/compile-changelog.sh
  CHANGELOG.md
  .github/release-candidate.yml
  app/package.json
  app/pnpm-lock.yaml
  app/src-tauri/tauri.conf.json
  app/src-tauri/Cargo.toml
  app/src-tauri/Cargo.lock
)

init_release_fixture() {
  local root="$1" repo="$2" path
  git init -q -b main "$repo"
  git -C "$repo" config user.name 'Release Test'
  git -C "$repo" config user.email 'release@example.com'
  for path in "${release_fixture_paths[@]}"; do
    mkdir -p "$repo/$(dirname "$path")"
    cp -R "$root/$path" "$repo/$path"
  done
  if [ -d "$root/changelog.d" ]; then
    cp -R "$root/changelog.d" "$repo/changelog.d"
  fi
  git -C "$repo" add -A
  git -C "$repo" commit -q -m 'release fixture baseline'
}

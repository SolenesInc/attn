#!/usr/bin/env bash

init_release_fixture() {
  local root="$1" repo="$2" source path
  git init -q -b main "$repo"
  git -C "$repo" config user.name 'Release Test'
  git -C "$repo" config user.email 'release@example.com'
  for source in \
    "$root/go.mod" \
    "$root/go.sum" \
    "$root/cmd/release-train" \
    "$root/scripts/compile-changelog.sh" \
    "$root/CHANGELOG.md" \
    "$root/.github/release-candidate.yml" \
    "$root/app/package.json" \
    "$root/app/pnpm-lock.yaml" \
    "$root/app/src-tauri/tauri.conf.json" \
    "$root/app/src-tauri/Cargo.toml" \
    "$root/app/src-tauri/Cargo.lock"; do
    path="${source#"$root/"}"
    mkdir -p "$repo/$(dirname "$path")"
    cp -R "$source" "$repo/$path"
  done
  mkdir -p "$repo/changelog.d"
  printf '%s\n' 'kind: added' 'area: releases' 'change: The release fixture has one pending release note.' \
    >"$repo/changelog.d/20990101-release-fixture.yaml"
  git -C "$repo" add -A
  git -C "$repo" commit -q -m 'release fixture baseline'
}

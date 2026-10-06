# Making a release

## Changelog fragments

Each ordinary PR adds a uniquely named `changelog.d/*.yaml`:

```yaml
kind: fixed # added | changed | fixed | removed | internal
area: queue
change: Auto-settle advances to the next agent with an outstanding turn.
```

Describe what users observe; invisible work uses `kind: internal`. Optional
fields: `symptom`, `notes`. CI validates fragments;
`go run ./cmd/changelog-check` does the same locally.

## Release

From a clean, current `main` with green Acceptance:

```bash
./scripts/release.sh vX.Y.Z
```

This cuts `release/vX.Y.Z` from that commit, compiles its fragments into
`CHANGELOG.md`, bumps every version, and opens a PR to `main`. Merge it once
`PR gate` and `App acceptance` are green. The merged commit is the release
commit: when its Acceptance is green, it gets tagged and published.

`main` may keep moving while the release PR is open. Changes merged after the
release was cut ship with it, and their fragments stay pending for the next
release.

If the automated app acceptance cannot cover the release PR or the merged
release commit, record a manual receipt for that exact commit with the command
printed in the release PR, then rerun its CI.

If Acceptance fails on the release commit, fix it on `main` and prepare the
release again with the same version.

## Hotfix

Fix forward: merge the fix to `main`, then release a patch version.

## After publishing

Confirm the release has the versioned DMG, `attn_aarch64.dmg`,
`attn-linux-amd64`, and `attn-linux-arm64`, then check
`brew upgrade --cask victorarias/attn/attn`. A failed publication leaves a draft
and a `Release health` issue; after fixing, retry the tag:

```bash
gh api --method POST repos/victorarias/attn/dispatches \
  -f event_type=release -F 'client_payload[tag]=<tag>'
```

Update the What's New modal (`app/src/components/WhatsNewModal.tsx`) for milestones.

## The `next` branch

`next` mirrors `main` so older checkouts keep pulling. A workflow fast-forwards
it after every push to `main`; nothing else writes to it.

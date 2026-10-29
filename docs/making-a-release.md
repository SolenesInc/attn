# Making a release

## Release notes

Release notes are written from the commits that land on `main`.
`scripts/compile-changelog.sh` reads the commits since the previous release,
groups them by their `Attn-Seed:` trailers, and has Claude write the
`CHANGELOG.md` section from each seed's title, description and notes and from
the commits' messages. Commits without a seed, and seeds it cannot read, are
judged by their messages alone, so write commit messages that say what users
observe. Run the release inside attn, in the profile that holds attn's seeds,
so their seeds can be read.

The section ends with a marker naming the commits it covers, and the release
checks require it, so no commit is skipped or covered twice. A pull request
from a fork needs nothing extra: its squashed commit carries its title and
description. Fragments left in `changelog.d` from before reach the writer with
the commits that added them, and the next release deletes them.

## Release

From a clean, current `main` with green Acceptance:

```bash
./scripts/release.sh vX.Y.Z
```

This cuts `release/vX.Y.Z` from that commit, compiles the commits since the
last release into `CHANGELOG.md`, bumps every version, and opens a PR to
`main`. Merge it once `PR gate` and `App acceptance` are green. The merged
commit is the release commit: when its Acceptance is green, it gets tagged and
published.

`main` may keep moving while the release PR is open. Changes that land after
the release was cut ship with it, and the next release's notes cover them.

If the automated app acceptance cannot cover the release PR or the merged
release commit, record a manual receipt for that exact commit with the command
printed in the release PR, then rerun its CI.

If Acceptance fails on the release commit, fix it on `main` and prepare the
release again with the same version.

## Hotfix

Fix forward: land the fix on `main`, then release a patch version.

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

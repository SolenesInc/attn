# Making a release

## Release notes

Release notes come from the commits that land on `main`.
`scripts/compile-changelog.sh` reads the commits since the previous release and
groups them by their `Attn-Seed:` trailers. It then has Claude write the
`CHANGELOG.md` section from each seed's title, description, and notes, and from
the commit messages. For a commit without a seed, and for a seed the script
cannot read, the script uses only the commit messages. Write commit messages
that say what users observe. Run the release inside attn, in the profile that
holds attn's seeds, so the script can read them.

The section ends with a marker that names the commits the section covers. The
release checks require the marker, so no commit is skipped or covered twice. A
pull request from a fork needs nothing extra, because its squashed commit
carries the pull request's title and description. Fragments left in
`changelog.d` from before go to Claude together with the commits that added
them, and the next release deletes the fragments.

## Release

Run this from a clean, current `main` whose head has green Acceptance and is
contained in `reviewed`:

```bash
./scripts/release.sh vX.Y.Z
```

When `reviewed` does not contain the head yet, the script stops. Run the
`review-main` skill, or wait for the review automation to move `reviewed`.
Otherwise the script cuts `release/vX.Y.Z` from that commit, compiles the
commits since the last release into `CHANGELOG.md`, bumps every version, and
opens a PR to `main`. Merge the PR once `PR gate` and `App acceptance` are
green. The merged commit is the release commit. CI tags and publishes the
release commit once both hold: its Acceptance is green and `reviewed` contains
it. Whichever happens last triggers the publish: CI finishing on the release
commit, or a review moving `reviewed` to contain it.

`main` may keep moving while the release PR is open. Changes that land after
the release was cut ship in the release, but this release's notes do not list
them. The next release's notes do. The review automation reviews these changes
together with the release commit before the release ships.

If the automated app acceptance cannot cover the release PR or the merged
release commit, record a manual receipt for that exact commit with the command
printed in the release PR, then rerun the CI for that commit.

If Acceptance fails on the release commit, fix the failure on `main` and
prepare the release again with the same version.

## Hotfix

Fix forward: land the fix on `main`, then release a patch version.

## After publishing

Confirm the release has the versioned DMG, `attn_aarch64.dmg`,
`attn-linux-amd64`, and `attn-linux-arm64`, then run
`brew upgrade --cask victorarias/attn/attn` and confirm that it installs the new
version. A failed publication leaves a draft and a `Release health` issue.
After you fix the failure, retry the tag. The retry, like every release run,
refuses a tag that `reviewed` does not contain:

```bash
gh api --method POST repos/victorarias/attn/dispatches \
  -f event_type=release -F 'client_payload[tag]=<tag>'
```

Update the What's New modal (`app/src/components/WhatsNewModal.tsx`) for milestones.

## The `next` branch

`next` mirrors `main` so older checkouts keep pulling. A workflow fast-forwards
`next` after every push to `main`. Nothing else writes to `next`.

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

Run this from a clean, current `main`:

```bash
./scripts/release.sh vX.Y.Z
```

The script cuts `release/vX.Y.Z` from the newest `main` commit that has green
Acceptance and that `reviewed` contains, so landings do not have to pause. It
stops when no commit since the last release qualifies, or when `reviewed` does
not yet contain the previous release commit. Then run the
`review-main` skill, or wait for the review automation to move `reviewed`.

The script compiles the commits since the last release into `CHANGELOG.md`,
bumps every version, and opens a PR to `main`. Merge the PR once `PR gate` and
`App acceptance` are green. The merged commit is the release commit. CI on `main` runs one push at a
time and drops a waiting push when a newer one arrives, but the release commit
gets a run of its own that later landings cannot drop. CI tags and publishes
the release commit once both hold: its Acceptance is green and `reviewed`
contains it. Whichever happens last triggers the publish: CI finishing on the release
commit, or a review moving `reviewed` to contain it.

`main` may have moved past that commit, and may keep moving while the release
PR is open. Commits after that commit ship in the release, but this release's
notes do not list them. The next release's notes do. The review automation reviews these changes
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
`attn-linux-amd64`, `attn-linux-arm64`, `attn-pty-host-linux-amd64`, and
`attn-pty-host-linux-arm64`, because an outpost install needs both binaries,
then run `brew upgrade --cask solenesinc/attn/attn` and confirm that it installs the new
version.

Open the upgraded app from Finder and confirm that this first launch starts, and
that macOS blocks nothing. Then confirm that the host is active and that a new
agent runs without `attn -s`:

```bash
attn settings get pty_shared_host_active    # true
ps -o command= -p "$(ps -o ppid= -p <new agent's pid>)"    # attn-pty-host ..., not attn -s
```

This is the only check of a notarized bundle that macOS starts through its
first-launch path. Tests can't reach it, because a released app always opens
production's data.

A failed publication leaves a draft and a `Release health` issue. After you fix
the failure, retry the tag. The retry, like every release run, refuses a tag
that `reviewed` does not contain:

```bash
gh api --method POST repos/SolenesInc/attn/dispatches \
  -f event_type=release -F 'client_payload[tag]=<tag>'
```

Update the What's New modal (`app/src/components/WhatsNewModal.tsx`) for milestones.

## The `next` branch

`next` mirrors `main` so older checkouts keep pulling. A workflow fast-forwards
`next` after every push to `main`. Nothing else writes to `next`.

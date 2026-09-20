# Making a release

## Changelog fragments

Each ordinary PR adds a uniquely named `changelog.d/*.yaml`:

```yaml
kind: fixed # added | changed | fixed | removed | internal
area: queue
change: Auto-settle advances to the next agent with an outstanding turn.
```

- `kind`, `area`, and `change` are required; `symptom` and `notes` are optional.
- State observable changes. Invisible work uses `kind: internal` and a one-line
  `change`; release notes omit it.
- Validate with `go run ./cmd/changelog-check` or `./scripts/changelog-gate.sh next`.
- Direct `CHANGELOG.md` edits are for release compilation, copy corrections,
  and [unpublished-release repairs](#repair-an-unpublished-release).

## Gates

- Ordinary branches/PRs follow [working-with-next.md](working-with-next.md).
- `PR gate` checks the proposed merge; `Acceptance` checks the exact merged SHA
  on `next`/`main`. Filtered/skipped jobs cannot grant Acceptance.
- Promotions require accepted source SHA, current `main` baseline, matching
  versions, absent tag, consumed fragments, and a release-only preparation diff.
  User-facing fragments require compiled changelog copy with the generated receipt.
- Automatic candidates need exact-head `App acceptance` from `ci.yml` or the
  protected-`main` manual override. Hotfixes earn their own `PR gate` and `App
  acceptance`; they do not inherit `next` Acceptance. Held promotions are CI-only
  and cannot create a tag or dispatch a release.
- Only validated candidates and generated sync PRs get changelog exemptions.

## Prepare a frozen candidate

From a clean, current local `next`:

```bash
git switch next
git pull --ff-only origin next
./scripts/release.sh vX.Y.Z
```

The script requires green exact-SHA Acceptance, no existing tag or open candidate.
It creates `release/vX.Y.Z`, compiles fragments, updates versions, writes
`.github/release-candidate.yml`, and opens a ready PR to `main`.
It does not merge, tag, or publish. Internal-only fragments produce no release-note section.

Later `next` changes, including red Acceptance, do not change the frozen source.
If `main` moves, close and prepare the candidate again. Product fixes to a
promotion go through `next` and a newly accepted source.

## Promote without publishing

Use a held promotion to exercise the complete `next` to accepted-`main` path
without claiming packaged-app verification or creating public release state:

```bash
./scripts/release.sh vX.Y.Z --hold
```

The manifest records `publication: held`. The candidate still needs exact-source
Acceptance and green normal PR CI. After merge, `main` runs full Acceptance and
the release controller validates the candidate, then exits before App acceptance,
tag creation, or release dispatch. The committed hold also stops later `main`
runs from publishing that version.

Sync held `main` back into `next` normally. A held version is never published;
prepare a new candidate and version when a public release is wanted.

## Prepare a post-release hotfix

Branch `hotfix/*` from current `main`; commit the fix and changelog fragment.
On the clean branch, run `make release-hotfix VERSION_TAG=vX.Y.Z` with a fresh version.
It commits release preparation and a fresh `kind: hotfix` manifest, then opens
a ready PR to `main`. If `main` moves, prepare again from current `main`.

## App acceptance

`App acceptance` verifies the packaged app at the exact head. Linux exercises
the serial scenario matrix; `App acceptance macOS` exercises native Cmd+C
terminal copying.

Acceptance runs before merge for PRs changing the app, daemon, plugins, harness,
or build inputs. Documentation-only PRs may skip it. Pushes to `next` and `main`
and PRs onto `main` always require it.

The protected-main manual override and the candidate receipt on `main` can
substitute for Linux acceptance; macOS native clipboard verification must still
pass.

Use the command generated in the candidate PR when the automated job cannot
cover the candidate and a manual override is warranted:

```bash
gh workflow run app-acceptance.yml --ref main \
  -f candidate_sha=<full-candidate-SHA> -f profile=<profile> \
  -f scenarios='<scenarios run>' -f evidence='<recording URL or receipt>' \
  -f outcome=passed
```

Record failures with `outcome=failed`. Any head change needs a new receipt.
Keep the override workflow on protected `main`; it checks out the candidate
separately. A manual dispatch does not restart candidate CI, so rerun it after
recording the override. Merge only with green `PR gate`, `App acceptance`, and
required approval.

## Accept and release main

- Merged `main` must earn exact-SHA Acceptance before tagging/publication.
  Failure leaves it untagged and opens a branch-health issue.
- `Release accepted main` validates current `main`, an automated or manual app
  receipt, identical candidate/main trees, manifest, and versions.
  It creates the immutable tag and dispatches protected-`main` `release.yml`.
- A held promotion validates through `main` Acceptance and exits before the app
  receipt, tag, and dispatch steps.
- Manual tag pushes cannot publish. Dispatches revalidate acceptance and tag
  identity; builds use the validated SHA and publishing rechecks the tag.
- Retain the manifest for sync. A published version needs a fresh manifest/version
  for later fixes.

## Repair an unpublished release

If `main` Acceptance fails, make a minimal `hotfix/*` PR from `main`.
Keep the manifest only while its version is unpublished; update `CHANGELOG.md`
directly, leaving no pending fragments. Obtain PR/app acceptance, merge, then
wait for the repaired `main` SHA's Acceptance.

## Sync main back into next

After every `main` change, run `./scripts/sync-main-to-next.sh`.
It refuses while a candidate is open; otherwise it merges `main` into `next`
in a temporary worktree and opens `sync/main-into-next-*` against `next`.
It removes only fragments unchanged from the frozen source; rewritten fragments
require inspection, and later fragments survive.

Merge the sync PR with a merge commit. Wait for sync and green `next` Acceptance
before preparing another candidate.

## Release preflight

The `Release Preflight` workflow builds and validates the Linux daemon and app
runtime host on amd64 and arm64, and uploads artifacts without publishing a release.

## Release artifacts

The release workflow publishes the signed and notarized macOS app, the Homebrew
DMG, and Linux daemon binaries for amd64 and arm64. The macOS job needs
`APPLE_CERTIFICATE`, `APPLE_CERTIFICATE_PASSWORD`, `KEYCHAIN_PASSWORD`,
`APPLE_API_ISSUER`, `APPLE_API_KEY`, and `APPLE_API_KEY_P8` secrets.

After publication, confirm the release contains the versioned DMG,
`attn_aarch64.dmg`, `attn-linux-amd64`, and `attn-linux-arm64`. Verify the Homebrew
path with `brew upgrade --cask victorarias/attn/attn`.

Publication waits for all build, bundled-plugin, signing, and notarization gates.
Failure leaves a draft and opens `Release health`. Fix the cause, then retry
the same tag only while it still names current `main`:

```bash
gh api --method POST repos/victorarias/attn/dispatches \
  -f event_type=release -F 'client_payload[tag]=<tag>'
```

If `main` advanced, prepare a fresh candidate/version. Successful retry replaces
assets, publishes, and closes the issue. Publishing an older version does not
move GitHub's latest release or the Homebrew stable-download path backward.

## What's New modal

Write milestone copy in `app/src/components/WhatsNewModal.tsx` from changelog
sections since the previous `WHATS_NEW_ID` in `app/src/hooks/useWhatsNew.ts`.

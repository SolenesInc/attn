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
- Every candidate needs protected-`main` `App acceptance` for its exact head.
  Rerun candidate CI after recording it. Hotfixes earn their own `PR gate`
  and `App acceptance`; they do not inherit `next` Acceptance.
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
`.github/release-candidate.yml`, and opens a draft PR to `main`.
It does not merge, tag, or publish. Internal-only fragments produce no release-note section.

Later `next` changes, including red Acceptance, do not change the frozen source.
If `main` moves, close and prepare the candidate again. Product fixes to a
promotion go through `next` and a newly accepted source.

## Prepare a post-release hotfix

Branch `hotfix/*` from current `main`; commit the fix and changelog fragment.
On the clean branch, run `make release-hotfix VERSION_TAG=vX.Y.Z` with a fresh version.
It commits release preparation and a fresh `kind: hotfix` manifest, then opens
a draft PR to `main`. If `main` moves, prepare again from current `main`.

## Record app acceptance

Install and exercise the packaged app from the exact candidate head.
Use the command generated in its draft PR:

```bash
gh workflow run app-acceptance.yml --ref main \
  -f candidate_sha=<full-candidate-SHA> -f profile=<profile> \
  -f scenarios='<scenarios run>' -f evidence='<recording URL or receipt>' \
  -f outcome=passed
```

Record failures with `outcome=failed`. Any head change needs a new receipt.
Keep the workflow on protected `main`; it checks out the candidate separately.
Mark ready after reviewing changelog copy and obtaining green `PR gate` and
`App acceptance`. Merge only with those checks and required approval.

## Accept and release main

- Merged `main` must earn exact-SHA Acceptance before tagging/publication.
  Failure leaves it untagged and opens a branch-health issue.
- `Release accepted main` validates current `main`, its originating candidate's
  protected-main app receipt, identical candidate/main trees, manifest, and versions.
  It creates the immutable tag and dispatches protected-`main` `release.yml`.
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

Merge the sync PR with a merge commit. Never squash/rebase it or cherry-pick
the hotfix into `next`. Wait for sync and green `next` Acceptance before
preparing another candidate.

## Release artifacts

Publication waits for all build, bundled-plugin, signing, and notarization gates.
Failure leaves a draft and opens `Release health`. Fix the cause, then retry
the same tag only while it still names current `main`:

```bash
gh api --method POST repos/victorarias/attn/dispatches \
  -f event_type=release -F 'client_payload[tag]=<tag>'
```

If `main` advanced, prepare a fresh candidate/version. Successful retry replaces
assets, publishes, and closes the issue.

## What's New modal

Write milestone copy in `app/src/components/WhatsNewModal.tsx` from changelog
sections since the previous `WHATS_NEW_ID` in `app/src/hooks/useWhatsNew.ts`.

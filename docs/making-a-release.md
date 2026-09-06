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

`ci.yml` builds the packaged Linux app from the exact head in `App acceptance
build`, and four `App acceptance shard N/4` jobs each install that one tree and
run a slice of the real-app serial matrix under Xvfb. The slices are balanced by
`app/scripts/real-app-harness/scenario-durations.json`, a duration per scenario
recorded from a green run.

`App acceptance` is the verdict and the only name the gates read. It aggregates
every shard's `last-matrix.json`, so it reds when a scenario failed, when a
scenario ran in no shard or in two, or when a shard reported nothing at all. It
also accepts the candidate receipt on `main` and the protected-`main` override.

The run costs around 9 minutes of wall time and about 22 minutes of runner time,
so it runs only where its receipt is needed:
pushes to `next` and `main`, PRs onto `main` (`release/v*` and `hotfix/*`, the
only branches `main-route.sh` admits), and PRs that touch the harness, the Tauri
shell, the app runtime host, or the job's own definition — the `harness` filter
in the `changes` job lists the paths. Every other PR skips it, and `PR gate`
accepts `skipped`. For those changes the receipt is the `Acceptance` run on
`next` after merge, which still requires `App acceptance` to succeed; a red
there blocks release eligibility until it is fixed.

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

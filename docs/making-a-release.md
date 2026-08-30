# Making a release

Release preparation freezes an accepted `next` commit, compiles its changelog
fragments, and opens a candidate PR to `main`. Publication follows acceptance
of the merged commit. For everyday branches and PRs, see
[Working with `next`](working-with-next.md).

## Changelog fragments

Each ordinary PR adds one YAML file under `changelog.d/`. Use a unique name
such as `<branch>-<short-slug>.yaml` so concurrent PRs avoid changelog conflicts.

```yaml
# changelog.d/amber-manatee-handover.yaml
kind: fixed            # added | changed | fixed | removed | internal
area: queue            # the subsystem touched, free-form
change: >
  The queue advances to the next agent with an outstanding turn whenever a
  turn closes, including through auto-settle.
symptom: >             # optional: what the user noticed before a fix
  Auto-settle left you on a finished agent while another needed your attention.
notes: >               # optional: context for the release writer
  Same root cause as the sidebar-row settle path; both fixed here.
```

State what changed and what a user would notice. The release writer combines
related fragments and turns them into user-facing copy once per release.

Rules:

- `kind`, `area`, and `change` are required. `kind` maps to the changelog
  category (`added` → "### Added", etc.).
- Use `kind: internal` and a one-line `change` for work with no user-visible
  behavior. The writer reads internal fragments but omits them from release notes.
- `go run ./cmd/changelog-check` validates fragments locally; the same check
  runs in CI.

Direct `CHANGELOG.md` edits are reserved for release compilation, corrections
to existing copy, and [repairs to an unpublished release](#repair-an-unpublished-release).

## The release flow

Release branches freeze one accepted `next` commit. Later work on `next` waits
for the following release.

```text
feature/fix PR ──squash──▶ next ──Acceptance──▶ accepted next SHA
                              │
                              ├── more PRs may keep landing
                              │
accepted next SHA ──freeze──▶ release/vX.Y.Z ──PR──▶ main
                                                       │
                                                       ├── Acceptance ──▶ release
                                                       │
                                                       └── merge-commit sync ──▶ next
```

CI exposes two stable results:

- `PR gate` says a proposed merge passed every applicable check.
- `Acceptance` reruns every check against the exact commit now on `next` or
  `main`. A filtered or skipped job cannot produce acceptance.

A branch-health issue opens when Acceptance fails and closes when the same
branch recovers. Real-app verification remains a manual candidate step until
the app and its harness run reliably on Linux CI.

## The PR gates

The `Changelog` job requires an added fragment or a `CHANGELOG.md` edit, except
for validated release candidates and generated sync PRs.

A promotion candidate must prove its exact source Acceptance, recorded source
and `main` SHAs, versions, absent tag, consumed fragments, and release-only
preparation diff. User-facing fragments require a compiled `CHANGELOG.md`
update. Every candidate also needs `App acceptance` for its exact head from
the protected `main` workflow. The first candidate CI run waits on this manual
receipt; rerun it after recording acceptance. A prepared hotfix earns its own
`PR gate` and `App acceptance` without inheriting source Acceptance from `next`.

`Main route` enforces the [branch rules](working-with-next.md#releases-hotfixes-and-syncs).
Run the changelog gate locally with `./scripts/changelog-gate.sh next`.

## Prepare a frozen candidate

Start from a clean, current local `next`:

```bash
git switch next
git pull --ff-only origin next
./scripts/release.sh v0.9.5
```

The command refuses a stale or divergent branch, a missing or red exact-SHA
`Acceptance`, an existing tag, or another open candidate. It creates
`release/v0.9.5` from the accepted `next` head, compiles the changelog, updates
all committed versions, writes `.github/release-candidate.yml`, validates the
release-only diff, and opens a draft PR to `main`. It never merges, tags, or
starts a release.

The draft PR links the source Acceptance and both frozen SHAs. It also carries
the raw changelog inputs so the compiled copy can be reviewed without
reconstructing the candidate. The compiled section includes a hidden SHA-256
receipt over the frozen fragment paths and Git blobs; the candidate gate checks
that receipt, so an unrelated `CHANGELOG.md` edit cannot stand in for compiling
the release notes.

If every pending fragment is `internal`, the script removes the fragments
without adding a section.

## Prepare a post-release hotfix

A fix made after a version is published needs its own version and manifest.
Start from current `main`, commit the product fix and its changelog fragment,
then prepare the same branch as a hotfix candidate:

```bash
git switch main
git pull --ff-only origin main
git switch -c hotfix/startup-crash
# implement the fix, add changelog.d/startup-crash.yaml, and commit both
make release-hotfix VERSION_TAG=v0.9.6
```

The command requires a clean `hotfix/*` branch containing current `main`. It
records the fix commit as the hotfix source, compiles its changelog fragment,
updates every committed version, writes a fresh `kind: hotfix` manifest, and
adds one release-preparation commit in place. It opens a draft PR to `main`
using the fix commit's title.

## Record app acceptance

Install and exercise the packaged app from the exact candidate head. Record the
profile, scenarios, and evidence with the command generated in the draft PR:

```bash
gh workflow run app-acceptance.yml \
  --ref main \
  -f candidate_sha=<full candidate SHA> \
  -f profile=<profile> \
  -f scenarios='<scenarios run>' \
  -f evidence='<recording URL or concise receipt>' \
  -f outcome=passed
```

The workflow and receipt script always load from protected `main`; they check
out `candidate_sha` separately and fail if that checkout differs. This keeps a
candidate from changing the workflow that accepts it. A failed manual run
should be recorded with `outcome=failed`. Any candidate edit creates a new head
and requires a new receipt.

Make the PR ready for review only after the changelog reads well and the exact
head has both `PR gate` and `App acceptance` green.

If `next` Acceptance turns red after a promotion freezes, the candidate remains
valid because its source SHA was accepted. If `main` moves, close and prepare
the candidate again. Product fixes to a promotion go through `next` and a new
accepted source; post-release hotfixes start again from current `main`.

## Accept and release main

Merge the candidate to `main` only after its PR gate and manual app verification
pass. CI reruns the same suite on the resulting `main` SHA. A release may start
only after that exact SHA has green `Acceptance`; a red run leaves `main`
untagged and opens the branch-health issue.

`Release accepted main` is serialized across versions. It checks the triggering
CI run's own `Acceptance` job, verifies that the SHA is still the current
`main`, resolves the promotion or hotfix PR that produced that SHA, and requires
green `App acceptance` from the protected `main` workflow for the last commit
in the merged PR's commit record. The
app-accepted candidate and merged `main` must have the same Git tree, so a base
change cannot add untested code during the squash merge. It then validates the
candidate manifest and every committed version source before
inspecting or creating the immutable tag, and explicitly dispatches the
protected `main` copy of `release.yml` with that tag as input. A retry
recognizes both an existing exact tag and an existing release run for the same
commit, so it cannot start a second release.

`release.yml` accepts the `release` repository event only, so GitHub always
loads it from protected `main`. Pushing a `v*` tag by hand cannot start
publication. The validation job treats the requested tag only as data. Every dispatch
independently proves that the tag is the exact current `main`, the SHA has a green
`Acceptance` job from `ci.yml`, the originating candidate has a green exact-head
receipt from the protected `main` copy of `app-acceptance.yml`, and the manifest
and committed versions agree. Runs for the same tag are serialized. The
accepted-main gate is the sole authority that dispatches a release
automatically.

The validation job passes the accepted commit SHA to every build job. Builds
check out that SHA, and the publish job resolves the remote tag again before it
makes the draft public. A moved tag cannot swap the code after validation.
GitHub selects the latest release atomically by stable semantic version, so
overlapping versions cannot move the latest or Homebrew path backward.
Once accepted-main creates the immutable tag, it dispatches that release even
if a later `main` commit lands; abandoning the tag would strand that version.

The manifest stays in Git history so `next` can reconcile the release. Once its
tag points to an earlier `main` SHA, automation treats it as consumed. Later
`main` changes need a fresh version and manifest.

## Repair an unpublished release

If `main` Acceptance fails, make the smallest `hotfix/*` PR from `main`, merge
it after its PR gate and manual verification pass, and wait for the repaired
`main` SHA to earn Acceptance. Update `CHANGELOG.md` directly for this
pre-release repair of the still-unpublished version; pending fragments make the
accepted-main validator stop. A hotfix may keep the existing manifest only while
its version remains unpublished.

## Sync main back into next

After every change to `main`:

```bash
./scripts/sync-main-to-next.sh
```

The command refuses to start while a release candidate PR is open. It fetches
`main` and `next`, creates an isolated temporary worktree, merges `main` into
`next`, and calls `release-train sync apply`. Promotion sync removes a fragment
only when its blob is identical to the one recorded at the frozen source SHA;
later fragments survive, and a rewritten fragment stops the command for human
inspection.

The command pushes a `sync/main-into-next-*` branch and opens a PR to `next`.
CI checks its exact parents, generated merge tree, and deletion of only the
frozen fragments before granting the changelog exemption.

Merge that PR with a merge commit. Never squash or rebase it, or cherry-pick
the hotfix into `next`; those operations lose the ancestry link. Prepare the
next candidate only after the sync completes and `next` earns Acceptance again.

## Release artifacts

The release workflow builds the app, stages the bundled plugins into it, checks
they survived into the bundle, notarizes and staples, and builds the Linux
daemons against a draft release. Publication waits for every gate. On failure,
the release stays a private draft and `Release health` opens one issue for the
tag with the failed run and draft receipt. Fix the cause and retry the same tag
while `main` still points to it:

```bash
gh api --method POST repos/victorarias/attn/dispatches \
  -f event_type=release -F 'client_payload[tag]=<tag>'
```

The retry replaces assets, publishes after all gates pass, and closes the issue.
If `main` has advanced, prepare a fresh candidate and version.

## What's New modal

The in-app What's New modal (`app/src/components/WhatsNewModal.tsx`,
`WHATS_NEW_ID` in `app/src/hooks/useWhatsNew.ts`) is hand-written per milestone.
When updating it, draw from the
compiled changelog sections since the last `WHATS_NEW_ID` bump.

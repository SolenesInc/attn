# Making a release

attn's changelog is compiled, not accreted. PRs never edit `CHANGELOG.md`;
each PR ships a small **changelog fragment** — a raw statement of what changed
— and a release-time compilation step turns the accumulated fragments into one
curated, user-facing `CHANGELOG.md` section. This removes the merge conflicts
that concurrent branches used to hit on `CHANGELOG.md`, and it means the
changelog is written once per release by a writer that saw the whole release,
instead of thirty times by authors who each saw one PR.

## Per PR: add a changelog fragment

Every PR adds one YAML file under `changelog.d/`. Name it
`<branch>-<short-slug>.yaml` — uniqueness across in-flight branches is what
keeps fragments conflict-free.

```yaml
# changelog.d/amber-manatee-handover.yaml
kind: fixed            # added | changed | fixed | removed | internal
area: queue            # the subsystem touched, free-form
change: >
  Closing a turn now hands over the next agent that owes one regardless of
  how the turn closed, not only via Cmd+Shift+E.
symptom: >             # optional — for fixes, what the user noticed before
  Auto-settle finishing on the agent you were watching left you sitting on a
  done agent instead of moving you to the next one.
notes: >               # optional — extra context for the release writer
  Same root cause as the sidebar-row settle path; both fixed here.
```

A fragment is **evidence for the release writer, not final copy**. State
plainly what changed and what a user would notice; do not attempt release-note
voice. The writer merges related fragments, sets the tone, and decides what
makes the cut.

Rules:

- `kind`, `area`, and `change` are required. `kind` maps to the changelog
  category (`added` → "### Added", etc.).
- A change with no user-visible behavior still ships a fragment, with
  `kind: internal` and a one-line `change`. Internal fragments give the
  release writer the full picture; they never become bullets.
- `go run ./cmd/changelog-check` validates fragments locally; the same check
  runs in CI.

## The branch flow

Normal work lands on `next`. `main` changes only when an accepted release or an
urgent repair is ready:

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

The release branch is a snapshot of one accepted `next` commit. Work merged to
`next` after that point waits for the following release; it cannot drift into
the open candidate.

CI exposes two stable results:

- `PR gate` says a proposed merge passed every applicable check.
- `Acceptance` reruns every check against the exact commit now on `next` or
  `main`. A filtered or skipped job cannot produce acceptance.

A branch-health issue opens when Acceptance fails and closes when the same
branch recovers. Real-app verification remains a manual candidate step until
the app and its harness run reliably on Linux CI.

## The PR gates

The `Changelog` job in CI fails any PR that neither adds a
`changelog.d/*.yaml` fragment nor modifies `CHANGELOG.md`. Touching
`CHANGELOG.md` directly is the escape hatch for the compilation PR itself and
for hand-fixes to existing copy. Frozen `release/vX.Y.Z` candidates are exempt
because an all-internal release deletes its fragments without adding user-facing
copy. Generated `sync/main-into-next-*` PRs are also exempt because they only
reconcile fragments already represented in the released changelog. The
candidate and sync validators enforce those narrower diffs. Run the gate locally
with `./scripts/changelog-gate.sh next`.

The `Main route` job rejects ordinary PRs aimed at `main`. These are the only
allowed routes:

| Head branch | Base branch | Purpose |
| --- | --- | --- |
| feature, fix, or completed `epic/*` | `next` | normal work |
| `release/vX.Y.Z` | `main` | frozen promotion candidate |
| `hotfix/*` | `main` | urgent repair |
| `epic/release-train` | `main` | one-time workflow bootstrap |

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
reconstructing the candidate.

If every pending fragment is `internal`, the script removes the fragments
without adding a section.

## Record app acceptance

Install and exercise the packaged app from the exact candidate head. Record the
profile, scenarios, and evidence with the command generated in the draft PR:

```bash
gh workflow run app-acceptance.yml \
  --ref release/v0.9.5 \
  -f candidate_sha=<full candidate SHA> \
  -f profile=<profile> \
  -f scenarios='<scenarios run>' \
  -f evidence='<recording URL or concise receipt>' \
  -f outcome=passed
```

The `App acceptance` check attaches to the commit selected by `--ref` and fails
if it differs from `candidate_sha`. A failed manual run should be recorded with
`outcome=failed`, which leaves a red receipt on that candidate. Any candidate
edit creates a new head and requires a new receipt.

Make the PR ready for review only after the changelog reads well and the exact
head has both `PR gate` and `App acceptance` green.

If `next` Acceptance turns red after the freeze, the candidate remains valid:
it points to the exact accepted source SHA. If `main` moves, or the candidate
needs a product-code fix, close it and cut a new candidate from a newly accepted
source.

## Accept and release main

Merge the candidate to `main` only after its PR gate and manual app verification
pass. CI reruns the same suite on the resulting `main` SHA. A release may start
only after that exact SHA has green `Acceptance`; a red run leaves `main`
untagged and opens the branch-health issue.

```text
main SHA ──CI──▶ Acceptance green ──validate manifest + versions──▶ vX.Y.Z
   │                                                                    │
   │ red or stale                                                       │ explicit dispatch
   └────────────────────────────▶ no tag                                ▼
                                                         draft release + all assets
                                                                    │
                                                        every build gate passes
                                                                    ▼
                                                              public release
```

`Release accepted main` is serialized across versions. It checks the triggering
CI run's own `Acceptance` job, verifies that the SHA is still the current
`main`, validates the candidate manifest and every committed version source,
then creates the immutable tag and explicitly dispatches `release.yml` from
that tag. A retry recognizes both an existing exact tag and an existing release
run for the same commit, so it cannot start a second release.

The manifest stays in Git history so `next` can reconcile the release. Once its
tag points to an earlier `main` SHA, release automation treats that manifest as
consumed and exits cleanly. It never reuses a published version for a later
`main` change. A post-release hotfix therefore needs a fresh version and
manifest; the hotfix preparation path is an activation prerequisite.

If `main` Acceptance fails, make the smallest `hotfix/*` PR from `main`, merge
it after its PR gate and manual verification pass, and wait for the repaired
`main` SHA to earn Acceptance. Update `CHANGELOG.md` directly for this
pre-release repair; pending fragments make the accepted-main validator stop.
Release that repaired SHA, not the failed one.

## Sync main back into next

After every release or urgent hotfix merge:

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
Merge that PR with a **merge commit**. Squashing or rebasing it loses the
ancestry link, so another candidate must not be prepared until the merge-commit
sync is complete and `next` earns Acceptance again.

Routine fixes always go through `next`. For an urgent production fix, branch
`hotfix/*` from `main`, target `main`, then run the same sync command. Do not
cherry-pick the hotfix into `next`; the merge-commit sync is the single path
back and prevents the two lines from silently diverging.

## Release artifacts

The release workflow builds the app, stages the bundled plugins into it, checks
they survived into the bundle, notarizes and staples, and builds the Linux
daemons, all against a **draft** release. It is published only after every one
of those has passed. A failed release run leaves any partial release as a
private draft, not a half-built public release. `Release health` opens one issue
for that tag with the exact run and draft receipt. Fix the cause and rerun the
workflow on the same tag
(`gh workflow run release.yml --ref <tag> -f tag=<tag>`); the rerun replaces
the assets, publishes when the whole set is right, and closes the issue.

## What's New modal

The in-app What's New modal (`app/src/components/WhatsNewModal.tsx`,
`WHATS_NEW_ID` in `app/src/hooks/useWhatsNew.ts`) stays hand-written — it
tells a story per milestone, not per release. When updating it, draw from the
compiled changelog sections since the last `WHATS_NEW_ID` bump.

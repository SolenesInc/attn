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
for hand-fixes to existing copy. Generated `sync/main-into-next-*` PRs are
exempt because they only reconcile fragments already represented in the
released changelog. Run the gate locally with `./scripts/changelog-gate.sh
next`.

The `Main route` job rejects ordinary PRs aimed at `main`. These are the only
allowed routes:

| Head branch | Base branch | Purpose |
| --- | --- | --- |
| feature, fix, or completed `epic/*` | `next` | normal work |
| `release/vX.Y.Z` | `main` | frozen promotion candidate |
| `hotfix/*` | `main` | urgent repair |
| `epic/release-train` | `main` | one-time workflow bootstrap |

## Prepare a frozen candidate

Start only from an exact `next` SHA whose `Acceptance` run is green. Record that
SHA, the current `main` SHA, and confirm there is no other open
`release/vX.Y.Z` PR. Then create the candidate from the accepted source:

```bash
git fetch origin main next
source_sha="$(git rev-parse origin/next)"
main_sha="$(git rev-parse origin/main)"
git switch -c release/v0.9.5 "$source_sha"

./scripts/compile-changelog.sh            # or --dry-run to inspect the prompt
go run ./cmd/release-train version set v0.9.5
(cd app && pnpm install --frozen-lockfile)
(cd app/src-tauri && cargo check -q)
go run ./cmd/release-train manifest write \
  --version v0.9.5 --kind promotion \
  --source "$source_sha" --main "$main_sha"
```

The compile script validates the pending fragments, has `claude` write a dated
`CHANGELOG.md` section from them, inserts it at the top of the file, and deletes
the consumed fragments. Everything is left staged. Review and edit the section,
commit the release-only changes, and open a PR to `main`.

If every pending fragment is `internal`, the script removes the fragments
without adding a section.

Before merging, inspect the changes between the recorded source and the
candidate, run the required real-app scenarios from the candidate, and record
the manual result in the PR. The validation command checks the frozen source,
the unchanged `main` baseline, version agreement, release-only diff, consumed
fragments, tag availability, and the single-candidate rule:

```bash
go run ./cmd/release-train candidate validate \
  --current-main origin/main \
  --source-acceptance success \
  --other-open-candidates 0
```

If `next` Acceptance turns red after the freeze, the candidate remains valid:
it points to the exact accepted source SHA. If `main` moves, or the candidate
needs a product-code fix, close it and cut a new candidate from a newly accepted
source.

## Accept and release main

Merge the candidate to `main` only after its PR gate and manual app verification
pass. CI reruns the same suite on the resulting `main` SHA. A release may start
only after that exact SHA has green `Acceptance`; a red run leaves `main`
untagged and opens the branch-health issue.

If `main` Acceptance fails, make the smallest `hotfix/*` PR from `main`, merge
it after its PR gate and manual verification pass, and wait for the repaired
`main` SHA to earn Acceptance. Release that repaired SHA, not the failed one.

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
of those has passed. A failed release run leaves a draft, not a half-built
public release: fix the cause and rerun the workflow on the same tag
(`gh workflow run release.yml -f tag=<tag>`), which replaces the assets and
publishes when the whole set is right.

## What's New modal

The in-app What's New modal (`app/src/components/WhatsNewModal.tsx`,
`WHATS_NEW_ID` in `app/src/hooks/useWhatsNew.ts`) stays hand-written — it
tells a story per milestone, not per release. When updating it, draw from the
compiled changelog sections since the last `WHATS_NEW_ID` bump.

# Trunk-based development

attn is developed trunk-based. Maintainers and their agents commit straight to
`main`. No pull request stands between a change and `main`. Reviewers read each
change twice: before it lands, scaled to its risk, and again on `main`
afterwards. A release ships only commits that have passed both CI and review on `main`.
Contributors working from a fork use [pull requests](pull-requests.md).

## Landing

A landing is one push of finished commits to `main` through `scripts/land.sh`.
Work lands in slices, each as soon as it works, so no branch waits and collects
conflicts. Unfinished work lands unwired, or behind an experimental
setting that is off by default. The `land-on-main` skill is the
procedure, from the working branch to the report after the push.

After the push, these run without you. `main`'s CI runs the full test suites and
the scenario matrix. When a landing breaks `main`, the sheriff gets `main` green
again. The review automation reviews the commits and messages their author or
dispatches a fixer. The release process writes release notes from the commits.

### What land.sh does

`scripts/land.sh` rebases onto `origin/main`, runs a fast gate for the paths the
landing changes, and pushes:

- Go: compile all packages and tests, run vet and staticcheck on changed
  packages, and check daemon lifetimes and import boundaries. A module change
  checks all packages.
- Protocol schema or prompt inputs: check that generated types or catalogs
  are current.
- `app/`: lint, and typecheck with tsrs, a Rust port of the TypeScript 7
  checker. CI on `main` still typechecks with the TypeScript version that
  `app/package.json` pins.
- `scripts/`: run the shell script tests that cover the changed scripts. A
  script test covers the files it names, and the files those scripts name in
  turn, so a change to `scripts/lib/` runs every test that sources it. A test
  covers itself. `land.sh` prints each test it chose and the changed files that
  chose it, and says so when no script test covers the change. `make
  test-scripts` runs them all, as CI does.
- The landing tests. `land.sh` chooses a few tests for the commit it is about to
  push, not a suite: the groups of `scripts/landing-tests` that match the
  changed paths, the tests the landing changes, and every test that failed on
  `main` and has since passed there, until it clears. A test that is still red
  on `main` is listed, not run. `land.sh` prints each test it added and why.
  [Test history](test-history.md#selection) specifies the choice, and
  `scripts/land.sh --baseline` lands with the baseline groups alone.

When only protocol types or prompt catalogs conflict, `land.sh` regenerates
them. When `main` moves during landing, `land.sh` rebases, runs the gate again
on the new combination, and pushes.

## Verifying before landing

Checks before landing are fast and local: targeted tests, the change on the
surface it touches, and a review scaled to the change's risk. The full Go and
frontend suites and the scenario matrix, macOS included, run in CI on `main`
after landing, and so do the combinations of changes that landed together.

Reviewers catch a defect before landing, while the author still remembers the
work and before other slices build on the defect. The review automation on `main` still reads every commit afterwards, with the
slices of a plan side by side.

The Acceptance soak workflow repeats chosen scenarios to hunt flaky ones. It is
not a landing step.

## Commits

Commits made inside attn carry an `Attn-Session:` trailer and one `Attn-Seed:`
per seed the session works on. Work users will observe has a seed. The
trailers let reviewers, the sheriff on a red `main`, and whoever writes the release notes find
the work and the session behind a commit.

## Review on main

A scheduled review automation runs Codex over the commits `main` has gained
since the `reviewed` branch, grouped by seed, and judges each seed's commits
against its plan. For each finding, the automation messages the session named by
`Attn-Session:` while that session is live, and otherwise dispatches an agent
to fix the finding. Then the automation moves `reviewed` forward. The
`review-main` skill is the automation's procedure.

When an Orchestrator coordinates Builders, the Orchestrator reviews their work
after landing. When a seed check is rejected, attn rings the Builder through the
delegation watch, even after harvest. Corrections are new landings.

## When main is red

CI runs on `main` after every landing, and it batches pushes that arrive during
a run into the next run. When Acceptance fails, CI opens the
`Branch health: main acceptance` issue. The sheriff automation watches `main`'s
CI and counts a run as red when any job in it failed, even when the run itself
concluded `cancelled`. The sheriff never re-runs CI. As soon as a job fails, the
sheriff reads it, finds the commit that broke it, and delegates an agent
to fix forward when the fix is small and clear, or to revert the commit when the
revert is clean. The fix lands like any other, and the next push run on
`main` shows whether the fix worked. The issue records each failure once, with
the failure's owner and the repair that landed, so the sheriff does not fix a
failure that already has an owner or a repair. Every revert gets an owner who
will land the change again: the commit's live session, or a seed that the chief
receives. When neither fixing forward nor reverting is clearly right, the sheriff asks the
user. The `sheriff` skill is the sheriff's procedure. Keep landing while `main` is red. A
release waits for green.

## Releases

A release is cut on demand from a `main` commit that has green Acceptance and
that `reviewed` contains. Its notes are written from the commits since the last
release, grouped by their `Attn-Seed:` trailers, so landings add no changelog
fragment. See [Making a release](making-a-release.md).

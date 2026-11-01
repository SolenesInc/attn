# Trunk-based development

attn is developed trunk-based. Maintainers and their agents commit straight to
`main`. No pull request stands between a change and `main`. Review comes twice:
before the change lands, scaled to its risk, and again on `main` afterwards. A
release ships only commits that have passed both CI and review on `main`.
Contributors working from a fork use [pull requests](pull-requests.md).

## Landing

A landing is one push of finished commits to `main` through `scripts/land.sh`.
Work lands in slices, each as soon as it works, so no branch sits waiting and
turns into conflicts. Unfinished work lands unwired, or behind an experimental
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
- `app/`: typecheck and lint.
- `scripts/`: run the shell script tests.
- The landing tests, which the script chooses for the commit it is about to
  push: the baseline (the groups in `scripts/landing-tests` whose path pattern
  matches the changes), the tests the landing changes, and every test that
  failed on `main` and has since passed there, until the test has passed on 30
  later commits of `main`. They are a few tests, not a suite. The script prints
  each test it added and why, each test's result (skipped tests included), and
  the command that reruns a failed test.

When only protocol types or prompt catalogs conflict, `land.sh` regenerates
them. When `main` moves during landing, `land.sh` rebases, runs the gate again
on the new combination, and pushes.

### How the landing tests evolve

attn's test runners record every run, and a shared test history collects the
runs of CI and of every machine. `land.sh` syncs the history before it chooses
the tests and again after it pushes. [Test history](test-history.md) is the
specification.

- A test that fails on a clean commit of `main`, in CI or on any machine, is
  red. A red test blocks no landing, unless the test is in the baseline or the
  landing changes the test. `land.sh` lists a red test and leaves it to the
  sheriff. Once the test has passed on `main` again, it joins every landing that
  runs landing tests.
- The test stops joining landings once it has passed afresh on 30 later commits
  of `main`, in the lane where it failed. A commit where the test did not run
  does not count, and a new failure starts the count over.
- Runs of unlanded or uncommitted work never count as `main`'s health.
- A landing only counts a failed test that is gone from the checkout. Native
  scenarios have no landing recipe and stay with CI. A landing lists a native
  scenario that has not cleared as unavailable.
- The baseline still grows from red `main`. When the sheriff's repair lands,
  the test that caught the failure joins its group in `scripts/landing-tests` if
  the test runs in seconds.

Without a synced history, `land.sh` runs the baseline and says so.
`scripts/land.sh --baseline` lands with the baseline alone. Use `--baseline`
when the history or the choice of tests misbehaves, and never to skip a failing
test.

## Verifying before landing

Checks before landing are fast and local: targeted tests, the change on the
surface it touches, and a review scaled to the change's risk. The full Go and
frontend suites and the scenario matrix, macOS included, run in CI on `main`
after landing, and so do the combinations of changes that landed together.

Reviewers catch a defect before landing, while the author still remembers the
work and before other slices build on the defect. The review on `main` still reads every commit afterwards, with the slices of a plan side by side.

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

CI runs on `main` after every landing. Pushes that arrive during a run are
batched into the next run. A red `main` opens the
`Branch health: main acceptance` issue. The sheriff automation watches `main`'s
CI and counts a run as red when any job in it failed, even when the run itself
concluded `cancelled`. The sheriff never re-runs CI. The sheriff reads the first failed job as soon as
that job fails, finds the commit that broke the job, and delegates an agent to
fix forward when the fix is small and clear, or to revert the commit when the
revert is clean. The fix lands like any other, and the next push run on `main`
judges the fix. The issue records each failure once, with the failure's owner
and the repair that landed, so a failure already owned or already repaired gets
no second fix. Every revert gets an owner: the commit's live session, or a seed
that the chief receives, to land the change again. When neither path is clearly
right, the sheriff asks the user. The `sheriff` skill is the sheriff's
procedure. Keep landing while `main` is red. A release waits for green.

## Releases

A release is cut on demand from a `main` commit that has green Acceptance and
that `reviewed` contains. Its notes are written from the commits since the last
release, grouped by their `Attn-Seed:` trailers, so landings add no changelog
fragment. See [Making a release](making-a-release.md).

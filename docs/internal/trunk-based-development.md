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

`scripts/land.sh` rebases onto `origin/main`, runs the catalog scenarios the
landing selects on CI's Linux, runs a fast gate for the paths the landing
changes, and pushes.

A landing selects two kinds of scenario:

- each scenario it adds or changes, because a scenario can pass on the Mac where
  its author ran it and fail on Linux
- when it changes product code, each scenario listed under `## Tests` by a
  feature file it changes in `.agents/skills/drive-attn/features/`, because a
  landing that changes a feature's behavior can leave that feature's scenarios
  expecting the old behavior

`land.sh` starts the Acceptance soak workflow for them on a temporary
`land-scenarios/` branch, waits for it, and deletes the branch. Every machine
lands through this one path. Scenarios skipped on Linux are not run.

Over 884 `main` commits ending at 582e27999, this selection runs a scenario for
17% of landings, up from 8% for changed scenarios alone, with a median of 3
scenarios, 13 at the 90th percentile and 57 at most. On CI, 1 scenario took a
median of 291s over 28 runs, 16 took 704s and 23 took 1024s, in single runs. Of
the 8 windows where `main` broke a scenario the landing did not change in that
period, the selection stops 3 (7f90df394, 6bd6f16f8, 94ea7af92), and the whole
Vitest suite in the landing stops 1 (d6269a1d6). The other 4 still reach `main`,
because the landing changed no feature file that lists the scenario, or because
the scenario failed only after other scenarios ran in the same shard. Running
the whole matrix for every product change would catch them, on 58% of landings,
at 48 times this selection's scenario time.
A landing that edits only a shared harness file selects no scenario. This step
runs before `land.sh`
takes the land queue, and only once, so a later rebase does not repeat it. The
fast gate:

- Go: vet all packages and their tests, which type-checks everything without
  linking test binaries; run staticcheck on changed packages; and check daemon
  lifetimes and import boundaries. A module change checks all packages. Link
  failures surface in the landing tests and in CI.
- Protocol schema or prompt inputs: check that generated types or catalogs
  are current.
- `app/`: lint, and typecheck with tsrs, a Rust port of the TypeScript 7
  checker. CI on `main` still typechecks with the TypeScript version that
  `app/package.json` pins.
- Script tests: run the shell script tests that cover the changed files. A
  script test covers itself, the scripts it names, and the scripts those name
  in turn, so a change to `scripts/lib/` runs every test that sources it. It
  also covers each file it or those scripts read from the checkout, written as
  `$root/<path>`, and every file under a directory read that way, so a change
  to a workflow a test checks runs that test. When no test names a changed
  script, `land.sh` runs every script test. `land.sh` prints each test it chose
  and the changed files that chose it. `make test-scripts` runs them all, as
  CI does.
- The landing tests. `land.sh` chooses a few tests for the commit it is about to
  push, not a suite: the groups of `scripts/landing-tests` that match the
  changed paths, the tests the landing changes, and every test that failed on
  `main` and has since passed there, until it clears. A change under `app/`
  runs the whole Vitest suite: for such landings, Vitest's import graph selected
  164 to 191 of 270 files and took as long. A test that is still red on `main` is
  listed, not run. `land.sh` prints each test it added and why.
  [Test history](test-history.md#selection) specifies the choice, and
  `scripts/land.sh --baseline` lands with the baseline groups alone.

The Go checks and the landing tests run side by side. `land.sh` first compiles
the changed packages and the packages of the matching groups' Go tests in one
process, while it chooses the landing tests. Both then read that one build. It
waits for both and prints both outcomes, and a failure in either stops the
push. The other checks run before them, because some rewrite tracked files.

Before all of this, `land.sh` refuses a landing with a commit whose author email
differs from the global git identity, and names the commits. Running `git config`
in a linked worktree writes to the config that every worktree of the clone
shares, and nothing else would stop the wrong author from reaching `main`. On a
machine with no global identity, it skips this check.

`land.sh` also warns, without stopping the push, when the landing undoes an
earlier commit. It compares the landing with the last 30 commits on `main` that
share a file with it, and names each commit whose whole change could be applied
again on top of the landing. It misses a commit that the landing only partly
undoes, and one whose lines a later commit changed.

After the push, `land.sh` runs `scripts/native-build-clean`, which removes old
Rust build output. [Native app](native-app.md#build-output) says what it removes.

One land runs at a time per clone. `land.sh` takes the clone's land queue, a
lock that every worktree of the clone shares, before its first fetch, and holds
it until it exits. A land that finds the queue taken prints the holder's PID,
worktree and start time, waits, and says when it takes the queue. The lock dies
with its process, so a land that crashes or is killed holds nothing. Lands from
one clone therefore never push under each other's running gate.

When only protocol types or prompt catalogs conflict, `land.sh` regenerates
them. When `main` moves during landing, such as from a land in another clone or
on another machine, `land.sh` rebases, runs the gate again on the new
combination, and pushes. Go's test cache answers for the Go landing tests the
new commits do not reach.

## Verifying before landing

Checks before landing are fast and local: targeted tests, the change on the
surface it touches, and a review scaled to the change's risk. The full Go
suite, the Playwright specs and the scenario matrix, macOS included, run in CI
on `main` after landing, and so do the combinations of changes that landed
together.

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

A scheduled review automation delegates attn agents to review the commits `main`
has gained since the `reviewed` branch, grouped by seed. Each reviewer judges a
seed's commits against its plan. The automation sends each confirmed finding to
the session named by `Attn-Session:` while that session is live, or to the agent
tending the commit's seed or plot. Otherwise the reviewer fixes the finding. Once
every finding has reached someone, the automation moves `reviewed` forward. The
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

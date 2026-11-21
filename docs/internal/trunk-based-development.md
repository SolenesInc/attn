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

After the push, these run without you. `main`'s CI runs the test suites and the
scenario matrices whose inputs changed. When a landing breaks `main`, the sheriff gets `main` green
again. The review automation reviews the commits and messages their author or
dispatches a fixer. The release process writes release notes from the commits.

### What land.sh does

`scripts/land.sh` rebases onto `origin/main`, runs the catalog scenarios the
landing selects on Linux beside a fast gate for the paths the landing
changes, and pushes.

Each check runs in one of two places:

- On the host that lands: the fast gate and the landing tests, both below.
- On Linux, through `scripts/land-linux.sh`: the scenarios, on the landing
  runner or on CI's Linux, and, for a landing from a host other than Linux
  that changes a path of the `native` landing group, the native wire suite
  (`make test-native-wire`), on the landing runner.

A landing selects three kinds of the native app's scenarios, and never one of
the Tauri app's:

- each scenario it adds or changes, because a scenario can pass on the Mac where
  its author ran it and fail on Linux
- when it changes product code, each scenario listed under `## Tests` by a
  feature file it changes in `.agents/skills/drive-attn/features/`, because a
  landing that changes a feature's behavior can leave that feature's scenarios
  expecting the old behavior
- each scenario that failed on `main` and has since passed there, until it
  clears, when the landing runs landing tests (below): the landing tests choose
  it as they choose any test with that history, and `land.sh` hands it to the
  scenario step. A landing prints each scenario of this kind that does not run
  on Linux or is gone from the scenario catalog.

`land.sh` starts the Acceptance soak workflow for them on a temporary
`land-scenarios/` branch, waits for it, and deletes the branch. Scenarios
skipped on Linux are not run.

A clone with `git config attn.landRunner ssh:<target>` runs them on a landing
runner instead: a Linux machine that keeps its build output between landings.
[Linux runner](linux-runner.md#landing-runner) says how to set one up, and how
several share the landings. When no
machine can be reached, or other landings keep them busy past the limit
[Linux runner](linux-runner.md#landing-runner) gives, the landing says so and
uses the Acceptance soak workflow. A scenario that fails on the landing runner,
or that the runner does not finish, runs again on the workflow, and only that
run blocks the landing.
Unset, or `github`, is the workflow.

The native wire suite runs on Linux because the native app measures and
draws text with the fonts of the machine it runs on: 65dce7a01, landed from a
Mac, passed its landing tests there and failed 382 wire tests on Linux. A
landing from Linux already runs the suite in its landing tests, so only a
landing from another host runs it on the landing runner, in the same turn as
its scenarios and before them. A failure there stops the push and names the
failed tests. When the clone has no landing runner, no landing runner can be
reached, or another landing keeps it busy past the limit, the landing says so
and pushes, and the suite runs on Linux only in CI's Native job after the push.
The landing runners and CI's Linux all run Ubuntu 24.04 with DejaVu Sans Mono,
so they agree on fonts; a Fedora host such as ai-sandbox has none of the
terminal's named fonts and can fail where they pass.

A scenario that passed for the landing's diff does not run again. The diff is
`git diff origin/main...HEAD`, identified by `git patch-id --verbatim`, so it
survives a rebase that leaves the landing's own change as it was. Each pass
adds a line `<scenario> github <run URL>` to
`<git common dir>/attn-land-scenarios/<patch-id>`, which every worktree of the
clone shares, and `land.sh` names the earlier run when it skips one. Of 201
passed runs followed by another run of the same change from 6 to 17 November
2026, 65 had an identical diff. `land.sh` deletes records older than 7 days
after a push. A pass record ignores what `main` gained since the pass, as a
rebase under the queue already does.

Over 884 `main` commits ending at 582e27999, the first two kinds run a scenario for
17% of landings, up from 8% for changed scenarios alone, with a median of 3
scenarios, 13 at the 90th percentile and 57 at most. On CI, 1 scenario took a
median of 291s over 28 runs, 16 took 704s and 23 took 1024s, in single runs. Of
the 8 windows where `main` broke a scenario the landing did not change in that
period, the selection stops 3 (7f90df394, 6bd6f16f8, 94ea7af92), and the Tauri
app's Vitest suite, which landings no longer run, stopped 1 (d6269a1d6). The
other 4 still reach `main`,
because the landing changed no feature file that lists the scenario, or because
the scenario failed only after other scenarios ran in the same shard. Running
the whole matrix for every product change would catch them, on 58% of landings,
at 48 times this selection's scenario time.
A shared harness file selects no scenario of its own. The
fast gate:

- Go: vet all packages and their tests, which type-checks everything without
  linking test binaries; run staticcheck on changed packages; and check daemon
  lifetimes and import boundaries. A module change checks all packages. Link
  failures surface in the landing tests and in CI.
- Protocol schema or prompt inputs: check that generated types or catalogs
  are current.
- `app/`, `sdk/` or `cmd/prompt-editor/`: `make check-js-tools`, which lints
  the App acceptance harness, the plugin SDK and the prompt editor, and checks
  that the committed browser runtime the native app embeds matches its sources.
- `native/`, the protocol schema or `cli/protocol`: `make check-native`, which
  checks formatting, runs `cargo check` on the whole `native/` workspace, and
  runs clippy on the crates the landing changes, or on every crate when it
  changes a file at the top of `native/`.
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
  `main` and has since passed there, until it clears. A change under `native/`
  runs every test of the `native/` workspace, and a change under
  `app/scripts/real-app-harness/` runs the harness's tests, and a change under
  `app/src/browser/` the browser runtime's. A test that is still red on `main` is
  listed, not run. `land.sh` prints each test it added and why.
  [Test history](test-history.md#selection) specifies the choice, and
  `scripts/land.sh --baseline` lands with the baseline groups alone.

`land.sh` chooses the landing tests before it starts the scenarios and the gate,
because the choice names scenarios for the scenario step. The Go checks and the
landing tests run side by side. `land.sh` first compiles the changed packages
and the packages of the matching groups' Go tests in one process. Both then
read that one build. It waits for both and prints both outcomes, and a failure
in either stops the push. The other checks run before them, because some
rewrite tracked files.

Before all of this, `land.sh` refuses a landing that changes `app/src` or
`app/src-tauri`, other than files generated from the protocol schema or the
prompts, while the Tauri app is frozen for the move to the native app. Only P0
and P1 fixes to the shipped app land there; a fix in an area the native app
already covers lands in both apps. Name the fix's seed to land one:
`ATTN_FREEZE_EXCEPTION=<seed id> scripts/land.sh`. `land.sh` refuses an id
that names no seed.

`land.sh` also refuses a landing with a commit whose author email
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

The scenarios and the gate run side by side, once each. From 6 to 17 November
2026 the rest of `land.sh` after the scenarios took 145 s at the median, against
291 s for one scenario on CI, so a gate failure stops the landing minutes before
the scenarios finish: `land.sh` cancels the scenario run and deletes its
branch. A scenario failure stops the push once the gate is done. Lands of one
clone also run side by side. Only the push waits: `land.sh` then takes the
clone's land queue, a lock that every worktree of the clone shares, fetches,
rebases onto `origin/main` and pushes. The queue keeps two lands of one clone
from fetching into the shared `origin/main` ref at once, and a land that finds
it taken prints the holder's PID, worktree and start time, waits, and says when
it takes the queue. The lock
dies with its process, so a land that crashes or is killed holds nothing.

When `main` moved after the checks, `land.sh` says so, naming the commit the
checks ran on, and pushes the rebased commits without running the checks again.
Rerunning would make every land behind a push start over, so that concurrent
lands mostly reran each other's gates. Two changes that pass alone can still
break `main` together. CI on `main` catches that, and the sheriff fixes it. When
only protocol types or prompt catalogs conflict, `land.sh` regenerates them;
any other conflict stops the land before the push.

## Verifying before landing

Checks before landing are fast and local: targeted tests, the change on the
surface it touches, and a review scaled to the change's risk. The full Go
suite and the native app's Linux scenario matrix run in CI on `main` after
landing, and so do the combinations of changes that landed together. Neither
CI nor the landing runs the Tauri app's tests, because the native app replaces
it. A push runs each job group, such
as the four `Daemon` jobs, unless the group already passed with the same
inputs: every tracked file except those `.github/ci-inputs.yml` names as
unread by any job of the group. A file the list leaves out is a claim that no
job of the group reads it; a file it misses only makes the group run. When every job of a group passes on `main`, `Acceptance`
saves a pass record for those inputs in the Actions cache, and later runs carry
the group instead of running it. `Acceptance` names each carried group and the
run that passed it. Pull requests read `main`'s pass records and write none.
CI runs every job nightly, on dispatch, on a release commit and on a
`release/v*` pull request, so a group carried past a file it does read fails
within a day. The macOS jobs run in their own workflow, `CI macOS`, so
waiting for a macOS runner never holds up Linux's Acceptance. `CI macOS`
carries its groups the same way, with pass records of its own, because a Linux
pass does not prove macOS. Two of its groups, `native_macos` and
`pty_upgrade_macos`, also leave out paths that behave the same on Linux, where
`CI` proves them. `CI macOS` runs every macOS job nightly and on a release
commit. When no runner picks up
a macOS job of a push run, `macOS acceptance` passes with a warning that names
the job, and the nightly run covers the gap. A macOS job that ran and failed
still turns `macOS acceptance` red.

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

CI and CI macOS run on `main` after every landing, and each batches pushes that
arrive during its run into its next run. When Acceptance fails, CI opens the
`Branch health: main acceptance` issue. When `macOS acceptance` fails, CI macOS
opens `Branch health: main macOS`. Every night CI runs every job, and CI macOS
every macOS job, on `main`'s head. A red CI full run opens
`Branch health: main CI full run` and a red CI macOS full run opens
`Branch health: main full run`. Only that workflow's next green full run
closes each. The sheriff automation watches both
workflows on `main` and counts a run as red when any job in it failed, even when the run itself
concluded `cancelled`. The sheriff never re-runs CI. As soon as a job fails, the
sheriff reads the job's log, finds the commit that broke `main`, and picks a
path. For a small, clear fix forward, the sheriff messages the culprit's live
session first, or delegates a Builder if that session is gone or stalled. For a
larger break with a clean revert, the sheriff delegates a Builder to revert. The fix lands like any other, and the next push run on
`main` shows whether the fix worked. Fast landings are intentional: occasional
breaks on `main` are why the sheriff exists, so repairs keep `land.sh` fast
instead of gating landings on slow suites. The issue records each failure once, with
the failure's owner and the repair that landed, so the sheriff does not fix a
failure that already has an owner or a repair. Every revert gets an owner who
will land the change again: the commit's live session, or a seed that the chief
receives. When neither fixing forward nor reverting is clearly right, the sheriff asks the
user. The `sheriff` skill is the sheriff's procedure. Keep landing while `main` is red. A
release waits for green.

## Releases

A release is cut on demand from a `main` commit that has green Acceptance and
that `reviewed` contains. CI runs every job and CI macOS every macOS job on the
release commit, and the release ships only when that commit's `Acceptance` and
`Full macOS run` are green. Its notes are written from the commits since the last
release, grouped by their `Attn-Seed:` trailers, so landings add no changelog
fragment. See [Making a release](making-a-release.md).

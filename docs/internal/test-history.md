# Test history

attn's test runners record a receipt for every run: what the command was
asked to run, what each test did, and the source, platform and machine it ran
on. Shared history and test selection are built from these receipts.

## Recording

These commands record a receipt:

- `scripts/test-go.sh`: one receipt per `go test` invocation. A full run records
  one per job (`packages`, `race`, `daemon-1`…), named as the receipt's shard.
- `pnpm --dir app test`, `pnpm --dir app run e2e` and
  `pnpm --dir app run real-app:serial-matrix`.
- `scripts/landing-tests.sh`, which also names the tests it requested, so a
  test that reported nothing shows as `MISSING`. It runs groups of
  `scripts/landing-tests`, or single tests with `--unit '<unit>'`.

Running `go test`, `vitest` or `playwright` directly records nothing.

Each of them runs through
`go tool test-history capture <runner> [--unit <unit>]... [--shard <shard>] [--receipt-out <file>] -- <command>`.
The runner is `go`, `vitest`, `playwright` or `native`. For `go`, the command is
the arguments to `go test`: capture runs `go test -json` and prints the text
`go test` itself prints. The output and the exit status are the command's own.
When the receipt cannot be written, capture warns and the run goes on.

- `--unit` names a requested unit (see [Receipt](#receipt)); one that reports
  nothing shows as `MISSING` in `summary`.
- `--shard` labels the receipt. It defaults to a `--shard` argument of the
  command.
- `--receipt-out` also writes the sealed receipt to a file.

Receipts are kept in the spool, `$ATTN_TEST_HISTORY_DIR` or else
`attn-test-history` in the Git common directory, which every worktree of the
checkout shares. `go tool test-history spool` prints its path.


- `working/<id>/`: a run in progress. Capture and the test command hold its
  lock while they run.
- `receipts/<id>.json`: sealed receipts. A sealed receipt is never rewritten.
- `shared/<id>.json`: receipts synced from the shared history. Selection reads
  them with the sealed ones.

`go tool test-history recover` seals the working files of runs that ended
without sealing, such as a capture killed with `SIGKILL`, as incomplete. It
leaves a run whose command still holds the lock alone. CI runs it before
uploading; locally it runs only when you run it.

`go tool test-history summary <receipt>` prints one line per requested unit,
or per reported unit when none was requested, and exits 1 unless each passed
or skipped. Lines read `<OUTCOME> <unit> (<seconds>s)`, with the outcome in
capitals, or `MISSING <unit> - <reason>`.

The CI jobs that run these commands upload their receipts as
`test-receipts-<job>[-<shard>]-<attempt>` artifacts, whatever the outcome.

## Receipt

The schema is `attn.test-receipt/v1`; `internal/testhistory/receipt.go` holds
its types. A change to a field's meaning needs a new schema version.

- `source`: `local` or `ci`, with the CI run, attempt, job and shard.
- `revision`: the commit and tree tested. `dirty_fingerprint` is present when
  the checkout had changes: a SHA-256 of the diff against `HEAD` and of the
  untracked files' blob ids. Source contents never leave the machine.
- `lane`: operating system, architecture and the Go race detector.
- `diagnostics`: machine class and tool versions, for comparing cost.
- `command`: the argv, the requested units, start, finish, exit code or signal,
  wall time, and the signal capture received, if any.
- `results`: one entry per unit, case and attempt.
- `complete`: capture saw the command exit and the runner's report reach its
  end. It does not say that unlisted tests passed.

A unit is what selection can schedule:

| Unit | Example |
| --- | --- |
| Go top-level test | `go ./internal/daemon TestStateExplain` |
| Go package binary: build, `TestMain`, overall result and time | `go ./internal/daemon` |
| Vitest file, relative to `app/` | `vitest src/App.desktops.test.tsx` |
| Playwright file, relative to `app/` | `playwright e2e/queue-sidebar-fit.spec.ts` |
| Native scenario | `native agent-reload` |

A result's `case` names a Go subtest path, a Vitest test's full name, or a
Playwright `project › describe › title`. A result without a case is the
unit's own.

| Outcome | Meaning |
| --- | --- |
| `pass` | It ran and passed. `cached` marks a Go result replayed from the test cache, not run again. |
| `fail` | It ran and failed, including a timeout or an unexpected pass of a test declared to fail. |
| `skip` | It was skipped; `detail` holds the reason when the runner gives one. |
| `expected-failure` | It was declared to fail, and failed. |
| `interrupted` | A signal or the end of the run stopped it before it finished. |
| `build-failure` | Its Go package did not compile. |

A test the runner never reported has no result: absence is never a pass. A Go
test still running when its package ended, as after a test timeout, failed; one
stopped by a signal was interrupted. Every retry is its own attempt. Vitest
reports only how many retries a test needed, so its earlier attempts are
recorded as failed with the detail `retried`.

`internal/testhistory/testdata` holds real output of each runner from small
fixture projects, and native matrix results from CI and local runs.
`testdata/regenerate.sh` reruns the fixture projects after a runner upgrade.

## Selection

`go tool test-history select` proposes the landing tests for the change
between the checkout and `origin/main` from the receipts in the spool. It is a
shadow: `scripts/land.sh` still runs the fixed list in `scripts/landing-tests`,
the baseline. Every proposal names its policy revision: `success-count/1` by
default, `recent-failure/1` with `--policy recent-failure`.

```sh
go tool test-history select                 # replace mode, success-count policy
go tool test-history select --mode extend --allowance 30s
go tool test-history select --mode baseline # the fixed list
go tool test-history select --policy recent-failure
go tool test-history explain --test 'go ./internal/daemon TestStateExplain'
go tool test-history pending                # unpriced or unavailable candidates
go tool test-history measure 'vitest src/App.desktops.test.tsx'
go tool test-history replay --from <rev>    # what each policy would have chosen
```

`select`, `explain` and `pending` take `--json`, and `--main <ref>` in place of
`origin/main`. `measure` runs its units through
`scripts/landing-tests.sh --unit <unit>...`, the runner the landing uses, so
its receipt prices them.

### Health evidence

A receipt is health evidence when it ran a clean checkout (no
`dirty_fingerprint`) of a commit on the first-parent history of `origin/main`.
Dirty and unlanded runs still price tests. A clean local run counts once its
exact commit lands. Revisions are ordered by their place in main's history,
never by when a receipt was written or arrived.

Within one lane (operating system, architecture, race detector), for each unit
and main revision:

- The revision failed when any result for the unit or one of its cases is
  `fail`, including an earlier attempt of a retried test. A failure at a
  revision wins over passes at the same revision.
- The revision passed when the unit's own result is a `pass` that was not
  replayed from the Go test cache, and the revision did not fail.
- `skip`, `expected-failure`, `interrupted`, cached passes and revisions where
  the unit did not run are neither. Silence never clears a failure.
- Many receipts at one revision are one vote. Go package results (`go ./pkg`),
  such as build failures, are not schedulable tests and carry no health.

Tool versions and machine class are not part of the lane.

### Standing

A unit's anchor in a lane is the later of its latest failing revision in that
lane and, for a unit in the baseline list, its admission: the commit that added
its line to `scripts/landing-tests`, as `git blame` reports it. A failure at the
admission revision is the anchor. A line not yet on main is admitted after every
main revision. A lane where the unit has no anchor does not apply.

The unit's passes since its anchor are the main revisions after the anchor that
passed in that lane. When the anchor is a failure with failing cases, a passing
revision must also pass each of those cases; a run filtered to other cases does
not clear it. The unit stands in its least cleared lane: fewest passes, then the
newest anchor, then the lane name. A baseline unit with no observations stands
at its admission with no passes. A unit with neither a failure nor an admission
has no standing.

A failure in one lane nominates the unit wherever it can run, and only passes in
the failing lane clear it.

### Cost

Costs come from receipts on this machine class and lane whose command exited
with status 0, including dirty and unlanded ones; each figure is the median of
its samples.

- A Vitest or Playwright file costs the wall time of a command that reported
  only that file, setup included. A run filtered to some tests (Vitest `-t`,
  Playwright `--grep`) prices nothing.
- A Go selection runs as one `go test` command, whose packages run in parallel.
  It costs the command's setup, plus the slowest package: that package's own
  setup and teardown plus its selected tests' times. A receipt prices the
  command setup as its wall time less its slowest package, and each package's
  setup as its time less its tests' times.
- A Go receipt with a cached result, repeated attempts (`-count` above 1), a
  `-skip` or a `-run` pattern naming subtests prices nothing.

### Candidates and modes

The groups whose pattern matches a changed path are selected, as in
`land.sh`. The candidates are the selected groups' baseline units, units with a
failure on main whose runner a selected group runs, and the tests the change
touches: top-level Go tests whose function overlaps a changed line in a
`_test.go` file outside `testdata`, and changed Vitest and Playwright files.
Vitest files are those Vitest's `include` in `app/vite.config.ts` names;
Playwright files are `app/e2e/**/*.spec.ts`.

A candidate missing from the checkout, such as a renamed test, is
`UNAVAILABLE`, with the reason. No group runs native scenarios, so they are
never candidates; `pending` lists a native scenario that failed on main as
unavailable. In `replace` and `extend`,
changed tests are always `SELECTED`, outside any budget. The rest are ranked:
fewest passes since the anchor, then the newest anchor, then the lower cost,
then the unit name. `recent-failure/1` ranks the same way but ignores passes
since.

- `replace` selects ranked units, per runner, while the estimated cost of the
  selection stays within the estimated cost of that runner's baseline units.
- `extend` keeps every baseline unit and adds ranked units, within the
  baseline's cost plus `--allowance` when one is given.
- `baseline` selects only the baseline units of the selected groups, as
  `land.sh` does.

A ranked unit that would exceed the budget is `OVER-BUDGET`; one without a cost
is `UNPRICED`, never assumed free. When a baseline unit of a runner is unpriced,
that runner has no budget and its baseline runs.

### Replay

`replay --from <rev>` prints one JSON line per main commit after `<rev>`. It
selects for that commit with the receipts that had finished by the commit's
committer time, the history before it, and the commit's own list, tests and
diff; then compares the selections of `baseline`, `success-count`,
`recent-failure` and `extend-all` (extend without an allowance) with the
failures later observed on that commit. A failure a selection included is not
proof that the selection would have prevented it: a Linux failure, for one,
need not fail on the machine that lands.

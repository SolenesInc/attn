# Test history

attn's test runners record a receipt for every run: what the command was
asked to run, what each test did, and the source, platform and machine of the
run. attn builds shared history and test selection from these receipts.

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

Each of these commands runs through
`go tool test-history capture <runner> [--unit <unit>]... [--shard <shard>] [--receipt-out <file>] -- <command>`.
The runner is `go`, `vitest`, `playwright` or `native`. For `go`, the command is
the arguments to `go test`: capture runs `go test -json` and prints the text
`go test` itself prints. The output and the exit status are the command's own.
When the spool cannot be written, capture warns that history will not keep
the run, still writes `--receipt-out`, and the run goes on.

- `--unit` names a requested unit (see [Receipt](#receipt)). A unit that reports
  nothing shows as `MISSING` in `summary`.
- `--shard` labels the receipt. It defaults to a `--shard` argument of the
  command.
- `--receipt-out` also writes the sealed receipt to a file.

attn keeps receipts in the spool: `$ATTN_TEST_HISTORY_DIR`, or else
`attn-test-history` in the Git common directory. Every worktree of the checkout
shares that directory. `go tool test-history spool` prints the path of the
spool. The spool holds:

- `working/<id>/`: a run in progress. Capture and the test command hold the
  run's lock while they run.
- `receipts/<id>.json`: sealed receipts. A sealed receipt is never rewritten.
- `shared/<id>.json.zst`: this repository's receipts that [sync](#sharing)
  downloaded from the shared history, this machine's own included, each the
  sealed bytes in one zstd frame. Selection reads them with the sealed ones.
- `uploaded/<id>`: an empty file marking a sealed receipt the service
  acknowledged.
- `sync.json`: the download cursor with the service, repository and `shared/`
  layout the cursor was read for, and when sync last ran, last succeeded and why
  sync last failed.

`go tool test-history recover` seals the working files of runs that ended
without sealing, such as a capture killed with `SIGKILL`, as incomplete.
`recover` leaves alone a run whose command still holds the lock. CI runs
`recover` before uploading. Locally, `recover` runs only when you run it.

`go tool test-history summary <receipt>` prints one line per requested unit,
or per reported unit when none was requested, and exits 1 unless each passed
or skipped. Lines read `<OUTCOME> <unit> (<seconds>s)`, with the outcome in
capitals, or `MISSING <unit> - <reason>`.

The CI jobs that run these commands upload their receipts as
`test-receipts-<job>[-<shard>]-<attempt>` artifacts, whatever the outcome.

## Sharing

The private service `attn-test-history` (repository
SolenesInc/attn-test-history, on Oven at
`https://attn-test-history.int.caketroll.cloud`, tailnet only) keeps every
machine's receipts. The service imports CI's receipt artifacts automatically.
`go tool test-history sync` exchanges the rest:

- Sync uploads each sealed receipt the service has not acknowledged and marks
  the receipt in `uploaded/<id>`. The service can refuse a receipt. Sync also
  refuses a receipt whose `repo` is not `host/owner/name`, as for a run captured
  outside a checkout with an origin remote (`repo` is `unknown`). Sync reports
  each refused receipt, offers the receipt again on every sync, and exits 1.
- Sync downloads this repository's receipts into `shared/<id>.json.zst`,
  resuming after the cursor in `sync.json`. Sync asks for each page as one zstd
  stream, about a thirtieth of the page's JSON, because receipts repeat the same
  test names. A cursor read from another service, repository or `shared/`
  layout empties `shared/` and starts the download over. Until that download
  finishes, no sync has succeeded. The service treats every spelling of a
  repository as one, so a checkout whose origin is spelled
  `github.com/solenesinc/attn` receives CI's `github.com/SolenesInc/attn`
  receipts.
- On success, sync prints how many receipts it uploaded and downloaded and how
  many wait to upload. On failure, sync prints how many wait to upload and when
  a sync last succeeded, keeps every receipt, and exits 1.
- Sync prints each warning the service sends with the pages, such as storage
  growing past the service's daily tripwire, without failing.

A sync never changes a test's result. `scripts/land.sh` runs sync before
choosing landing tests and after a push. No test command runs sync.
`ATTN_TEST_HISTORY_URL` points sync at another service. Each request has a
2-minute budget, and connecting has a 10-second budget. A connection to the
service takes about 0.15 seconds, and off the tailnet a connection never
completes.

Sync reads the service token from
`${XDG_CONFIG_HOME:-~/.config}/attn-test-history/token`. The token lives in
Proton Pass (Personal vault, login `attn-test-history ingestion`). Install the
token with:

```sh
dir="${XDG_CONFIG_HOME:-$HOME/.config}/attn-test-history"
mkdir -p "$dir"
(umask 077; pass-cli item view --vault-name Personal \
  --item-title 'attn-test-history ingestion' --field password > "$dir/token")
```

## Receipt

The schema is `attn.test-receipt/v1`. `internal/testhistory/receipt.go` holds
the types of the schema. If you change a field's meaning, add a new schema
version.

- `source`: `local` or `ci`, with the CI run, attempt, job and shard.
- `revision`: the commit and tree tested. `dirty_fingerprint` is present when
  the checkout had changes: a SHA-256 of the diff against `HEAD` and of the
  untracked files' blob ids, or `unknown` when the changes could not be
  fingerprinted. Source contents never leave the machine.
- `lane`: operating system, architecture and the Go race detector.
- `diagnostics`: machine class and tool versions, for comparing cost.
- `command`: the argv, the requested units, start, finish, exit code or signal,
  wall time, and the signal capture received, if any.
- `results`: one entry per unit, case and attempt.
- `complete`: capture saw the command exit and the runner's report reach its
  end. A complete receipt does not say that unlisted tests passed.

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
| `skip` | It was skipped. `detail` holds the reason when the runner gives one. |
| `expected-failure` | It was declared to fail, and failed. |
| `interrupted` | A signal or the end of the run stopped it before it finished. |
| `build-failure` | Its Go package did not compile. |

A test the runner never reported has no result: absence is never a pass. A Go
test still running when its package ended, as after a test timeout, failed. A
test stopped by a signal was interrupted. Every retry is its own attempt.
Vitest reports only how many retries a test needed, so the test's earlier
attempts are recorded as failed with the detail `retried`.

`internal/testhistory/testdata` holds real output of each runner from small
fixture projects, and native matrix results from CI and local runs.
`testdata/regenerate.sh` reruns the fixture projects after a runner upgrade.

## Selection

After every rebase, `scripts/land.sh` chooses landing tests for the commit it
is about to push, with
`go tool test-history land --units-out <file> --group <group>...`. The `--group`
arguments name the groups whose path pattern matched the change. `land.sh`
runs:

- the baseline: every unit of those groups in `scripts/landing-tests`
- the tests the change touches:
  - top-level Go tests whose function overlaps a changed line in a `_test.go`
    file outside `testdata`
  - changed Vitest files that `app/vite.config.ts` lists under `include`
  - changed Playwright files (`app/e2e/**/*.spec.ts`)
- every test that failed on `main` and has since passed there, on at least one
  and fewer than 30 later main revisions in the lane where it failed, whatever
  its runner. A test still red on `main`, with no pass since its last failure,
  is listed as `RED` and not run. The sheriff owns it.

`land.sh` runs the tests through `scripts/landing-tests.sh --unit`, and any
failure stops the push.

- `land` prints each added test with the commit and lane where it failed, its
  passes since, and how many more passes clear it. `land` also prints each red,
  missing baseline or unavailable test with the reason, the baseline's size,
  and how many failures have cleared or are gone from the checkout. `land`
  requests a baseline unit missing from the checkout, such as a renamed test,
  anyway. The run reports the unit `MISSING`, and the landing fails until the
  unit's line in `scripts/landing-tests` names a test again.
- Before choosing, `land.sh` runs `sync`, and after a push `land.sh` runs `sync`
  again, so the landing's own receipt reaches the shared history. When a sync
  fails, `land.sh` prints when a sync last succeeded and continues. The landing
  chooses from the history already in the spool.
- With no successful sync in the spool, `land` chooses the baseline and says so.
- When `land` fails or chooses nothing, `land.sh` prints that it is running the
  baseline groups, and runs them.
- `scripts/land.sh --baseline` runs the baseline groups without syncing or
  choosing. Use it when the history or the choice misbehaves.

```sh
go tool test-history land --units-out units --group go  # what land.sh would run
go tool test-history select                 # the same for the checkout, cleared failures included
go tool test-history explain --test 'go ./internal/daemon TestStateExplain'
go tool test-history replay --from <rev>    # what the baseline and the rule would have run
```

`select` and `explain` take `--json`, and `--main <ref>` in place of
`origin/main`. `select` takes the groups the change matches unless `--group`
names them.

### Health evidence

A receipt is health evidence when the run it records used a clean checkout (no
`dirty_fingerprint`) of a commit on the first-parent history of `origin/main`.
Dirty and unlanded runs count for nothing here, though `explain` and `replay`
still use them to price tests. A clean local run counts once the run's exact
commit lands. attn orders revisions by their place in main's history, never by
when a receipt was written or arrived.

Within one lane (operating system, architecture, race detector), for each unit
and main revision:

- The revision failed when any result for the unit or one of its cases is
  `fail`, including an earlier attempt of a retried test. A failure at a
  revision wins over passes at the same revision.
- The revision passed when the unit's own result is a `pass` that was not
  replayed from the Go test cache, and the revision did not fail.
- `skip`, `expected-failure`, `interrupted`, cached passes and revisions where
  the unit did not run are neither. A revision where the unit did not run never
  clears a failure.
- Many receipts at one revision count once. Go package results (`go ./pkg`),
  such as build failures, are not schedulable tests and carry no health.

Tool versions and machine class are not part of the lane.

### Clearing

A unit's anchor in a lane is the unit's latest failing revision there. The
unit's passes since are the main revisions after the anchor that passed in that
lane. When the
failure named cases, a passing revision must also pass each of them, or be a
run of the whole unit that no longer reports the case, as after a renamed
subtest. A run filtered to other cases (Go `-run` with a subtest or `-skip`,
Vitest `-t`, Playwright `--grep`) does not clear the failure, and neither does
a skip of the case. A failure in one lane makes the unit a candidate wherever
the unit can run, and only passes in the failing lane clear it. The unit counts
in its least cleared lane: fewest passes, then the newest anchor. With no pass
since, the unit is red. The unit has cleared once that lane has 30 passes
since. A failure on a later revision starts the count again.

The 30-pass threshold catches 40 of 50 observed recurrences: over 312 main commits of CI history,
40 of the 50 tests that failed again after passing on `main` did so within 30
passing revisions. At 30, a landing added a median of 7 tests and at most 36,
and the largest set took this Mac's Go landing tests from 20.6s to 38.3s.
`testhistory.ClearedAfter` keeps the measurement. Remeasure the threshold when
recurrences get past the landing tests, or when landings add too many tests or
run slowly.

A failed test that is no longer in the checkout, such as a renamed or removed
test, is `MISSING`. `land` only counts it. A baseline unit missing from the
checkout is also `MISSING`, and `land` requests it anyway, as above. A test
whose file does not build on this platform, such as a `_linux_test.go` test on
a Mac, is `UNAVAILABLE`, with the reason. A native scenario that has not
cleared is also `UNAVAILABLE`, because no group runs native scenarios. Broad CI
runs them.

### Cost

Selection ignores cost. `explain` and `replay` report it. Costs come from
receipts on this machine class and lane whose command exited with status 0,
including dirty and unlanded receipts. Each figure is the median of its
samples.

- A Vitest or Playwright file costs the wall time of a command that reported
  only that file, setup included. Pricing ignores a run filtered to some tests
  (Vitest `-t`, Playwright `--grep`).
- A Go selection runs as one `go test` command, whose packages run in parallel.
  The selection costs the command's setup, plus the slowest package: that
  package's own setup and teardown plus the times of its selected tests. The
  command setup costs the receipt's wall time less its slowest package, and
  each package's setup costs its time less its tests' times.
- Pricing ignores a Go receipt with a cached result, repeated attempts
  (`-count` above 1), a `-skip`, or a `-run` pattern naming subtests.

### Replay

`replay --from <rev>` prints one JSON line per main commit after `<rev>`. For
each commit, `replay` selects tests with the receipts that had finished by the
commit's committer time, the history before the commit, and the commit's own
list, tests and diff. Then `replay` compares what the baseline and the rule run
(`baseline`, `landing`) with the failures later observed on that commit.
`replay` leaves out a receipt without a finish time, such as one `recover`
sealed, because the time its results existed is unknown. `select` still reads
that receipt.

Each selection's `estimate_ms` lists under `unpriced` the selected units that
`replay` could not price. `actual_landing` is the measured baseline on this machine at
that commit: the median wall time of each run whose requested units are a
baseline component (the Go baseline units together, or one app unit), summed.
Components with no run are `missing`. `replay` counts runs with other
requested units as `unattributed`. A failure that a selection included does not
prove that the selection would have prevented it. A Linux failure, for one,
need not fail on the machine that lands.

## Failure report

`go tool test-history report` ranks the tests that failed on clean commits of
`origin/main`, from the same health evidence as selection. An entry is a unit
in one lane:

- A **flake** also passed on the commit of its last failure in that lane, in a
  retry or in another run of that commit, including each case that failed. A
  run that passed only other cases of the test does not make it a flake.
- An **unresolved** failure did not: a break, fixed or not, or a flake nobody
  ran again. Its earlier failures do not make it a flake.
- **Passes since** counts the commits of `main` where the test passed afresh
  in that lane after its last failure, as [Clearing](#clearing) does. A flake
  with at least `--dormant-after` passes since (default 60) is dormant. That
  threshold only sorts the report. The 30 passes that clear a landing test are
  separate.

`report` reads the spool, so after `sync` the report covers every machine and
CI, or every receipt under `--receipts`. `scripts/ci-flake-report.sh` reports
CI alone. The script downloads the receipt artifacts of recent `ci.yml` runs
with the GitHub token, reads those artifacts, and names how many artifacts it
read, found expired or could not read, and how many runs uploaded none. The daily `Flake ledger` workflow
publishes its markdown to the `CI flake ledger` issue. An expired or missing
artifact is a gap in the window, never a pass.

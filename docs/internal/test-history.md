# Test history

attn's test runners record a receipt for every run: what the command was
asked to run, what each test did, and the source, platform and machine of the
run. attn builds shared history and test selection from these receipts.

## Recording

These commands record a receipt:

- `scripts/test-go.sh`: one receipt per `go test` invocation. A full run records
  one per job (`packages`, `daemon-1`…, `attn-1`…), named as the
  receipt's shard.
- `pnpm --dir app test`, `pnpm --dir app run e2e` and
  `pnpm --dir app run real-app:serial-matrix`.
- `scripts/landing-tests.sh`, which also names the tests it requested, so a
  test that reported nothing shows as `MISSING`. It runs groups of
  `scripts/landing-tests`, or single tests with `--unit '<unit>'`.
- CI's `Native (<os>)` jobs, which run `cargo test` for the Rust workspace in
  `native/` as the `cargo` runner. They are not the `native` runner's
  scenarios.

Running `go test`, `vitest`, `playwright` or `cargo test` directly records
nothing.

Each of these commands runs through
`go tool test-history capture <runner> [--unit <unit>]... [--shard <shard>] [--receipt-out <file>] -- <command>`.
The runner is `go`, `vitest`, `playwright`, `native` or `cargo`. For `go`, the
command is the arguments to `go test`: capture runs `go test -json` and prints
the text `go test` itself prints. For `cargo`, the command is the arguments to
`cargo test`: capture runs `cargo test --message-format=json` from the current
directory, `native/` for attn's workspace, and prints what `cargo test` prints,
stderr included, without cargo's JSON messages. For every runner, the output
and the exit status are the command's own.
When capture cannot write to the spool (described below), it warns that history
will not keep the run. It still writes `--receipt-out` and runs the command to
the end.

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
- `mirror/<id>.json.zst`: this repository's receipts that [sync](#sharing)
  downloaded from the shared history, this machine's own included, each the
  sealed bytes in one zstd frame. Selection reads them with the sealed ones.
- `uploaded/<id>`: an empty file marking a sealed receipt the service
  acknowledged.
- `mirror.json`: the download cursor with the service and repository the cursor
  was read for, and when sync last ran, last succeeded and why sync last
  failed.
- `mirror.lock`: the lock a sync holds from reading `mirror.json` until writing
  it back.
- `shared/`, `sync.json` and `sync.lock`: the mirror that older versions of
  sync keep. A worktree that has not rebased onto a version with `mirror/`
  still syncs there. Nothing current reads or writes them, so an older sync
  never disturbs `mirror/`. Delete them once every worktree has rebased.

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
- Sync downloads this repository's receipts into `mirror/<id>.json.zst`,
  resuming after the cursor in `mirror.json`. Sync asks for each page as one zstd
  stream, about a thirtieth of the page's JSON, because receipts repeat the same
  test names. When the cursor in `mirror.json` was read for another service
  or repository, sync empties `mirror/` and starts the download over. Until that download
  finishes, no sync has succeeded. The service treats every spelling of a
  repository as one, so a checkout whose origin is spelled
  `github.com/solenesinc/attn` receives CI's `github.com/SolenesInc/attn`
  receipts.
- On success, sync prints how many receipts it uploaded and downloaded and how
  many wait to upload. On failure, sync prints how many wait to upload and when
  a sync last succeeded, keeps every receipt, and exits 1.
- Sync prints each warning the service sends with the pages, such as storage
  growing past the service's daily tripwire, without failing.

Syncs of one spool take turns, because every worktree shares `mirror/` and
`mirror.json`. A sync that starts while another runs prints that it is waiting
and starts when the other ends, from the cursor the other saved. A sync killed
midway releases its turn, and the next sync resumes from the last page it saved.

A sync never changes a test's result. `land` runs sync before choosing landing
tests, and `scripts/land.sh` runs sync after a push. No test command runs sync.
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
| Test target of a crate in `native/`: unit tests, doc tests, a test or a binary | `cargo attn-client --lib`, `cargo attn-client --doc`, `cargo attn-app --test wire` |

A result's `case` names a Go subtest path, a Vitest test's full name, a
Playwright `project › describe › title`, or a Rust test's path, such as
`wire::window::the_window_shows_the_placeholder`. A result without a case is the
unit's own.

| Outcome | Meaning |
| --- | --- |
| `pass` | It ran and passed. `cached` marks a Go result replayed from the test cache, not run again. |
| `fail` | It ran and failed, including a timeout or an unexpected pass of a test declared to fail. |
| `skip` | It was skipped. `detail` holds the reason when the runner gives one. |
| `expected-failure` | It was declared to fail, and failed. |
| `interrupted` | A signal or the end of the run stopped it before it finished. |
| `build-failure` | Its Go package, or its Rust test target, did not compile. |

A test the runner never reported has no result: absence is never a pass. A Go
test still running when its package ended, as after a test timeout, failed. A
test stopped by a signal was interrupted. Every retry is its own attempt.
Vitest reports only how many retries a test needed, so the test's earlier
attempts are recorded as failed with the detail `retried`. A Rust test binary
that exits before it reports its result, as after an abort, failed with the
detail `the test binary exited before reporting its result`, and its tests that
had not reported have no result.

`internal/testhistory/testdata` holds real output of each runner from small
fixture projects, and native matrix results from CI and local runs.
`testdata/regenerate.sh` reruns the fixture projects after a runner upgrade.

## Selection

After every rebase, `scripts/land.sh` chooses landing tests for the commit it
is about to push, with
`go tool test-history land --units-out <file> --group <group>...`. The `--group`
arguments name the groups whose path pattern matched the change. `land.sh`
runs:

- the baseline: every unit of those groups in `scripts/landing-tests`, including
  `vitest .`, the whole Vitest suite in one run, and `cargo .`, every test of
  the `native/` workspace in one run
- the tests the change touches:
  - top-level Go tests whose function overlaps a changed line in a `_test.go`
    file outside `testdata`
  - changed Vitest files that match `testFilePatterns` in `app/vite.config.ts`
  - changed Playwright files (`app/e2e/**/*.spec.ts`)
- every test that failed on `main` and has since passed there, on at least one
  and fewer than 30 later main revisions in the lane where it failed, whatever
  its runner. A test still red on `main`, with no pass since its last failure,
  is listed as `RED` and not run. The sheriff owns it. A red test blocks no
  landing, unless the test is in the baseline or the landing changes the test.
  `land` writes a red Vitest file as `red vitest <file>`, and `landing-tests.sh`
  leaves it out of the whole suite through `ATTN_VITEST_FILES_RED_ON_MAIN`, which
  `app/vite.config.ts` reads. A red `native/` test is not left out of
  `cargo .`, so it blocks every landing that changes `native/` until it is
  fixed.

`land.sh` runs the tests through `scripts/landing-tests.sh --unit`, and any
failure stops the push. `land.sh` prints each test's result, skipped tests
included, and the command that reruns a failed test. The whole Vitest suite prints its failures, a count of the files that passed,
and its log only when a file failed. `cargo .` does the same and counts test
targets. `landing-tests.sh` runs its Go, Vitest, Playwright and cargo tests at
once, and splits `./cmd/attnd`'s tests across five `go test` runs, each with
its own receipt. When a landing runs the Go
checks, `land.sh` passes `--no-go-vet`, which leaves out `go test`'s own vet,
because those checks run `go vet` on every package. Every other run keeps it,
and only the first `./cmd/attnd` run vets that package.

Go's test cache answers for Go landing tests whose inputs did not change, so
after a rebase only the tests the new commits reach run again, and the others
print `(cached)`. Go keys a cached result on the test binary, its flags, and
the environment variables and files the test read, and every worktree on the
machine shares the cache. The attn binary that stack tests run counts as an
input. A test whose result depends on something else Go cannot see, such as
a server or a binary built during the test from source outside the test's
package, must be listed in `scripts/landing-tests.sh`, which runs it with
`-count=1`. The ClickHouse tests, and a test that builds attn at another
version, are listed there.

- `land` prints each added test with the commit and lane where it failed, its
  passes since, and how many more passes clear it. `land` also prints each red test, each missing baseline unit, and each unavailable test with its reason, the baseline's size, and how many failures have cleared or are gone from the checkout. `land`
  requests a baseline unit missing from the checkout, such as a renamed test,
  anyway. The run reports the unit `MISSING`, and the landing fails until the
  unit's line in `scripts/landing-tests` names a test again.
- Before choosing, `land` runs `sync` with the rebased checkout's own code, so
  the choice reads the history that code keeps. After a push, `land.sh` runs
  `sync` again, so the landing's own receipt reaches the shared history. When
  the sync before choosing fails, `land` prints when a sync last succeeded and
  chooses from the history already in the spool.
- With no successful sync in the spool, `land` chooses the baseline and says so.
- When `land` fails or chooses nothing, `land.sh` prints that it is running the
  baseline groups, and runs them.
- `scripts/land.sh --baseline` runs the baseline groups without syncing or
  choosing. Use it when the history or the choice misbehaves, and never to
  skip a failing test.

The baseline still grows from red `main`. When the test that caught the
failure runs in seconds, the sheriff's repair adds the test to its group in
`scripts/landing-tests` in the same landing.

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
The measurement is recorded in the comment on `testhistory.ClearedAfter`. Remeasure the threshold when
recurrences get past the landing tests, or when landings add too many tests or
run slowly.

A failed test that is no longer in the checkout, such as a renamed or removed
test, is `MISSING`. `land` prints only a count of such tests and runs none of them. A baseline unit missing from the
checkout is also `MISSING`, and `land` requests it anyway, as above. A test
whose file does not build on this platform, such as a `_linux_test.go` test on
a Mac, is `UNAVAILABLE`, with the reason. A native scenario that has not
cleared is also `UNAVAILABLE`, because no group runs native scenarios. Broad CI
runs them, and `scripts/land-scenarios.sh` runs the ones a landing selects
outside this selection, as
[Trunk-based development](trunk-based-development.md#what-landsh-does) says.

### Cost

Selection ignores cost. `explain` and `replay` report it. Costs come from
receipts on this machine class and lane whose command exited with status 0,
including dirty and unlanded receipts. Each figure is the median of its
samples.

- A Vitest or Playwright file costs the wall time of a command that reported
  only that file, setup included. Pricing ignores a run filtered to some tests
  (Vitest `-t`, Playwright `--grep`).
- A Go selection runs as one `go test` command, and its packages run in
  parallel. The selection's price is the command setup plus the time of the
  slowest package. A package's time is its own setup and teardown plus the times
  of its selected tests. The command setup is the receipt's wall time minus the
  slowest package's time. A package's setup is its time minus its tests' times.
- Pricing ignores a Go receipt with a cached result, repeated attempts
  (`-count` above 1), a `-skip`, or a `-run` pattern naming subtests.

### Replay

`replay --from <rev>` prints one JSON line per main commit after `<rev>`. For
each commit, `replay` selects tests from two sources. The first is the history
before the commit: the receipts that had finished by the commit's committer
time. The second is the commit's own list, tests and diff. Then `replay`
compares what the baseline and the landing selection (`baseline`, `landing`)
would run with the failures later observed on that commit.
`replay` leaves out a receipt without a finish time, such as one `recover`
sealed, because the time its results existed is unknown. `select` still reads
that receipt.

Each selection's `estimate_ms` lists under `unpriced` the selected units that
`replay` could not price. `actual_landing` is the measured baseline on this machine at
that commit: the median wall time of each run whose requested units are a
baseline component (the Go baseline units together, or one app unit), summed.
Components with no run are `missing`. `replay` counts runs with other
requested units as `unattributed`. A selection that includes a test that later failed on that commit does not prove that the selection would have prevented the failure. For example, a Linux failure need not occur on the machine that lands.

## Failure report

`go tool test-history report` ranks the tests that failed on clean commits of
`origin/main`, from the same health evidence as selection. An entry is a unit
in one lane:

- A **flake** also passed on the commit of its last failure in that lane, in a
  retry or in another run of that commit, including each case that failed. A
  run that passed only other cases of the test does not make it a flake.
- An **unresolved** failure did not pass on the commit of its last failure. It
  is a break (fixed or not), or a failure that nobody ran again on that commit.
  Earlier failures of the test do not make it a flake.
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

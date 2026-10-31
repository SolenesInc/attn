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
When the spool cannot be written, capture warns that history will not keep
the run, still writes `--receipt-out`, and the run goes on.

- `--unit` names a requested unit (see [Receipt](#receipt)); one that reports
  nothing shows as `MISSING` in `summary`.
- `--shard` labels the receipt. It defaults to a `--shard` argument of the
  command.
- `--receipt-out` also writes the sealed receipt to a file.

Receipts are kept in the spool, `$ATTN_TEST_HISTORY_DIR` or else
`attn-test-history` in the Git common directory, which every worktree of the
checkout shares. `go tool test-history spool` prints its path. It holds:

- `working/<id>/`: a run in progress. Capture and the test command hold its
  lock while they run.
- `receipts/<id>.json`: sealed receipts. A sealed receipt is never rewritten.
- `shared/<id>.json`: this repository's receipts that [sync](#sharing)
  downloaded from the shared history, this machine's own included. Selection
  reads them with the sealed ones.
- `uploaded/<id>`: an empty file marking a sealed receipt the service
  acknowledged.
- `sync.json`: the download cursor with the service and repository it was read
  from, and when sync last ran, last succeeded and why it last failed.

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

## Sharing

The private service `attn-test-history` (repository
SolenesInc/attn-test-history, on Oven at
`https://attn-test-history.int.caketroll.cloud`, tailnet only) keeps every
machine's receipts. It imports CI's receipt artifacts on its own;
`go tool test-history sync` exchanges the rest:

- It uploads each sealed receipt the service has not acknowledged and marks
  it in `uploaded/<id>`. The service can refuse a receipt, and so does sync
  when the receipt's `repo` is not `host/owner/name`, as for a run captured
  outside a checkout with an origin remote (`repo` is `unknown`). Sync reports
  each refused receipt, offers it again on every sync, and exits 1.
- It downloads this repository's receipts into `shared/<id>.json`, resuming
  after the cursor in `sync.json`. A cursor read from another service or
  repository starts the download over. The service treats every spelling of a
  repository as one, so a checkout whose origin is spelled
  `github.com/solenesinc/attn` receives CI's `github.com/SolenesInc/attn`
  receipts.
- On success it prints how many receipts it uploaded and downloaded and how
  many wait to upload. On failure it prints how many wait to upload and when a
  sync last succeeded, keeps every receipt, and exits 1.

A sync never changes a test's result. `scripts/land.sh` runs it before
choosing landing tests; no test command does. `ATTN_TEST_HISTORY_URL` points
it at another service. Each request has a 2-minute budget, and connecting has
10 seconds: a connection to the service takes about 0.15 seconds, and off the
tailnet one never completes.

The service token is read from
`${XDG_CONFIG_HOME:-~/.config}/attn-test-history/token`. It lives in Proton
Pass (Personal vault, login `attn-test-history ingestion`); install it with:

```sh
dir="${XDG_CONFIG_HOME:-$HOME/.config}/attn-test-history"
mkdir -p "$dir"
(umask 077; pass-cli item view --vault-name Personal \
  --item-title 'attn-test-history ingestion' --field password > "$dir/token")
```

## Receipt

The schema is `attn.test-receipt/v1`; `internal/testhistory/receipt.go` holds
its types. A change to a field's meaning needs a new schema version.

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

`scripts/land.sh` chooses its landing tests after every rebase, for the commit
it is about to push, with
`go tool test-history land --units-out <file> --group <group>...`, naming the
groups whose path pattern matched the change. It runs:

- the baseline: every unit of those groups in `scripts/landing-tests`;
- the tests the change touches: top-level Go tests whose function overlaps a
  changed line in a `_test.go` file outside `testdata`, and changed Vitest
  files (those `include` in `app/vite.config.ts` names) and Playwright files
  (`app/e2e/**/*.spec.ts`);
- every test that failed on `main` and has not yet passed on 30 later main
  revisions in the lane where it failed, when a matched group runs its runner.

It runs them through `scripts/landing-tests.sh --unit`, and any failure stops
the push.

- `land` prints each added test with the commit and lane it failed at, its
  passes since and how many more passes clear it; each unavailable test with
  the reason; the baseline's size; and how many failures have cleared or are
  gone from the checkout. A
  baseline unit missing from the checkout, such as a renamed test, is
  requested anyway: the run reports it `MISSING` and the landing fails until
  its line in `scripts/landing-tests` names a test again.
- Before choosing, `land.sh` runs `sync`, and after a push it runs `sync`
  again, so the landing's own receipt reaches the shared history. When a sync
  fails, it prints when a sync last succeeded and carries on; the landing
  chooses from the history already in the spool.
- With no successful sync in the spool, `land` chooses the baseline and says so.
- When `land` fails or chooses nothing, `land.sh` prints that it is running the
  baseline groups, and runs them.
- `scripts/land.sh --baseline` runs the baseline groups without syncing or
  choosing: the way out when history or the choice misbehaves.

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

A receipt is health evidence when it ran a clean checkout (no
`dirty_fingerprint`) of a commit on the first-parent history of `origin/main`.
Dirty and unlanded runs count for nothing here, though they still price tests
for reports. A clean local run counts once its exact commit lands. Revisions
are ordered by their place in main's history, never by when a receipt was
written or arrived.

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

### Clearing

A unit's anchor in a lane is its latest failing revision there. Its passes since
are the main revisions after the anchor that passed in that lane. When the
failure named cases, a passing revision must also pass each of them, or be a
run of the whole unit that no longer reports the case, as after a renamed
subtest. A run filtered to other cases (Go `-run` with a subtest or `-skip`,
Vitest `-t`, Playwright `--grep`) does not clear it, nor does a skip of the
case. A failure in one lane makes the unit
a candidate wherever it can run, and only passes in the failing lane clear it.
The unit stands in its least cleared lane: fewest passes, then the newest
anchor. It has cleared once that lane has 30 passes since; a failure on a
later revision starts it again.

30 catches 87 of 97 observed recurrences: over 312 main commits of CI history,
87 of the 97 tests that failed again did so within 30 passing revisions.
`testhistory.ClearedAfter` keeps the measurement; remeasure it when landings
miss recurrences or carry too many tests.

A failed test that is no longer in the checkout, such as a renamed or removed
one, is `MISSING`: `land` only counts it. No group runs native scenarios, so a
native scenario that has not cleared is `UNAVAILABLE`, with the reason: broad
CI runs it.

### Cost

Selection ignores cost. `explain` and `replay` report it. Costs come from
receipts on this machine class and lane whose command exited with status 0,
including dirty and unlanded ones; each figure is the median of its samples.

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

### Replay

`replay --from <rev>` prints one JSON line per main commit after `<rev>`. It
selects for that commit with the receipts that had finished by the commit's
committer time, the history before it, and the commit's own list, tests and
diff; then compares what the baseline and the rule run (`baseline`,
`landing`) with the failures later observed on that commit. A receipt
without a finish time, such as one `recover` sealed, is left out of the
replay, since when its results existed is unknown; `select` still reads it.

Each selection's `estimate_ms` lists the selected units it could not price
under `unpriced`. `actual_landing` is the measured baseline on this machine at
that commit: the median wall time of each run whose requested units are a
baseline component (the Go baseline units together, or one app unit), summed.
Components with no run are `missing`; runs with other requested units are
counted as `unattributed`. A failure a selection included is not proof that the
selection would have prevented it: a Linux failure, for one, need not fail on
the machine that lands.

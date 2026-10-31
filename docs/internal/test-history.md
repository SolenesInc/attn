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
  test that reported nothing shows as `MISSING`.

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

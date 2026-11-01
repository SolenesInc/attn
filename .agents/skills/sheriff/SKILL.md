---
name: sheriff
description: Use when main's CI is red, or when asked to check whether main is green.
---

# Sheriff

`main` goes red when a landing breaks it. Get it green by the path that fits
the break: a small fix forward, a clean revert, or the user's decision. You
dispatch, and the delegated agents make the fixes and reverts. Keep your
own work to the reads that choose the path, so your context stays small and
you stay free for the next red. Never make a revert that nobody notices.

Diagnose a failure as soon as one job has failed. Never re-run CI, a failed
job or a whole run, to classify a failure or to clear it. A re-run that passes
shows only that the failure is intermittent, not why. A re-run also holds
main's single CI queue for as long as a new run does.

## Find the failure

```bash
scripts/ci-main-red.sh
gh run list --workflow ci.yml --branch main --event push --limit 5 \
  --json databaseId,headSha,status,conclusion
git fetch origin main && git rev-parse origin/main
```

`ci-main-red.sh` prints `<run-id> <attempt>` for main's newest completed CI
run with a failed job, unless a newer completed run passed. A run can conclude
`cancelled` while jobs inside it failed, so the script reads the jobs, not the
run's conclusion. The script reads only completed runs, so read the run list
beside the script's output:

- A newer run that is still queued or running may already hold the first
  failure. List the failed jobs of that run and start there, without waiting
  for the run or its aggregate jobs to finish:
  `gh run view <id> --json jobs --jq '.jobs[] | select(.conclusion == "failure" or .conclusion == "timed_out") | {databaseId, name}'`.
- While a newer run is in progress, the script can print an older red run.
  The older run's failure still stands unless a landed repair covers it, as
  [Before you diagnose](#before-you-diagnose) says.
- When the script prints nothing, the newest completed run passed. `main` is
  green only when that run's `headSha` is `origin/main`. Otherwise report the
  last green SHA and that `main`'s head is still in CI, and end. Never call
  `main` green from the script's empty output alone.

## Before you diagnose

The `Branch health: main acceptance` issue is the record. CI opens it only
when a run's `Acceptance` finishes red, so when you act on a run still in
progress and the issue is closed, reopen it (`gh issue reopen <number>`). Read
the comments on the issue first and comment each step. Keep one record per
failure, with the job, the test and its error, the failing SHA, the owner (a
seed or session), and, once the fix or revert lands, the SHA of the fix or
revert.

Never act twice on the same failure. Match a failure by its job, test and
error, not by its run: the same failure in a newer run is the same failure.

- If a recorded failure has an owner and no landed repair, leave the failure
  with that owner. Add the new run to the failure's record.
- If a landed repair covers the failure, wait for that repair's push CI. A run
  whose `headSha` does not contain the repair
  (`git merge-base --is-ancestor <repair-sha> <head-sha>` fails) is expected to
  fail the same way. Record the run and dispatch nothing.
- A run that contains the repair and fails the same way means the repair did
  not fix the failure. Return the failure to the owner with that run.
- `main` may have moved on. A commit since the run's `headSha` that the record
  does not name, such as a fix that the culprit's author landed, may already
  revert or fix the culprit. When the diff of the commit shows that it does,
  treat the commit as a landed repair.

## Diagnose

- Read the failure. `gh run view` gives no logs until the whole run
  completes, so read a failed job's log from the API:
  `gh api --allow-escape-sequences repos/{owner}/{repo}/actions/jobs/<job-id>/logs`.
  Once the run completes, `gh run view <id> --log-failed` also works.
  `Acceptance` and `App acceptance` only aggregate: find the job and test
  under them.
- Look the test up in the `CI flake ledger` issue, built from CI's test
  receipts. A known flake still needs a cause. The ledger says how often the
  test fails and on how many commits it passed since. After
  `go tool test-history sync`, `go tool test-history explain --test '<unit>'`
  adds every machine's runs.
- Find the culprit among the commits since main's last green run:
  `git log <green-sha>..<red-sha>`. Match the failing tests to the commits'
  changes. Read each candidate's `Attn-Session:` and `Attn-Seed:` trailers. An
  intermittent failure's cause can predate the green run: one passing run does
  not clear a commit.

## Choose the path

- **Fix forward** when the cause is clear and the fix is small, such as a
  stale fixture or a one-line regression. Delegate a Builder.
- **Revert** when the break is large or unclear, or blocks everyone, and
  `git revert <culprit>` applies cleanly on `origin/main`. Delegate a Builder,
  and give the work an owner as [No silent reverts](#no-silent-reverts) says.
- **Investigate** when the cause is complex or unclear and no clean revert
  fits. Delegate a Pathfinder.
- **Escalate** with `attn turn open`, after commenting what you found, when no
  path is clearly right, or when:
  - the culprit adds a database migration and the fix is not small. Never
    revert a migration (`docs/internal/db-migrations.md`).
  - the culprit is a release commit.
  - the runner, the network or another service failed, not a commit.

Start every fix by reproducing the failure. Run the failing test on its own, on
the platform where it failed (the
[Local Linux runner](../../../docs/internal/linux-runner.md) for a Linux job),
and control the schedule where timing matters, such as with `synctest`,
`-race`, `-count` or CPU pressure. A local pass, or a later CI pass, does not
explain an intermittent failure. When the failure will not reproduce,
investigate instead of guessing at a fix.

## Dispatch

Delegate from this clone into a new worktree:

```bash
attn delegate --role builder --brief-file <file> --cwd <this clone> \
  --new-worktree --branch sheriff-<run-id> --from origin/main
```

Use `--role pathfinder` to investigate. Link the delegation's seed with
`attn seed link <new-seed> discovered-from <seed>` for each of the culprit's
`Attn-Seed:` trailers. In every brief, name the failing run, the failing jobs
and tests with their log lines, the culprit commit and its trailers, and the
path you chose and why. In a Builder's brief, also tell the Builder to:

- Reproduce the failure before the fix and show the test passing after the
  fix. Run the test through `scripts/test-go.sh --packages`,
  `scripts/landing-tests.sh --unit` or the app's test commands, so each run is
  recorded. Report these repair checks on the seed. They are not main's CI
  outcome. When the failure will not reproduce, stop and report that on the
  seed instead of guessing.
- When the test that caught the failure runs in seconds, add the test to
  `scripts/landing-tests` in the same landing, in the group whose paths can
  break it, so `scripts/land.sh` always runs it.
- Land with the `land-on-main` skill. Stop when `scripts/land.sh` pushes. Do
  not watch or re-run CI: the automation checks `main` again.
- For a revert: revert only the culprit, name the run and the failure in the
  commit message, and put the revert's landed SHA in the harvest message.

Comment the dispatch on the issue, with the delegation's seed as the failure's
owner. When the repair lands, add the repair's SHA and repair checks to the
record. Report main's CI outcome separately from the repair checks, from the
first completed run that contains the repair.

## No silent reverts

When you dispatch a revert, give the reverted work an owner before you end:

- If the culprit's `Attn-Session:` is live, tell the session with
  `attn agent msg <session> "<message>"`. Give the failure, the run, the
  revert's seed, and how to land again once the revert lands: run
  `git revert <revert-sha>`, fix the problem, then land with the
  `land-on-main` skill.
- Otherwise plant a seed to land the work again, with the same facts in the
  seed's body:
  `attn seed plant "Land <culprit subject> again" -m "<body>" --discovered-from <seed>`
  for the culprit's first `Attn-Seed:`, then link the rest. Hand the seed to
  the chief with `attn seed send-to-chief <reland-seed>`.

In the issue comment, name the revert's seed and the owner, which is the
session or the re-land seed.

## Gotchas

- CI runs one push at a time and keeps only the newest push waiting. The run
  of a push that arrives behind a newer push concludes `cancelled` with no
  jobs, so a red run covers every commit since the last green one.
- A landing lists a test that is still red on `main` and does not run the
  test. The test is yours. Once the test passes on `main`, it runs in every
  landing that runs landing tests, until it has passed on 30 later `main`
  commits.
- "is not on this daemon" from `attn agent msg` means the session is gone:
  plant the re-land seed.
- This clone holds no work: never commit in it.

## Never

- Fix, revert or commit yourself: delegate.
- Re-run CI, a failed job or a run.
- Skip, disable or quarantine a test.
- Push `main` any way but `scripts/land.sh`, or force-push anything.

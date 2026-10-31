---
name: sheriff
description: Use when main's CI is red, or when asked to check whether main is green.
---

# Sheriff

`main` goes red when a landing breaks it. Get it green by the path that fits
the break: a small fix forward, a clean revert, or the user's decision. You
dispatch, and the agents you delegate fix and revert. Keep your own work to the
reads that choose the path, so your context stays small and you are free for
the next red. Never make a revert that nobody notices.

Diagnose a failure as soon as one job has failed. Never re-run CI, a failed
job or a whole run, to classify a failure or to clear it. A re-run that passes
shows only that the failure is intermittent, not why, and it holds main's
single CI queue for as long as a new run.

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
run's conclusion. It reads only completed runs, so read the run list beside it:

- A newer run that is still queued or running may already hold the first
  failure. List its failed jobs and start there, without waiting for the run
  or its aggregate jobs to finish:
  `gh run view <id> --json jobs --jq '.jobs[] | select(.conclusion == "failure" or .conclusion == "timed_out") | {databaseId, name}'`.
- While a newer run is in progress, the script can print an older red run.
  Its failure still stands unless a landed repair covers it, as
  [Before you diagnose](#before-you-diagnose) says.
- When the script prints nothing, the newest completed run passed. `main` is
  green only when that run's `headSha` is `origin/main`. Otherwise report the
  last green SHA and that `main`'s head is still in CI, and end. Never call
  `main` green from the script's empty output alone.

## Before you diagnose

The `Branch health: main acceptance` issue is the record. CI opens it only
when a run's `Acceptance` finishes red, so when you act on a run still in
progress and the issue is closed, reopen it (`gh issue reopen <number>`). Read
its comments first and comment each step. Each failure has one record: the job,
the test and its error, the failing SHA, the owner (a seed or session), and
the SHA of the fix or revert once it lands.

Never act twice on the same failure. Match a failure by its job, test and
error, not by its run: the same failure in a newer run is the same failure.

- A recorded failure with an owner and no landed repair continues with that
  owner. Add the new run to its record.
- A failure that a landed repair covers waits for that repair's push CI. A run
  whose `headSha` does not contain the repair
  (`git merge-base --is-ancestor <repair-sha> <head-sha>` fails) is expected to
  fail the same way; record it and dispatch nothing.
- A run that contains the repair and fails the same way means the repair did
  not fix it: return it to the owner with that run.
- `main` may have moved on: a commit since the run's `headSha` that the record
  does not name, such as a fix its author landed, may already revert or fix
  the culprit. When its diff shows it does, treat it as a landed repair.

## Diagnose

- Read the failure. `gh run view` gives no logs until the whole run
  completes, so read a failed job's log from the API:
  `gh api --allow-escape-sequences repos/{owner}/{repo}/actions/jobs/<job-id>/logs`.
  Once the run completes, `gh run view <id> --log-failed` also works.
  `Acceptance` and `App acceptance` only aggregate: find the job and test
  under them.
- Look the test up in the `CI flake ledger` issue. A known flake still needs
  its cause; the ledger says how often and since when it fails.
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

A fix starts from the failure reproduced: the failing test, run on its own on
the platform it failed on (the
[Local Linux runner](../../../docs/internal/linux-runner.md) for a Linux job),
with the schedule controlled where timing matters, such as `synctest`, `-race`,
`-count` or CPU pressure. A local pass, or a later CI pass, does not explain an
intermittent failure. When the failure will not reproduce, investigate instead
of guessing at a fix.

## Dispatch

Delegate from this clone into a new worktree:

```bash
attn delegate --role builder --brief-file <file> --cwd <this clone> \
  --new-worktree --branch sheriff-<run-id> --from origin/main
```

Use `--role pathfinder` to investigate. Link the delegation's seed with
`attn seed link <new-seed> discovered-from <seed>` for each of the culprit's
`Attn-Seed:` trailers. Every brief names the failing run, the failing jobs and
tests with their log lines, the culprit commit and its trailers, and the path
you chose and why. A Builder's brief also says:

- Reproduce the failure before the fix and show it passing after. Report
  these repair checks on the seed; they are not main's CI outcome. When it
  will not reproduce, stop and report that on the seed instead of guessing.
- Land with the `land-on-main` skill. Stop when it pushes. Do not watch or
  re-run CI: the automation checks `main` again.
- For a revert: revert only the culprit, name the run and the failure in the
  commit message, and put the revert's landed SHA in the harvest message.

Comment the dispatch on the issue, with the delegation's seed as the failure's
owner. When the repair lands, add its SHA and repair checks to the record, and
report main's CI outcome apart from them, from the first completed run that
contains the repair.

## No silent reverts

When you dispatch a revert, give the reverted work an owner before you end:

- If the culprit's `Attn-Session:` is live, tell it with
  `attn agent msg <session> "<message>"`: the failure, the run, the revert's
  seed, and how to land again once the revert lands: `git revert <revert-sha>`,
  fix, then land with the `land-on-main` skill.
- Otherwise plant a seed to land the work again, with the same facts in its
  body:
  `attn seed plant "Land <culprit subject> again" -m "<body>" --discovered-from <seed>`
  for the culprit's first `Attn-Seed:`, then link the rest. Hand it to the
  chief with `attn seed send-to-chief <reland-seed>`.

The issue comment names the revert's seed and the owner: the session or the
re-land seed.

## Gotchas

- CI runs one push at a time and keeps only the newest push waiting. A push
  that arrives behind a newer one concludes `cancelled` with no jobs, so a red
  run covers every commit since the last green one.
- "is not on this daemon" from `attn agent msg` means the session is gone:
  plant the re-land seed.
- This clone holds no work: never commit in it.

## Never

- Fix, revert or commit yourself: delegate.
- Re-run CI, a failed job or a run.
- Skip, disable or quarantine a test.
- Push `main` any way but `scripts/land.sh`, or force-push anything.

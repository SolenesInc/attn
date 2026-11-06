---
name: sheriff
description: Use when main's CI is red, or when asked to check whether main is green.
---

# Sheriff

`main` goes red when a landing breaks it. Get it green by the path that fits
the break: a small fix forward, a clean revert, or the user's decision. You
dispatch, and the delegated agents make the fixes and reverts. Never make a
revert that nobody notices.

Load the `attn-workflow` skill. Follow its
[Orchestration](../../../internal/prompts/content/skills/attn-workflow/references/orchestration.md)
process to dispatch, advise, review and judge, and its
[Debugging](../../../internal/prompts/content/skills/attn-workflow/references/debugging.md)
process for the reads that choose a path. Where they differ, follow this
skill: a Sheriff makes no change itself, however small, and leaves
reproduction and experiments to the delegate it dispatches.

Diagnose a failure as soon as one job has failed. Never re-run CI, a failed
job or a whole run, to classify a failure or to clear it. A re-run that passes
shows only that the failure is intermittent, not why. A re-run also holds
main's single CI queue for as long as a new run does.

## Principles

These principles from `attn-workflow` matter most to a Sheriff. Each entry
says when it applies.

- [Guard the context window](../../../internal/prompts/content/skills/attn-workflow/principles/guard-the-context-window.md):
  keep your own work to the reads that choose the path, so you stay free for
  the next red. Hand a long investigation to a Pathfinder. Keep each failure's
  record in the issue and the seed, not in the conversation.
- [Fix root causes](../../../internal/prompts/content/skills/attn-workflow/principles/fix-root-causes.md):
  when you choose the path and write the brief, aim at the first wrong
  decision, not the place where the failure surfaced. A test bug is the
  cause when the test's premise is wrong, not only when its timing is.
- [Attack the premise](../../../internal/prompts/content/skills/attn-workflow/principles/attack-the-premise.md):
  when a second landed repair fails, as
  [Before you diagnose](#before-you-diagnose) says, do not return the failure
  for a third patch. Write down the assumption the repairs share, check it
  against the code or the evidence, and bring the owner a redesign. If the
  owner disagrees, escalate. QUICK-CAPTURE's 14785f5dc moved the Dictation
  step and 14558b89d cleared QUICK-CAPTURE's draft. Both repairs assumed the step could
  share the scenario's serial state.
- [Encode lessons in structure](../../../internal/prompts/content/skills/attn-workflow/principles/encode-lessons-in-structure.md):
  when the same class of failure comes back, plant a
  [Correction](../../../internal/prompts/content/skills/attn-workflow/references/correction.md)
  seed with `--discovered-from` the failure's seed instead of dispatching
  another one-off fix. Scenario state that leaks into later steps is one such
  class: delegation-chain's leftover desktops (b76ed7928) and QUICK-CAPTURE's
  draft.
- [Prove it works](../../../internal/prompts/content/skills/attn-workflow/principles/prove-it-works.md):
  report main's CI outcome from the first completed run that contains the
  repair, separately from the repair checks. Label each claim measured,
  inferred or guessed.
- [Never block on the user](../../../internal/prompts/content/skills/attn-workflow/principles/never-block-on-the-user.md):
  ask with `attn ask` and keep working. Run `attn turn open` only for the
  escalations that [Choose the path](#choose-the-path) lists.

## Corrected failure classes

When a failure belongs to a class listed here, the fix did not hold. The
Correction seed you plant moves the class to a stronger fix.

| Class | Instance | What prevents it |
|---|---|---|
| A package host that a job is not testing turns App acceptance red | Run 41902160029, shard 2/10: `apt download exceeded 300s`, no test failed | `setup-linux-app-deps`: shards restore a frozen apt layer and install with every network source removed. Only the build job can reach the mirror, on a cache miss |
| A landing adds or changes a scenario that passed only on the author's Mac, and it fails on Linux in CI | 729fdd236, SHELF-MOVES: `the active tile sits at the end of the screen` | `scripts/land-scenarios.sh`: `land.sh` runs each changed catalog scenario on CI's Linux before the push |

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
- A run that contains the repair and fails in the same job and scenario or
  test, even at a different step or with a different error, means the repair
  did not fix the failure. Return the failure to the owner with that run. When
  a second landed repair for the same failure record fails this way,
  apply [Attack the premise](#principles) instead of returning the failure
  again.
- `main` may have changed since the run's `headSha`. A newer commit that the
  record does not name, such as a fix the culprit's author landed, may already
  revert or fix the culprit. Read the commit's diff. If the diff reverts or
  fixes the culprit, treat the commit as a landed repair.

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
  - the owner disagrees with the redesign you brought them under
    [Attack the premise](#principles).

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
path you chose and why, and also ask for the first wrong decision, not the
place where the failure surfaced. In a Builder's brief, also tell the Builder
to:

- Reproduce the failure before the fix, and show the test passing after the
  fix. Run the failing test on its own, on the platform where it failed (the
  [Local Linux runner](../../../docs/internal/linux-runner.md) for a Linux
  job), and control the schedule where timing matters, such as with
  `synctest`, `-race`, `-count` or CPU pressure. A local pass, or a later CI
  pass, does not explain an intermittent failure. Run the test through
  `scripts/test-go.sh --packages`, `scripts/landing-tests.sh --unit` or the
  app's test commands, so each run is recorded. Report these repair checks on
  the seed. When the failure will not reproduce, stop and report that on the
  seed instead of guessing at a fix.
- When the test that caught the failure runs in seconds, add the test to
  `scripts/landing-tests` in the same landing, in the group whose paths can
  break it, so `scripts/land.sh` always runs it.
- Land with the `land-on-main` skill. Stop when `scripts/land.sh` pushes. Do
  not watch or re-run CI: the automation checks `main` again.
- For a revert: revert only the culprit, name the run and the failure in the
  commit message, and put the revert's landed SHA in the harvest message.

Comment the dispatch on the issue, with the delegation's seed as the failure's
owner. Redirect, advise and reassess a stalled delegation as Orchestration
describes. A stalled Sheriff delegation can also need a different role.

When the repair lands, review it against its brief and record the check
verdicts as Orchestration describes. Report only P0 and P1 findings, and say
when you and the Builder ran on the same model. Then add the repair's SHA and
repair checks to the failure's record, and close the delegate as
[Orchestration](../../../internal/prompts/content/skills/attn-workflow/references/orchestration.md)
says.

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

- CI runs one push at a time and keeps only the newest push waiting. When a
  newer push arrives while another push is waiting, the waiting push's run
  concludes `cancelled` with no jobs. A red run therefore covers every commit
  since the last green run. A release commit is the exception: its run starts
  at once, beside the run in progress, and is never dropped.
- You own a test that is still red on `main`. The
  [land-on-main skill](../land-on-main/SKILL.md) says how a landing treats it.
- "no session or crew member matches" or "is not on this daemon" from
  `attn agent msg` means the session is gone: plant the re-land seed.
- This clone holds no work: never commit in it.

## Never

- Fix, revert or commit yourself: delegate.
- Re-run CI, a failed job or a run.
- Skip, disable or quarantine a test.
- Push `main` any way but `scripts/land.sh`, or force-push anything.

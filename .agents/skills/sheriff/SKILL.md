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

```bash
scripts/ci-main-red.sh
```

It prints `<run-id> <attempt>` for main's newest CI run with a failed job,
unless a newer run passed. A run can conclude `cancelled` while jobs inside it
failed, so the script reads the jobs, not the run's conclusion. When it prints
nothing, main is green: end in one line.

## Before you diagnose

- Keep state as comments on the open `Branch health: main acceptance` issue:
  read them first, comment each step, never act twice on the same run and
  attempt.
- `main` may have moved on: a commit since the run's `headSha` may already
  revert or fix the culprit. Then the newer run decides.
- On `attempt` 1, re-run the failed jobs once (`gh run rerun <id> --failed`),
  comment, and stop. If the re-run fails too, the automation wakes you for
  `attempt` 2. If it passes, it was a flake, and the `CI flake ledger` issue
  records it.

## Diagnose

- Read the failure: `gh run view <id> --log-failed`, or
  `gh run view --job <job-id> --log` when that leaves a job out. `Acceptance`
  and `App acceptance` only aggregate: find the job and test under them.
- Find the culprit among the commits since main's last green run:
  `git log <green-sha>..<red-sha>`. Match the failing tests to the commits'
  changes. Read each candidate's `Attn-Session:` and `Attn-Seed:` trailers.

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
    revert a migration (`docs/db-migrations.md`).
  - the culprit is a release commit.
  - the runner, the network or another service failed, not a commit.

## Dispatch

Delegate from this clone into a new worktree:

```bash
attn delegate --role builder --brief-file <file> --cwd <this clone> \
  --new-worktree --branch sheriff-<run-id> --from origin/main
```

Use `--role pathfinder` to investigate. Link the delegation's seed with
`attn seed link <new-seed> discovered-from <seed>` for each of the culprit's
`Attn-Seed:` trailers. Every brief names the failing run and attempt, the
failing jobs and tests with their log lines, the culprit commit and its
trailers, and the path you chose and why. A Builder's brief also says:

- Run the failing test and other targeted tests, and check the affected
  surface. Do not run `make test`, `make lint` or `make test-frontend`: the
  full suites run in CI on `main`.
- Land with `scripts/land.sh` and stop there. Do not watch CI: the automation
  checks `main` again.
- For a revert: revert only the culprit, name the run and the failure in the
  commit message, and put the revert's landed SHA in the harvest message.

Comment the dispatch on the issue, with the delegation's seed.

## No silent reverts

When you dispatch a revert, give the reverted work an owner before you end:

- If the culprit's `Attn-Session:` is live, tell it with
  `attn agent msg <session> "<message>"`: the failure, the run, the revert's
  seed, and how to land again once the revert lands: `git revert <revert-sha>`,
  fix, run targeted tests, check the affected surface, then `scripts/land.sh`.
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
- Skip, disable or quarantine a test.
- Push `main` any way but `scripts/land.sh`, or force-push anything.

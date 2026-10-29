---
name: sheriff
description: Use when main's CI is red, or when asked to check whether main is green.
---

# Sheriff

`main` goes red when a landing breaks it. Make it green by reverting the
commit that broke it, not by fixing it: its author lands it again with a fix.

```bash
gh run list --workflow ci.yml --branch main --event push --status completed \
  --limit 1 --json databaseId,headSha,conclusion,attempt,url
```

Unless that run failed, end in one line.

- Keep state as comments on the open `Branch health: main acceptance` issue:
  read them first, comment each step, never act twice on the same run.
- On `attempt` 1, re-run the failed jobs once
  (`gh run rerun <id> --failed`, then `gh run watch <id> --exit-status`).
  Green means a flake: comment the job and test, and stop.
- Find the culprit among the commits since main's last green run. Match
  `gh run view <id> --log-failed` to them, and run the failing test at each
  candidate when several fit.
- Revert only the culprit, in a throwaway worktree off `origin/main`, and land
  the revert with `scripts/land.sh`. Name the run and failure in its message.
- Tell the author the failure, the run, the revert, and how to land again:
  `git revert <revert-sha>`, fix, `scripts/land.sh`.
  - Its `Attn-Session:` is live: `attn agent msg <session> "<message>"`.
  - Otherwise: `attn delegate --brief-file <file> --cwd <this clone>
    --new-worktree --branch reland-<short-sha> --from origin/main`, then
    `attn seed link <new-seed> discovered-from <seed>` for each `Attn-Seed:`.

## Run `attn turn open` instead of reverting when

- The culprit adds a database migration. Those are fixed forward
  (`docs/db-migrations.md`): send the failure to its author as above.
- The revert conflicts, the culprit is a release commit, or no commit is
  clearly at fault.
- The runner, the network or another service failed, not a commit.

## Gotchas

- CI runs one push at a time and batches the rest, so a red run covers every
  commit since the last green one.
- `main` may have moved on: a newer commit may already revert or fix the
  culprit. Then its own run decides.
- "is not on this daemon" from `attn agent msg` means the session is gone:
  dispatch.
- This clone holds no work: never commit in it.

## Never

- Skip, disable or quarantine a test.
- Push `main` any way but `scripts/land.sh`, or force-push anything.

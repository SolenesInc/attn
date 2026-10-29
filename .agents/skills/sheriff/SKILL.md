---
name: sheriff
description: Keep main green under trunk-based development. When main's CI is red, re-run it once, find the commit that broke it, revert that commit with scripts/land.sh, and tell the session that made it or dispatch an agent to land it again with a fix. Use when the sheriff automation runs, or when asked why main is red.
---

# Sheriff

attn is developed trunk-based (`docs/trunk-based-development.md`): changes land
on `main` before CI has tested them together, so `main` sometimes goes red. The
sheriff makes it green again by reverting the commit that broke it, not by
fixing it: the change comes back when its author lands it again with a fix. A
release waits for a green `main`, so nothing broken reaches users meanwhile.

You run in a clone of attn kept for the automations. It holds no work: make
each revert in a throwaway worktree.

## A pass

1. Read `main`'s latest finished CI run:

   ```bash
   gh run list --workflow ci.yml --branch main --event push --status completed \
     --limit 1 --json databaseId,headSha,conclusion,attempt,url
   ```

   Unless its conclusion is `failure`, end the pass in one line and do nothing
   else.
2. Find the `Branch health: main acceptance` issue
   (`gh issue list --search 'in:title "Branch health: main acceptance"' --state open`)
   and read its comments. Each sheriff step leaves a comment there: continue
   from the last one, and never act twice on the same run.
3. On a first attempt (`attempt` 1), comment that you re-run the failed jobs
   once, then:

   ```bash
   gh run rerun <run> --failed
   gh run watch <run> --exit-status
   ```

   When it passes, it was a flake: comment which job and test failed, and stop.
   The flake ledger tracks repeats.
4. Still red: find the commit that broke it. The run covers every commit since
   `main`'s last green run:

   ```bash
   gh run list --workflow ci.yml --branch main --event push --status success \
     --limit 1 --json headSha
   git fetch origin main
   git log --reverse --format='%H %s' <green-sha>..<red-sha>
   ```

   Read the failure (`gh run view <run> --log-failed`) and match it to the
   commits that touch that code. With several candidates, run the failing test
   at each one in a throwaway worktree when it can run on this machine. When
   `main` has moved past the red commit, look at the newer commits first: one
   may already revert or fix it, and then its own run decides.
5. Revert that commit and land the revert:

   ```bash
   git worktree add -b sheriff-revert-<short-sha> "$(mktemp -d)/revert" origin/main
   cd <that worktree>
   git revert --no-edit <culprit>
   scripts/land.sh
   ```

   Say in the revert's message which run failed and how. Remove the worktree
   after.
6. Tell the author, with the failure, the run, the revert, and how to land the
   change again: on a working branch, `git revert <revert-sha>`, fix, check,
   `scripts/land.sh`.
   - When the culprit's `Attn-Session:` session is live:
     `attn agent msg <session> "<message>"`.
   - Otherwise dispatch an agent with that message as its brief:

     ```bash
     attn delegate --brief-file <brief> --cwd <this clone> \
       --new-worktree --branch reland-<short-sha> --from origin/main
     ```

     Link its seed to each seed the culprit names:
     `attn seed link <new-seed> discovered-from <seed>`.
7. Comment on the issue: the run, the culprit, the revert, and who was told.
   Note the same on your seed and harvest it, as your seed asks; the next pass
   replants it.

## Tell the user instead of reverting

Run `attn turn open` and report when:

- the culprit adds a database migration. Merged migrations are never edited or
  removed (`docs/db-migrations.md`), and instances may already hold it, so it
  is fixed forward: send the failure to its session, or dispatch an agent to
  fix it, as in step 6.
- the revert conflicts, the culprit is a release commit, or you cannot tell
  which commit broke `main`.
- the failure comes from the runner, the network or another service, not from
  a commit.

## Never

- Revert more than the commit that broke `main`.
- Skip, disable or quarantine a test.
- Push `main` any way but `scripts/land.sh`, or force-push anything.
- Comment, message or open a turn while `main` is green.

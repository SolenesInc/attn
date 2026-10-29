---
name: review-main
description: Review the commits main has gained since the reviewed branch, send each finding to the session that made the commit or to an agent dispatched to fix it, and move reviewed forward. Use when the review automation runs, or when asked to review what landed on main.
---

# Review main

attn is developed trunk-based (`docs/trunk-based-development.md`): changes land
on `main` without a pull request, and this review is where they get read. The
`reviewed` branch on `origin` marks the last commit of `main` that has been
reviewed. A release ships only commits that `reviewed` contains.

You run in a clone of attn kept for the automations. It holds no work: never
commit in it, and never push `main` from it.

## A pass

1. Update the clone and list what is not reviewed yet:

   ```bash
   git fetch origin main reviewed
   git checkout --detach origin/main
   git log --reverse --no-merges --format='%H %s' origin/reviewed..origin/main
   ```

   When nothing is listed, end the pass in one line.
2. Take the oldest commits, up to about 30 commits or 3,000 changed lines,
   whichever comes first. The rest waits for the next pass.
3. Group them by the seeds their `Attn-Seed:` trailers name
   (`git log -1 --format='%(trailers:key=Attn-Seed,valueonly)' <sha>`). A
   commit that names two seeds belongs to both groups; a commit that names none
   stands alone. Read each seed before its commits: `attn seed show <seed>`.
   When a commit's reason is unclear, read the conversation behind it:
   `attn session transcript <Attn-Session>`.
4. Review each group with `git show <sha>` and the code around it, as one
   change: later commits of a seed may fix what earlier ones left out. Review
   as the PR posture in `AGENTS.md` says: report only P0 and P1, look for extra
   machinery as well as bugs, and hold tests to `docs/testing.md`. When a claim
   is cheap to check, check it in a throwaway worktree and remove it after:

   ```bash
   git worktree add --detach "$(mktemp -d)/check" origin/main
   ```

5. Send each finding (see [Sending a finding](#sending-a-finding)).
6. Move `reviewed` to the last commit you reviewed:

   ```bash
   git push origin <last-sha>:refs/heads/reviewed
   ```

   A rejected push means another pass moved it first: fetch, and stop.
7. Report the range, each finding and where it went, and anything the user
   must decide. An open P0 matters to the next release. Note the report on
   your seed and harvest it, as your seed asks; the next pass replants it.
   When the user must decide something, run `attn turn open` so the report
   reaches them. Otherwise end the pass quietly.

## Sending a finding

Write one message per commit: its sha and subject, each problem, where it is
(`file:line`), why it matters, and what a fix needs. Ask the author to fix it
and land the fix with `scripts/land.sh`, or to note on its seed why not.

- When the commit's `Attn-Session:` session is live, send it there:
  `attn agent msg <session> "<message>"`. A queued or notified result means it
  arrived.
- When the session is gone ("is not on this daemon"), refuses the message, or
  the commit has no `Attn-Session:`, dispatch an agent to fix it. Write the
  message to a file as its brief, then:

  ```bash
  attn delegate --brief-file <brief> --cwd <this clone> \
    --new-worktree --branch fix-<short-sha> --from origin/main
  ```

  Link the new seed to each seed the commit names:
  `attn seed link <new-seed> discovered-from <seed>`.

Send a finding once. A finding you are not sure of is not sent; put it in the
report instead.

## Never

- Fix the code yourself: the author or the agent you dispatch does.
- Move `reviewed` past a commit you have not read, or back.
- Push `main`.

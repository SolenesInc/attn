---
name: review-main
description: Use when asked to review the commits that landed on main since the last review.
---

# Review main

Changes land on `main` without a pull request; this is where they get read.
`origin/reviewed` marks the last reviewed commit. Done means every P0 and P1
in the commits you read has reached someone who will fix it, and `reviewed`
has moved to the last of them.

- Read `origin/reviewed..origin/main` oldest first, at most about 30 commits
  or 3,000 changed lines per pass.
- Review per seed, not per commit. Group commits by their `Attn-Seed:`
  trailers, read each seed (`attn seed show`) first, and judge its commits as
  one change. `Attn-Session:` names the session behind a commit
  (`attn session transcript`).
- Hold them to the PR posture in `AGENTS.md`.
- Send one message per commit: sha, `file:line`, why it matters, what a fix
  needs, and to land the fix with `scripts/land.sh`.
  - Its `Attn-Session:` is live: `attn agent msg <session> "<message>"`.
  - Otherwise: `attn delegate --brief-file <file> --cwd <this clone>
    --new-worktree --branch fix-<short-sha> --from origin/main`, then
    `attn seed link <new-seed> discovered-from <seed>` for each of its seeds.
- Then `git push origin <last-sha>:refs/heads/reviewed`.
- Note the range and where each finding went on your seed. Run
  `attn turn open` only when the user must decide something.

## Gotchas

- "is not on this daemon" from `attn agent msg` means the session is gone:
  dispatch.
- Commits made outside attn carry no trailers: review each alone.
- A rejected push to `reviewed` means another pass got there first: stop.
- This clone holds no work. Check claims in a throwaway worktree.
- A finding you are unsure of goes in your note, not to an author.

## Never

- Fix the code yourself or push `main`.
- Move `reviewed` back, or past a commit you have not read.

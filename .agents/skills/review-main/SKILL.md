---
name: review-main
description: Use when asked to review the commits that landed on main since the last review.
---

# Review main

Changes land on `main` without a pull request, so you review them here.
`origin/reviewed` marks the last reviewed commit. You are done when every P0
and P1 in `origin/reviewed..<head>` has reached someone who will fix it, and
`origin/reviewed` points at `<head>`.

You orchestrate. Subagents read the code. Review the whole range, however
large. Split the range into units, and give each unit a context of its own.

0. Run `git fetch origin`. Call `origin/main` `<head>` for the rest of the
   pass. Make one temporary folder for the pass,
   `$TMPDIR/review-main-<head short sha>`.
1. Close the fixers you delegated whose seeds are harvested.
   `attn seed ls --plot <your seed>` lists them. For each, read the session
   that worked it from `seed.last_execution_id` in
   `attn seed show <id> --json`, and close it with
   `attn agent close <last_execution_id> -m "<reason>"`.
2. Split the range `origin/reviewed..<head>` into units. A unit is the commits
   of one seed, grouped by their `Attn-Seed:` trailers. A commit that names
   two seeds joins both units. A commit without a trailer is its own unit.
   Split a unit of more than about 2,000 changed lines by area.
3. Find: start at most four subagents, in parallel. With more than four
   units, give each subagent several units. Give each subagent its units,
   their seed ids, the pass's temporary folder and [finding.md](finding.md).
   Tell each subagent not to read generated files, which are the files
   `scripts/land.sh` regenerates.
4. Verify: give the candidate findings of each finder to one fresh subagent,
   with only each claim, its `file:line`, the pass's temporary folder and
   finding.md. The subagent confirms or refutes each finding at `<head>`.
   Send only confirmed findings.
5. Send one message per commit. Give the sha, each finding, what a fix needs,
   and the instruction to land the fix with the `land-on-main` skill.
   - If the commit's `Attn-Session:` is live, send the message with
     `attn agent msg <session> "<message>"`.
   - Otherwise, run `attn delegate --brief-file <file> --cwd <this clone>
     --new-worktree --branch fix-<short-sha> --from origin/main`, with the
     same content in the brief. Then run
     `attn seed link <new-seed> discovered-from <seed>` for each
     `Attn-Seed:` trailer of the commit.
6. Write one note on your own seed: the range, the units, and where each
   finding went.
7. `git push origin <head>:refs/heads/reviewed`.
8. Remove the throwaway worktrees under the pass's temporary folder with
   `git worktree remove --force <path>`, then the folder. Leave the fixers'
   worktrees alone. Run `attn turn open` only when the user must decide
   something.

## Gotchas

- A ring that says your seed `moved: work.ready` is a new run. Start a pass,
  even when `attn seed show --since` lists nothing new.
- Authors message you when they land a fix. Read the message and end your
  turn: the next pass reviews the fix on `main`. Reply only to a question.
- If `attn agent msg` says "no session or crew member matches" or "is not on
  this daemon", the session is gone. Dispatch a fixer.
- A seed's earlier commits may sit before `reviewed`. They are context. Do not
  review them. To list them, run
  `git log --grep 'Attn-Seed: <seed>' origin/main`.
- A later unit may already fix what an earlier unit broke. Judge the code at
  `<head>`.
- If the push to `reviewed` is rejected, another pass pushed first. Stop.
- This clone holds no work.

## Never

- Fix the code yourself or push `main`.
- Move `reviewed` back, or past a unit that no subagent finished.

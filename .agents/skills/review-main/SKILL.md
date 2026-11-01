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
   pass.
1. Split the range `origin/reviewed..<head>` into units. A unit is the commits
   of one seed, grouped by their `Attn-Seed:` trailers. A commit that names
   two seeds joins both units. A commit without a trailer is its own unit.
   Split a unit of more than about 2,000 changed lines by area.
2. Find: start one subagent per unit, all in parallel, in waves if your
   harness caps them. Give each subagent its unit, its seed id, and
   [finding.md](finding.md). Tell each subagent not to read generated files,
   which are the files `scripts/land.sh` regenerates.
3. Verify: give each candidate finding to a fresh subagent, with only the
   claim, the finding's `file:line` and finding.md. The subagent confirms or
   refutes the finding at `<head>`. Send only confirmed findings.
4. Send one message per commit. Give the sha, each finding, what a fix needs,
   and the instruction to land the fix with the `land-on-main` skill.
   - If the commit's `Attn-Session:` is live, send the message with
     `attn agent msg <session> "<message>"`.
   - Otherwise, run `attn delegate --brief-file <file> --cwd <this clone>
     --new-worktree --branch fix-<short-sha> --from origin/main`, with the
     same content in the brief. Then run
     `attn seed link <new-seed> discovered-from <seed>` for each
     `Attn-Seed:` trailer of the commit.
   - After you send each unit, add a note about that unit to your own seed.
5. `git push origin <head>:refs/heads/reviewed`.
6. On your own seed, note the range and where each finding went. Run
   `attn turn open` only when the user must decide something.

## Gotchas

- Your seed persists across passes. A pass that died left notes there. Skip
  the units that pass already sent.
- If `attn agent msg` says "is not on this daemon", the session is gone.
  Dispatch a fixer.
- A seed's earlier commits may sit before `reviewed`. They are context. Do not
  review them. To list them, run
  `git log --grep 'Attn-Seed: <seed>' origin/main`.
- A later unit may already fix what an earlier unit broke. Judge the code at
  `<head>`.
- If the push to `reviewed` is rejected, another pass got there first. Stop.
- This clone holds no work. Subagents check claims in throwaway worktrees.

## Never

- Fix the code yourself or push `main`.
- Move `reviewed` back, or past a unit that no subagent finished.

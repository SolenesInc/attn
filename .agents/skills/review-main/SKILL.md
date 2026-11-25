---
name: review-main
description: Use when asked to review the commits that landed on main since the last review.
---

# Review main

Changes land on `main` without a pull request, so you review them here.
`origin/reviewed` marks the last reviewed commit. You are done when every P0
and P1 in `origin/reviewed..<head>` has reached someone who will fix it, and
`origin/reviewed` points at `<head>`.

You orchestrate attn agents. They read the code; you never do, and you never
use your harness's own subagents. Review the whole range, however large. Split
the range into units, and give each unit a reviewer of its own.

0. Run `git fetch origin`. Call `origin/main` `<head>` for the rest of the
   pass. Call the output of `attn presence --session-id` `<orchestrator>`.
1. Close the reviewers you delegated whose seeds are harvested.
   `attn seed ls --plot <your seed>` lists them. For each, read the session
   that worked it from `seed.last_execution_id` in
   `attn seed show <id> --json`, and close it with
   `attn agent close <last_execution_id> -m "<reason>"`.
2. Split the range `origin/reviewed..<head>` into units. A unit is the commits
   of one seed, grouped by their `Attn-Seed:` trailers. A commit that names
   two seeds joins both units. A commit without a trailer is its own unit.
   Split a unit of more than about 2,000 changed lines by area.
3. Review: delegate at most four reviewers in parallel, each with
   `attn delegate --host ai-sandbox --cwd /home/victor/projects/victor/attn
   --new-worktree --branch review-<head short sha>-<n> --from origin/main`. With
   more than four units, give each reviewer several units. The brief names
   the reviewer's units, their seed ids, `<head>`, `<orchestrator>`,
   [finding.md](finding.md), the instruction not to read generated files,
   which are the files `scripts/land-resolve.sh` regenerates, and
   [the reviewer's job](#the-reviewers-job). End your turn. attn tells you
   when a reviewer messages.
4. Record and route each reviewer's confirmed findings, with one decision per
   commit:
   - Best effort, before routing: record each confirmed defect with
     `land bug <sha> --title "<title>" --severity p0|p1 --source review [--location path:line] --detail-file <file>`
     to capture its short bug id (`land: id <id>`), and post each finding with
     `land event <sha> --kind finding --state fail --title "P0|P1: <title>" --detail-file <file>`.
     If posting fails, note it in your seed note and continue.
   - If the commit's `Attn-Session:` is live, or an agent tends its seed or
     plot, send that agent the sha, each finding (with its bug id when
     registered), what a fix needs, and the instruction to add
     `Fixes-Bug: <id>` to the fix commit's trailers and land the fix with `land`
     (`land guide` and the `land-on-main` skill), using `attn agent msg`. Tell
     the reviewer it is done.
   - Otherwise, reply to the reviewer with `attn agent msg`, telling it to make
     the fix, include `Fixes-Bug: <id>` in the fix commit's trailers, and land
     it with `land` (`land guide` and `land-on-main`).

   A reviewer with no confirmed findings is done. Tell it so.
5. When every reviewer has reported and every finding has been routed, write
   one note on your own seed: the range, the units, where each reviewer ran,
   and where each finding went. Give each routed finding a line of its own,
   starting at the beginning of the line, in this form:

   ```text
   finding: <sha> <seed id, or - when the commit names none> P0|P1 <title>
   ```

   `scripts/workflow-scorecard.mjs` counts these lines, so keep the form exact.
6. `git push origin <head>:refs/heads/reviewed`. A fix still in progress does
   not hold this up. Run `attn turn open` only when the user must decide
   something.

## The reviewer's job

- Run `git reset --hard <head>` in your new worktree. It started at the latest
  `origin/main`, which may be past `<head>`. Then review your units.
- If you spot a concrete pre-existing P0 or P1 bug in surrounding code, plant a
  seed for it with `attn seed plant --discovered-from <seed>`. Omit
  `--discovered-from` when the unit names no seed. Apply the trigger and proof
  bar in `finding.md`, and never plant a seed for cleanup or speculation. Leave
  pre-existing bugs out of your report to `<orchestrator>`.
- Before reporting, check your findings with one fresh subagent of your
  harness. Give it only each claim, its `file:line`, `<head>` and finding.md,
  none of your reasoning. It confirms or refutes each claim at `<head>`. Report
  only confirmed findings.
- Report with `attn agent msg <orchestrator>`, naming each confirmed finding by
  commit, or saying there are none. Then wait for the reply.
- Never use `attn ask`. Send questions to the orchestrator by message.
- Fix only when the orchestrator tells you to.
- Harvest your seed when the orchestrator says you are done, or when your fix
  has landed.

## Gotchas

- A ring that says your seed `moved: work.ready` is a new run. Start a pass,
  even when `attn seed show --since` lists nothing new. While a reviewer of the
  current pass has not reported, keep that pass and end your turn.
- If `attn delegate --host ai-sandbox` refuses as unreachable, delegate the
  reviewer on this machine, with this clone as `--cwd` and no `--host`, and say
  so in your note. Do not fall back to your harness's subagents.
- Authors message you when they land a fix. Read the message and end your
  turn: the next pass reviews the fix on `main`. Reply only to a question.
- If `attn agent msg` says "no session or crew member matches" or "is not on
  this daemon", the session is gone. Tell the reviewer to make the fix.
- A seed's earlier commits may sit before `reviewed`. They are context. Do not
  review them. To list them, run
  `git log --grep 'Attn-Seed: <seed>' origin/main`.
- A later unit may already fix what an earlier unit broke. Judge the code at
  `<head>`.
- If the push to `reviewed` is rejected, another pass pushed first. Stop.
- This clone holds no work.

## Never

- Fix the code yourself or push `main`.
- Move `reviewed` back, or past a unit whose reviewer has not reported.

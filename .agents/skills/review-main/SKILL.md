---
name: review-main
description: Use when asked to review the commits that landed on main since the last review.
---

# Review main

Changes land on `main` without a pull request; this is where they get read.
`origin/reviewed` marks the last reviewed commit. Done means every P0 and P1
in `origin/reviewed..<head>` has reached someone who will fix it, and
`reviewed` has moved to `<head>`, the `origin/main` you fetched at the start.

You orchestrate; subagents read the code. Review the whole range, however
large: it splits into units, and each unit gets a context of its own.

1. Split the range into units. A unit is one seed's commits, grouped by their
   `Attn-Seed:` trailers; a commit naming two seeds joins both. A commit
   without one is its own unit. Split a unit over about 2,000 changed lines
   by area. Generated files (those `scripts/land.sh` regenerates) are not
   read.
2. Find: one subagent per unit, all in parallel, waves if your harness caps
   them. Give each its unit, its seed id, and [finding.md](finding.md).
3. Verify: give each candidate finding to a fresh subagent with only the
   claim, its `file:line` and finding.md, to confirm or refute it at
   `<head>`. Send only confirmed findings.
4. Send one message per commit: sha, each finding, what a fix needs, and to
   run targeted tests, check the affected surface, then land the fix with
   `scripts/land.sh`. Include that verification scope in dispatched fixers'
   briefs too: the full Go and frontend suites run in CI on `main`, not before
   landing. Do not ask for `make test`, `make lint`, or `make test-frontend`.
   - Its `Attn-Session:` is live: `attn agent msg <session> "<message>"`.
   - Otherwise: `attn delegate --brief-file <file> --cwd <this clone>
     --new-worktree --branch fix-<short-sha> --from origin/main`, then
     `attn seed link <new-seed> discovered-from <seed>` for each seed.
   - Note each unit on your seed as you send it.
5. `git push origin <head>:refs/heads/reviewed`.
6. Note the range and where each finding went on your seed. Run
   `attn turn open` only when the user must decide something.

## Gotchas

- Your seed persists across passes. A pass that died left notes there: skip
  the units it already sent.
- "is not on this daemon" from `attn agent msg` means the session is gone:
  dispatch.
- A seed's earlier commits may sit before `reviewed`. They are context, not
  under review: `git log --grep 'Attn-Seed: <seed>' origin/main`.
- A later unit may already fix what an earlier one broke. Judge the code at
  `<head>`.
- A rejected push to `reviewed` means another pass got there first: stop.
- This clone holds no work. Subagents check claims in throwaway worktrees.

## Never

- Fix the code yourself or push `main`.
- Move `reviewed` back, or past a unit no subagent finished.

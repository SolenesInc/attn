---
name: land-on-main
description: Use when landing a change on attn's main, from checking and reviewing it to pushing it with scripts/land.sh.
---

# Land on main

attn lands changes straight on `main`, with no pull request;
`docs/trunk-based-development.md` explains the system around it. A landing is
one push of finished commits through `scripts/land.sh`. Your work ends when
it pushes: CI, the sheriff and review on `main` take it from there.

## Branch

Work on a private, short-lived branch from the latest `main`. It is never a
pull request and never pushed under its own name.

```bash
git fetch origin main && git switch -c <name> origin/main
```

Rebase onto `origin/main` at the start of every step. Land each slice as soon
as it works: commits held back on the branch turn into conflicts. Unfinished
work lands unwired, or behind an experimental setting that is off by default.

## Commit

Inside attn, the commit hook adds the `Attn-Session:` and `Attn-Seed:`
trailers, as `AGENTS.md` describes. Check that every commit carries them.
Outside attn, leave them out.

When the slice adds code that nothing uses yet, say in one line of the commit
message what will use it and which seed or plan section says so. Review on
`main` reads it.

## Check

Run targeted tests for the behavior you changed, then check the change on the
surface it touches: the CLI, the daemon over `wsctl`, a non-production
instance, the app. The drive-attn skill's feature map lists every way into a
feature. Run no full suite, as `AGENTS.md` "Commands and verification" says:
CI runs them on `main` after landing.

Running the scenarios that cover an app-observable change locally, on a
non-production instance, is optional, when the machine can take it. When no
scenario covers the change, add one or tell the user.

Before you land, clean every instance the check started, as the drive-attn
skill's Cleanup says.

## Review

Review the slice before it lands, scaled to its risk:

| Slice | Review |
|---|---|
| A trivial one-liner, or a clean revert | none |
| Docs, agent guidance, prompts or CLI help only | the matching `verify-*` skills |
| Any other change | the matching `verify-*` skills and one adversarial reader |
| Risky: PTY, harness integration, migration, wire protocol, daemon lifecycle | the matching `verify-*` skills and up to three adversarial readers |

A `verify-*` skill matches when its description covers the change. An
adversarial reader looks for findings as
[finding.md](../review-main/finding.md) defines them. Give each reader of a
risky slice its own scope:

- Correctness: edge cases, concurrency, restarts, Linux, what the user sees.
- Every surface: the parallel paths the change should reach: other entry
  points, clients and platforms, the wire contract, the docs, and the way back
  out of anything new.
- Extra machinery: each new layer, option, fallback, cache or copy of state,
  and the current requirement it serves.

Run each reviewer as an independent reader whose only context is the diff
(`git diff origin/main...HEAD`), the slice's direction and the guidance it
names. Use your runtime's native subagents in parallel when it has them;
otherwise run each as its own fresh pass. Reviewers report; they do not edit.

Run reviewers one model step below your own, at one reasoning level above
yours, or at the highest when you are already there. Any model not listed runs
its own reviewers at your reasoning level.

| You | Reviewers |
|---|---|
| Fable | Opus |
| Opus | Sonnet |
| Astra | Sol |
| Sol | Terra |

### Direction

A slice often builds toward later ones, so readers judge it against where it
is going, as finding.md describes. Give every reviewer:

- the seed from the commits' `Attn-Seed:` trailers, and its plot's plan
- which slice of the plan this is
- what the next slices will use from it

### Settle the findings

When every reviewer has reported, merge duplicate findings. Give each finding
to its own verifier, in parallel, with the finding, its `file:line`, the
direction and finding.md. A verifier tries to refute the finding against the
code and finding.md's bar.

Fix the confirmed findings, check the fixes as in [Check](#check), and commit.
Settle uncertain findings yourself against the code, or ask the user.

## Land

```bash
scripts/land.sh
```

It rebases onto `origin/main`, runs a fast gate for the changed paths, and
pushes. It is the only way to push `main`: `.githooks/pre-push` refuses a plain
`git push`. Use `git push --no-verify` only when `scripts/land.sh` cannot run,
after running its checks yourself.

When `main` moves during landing, `scripts/land.sh` rebases and pushes again on
its own, and your checks still count. When it stops on a conflict in your code,
resolve it with `git rebase origin/main`, rerun the checks the resolution
touches, and run `scripts/land.sh` again.

## Report

Report on the seed and in the conversation:

- the landed commits, and the commit your checks ran on when a rebase moved
  them
- each of the seed's checks with its evidence, as
  `attn seed check report <seed> <C#> -m "<summary line>" --evidence
  <file|artifact|url> --commit <rev>`, which copies a local file onto the
  seed, or in a note when the seed has none
- the review: which readers ran, the confirmed findings and their fixes, and
  the findings refuted

Then harvest. Do not watch CI: the sheriff watches `main`.

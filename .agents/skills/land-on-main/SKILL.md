---
name: land-on-main
description: Use when landing a change on attn's main, from checking and reviewing it to pushing it with scripts/land.sh.
---

# Land on main

attn lands changes straight on `main`, with no pull request.
`docs/internal/trunk-based-development.md` explains the system around that. A
landing is one push of finished commits through `scripts/land.sh`. Your work
ends when `scripts/land.sh` pushes. After that, CI, the sheriff and the review
on `main` take over.

## Branch

Work on a private, short-lived branch from the latest `main`. Never open a
pull request from it, and never push it under its own name.

```bash
git fetch origin main && git switch -c <name> origin/main
```

Rebase onto `origin/main` at the start of every step. Land each slice as soon
as it works, because commits held back on the branch turn into conflicts. Land
unfinished work unwired, or behind an experimental setting that is off by
default.

## Commit

Inside attn, the commit hook adds the `Attn-Session:` and `Attn-Seed:`
trailers, as `AGENTS.md` describes. Check that every commit carries them.
Outside attn, leave them out.

When the slice adds code that nothing uses yet, say in one line of the commit
message what will use the code and which seed or plan section says so.
Reviewers on `main` read that line.

## Check

Run targeted tests for the behavior you changed. Then check the change on the
surface it touches: the CLI, the daemon over `wsctl`, a non-production
instance, or the app. The drive-attn skill's feature map lists every way into
a feature. Do not run a full suite. CI runs the full suites on `main` after
landing, as `AGENTS.md` "Commands and verification" says.

When the machine has the capacity, you may run the scenarios that cover an
app-observable change on a non-production instance. When no scenario covers
the change, add one or tell the user. `scripts/land.sh` runs on CI's Linux each
scenario you add or change and, when you change product code, each scenario
listed by a feature file you change, so a scenario you ran on a Mac still gets
its Linux run before the push.

Before you land, clean every instance the check started, as the drive-attn
skill's Cleanup says.

## Review

Review the slice before it lands. Scale the review to the risk of the slice:

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

- Correctness: edge cases, concurrency, restarts, Linux, and what the user
  sees.
- Every surface: the parallel paths the change should reach. These are the
  other entry points, clients and platforms, the wire contract, the docs, and
  the way back out of anything new.
- Extra machinery: each new layer, option, fallback, cache or copy of state,
  and the current requirement that it serves.

Run each reviewer as an independent reader. Its only context is the diff
(`git diff origin/main...HEAD`), the direction of the slice, and the guidance
that the slice names. When your runtime has native subagents, run the
reviewers as subagents in parallel. Otherwise, run each reviewer as its own
fresh pass. Reviewers report. They do not edit.

Run reviewers on the model one step below yours in the table, at one reasoning
level above yours. If you already run at the highest reasoning level, run them
at the highest. If your model is not in the table, run your reviewers at your
own reasoning level.

| You | Reviewers |
|---|---|
| Fable | Opus |
| Opus | Sonnet |
| Astra | Sol |
| Sol | Terra |

### Direction

A slice often builds toward later slices. Reviewers therefore judge the slice
against the direction it is heading, as finding.md describes. Give every
reviewer:

- the seed from the commits' `Attn-Seed:` trailers, and the plan of the
  seed's plot
- which slice of the plan this is
- what the next slices will use from this slice

### Settle the findings

When every reviewer has reported, merge duplicate findings. Give each finding
to its own verifier, in parallel, with the finding, the finding's `file:line`,
the direction and finding.md. The verifier tries to refute the finding against
the code and against the bar in finding.md.

Fix the confirmed findings, check the fixes as in [Check](#check), and commit.
Settle uncertain findings yourself against the code, or ask the user.

## Land

```bash
scripts/land.sh
```

Push with `scripts/land.sh`. It rebases onto `origin/main`, runs the scenarios
your change selects on CI's Linux, runs a fast gate for the changed paths, and
pushes. `.githooks/pre-push` refuses a plain
`git push`. Use `git push --no-verify` only when `scripts/land.sh` cannot run,
after you run its checks yourself.

`scripts/land.sh` refuses commits whose author is not your global git identity,
and says how to fix them. Set a throwaway identity with `git -c user.email=…`,
never `git config` in a worktree, because that writes to the config every
worktree of the clone shares.

The last lines of the output warn when the landing undid earlier commits on
`main`, and name them. The push has already happened. If you did not mean to
undo a commit, restore it right away in a new landing with `git cherry-pick`,
and say so in your report.

One land runs at a time per clone. When a land from another worktree of the
clone holds the land queue, `scripts/land.sh` prints `land: waiting for the
land queue: held by pid <pid> (<worktree>) since <time>` and waits. Waiting is normal: leave the land
running, and do not kill it or start it again. It prints `land: took the land
queue` when its turn comes.

When `main` moves during landing, `scripts/land.sh` rebases, runs its gate
again and pushes on its own. Your checks still count. When `scripts/land.sh`
stops on a conflict in your code, resolve the conflict with
`git rebase origin/main`, rerun the checks that the resolution touches, and
run `scripts/land.sh` again.

One selected scenario adds about five minutes, and 23 added about 17. The land
prints the run's URL while it waits. When a scenario fails there, the land stops before the
push and prints how to read the failure. Fix it and land again: when your change
meant to alter what the scenario expects, update the scenario in this landing. A
product failure on Linux is a finding, as `app/scripts/real-app-harness/AGENTS.md`
says.

The landing tests of the gate come from the shared test history. They are the
groups of `scripts/landing-tests` that the changed paths match, which include
the whole Vitest suite for a change under `app/`, the tests you
changed, and every test that failed on `main` and has since passed there. A
test stays in the gate until it has passed on 30 later `main` commits. The
gate prints each test it added, and why. It lists the tests that are still red
on `main` without running them, because the sheriff owns those.

A failing landing test stops the push like any other check, whether you
touched the test or not. If the test passes on `origin/main` and on a second
run of `scripts/land.sh`, it is a flake. Land, and name the test in your
report. Run `scripts/land.sh --baseline`, which runs the baseline groups
alone, only when the shared test history or the test selection misbehaves. Never use it to skip a
failing test.

## Report

Report on the seed and in the conversation:

- the landed commits, and the commit your checks ran on when a rebase moved
  the landed commits
- the seed's checks. For each check, run
  `attn seed check report <seed> <C#> -m "<summary line>" --evidence
  <file|artifact|url> --commit <rev>`. The command copies a local file onto the
  seed. If the seed has no checks, put the evidence in a note
- the review: which readers ran, the confirmed findings and their fixes, and
  the findings you refuted
- any landing test that failed and passed on a rerun, and whether you used
  `scripts/land.sh --baseline`, and why

Then harvest. Do not watch CI, because the sheriff watches `main`.

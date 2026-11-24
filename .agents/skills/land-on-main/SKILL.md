---
name: land-on-main
description: attn-specific additions to `land guide` when landing a change on attn's main with `land`.
---

# Land on main

attn lands straight on `main` with `land`, with no pull request. Run
`land guide` first for the universal landing steps. This skill holds what attn
adds to them. [Trunk-based development](../../../docs/internal/trunk-based-development.md)
explains the system around landing.

## Commit

Inside attn, `.githooks/prepare-commit-msg` adds the `Attn-Session:` and
`Attn-Seed:` trailers, as `AGENTS.md` describes. Check that every commit
carries them. Outside attn, leave them out.

When a slice adds code that nothing uses yet, state in one line of the commit
message what will use the code and which seed or plan section says so.
Reviewers on `main` read that line.

## Check

Run targeted tests for the behavior you changed. Then check the change on every
surface it touches: the CLI, the daemon over `wsctl`, a non-production
instance, or the app. The `drive-attn` skill's feature map lists every way into
a feature. Do not run a full suite locally. CI runs the full suites on `main`
after landing, as `AGENTS.md` "Commands and verification" says.

When the machine has the capacity, you may run the scenarios that cover an
app-observable change on a non-production instance. When no scenario covers the
change, add one or tell the user. `land`'s gate (`scripts/land-gate.sh`) runs
on Linux each scenario you add or change and, when you change product code,
each scenario listed by a feature file you change, so a scenario you ran on a
Mac still gets its Linux run before the push.

Before you land, clean every instance the check started, as the `drive-attn`
skill's Cleanup says.

## Review

Scale the pre-landing review to the slice's risk:

| Slice | Review |
|---|---|
| A trivial one-liner, or a clean revert | none |
| Docs, agent guidance, prompts or CLI help only | the matching `verify-*` skills |
| Any other change | the matching `verify-*` skills and one adversarial reader |
| Risky: PTY, harness integration, migration, wire protocol, daemon lifecycle | the matching `verify-*` skills and up to three adversarial readers |

A `verify-*` skill matches when its description covers the change. An
adversarial reader judges findings against
[finding.md](../review-main/finding.md), using the three scopes in `land guide`
(Correctness, Every surface, Extra machinery).

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

Give every reviewer:

- the seed from the commits' `Attn-Seed:` trailers, and the plan of the seed's
  plot
- which slice of the plan this is
- what later slices will use from this slice

### Settle the findings

When every reviewer has reported, merge duplicate findings and check each
against the code or text. Judge adversarial findings against
[finding.md](../review-main/finding.md), and `verify-*` findings against that
skill's rules. For any uncertain adversarial finding, run one independent
verifier with the finding, its `file:line`, `HEAD`, the direction, and
`finding.md`. Fix confirmed findings, re-check, and commit.

## Note

Write a short `.land/note.md` for readers of the feed
(`https://land-feed.caketroll.cloud/SolenesInc/attn`): one or two sentences on
what changed for users or maintainers and why. For an app-visible change on
macOS, you can capture a clip with `land record <name> --instance <instance>
[--seconds N]` (which wraps `scripts/pr-evidence.sh record` into `$LAND_MEDIA`)
and paste the printed markdown line into `.land/note.md`.

## Land

```bash
land
```

`land` reads `land.toml`:

- `resolve = "scripts/land-resolve.sh"` automatically regenerates conflicts
  that touch only protocol or prompt generated files during rebase.
- `gate = "scripts/land-gate.sh"` runs the selected Linux scenarios
  (`scripts/land-linux.sh`) beside the fast host gate and landing tests from
  shared test history (`scripts/landing-tests`, `go tool test-history land`),
  then syncs test receipts.
- `feed = "https://land-feed.caketroll.cloud"` publishes the landing ref,
  `.land/note.md`, and any linked recordings.

`.githooks/pre-push` refuses a plain `git push` to `main`. Use
`git push --no-verify` only when `land` cannot run, after you run
`scripts/land-gate.sh` yourself.

One selected scenario adds about five minutes, and 23 added about 17. The gate
prints the run's URL while checks run, cancels the scenario run if checks fail,
and skips any scenario that already passed for the same diff.

A failing landing test stops the push like any other check, whether you touched
the test or not. Never re-run `land` or the test blindly hoping it passes:
`land` refuses a second run on an unchanged diff until you change the diff or
pass `--ack-flake '<unit>' --owner <seed-or-fix-sha>`.

When a landing test fails:

1. Read the failure output, the failing test, and the product code. Check the
   test's history with the printed `history:` command
   (`go tool test-history explain --test '<unit>'`).
2. Check whether a newer commit on `origin/main` already fixed the failure. If
   one did and your diff stays unchanged, run
   `land --ack-flake '<unit>' --owner <fix-sha>`.
3. Otherwise, run a paired reproduction with the printed `repro:` command
   (`-count=10 -race` for Go, or repeated runs for Cargo) on `HEAD` and on
   `origin/main` in a scratch worktree. Never treat a single passing run as
   proof of a flake.
4. If the test fails only on `HEAD`, or fails more often on `HEAD` than on
   `origin/main`, your change caused or worsened it. Fix it before landing.
5. If the test fails on `origin/main` at the same rate, or if both checkouts
   pass all runs while `history:` or the failure trace shows an existing flake
   outside your change:
   - Fix the root cause now when it is small, clear (such as a missing signal,
     a wall-clock wait, or a narrow product race), and within your
     authorization. Land it in its own commit or a separate landing from
     `origin/main`, without widening assertions, adding retries, or raising
     timeouts.
   - Otherwise, attach your failure log and paired counts to an open repair
     seed with a live tender or active sheriff repair for that exact
     `(test, error)`. If none exists and you report to an Orchestrator, report
     the failing line, root mechanism, paired counts, and log to your
     Orchestrator so it can plant or dispatch the repair seed and give you the
     seed id. If you have no Orchestrator, plant a flake seed with those
     details and send it to the chief (`attn seed send-to-chief <seed-id>`).
     Then run `land --ack-flake '<unit>' --owner <seed-id>`.

Run `ATTN_LAND_BASELINE=1 land` (baseline groups alone) only when shared test
history or test selection misbehaves, never to skip a failing test.

## Report

Report on the seed and in the conversation:

- the landed commits (`land: push   main is at <commit>`), the landing feed
  URL (`land: feed   <address>`), and the commit the gate ran on when a rebase
  moved the landed commits
- the seed's checks: run
  `attn seed check report <seed> <C#> -m "<summary line>" --evidence <file|artifact|url> --commit <rev>`.
  If the seed has no checks, put the evidence in a note
- the review: which readers ran, confirmed findings and their fixes, and
  findings you refuted
- any acknowledged flake (`--ack-flake` and its `--owner` seed or fix commit),
  any undone-commit warning you acted on, and whether you used
  `ATTN_LAND_BASELINE=1 land` and why

Then harvest. Do not watch CI, because the sheriff watches `main`.

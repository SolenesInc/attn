---
name: sheriff
description: Use when main's CI is red, or when asked to check whether main is green.
---

# Sheriff

Keep `main` green and keep landings fast. `scripts/land.sh` is a fast filter,
not full CI. Occasional breaks will reach `main`, and repairing them quickly is
your job. Never slow down normal landings to prevent rare breaks.

You triage failures, route small fixes to live authors, and delegate Builders
or Pathfinders. Never edit code, reproduce bugs, commit, or re-run CI yourself.
Keep failure records in the matching Branch health issue and on your own seed,
not in the conversation.

## Pass

Work through these outcomes in order on each run.

### 0. Close out finished work and reclaim stalled repairs

**Outcome**: no finished delegate sits unreviewed or open, no stalled
live-author repair silently blocks `main`, and every landed repair is verified
and recorded.

- Fetch `origin/main` (`git fetch origin main`).
- Review each finished delegate on your plot (`attn seed ls --plot <your seed>`):
  check its landed commits and check reports against its brief, report only P0
  and P1 findings, and say when you and the delegate ran on one model or two
  models from one vendor. Record check verdicts with
  `attn seed check confirm <seed> <C#>` or
  `attn seed check reject <seed> <C#> -m "<what would prove it>" --ring`. Once
  every check is confirmed (or when abandoning the delegate), record the landed
  repair SHA and close the delegate by session id
  (`attn agent close <session> -m "<why>"`), because a closed seed has no
  tender. Leave the delegate open after a rejection so the delegate can act on
  the ring.
- Check live-author owners passively with `attn agent peek <session>`. If the
  author landed a repair, review the commit and record its SHA. If the author
  closed without fixing the failure, declined, or stayed idle for about 30
  minutes while `main` stayed red, tell the author you are taking over (when
  the session is still live) so the author does not land a second fix, then
  delegate a Builder.

### 1. Establish the verdict on `main`

**Outcome**: an exact verdict on `origin/main` across all four CI tracks (Linux
push and full, macOS push and full), acting on the first failed job without
waiting for a run to finish or calling `main` green early.

```bash
scripts/ci-main-red.sh
gh run list --workflow ci.yml --branch main --event push --limit 5 \
  --json databaseId,headSha,status,conclusion
gh run list --workflow ci-macos.yml --branch main --event push --limit 5 \
  --json databaseId,headSha,status,conclusion
git rev-parse origin/main
```

- Inspect queued or running push runs for already failed or timed-out jobs
  right away:
  `gh run view <id> --json jobs --jq '.jobs[] | select(.conclusion == "failure" or .conclusion == "timed_out") | {databaseId, name}'`.
- Inspect every `<run-id> <attempt>` line from `scripts/ci-main-red.sh`.
- Call `main` green only when `scripts/ci-main-red.sh` prints nothing, no
  in-progress run has a failed job, and the newest completed push run of both
  `ci.yml` and `ci-macos.yml` passed at `origin/main`. When nothing is red but
  `origin/main` is still in CI, report the last green SHA and stop.
- For pass records, carried groups, nightly runs, and release commits, see
  [Verifying before landing](../../../docs/internal/trunk-based-development.md#verifying-before-landing).

### 2. Decide which failures need action

**Outcome**: each real failure is tracked once on its own track's Branch health
issue, nothing gets dispatched twice, stale runs predating a repair are
ignored, and repeated failed repairs stop patching and question the premise.

| Workflow | Run kind | Branch health issue |
|---|---|---|
| `CI` (`ci.yml`) | push | `Branch health: main acceptance` |
| `CI` (`ci.yml`) | full (nightly, dispatch, release) | `Branch health: main CI full run` |
| `CI macOS` (`ci-macos.yml`) | push | `Branch health: main macOS` |
| `CI macOS` (`ci-macos.yml`) | full (nightly, dispatch, release) | `Branch health: main full run` |

Read recent comments on the matching issue first. Reopen a closed issue
(`gh issue reopen <number>`) only when acting on an in-progress run of that
same track. Match failures by `(job, test, error)`, not run id:

- **Owned, no landed repair**: log the new run and stop.
- **Covered by a landed repair** (or an unrecorded newer `main` commit whose
  diff fixes or reverts the culprit): if
  `git merge-base --is-ancestor <repair-sha> <head-sha>` fails, log the run and
  wait for CI on a commit containing the repair.
- **Contains repair 1 and fails in the same job and test** (even at another
  step or error): return the failure to its owner with that run.
- **Contains repair 2 and fails in the same job and test**: stop patching. Read
  [Attack the premise](../../../internal/prompts/content/skills/attn-workflow/principles/attack-the-premise.md),
  test the shared assumption against the code or evidence, and bring the owner
  a redesign. Escalate if the owner disagrees.
- **One-off past failure now green on a newer `main` commit**: if a newer run
  on that track passed the same job and test and `CI flake ledger` lists no
  prior failure, log the failure once and stop. Act if the failure repeats.

### 3. Gather enough evidence to pick a path and name a culprit

**Outcome**: the failing job, test or scenario, exact error, evidence artifact,
flake history, and culprit commit (with `Attn-Session:` and `Attn-Seed:`),
gathered through fast reads without reproducing bugs or burning context.

- Fetch failed job logs from the API (works mid-run):
  `gh api --allow-escape-sequences repos/{owner}/{repo}/actions/jobs/<job-id>/logs`.
  Drill past aggregate jobs (`Acceptance`, `macOS acceptance`,
  `Full macOS run`, `App acceptance`, and `App acceptance (native) / Matrix`).
  For App acceptance failures, note the evidence artifact
  (`app-acceptance-evidence-native-<attempt>-shard-<n>` or
  `app-acceptance-macos-native-evidence-<attempt>`).
- Check `CI flake ledger` (or run `go tool test-history sync` and
  `go tool test-history explain --test '<unit>'`). Repeating flakes still need
  a root-cause fix.
- Find candidates in `git log <green-sha>..<red-sha>` (for a full run, look
  back to the last green run from
  `gh run list --workflow <workflow> --branch main --event schedule`).
  Intermittent bugs can predate the last green run.
- When a full run fails in a carried group and the failure is not a flake,
  `.github/ci-inputs.yml` excluded a file that the group reads. The repair must
  fix the break and stop excluding that file in `.github/ci-inputs.yml` (remove
  the file from the group's list or add a `'!'` entry).

### 4. Put each unowned failure on the fastest clean path to green

**Outcome**: every failure has one clear owner working toward a root-cause fix
or clean revert, every revert has a re-land owner, and any escalation is
self-contained for a reader with no context.

- **Fix forward** (clear culprit, small fix): route to the culprit's live
  `Attn-Session:` (or the live tender of its `Attn-Seed:`) first. Check the
  session with `attn agent peek <session>`. If live and not waiting on an
  approval prompt, send the failure and repair bar below with
  `attn agent msg <session>` and ask the author to reply with the landed SHA or
  decline right away. If the session is gone (`"no session or crew member
  matches"` or `"is between sessions"`), closed, or blocked, delegate a
  Builder.
- **Revert** (large or unclear break, or blocks everyone, and
  `git revert <culprit>` applies cleanly on `origin/main` without reverting a
  merged database migration): delegate a Builder immediately, then give the
  reverted work an owner:
  - Message the culprit's live `Attn-Session:` with the failure, run, revert
    seed, and re-land steps (`git revert <revert-sha>`, fix, `land-on-main`).
  - If that session is gone, plant a re-land seed with
    `--discovered-from <first culprit seed>` (link any further culprit seeds
    with `attn seed link <reland-seed> discovered-from <seed>`) and send the
    seed to the chief with `attn seed send-to-chief <reland-seed>`.
- **Investigate** (unclear cause, no clean revert): delegate a Pathfinder with
  diagnosis-only checks. Never paste Builder landing rules into a Pathfinder
  brief.
- **Escalate** (a culprit that adds a database migration when the fix forward
  is not small, a release commit, a runner or service outage, an owner who
  rejects a premise-attack redesign, or no clear path): never revert a merged
  database migration ([Database migrations](../../../docs/internal/db-migrations.md)).
  Comment findings on the matching Branch health issue and run `attn turn open`
  ([Asking the user](#asking-the-user)).

**Delegation constraints**:

- Plant the seed first with explicit `--check`s, `--part-of <your seed>`, and
  `--discovered-from <first culprit seed>` (link any further culprit
  `Attn-Seed:` trailers with
  `attn seed link <new-seed> discovered-from <seed>`). Dispatch that seed with
  `attn delegate --seed <seed> --role builder` (or `--role pathfinder`)
  `--new-worktree --branch sheriff-<run-id>-<slug> --from origin/main`.
- Delegate Linux and general repairs to a Linux outpost (`--host <outpost>`) to
  keep load off the Mac. Fall back to `--cwd <this clone>` (no `--host`) when
  outposts are unreachable or when a macOS-only failure must reproduce on macOS.
- In every brief, name the failing run, job, test, exact error lines, evidence
  artifact name if any, culprit commit and trailers, chosen path, and
  [Fix root causes](../../../internal/prompts/content/skills/attn-workflow/principles/fix-root-causes.md).
- Include this **repair bar** in live-author messages and Builder briefs:
  - Reproduce the failing test alone on the failing platform before fixing
    (control timing with `synctest`, `-race`, `-count`, or load). For Linux
    reproductions that need CI's Ubuntu 24.04 environment (from a Mac, or from
    a non-Ubuntu outpost that lacks Ubuntu's `DejaVu Sans Mono` font), run
    `pnpm --dir app real-app:linux` with `--provider ssh --target attn-linux`
    per [Local Linux runner](../../../docs/internal/linux-runner.md#landing-runner),
    never the primary landing runner. Record receipts through
    `scripts/test-go.sh --packages`, `scripts/landing-tests.sh --unit`, or app
    test commands. Stop and report if the failure will not reproduce.
  - Add the catching test to `scripts/landing-tests` only when the test runs in
    seconds.
  - For a revert, revert only the culprit (`git revert <culprit>`), name the
    run and failure in the commit message, and report the landed revert SHA.
  - Land with [land-on-main](../land-on-main/SKILL.md), report checks, and
    harvest. Do not watch or re-run CI.

### 5. Leave a clean trail and guard zero-tax repeat classes

**Outcome**: GitHub shows current branch health without noise, your seed holds
the scorecard record, and recurring failure classes only produce Correction
seeds that keep landings fast.

- **Branch health issue**: post state transitions only (job, test and error,
  failing SHA, owner, re-land owner for reverts, and repair SHA). Never post
  `GREEN` updates on a closed issue (`scripts/ci-branch-health.sh` closes the
  issue and posts recovery).
- **Your own seed**: write one concise note when a red `main` traces to a
  commit, when the repair lands, and when the first completed CI run containing
  the repair confirms the repair (run, job, test, culprit SHA and `Attn-Seed:`,
  owner, repair SHA, claims labeled measured or inferred).
  `node scripts/workflow-scorecard.mjs escapes` reads these notes.
- **Recurring classes**: when the exact same failure mechanism repeats, follow
  [Corrected failure classes](corrected-classes.md). Never dispatch unsolicited
  Pathfinders to propose slower landing gates.

## Asking the user

Follow
[Never block on the user](../../../internal/prompts/content/skills/attn-workflow/principles/never-block-on-the-user.md):
use `attn ask` for non-blocking questions and keep working, and run
`attn turn open` only for the escalations in
[Step 4](#4-put-each-unowned-failure-on-the-fastest-clean-path-to-green).

Write every question for a reader with **no context on the run, test, or seed**:

- **Plain problem**: one or two plain sentences explaining what broke or what
  the test and code actually do. Explain every test name or term in plain
  English.
- **Concrete options**: two or three choices with each option's real cost
  (including exact time added to landings, if any) and what each buys.
- **Recommendation and fallback**: state your recommendation, what happens if
  unanswered, and the seed for details.

## Gotchas

- An unacquired macOS job on a push run passes `macOS acceptance` with a
  `macOS coverage missing` warning (not a failure). On a full run, an
  unacquired macOS job turns `Full macOS run` red: escalate it as a runner
  outage.
- Waiting push runs replaced by a newer push conclude `cancelled` with no jobs,
  so a red run covers every commit since the last green run. Release commits
  run in their own concurrency group and are never dropped.
- You own tests still red on `main`. [land-on-main](../land-on-main/SKILL.md)
  lists them without blocking unrelated landings.
- Before approving a test-only repair while a live author or plot Orchestrator
  is active on the culprit's seed, confirm whether the product change was
  intentional so a Builder does not weaken a test the author is fixing in
  product code.

## Never

- Edit code, reproduce bugs, revert, or commit yourself (this clone holds no
  work). Route or delegate.
- Re-run CI, a failed job, or a whole run.
- Skip, disable, or quarantine a test.
- Propose or add slow pre-landing gates that tax normal landings.
- Push `main` any way except `scripts/land.sh`, or force-push anything.

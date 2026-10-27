# Run the crew memory eval

Run this eval manually on macOS. It uses real Claude crew sessions and stays
outside CI. The private cases, grading keys, reviewed configuration, and exact
attn binary are attached to `eval-crew-memory-real-agents` (`s-wxcdtv`).
The checkout holds the runner scripts and `sources.json`, which records source
paths, dates, and hashes. This guide is for the operator and agents preparing a
run. Only the user approves the digest and supplies authentication.

## Restore the reviewed flow

Read the seed's latest notes for the reviewed commit, archive, and SHA-256.
Use that checkout and run every command from its repository root. macOS can
clear the flow under `/private/tmp` on reboot. Restore the latest saved archive
if the flow is missing:

```sh
node scripts/evals/crew-memory/restore.mjs /path/to/seed-artifacts/pilot-flow.tar.gz
```

Restore reads the saved `private_root` from the archive and copies the exact
cases, state, binary and saved results there. All entries must belong to that
one root under `/private/tmp`. The usual unit flow uses
`/private/tmp/crew-memory-eval-private`. It refuses to overwrite an existing
flow. It prints the computed digest and saved approval without making model
calls. Keep the pinned Claude executable installed at the reviewed path.
Rebuilding attn, changing the checkout, or changing the configuration changes
the digest. Restore preserves the state file's bytes and field order.

To check the digest at any time without spending:

```sh
node scripts/evals/crew-memory/run.mjs \
  --flow /private/tmp/crew-memory-eval-private/flow --check-harness
```

For a new configuration, edit `_state.json` in the flow directory.
Keep the flow and case pack under `private_root`,
outside the checkout. Set `cases_path`, `attn_binary`, and `claude_binary` to
absolute paths. Pin `member_model`, `member_effort`, `simulator_model`,
`judge_model`, and `reps`. The judge must differ from the member. Include the
reviewed user voice in `voice`, additional hidden directories in `hidden_roots`,
and every other executed helper in `harness_paths`.
Each case needs a public `day_brief` that scopes today's work without revealing
hidden facts. The trial puts it before the copied charter. Add the brief without
editing the case's historical letters.

Pin resolved model ids rather than aliases. The runner rejects model
substitution and missing usage receipts. It saves and checks the member's
resolved model and effort through `attn crew`. The reviewed pilot uses Opus at
medium effort, Sonnet for the simulator, and Fable for the judge.

## Audit before spending

Set `CREW_EVAL_FLOW` to `/private/tmp/crew-memory-eval-private/flow`, then run:

```sh
node scripts/evals/crew-memory/audit.mjs "$CREW_EVAL_FLOW/_state.json"
```

Read `audit.md` under `private_root`. It separates measured checks from checks
that still need real member or judge calls. The sandbox denies the member's
process tree access to the private facts, source crew homes, production socket,
and previous trials. Each attempt gets a fresh member home and Claude configuration with only
onboarding completion, a dark theme, and trust for that member home prefilled.
It carries no authentication or conversation history. Claude's own memory and
prompt suggestions are disabled in both eval arms. A fixed native session name
prevents automatic title-generation calls, so those calls cannot escape the
member's usage accounting. Attn's background model tasks stay off.
Claude's native auto-mode checks have their own requests. Disabling attn's
background tasks does not disable those checks. Treat the dollar receipts as a
lower bound on spend until you have reconciled those auxiliary requests against
the provider's usage record.

Each simulator and judge call gets a fresh working directory and Claude
configuration. Native memory, slash commands, Chrome, and ordinary tools are
disabled. The runner reads structured output and receipts from the result
object. When Claude returns an event array, it uses the last `result` event and
saves the whole array next to the normalized result for inspection.

The simulator may assert only the supplied task, disclosures and public day
brief. It must not turn a member suggestion into a user decision or invent an
approval. After each case, one separate check by the `judge_model` compares
each delivered simulator message with its own task, disclosures and public day
brief. Greetings, thanks, acknowledgements of orientation or checking,
questions, saying "I do not know", and restating the public day brief are
allowed. New product decisions, work approvals, access constraints and changes
to who performs the task still need support in the supplied sources. The
check's verdict and usage are saved with the grading receipts. Unsent
`done=true` messages remain in the call archive and are excluded from that
check.

A reported violation sets `simulator_unfaithful: true` on the result. The result
keeps its grade and the run continues. If the check cannot complete, the
result has a null flag and an `unverified` fidelity status. The case remains
available for review. This model check can make mistakes, so inspect its
reasons before attributing a flagged result to the simulator.

Check the pinned Claude's interactive startup without credentials or model calls:

```sh
node scripts/evals/crew-memory/check-startup.mjs "$CREW_EVAL_FLOW/_state.json"
```

This launches a real crew member through the trial wrapper and sandbox. It must
reach the unauthenticated prompt without onboarding, and every PTY worker must
use a copy of the pinned attn binary. The check removes authentication variables
from its own environment and saves `startup-check.json` under `private_root`.

Then check the actual member lifecycle without paid calls:

```sh
node scripts/evals/crew-memory/check-lifecycle.mjs "$CREW_EVAL_FLOW/_state.json"
node scripts/evals/crew-memory/check-lifecycle.mjs "$CREW_EVAL_FLOW/_state.json" \
  --omit-simulator-start
```

These checks clear authentication variables, use a dummy key and a local
provider mock, and restrict the real member's network access to localhost and
Unix sockets. The first check drives the pinned Claude and attn binaries
through a charter read, two distinct inputs, a tool continuation, crew sleep
and direct sleep confirmation. It files the letter through the controller CLI.
The second deliberately omits the simulator and must fail with a
`controller_handoff` error that reports `controller_handoff_timeout_ms` and
`awaiting=simulator_start`. Receipts and synthetic provider requests go under
the flow's `audit-lifecycle/` directory, outside the paid result files. These
checks prove turn delivery and cleanup. Task quality, model-written letters,
provider latency, and simulator and judge behavior still need the reviewed
pilot.

Check the simulator and judge print-mode calls as well:

```sh
node scripts/evals/crew-memory/check-model-calls.mjs "$CREW_EVAL_FLOW/_state.json"
node scripts/evals/crew-memory/check-model-calls.mjs "$CREW_EVAL_FLOW/_state.json" --refuse-sleep
node scripts/evals/crew-memory/check-model-calls.mjs "$CREW_EVAL_FLOW/_state.json" --never-sleep
```

This drives a complete case through the real member, simulator, and judge
binaries against a local provider mock. It checks native array and object
results, both judge passes, receipt validation, fresh controller directories,
and removal of those directories after completed calls. A synthetic personal
configuration supplies canaries, and the check fails if any canary reaches the
controller requests.

The script also verifies that flagged and unverified fidelity verdicts leave
the case grade in place and that the summary reports the all-case pass rate
beside the checked-faithful pass rate. The member receives a multiline paste
with Claude's native paste-tag rollout enabled in its disposable configuration.
The check verifies the wrapped text reaches the member and the controller
recognizes its completion. It also rechecks a saved fidelity verdict with one
additional fidelity call, preserving the grade and the previous verdict without
starting another member or simulator call. A judge-only replay makes exactly
two outcome-judge calls, changes the synthetic grade and preserves the fidelity
verdict. The summary includes retired-result costs without counting those
results in scores. The script prints its receipt path under
`private_root/print-proof-*`.

The `--refuse-sleep` probe asks for direct confirmation after the inbox sleep
request. The controller sends `sleep` once and records
`sleep_confirmation_needed: true`. The `--never-sleep` probe stays open after
that confirmation and must fail through the measured `crew_sleep` handoff
watchdog. The default probe closes without a confirmation.

The disposable member configuration accepts the dummy key and uses native
permission bypass for its scripted tool calls. Its filesystem and localhost
sandbox restrictions remain active. This proof does not measure production
auto-mode checks or their costs. Provider answers, usage, and grades are
synthetic, so task quality and calibration still need the paid pilot.

To verify Ctrl-C through the runner, prepare a separate, unused pilot flow with
`local_provider_only: true` and include `check-cancellation.mjs` and its imported
helpers in `harness_paths`. The user must approve that exact configuration
through the normal digest gate first. Then run:

```sh
node scripts/evals/crew-memory/check-cancellation.mjs /path/to/cancellation-flow/_state.json
```

The check runs `run.mjs` with dummy authentication and sends it SIGINT once the
first attempt's `runtime.json` appears. It must exit 130, save one
interrupted attempt, clean the instance, and start no second case. It saves
`cli-cancellation-proof.json` in that flow.

## Supply authentication in your terminal

Use zsh for the commands below (`zsh` starts it). Run `claude setup-token`.
Its token draws on your Claude subscription. Enter the token without echoing it:

```sh
read -rs 'CLAUDE_CODE_OAUTH_TOKEN?Evaluation token: '
export CLAUDE_CODE_OAUTH_TOKEN
```

Keep the token out of chat and seed artifacts. The runner inherits the token
and does not serialize it into its wrapper or configuration. A missing token
stops execution before any case starts. Agents must not copy Keychain credentials.

## Approve the digest and run the unit pilot

Review the runner, private cases, and configuration first. The user runs the
approval command, which records the SHA and exits without model calls. Agents
must not pass `--approve-harness` themselves.

```sh
node scripts/evals/crew-memory/run.mjs \
  --flow "$CREW_EVAL_FLOW" --approve-harness
```

The pilot has one repetition of one case per unit. Run its use and write cases
first, then stop to review tokens, dollars, runtime, and failed attempts:

```sh
node scripts/evals/crew-memory/run.mjs \
  --flow "$CREW_EVAL_FLOW" --variant baseline \
  --model claude-opus-5-5 --reps 1 --pilot-stage units
node scripts/evals/crew-memory/summarize.mjs "$CREW_EVAL_FLOW"
```

With `pilot: true`, `units` selects only the two one-day cases. It is the default
stage. The simulator sends a prompt after a fresh native assistant completion
and a settled attn state (`idle` or `waiting_input`). Each response must follow
the matching input. An old idle state or an intermediate tool response cannot
advance the day. The controller calls `attn crew sleep` after the task. If a
fresh native completion leaves the member settled and still registered, the
controller sends the direct user confirmation `sleep` once. It records
`sleep_confirmation_needed` on the result and saves the confirmation in
`sleep-confirmations.json`. The judge grades the filed letters and output files
twice.
The runner captures the work transcript before sleep and keeps whatever closure
records Claude persists. A Bash tool that runs `attn handoff --sleep` can exit
before Claude writes that final tool call to the transcript, so the result's
`trace_limitations` field records this gap. The filed letter and output files
remain the grading evidence.

For a separately approved use-only pilot, record the user's approval under
`approvals.use_pilot` and set `pilot_stage: "use"` in the reviewed state. A
different stage, including the default `units`, is then refused. This stage
selects every use case in the case pack, including controls, with one
repetition. It runs no write or twelve-day case:

```sh
node scripts/evals/crew-memory/run.mjs \
  --flow "$CREW_EVAL_FLOW" --variant baseline \
  --model claude-opus-5-5 --reps 1 --pilot-stage use
```

The ordinary `units` stage still selects the one marked pilot case per unit.
Approval of one stage does not approve another stage or a changed case pack.

Set `controller_handoff_timeout_ms` from the `handoffs` timings in a reviewed
`audit-lifecycle/real-lifecycle-proof.json`. It bounds the local controller's
acceptance and delivery of turns while the member is settled. Member and
simulator provider calls stay under `case_timeout_s`. The receipt must
distinguish the handoff timing from the case timing. The post-sleep wait uses
this same watchdog with phase `crew_sleep`. A member that stays open after
direct confirmation fails its case instead of waiting silently for the whole
case ceiling. A stalled handoff fails with the limit, elapsed time, session,
exchange, and phase. That case fails and the pass continues after cleanup. A
cleanup failure stops the remaining pass. The controller accepts an input only
when it finds the exact sent text, including when Claude encloses the whole
paste in its native `pasted_content` container.

The reviewed `case_timeout_s` values come from the seed's runtime receipt.
They cover the member run and judge calls. After a timeout, the runner still
stops the daemon and cleans the instance. A timeout error names the case, unit,
limit, elapsed seconds, and requested day count. `--timeout-s` can
only repeat the reviewed value.

## Preserve each stage before continuing

Successful rows go to `baseline/results.jsonl`. Every failed attempt goes to
`baseline/errors.jsonl` with its failure class. Calls, events, outcomes, and native usage receipts stay
under `baseline/out/`, with transcripts under `baseline/traces/`. Rerunning a
stage skips completed cases and retries failed cases. To replace a completed
case, preserve its row in `baseline/retired.jsonl` and its old reference and trace before
removing it from the active result set. Retired rows contribute to known spend
and do not contribute to scores. Keep the old outcome artifacts.
Failed attempts also keep, untruncated, whatever native transcript records
existed when the failure was caught.
Press Ctrl-C once to abort the active case, stop its instance, and save partial
calls, native usage, and the interruption in `errors.jsonl`. The runner exits
130 after cleanup and starts no further case. A second Ctrl-C forces Node to
exit and may interrupt that cleanup. Each attempt's `runtime.json` identifies
its instance and data directory while it runs.

Save the entire flow after approval and after each stage, including failed
attempts. Use a new archive name for each snapshot:

```sh
tar -czf /private/tmp/pilot-units.tar.gz -C /private/tmp \
  crew-memory-eval-private/cases.json \
  crew-memory-eval-private/bin \
  crew-memory-eval-private/flow
attn seed attach s-wxcdtv --path /private/tmp/pilot-units.tar.gz --copy
```

Restore that newer archive after a reboot to retain approval and completed
results. Restoring the original pre-approval archive requires human approval
again. Losing completed rows causes those paid cases to run again.

Report the unit measurements to the user. Once the user approves the twelve-day
run, start the held-out pilot separately:

```sh
node scripts/evals/crew-memory/run.mjs \
  --flow "$CREW_EVAL_FLOW" --variant baseline \
  --model claude-opus-5-5 --reps 1 --pilot-stage e2e
```

The `e2e` stage refuses to start until both unit cases have completed. Save a new
flow snapshot afterward. Its twelve days count as one case.

## Run the six-day trajectory pilot

A separately reviewed letters-only stage uses `pilot_stage: "trajectory"`,
`approvals.trajectory_pilot`, and exactly two `trajectory_ids`. Each selected
case has six fixed `scripted_message` values, one output file and required
fields per day, and a frozen `fact_ledger`. Other end-to-end cases still require
twelve days. This stage runs only `baseline`, with one repetition per case:

```sh
node scripts/evals/crew-memory/run.mjs \
  --flow "$CREW_EVAL_FLOW" --variant baseline \
  --model claude-opus-5-5 --reps 1 --pilot-stage trajectory
```

The runner sends each frozen message directly after the member settles. It
makes no simulator or simulator-fidelity model calls for these scripts. Native
input matching confirms delivery, and `scripted-turns.json` records the exact
messages. Fidelity is recorded as `faithful` with mode `fixed_messages`.

Each day's task file is saved to `day-snapshots.json` before closure, so a
later failure preserves completed daily work. The result's `day_outputs` field
holds the same snapshots, so a later file edit cannot repair an earlier probe.
Each day's carry capture (what the member can take into the next day) includes
every new or changed regular file, every new letter and the memory log.
Unchanged initial files remain readable by the member. The outcome keys come
from the frozen messages and fact ledger, never from filed letters. Missing
required fields on a probe day fail the outcome and appear separately in
`probe_omissions`. An explicit JSON `null` counts as a provided answer. The
semantic judges decide whether it is correct.

Two outcome votes grade the work. Two additional votes diagnose each day's
carry against the facts available then, including later reversals. They inspect
all carry surfaces together. Absence from the latest letter alone is not loss.
The diagnostic votes distinguish irrelevant or unscored text from unsupported
assertions. This stage writes no memory entries, so memory-entry metrics do not
apply. These repeated votes measure judge noise. Human calibration remains
separate. All available call receipts count toward known spend. If a carry
diagnostic fails, completed outcome grades remain saved, with partial
diagnostic votes, `diagnostics_status: "unverified"`, and the error. Missing
call receipts leave spend marked incomplete.

Run the native six-day proofs without paid calls before release:

```sh
node scripts/evals/crew-memory/check-model-calls.mjs "$CREW_EVAL_FLOW/_state.json" --trajectory
node scripts/evals/crew-memory/check-model-calls.mjs "$CREW_EVAL_FLOW/_state.json" --trajectory --second-trajectory
node scripts/evals/crew-memory/check-model-calls.mjs "$CREW_EVAL_FLOW/_state.json" --trajectory --omit-probe-field
node scripts/evals/crew-memory/check-model-calls.mjs "$CREW_EVAL_FLOW/_state.json" --trajectory --abort-trajectory-diagnostic
node scripts/evals/crew-memory/check-model-calls.mjs "$CREW_EVAL_FLOW/_state.json" --trajectory --fail-day-six
```

The proofs send each complete scripted message, file six letters and preserve a
day-five snapshot after a deliberate day-six edit. The omission probe fails its
required-field grade even when the synthetic outcome judges return yes. Read
the seed's pre-registered drift and budget rule before sizing the next run.
Neither these proofs nor the pilot approve a full memory comparison. The
private release driver runs the seed's receipt-analysis script afterward,
recording per-type outcomes, omission reasons, judge disagreement, daily role
costs and comparison projections. Projections are inferred from pilot receipts,
not measured costs for a longer run.

## Review the report before a full run

```sh
node scripts/evals/crew-memory/summarize.mjs "$CREW_EVAL_FLOW"
node /path/to/seed-artifacts/build-report-lite.mjs "$CREW_EVAL_FLOW"
```

Open `report.html` for cases and traces. Read `measurements.md` for per-unit
intervals, headroom, judge disagreement, and costs. Pilot judge scores are
exploratory until the user has rated the same artifacts.
The measurements show the all-case pass rate and the pass rate restricted to
cases checked as simulator-faithful. Flagged cases and cases without a successful
fidelity check, including older runs, are excluded from the checked-faithful rate. The
table also shows the flagged and unchecked rep counts and the number of
checked-faithful cases. Known spend includes flagged and unverified cases and the
fidelity calls. A row with an unverified check is marked "+ unknown spend".
`report.html` keeps the original all-case grades.

Keep the cumulative receipt ledger in `spend-ledger.json` at the flow root and
include it in `harness_paths`. It must hold three USD numbers:
`hard_total_cap_usd` (the whole-eval ceiling), `known_spend_usd` (known spend
through a checkpoint), and `known_flow_spend_at_checkpoint_usd` (the part of
this flow's spend already counted there). `summarize.mjs` adds only the flow's
spending since that checkpoint, writes `spend-summary.json`, and prints
cumulative known spend in `measurements.md`. Unknown native and auxiliary
costs remain outside that lower bound. Keep those gaps visible when sizing the
next run.

After reviewing a checker change, recheck saved simulator turns without rerunning
the member, simulator or outcome judges. The same digest and stage approval
gates apply, so pass the stage that produced the rows. This command makes paid
fidelity calls for completed cases only:

```sh
node scripts/evals/crew-memory/run.mjs \
  --flow "$CREW_EVAL_FLOW" --variant baseline \
  --model claude-opus-5-5 --reps 1 --pilot-stage units --recheck-fidelity
```

Each recheck saves the previous result, new verdict and new call receipts under
the case's output directory before updating its result. Grades stay unchanged.
Usage and known spend include both checks. Save the flow before and after the
recheck.

After the user approves an answer-key correction, put the saved case IDs to
regrade in `regrade_ids` in the reviewed state. Then run the judge-only replay:

```sh
node scripts/evals/crew-memory/run.mjs \
  --flow "$CREW_EVAL_FLOW" --variant baseline \
  --model claude-opus-5-5 --reps 1 --pilot-stage units --regrade-only
```

The stage must match the saved cases. The runner refuses a missing saved
result or a target outside that stage. It makes two outcome-judge calls per
selected case and preserves its member output and fidelity verdict. The old
grade, grading record, corrected claims and fresh receipts stay in a
`regrade-*` file. Usage and known spend include the old and new votes. The
runner writes the whole new results ledger first, then swaps it in for the old
one. Fidelity recheck and judge-only regrade are separate operations and
cannot be requested together.

Before setting `pilot` to `false`, record user approval of the case list,
grading, and measured budget under `approvals.cases`, `approvals.grading`, and
`approvals.budget`. Put agreed claim ids in `calibrated_claims`. Those edits
change the digest and require human review and approval again.

A separately approved stale-entry diagnostic can run `--variant v1` with
`--pilot-stage use`. Its reviewed state needs `approvals.use_pilot`,
`approvals.stale_pilot`, and one to three distinct `stale_probe_ids`. Each selected
use case needs `stale_entry` and `frozen_memory`, a directory under the flow
containing exactly that one entry. Include every frozen memory file in
`harness_paths`. Public tasks, historical letters and grading claims stay
identical to the baseline. Each selected probe needs a saved baseline reference
under `baseline/ref/`. Missing references are refused before any member run.
This diagnostic measures harm from an old decision whose later override is
available in the letters. It does not measure memory-writing quality or approve
a full memory comparison. Other memory variants remain disabled pending
approved thresholds and frozen-build verification.

Build the diagnostic fixtures through the real memory CLI on disposable
localhost-only instances with dummy credentials:

```sh
node scripts/evals/crew-memory/freeze-stale-memory.mjs "$CREW_EVAL_FLOW/_state.json"
node scripts/evals/crew-memory/check-model-calls.mjs "$CREW_EVAL_FLOW/_state.json" --stale-memory
```

The fixture builder wakes a bound session, adds the old ruling through
`attn memory add`, verifies the single entry and copies attn's memory files
into the flow. It refuses existing fixture destinations. The entry text names
the old decision date. Its storage timestamp records fixture creation. Use a
memory-capable attn binary and review its digest separately from the baseline
binary. The localhost proof checks both the real provider request and Claude's
native transcript.

Before sending the task, the diagnostic requires the expected entry under
`## Your memory` in the waking member's first main-thread request system
prompt. It matches `api-request` to `api-request-shape` by native session and
shape hash, then saves `wake-view.json`. Missing evidence is a harness failure
before grading. `engagement.wake_view` is true only after that confirmation.
Use-case pairwise judges receive only the task output files, with randomized
labels. The memory log and closure letters are excluded from that comparison.
The seed tracks calibration, the trajectory comparison and remaining evidence.

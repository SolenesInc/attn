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
node scripts/evals/crew-memory/restore.mjs /path/to/seed-artifacts/pilot-flow-r3.tar.gz
```

Restore copies the exact cases, state, binary, and any saved results to
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
and previous trials. Each attempt gets a fresh member home and empty Claude
configuration. Claude's own memory is disabled in both eval arms.

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
stage. The simulator sends prompts only when attn reports `waiting_input`.
The controller calls `attn crew sleep` after the task and grades the filed
letters and output files. The judge grades each output twice.

The reviewed `case_timeout_s` values come from the seed's runtime receipt.
They cover the member run and judge calls. After a timeout, the runner still
stops the daemon and cleans the instance. A timeout error names the case, unit,
limit, elapsed seconds, and requested day count. `--timeout-s` can
only repeat the reviewed value.

## Preserve each stage before continuing

Successful rows go to `baseline/results.jsonl`. Every failed attempt goes to
`baseline/errors.jsonl` with its failure class. Calls, events, outcomes, and native usage receipts stay
under `baseline/out/`, with transcripts under `baseline/traces/`. Rerunning a
stage skips completed cases and retries failed cases.

Save the entire flow after approval and after each stage, including failed
attempts. Use a new archive name for each snapshot:

```sh
tar -czf /private/tmp/pilot-units-r3.tar.gz -C /private/tmp \
  crew-memory-eval-private/cases.json \
  crew-memory-eval-private/bin \
  crew-memory-eval-private/flow
attn seed attach s-wxcdtv --path /private/tmp/pilot-units-r3.tar.gz --copy
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

## Review the report before a full run

```sh
node scripts/evals/crew-memory/summarize.mjs "$CREW_EVAL_FLOW"
node /path/to/seed-artifacts/build-report-lite.mjs "$CREW_EVAL_FLOW"
```

Open `report.html` for cases and traces. Read `measurements.md` for per-unit
intervals, headroom, judge disagreement, and costs. Pilot judge scores are
exploratory until the user has rated the same artifacts.

Before setting `pilot` to `false`, record user approval of the case list,
grading, and measured budget under `approvals.cases`, `approvals.grading`, and
`approvals.budget`. Put agreed claim ids in `calibrated_claims`. Those edits
change the digest and require human review and approval again.

Only `--variant baseline` runs today. Memory variants (`v<N>`) remain disabled
until the memory adapter and frozen-build verification are ready. The seed
tracks the full baseline, thresholds, comparison, and remaining evidence.

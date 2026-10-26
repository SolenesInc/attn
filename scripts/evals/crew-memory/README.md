# Run the crew memory eval

Run this eval manually on macOS. It is outside CI and uses real Claude crew
sessions. The case pack and grading keys are private artifacts on
the seed `eval-crew-memory-real-agents` (`s-wxcdtv`). Git contains the runner and
`sources.json`, which records each case's source, date, and SHA-256.

## Prepare the private flow

Read the seed and its latest notes before running. Copy its private case pack
and reviewed `_state.json` into a private directory outside the checkout.
Set `private_root` to that directory and `cases_path` to the case pack.
Keep the flow directory beneath `private_root`. The member's process tree
cannot read `private_root`, the source crew homes, or the production socket.

Set these fields in `_state.json`:

| Field | Value |
| --- | --- |
| `private_root` | Absolute directory containing private cases and results |
| `cases_path` | Absolute path to the frozen case pack |
| `attn_binary` | Absolute path to a non-production build |
| `claude_binary` | Absolute path to the pinned Claude executable |
| `member_model` | Exact served model id for the member |
| `simulator_model` | Exact served model id for the user simulator |
| `judge_model` | Exact served model id, different from the member |
| `reps` | Reviewed count, matching `--reps`. Must be `1` for the pilot |
| `voice` | Reviewed user voice excerpt |
| `pilot` | `true` for one case per unit, one repetition |
| `hidden_roots` | Additional directories the member cannot read |
| `harness_paths` | Other files executed by the eval command |

Use Claude Code's unpaid model discovery to resolve aliases before pinning ids.
The runner rejects substituted models and missing token or dollar receipts.
The baseline disables Claude Code's own memory and uses letters for continuity.

Each case uses an empty `CLAUDE_CONFIG_DIR`. The user creates an evaluation
token through Claude's supported `setup-token` flow and supplies it as
`CLAUDE_CODE_OAUTH_TOKEN` in their own terminal. The runner inherits that
variable and does not serialize it into its generated wrapper or config files.
Agents must not read or copy Keychain credentials.

In your own terminal, run `claude setup-token`, then enter its token without
echoing it (zsh):

```sh
read -rs 'CLAUDE_CODE_OAUTH_TOKEN?Evaluation token: '
export CLAUDE_CODE_OAUTH_TOKEN
```

Run the approved pilot from that terminal. Keep the token out of chat and seed
artifacts. A missing token stops the runner before any case starts.

## Audit before spending

Set `CREW_EVAL_FLOW` to the private flow directory, then run:

```sh
node scripts/evals/crew-memory/audit.mjs "$CREW_EVAL_FLOW/_state.json"
```

Read `audit.md` under `private_root`. It distinguishes measured checks from
checks that still need real member or judge calls. The pilot must exercise
those remaining checks before a full run.

## Approve and run the pilot

The user reviews the runner, case pack, and configuration, then runs the
`run.mjs` command below once with `--approve-harness` added. It needs no
`--timeout-s` for that step. The flag records the
harness SHA and exits without model calls. Agents must not pass it themselves.
The digest covers the runner, adapter, cases, binaries, configuration, listed
harness files, and lockfiles beside the runner or in the current directory.
A change requires another review.

Set `CREW_EVAL_MEMBER_MODEL` to the reviewed model id and
`CREW_EVAL_TIMEOUT_SECONDS` to the reviewed per-case wall-clock ceiling. Run:

```sh
node scripts/evals/crew-memory/run.mjs \
  --flow "$CREW_EVAL_FLOW" --variant baseline \
  --model "$CREW_EVAL_MEMBER_MODEL" --reps 1 \
  --timeout-s "$CREW_EVAL_TIMEOUT_SECONDS"
```

Each attempt gets a fresh member home and named instance. Cases run one at a
time. The simulator sends turns only after attn reports `waiting_input`.
After each day the runner calls `attn crew sleep`. After the last day it
grades the letters and files the member produced.

Each successful `(case, rep)` writes a row to `baseline/results.jsonl` and
links to `baseline/traces/<case>_rep<rep>.json`. Harness failures append to
`baseline/errors.jsonl`. Running the same command again retries them. Attempts keep available
model calls and events under `baseline/out/`, beside completed outcome artifacts.

## Review measurements before a full run

```sh
node scripts/evals/crew-memory/summarize.mjs "$CREW_EVAL_FLOW"
node /path/to/seed-artifacts/build-report-lite.mjs "$CREW_EVAL_FLOW"
```

Open `report.html` for per-case results and trace links. Read `measurements.md`
for per-unit intervals, headroom, repeated-judge disagreement, and costs.
The end-to-end (`e2e`) run lasts twelve days and counts as one case. Pilot judge grades are
exploratory until the user has rated the same artifacts.

Before setting `pilot` to `false`, record user approval of the case list,
grading method, and measured run budget under `approvals.cases`,
`approvals.grading`, and `approvals.budget`. Put the calibrated claim ids in
`calibrated_claims`. Those edits change the digest, so the user reviews the
configuration and runs the command again with `--approve-harness`.

Only `--variant baseline` runs today. The runner rejects memory variants
(`v<N>`) until the memory adapter and its frozen-build verification are ready.
The seed tracks the full baseline, threshold approval, memory comparison,
and remaining evidence.

# pi receipts, the pin-bump gate

Scripts that measure pi and attn's auto mode against real models. Most drive
pi's SDK directly, without attn. They are the evidence behind the headless
host's design, and they are the gate a pi version bump has
to pass: run them against the new pin, diff the receipts against the ones
recorded in `docs/grounding/pi-plugins.md`, and treat any change as a breaking
change until proven otherwise. pi ships breaking changes every few releases and
has no compat gate — an extension built against old types loads silently and
fails at the first missing call site — so nothing else tells you what moved.

Run them from a checkout, never from a packaged app:

```bash
cd plugins/attn-pi/receipts
bun install
bun smoke.js
```

| Scenario | What it pins down |
| --- | --- |
| `smoke.js` | `createAgentSession` + `bindExtensions`, where the session file lands and when, event ordering around `agent_end`/`agent_settled` |
| `delta-rate.js` | assistant delta rate — the receipt behind the host's ~30 ms coalescing window |
| `steer.js` | steering a live run (slice 2) |
| `abort.js` | aborting a run mid-flight |
| `child-processes.js` | what happens to pi's tool subprocesses |
| `crash-revive.js`, `crash-revive-host.js` | the orphan-on-hard-kill receipt: killing the host strands the tool subprocesses, which is why attn's daemon owns the host's process group and kills the group |
| `memory-slope.js` | memory over a long session |
| `classifier-cost.js` | auto-mode classifier latency/cost/quality across candidate models, against an inline prompt |
| `classifier-verdicts.ts` | what the shipped two-stage classifier decides over the corpus |
| `stage-one-severities.ts` | what stage 1 grades each case at, which is what a threshold change is measured against |

Each scenario writes JSONL to `logs/` and pi session files to `sessions/`, both
gitignored: the scripts are the gate, what they produce is per-run. Session
storage is redirected under this directory and asserted to be — the harness
refuses to run if it would write into `~/.pi/agent/sessions`. The real
`~/.pi/agent` is still read, for auth and resource discovery, exactly the way a
bare `pi` invocation reads it.

The model is pinned in `common.js` and costs real money. Keep the scenarios
short.

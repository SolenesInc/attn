# Auto-mode classifier receipts

Scripts that measure attn's auto-mode classifier against real models. Run them
against the current implementation before changing its model or thresholds,
then compare the candidate behavior.

Run them from a checkout, never from a packaged app:

```bash
cd plugins/attn-pi/receipts
bun install
bun classifier-cost.js
```

| Scenario | What it pins down |
| --- | --- |
| `classifier-cost.js` | auto-mode classifier latency/cost/quality across candidate models, against an inline prompt |
| `classifier-verdicts.ts` | what the shipped two-stage classifier decides over the corpus |
| `stage-one-severities.ts` | what stage 1 grades each case at, which is what a threshold change is measured against |
| `replay-loop.ts` | replays a denial from the live ledger by timestamp (`bun run replay-loop.ts 2026-09-05T23:08 16 provider/model minimal asis|current`; `current` swaps in the checkout's pass-2 instruction), reporting unreadable answers per run: the receipt behind dropping tagged thinking from pass 2 |
| `reason-presence.ts` | how often a model returns a native reasoning block, and whether a trailing `<reason>` tag is answered, at a given effort level |

Each scenario writes per-run JSONL to the gitignored `logs/` directory. The
model calls cost real money, so keep the corpus focused.

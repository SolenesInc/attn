# attn-pi receipts

Scripts that measure this plugin: the Guardian reviewer against real models, and
the cost of the shell parsing behind command permissions. Run them against the
current implementation before changing a model, a threshold or a parser, then
compare the candidate behavior.

Run them from a checkout, never from a packaged app:

```bash
cd plugins/attn-pi/receipts
bun install
bun run guardian-verdict.ts
```

| Scenario | What it pins down |
| --- | --- |
| `guardian-verdict.ts` | what the Guardian decides about a denial recorded in the ledger (`bun run guardian-verdict.ts 2026-09-06T11:55:23 provider/model`), against the real model and the shipped policy prompt |
| `shell-parse-cost.ts` | what the tree-sitter-bash wasm costs to load once, and what one `parseBashCommands` call costs after that (no model calls) |

Each scenario writes per-run output to the gitignored `logs/` directory. The
model calls cost real money, so keep the corpus focused.

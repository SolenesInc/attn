# Usage and cost

Tokens, cost and context window for each session. attn reads what each
session spends from its harness's own transcript, native subagents included,
prices it per model, and shows it on the agent's tile, in a receipt per model
and in the ledger. Usage of a model with no known price still counts its
tokens and marks the dollar amount as partial. People can set their own
prices, and cap a Claude Code or Codex session's context window so it
compacts at a budget they choose.

## Sub-features

- `usage-badge`: the tile header's badge: dollars when priced, `*` when part of it has no price, tokens when nothing is priced.
- `usage-receipt`: totals per model with each one's tokens and cost, from the badge or the palette.
- `ledger-usage`: the same usage on each ledger row and in `attn session show`.
- `context-cap`: cap a Claude Code or Codex session's context window; the agent relaunches to compact at it.
- `prices`: price overrides per model, and which model a usage is billed as.
- `background-caps`: context window caps for the chief and for background model tasks.

## How to get to it

### People

- The tile header's badge, `[data-testid="session-usage-<session>"]`, labelled "Session usage <badge>" (`app/src/components/SessionTerminalDesktop/SessionUsage.tsx`). Hovering or focusing it opens the receipt, `.session-usage-popover`; ↓ pins it and Escape closes it.
- Palette (`app/src/application/useAppCommands.tsx`): `show-session-usage` ("Show <agent>'s usage"), offered once the agent on screen has measured usage, opens the receipt pinned. `set-session-context-cap` ("Cap <agent>'s context window", or "Change <agent>'s context window cap" once set), offered for Claude Code and Codex agents, opens a prompt with the field "Context window cap in tokens" (`app/src/components/SessionContextCapPrompt.tsx`).
- The ledger's Sessions list shows the badge for the selected row (`app/src/components/ledger/SessionsTab.tsx`).
- Settings, Agents and models, "Model pricing overrides" (`app/src/components/SessionCostPriceSettings.tsx`); Settings, Background agents, the Chief and headless context window caps (`app/src/components/SettingsModalSections.tsx`).
- Their own shell: the verbs below.

### Agents

- `attn session show <id>` prints a `usage` line, and `attn session list --json` carries the same (`cmd/attn/session_ledger.go`); the verbs are [ledger.md](ledger.md)'s.
- `attn settings set session_cost.price.<model> '<json>'` sets a price, and `""` clears it (`cmd/attn/settings.go`). The JSON needs `input_usd_per_mtok`, `output_usd_per_mtok`, `cache_read_usd_per_mtok`, `cache_write_5m_usd_per_mtok` and `cache_write_1h_usd_per_mtok`. `headless_context_window_cap` and `chief_context_window_cap` set the background caps. No verb sets a session's context cap.

### Triggers

- As a harness writes its transcript, attn adds each new turn's usage to its session, its native subagents' included, and prices it again when a price setting changes (`internal/daemon/session_cost.go`).
- Setting or clearing a session's cap relaunches its agent with the new compaction window (`internal/daemon/session_context_cap.go`).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`).
- For pixel proof, run `echo 'export ATTN_HARNESS_PARK_VISIBLE_PX=0' >> "$DRIVE_ENV"`, then run `bridge --fresh-launch --wait-ready ping` from the next instance shell. This puts the app fully on screen. Use a native window capture for the receipt and terminal.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-usage.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- An agent folder, `U=$(mktemp -d /tmp/drive-attn-usage-agent.XXXX) && echo "export U=$U" >> $DRIVE_ENV`, holding `{"version": 1, "resumable": true, "turns": [{"includes": "Measure", "actions": [{"type": "reply", "text": "Measured."}]}]}` as `$U/.attn-mock-agent.json`.
- The agent, started and on screen: `wsctl add-session --cwd $U --agent claude --label usage-agent --id usage-agent --place`, then `bridge select_session '{"sessionId":"usage-agent"}'` for setup.

`<terminal>` is the agent's terminal id from `wsctl list`; `<P>` is the proof folder's path written out.

- **Measure one turn.** Run `wsctl input --id <terminal> --text "Measure this session" --enter` and wait for `waiting_input` in `./attn agent list`. Re-read `./attn session show usage-agent` until it prints `usage      2095 tokens, some usage has no price`: the mock reports its model as `mock-agent-1`, which has no price.
- **The badge.** `bridge dom_text '{"selector":"[data-testid=\"session-usage-usage-agent\"]"}'` reads `2.1k tokens`.
- **The receipt from the palette.** Run `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`, `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'`, `bridge native_text '{"text":"Show usage-agent"}'` and `bridge native_key '{"key":"Enter"}'`. `bridge dom_wait '{"selector":".session-usage-popover","timeoutMs":5000}'` matches, and `bridge dom_text '{"selector":".session-usage-popover"}'` includes `2,095 tokens`, `mock-agent-1` and `No price is configured for this model.` Run `bridge native_key '{"key":"Escape"}'`: the receipt closes.
- **Price the model from the shell.** Run `./attn settings set session_cost.price.mock-agent-1 '{"input_usd_per_mtok":1000,"output_usd_per_mtok":1000,"cache_read_usd_per_mtok":1000,"cache_write_5m_usd_per_mtok":1000,"cache_write_1h_usd_per_mtok":1000}'`. It prints `session_cost.price.mock-agent-1 = ` and the JSON. The badge turns into a dollar amount with no `*`, `./attn session show usage-agent` adds the dollars to its `usage` line, and Settings, Agents and models, shows a card for `mock-agent-1` once the collapsed "Model pricing overrides" section is opened. `./attn settings set session_cost.price.mock-agent-1 ""` puts the badge back to tokens.
- **Cap the context window.** Set a new launch banner with `jq '.banner = "Claude Code mock agent (cap set)"' "$U/.attn-mock-agent.json" > "$U/next.json" && mv "$U/next.json" "$U/.attn-mock-agent.json"`. Open the palette, type `Cap usage-agent`, and press Enter. `bridge dom_wait '{"selector":"[aria-label=\"Context window cap in tokens\"]","focused":true,"timeoutMs":5000}'` matches. Run `bridge native_text '{"text":"500000"}'` and `bridge native_key '{"key":"Enter"}'`. `./attn agent peek usage-agent` shows the mock's splash and `• Measured.` replayed, with the banner `Claude Code mock agent (cap set)`, which only the restarted process reads. The palette now offers "Change usage-agent's context window cap" with `Compacts at 500,000 tokens`. Before clearing it, change the banner to `cap cleared` with the same `jq` command. Open it again: the field opens selected, so `bridge native_key '{"key":"backspace"}'` empties it. Press Enter: the agent replays `Measured.` under the `cap cleared` banner, and the palette offers "Cap usage-agent's context window".
- **In the ledger.** Press ⌘⇧L, select the `usage-agent` row with `bridge native_key '{"key":"ArrowDown"}'`, and run `bridge dom_text '{"selector":".ledger-inspector"}'`: it shows the same badge.
- **Background caps.** Open Settings with `bridge native_key '{"key":",","modifiers":["command"]}'` and click `[data-testid="settings-nav-backgroundAgents"]` natively. Run `bridge dom_scroll_into_view '{"selector":"[data-testid=\"settings-headless-context-cap\"]"}'` for setup, then click `[data-testid="settings-headless-context-cap"]` natively, press ⌘A with `bridge native_key '{"key":"a","modifiers":["command"]}'`, type `500000` with `bridge native_text` and press Enter. `[data-testid="settings-headless-context-cap-saved"]` appears and `./attn settings get headless_context_window_cap` prints `500000`. Run `./attn settings set headless_context_window_cap ""` to put it back. The Chief field, `[data-testid="settings-chief-context-cap"]`, works the same way; scroll that field into view before its native click too.
- **Proof.** In `$P`, keep `./attn session show usage-agent` after the turn, after pricing and after clearing it; `bridge dom_text` of the badge and of the receipt; the `./attn settings set` output; `./attn agent peek usage-agent` after the session cap; `./attn settings get headless_context_window_cap` after the background cap; and `bridge capture_native_window_screenshot '{"path":"<P>/usage-receipt.png"}'` with the receipt open. Put the way in in each file's name, such as `usage-palette-receipt.txt`.

## Contexts

- **Durability across restarts**: usage is kept per session and survives every restart, and an upgrade carries over what an earlier version measured. A resumed conversation counts only the usage after the resume, so nothing is counted twice.
- **Profiles**: prices are settings and apply across all profiles; each session's usage stays with it in its profile's ledger.
- **Performance and idle cost**: `internal/sessionmonitor/cost_bench_test.go` and `internal/transcript/parser_bench_test.go` measure reading usage from transcripts.
- **Harnesses**: Claude Code's usage comes from each assistant message, Codex's from its token counts with its tier and context length, Pi's through its plugin. Only Claude Code and Codex agents take a context cap; a shell or out-of-range value is refused.
- **Keyboard and native input**: the badge takes focus, ↓ pins the receipt and Escape closes it; the palette reaches the receipt and the cap without a pointer.
- **Compatibility across versions**: an older version's single cost cursor upgrades without losing unread usage.

## Tests

- Wire: `internal/daemon/session_cost_wire_test.go`, `internal/daemon/session_cost_forks_wire_test.go`, `internal/daemon/session_cost_upgrade_wire_test.go`, `internal/daemon/session_usage_wire_test.go`, `internal/daemon/context_window_cap_wire_test.go`, `internal/daemon/codex_usage_wire_test.go`, `internal/daemon/codex_close_usage_wire_test.go`, `internal/daemon/plugin_usage_wire_test.go`.
- Stack: `internal/daemon/plugin_usage_admission_stack_test.go`.
- Scenario: `session-usage`.
- Kernel: `internal/sessioncost/*_test.go`, `internal/transcript/usage_test.go`, `internal/transcript/usage_sources_test.go`.
- App: `app/src/App.sessionCostPrices.test.tsx`, `app/src/components/ledger/SessionsTab.usage.test.tsx`.

## Gotchas

- Usage can arrive after the agent first reaches waiting. Wait for the measured `usage` line before reading the badge or opening its receipt.
- The palette offers the usage receipt only once the session has usage, and the badge shows nothing before the first turn ends. Measure a turn first.
- The mock's model has no price, so on the baseline the badge shows tokens, not dollars. A dollar amount needs a price override, or usage records of a priced model added to the mock's transcript, as the `session-usage` scenario does.
- `2095` is what one mock turn with one reply spends. A second turn, or a fixture with more actions, adds to it.
- While the ledger is open its row badge shares the tile's `data-testid`. Read the tile's badge with the ledger closed.
- A price override needs all five rates; one with a rate missing is refused, naming the five.
- Use `"resumable": true` in the fixture so the relaunch can find the prior conversation in scratch harness storage. Without it the cap reload starts fresh and has no `Measured.` reply to replay.
- A reload keeps the terminal id and can keep `state_since` when the state stays waiting. Change the fixture banner before a cap action, then read the new banner and replay to prove the process restarted.
- A parked-window `capture_screenshot` can miss the receipt and terminal layers even when DOM reads find them. The screenshot precondition uses an on-screen app and `capture_native_window_screenshot`; it showed the receipt and instance marker on `drive-1`.
- Background-cap fields can be below the settings viewport. Scroll each field into view before converting its bounds to a native click. An out-of-window `native_mouse` coordinate can hang the app until it is restarted; `drive-1` reproduced this at `y=1.30375`, with its headless field at `y=992` in a 768px viewport.
- Setting or clearing a cap relaunches the agent at once.
- Working but wrong: the badge says `2.1k tokens` and the receipt `2,095 tokens` for the same number; and a partially priced session shows a dollar amount with a `*` that only the receipt explains.

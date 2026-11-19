# Usage and cost

Tokens, cost and context window for each session. attn reads what each
session spends from the transcript of its harness, native subagents included.
It prices that usage per model and shows it on the agent's tile, in a receipt
per model and in the ledger. Usage of a model with no known price still counts
its tokens and marks the dollar amount as partial. People can set their own
prices. They can also cap a Claude Code or Codex session's context window, so
the session compacts at a token budget they choose.

## Sub-features

- `usage-badge`: the tile header's badge: dollars when priced, `*` when part of it has no price, tokens when nothing is priced.
- `usage-receipt`: totals per model with each one's tokens and cost, from the badge or the palette.
- `ledger-usage`: the same usage on each ledger row and in `attn session show`.
- `context-cap`: cap a Claude Code or Codex session's context window. The agent relaunches to compact at the cap.
- `prices`: price overrides per model, and which model a usage is billed as.
- `background-caps`: context window caps for the chief and for background model tasks.

## How to get to it

### People

- The tile header's badge, keyed `tile.<pane>.usage` and named "Session usage <badge>" (`native/crates/attn-app/src/desktop/usage.rs`). While the pointer rests on the badge, a preview of the breakdown opens, `tile.<pane>.usage.popover`. A press on the badge pins the breakdown, and Escape or a second press closes it.
- Palette (`native/crates/attn-app/src/palette/commands.rs`): `show-session-usage` ("Show <agent>'s usage"), offered once the agent on screen has measured usage, opens the breakdown pinned. `set-session-context-cap` ("Cap <agent>'s context window", or "Change <agent>'s context window cap" once set), offered for Claude Code and Codex agents, opens the `context-cap-dialog` with the field `context-cap.field` (`native/crates/attn-app/src/context_cap.rs`).
- The ledger's inspector shows a Usage field for the selected session (`native/crates/attn-app/src/ledger/view.rs`).
- **Settings → Agents and models**, "Model pricing overrides" (`native/crates/attn-app/src/settings/agents/pricing.rs`). **Settings → Background agents**, the headless context window cap (`native/crates/attn-app/src/settings/background.rs`).
- Their own shell: the verbs below.

### Agents

- `attn session show <id>` prints a `usage` line, and `attn session list --json` carries the same (`cli/attn/src/commands/session/`). See [ledger.md](ledger.md) for the verbs.
- `attn settings set session_cost.price.<model> '<json>'` sets a price, and `""` clears it (`cli/attn/src/commands/settings.rs`). The JSON needs `input_usd_per_mtok`, `output_usd_per_mtok`, `cache_read_usd_per_mtok`, `cache_write_5m_usd_per_mtok` and `cache_write_1h_usd_per_mtok`. `headless_context_window_cap` sets the background caps. No verb sets a session's context cap.

### Triggers

- As a harness writes its transcript, attn adds each new turn's usage to the session, native subagents included. When a price setting changes, attn prices the usage again (`internal/daemon/session_cost.go`).
- When a session's cap is set or cleared, the daemon relaunches the session's agent with the new compaction window (`internal/daemon/session_context_cap.go`).

## Driving it

Preconditions:

- Use the baseline instance with its app, launched as [../SKILL.md](../SKILL.md) says, then `bridge wait '{"target":{"key":"home"}}'`. On Linux, run every command that reaches the app inside one `xvfb-run -a -s '-noreset -screen 0 1600x1000x24' bash -c '…'`, and press `mod+alt+k` for the palette where macOS has `mod+shift+k`.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-usage.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- Make an agent folder with `U=$(mktemp -d /tmp/drive-attn-usage-agent.XXXX) && echo "export U=$U" >> $DRIVE_ENV`, and save `{"version": 1, "resumable": true, "turns": [{"includes": "Measure", "actions": [{"type": "reply", "text": "Measured."}]}]}` as `$U/.attn-mock-agent.json`.
- Start the agent and put it on screen: run `wsctl add-session --cwd $U --agent claude --label usage-agent --id usage-agent --place`, then `bridge press '{"target":{"key":"sidebar.session.usage-agent"}}'`.
- `<pane>` is the `pane_id` of the pane whose `session_id` is `usage-agent` in `bridge state '{"of":"desktops"}'`, and `<terminal>` its `runtime_id`. `<P>` is the proof folder's path written out.

- **Measure one turn.** Press `terminal.<terminal>`, `bridge type '{"text":"Measure this session"}'` and press `Enter`. Wait for `waiting_input` in `./attn agent list`. Re-read `./attn session show usage-agent` until it prints `usage      2095 tokens, some usage has no price`: the mock reports its model as `mock-agent-1`, which has no price.
- **The badge.** `bridge wait '{"target":{"role":"button","name":"Session usage 2.1k tokens, some usage has no price"}}'` answers with the key `tile.<pane>.usage`.
- **The breakdown on hover.** `bridge hover '{"target":{"key":"tile.<pane>.usage"}}'`: `tile.<pane>.usage.popover` answers, and its rows in `bridge tree` hold `2,095 tokens` and `mock-agent-1`, with no `esc close`. Hover `terminal.<terminal>`: the popover goes.
- **The breakdown pinned by a press.** `bridge press '{"target":{"key":"tile.<pane>.usage"}}'`: the popover opens, and its rows hold `esc close`. Press `Escape`: it goes.
- **The breakdown from the palette.** Press `mod+shift+k`, type `usage-agent usage`, wait for `palette.row.command:show-session-usage` and press `Enter`: the popover opens pinned, and its rows hold `esc close`, `mock-agent-1` and a `*` line that says why part of it has no price. Press `Escape`: it goes.
- **Price the model from the shell.** Run `./attn settings set session_cost.price.mock-agent-1 '{"input_usd_per_mtok":1000,"output_usd_per_mtok":1000,"cache_read_usd_per_mtok":1000,"cache_write_5m_usd_per_mtok":1000,"cache_write_1h_usd_per_mtok":1000}'`. It prints `session_cost.price.mock-agent-1 = ` and the JSON. The badge's name turns into a dollar amount with no `*`, and `./attn session show usage-agent` adds the dollars to its `usage` line. Open Settings with `mod+,`, press `settings.nav.agents`, then `settings.pricing` ("Model pricing overrides"): `settings.price.mock-agent-1` answers. Run `./attn settings set session_cost.price.mock-agent-1 ""` and the badge shows tokens again.
- **Cap the context window.** Set a new launch banner with `jq '.banner = "Claude Code mock agent (cap set)"' "$U/.attn-mock-agent.json" > "$U/next.json" && mv "$U/next.json" "$U/.attn-mock-agent.json"`. Press `mod+shift+k`, type `Cap usage-agent` and press `Enter`: `bridge find '{"target":{"key":"context-cap-dialog"}}'` answers and `context-cap.field` is focused and empty. Type `1` and press `Enter`: `context-cap.refused` names the daemon's range and the dialog stays. Press `mod+a`, type `500000` and press `Enter`: the dialog goes and `./attn agent peek usage-agent` shows the mock's splash and `• Measured.` replayed under the banner `Claude Code mock agent (cap set)`. Only the restarted process reads the new banner. The palette now offers `palette.row.command:set-session-context-cap` named "Change usage-agent's context window cap", described `Compacts at 500,000 tokens — change or clear the cap`. Before clearing the cap, change the banner to `cap cleared` with the same `jq` command. Run the command again, which opens the field holding `500000` selected, then press `backspace` and `Enter`: the agent replays `Measured.` under the `cap cleared` banner, and the palette offers "Cap usage-agent's context window". A shell on screen offers no such command.
- **In the ledger.** Press `mod+shift+l`, then `bridge press '{"target":{"key":"ledger.row.usage-agent"}}'`. `bridge tree` shows the inspector's Usage field with `2.1k tokens`. ↓ selects the first row, which is another session once the ledger holds more than one.
- **Background caps.** Open Settings, press `settings.nav.backgroundAgents`, then `settings.setting.headless_context_window_cap`. Press `mod+a`, type `500000` and press `Enter`: `./attn settings get headless_context_window_cap` prints `500000`. Run `./attn settings set headless_context_window_cap ""` to put it back.
- **Proof.** In `$P`, keep `./attn session show usage-agent` after the turn, after pricing and after clearing the price. Also keep the badge's `bridge find` after each change, the popover's rows from `bridge tree`, the `./attn settings set` output, `./attn agent peek usage-agent` after the session cap, `./attn settings get headless_context_window_cap` after the background cap, and `bridge shot '{"path":"<P>/usage-pinned.png"}'` with the breakdown pinned. Name each file for its way in, such as `usage-palette-receipt.txt`. The `native-session-usage` scenario runs the badge, a native subagent and a partly priced model, the hover and the palette.

## Contexts

- **Durability across restarts**: usage is kept per session and survives every restart, and an upgrade carries over what an earlier version measured. A resumed conversation counts only the usage after the resume, so nothing is counted twice.
- **Linux**: the palette is `mod+alt+k`, Settings `mod+shift+,` and the ledger `mod+alt+l`, where macOS has `mod+shift+k`, `mod+,` and `mod+shift+l`.
- **Profiles**: prices are settings and apply across all profiles. Each session's usage stays with it in its profile's ledger.
- **Performance and idle cost**: `internal/sessionmonitor/cost_bench_test.go` and `internal/transcript/parser_bench_test.go` measure reading usage from transcripts.
- **Harnesses**: attn reads Claude Code's usage from each assistant message. It reads Codex's usage from the Codex token counts together with the tier and the context length. Only Claude Code and Codex agents take a context cap. attn refuses a cap on a shell session, and it refuses a cap value outside the allowed range.
- **Keyboard and native input**: the palette reaches the breakdown and the cap without a pointer, and Escape closes a pinned breakdown.
- **Compatibility across versions**: an older version's single cost cursor upgrades without losing unread usage.

## Tests

- Wire: `native/crates/attn-app/tests/wire/tile_header.rs` (the badge and breakdown), `native/crates/attn-app/tests/wire/palette.rs` (the context cap dialog), `native/crates/attn-app/tests/wire/settings/background.rs` (the headless cap), `internal/daemon/session_cost_wire_test.go`, `internal/daemon/session_cost_forks_wire_test.go`, `internal/daemon/session_cost_upgrade_wire_test.go`, `internal/daemon/session_usage_wire_test.go`, `internal/daemon/context_window_cap_wire_test.go`, `internal/daemon/codex_usage_wire_test.go`, `internal/daemon/codex_close_usage_wire_test.go`.
- Scenario: `native-session-usage`.
- Kernel: `internal/sessioncost/*_test.go`, `internal/transcript/usage_test.go`, `internal/transcript/usage_sources_test.go`.

## Gotchas

- Usage can arrive after the agent first reaches waiting. Wait for the measured `usage` line before reading the badge or opening its breakdown.
- The palette offers the usage breakdown only once the session has usage, and the badge shows nothing before the first turn ends. Measure a turn first.
- The mock's model has no price, so on the baseline the badge shows tokens, not dollars. A dollar amount needs a price override, or usage records of a priced model added to the mock's transcript, as the `native-session-usage` scenario does.
- `2095` is what one mock turn with one reply spends. A second turn, or a fixture with more actions, adds to it.
- A price override needs all five rates. attn refuses an override with a rate missing and names the five.
- Use `"resumable": true` in the fixture so the relaunch can find the prior conversation in scratch harness storage. Without it the cap reload starts fresh and has no `Measured.` reply to replay.
- A reload keeps the terminal id and can keep `state_since` when the state stays waiting. Change the fixture banner before a cap action, then read the new banner and replay to prove the process restarted.
- The daemon relaunches the agent as soon as you set or clear a cap.
- The preview waits for the pointer to rest on the badge before it opens. After `bridge hover`, `bridge wait` on the popover's key waits for it.
- Working but wrong: the badge says `2.1k tokens` and the breakdown `2,095 tokens` for the same number. A partially priced session shows a dollar amount with a `*` that only the breakdown explains.

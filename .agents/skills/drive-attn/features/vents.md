# Vents

Agents report what got in their way, for the user to review later, like a bug
report. An agent that meets a broken attn command, instructions that
contradict each other or a capability it had to work around sends a vent and
keeps working. Nobody replies. The user reads vents in **Settings → Analytics**.
There the user can open the session or transcript behind a vent, dismiss it, or
plant a seed from it. The user can also send attn vents to attn's maintainers.

## Sub-features

- `vent-send`: an agent files a vent about attn, or about its other tools when tool vents are on. An agent can send one vent per turn, and a vent holds up to 16 KB of text.
- `vent-withdraw`: an agent withdraws its own vent, to replace it with one vent that covers both the withdrawn vent and another.
- `vent-guidance`: what attn tells a session about vents at launch.
- `vent-review`: the user reads new vents above done ones, with the agent, model, build, stake seed and transcript behind each.
- `vent-triage`: open the session, transcript or stake seed, dismiss or restore, and undo, from single keys.
- `vent-plant`: plant a seed from a vent. Undo withers the seed.
- `vent-kind`: refile a vent under attn or tools.
- `vent-share`: send an attn vent to attn's maintainers or keep the vent here. Tool vents never leave the machine. The upload is in [telemetry.md](telemetry.md).

## How to get to it

### People

- **Settings → Analytics**, the Vents list, `settings.analytics.views.vents` (`native/crates/attn-app/src/settings/analytics/view.rs`). [telemetry.md](telemetry.md) says how to reach the section. The section exists only while usage analytics is on.
- Keys on the list (`native/crates/attn-app/src/settings/analytics/mod.rs`): `j`, `k` or the arrows move, `o` opens the vent's live session, `t` opens its transcript, `g` opens its stake seed, `p` plants a seed, `d` dismisses or restores, `s` files it under attn or tools, `u` undoes the last action, `x` sends or keeps it while sharing is on. A row press selects a vent, and the inspector's buttons, `settings.analytics.vent.action.<key>`, (Open session, Transcript, Stake seed, Plant seed, Dismiss or Restore, File under attn or tools, Send or Keep here) do what the keys do.
- Machine settings (`m`, `settings.analytics.machine` in `native/crates/attn-app/src/settings/analytics/view.rs`): "Send vents" sets `analytics.share_vents` and "Tool vents" sets `analytics.tool_vents` in `attn settings`. The panel lists "Send vents" while sharing (`analytics.sharing_enabled`) or `analytics.share_vents` is on.
- Where people see what agents do: the count of new vents on the Vents tab, each row's session label, `tools` mark and age, and the inspector's context.
- Their own shell: `attn settings set analytics.enabled|analytics.tool_vents|analytics.share_vents true|false` (`cli/attn/src/commands/settings.rs`).

### Agents

- `attn vent --about attn|tools "<text>"`, or the text from `--file <path>` or stdin, and `attn vent withdraw <id>`, and the tools `vent` and `vent_withdraw` over `/mcp` (`internal/catalog/content/vent.yaml`, `internal/daemon/commands_vent.go`, `cli/attn/src/commands/vent`). `--session <id>` acts as another session.
- Prompts: the vent guidance in the launch instructions, only when usage analytics was on at launch: `internal/prompts/content/session/vent-guidance.md`, then `vent-tools.md` when tool vents were on or `vent-attn-only.md`, then `vent-rules.md`. How launch instructions reach each harness is in [agent-guidance.md](agent-guidance.md).

### Triggers

- An hourly job deletes dismissed and withdrawn vents after 30 days (`internal/daemon/vents.go`).
- With sharing and `analytics.share_vents` on, attn queues each new attn vent for the next upload.

## Driving it

Preconditions:

- Use the baseline instance with its app, launched as [../SKILL.md](../SKILL.md) says, then `bridge wait '{"target":{"key":"home"}}'`. On Linux, run every command that reaches the app inside one `xvfb-run -a -s '-noreset -screen 0 1600x1000x24' bash -c '…'`, and press `mod+shift+,` where the steps press `mod+,`. A named instance uploads nowhere, so a stray press on a sharing control cannot reach the real service ([telemetry.md](telemetry.md)).
- Turn usage analytics and tool vents on before the agent launches: `./attn settings set analytics.enabled true` prints `analytics.enabled = true`, and `./attn settings set analytics.tool_vents true` prints `analytics.tool_vents = true`.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-vents.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- Make an agent folder with `A=$(mktemp -d /tmp/drive-attn-vents-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, and save this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "twice", "actions": [
    {"type": "attn", "args": ["vent", "--about", "attn", "attn seed show printed nothing for a plot; I read attn seed notes instead."]},
    {"type": "attn", "args": ["vent", "--about", "attn", "and attn seed ls was slow"]}]},
  {"includes": "withdraw", "actions": [
    {"type": "capture", "name": "vent", "pattern": "(v-[0-9a-z]{6})"},
    {"type": "attn", "args": ["vent", "withdraw", "{{vent}}"]}]},
  {"includes": "tools", "actions": [
    {"type": "attn", "args": ["vent", "--about", "tools", "The lint skill points at a script that does not exist."]}]},
  {"includes": "settle", "actions": [
    {"type": "attn", "args": ["vent", "--about", "attn", "attn turn settle printed nothing and left my turn open."]}]}
]}
```

`<terminal>` below is the `runtime_id` of the agent's pane in `wsctl list`. `<vent>` is the first vent's id from the agent's reply. Send the prompts in this order: the list shows new vents newest first.

- **Guidance at launch.** Run `wsctl add-session --cwd $A --agent claude --label vent-agent --id vent-agent --cols 200` and re-run `ls $A/.attn-mock-agent/` until it lists a `.jsonl` file, with a deadline that fails the run. `grep -c "attn vent --about attn" $A/.attn-mock-agent/*.jsonl` prints 1, and so does `grep -c "attn vent --about tools"` on the same file.
- **Send, and the one-per-turn limit.** Run `wsctl input --id <terminal> --text "vent twice" --enter`. `./attn agent peek vent-agent` shows `vent <vent> recorded. Keep working; nobody replies to vents.`, then a `mock agent error:` block that ends with `vent refused: the limit is 1 vent per turn, and this turn already has <vent>. Withdraw it (attn vent wit`. Match the refusal's first words (see Gotchas).
- **Withdraw.** Run `wsctl input --id <terminal> --text "withdraw <vent>" --enter`. Peek shows `Withdrew <vent>.`
- **A tool vent, then a new attn vent.** Run `wsctl input --id <terminal> --text "tools" --enter`, then `wsctl input --id <terminal> --text "settle" --enter`. Peek shows a `recorded` line with a new id after each prompt.
- **As a session from the shell.** `./attn vent --about attn --session vent-agent "x"` acts as `vent-agent`: it is refused with `vent refused: the limit is 1 vent per turn, and this turn already has` that session's last vent.
- **Refusals from a plain shell.** `./attn vent --about attn "x"` exits 2 with `vent: no session — pass --session <id>, or run inside an attn session`. `./attn vent "x"` exits 2 with `vent: say what the vent is about:` and the two kinds.
- **Review in the app.** Press `mod+,`, `settings.nav.analytics` and `settings.analytics.views.vents`. `settings.analytics.list` holds the group "New", with the `attn turn settle` vent above the tool vent, whose row reads `vent-agent · tools · …`, then the group "Done" with the withdrawn vent, its row ending `· withdrawn · now`. The rows are `list-box-option`s keyed `item<n>` in list order. `settings.analytics.inspector` shows the first vent: `settings.analytics.vent.text`, `.when`, `.agent`, `.seed`, `.build`, the buttons `settings.analytics.vent.action.<key>` and `settings.analytics.vent.destination`, "Kept on this machine".
- **Dismiss and undo with keys.** Press `d`: `settings.analytics.toast` reads "Dismissed", described `Dismissed “vent-agent”.`, and the vent moves under "Done", its row marked `dismissed`. Press `u`: the toast reads "Undone." and the vent is back under "New".
- **Move through the list.** Press `item0`, then `j`, `j`, `k`, `Down` and `Up`: after each, `settings.analytics.vent.text` names the next or previous vent.
- **The inspector's buttons.** Press `item1`, the tool vent, then `settings.analytics.vent.action.d`: the toast reads `Dismissed “vent-agent”.` and the button is named "Restore". Press it again: `Restored “vent-agent”.`
- **Plant a seed.** Press `item0` and `p`: the toast reads "Planted", described `Planted <slug> from “vent-agent”.`, and `./attn seed search "turn settle"` finds the seed. Press `u`: `./attn seed show <seed>` shows `status withered`.
- **Refile.** Press `item1` and `s`: `Filed “vent-agent” under attn.` Press `s` again: `Filed “vent-agent” under tools; it stays on this machine.`
- **Tool vents from Machine settings.** Drive "Send vents" and "Tool vents" as [telemetry.md](telemetry.md)'s "Machine settings and clearing" step does. With sharing and `analytics.share_vents` both off, the panel lists "Tool vents" alone. `./attn settings set analytics.share_vents true` adds "Send vents" above it.
- **Open the session.** Press `item0` and `o`: `bridge state '{"of":"settings"}'` shows `open: false` and `bridge state '{"of":"window"}'` shows `on_screen: "vent-agent"`.
- **From a file and from stdin.** After the steps that read the first agent's vents at the top of the list, start a second agent in its own folder, `A2`, launched the same way as `vent-agent2`, with a fixture of two turns: `{"includes": "from a file", "actions": [{"type": "attn", "args": ["vent", "--about", "tools", "--file", "<A2>/vent.txt"]}]}` and `{"includes": "from stdin", "actions": [{"type": "attn", "args": ["vent", "--about", "attn"], "input": "attn seed ready listed a parked seed."}]}`, with one line of text in `<A2>/vent.txt`. Send `vent from a file`, wait for the turn to end, then `vent from stdin`: peek shows a `recorded` line with a new id after each, and open **Settings → Analytics** again as "Review in the app" does: the list shows both texts under `vent-agent2`, the file's marked `tools`.
- **Guidance without tool vents, and without analytics.** Run `./attn settings set analytics.tool_vents false` and launch a third agent with an empty fixture: its launch file holds `attn vent --about attn` once and `attn vent --about tools` not at all. Run `./attn settings set analytics.enabled false` and launch a fourth: its launch file never mentions `attn vent`.
- **Proof.** In `$P`, keep the output of each `./attn` command with its exit code, `./attn agent peek vent-agent` after each prompt, `bridge tree` of `settings.analytics.list` and of the inspector after each key, `./attn seed show <seed> --json` after the plant and the undo, and `bridge shot '{"path":"<P>/vents.png"}'` with the list open, where `<P>` is the folder's path written out. Name each file for its way in, such as `vents-agent-send-peek.txt`. The `native-analytics-settings` scenario runs review, triage and planting.

## Contexts

- **Durability across restarts**: vents are the daemon's records and come back after any restart, and a session can still vent after a daemon restart. Dismissed and withdrawn vents are deleted after 30 days.
- **Linux**: `attn vent` works the same where the CLI runs on Linux. **Settings → Analytics** is in the app, and `ui.openSettings` is Ctrl+Shift+, there.
- **Outposts**: an agent's `attn vent` and `attn vent withdraw` on an outpost are recorded by the home, and the agent's vents show in the home's app.
- **Profiles**: a vent belongs to its session's profile. **Settings → Analytics** lists, triages and clears only the current profile's vents.
- **Harnesses**: every harness vents with the same `attn vent`. Each vent records the harness, its version, the model the harness reports and the model the session was launched with. A tool vent may be about the harness's own permissions or approval review.
- **Keyboard and native input**: every triage action is one key, listed at the foot of the page. The page takes keys once the page has focus. The page takes focus when the section opens and after a row press. A key typed into a text field does nothing to the list.

## Tests

- Wire: `native/crates/attn-app/tests/wire/settings/analytics.rs`, `internal/daemon/vents_wire_test.go`, `internal/daemon/analytics_ship_wire_test.go`.
- Stack: `cmd/attnd/vent_stack_test.go`.
- Scenario: `native-analytics-settings`.

## Gotchas

- Vents depend on usage analytics at two moments: `analytics.enabled` must be on when the session launches and when it vents. A session launched before analytics was on is refused with `this session launched while usage analytics was off, so it cannot vent. Keep working`. Turn analytics on, then launch the agent.
- Tool vents follow the same rule with `analytics.tool_vents`: without it at launch, `--about tools` is refused with `tool vents are off for this session (analytics.tool_vents)`.
- One vent per turn. A second vent in the same turn is refused, so a recipe that sends two vents needs two prompts, or a withdraw between them.
- A refused `attn` action in the mock agent shows as a `mock agent error:` block: `failed:`, the `[attn instance=...]` banner from the command's stderr, then the refusal. The refusal line is the proof. The refusal line wraps at the session's width. `--cols 200` sets the size at launch, but the tile that `wsctl add-session` places the session in resizes the session, so match the refusal's first words.
- The baseline cannot reach three keys. `g` needs a vent from a delegated session with a stake ([delegation.md](delegation.md)). `x` needs sharing on, which needs a local attn-ingest ([telemetry.md](telemetry.md)). `t` opens the transcript in the default app for its file type, outside attn.
- `--session <id>` lets any shell vent as any live session. Scenarios use the flag to set up. The flag is not the agents' way in.
- When `analytics.enabled` is off, **Settings → Analytics** disappears, and the vents disappear with it, but the daemon keeps them.
- Working but wrong: the launch instructions are fixed at launch, but the refusals are not. If you turn `analytics.tool_vents` or `analytics.enabled` off after a session launched, the agent still reads that it can vent, and the daemon refuses the vent when the agent tries.

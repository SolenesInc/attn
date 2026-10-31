# Vents

Agents report what got in their way, for the user to review later, like a bug
report. An agent that meets a broken attn command, instructions that
contradict each other or a capability it had to work around sends a vent and
keeps working; nobody replies. The user reads vents in Settings › Analytics,
opens the session or transcript behind one, dismisses it or plants a seed from
it, and can send attn vents to attn's maintainers. Vents need usage analytics
on when the session launched.

## Sub-features

- `vent-send`: an agent files a vent about attn, or about its other tools when tool vents are on; one per turn, up to 16 KB of text.
- `vent-withdraw`: an agent takes back its own vent, to replace it with one that covers both.
- `vent-guidance`: what attn tells a session about vents at launch.
- `vent-review`: the user reads new vents above done ones, with the agent, model, build, stake seed and transcript behind each.
- `vent-triage`: open the session, transcript or stake seed, dismiss or restore, and undo, from single keys.
- `vent-plant`: plant a seed from a vent; undo withers it.
- `vent-kind`: refile a vent under attn or tools.
- `vent-share`: send an attn vent to attn's maintainers or keep it here; tool vents never leave the machine. The upload is in [telemetry.md](telemetry.md).

## How to get to it

### People

- Settings › Analytics, the Vents list (`app/src/components/Analytics/AnalyticsSettings.tsx`). Reaching the section is in [telemetry.md](telemetry.md); it exists only while usage analytics is on.
- Keys on the list (`app/src/components/Analytics/useVentTriage.tsx`): `j`/`k` or the arrows move, `o` opens the vent's live session, `t` opens its transcript, `g` its stake seed, `p` plants a seed, `d` dismisses or restores, `s` files it under attn or tools, `u` undoes the last action, `x` sends or keeps it while sharing is on. A row click selects a vent, and the inspector's buttons (Open session, Transcript, Stake seed, Plant seed, Dismiss or Restore, File under attn or tools, Send or Keep here) do what the keys do.
- Machine settings (`m`, `app/src/components/Analytics/AnalyticsMachine.tsx`): "Send vents", listed while sharing or `analytics.share_vents` is on, and "Tool vents", the same as `analytics.share_vents` and `analytics.tool_vents` in `attn settings`.
- Where people see what agents do: the count of new vents on the Vents tab, each row's session label, `tools` mark and age, and the inspector's context.
- Their own shell: `attn settings set analytics.enabled|analytics.tool_vents|analytics.share_vents true|false` (`cmd/attn/settings.go`).

### Agents

- `attn vent --about attn|tools "<text>"`, or the text from `--file <path>` or stdin, and `attn vent withdraw <id>` (`cmd/attn/vent.go`). `--session <id>` acts as another session.
- Prompts: the vent guidance in the launch instructions, only when usage analytics was on at launch: `internal/prompts/content/session/vent-guidance.md`, then `vent-tools.md` when tool vents were on or `vent-attn-only.md`, then `vent-rules.md`. How launch instructions reach each harness is in [agent-guidance.md](agent-guidance.md).

### Triggers

- An hourly job deletes dismissed and withdrawn vents after 30 days (`internal/daemon/vents.go`).
- With sharing and `analytics.share_vents` on, each new attn vent is queued for the next upload.

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`). The baseline's `ATTN_ANALYTICS_INGEST_URL` keeps a stray click on a sharing control away from the real service ([telemetry.md](telemetry.md)).
- Usage analytics and tool vents on before the agent launches: `./attn settings set analytics.enabled true` prints `analytics.enabled = true`, and `./attn settings set analytics.tool_vents true` prints `analytics.tool_vents = true`.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-vents.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- An agent folder, `A=$(mktemp -d /tmp/drive-attn-vents-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, holding this fixture as `$A/.attn-mock-agent.json`:

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

`<terminal>` below is the agent's terminal id from `wsctl list`; `<vent>` is the first vent's id from the agent's reply. Send the prompts in this order: the list shows new vents newest first.

- **Guidance at launch.** Run `wsctl add-session --cwd $A --agent claude --label vent-agent --id vent-agent --cols 200` and re-run `ls $A/.attn-mock-agent/` until it lists a `.jsonl` file, with a deadline that fails the run. `grep -c "attn vent --about attn" $A/.attn-mock-agent/*.jsonl` prints 1, and so does `grep -c "attn vent --about tools"` on the same file.
- **Send, and the one-per-turn limit.** Run `wsctl input --id <terminal> --text "vent twice" --enter`. `./attn agent peek vent-agent` shows `vent <vent> recorded. Keep working; nobody replies to vents.`, then a `mock agent error:` block that ends with `vent refused: the limit is 1 vent per turn, and this turn already has <vent>. Withdraw it (attn vent wit`. `wsctl add-session` places the session in a tile on the current desktop, which resizes it to the tile's width (106 columns in the parked window), so the refusal wraps; match its first words.
- **Withdraw.** Run `wsctl input --id <terminal> --text "withdraw <vent>" --enter`. Peek shows `Withdrew <vent>.`
- **A tool vent, then a new attn vent.** Run `wsctl input --id <terminal> --text "tools" --enter`, then `wsctl input --id <terminal> --text "settle" --enter`. Peek shows a `recorded` line with a new id after each prompt.
- **From a file and from stdin.** Start a second agent in its own folder, `A2`, launched the same way as `vent-agent2`, with a fixture of two turns: `{"includes": "from a file", "actions": [{"type": "attn", "args": ["vent", "--about", "tools", "--file", "<A2>/vent.txt"]}]}` and `{"includes": "from stdin", "actions": [{"type": "attn", "args": ["vent", "--about", "attn"], "input": "attn seed ready listed a parked seed."}]}`, with one line of text in `<A2>/vent.txt`. Send `vent from a file`, wait for the turn to end, then `vent from stdin`: peek shows a `recorded` line with a new id after each, and the list shows both texts under `vent-agent2`, the file's marked `tools`.
- **As a session from the shell.** `./attn vent --about attn --session vent-agent "x"` acts as `vent-agent`: it is refused with `vent refused: the limit is 1 vent per turn, and this turn already has` that session's last vent.
- **Refusals from a plain shell.** `./attn vent --about attn "x"` exits 2 with `vent: no session — pass --session <id>, or run inside an attn session`. `./attn vent "x"` exits 2 with `vent: say what the vent is about:` and the two kinds.
- **Review in the app.** Press ⌘, with `bridge native_key '{"key":",","modifiers":["command"]}'` and scroll the Analytics nav button into view with `bridge dom_scroll_into_view '{"selector":"[data-testid=\"settings-nav-analytics\"]"}'` before clicking it natively. Run `bridge dom_wait '{"selector":"ol[aria-label=\"Vents\"]","textIncludes":"attn turn settle","timeoutMs":5000}'`. `bridge dom_text '{"selector":"ol[aria-label=\"Vents\"]"}'` reads `New` above the `attn turn settle` vent and the `tools` vent, and `Done` above the withdrawn one, marked `withdrawn`.
- **Dismiss and undo with keys.** Click `ol[aria-label="Vents"] li:first-child button` natively, the `attn turn settle` vent. Run `bridge native_key '{"key":"d"}'`: `bridge dom_wait '{"selector":"[role=\"status\"]","textIncludes":"Dismissed “vent-agent”","timeoutMs":5000}'` matches and the row moves under `Done`. Run `bridge native_key '{"key":"u"}'`: `[role="status"]` reads `Undone.` and the vent is back under `New`.
- **Move through the list.** Click the first row natively, then press `j`, `j`, `k`, `arrowdown` and `arrowup` with `bridge native_key`; after each, `bridge dom_text '{"selector":"[data-testid=\"analytics-inspector\"]"}'` names the next or previous vent.
- **The inspector's buttons.** With a vent selected, `[data-testid="analytics-inspector"] .analytics-actions button` holds the actions as `<key> <label>`. Click the one reading `d Dismiss` natively: `[role="status"]` reads `Dismissed “vent-agent”.` and the button reads `d Restore`; click it again: `Restored “vent-agent”.`
- **Plant a seed.** Click the same first row natively and run `bridge native_key '{"key":"p"}'`. `[role="status"]` reads `Planted <slug> from “vent-agent”.` `./attn seed search "turn settle"` finds the seed, titled with the vent's first line, and its body carries the vent's context. Run `bridge native_key '{"key":"u"}'`: `./attn seed show <seed>` shows `status withered`.
- **Refile.** Click `ol[aria-label="Vents"] li:nth-child(2) button` natively, the tool vent, and run `bridge native_key '{"key":"s"}'`. `[role="status"]` reads `Filed “vent-agent” under attn.`; press `s` again to file it back under tools.
- **Tool vents from Machine settings.** Run `bridge native_key '{"key":"m"}'`, then click `[data-testid="analytics-machine"] .settings-row-card:nth-child(3) button` natively, the "Tool vents" row while sharing and `analytics.share_vents` are both off: `./attn settings get analytics.tool_vents` prints `false`. Click it again: `true`. Run `./attn settings set analytics.share_vents true`, select a row and press `m` again: a "Send vents" row appears above "Tool vents". Click its button natively: `./attn settings get analytics.share_vents` prints `false` and the row goes. Press `m` to close the panel.
- **Open the session.** Closing Machine settings takes focus out of the page, so click `ol[aria-label="Vents"] li:first-child button` natively first, then run `bridge native_key '{"key":"o"}'`. The settings dialog closes (`bridge settings_get_state` shows `open: false`) and the app opens `vent-agent`, placing it on the current desktop: `bridge get_state` shows `activeLeaf.sessionId` set to `vent-agent`.
- **Guidance without tool vents, and without analytics.** Run `./attn settings set analytics.tool_vents false` and launch a third agent with an empty fixture: its launch file holds `attn vent --about attn` once and `attn vent --about tools` not at all. Run `./attn settings set analytics.enabled false` and launch a fourth: its launch file never mentions `attn vent`.
- **Proof.** In `$P`, keep the output of each `./attn` command with its exit code, `./attn agent peek vent-agent` after each prompt, `bridge dom_text` of the list and of `[data-testid="analytics-inspector"]` after each key, `./attn seed show <seed> --json` after the plant and the undo, and `bridge capture_screenshot '{"path":"<P>/vents.png"}'` with the list open, where `<P>` is the folder's path written out. Put the way in in each file's name, such as `vents-agent-send-peek.txt`.

## Contexts

- **Durability across restarts**: vents are the daemon's records and come back after any restart, and a session can still vent after a daemon restart. Dismissed and withdrawn vents are deleted 30 days on.
- **Linux**: `attn vent` works the same where the CLI runs on Linux. Settings › Analytics is in the app, and `ui.openSettings` is Ctrl+Shift+, there.
- **Outposts**: an agent's `attn vent` and `attn vent withdraw` on an outpost are recorded by the home, and its vents show in the home's app.
- **Profiles**: a vent belongs to its session's profile. Settings › Analytics lists, triages and clears only the current profile's vents.
- **Harnesses**: every harness vents with the same `attn vent`. Each vent records the harness, its version and the model. A tool vent may be about the harness's own permissions or approval review.
- **Keyboard and native input**: every triage action is one key, listed at the foot of the page. The page takes keys once it has focus, which it takes when the section opens and after a row click; a key typed into a text field does nothing to the list.

## Tests

- Wire: `internal/daemon/vents_wire_test.go`, `internal/daemon/analytics_ship_wire_test.go`.
- Stack: `cmd/attn/vent_stack_test.go`.
- Scenario: `analytics-settings`.
- App: `app/src/components/Analytics/AnalyticsSettings.vents.test.tsx`.

## Gotchas

- Vents depend on usage analytics at two moments: `analytics.enabled` must be on when the session launches and when it vents. A session launched before analytics was on is refused with `this session launched while usage analytics was off, so it cannot vent. Keep working`. Turn analytics on, then launch the agent.
- Tool vents follow the same rule with `analytics.tool_vents`: without it at launch, `--about tools` is refused with `tool vents are off for this session (analytics.tool_vents)`.
- One vent per turn. A second vent in the same turn is refused, so a recipe that vents twice needs two prompts or a withdraw between.
- A refused `attn` action in the mock agent shows as a `mock agent error:` block: `failed:`, the `[attn instance=...]` banner from the command's stderr, then the refusal. The refusal line is the proof. It wraps at the session's width: `--cols 200` sets the size at launch, but the tile `wsctl add-session` places the session in resizes it, so match the refusal's first words.
- The baseline cannot reach three keys. `g` needs a vent from a delegated session with a stake ([delegation.md](delegation.md)). `x` and "Send vents" need sharing on, which needs a local attn-ingest ([telemetry.md](telemetry.md)). `t` opens the transcript in the default app for its file type, outside attn.
- `--session <id>` lets any shell vent as any live session. Scenarios use it to set up; it is not the agents' way in.
- Settings › Analytics disappears when `analytics.enabled` is off, and the vents with it, though they are kept.
- Working but wrong: the launch instructions are fixed at launch, the refusals are not. Turn `analytics.tool_vents` or `analytics.enabled` off after a session launched, and the agent still reads that it can vent, then is refused when it tries.

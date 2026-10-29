# Vents

Agents report problems they hit and keep working; nobody replies. The user
can review and triage vents, and send attn vents to the maintainers. Vents
need usage analytics on when the session launched.

## Actions and ways in

### File or withdraw a vent

Agents run `attn vent --about attn|tools "<text>"`, or supply text with `--file <path>` or stdin; `attn vent withdraw <id>` takes back a vent, and `--session <id>` acts as another session (`cmd/attn/vent.go`). An agent may file one vent per turn, up to 16 KB, and replace a withdrawn vent with one covering both issues.

Launch instructions include `internal/prompts/content/session/vent-guidance.md` when usage analytics was on, then `vent-tools.md` if tool vents were on or `vent-attn-only.md`, then `vent-rules.md`. [agent-guidance.md](agent-guidance.md) covers delivery.

### Review and triage

Settings › Analytics has the Vents list while usage analytics is on (`app/src/components/Analytics/AnalyticsSettings.tsx`; [telemetry.md](telemetry.md)). It shows new vents above done ones, with the session label, `tools` mark, age and a count of new vents. The inspector shows the agent, model, build, stake seed and transcript behind each vent.

In the list (`app/src/components/Analytics/useVentTriage.tsx`), `j`/`k` or arrows move, `o` opens the live session, `t` its transcript, `g` its stake seed, `p` plants a seed, `d` dismisses or restores, `s` files it under attn or tools, `u` undoes the last action, and `x` sends or keeps it while sharing is on. A row click selects; inspector buttons Open session, Transcript, Stake seed, Plant seed, Dismiss or Restore, File under attn or tools, and Send or Keep here do the same actions. Undoing a planted seed withers it. An hourly job deletes dismissed and withdrawn vents after 30 days (`internal/daemon/vents.go`).

### Choose what to share

Machine settings (`m`, `app/src/components/Analytics/AnalyticsMachine.tsx`) has "Send vents", shown while sharing or `analytics.share_vents` is on, and "Tool vents". People can run `attn settings set analytics.enabled|analytics.tool_vents|analytics.share_vents true|false` (`cmd/attn/settings.go`). With sharing and `analytics.share_vents` on, new attn vents queue for upload; tool vents stay on this machine.

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
- **Send, and the one-per-turn limit.** Run `wsctl input --id <terminal> --text "vent twice" --enter`. `./attn agent peek vent-agent` shows `vent <vent> recorded. Keep working; nobody replies to vents.`, then a `mock agent error:` block that ends with `vent refused: the limit is 1 vent per turn, and this turn already has <vent>. Withdraw it (attn vent withdraw <vent>) and send one vent that covers both`. The session has no tile yet, so nothing resizes its 200 columns and that line does not wrap.
- **Withdraw.** Run `wsctl input --id <terminal> --text "withdraw <vent>" --enter`. Peek shows `Withdrew <vent>.`
- **A tool vent, then a new attn vent.** Run `wsctl input --id <terminal> --text "tools" --enter`, then `wsctl input --id <terminal> --text "settle" --enter`. Peek shows a `recorded` line with a new id after each prompt.
- **Refusals from a plain shell.** `./attn vent --about attn "x"` exits 2 with `vent: no session — pass --session <id>, or run inside an attn session`. `./attn vent "x"` exits 2 with `vent: say what the vent is about:` and the two kinds.
- **Review in the app.** Press ⌘, with `bridge native_key '{"key":",","modifiers":["command"]}'` and click `[data-testid="settings-nav-analytics"]` natively. Run `bridge dom_wait '{"selector":"ol[aria-label=\"Vents\"]","textIncludes":"attn turn settle","timeoutMs":5000}'`. `bridge dom_text '{"selector":"ol[aria-label=\"Vents\"]"}'` reads `New` above the `attn turn settle` vent and the `tools` vent, and `Done` above the withdrawn one, marked `withdrawn`.
- **Dismiss and undo with keys.** Click `ol[aria-label="Vents"] li:first-child button` natively, the `attn turn settle` vent. Run `bridge native_key '{"key":"d"}'`: `bridge dom_wait '{"selector":"[role=\"status\"]","textIncludes":"Dismissed “vent-agent”","timeoutMs":5000}'` matches and the row moves under `Done`. Run `bridge native_key '{"key":"u"}'`: `[role="status"]` reads `Undone.` and the vent is back under `New`.
- **Plant a seed.** Click the same first row natively and run `bridge native_key '{"key":"p"}'`. `[role="status"]` reads `Planted <slug> from “vent-agent”.` `./attn seed search "turn settle"` finds the seed, titled with the vent's first line, and its body carries the vent's context. Run `bridge native_key '{"key":"u"}'`: `./attn seed show <seed>` shows `status withered`.
- **Refile.** Click `ol[aria-label="Vents"] li:nth-child(2) button` natively, the tool vent, and run `bridge native_key '{"key":"s"}'`. `[role="status"]` reads `Filed “vent-agent” under attn.`; press `s` again to file it back under tools.
- **Tool vents from Machine settings.** Run `bridge native_key '{"key":"m"}'`, then click `[data-testid="analytics-machine"] .settings-row-card:nth-child(3) button` natively, the "Tool vents" row while sharing and `analytics.share_vents` are both off: `./attn settings get analytics.tool_vents` prints `false`. Click it again: `true`. Press `m` to close the panel.
- **Open the session.** Closing Machine settings takes focus out of the page, so click `ol[aria-label="Vents"] li:first-child button` natively first, then run `bridge native_key '{"key":"o"}'`. The settings dialog closes (`bridge settings_get_state` shows `open: false`) and the app opens `vent-agent`, placing it on the current desktop: `bridge get_state` shows `activeLeaf.sessionId` set to `vent-agent`.
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
- A refused `attn` action in the mock agent shows as a `mock agent error:` block: `failed:`, the `[attn instance=...]` banner from the command's stderr, then the refusal. The refusal line is the proof; at the default 80 columns it wraps, which is why the session starts with `--cols 200` and stays without a tile until the last step opens it.
- The baseline cannot reach three keys. `g` needs a vent from a delegated session with a stake ([delegation.md](delegation.md)). `x` and "Send vents" need sharing on, which needs a local attn-ingest ([telemetry.md](telemetry.md)). `t` opens the transcript in the default app for its file type, outside attn.
- `--session <id>` lets any shell vent as any live session. Scenarios use it to set up; it is not the agents' way in.
- Settings › Analytics disappears when `analytics.enabled` is off, and the vents with it, though they are kept.
- Working but wrong: the launch instructions are fixed at launch, the refusals are not. Turn `analytics.tool_vents` or `analytics.enabled` off after a session launched, and the agent still reads that it can vent, then is refused when it tries.

# Queue flow

Queue flow brings the user the next agent that needs them and moves them on
after every prompt, like working an inbox to zero. An agent that stops is owed
a turn. The queue lists turns with priority first, then oldest first. The user
settles a turn when they are done with it, snoozes an agent until later, or
marks one as priority. Auto-settle can close a turn once the user has steered
the agent and it went back to work. Agents put themselves in the queue with
`attn turn open` when they cannot go on without the user. Automation runs never
queue, but the user settles a run's turn the same way in either flow.

## Actions and ways in

Shortcuts are in `app/src/shortcuts/registry.ts`, palette commands in
`app/src/application/useAppCommands.tsx`, and queue rows in
`app/src/components/QueueSidebar.tsx` and `app/src/components/QueueRows.tsx`.
Settle, snooze and priority keys act on the focused queue row, otherwise on
the agent on screen. In the agent palette they act on the highlighted agent;
[palette.md](palette.md) drives those keys.

### Switch flow

The sidebar button says "Switch to queue flow" or "Switch to desktop flow"
(`app/src/components/SidebarHeader.tsx`). The palette's `toggle-queue-mode`
says "Turn on the agent queue" or "Turn off the agent queue". A person can
also run `attn settings set queue_mode_enabled true` in their own shell.
The sidebar changes shape ([sidebar.md](sidebar.md)); the setting is shared
across profiles today.

### Open and find a turn

A turn opens when an agent becomes waiting, idle, needs approval or unknown
(`internal/attention/turn.go`). Shells and the chief never owe a turn;
automation runs can owe turns but never queue. Priority turns come first, then the
oldest. An agent can run `attn turn open` (`cmd/attn/turn.go`) to request a turn.
When called while working, it opens at the next stop. With queue flow off it
succeeds and changes nothing.

Experimental quiet notifications change a notification run's stop: it opens
a turn only for approval, an unknown state, or an agent that ran
`attn turn open`. Enable "Quiet notifications in the queue" in Settings, Experimental
(`[data-testid="settings-nav-experimental"]`,
`settings-queue-quiet-notifications-toggle`,
`app/src/components/SettingsModalSections.tsx`), or set
`queue_quiet_notifications` through `attn settings` ([settings.md](settings.md),
`internal/daemon/settings_registry.go`). After inbox items in a quiet notification run, `attn agent inbox` prints a
note about this behavior (`internal/prompts/content/session/inbox-quiet.md`).
`attn turn --help` explains when to open or settle a turn. The orchestration
reference (`internal/prompts/content/skills/attn-workflow/references/orchestration.md`)
tells agents to open one before ending with questions when they cannot use an ask.

⌘J (`session.jumpToWaiting`), the sidebar's "N waiting" head
`[data-testid="queue-waiting-head"]`, and palette command `jump-to-waiting`
("Jump to the oldest turn") open the head. ⌘↑ (`session.prev`) and ⌘↓
(`session.next`) walk turns, working agents, then snoozed agents, wrapping;
from home they start at the first or last row. [desktops.md](desktops.md)
owns these keys in desktop flow.

The sidebar shows the waiting count, turn age, priority and row order. Home
shows turns under "Your turn", snoozed agents with their unsnooze button, and
"All settled" when nothing is owed ([home.md](home.md) drives those controls).
`attn agent list` shows `owed` in the TURN column (`cmd/attn/agent.go`), and
`attn session list --tree` shows `owed` or `snoozed until <instant>`
(`cmd/attn/session_tree.go`). `attn callout` can point at the `queue` anchor,
the waiting card or the queue bar's waiting pill ([callouts.md](callouts.md)).

### Settle a turn

⌘⇧E (`session.settle`), a turn row's ✓ `[aria-label="Settle <name>"]`, or
the palette's `settle-active-session` ("Settle this agent") closes a turn.
When the turn on screen closes without the user moving, the app moves to the
next owed turn, or home with "Take me to the next agent that needs you"
checked (`app/src/navigation/sessionNavigation.ts`). In queue flow, ⌘⇧E on an
agent that owes no turn and is not snoozed moves on without settling anything.

An automation run's turn can be settled in either flow without moving the user
([automations.md](automations.md)). Its row has ✓
`[data-testid="session-settle-<session>"]`, titled "Settle this run (⌘⇧E)".
In desktop flow the row is on the run's launch desktop; in queue flow it is in
its automation's group, opened with
`[data-testid="sidebar-automation-header-<automation-id>"]`
(`app/src/components/SidebarRows.tsx`, `app/src/components/SidebarDesktops.tsx`).
In desktop flow the key and palette command settle only runs. The palette
command appears only when the on-screen agent owes a turn the key can settle.
An agent can run `attn turn settle` (`cmd/attn/turn.go`) only for a turn it
opened itself; with queue flow off it succeeds and changes nothing.

### Snooze and unsnooze

⌘⇧S (`session.snooze`), a row's ☾ `[aria-label="Snooze <name>"]`, or the
palette's `snooze-active-session` ("Snooze this agent…") opens
`[data-testid="snooze-menu"]`. Its choices are `snooze-choice-30m`, `1h`,
`2h`, `4h`, `8h`, `tomorrow`, `saturday` and `monday`
(`app/src/components/SnoozeMenu.tsx`). Snoozing moves the user on. A snoozed
row's ↩ `[aria-label="Wake <name>"]` or the palette's `wake-active-session`
("Wake this agent now") ends it early. At the deadline, the turn opens again
if the agent is stopped; an exit or unknown state breaks through the snooze
(`internal/sessionmonitor/turns.go`). An unsnoozed turn goes to the back.

People can use these verbs from their own shell; agents can use them as a
session (`cmd/attn/session_snooze.go`):

- `attn session snooze <id>... --until <when>` accepts an RFC3339 instant, a
  positive duration such as `30m` or `1h`, or `tomorrow`, `saturday` or
  `monday` at 09:00 local.
- `attn session unsnooze <id>...` ends a snooze early.

A session may snooze itself and agents below it in its delegation chain; the
chief may snooze any agent in its profile. A plain shell can act on any live
session.

### Set priority

⌘⇧I (`session.priority`), the row's actions menu
`[data-testid="session-actions-<session>"]` and its
`[data-testid="session-priority-action"]` ("Mark priority" or "Unmark
priority"), or the palette's `toggle-session-priority` ("Mark as priority" or
"Clear priority") toggle priority (`app/src/components/SessionActionsPopover.tsx`).
People and agents can run `attn session priority on|off [--session <id>]`
(`cmd/attn/session_priority.go`); any agent may mark any session.

### Auto-settle and keep a turn

Auto-settle arms after the arm delay once the user's prompt sets the agent
working, counts down, then settles the turn. Typing or moving the pointer
holds it until things are quiet (`internal/sessionmonitor/autosettle.go`).
Settings, Attention queue (`[data-testid="settings-nav-hygiene"]`) has the
switch `[data-testid="settings-auto-settle-toggle"]`, arm delay and countdown
(`app/src/components/SettingsModalSections.tsx`). The palette's
`toggle-auto-settle` says "Turn on auto-settle". `attn settings` also exposes
`auto_settle_enabled`, `auto_settle_arm_seconds` and
`auto_settle_countdown_seconds` ([settings.md](settings.md),
`internal/daemon/settings_registry.go`).

During a countdown the tile header shows "Settling…"
`[data-testid="settling-indicator"]`, labelled "Keep this turn". ⌘.
(`session.cancelCountdown`) stops it or keeps the next turn of the agent on
screen; pressing it again lets a later countdown start. The header then shows
"Turn kept" `[data-testid="settle-kept-chip"]`
(`app/src/components/SettlingIndicator.tsx`). ⌘. reaches attn through the
File menu's "Stop Countdown" item because macOS takes it before the web view
(`app/src-tauri/core/src/lib.rs`).

### Related settings

The palette's `toggle-crew-queue` ("Put the crew in the queue") is driven in
[crew.md](crew.md). Settings, Experimental also has "Show hidden sessions in
the queue", driven in [shared-codex.md](shared-codex.md). The corresponding
`attn settings` keys are `queue_crew_enabled` and `queue_show_hidden_sessions`
(`internal/daemon/settings_registry.go`).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`). The baseline starts in desktop flow with auto-settle off.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-queue.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- Two agent folders, `A=$(mktemp -d /tmp/drive-attn-queue-a.XXXX) && echo "export A=$A" >> $DRIVE_ENV` and `B=$(mktemp -d /tmp/drive-attn-queue-b.XXXX) && echo "export B=$B" >> $DRIVE_ENV`. `$A/.attn-mock-agent.json` holds this fixture:

```json
{"version": 1, "turns": [
  {"includes": "question", "actions": [{"type": "reply", "text": "Which option do you want?"}]},
  {"includes": "longrun", "actions": [{"type": "delay", "ms": 40000}, {"type": "reply", "text": "long run done"}]},
  {"includes": "settleturn", "actions": [{"type": "attn", "args": ["turn", "settle"]}, {"type": "delay", "ms": 15000}]},
  {"includes": "selfsnooze", "actions": [{"type": "attn", "args": ["session", "snooze", "queue-a", "--until", "1h"]}]},
  {"includes": "selfwake", "actions": [{"type": "attn", "args": ["session", "unsnooze", "queue-a"]}]},
  {"includes": "markpeer", "actions": [{"type": "attn", "args": ["session", "priority", "on", "--session", "queue-b"]}]},
  {"includes": "unread items", "actions": [
    {"type": "attn", "args": ["agent", "inbox"]},
    {"type": "attn", "args": ["turn", "open"]}]}
]}
```

- `$B/.attn-mock-agent.json` holds this fixture:

```json
{"version": 1, "turns": [
  {"includes": "question", "actions": [{"type": "reply", "text": "Which option do you want?"}]},
  {"includes": "longrun", "actions": [{"type": "delay", "ms": 40000}, {"type": "reply", "text": "long run done"}]},
  {"includes": "settleturn", "actions": [{"type": "attn", "args": ["turn", "settle"]}]},
  {"includes": "snoozepeer", "actions": [{"type": "attn", "args": ["session", "snooze", "queue-a", "--until", "1h"]}]},
  {"includes": "unread items", "actions": [{"type": "attn", "args": ["agent", "inbox"]}]}
]}
```

- The agents, started in order: `wsctl add-session --cwd $A --agent claude --label queue-a --id queue-a`. Re-read `./attn agent list` until `queue-a` shows `owed`. Then the same with `$B`, `queue-b` and `queue-b`. The wait makes `queue-a` the older turn. `<terminal-a>` and `<terminal-b>` are their terminal ids from `wsctl list`.
- An automation run that owes a turn. A run folder, `R=$(mktemp -d /tmp/drive-attn-queue-run.XXXX) && echo "export R=$R" >> $DRIVE_ENV`, with `$R/.attn-mock-agent.json` holding `{"version": 1, "turns": [{"includes": "question", "actions": [{"type": "reply", "text": "Which option do you want?"}]}]}`, and a manual automation that starts its runs there, written with the paths filled in and applied:

```bash
cat > $P/run.yaml <<EOF
api_version: attn.dev/automations/v1alpha1
name: Queue run
trigger:
  type: manual
prompt: |
  Ask the user a question.
launch:
  driver: codex
  model: mock-agent-1
  executable: $ATTN_CODEX_EXECUTABLE
location:
  type: directory
  path: $R
EOF
./attn automation apply --file $P/run.yaml --launch-desktop own --desktop-name "Queue runs"
```

  `<automation-id>` is the `id` it prints. Run `./attn automation run <automation-id>`, then re-read `./attn automation runs <automation-id>` until the run is `delivered` with a `session_id`, `<run>`, and `./attn agent peek <run>` until it shows `turn: owed to this session`. `<terminal-run>` is its terminal id from `wsctl list`.

`<name>` in a selector is the agent's label, `queue-a` or `queue-b`. A row's buttons take clicks only while the pointer is over the row, so move there first, as the README says.

- **Turns open in any flow.** Each agent stops after booting. `./attn agent list` shows `owed` in the TURN column for both, and `bridge queue_get_state` shows `present: false`: desktop flow tracks the turns but shows no queue.
- **In desktop flow, ⌘⇧E settles only a run.** Select `queue-a` for setup with `bridge select_session '{"sessionId":"queue-a"}'` and run `bridge native_key '{"key":"e","modifiers":["command","shift"]}'`: `./attn agent list` still shows `queue-a` owed, and `bridge dom_wait '{"selector":"[data-testid=\"session-settle-queue-a\"]","absent":true,"timeoutMs":5000}'` matches. Show the run with ⌘⇧J ([automations.md](automations.md)), `bridge native_key '{"key":"j","modifiers":["command","shift"]}'`: `bridge get_state` shows `activeLeaf.sessionId` set to `<run>`. Press ⌘⇧E again: `./attn agent peek <run>` no longer shows `turn: owed to this session`, and `bridge get_state` still shows `<run>`; settling a run never moves the user.
- **Settle a run from the palette in desktop flow.** Run `wsctl input --id <terminal-run> --text "question" --enter` and re-read `./attn agent peek <run>` until it shows `turn: owed to this session`. With `<run>` still on screen, open the palette as the README says, type `Settle this agent` and press Enter: peek no longer shows the turn line.
- **Settle a run from its sidebar row in desktop flow.** Make `<run>` owe a turn again as in the last step. Move the pointer over `[data-testid="sidebar-session-<run>"]` with a native `move`, then click `[data-testid="session-settle-<run>"]` natively, converting their `bridge dom_bounds` boxes as the README says: peek no longer shows the turn line, and `bridge dom_wait '{"selector":"[data-testid=\"session-settle-<run>\"]","absent":true,"timeoutMs":5000}'` matches.
- **Turn queue flow on from the palette.** Run `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`, `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'`, `bridge native_text '{"text":"Turn on the agent queue"}'` and `bridge native_key '{"key":"Enter"}'`. `./attn settings get queue_mode_enabled` prints `true`, and `bridge queue_get_state` shows `present: true` with `turns` naming `queue-a`, then `queue-b`.
- **Switch with the sidebar's button, and from the shell.** Click `[aria-label="Switch to desktop flow"]` natively, converting its `bridge dom_bounds` box as the README says. `./attn settings get queue_mode_enabled` prints `false`. Click `[aria-label="Switch to queue flow"]` the same way: it prints `true`. Run `./attn settings set queue_mode_enabled false`: `bridge dom_wait '{"selector":"[data-testid=\"sidebar-queue\"]","absent":true,"timeoutMs":5000}'` matches. Run `./attn settings set queue_mode_enabled true`: `[data-testid="sidebar-queue"]` comes back.
- **Jump to the head of the queue.** Run `bridge native_key '{"key":"j","modifiers":["command"]}'`. `bridge get_state` shows `view: "session"` and `activeLeaf.sessionId: "queue-a"`. Go home with ⌘⇧H ([home.md](home.md)) and click `[data-testid="queue-waiting-head"]` natively: `queue-a` again. Go home again, open the palette as above, type `Jump to the oldest turn` and press Enter: `queue-a` again.
- **Walk the queue.** Go home with ⌘⇧H, then run `bridge native_key '{"key":"ArrowDown","modifiers":["command"]}'`: `bridge get_state` shows `activeLeaf.sessionId: "queue-a"`, the first row. Press ⌘↓ again: `queue-b`. Press ⌘↓ once more: it wraps to `queue-a`. Run `bridge native_key '{"key":"ArrowUp","modifiers":["command"]}'`: `queue-b`, then ⌘↑ again: `queue-a`.
- **Settle with the key.** With `queue-a` on screen, run `bridge native_key '{"key":"e","modifiers":["command","shift"]}'`. `./attn agent list` shows `-` for `queue-a`; the app moves on, so `bridge get_state` shows `activeLeaf.sessionId: "queue-b"`, and `bridge queue_get_state` lists only `queue-b` in `turns`.
- **⌘⇧E without a turn moves the user on.** Select `queue-a`, which owes nothing now, with `bridge select_session '{"sessionId":"queue-a"}'`, and press ⌘⇧E. `./attn agent list` still shows `-` for `queue-a` and `owed` for `queue-b`, and `bridge get_state` shows `activeLeaf.sessionId: "queue-b"`, the head of the queue.
- **Settle a run from the queue sidebar.** Run `wsctl input --id <terminal-run> --text "question" --enter` and re-read `./attn agent peek <run>` until it shows `turn: owed to this session`. `bridge queue_get_state` still lists only `queue-b` in `turns`: runs never queue. Show the run with ⌘⇧J, which opens its group: `bridge dom_wait '{"selector":"[data-testid=\"sidebar-automation-header-<automation-id>\"][aria-expanded=\"true\"]","timeoutMs":5000}'` matches. Move the pointer over `[data-testid="sidebar-session-<run>"]`, then click `[data-testid="session-settle-<run>"]` natively: peek no longer shows the turn line, and `bridge get_state` still shows `activeLeaf.sessionId` set to `<run>`.
- **A new turn goes to the back.** Run `wsctl input --id <terminal-a> --text "question" --enter`. Re-read `./attn agent peek queue-a` until it shows `Which option do you want?`. `bridge queue_get_state` lists `queue-b`, then `queue-a`.
- **Priority from the shell and the key.** Run `./attn session priority on --session queue-a`: it prints `queue-a priority on`, and `turns` now starts with `queue-a`. Select `queue-a` for setup with `bridge select_session '{"sessionId":"queue-a"}'` and run `bridge native_key '{"key":"i","modifiers":["command","shift"]}'`: in `wsctl list`, `queue-a` no longer has `"priority": true`, and `turns` lists `queue-b` first again.
- **Priority from the row menu and the palette.** Click `[data-testid="session-actions-queue-a"]` natively, then `[data-testid="session-priority-action"]`: `turns` starts with `queue-a` and `wsctl list` shows `"priority": true`. Select `queue-a` for setup, open the palette, type `Clear priority` and press Enter: `turns` lists `queue-b` first again.
- **Snooze with the key.** Select `queue-a` for setup. Run `bridge native_key '{"key":"s","modifiers":["command","shift"]}'` and `bridge dom_wait '{"selector":"[data-testid=\"snooze-choice-30m\"]","focused":true,"timeoutMs":5000}'`, then `bridge native_key '{"key":"Enter"}'`. `./attn session list --tree` shows `queue-a` with `snoozed until <instant>` half an hour ahead, and `bridge get_state` shows `activeLeaf.sessionId: "queue-b"`.
- **Unsnooze from the palette.** Select `queue-a` with `bridge select_session '{"sessionId":"queue-a"}'`, open the palette with ⌘⇧K as above, type `Wake this agent now` and press Enter. `./attn session list --tree` shows `owed` for `queue-a`.
- **Snooze from the palette.** Select `queue-b` for setup, open the palette, type `Snooze this agent` and press Enter. `[data-testid="snooze-choice-30m"]` takes focus. Press Escape: `./attn session list --tree` shows no snooze for `queue-b`.
- **Snooze from the row, unsnooze from the agent list.** Click `[aria-label="Snooze queue-b"]` natively: the snooze menu opens on `For 30 minutes`. Run `bridge native_key '{"key":"ArrowDown"}'` and `bridge native_key '{"key":"Enter"}'`: the tree shows `queue-b` snoozed an hour ahead. Run `bridge native_key '{"key":"a","modifiers":["command","shift"]}'` to open the agent list, then click `[data-testid="queue-wake-queue-b"]` natively: the tree shows `owed` again. Press ⌘⇧A to close the agent list.
- **Snooze and unsnooze from the shell.** `./attn session snooze queue-b --until 1h` prints `queue-b snoozed until <instant>` and exits 0; `./attn session unsnooze queue-b` prints `queue-b unsnoozed`.
- **Settle from the row and the palette.** Click `[aria-label="Settle queue-a"]` natively: `./attn agent list` shows `-` for `queue-a`. Select `queue-b`, open the palette, type `Settle this agent` and press Enter. Nothing is owed now, so the app lands home: `bridge home_get_state` shows `allSettled: true` and `followNextTurn: true`. Select `queue-b` for setup and press ⌘⇧E: nothing is settled, and with no turn left the app goes home again, so `bridge get_state` shows `view: "dashboard"` and `bridge home_get_state` shows `followNextTurn: true`.
- **The next turn comes to the user.** Run `wsctl input --id <terminal-b> --text "question" --enter`. When `queue-b` stops, home's follow takes the user to it: `bridge get_state` shows `activeLeaf.sessionId: "queue-b"`.
- **An agent cannot settle a turn the user owes it.** Run `wsctl input --id <terminal-b> --text "settleturn" --enter`. Re-read `./attn agent peek queue-b` until it shows `mock agent error: attn turn settle failed`; the daemon's refusal follows and ends with `only the user settles it`, which can wrap onto the next line. `./attn agent list` still shows `queue-b` owed.
- **Auto-settle from Settings, and the countdown.** Run `./attn settings set auto_settle_arm_seconds 5` and `./attn settings set auto_settle_countdown_seconds 60`; each prints `<key> = <value>`. Open Settings with `bridge native_key '{"key":",","modifiers":["command"]}'`, then click `[data-testid="settings-nav-hygiene"]` and `[data-testid="settings-auto-settle-toggle"]` natively: `./attn settings get auto_settle_enabled` prints `true`. Close Settings with Escape. With `queue-b` on screen and owed, run `wsctl input --id <terminal-b> --text "longrun"`, then `wsctl input --id <terminal-b> --enter`. Re-read `wsctl list` until `queue-b` carries `auto_settle_fires_at` (the countdown starts after the 5 second arm delay), and `bridge dom_wait '{"selector":"[data-testid=\"settling-indicator\"]","timeoutMs":20000}'` matches.
- **Stop the countdown.** Run `bridge native_key '{"key":".","modifiers":["command"]}'`. `auto_settle_fires_at` leaves `queue-b` in `wsctl list`, `[data-testid="settle-kept-chip"]` shows "Turn kept", and `./attn agent list` still shows `queue-b` owed. Press ⌘. again: `[data-testid="settling-indicator"]` comes back after the 5 second arm delay.
- **Let it settle.** Re-read `./attn agent peek queue-b` until it shows `long run done`, with a 60 second deadline. Run `./attn settings set auto_settle_countdown_seconds 3` and send `longrun` to `queue-b` again as two inputs. Re-read `./attn agent list` until `queue-b` shows `-` with `working` in its STATE column: the turn closed while the agent works, after the 5 second arm delay and the 3 second countdown. The app moves off `queue-b`, to the next owed turn or home. The turn opens again when the run ends, as any stop does.
- **Turn auto-settle off from the palette.** Open the palette, type `Turn off auto-settle` and press Enter: `./attn settings get auto_settle_enabled` prints `false`.
- **Agents use the session verbs.** Re-read `./attn agent list` until `queue-b` shows `owed` again, which means its long run ended. Then click `[aria-label="Settle queue-b"]` natively. Run `wsctl input --id <terminal-b> --text "snoozepeer" --enter`: `queue-b` tries to snooze `queue-a`, which is outside its delegation chain. Re-read `./attn agent peek queue-b` until it shows `mock agent error: attn session snooze queue-a`, and `./attn session list --tree` shows `queue-a` without a snooze. Run `wsctl input --id <terminal-a> --text "selfsnooze" --enter`: the tree shows `queue-a` with `snoozed until <instant>` an hour ahead. Run `wsctl input --id <terminal-a> --text "selfwake" --enter`: peek shows `queue-a unsnoozed`, and when the agent stops the tree shows `owed` for `queue-a`. Run `wsctl input --id <terminal-a> --text "markpeer" --enter`: peek shows `queue-b priority on` and `wsctl list` shows `"priority": true` on `queue-b`. Clear it with `./attn session priority off --session queue-b`.
- **Quiet notifications, and an agent that opens its own turn.** Click `[aria-label="Settle queue-a"]` and `[aria-label="Settle queue-b"]` natively for any agent still owed. Open Settings, click `[data-testid="settings-nav-experimental"]` and `[data-testid="settings-queue-quiet-notifications-toggle"]` natively: `./attn settings get queue_quiet_notifications` prints `true`. Close Settings with Escape. Send each agent a message from the other: `./attn agent msg queue-a "status?" --source-session queue-b` and `./attn agent msg queue-b "status?" --source-session queue-a`. Each agent gets its ring 30 seconds after the last input into its terminal and reads its inbox. Re-read `./attn agent peek queue-a` until it shows the quiet note starting `attn woke you to read these`, then `Your turn opens when you stop`, each wrapped across lines, with a 90 second deadline. `./attn agent list` shows `queue-a` owed. `queue-b` reads the same note but asks for nothing, so it stays at `-`. `./attn turn --help` prints `usage: attn turn open` and exits 0.
- **An agent settles a turn it opened.** `queue-a` opened its own turn in the last step. Run `wsctl input --id <terminal-a> --text "settleturn" --enter`. Re-read `./attn agent peek queue-a` until it shows `Your turn is settled`. While the 15 second hold in its fixture runs, `./attn agent list` shows `-` with `working` for `queue-a`. When the run ends, its stop opens a new turn, because the user started the run, and the list shows `owed` again.
- **Proof.** In `$P`, keep each `./attn` command's output and exit code; `./attn agent list` and `./attn session list --tree` after each settle, snooze and unsnooze; `./attn agent peek <run>` after each settle of the run; `bridge queue_get_state` and `bridge get_state` after each step in the app; `wsctl list` while the countdown runs and after ⌘.; `./attn agent peek` for both agents after the refused settle, after the session verbs and after the rings; and `bridge capture_screenshot '{"path":"<P>/queue-sidebar.png"}'` with two turns waiting and `<P>/countdown.png` while "Settling…" shows, with `<P>` written out. Put the way in in each file's name, such as `queue-settle-key-agents.txt`.

## Contexts

- **Durability across restarts**: turns, settles, snoozes and priority are the daemon's records. A settle and a snooze survive a daemon restart, a snooze still ends at its deadline afterwards, and an agent's request for its turn outlives a restart. An agent on the embedded backend stops when the daemon restarts.
- **Linux**: `session.jumpToWaiting` is Ctrl+Shift+J, `session.prev` and `session.next` Ctrl+Shift+PageUp and Ctrl+Shift+PageDown, `session.settle` Ctrl+Alt+E, `session.snooze` Ctrl+Alt+S, `session.priority` Ctrl+Alt+I and `session.cancelCountdown` Ctrl+Shift+. there (`LINUX_SHORTCUTS`); there is no native menu in the way of the cancel key. `attn turn` and `attn session` work the same.
- **Outposts**: an agent whose outpost is offline is left out of the waiting count, has no settle or snooze in the app, and is skipped when the queue moves the user on. Its countdown shows "Settling paused while the host is unreachable".
- **Profiles**: each profile's queue lists only its own agents, and only that profile's chief may snooze any of them. Queue flow itself is one setting for every profile today.
- **Performance and idle cost**: turn ages re-read the clock every 30 seconds, and the countdown bar animates only while a countdown runs. Nothing measures the queue's idle cost; when a change touches its rendering, check it for repaints with `docs/perf-testing.md`.
- **Harnesses**: a turn opens on the state attn reads from each harness ([agent-state.md](agent-state.md)). `attn turn` and `attn session` work the same from Claude Code, Codex and Pi. Shells never queue.
- **Keyboard and native input**: ⌘J, ⌘↑, ⌘↓, ⌘⇧E, ⌘⇧S, ⌘⇧I and ⌘. reach every action; in a text field outside a terminal, ⌘↑ and ⌘↓ stay with the field; the snooze menu takes arrows, Enter or Space, and Escape returns focus to where it was. While the snooze menu is open, app shortcuts wait. ⌘. arrives through the File menu, not the web view.
- **Compatibility across versions**: a snooze written in the old encoding still ends, and store migrations carry turn stamps and snooze deadlines forward. The flow is stored under its old key, `queue_mode_enabled`.
- **Flow**: with queue flow off, turns are still tracked and `./attn agent list` still shows `owed`, but the sidebar shows desktops, ⌘↑ and ⌘↓ step through desktops instead ([desktops.md](desktops.md)), ⌘⇧S and the palette's snooze commands are gone, ⌘⇧E, "Settle this agent" and the sidebar's ✓ settle only automation runs, ⌘⇧E on any other agent does nothing, and ⌘J opens the agent that has waited longest among those waiting, needing approval or unknown; an idle agent is never its target. An agent's `attn turn open` then prints `The user does not work in the queue flow, so there is no queue to join; nothing changed.`

## Tests

- Wire: `internal/daemon/turn_wire_test.go`, `internal/daemon/turn_owed_wire_test.go`, `internal/daemon/turn_heartbeat_wire_test.go`, `internal/daemon/owed_prompt_wire_test.go`, `internal/daemon/user_prompt_credit_wire_test.go`, `internal/daemon/auto_settle_wire_test.go`, `internal/daemon/monitor_auto_settle_wire_test.go`, `internal/daemon/priority_wire_test.go`, `internal/daemon/snooze_wire_test.go`, `internal/daemon/snooze_cli_wire_test.go`, `internal/daemon/snooze_deadline_wire_test.go`, `internal/daemon/monitor_snooze_replacement_wire_test.go`, `internal/daemon/quiet_notifications_wire_test.go`.
- Stack: `cmd/attn/turn_stack_test.go`, `cmd/attn/session_snooze_stack_test.go`, `cmd/attn/snooze_stack_test.go`, `cmd/attn/session_priority_stack_test.go`, `app/e2e/queue-bar.spec.ts`, `app/e2e/queue-sidebar-fit.spec.ts`, `app/e2e/queue-sidebar-rows.spec.ts`.
- Scenario: `agent-queue`, `countdown-cancel`, `snooze-keyboard`.
- Kernel: `internal/attention/turn_test.go`, `internal/store/turn_test.go`, `internal/store/turn_timestamps_test.go`, `internal/store/snooze_jobs_migration_test.go`.
- App: `app/src/App.queue.test.tsx`, `app/src/App.queueFocus.test.tsx`, `app/src/App.queueRows.test.tsx`, `app/src/App.autoSettle.test.tsx`, `app/src/App.cancelCountdownTarget.test.tsx`, `app/src/App.priority.test.tsx`, `app/src/App.sidebar.test.tsx`, `app/src/App.activeLeaf.test.tsx`, `app/src/components/QueueSidebar.sections.test.tsx`, `app/src/components/QueueBar.sections.test.tsx`, `app/src/utils/queueBands.test.ts`, `app/src/utils/snoozeDurations.test.ts`.

## Gotchas

- A mock agent owes a turn as soon as it finishes booting, in either flow. Expect `owed` before any prompt.
- Typing into an agent does not settle its turn; only a settle, a snooze or auto-settle does. A turn the agent did not open is the user's: `attn turn settle` from the agent exits 1 with `only the user settles it`. A turn the agent opened with `attn turn open` can be settled by the agent.
- Every prompt ends in a stop, and a stop opens a turn. After each `wsctl input` the agent owes a turn again, unless it is snoozed or a notification run started it with quiet notifications on. Settle between steps that expect `-`.
- `attn turn` acts as the calling session. From a plain shell it prints the queue-off line while queue flow is off and is refused once it is on; drive it through the mock agent.
- From a plain shell, `attn session snooze` and `unsnooze` may act on any live session; from an agent they follow the delegation-chain rule. A shell's success is not proof of what an agent may do.
- Auto-settle arms only behind the user's input. `wsctl input` counts; a ring or `attn agent msg` does not. The scenarios type the prompt and Enter as two inputs, and so does this recipe.
- The arm delay is at least 5 seconds and the countdown at least 3; `attn settings set` refuses less. The agent must keep working past both: a stop ends the countdown, and the stop opens the turn again.
- Typing or moving the pointer pauses the countdown ("Settling paused") until five quiet seconds pass. `wsctl input` into the agent's terminal counts as typing. A run that keeps touching the window never settles.
- Pressing ⌘. again after a cancel does not resume the old countdown. It lets the next one start, so "Settling…" returns only after the arm delay.
- ⌘. comes through the File menu. The countdown-cancel scenario activates the app before pressing it; if `bridge native_key` does nothing, bring the app window forward and press it again. With no countdown on screen, ⌘. keeps the next turn of the agent on screen, so a stray press shows "Turn kept" later.
- A row's ✓, ☾, ↩ and ••• buttons take clicks only while the pointer is on the row. After a native click on one, keyboard focus stays on that row, and ⌘⇧E, ⌘⇧S and ⌘⇧I then act on that row, not on the agent on screen. Select the agent with `bridge select_session` before pressing the key.
- The mock agent prints errors and replies at the terminal's width, so a long line wraps anywhere. Match the first words of a line, not its ending.
- ⌘⇧S and the palette's snooze and unsnooze commands exist only in queue flow and only with an agent on screen. "Settle this agent" shows in either flow, but only while the agent on screen owes a turn the user can settle: in queue flow an agent of the queue or a run, in desktop flow only a run. A missing palette command usually means the precondition is missing, not that the command is broken.
- In queue flow, ⌘⇧E on an agent of the queue that owes no turn settles nothing and takes the user to the head of the queue, or home. A press that jumps instead of settling means the turn had already closed.
- A run's ✓ in the queue sidebar shows only inside its automation's open group. Showing the run opens the group, and a click on the group's header closes it again.
- Rings wait 30 seconds after the last `wsctl input` into that terminal. Quiet notifications change nothing while queue flow is off.
- Turn ages tick every 30 seconds; a fresh turn reads `now`.
- `queue_show_hidden_sessions` matters only for sessions without a tile, which come from shared Codex ([shared-codex.md](shared-codex.md)); the baseline has none.
- Working but wrong: the app says "Wake" for an unsnooze ("Wake this agent now", `[aria-label="Wake <name>"]`), the same word that starts a crew member's session. The palette calls queue flow "the agent queue", and the shortcut sheet labels ⌘↑ and ⌘↓ with the old name for queue flow. The row menu says "Unmark priority" where the palette says "Clear priority". The flow is one setting shared by every profile.

# Agent state

What each agent is doing right now, and why attn gives it that state. The
states are launching, working, waiting for the user, needs approval,
scheduled, idle, recoverable and unknown. attn reads the state from what the
harness reports and, when a turn ends, from the last message. People see the
state beside every agent in the sidebar, the home screen and the palette.
Agents read the state of every session with `attn agent list` and
`attn agent peek`, and anyone can ask why a session has its state with
`attn state explain`.

## Sub-features

- `state-shown`: the state beside each agent in the sidebar, the home screen's groups and the palette.
- `agent-list`: every session on the daemon with its harness, profile, state and whether a turn is owed.
- `agent-peek`: a session's state, last reply and screen, read without interrupting it.
- `state-explain`: the claims behind a state, the source of each claim, and whether attn applied the claim.
- `approval`: a permission prompt puts the agent in needs approval until someone answers it.
- `stop-verdict`: when a turn ends, attn decides waiting or idle from the turn's last message.
- `activity-line`: a one-line summary of what each agent is doing, written by a background agent.

## How to get to it

### People

- Sidebar, in desktop flow (`app/src/components/SidebarRows.tsx`): each agent's row, `[data-testid="sidebar-session-<session>"]`, carries the state in `.session-lead[data-state]`, and its harness logo is labelled "<Harness> · <state>", such as "Claude · working". With harness logos off, a state dot takes its place ([harnesses.md](harnesses.md)).
- Home screen, ⌘⇧H (`session.goToDashboard`, `app/src/shortcuts/registry.ts`): sessions grouped by state, "Waiting for input", "Pending approval", "Launching", "Working", "Scheduled", "Idle", "Recoverable" and "Unknown / error", each group with a `data-testid` such as `session-group-waiting` and `session-group-pending`, and each agent's activity line (`app/src/components/Dashboard.tsx`). The home screen itself is [home.md](home.md).
- The palette's agent rows show whether an agent waits or needs approval (`app/src/components/palette/agentPaletteRows.ts`).
- **Settings → Background agents** (`app/src/components/SessionActivitySettings.tsx`): the "Generate activity lines" switch (`[data-testid="settings-activity-toggle"]`, setting `activity.enabled`) stays disabled until the user chooses an agent in "Session activity model" (setting `activity.config`). Two refresh fields, "Refresh on home (seconds)" and "Refresh elsewhere in the app (seconds)", set how often lines are written.
- Their own shell: the verbs below.

### Agents

- `attn agent list [--json]` and `attn agent peek <session-or-member> [--json]` (`cmd/attn/agent.go`).
- `attn state explain <session> [--json]` (`cmd/attn/state_explain.go`).
- `attn activity [--json]` and `attn activity clear <session>` (`cmd/attn/activity.go`).
- Prompts: the attn skill's reference on other sessions, which names `agent list` and `agent peek` (`internal/prompts/content/skills/attn/references/converse-and-observe.md`). The classifier and activity prompts go to background models, under Triggers.

### Triggers

- The harness reports through hidden verbs its hooks call: `attn _hook-session-start`, `_hook-state`, `_hook-notification`, `_hook-stop`, `_hook-stop-failure`, `_hook-compact` and `_hook-tool-use` (`cmd/attn/main.go`, and Claude Code's hook settings in `internal/hooks/hooks.go`). Codex under shared Codex reports through its app-server.
- Each session's monitor watches the agent on the machine where the agent runs and keeps the agent's state (`internal/daemon/monitors.go`, `internal/daemon/session_state.go`).
- When a turn ends, a background model judges from the last message whether the agent waits for the user (`internal/daemon/turn.go`, prompt `internal/prompts/content/turn-classifier/classify.md`, run by `internal/daemon/headless_tasks.go`).
- With activity lines on, a background model writes each agent's line while someone can see it (`internal/daemon/activity.go`, prompts in `internal/prompts/content/activity/`).

## Driving it

Preconditions:

- Use the baseline instance with its app. Check that `bridge --launch --wait-ready ping` answers. Run `bridge dismiss_whats_new` to dismiss What's new, and stay in desktop flow, the default.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-state.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- Make an agent folder with `S=$(mktemp -d /tmp/drive-attn-state-agent.XXXX) && echo "export S=$S" >> $DRIVE_ENV`, and save this fixture as `$S/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "hello", "actions": [{"type": "reply", "text": "hello back"}]},
  {"includes": "quiet", "actions": []},
  {"includes": "who", "actions": [{"type": "attn", "args": ["agent", "list"]}]}
]}
```

`<terminal>` is the agent's terminal id from `wsctl list`. Re-read a state with a deadline that fails the run. Never sleep.

- **Launch.** Run `wsctl add-session --cwd $S --agent claude --label state-agent --id state-agent --place`. `./attn agent list` may show `state-agent` as `launching` for a moment. Re-read `./attn agent list` until the state is something else, with a deadline.
- **Working, then waiting.** Run `wsctl input --id <terminal> --text "hello" --enter`. For at least 1.5 seconds `./attn agent list` shows `working`. Then `./attn agent list` shows `waiting_input` with TURN `owed`. `./attn agent peek state-agent` shows `state: waiting_input` and `turn: owed to this session`. In the app, `bridge dom_wait '{"selector":"[data-testid=\"sidebar-session-state-agent\"] .session-lead[data-state=\"waiting_input\"]","timeoutMs":5000}'` matches.
- **A silent turn ends idle.** Run `wsctl input --id <terminal> --text "quiet" --enter`. The state goes `working`, then `idle`, and the sidebar row's `data-state` follows.
- **An agent reads the list.** Run `wsctl input --id <terminal> --text "who" --enter`. `./attn agent peek state-agent` shows a table with the columns ID, NAME, AGENT, MEMBER, PROFILE, STATE and TURN, with `state-agent` as `working` while it ran the command.
- **Needs approval.**
  - With the agent idle at its prompt, report a permission prompt the way Claude Code's Notification hook does: run `echo '{"notification_type":"permission_prompt","message":"Allow edit?"}' | ./attn _hook-notification <terminal>`. It exits 0 and prints nothing. `./attn agent list` shows `pending_approval`.
  - Press ⌘⇧H with `bridge native_key '{"key":"h","modifiers":["command","shift"]}'`: `bridge dom_text '{"selector":"[data-testid=\"session-group-pending\"]"}'` reads `Pending approval` and `state-agent`.
  - Open the agent palette with ⌘K (`bridge native_key '{"key":"k","modifiers":["command"]}'`): `bridge dom_wait '{"selector":"[data-testid=\"palette-agent-state-agent\"] .unified-palette-pill.is-approval","timeoutMs":5000}'` matches and the row reads `approval`.
  - Press Escape.
  - Answer the permission prompt by sending the agent a prompt, `wsctl input --id <terminal> --text "hello" --enter`: the state goes `working`, then `waiting_input`, and `session-group-waiting` holds the row. Open the agent palette again with ⌘K: the row's pill reads `waiting` (`.unified-palette-pill.is-waiting`).
- **Why.** Run `./attn state explain state-agent`. It prints `state:   waiting_input`, then a table with the columns OBSERVED, SOURCE, CLAIM, OUTCOME and WHY, holding a `hook_notify` row whose claim is `permission_prompt`, a `resolver` row applying `pending_approval`, and the rows that followed. `--json` prints the same facts.
- **Peek is passive.** Run `./attn agent peek state-agent` twice. The `since` time on the state line does not move, and the agent's screen shows no input.
- **Stopped.** Run `wsctl kill-session --id state-agent`. `./attn agent list` shows `idle`, peek shows `process exited with code`, `./attn state explain state-agent --json` holds an observation with source `resolver`, claim `idle` and detail `process_exited`, and the sidebar row's `data-state` follows. See [agent-launch.md](agent-launch.md) for resuming the agent.
- **Activity lines.**
  - Run `./attn activity`. It prints `presence:` and a tier, then `activity: off (Settings › Background agents › Session activity)`.
  - Open Settings with ⌘, (`bridge native_key '{"key":",","modifiers":["command"]}'`) and click `[data-testid="settings-nav-backgroundAgents"]` natively. `bridge dom_wait '{"selector":"[data-testid=\"settings-activity-toggle\"][disabled]","timeoutMs":5000}'` matches: the switch cannot be clicked until an agent is chosen.
  - Run `./attn settings set activity.enabled true`. `./attn activity` prints `activity: on, but not runnable: session activity has no agent selected: choose claude or codex`, and `bridge dom_text '{"selector":"[data-testid=\"settings-activity-toggle\"]"}'` reads `Disable`.
  - Run `./attn activity clear state-agent`: it prints `cleared the activity line for state-agent`.
  - Put the setting back with `./attn settings set activity.enabled false`.
- **Proof.** In `$P`, keep `./attn agent list --json` after each step, `./attn agent peek state-agent` after the reply, the approval and the stop, `./attn state explain state-agent --json`, `./attn activity` before and after the setting, and `bridge capture_screenshot '{"path":"<P>/state-home.png"}'` with the home screen showing the pending group, where `<P>` is the folder's path written out. Name each file for its way in, such as `state-approval-list.json`.

## Contexts

- **Durability across restarts**: after an app or daemon restart, each live agent's monitor reads the agent's state again on the machine where the agent runs. A machine restart leaves agents attn can bring back as `recoverable`. An agent on the embedded backend stops when the daemon restarts.
- **Outposts**: an agent on an outpost is watched by a monitor on that machine. While the agent's host is offline, the agent's row stays and attn marks it offline.
- **Profiles**: `attn agent list` names every session on the daemon, across profiles, with a PROFILE column. The sidebar and home screen show the current profile's agents.
- **Performance and idle cost**: the needs-approval dot flashes and the scheduled dot pulses, both opacity animations that run while an agent stays in that state. `internal/sessionmonitor/cost_bench_test.go` and `internal/transcript/parser_bench_test.go` measure reading transcripts. A change to the state indicator needs the repaint check in `docs/internal/perf-testing.md`.
- **Harnesses**: Claude Code reports through hooks and its terminal title. Codex reports through hooks and, under shared Codex, its app-server. A plugin harness reports through its plugin's driver. `docs/internal/harnesses.md` records each harness's facts.
- **Compatibility across versions**: Claude Code sessions launched before todo tracking was removed still call `attn _hook-todo`, which exits 0. Older launches carry their terminal identity as `ATTN_SESSION_ID` instead of `ATTN_TERMINAL_ID`.
- **Flow**: in desktop flow, each desktop's rows show the state. In queue flow, the state also sets where an agent stands in the queue ([queue-flow.md](queue-flow.md)).

## Tests

- Wire: `internal/daemon/session_state_edge_wire_test.go`, `internal/daemon/session_state_evidence_wire_test.go`, `internal/daemon/state_explain_wire_test.go`, `internal/daemon/stop_verdict_wire_test.go`, `internal/daemon/stop_yield_wire_test.go`, `internal/daemon/stop_outcomes_wire_test.go`, `internal/daemon/halt_replay_wire_test.go`, `internal/daemon/session_halt_wire_test.go`, `internal/daemon/session_approval_wire_test.go`, `internal/daemon/claude_stop_wire_test.go`, `internal/daemon/classifier_race_wire_test.go`, `internal/daemon/monitor_*_wire_test.go`, `internal/daemon/activity_*wire_test.go`, `internal/daemon/agent_peek_wire_test.go`, `internal/daemon/presence_wire_test.go`, `internal/daemon/program_status_wire_test.go`, `internal/daemon/transcript_binding_wire_test.go`, `internal/daemon/transcript_commentary_wire_test.go`, `internal/daemon/headless_launch_wire_test.go`, `internal/daemon/headless_tasks_wire_test.go`.
- Stack: `cmd/attn/state_explain_stack_test.go`, `cmd/attn/agent_stack_test.go`, `cmd/attn/transcript_stack_test.go`, `cmd/attn/shell_program_status_stack_test.go`, `internal/daemon/monitor_restart_stack_test.go`, `internal/daemon/monitor_conversation_stack_test.go`, `app/e2e/session-states.spec.ts`, `scripts/verify-monitor-routing.sh`.
- Kernel: `internal/sessionstate/*_test.go`, `internal/classifier/*_test.go`, `internal/sessionmonitor/*_test.go`, `internal/transcript/*_test.go`, `internal/activity/*_test.go`, `internal/statemarker/*_test.go`, `internal/hooks/*_test.go`, `internal/daemon/stop_terminality_test.go`.
- App: `app/src/App.arrivalPulse.test.tsx`, `app/src/App.sessionActivitySettings.test.tsx`, `app/src/components/Sidebar.harnessIcons.test.tsx`.

## Gotchas

- The CLI prints the wire names `waiting_input` and `pending_approval`. The home screen says "Waiting for input" and "Pending approval". The glossary says waiting and needs approval. Match the name each view prints.
- The baseline refuses background model tasks, so the turn classifier never runs. The mock decides instead: a turn with a reply ends `waiting_input`, a turn with no action ends `idle`. A real harness on the baseline can sit in a state the classifier would have changed.
- The needs-approval step stands in for Claude Code's permission dialog through the same hook it calls. Report the prompt while the agent is idle: the mock repeats its busy title every half second during a turn, and busy evidence newer than the prompt outranks the prompt. A real dialog needs a real harness.
- `wsctl input` counts as the user typing, so it opens and settles turns as typing does. The turn itself is [queue-flow.md](queue-flow.md).
- The activity switch is disabled until an agent is chosen, so a native click on the baseline does nothing. You can set `activity.enabled` from the shell without an agent, and then `attn activity` reports that no agent is selected. With an agent chosen the status reads `activity: on`, and no line is ever written while background tasks are off. `presence: away` means nobody is looking, and nothing is generated then either.
- `attn state explain` on a session whose state predates the daemon's start prints `No state observations recorded.`. Send the agent a prompt to get rows.
- A stopped agent reads `idle`, not `recoverable`, as [agent-launch.md](agent-launch.md) explains.
- Working but wrong: `attn agent list` lists sessions from every profile, while everything else shows one profile.

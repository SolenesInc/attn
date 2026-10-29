# Agent launch

Start an agent in a terminal tile, reload it, and close it. A person starts
one from the keyboard, the palette, the sidebar or their own shell, picks a
folder and a harness, and the agent lands beside the active tile on the
current desktop. Reloading restarts the agent in the same tile on the same
conversation. Closing ends the session for good, with a reason, and keeps its
row in the ledger. An agent that stops without quitting cleanly keeps its last
screen in its tile and offers Resume; a clean quit closes the session.

## Sub-features

- `launch-picker`: ⌘N opens the location picker; pick a folder and a harness, and the agent starts beside the active tile. ⌘⇧N splits sideways instead.
- `launch-inline`: an empty desktop shows the picker on the desktop itself, and ⌘N puts focus there.
- `launch-options`: the picker's harness choice and its YOLO and auto mode switches, the last only for a harness that supports auto mode; auto mode itself is in [auto-mode.md](auto-mode.md), worktrees offered for a repository in [worktrees.md](worktrees.md).
- `launch-deep-link`: `attn` with no verb, run in a folder, opens the app on a spawn link for that folder; a session already there is selected instead.
- `reload`: interrupt and restart an agent in place on the same conversation, model, effort and permission mode.
- `close`: close a session for good with a reason; the chief and crew members refuse.
- `stopped-tile`: an agent stopped other than by a clean quit keeps its last screen, with Resume and Close.
- `clean-quit`: a process that quits cleanly closes its session into the ledger.
- `launch-failure`: an agent that cannot start registers nothing.
- `session-title`: a background model names a new session from its first prompt.
- `launch-launcher`: the experimental "Launcher 2.0" replaces the picker with one dialog for the path, the repository and its worktree.
- `launch-mode`: the experimental "Direct agent launches" switch, described in Settings as launching agents using less memory.

## How to get to it

### People

- ⌘N (`session.new`, `app/src/shortcuts/registry.ts`) opens the picker, titled "New Session Location", from the home screen or a desktop with tiles. On an empty desktop it focuses the launcher already on the desktop (`app/src/application/EmptyDesktopLauncher.tsx`). ⌘⇧N (`session.newHorizontal`) opens the picker and splits sideways.
- The picker (`app/src/components/LocationPicker.tsx`, controls in `app/src/components/NewSessionDialog/SessionControls.tsx`): a path field with Tab completion, Recent and Directories lists, arrows, Enter to start, Escape to cancel, the "Session agent" choice with ⌥1 to ⌥9 and ⌥T for a terminal, YOLO with ⌥Q, and an auto mode switch for harnesses that support it (⌥A reaches it in Launcher 2.0 only).
- Palette (`app/src/application/useAppCommands.tsx`): `new-agent` ("New agent") opens the picker; `reload-active-session` ("Reload this agent") is offered while an agent is on screen.
- Sidebar: the "New agent" button in the header (`app/src/components/SidebarHeader.tsx`), "New Session" in the collapsed sidebar (`app/src/components/SidebarChrome.tsx`), and each row's actions button, `[data-testid="session-actions-<session>"]` (labelled "Actions for <name>"), whose menu holds "Reload session" and "Close session" (`app/src/components/SessionActionsPopover.tsx`).
- ⌘W (`session.close`) closes the agent on screen, or the focused tile when it is not a terminal, or an empty desktop. In the packaged app the File menu item "Close Pane" carries ⌘W and does the same (`app/src-tauri/core/src/lib.rs`).
- A stopped agent's tile says "Agent stopped. Resume to continue this conversation." with Resume and Close (`app/src/components/SessionTerminalDesktop/DesktopAgentBody.tsx`).
- Settings, Experimental: "Launcher 2.0" (`new_session_launcher_enabled`) swaps the picker for `app/src/components/NewSessionLauncher.tsx`; "Direct agent launches" (`direct_agent_launch_enabled`, `[data-testid="settings-direct-agent-launch-toggle"]`) changes how agents start (`app/src/components/SettingsModalSections.tsx`).
- Their own shell: `attn` with no verb, or `attn -s <label>`, in any folder opens the app on `<scheme>://spawn?cwd=<folder>&label=<label>` (`cmd/attn/main.go`, handled by `app/src/application/useAppDeepLinks.tsx`). The scheme is `attn-<instance>` for a named instance, `attn` for production.
- Where people see what agents do: an agent that reloads or closes another appears as a restart or a closed tile, and the closed row in the ledger names who closed it and why ([ledger.md](ledger.md)).

### Agents

- `attn agent reload <session>... | --all` and `attn agent close <session-or-seed> -m "<reason>"`, both with `--source-session` and `--json` (`cmd/attn/agent_reload.go`, `cmd/attn/agent.go`). A session may reload or close itself and the agents below it in its delegation chain; the chief may act on any agent of its profile.
- Starting another agent is delegation ([delegation.md](delegation.md)); opening a shell beside itself is `attn terminal open` ([terminals.md](terminals.md)).
- Prompts: the attn skill's reference on other sessions, which names `agent reload` and `agent close` (`internal/prompts/content/skills/attn/references/converse-and-observe.md`).

### Triggers

- A spawn places the tile and starts the harness; a harness that cannot start leaves no session behind (`internal/daemon/spawn_pipeline.go`, `internal/daemon/agent_launch.go`).
- A process that quits cleanly closes its session; any other exit, or an explicit stop, keeps the tile with its last screen (`internal/daemon/exit_screen.go`).
- After the first prompt a background model titles the session, unless a person named it (`internal/daemon/session_title.go`, prompts in `internal/prompts/content/session-title/`).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`), on the home screen.
- `R=$PWD && echo "export R=$R" >> $DRIVE_ENV` in the instance shell, at the repository root.
- A proof folder written with its physical path, `P=$(cd "$(mktemp -d /tmp/drive-attn-launch.XXXX)" && pwd -P) && echo "export P=$P" >> $DRIVE_ENV`, and two empty folders, `$P/picked` and `$P/link`. The picker and the spawn link record the folder as the machine resolves it, `/private/tmp/...` for a folder made under `/tmp`, so every path below starts from the resolved one.
- An agent folder, `A=$(cd "$(mktemp -d /tmp/drive-attn-launch-agent.XXXX)" && pwd -P) && echo "export A=$A" >> $DRIVE_ENV`, holding this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "resumable": true, "turns": [
  {"includes": "hello", "actions": [{"type": "reply", "text": "hello back"}]},
  {"includes": "reload", "actions": [
    {"type": "capture", "name": "target", "pattern": "reload (\\S+)"},
    {"type": "attn", "args": ["agent", "reload", "{{target}}"]}]},
  {"includes": "close", "actions": [
    {"type": "capture", "name": "target", "pattern": "close (\\S+)"},
    {"type": "attn", "args": ["agent", "close", "{{target}}", "-m", "done by the mock agent"]}]}
]}
```

`<picked>` is the id `./attn agent list --json` gives the `picked` row after the first launch, `<picked2>` the id of the row that appears after the split, and `<link>` the id of the `link` row. `<terminal>` is a session's terminal id: the `runtime_id` of the entry in `wsctl list`'s `desktops[].panes[]` whose `session_id` is that session; read it again after a reload. `<P>` is the proof folder's path written out.

- **New agent with ⌘N.** Run `bridge native_key '{"key":"n","modifiers":["command"]}'` and `bridge dom_wait '{"selector":"[data-testid=\"location-picker-overlay\"]","timeoutMs":5000}'`. `bridge location_picker_get_state` shows `open: true`, `title: "New Session Location"` and the harness in `selectedAgent`. Run `bridge native_text '{"text":"<P>/picked/"}'` and `bridge native_key '{"key":"Enter"}'`. `bridge dom_wait '{"selector":"[data-testid=\"location-picker-overlay\"]","absent":true,"timeoutMs":10000}'` matches, and `./attn agent list` shows a row named `picked` with that harness. `./attn agent list --json` gives its `directory` as `<P>/picked`.
- **Picker switches.** Press ⌘N again. Run `bridge native_key '{"key":"q","modifiers":["option"]}'`: `bridge dom_wait '{"selector":"[data-testid=\"location-picker-yolo-toggle\"][aria-checked=\"true\"]","timeoutMs":5000}'` matches. Press ⌥Q again and wait for `[data-testid=\"location-picker-yolo-toggle\"][aria-checked=\"false\"]`, which leaves the saved choice off. With Claude checked there is no `[data-testid="location-picker-automode-toggle"]`. Press Escape. The harness keys ⌥1 to ⌥9 are in [harnesses.md](harnesses.md).
- **Split sideways with ⌘⇧N.** With `picked` on screen, run `bridge native_key '{"key":"n","modifiers":["command","shift"]}'`, wait for the overlay, type `<P>/picked/` again and press Enter. `wsctl list` shows two sessions in `<P>/picked` on the current desktop, and the desktop's `tree_json` names a split with direction `horizontal`. The new session is `<picked2>`.
- **From the palette and the sidebar.** Run `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`, `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'`, `bridge native_text '{"text":"New agent"}'` and `bridge native_key '{"key":"Enter"}'`: the overlay opens. Press Escape. Read `bridge dom_bounds '{"selector":"[aria-label=\"New agent\"]"}'`, convert it as the README says and click it with `bridge native_mouse`: the overlay opens again. Press Escape. Collapse the sidebar with `bridge native_key '{"key":"b","modifiers":["command"]}'` (`session.toggleSidebar`), click `[title^="New Session"]` the same way: the overlay opens a third time. Press Escape and ⌘B again.
- **From a person's shell.** Run `(cd $P/link && $R/attn -s link)`. It exits 0, and `./attn agent list` shows a new row named `link`, in `<P>/link`, with the harness `claude`. Select another session with `bridge select_session '{"sessionId":"<picked>"}'`, then run `(cd $P/link && $R/attn -s link-again)`. It exits 0, no row named `link-again` appears, and `bridge get_state` shows `<link>` in `activeLeaf.sessionId`: a folder that already has a session selects it. The app ignores a link it already handled, so the second label has to differ.
- **Start the fixture agent.** Run `wsctl add-session --cwd $A --agent claude --label launch-agent --id launch-agent --place`, find its terminal in `wsctl list`, and run `wsctl input --id <terminal> --text "hello" --enter`. `./attn agent peek launch-agent` shows `• hello back`.
- **Prepare reload proof.** Before each reload below, change the fixture banner: run `jq --arg banner "Claude Code mock agent (<label>)" '.banner = $banner' "$A/.attn-mock-agent.json" > "$A/next.json" && mv "$A/next.json" "$A/.attn-mock-agent.json"`, replacing `<label>` with `palette reload`, `row reload` or `self reload` for that action. The running process keeps its old banner until it restarts.
- **Reload from the palette and the row menu.** Select it with `bridge select_session '{"sessionId":"launch-agent"}'`, open the palette, type `Reload this agent` and press Enter. `./attn agent peek launch-agent` shows the mock's splash and `• hello back` again, replayed from the conversation, its banner reads `Claude Code mock agent (palette reload)`, and `./attn agent list` still has one row for it. Click `[data-testid="sidebar-session-launch-agent"]` natively to focus its row. Move over `[data-testid="session-actions-launch-agent"]` with `bridge native_mouse` (`action: "move"`), click it natively, wait for `.session-actions-popover`, then `.session-actions-popover [role="menuitem"]:nth-last-child(2)` ("Reload session"): the replay appears with the `row reload` banner.
- **An agent reloads itself.** Run `wsctl input --id <terminal> --text "reload launch-agent" --enter`. `./attn agent peek launch-agent` shows the splash and `• hello back` again and the `self reload` banner. Read the result from peek and `agent list`, not from the agent's screen.
- **An agent closes itself.** Run `wsctl input --id <terminal> --text "close launch-agent" --enter`. `./attn agent list` no longer lists `launch-agent`, and `./attn session show launch-agent` shows `state      closed` and `because    done by the mock agent`.
- **Close with ⌘W.** Select `<picked>` for setup, then run `bridge native_key '{"key":"w","modifiers":["command"]}'`. `./attn agent list` drops its row, and `./attn session list --closed` lists `<picked>` as `closed`.
- **Close from the sidebar.** Click `[data-testid="sidebar-session-<picked2>"]` natively to focus its row. Move over `[data-testid="session-actions-<picked2>"]` with `bridge native_mouse` (`action: "move"`), click it natively, wait for `.session-actions-popover`, then `.session-actions-popover [role="menuitem"]:last-child` ("Close session"). The row leaves the sidebar and `./attn agent list`.
- **Stopped tile and Resume.** Select `<link>` for setup and run `wsctl kill-session --id <link>`. `./attn agent peek <link>` shows `process exited with code` and `screen at exit`, `./attn agent list` shows it `idle`, and `bridge dom_text '{"selector":".desktop-agent-stopped"}'` starts with `Agent stopped. Resume to continue this conversation.` Click `.desktop-agent-stopped button:first-of-type` ("Resume") natively. `bridge dom_wait '{"selector":".desktop-agent-stopped","absent":true,"timeoutMs":15000}'` matches and peek shows the agent running again.
- **A clean quit closes.** Run `wsctl add-session --cwd $P --agent shell --label launch-shell --id launch-shell --place`, then `wsctl input --id <terminal> --text "exit" --enter` on its terminal. `./attn agent list` no longer lists `launch-shell`, and `./attn session list --closed` does.
- **A launch that cannot start.** Run `wsctl add-session --cwd $A --agent pi --label no-pi --id no-pi`. It exits 1 with `error: daemon rejected spawn: agent "pi" is not available`, and `./attn agent list` has no `no-pi` row.
- **Callers from a plain shell.** `./attn agent reload <link>` exits 2 with `no caller; run inside a session or pass --source-session <id>`, and `./attn agent close <link> -m x` exits 2 naming `--source-session`.
- **An empty desktop.** Select `<link>` and press ⌘W. With no tile left, `bridge dom_wait '{"selector":"[data-testid=\"empty-desktop-launcher\"]","timeoutMs":5000}'` matches. Press ⌘N: no overlay opens, and `bridge dom_wait '{"selector":"[data-testid=\"empty-desktop-launcher\"] [data-testid=\"location-picker-path-input\"]","focused":true,"timeoutMs":5000}'` matches.
- **The experimental switches.** Open Settings with `bridge native_key '{"key":",","modifiers":["command"]}'` and click `[data-testid="settings-nav-experimental"]` natively. Click `[data-testid="settings-direct-agent-launch-toggle"]` natively: `./attn settings get direct_agent_launch_enabled` prints `true`. Click it again to put it back. Click `[aria-label="Launcher 2.0"]` natively: `./attn settings get new_session_launcher_enabled` prints `true`. Press Escape to close Settings, press ⌘⇧H to go home, then press ⌘N. `bridge dom_wait '{"selector":"[data-launcher=\"one-screen\"]","timeoutMs":5000}'` matches the Launcher 2.0 dialog. Press Escape, reopen Settings on Experimental, and click the switch again to put it back.
- **Proof.** In `$P`, keep each `./attn` and `wsctl` command with its output and exit code; `bridge location_picker_get_state` with the picker open; `./attn agent list --json` after each launch and close; `./attn agent peek` after each reload and the stop; `./attn session show launch-agent`; and `bridge capture_screenshot '{"path":"<P>/launch-stopped.png"}'` with the stopped tile on screen. Put the way in in each file's name, such as `launch-deep-link-list.json`.

## Contexts

- **Durability across restarts**: agents and their tiles come back after an app or daemon restart. A stopped tile keeps its screen across restarts until someone resumes or closes it. A machine restart brings back the sessions attn can resume and reaps the rest. An agent on the embedded backend stops when the daemon restarts. "Direct agent launches" applies to new and reloaded agents and survives a restart.
- **Linux**: ⌘N is Ctrl+Shift+N, ⌘⇧N is Ctrl+Alt+N and ⌘W is Ctrl+Shift+W (`LINUX_SHORTCUTS`). `attn` with no verb starts the instance's app executable with the link instead of `open`; `attn instance register-scheme` claims the scheme for links from elsewhere.
- **Outposts**: an agent can start on an outpost (`wsctl add-session --host <outpost>`). While its host is offline, Reload, Resume and Close refuse with `<host> is unreachable; this needs it now`, unless the agent is already recoverable.
- **Profiles**: a new agent belongs, for life, to the profile of the desktop it starts on. The spawn link only reuses a session of the selected profile. The chief closes and reloads agents of its own profile only.
- **Harnesses**: the picker lists a terminal, Claude Code, Codex and any harness a running plugin adds, such as Pi once `attn-pi` is installed ([harnesses.md](harnesses.md)); one the machine lacks is disabled. YOLO and auto mode switches act only for harnesses that support them, and on the baseline only YOLO does. Reload keeps the model, effort and permission mode. attn starts and reloads Claude Code with its agent view off (`CLAUDE_CODE_DISABLE_AGENT_VIEW=1`): in the tile, ← on an empty prompt does nothing, the footer has no "← for agents" hint, and `/bg` and `/background` are unknown commands, so the conversation stays in the tile and Reload can resume it. Claude the user starts by hand in a terminal running a shell keeps its agent view. A Codex agent under shared Codex starts against the app-server ([shared-codex.md](shared-codex.md)).
- **Keyboard and native input**: the picker is keyboard-only end to end. ⌥A toggles auto mode in Launcher 2.0 only; the picker has no key for it. ⌘W reaches the web view through the native File menu item, which takes the key first; with focus in a markdown, browser or seed tile, ⌘W closes that tile instead of the agent.

## Tests

- Wire: `internal/daemon/agent_launch_wire_test.go`, `internal/daemon/agent_catalog_wire_test.go`, `internal/daemon/agent_initial_prompt_wire_test.go`, `internal/daemon/agent_login_environment_wire_test.go`, `internal/daemon/agent_machine_wire_test.go`, `internal/daemon/agent_close_wire_test.go`, `internal/daemon/agent_close_lifecycle_wire_test.go`, `internal/daemon/session_close_guard_wire_test.go`, `internal/daemon/agent_reload_wire_test.go`, `internal/daemon/reload_wire_test.go`, `internal/daemon/reload_launch_wire_test.go`, `internal/daemon/reload_plugin_wire_test.go`, `internal/daemon/exit_screen_wire_test.go`, `internal/daemon/stopped_terminal_wire_test.go`, `internal/daemon/launch_intent_wire_test.go`, `internal/daemon/picker_wire_test.go`, `internal/daemon/recent_locations_wire_test.go`, `internal/daemon/slow_launch_wire_test.go`, `internal/daemon/spawn_wire_test.go`, `internal/daemon/spawn_failure_wire_test.go`, `internal/daemon/session_title_wire_test.go`.
- Stack: `cmd/attn/agent_launch_stack_test.go`, `cmd/attn/agent_reload_stack_test.go`, `cmd/attn/agent_env_stack_test.go`, `cmd/attn/agent_signal_stack_test.go`, `cmd/attn/wrapper_stack_test.go`, `internal/daemon/agent_launch_abort_stack_test.go`, `internal/daemon/session_exit_order_stack_test.go`, `app/e2e/location-picker.spec.ts`, `app/e2e/new-session-launcher.spec.ts`.
- Scenario: `new-session-launcher`, `session-creation-shortcuts`, `agent-reload`, `agent-close`, `autoclose-on-exit`.
- Kernel: `internal/daemon/session_title_sanitize_test.go`.
- App: `app/src/App.newSession.test.tsx`, `app/src/App.newSessionLauncher.test.tsx`, `app/src/App.locationPicker.test.tsx`, `app/src/App.emptyDesktopLauncher.test.tsx`, `app/src/App.sessionExit.test.tsx`, `app/src/App.stoppedTerminal.test.tsx`, `app/src/App.sessionCloseGuard.test.tsx`, `app/src/application/useAppDeepLinks.test.tsx`, `app/src/components/LocationPicker.test.tsx`.

## Gotchas

- On an empty desktop ⌘N opens no dialog: it focuses the launcher on the desktop, `[data-testid="empty-desktop-launcher"]`. Wait for the overlay only from the home screen or a desktop with tiles.
- The picker starts its path field empty on a fresh instance. When Settings names a projects folder it starts with that path, and typed text lands after it; press ⌘A first to replace it.
- The picker resolves the folder before it starts the agent, so a folder under `/tmp` runs as `/private/tmp/...` on macOS. Compare paths in that form, as the preconditions write them.
- The auto mode switch is absent for Claude and Codex, so ⌥A has nothing to toggle on the baseline. Driving it needs Pi with the `attn-pi` plugin installed ([auto-mode.md](auto-mode.md)).
- The spawn link selects a live session already in that folder in the selected profile, and the app ignores a link it handled before. Give each deep-link step a folder no session uses, and a label no earlier link used.
- On macOS the spawn link goes through Launch Services. With the instance's app not running, the app it starts drops the instance shell's environment; launch it with `bridge --launch` first.
- `agent reload` and `agent close` need a caller. From a plain shell they exit 2 unless `--source-session` names one; drive them through the mock agent. A self reload can cut the command off before its reply, so read the result from `agent peek` and `agent list`, not from the agent's screen. The changed fixture banner and replayed conversation prove the process restarted. The terminal keeps its `runtime_id`, and `state_since` can stay unchanged when the state stays waiting; the splash looks the same before and after, so reload an agent that has already answered a prompt.
- Use `"resumable": true` when reload or Resume must restore prior replies: attn checks the scratch harness storage before resuming. A fixture without it keeps its transcript only in its own folder, and reload starts fresh.
- Product finding on `drive-1`: after the mock reloads itself, its restored screen shows the prior reply and a prompt, but `agent peek` stays `working (bracket_open)` past the state deadline. The next-prompt precondition does not settle.
- A close is immediate and needs `-m`. The chief and crew members refuse to close, from the app and from `attn agent close`; [crew.md](crew.md) drives that.
- The mock never quits on its own, so a clean quit is driven with a shell. `wsctl kill-session` is an explicit stop: the tile stays stopped even when the process exits 0, and the session reads `idle`, not `recoverable`. `recoverable` marks a session a restart found without its terminal.
- `wsctl add-session --agent pi` is rejected on the baseline because no plugin offers Pi. With `attn-pi` installed it starts if the `pi` binary exists.
- Headless model tasks are off on the baseline, so no session gets a title; its name stays the folder name or the label given.
- Working but wrong: a Claude tile where ← on an empty prompt or `/bg` moves the conversation out of the terminal into Claude's `claude agents` view means the launch lost `CLAUDE_CODE_DISABLE_AGENT_VIEW=1`. The mock has no agent view, so on the baseline only the variable shows: a fixture turn whose `exec` action runs `printenv` with `CLAUDE_CODE_DISABLE_AGENT_VIEW` prints `1` under `$ printenv CLAUDE_CODE_DISABLE_AGENT_VIEW` in `./attn agent peek`.
- Working but wrong: the collapsed sidebar's button says "New Session" where the header and palette say "New agent"; the row menu says "Reload session" where the palette says "Reload this agent"; and the File menu item says "Close Pane" for what closes an agent.

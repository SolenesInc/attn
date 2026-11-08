# Agent launch

Start an agent in a terminal tile, reload it, and close it. A person starts
an agent from the keyboard, the palette, the sidebar or their own shell, picks
a folder and a harness, and attn places the agent beside the active tile on the
current desktop. Reload restarts the agent in the same tile on the same
conversation. Close ends the session for good, with a reason, and keeps the
session's row in the ledger. When an agent stops without quitting cleanly, its
tile keeps the last screen and offers Resume. When an agent quits cleanly, attn
closes the session.

## Sub-features

- `launch-picker`: ⌘N opens the location picker. Pick a folder and a harness, and the agent starts beside the active tile. ⌘⇧N opens it below the active tile instead.
- `launch-inline`: an empty desktop shows the picker on the desktop itself, and ⌘N puts focus there.
- `launch-options`: the picker's harness choice and the YOLO switch. See [worktrees.md](worktrees.md) for the worktrees the picker offers for a repository.
- `launch-deep-link`: `attn` with no verb, run in a folder, opens the app on a spawn link for that folder. If a session already runs in that folder, the app selects that session instead.
- `reload`: interrupt and restart an agent in place on the same conversation, model, effort and permission mode.
- `close`: close a session for good with a reason. attn refuses to close the chief or a crew member.
- `close-compact`: `attn agent close --self --compact` waits for the caller's turn to end, compacts the conversation through the Claude mod or the shared Codex app server, then closes. Nothing the user types or sends stops the close, a note sent while the compact runs is refused saying the session is closing, a compact that meets a running turn retries when the harness reports the turn's end, background work the agent leaves running does not hold the close, and a compact that fails or is skipped still closes with a reason that says so. Both work on an outpost: `TestARealClaudeOnAnOutpostThatClosesCompactingCompactsThroughTheModAndThenCloses` and the other tests in `cmd/attn/claude_compact_outpost_stack_test.go` drive them. Every other session is refused, naming `claude_mod_enabled` or `codex_shared_enabled`.
- `stopped-tile`: an agent that stops without a clean quit keeps its last screen on its tile, with Resume and Close.
- `clean-quit`: when a process quits cleanly, attn closes its session into the ledger.
- `launch-failure`: attn registers no session for an agent that cannot start.
- `session-title`: a background model names a new session from its first prompt.
- `launch-launcher`: the experimental "Launcher 2.0" replaces the picker with one dialog for the path, the repository and its worktree. With an outpost attached it starts on the highlighted recent's machine ([outposts.md](outposts.md) `launcher-machines`).
- `launch-mode`: the "Direct agent launches" switch, on by default, described in Settings as starting agents without an attn process beside each one.
- `launch-before-login-env`: the daemon answers while the login shell runs. Harnesses report `checking`, stay selectable, and every launch waits for the login environment. A launch that names a harness no plugin has registered also waits for a plugin to register it, up to 60 s after the capture, and then fails with `agent "<name>" is not available`. With no plugin installed, it fails when the capture ends.

## How to get to it

### People

- ⌘N (`session.new`, `app/src/shortcuts/registry.ts`) opens the picker, titled "New Session Location", from the home screen or a desktop with tiles. On an empty desktop, ⌘N focuses the launcher already on the desktop (`app/src/application/EmptyDesktopLauncher.tsx`). ⌘⇧N (`session.newHorizontal`) opens the picker and splits sideways.
- The picker (`app/src/components/LocationPicker.tsx`, controls in `app/src/components/NewSessionDialog/SessionControls.tsx`): a path field with Tab completion, Recent and Directories lists, arrows, Enter to start, Escape to cancel, the "Session agent" choice with ⌥1 to ⌥9 and ⌥T for a terminal, and YOLO with ⌥Q.
- Palette (`app/src/application/useAppCommands.tsx`): `new-agent` ("New agent") opens the picker. `reload-active-session` ("Reload this agent") is offered while an agent is on screen.
- Title bar and sidebar: the "New agent" button in the title bar (`app/src/components/TitleBarControls.tsx`), and each sidebar row's actions button, `[data-testid="session-actions-<session>"]` (labelled "Actions for <name>"), whose menu holds "Reload session" and "Close session" (`app/src/components/SessionActionsPopover.tsx`).
- ⌘W (`session.close`) closes the agent on screen, or the focused tile when it is not a terminal, or an empty desktop. In the packaged app, the File menu item "Close Pane" has the shortcut ⌘W and closes the same thing (`app/src-tauri/core/src/lib.rs`).
- A stopped agent's tile says "Agent stopped. Resume to continue this conversation." with Resume and Close (`app/src/components/SessionTerminalDesktop/DesktopAgentBody.tsx`).
- **Settings → Experimental**: "Launcher 2.0" (`new_session_launcher_enabled`) swaps the picker for `app/src/components/NewSessionLauncher.tsx`.
- **Settings → Agents and models**: "Direct agent launches" (`direct_agent_launch_enabled`, `[data-testid="settings-direct-agent-launch-toggle"]`, on by default) starts agents without `attn -s` (`app/src/components/SettingsModalSections.tsx`).
- Their own shell: `attn` with no verb, or `attn -s <label>`, in any folder opens the app on `<scheme>://spawn?cwd=<folder>&label=<label>` (`cmd/attn/main.go`, handled by `app/src/application/useAppDeepLinks.tsx`). The scheme is `attn-<instance>` for a named instance, `attn` for production.
- Where people see what agents do: when an agent reloads or closes another agent, people see a restart or a closed tile, and the ledger records who closed the session and why ([ledger.md](ledger.md)).

### Agents

- `attn agent reload <session-or-member>... | --all` and `attn agent close <session-or-seed> -m "<reason>"`, both with `--source-session` and `--json` (`cmd/attn/agent_reload.go`, `cmd/attn/agent.go`). `attn agent close --self -m "<reason>"` closes the caller without the caller's id. Combining `--self` with a target is a usage error. A session may reload or close itself and the agents below the session in its delegation chain. The chief may act on any agent of the chief's profile.
- `attn agent close --self -m "<reason>" --compact` and `attn seed standby <seed> -m "<note>" --close --compact` start a compacting close (`internal/daemon/agent_close_compact.go`). Through the Claude mod the compact is a `compact` request on `GET /claude/next`, answered on `POST /claude/compacted`; a compact Claude refuses mid-turn is answered `busy`, followed by `ready` when the turn completes (`internal/daemon/claude_mod.go`, `internal/claudemod/mod/hooks/register.tsx`). Through shared Codex it is `thread/compact/start` (`internal/daemon/codex_compact.go`).
- `attn --help` lists `agent close` and `agent reload`. `attn session --help` names `attn agent close` beside reopen. Closing an unknown target prints an error that names `--self`.
- Starting another agent is delegation ([delegation.md](delegation.md)). To open a shell beside the calling agent, use `attn terminal open` ([terminals.md](terminals.md)).
- Prompts: `attn skill --reference converse-and-observe`, which names `agent reload` and `agent close` (`internal/prompts/content/skills/attn/references/converse-and-observe.md`).

### Triggers

- A spawn places the tile and starts the harness. When the harness cannot start, the spawn leaves no session behind (`internal/daemon/spawn_pipeline.go`, `internal/daemon/agent_launch.go`).
- When a process quits cleanly, the daemon closes its session. After any other exit, or an explicit stop, the tile keeps its last screen (`internal/daemon/exit_screen.go`).
- After the first prompt a background model titles the session, unless a person named it (`internal/daemon/session_title.go`, prompts in `internal/prompts/content/session-title/`).

## Driving it

Preconditions:

- Use the baseline instance with its app. Check that `bridge --launch --wait-ready ping` answers. Run `bridge dismiss_whats_new` to dismiss What's new, and stay on the home screen.
- At the repository root in the instance shell, run `R=$PWD && echo "export R=$R" >> $DRIVE_ENV`.
- Make a proof folder with its physical path: run `P=$(cd "$(mktemp -d /tmp/drive-attn-launch.XXXX)" && pwd -P) && echo "export P=$P" >> $DRIVE_ENV`. Then make two empty folders, `$P/picked` and `$P/link`. The picker and the spawn link record the folder as the machine resolves it, `/private/tmp/...` for a folder made under `/tmp`, so every path below starts from the resolved one.
- Make an agent folder with `A=$(cd "$(mktemp -d /tmp/drive-attn-launch-agent.XXXX)" && pwd -P) && echo "export A=$A" >> $DRIVE_ENV`, and save this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "resumable": true, "turns": [
  {"includes": "hello", "actions": [{"type": "reply", "text": "hello back"}]},
  {"includes": "reload", "actions": [
    {"type": "capture", "name": "target", "pattern": "reload (\\S+)"},
    {"type": "attn", "args": ["agent", "reload", "{{target}}"]}]},
  {"includes": "reject-self-target", "actions": [
    {"type": "attn", "args": ["agent", "close", "launch-agent", "--self", "-m", "x"]}]},
  {"includes": "close yourself", "actions": [
    {"type": "attn", "args": ["agent", "close", "--self", "-m", "done by the mock agent"]}]},
  {"includes": "close", "actions": [
    {"type": "capture", "name": "target", "pattern": "close (\\S+)"},
    {"type": "attn", "args": ["agent", "close", "{{target}}", "-m", "done by the mock agent"]}]}
]}
```

`<picked>` is the id that `./attn agent list --json` gives the `picked` row after the first launch. `<picked2>` is the id of the row that appears after the split. `<link>` is the id of the `link` row. `<terminal>` is a session's terminal id: the `runtime_id` of the entry in `wsctl list`'s `desktops[].panes[]` whose `session_id` is that session. Read the terminal id again after a reload. `<P>` is the proof folder's path written out.

- **New agent with ⌘N.** Run `bridge native_key '{"key":"n","modifiers":["command"]}'` and `bridge dom_wait '{"selector":"[data-testid=\"location-picker-overlay\"]","timeoutMs":5000}'`. `bridge location_picker_get_state` shows `open: true`, `title: "New Session Location"` and the harness in `selectedAgent`. Run `bridge native_text '{"text":"<P>/picked/"}'` and `bridge native_key '{"key":"Enter"}'`. `bridge dom_wait '{"selector":"[data-testid=\"location-picker-overlay\"]","absent":true,"timeoutMs":10000}'` matches, and `./attn agent list` shows a row named `picked` with that harness. `./attn agent list --json` gives its `directory` as `<P>/picked`.
- **Picker switches.** Press ⌘N again. Run `bridge native_key '{"key":"q","modifiers":["option"]}'`: `bridge dom_wait '{"selector":"[data-testid=\"location-picker-yolo-toggle\"][aria-checked=\"true\"]","timeoutMs":5000}'` matches. Press ⌥Q again and wait for `[data-testid=\"location-picker-yolo-toggle\"][aria-checked=\"false\"]`. The saved choice is off again. Press Escape. [harnesses.md](harnesses.md) lists the harness keys ⌥1 to ⌥9.
- **Open below with ⌘⇧N.** With `picked` on screen, run `bridge native_key '{"key":"n","modifiers":["command","shift"]}'`, wait for the overlay, type `<P>/picked/` again and press Enter. `wsctl list` shows two sessions in `<P>/picked` on the current desktop, and the desktop's `tree_json` names a split with direction `horizontal`. The new session is `<picked2>`.
- **From the palette and the sidebar.**
  - Run `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`, `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'`, `bridge native_text '{"text":"New agent"}'` and `bridge native_key '{"key":"Enter"}'`: the overlay opens. Press Escape.
  - Resolve `bridge dom_native_point '{"selector":"[aria-label=\"New agent\"]"}'` and click its `x` and `y` with `bridge native_mouse`: the overlay opens again. Press Escape.
  - Collapse the sidebar with `bridge native_key '{"key":"b","modifiers":["command"]}'` (`session.toggleSidebar`), click `[title^="New Session"]` the same way: the overlay opens a third time. Press Escape and ⌘B again.
- **From a person's shell.** Run `(cd $P/link && $R/attn -s link)`. It exits 0, and `./attn agent list` shows a new row named `link`, in `<P>/link`, with the harness `claude`. Select another session with `bridge select_session '{"sessionId":"<picked>"}'`, then run `(cd $P/link && $R/attn -s link-again)`. It exits 0, no row named `link-again` appears, and `bridge get_state` shows `<link>` in `activeLeaf.sessionId`, because the app selects the session already in that folder. The app ignores a link it already handled, so the second label has to differ.
- **Start the fixture agent.** Run `wsctl add-session --cwd $A --agent claude --label launch-agent --id launch-agent --place`, find its terminal in `wsctl list`, and run `wsctl input --id <terminal> --text "hello" --enter`. `./attn agent peek launch-agent` shows `• hello back`. Wait for `waiting_input` before the next prompt.
- **Prepare reload proof.** Before each reload below, change the fixture banner: run `jq --arg banner "Claude Code mock agent (<label>)" '.banner = $banner' "$A/.attn-mock-agent.json" > "$A/next.json" && mv "$A/next.json" "$A/.attn-mock-agent.json"`, replacing `<label>` with `palette reload`, `row reload` or `self reload` for that action. The running process keeps its old banner until it restarts.
- **Reload from the palette and the row menu.**
  - Select `launch-agent` with `bridge select_session '{"sessionId":"launch-agent"}'`, open the palette, type `Reload this agent` and press Enter. Re-read `./attn agent peek launch-agent` until it shows the mock's splash with the banner `Claude Code mock agent (palette reload)` and `• hello back` again, replayed from the conversation. `./attn agent list` still has one row for it.
  - Click `[data-testid="sidebar-session-launch-agent"]` natively to focus its row. Move over `[data-testid="session-actions-launch-agent"]` with `bridge native_mouse` (`action: "move"`), click it natively, and wait for `.session-actions-popover`. Then click `.session-actions-popover [role="menuitem"]:nth-last-child(2)` ("Reload session") natively. Peek shows the replay with the `row reload` banner.
- **An agent reloads itself.** Run `wsctl input --id <terminal> --text "reload launch-agent" --enter`. Re-read `./attn agent peek launch-agent` until it shows the splash, `• hello back` and the `self reload` banner. Read the result from peek and `agent list`, not from the agent's screen, then read the terminal id again before the next prompt.
- **Find close and reject a target with self.** Run `./attn --help`, `./attn session --help` and `./attn agent close --help`: the first lists close and reload, the second names `attn agent close` beside reopen, and the third shows both close forms. Run `wsctl input --id <terminal> --text "close missing-session" --enter`. Peek shows an error naming `--self`. Send `"reject-self-target"` the same way. Peek shows exit 2 with both forms and `--self cannot be used with a target`, leaving the session open.
- **An agent closes itself.** Run `wsctl input --id <terminal> --text "close yourself" --enter`. `./attn agent list` no longer lists `launch-agent`, and `./attn session show launch-agent` shows `state      closed` and `because    done by the mock agent`. Repeat with a Codex fixture agent. The stack regression also checks the close note on a seed the caller tended and the `--source-session` override.
- **An agent closes itself compacting.** The mock agent has neither the mod nor a real compaction, so drive this through the tests. `./scripts/test-go.sh --packages -run 'Compact' ./internal/daemon` runs the fake mod and fake app-server cases. `./scripts/test-go.sh --packages -run TestARealClaudeSessionThatClosesCompacting ./cmd/attn` runs real `claude` with the mod against a mock model: the transcript gets a `system`/`compact_boundary` entry after the close request, and `attn session reopen` resumes from the summary. For the refusal, give the fixture a turn `{"includes": "compact yourself", "actions": [{"type": "attn", "args": ["agent", "close", "--self", "-m", "done by the mock agent", "--compact"]}]}` and send `compact yourself`: `./attn agent peek` shows the refusal naming `claude_mod_enabled` for a Claude fixture and `codex_shared_enabled` for a Codex one, and the session stays open.
- **Close with ⌘W.** Select `<picked>` for setup, then run `bridge native_key '{"key":"w","modifiers":["command"]}'`. `./attn agent list` drops its row, and `./attn session list --closed` lists `<picked>` as `closed`.
- **Close from the sidebar.** Click `[data-testid="sidebar-session-<picked2>"]` natively to focus its row. Move over `[data-testid="session-actions-<picked2>"]` with `bridge native_mouse` (`action: "move"`), click it natively, and wait for `.session-actions-popover`. Then click `.session-actions-popover [role="menuitem"]:last-child` ("Close session") natively. The row leaves the sidebar and `./attn agent list`.
- **Stopped tile and Resume.** Select `<link>` for setup and run `wsctl kill-session --id <link>`. `./attn agent peek <link>` shows `process exited with code` and `screen at exit`, `./attn agent list` shows it `idle`, and `bridge dom_text '{"selector":".desktop-agent-stopped"}'` starts with `Agent stopped. Resume to continue this conversation.` Click `.desktop-agent-stopped button:first-of-type` ("Resume") natively. `bridge dom_wait '{"selector":".desktop-agent-stopped","absent":true,"timeoutMs":15000}'` matches and peek shows the agent running again.
- **A clean quit closes.** Run `wsctl add-session --cwd $P --agent shell --label launch-shell --id launch-shell --place`, then `wsctl input --id <terminal> --text "exit" --enter` on its terminal. `./attn agent list` no longer lists `launch-shell`, and `./attn session list --closed` does.
- **A launch that cannot start.** Run `wsctl add-session --cwd $A --agent absent --label no-agent --id no-agent`. It exits 1 with `error: daemon rejected spawn: agent "absent" is not available`, and `./attn agent list` has no `no-agent` row.
- **Callers from a plain shell.** `./attn agent reload <link>` exits 2 with `no caller; run inside a session or pass --source-session <id>`, and `./attn agent close <link> -m x` exits 2 naming `--source-session`.
- **An empty desktop.** Select `<link>` and press ⌘W. With no tile left, `bridge dom_wait '{"selector":"[data-testid=\"empty-desktop-launcher\"]","timeoutMs":5000}'` matches. Press ⌘N: no overlay opens, and `bridge dom_wait '{"selector":"[data-testid=\"empty-desktop-launcher\"] [data-testid=\"location-picker-path-input\"]","focused":true,"timeoutMs":5000}'` matches.
- **The launch switches.**
  - Open Settings with `bridge native_key '{"key":",","modifiers":["command"]}'` and click `[data-testid="settings-nav-agents"]` natively.
  - Click `[data-testid="settings-direct-agent-launch-toggle"]` natively: `./attn settings get direct_agent_launch_enabled` prints `false`. Click it again to put it back.
  - Click `[data-testid="settings-nav-experimental"]` natively, then click `[aria-label="Launcher 2.0"]` natively: `./attn settings get new_session_launcher_enabled` prints `true`. Press Escape to close Settings, press ⌘⇧H to go home, then press ⌘N. `bridge dom_wait '{"selector":"[data-launcher=\"one-screen\"]","timeoutMs":5000}'` matches the Launcher 2.0 dialog. Press Escape, reopen Settings on Experimental, and click the switch again to put it back.
- **A launch during the login shell.** Stop the daemon by its captured PID, and start it again with a slow login shell. `shellmatrix.sh`, attached to the plot `s-1nsk45`, prints the environment for fish, zsh or bash. While the shell runs, `./attn settings get claude_available` prints `checking`, the picker's harness buttons carry the title "Checking for <Harness> on this machine" and the static mark `.harness-checking-mark`. When the check ends, the mark goes and nothing moves. `wsctl add-session --place` returns after the shell ends, and the agent's `PATH` has the shell's marker. A later `settings_updated` carries `true` or `false`. With a plugin harness installed, `./attn delegate --agent <harness> --model m --cwd <folder> --brief "…"` during the shell returns with the delegate running, and the delegate's `PATH` has the marker. Keep the shell's delay under the capture budget, or no launch gets the login environment.
- **Proof.** In `$P`, keep each `./attn` and `wsctl` command with its output and exit code, `bridge location_picker_get_state` with the picker open, `./attn agent list --json` after each launch and close, `./attn agent peek` after each reload and the stop, `./attn session show launch-agent`, and `bridge capture_screenshot '{"path":"<P>/launch-stopped.png"}'` with the stopped tile on screen. Name each file for its way in, such as `launch-deep-link-list.json`.

## Contexts

- **Durability across restarts**: agents and their tiles come back after an app or daemon restart. A stopped tile keeps its screen across restarts until someone resumes or closes it. After a machine restart, attn brings back the sessions it can resume and reaps the rest. "Direct agent launches" applies to new and reloaded agents and survives a restart.
- **Linux**: ⌘N is Ctrl+Shift+N, ⌘⇧N is Ctrl+Alt+N and ⌘W is Ctrl+Shift+W (`LINUX_SHORTCUTS`). `attn` with no verb starts the instance's app executable with the link instead of `open`. `attn instance register-scheme` claims the scheme for links from elsewhere.
- **Outposts**: an agent can start on an outpost (`wsctl add-session --host <outpost>`). While its host is offline, attn refuses Reload, Resume and Close with `<host> is unreachable; this needs it now`, unless the agent is already recoverable.
- **Profiles**: a new agent belongs, for life, to the profile of the desktop it starts on. The spawn link reuses only a session of the selected profile. The chief closes and reloads agents of its own profile only.
- **Harnesses**: the picker lists a terminal, Claude Code, Codex and any harness a running plugin adds ([harnesses.md](harnesses.md)). The picker disables a harness the machine lacks. The YOLO switch acts only for harnesses that support it. Reload keeps the model, effort and permission mode, and gives an agent that has not yet taken its opening prompt that prompt again. attn starts and reloads Claude Code with its agent view off (`CLAUDE_CODE_DISABLE_AGENT_VIEW=1`). In the tile, ← on an empty prompt does nothing, the footer has no "← for agents" hint, and `/bg` and `/background` are unknown commands. The conversation therefore stays in the tile, and Reload can resume it. A Claude that the user starts by hand in a terminal running a shell keeps its agent view. A Codex agent under shared Codex starts against the app-server ([shared-codex.md](shared-codex.md)).
- **Keyboard and native input**: you can drive the picker with the keyboard alone. ⌘W reaches the web view through the native File menu item, which takes the key first. With focus in a markdown, browser or seed tile, ⌘W closes that tile instead of the agent.

## Tests

- Wire: `internal/daemon/agent_launch_wire_test.go`, `internal/daemon/agent_catalog_wire_test.go`, `internal/daemon/agent_initial_prompt_wire_test.go`, `internal/daemon/agent_login_environment_wire_test.go`, `internal/daemon/agent_machine_wire_test.go`, `internal/daemon/agent_close_wire_test.go`, `internal/daemon/agent_close_compact_wire_test.go`, `internal/daemon/agent_close_compact_draft_wire_test.go`, `internal/daemon/claude_mod_compact_wire_test.go`, `internal/daemon/agent_close_lifecycle_wire_test.go`, `internal/daemon/session_close_guard_wire_test.go`, `internal/daemon/agent_reload_wire_test.go`, `internal/daemon/reload_wire_test.go`, `internal/daemon/reload_launch_wire_test.go`, `internal/daemon/reload_plugin_wire_test.go`, `internal/daemon/exit_screen_wire_test.go`, `internal/daemon/stopped_terminal_wire_test.go`, `internal/daemon/launch_intent_wire_test.go`, `internal/daemon/picker_wire_test.go`, `internal/daemon/recent_locations_wire_test.go`, `internal/daemon/slow_launch_wire_test.go`, `internal/daemon/spawn_wire_test.go`, `internal/daemon/spawn_failure_wire_test.go`, `internal/daemon/session_title_wire_test.go`.
- Stack: `cmd/attn/agent_launch_stack_test.go`, `cmd/attn/claude_compact_stack_test.go`, `cmd/attn/agent_reload_stack_test.go`, `cmd/attn/agent_env_stack_test.go`, `cmd/attn/agent_signal_stack_test.go`, `cmd/attn/wrapper_stack_test.go`, `internal/daemon/agent_launch_abort_stack_test.go`, `internal/daemon/session_exit_order_stack_test.go`, `app/e2e/location-picker.spec.ts`, `app/e2e/new-session-launcher.spec.ts`.
- Scenario: `new-session-launcher`, `session-creation-shortcuts`, `agent-reload`, `agent-close`, `autoclose-on-exit`.
- Kernel: `internal/daemon/session_title_sanitize_test.go`.
- App: `app/src/App.newSession.test.tsx`, `app/src/App.newSessionLauncher.test.tsx`, `app/src/App.newSessionLauncher.outposts.test.tsx`, `app/src/App.locationPicker.test.tsx`, `app/src/App.emptyDesktopLauncher.test.tsx`, `app/src/App.sessionExit.test.tsx`, `app/src/App.stoppedTerminal.test.tsx`, `app/src/App.sessionCloseGuard.test.tsx`, `app/src/application/useAppDeepLinks.test.tsx`, `app/src/components/LocationPicker.test.tsx`.

## Gotchas

- `bridge` cannot click the macOS menu bar. ⌘W exercised the File menu accelerator (`handledBy: ["menu"]`), but a pointer click on File → Close Pane remains unreachable here. That pointer path needs a packaged scenario that uses `createWindowDriver().menu`. No such proof ran here.
- Reload proof needs `"resumable": true`: the mock then writes its conversation under the scratch harness home where attn can discover it. attn checks that storage before resuming, so a fixture without `resumable` keeps its transcript only in its own folder, and reload or Resume starts fresh. A changed `banner` proves that a new process read the fixture. The restored reply proves the conversation survived. The terminal keeps its `runtime_id`, and `state_since` can stay unchanged across a reload, because `state_since` dates the state, not the process.
- Hover the sidebar row before clicking its actions button, as the row-menu steps do. The button's bounds exist even while the row hides it.
- On an empty desktop ⌘N opens no dialog, as **An empty desktop** shows. Wait for the overlay only from the home screen or a desktop with tiles.
- The path field starts empty on a fresh instance. When Settings has a projects folder, the field starts with that path, and typed text goes after the path. Press ⌘A first to replace the path.
- The spawn link selects a live session that is already in that folder and belongs to the selected profile. Give each deep-link step a folder no session uses, and a label no earlier link used, as **From a person's shell** does.
- On macOS the spawn link goes through Launch Services. If the instance's app is not running, the app that Launch Services starts does not get the instance shell's environment. Run `bridge --launch` first.
- `agent reload` and `agent close` need a caller, so drive them through the mock agent. **Callers from a plain shell** shows the refusal without one. A self reload can cut the command off before the command's reply, which is why **An agent reloads itself** reads `agent peek` and `agent list`. The splash looks the same before and after, so reload an agent that has already answered a prompt.
- Product finding on `drive-1`: after the mock reloads itself, its restored screen shows the prior reply and a prompt, but `agent peek` stays `working (bracket_open)` past the state deadline. The next-prompt precondition does not settle.
- A close is immediate and needs `-m`, except with `--compact`, which waits for the turn to end. A daemon restart drops a close that still waits for its compact. attn refuses to close the chief and crew members, from the app and from `attn agent close`. The recipe for that is in [crew.md](crew.md).
- The mock never quits on its own, so drive a clean quit with a shell. `wsctl kill-session` is an explicit stop: the tile stays stopped even when the process exits 0, and the session reads `idle`, not `recoverable`. `recoverable` marks a session a restart found without its terminal.
- Headless model tasks are off on the baseline, so no session gets a title. A session's name stays the folder name or the label given.
- Working but wrong: if ← on an empty prompt or `/bg` in a Claude tile moves the conversation out of the terminal into Claude's `claude agents` view, the launch lost `CLAUDE_CODE_DISABLE_AGENT_VIEW=1`. The mock has no agent view, so on the baseline only the variable shows. A fixture turn whose `exec` action runs `printenv` with `CLAUDE_CODE_DISABLE_AGENT_VIEW` prints `1` under `$ printenv CLAUDE_CODE_DISABLE_AGENT_VIEW` in `./attn agent peek`.
- Working but wrong: the row menu says "Reload session" where the palette says "Reload this agent". The File menu item says "Close Pane" for the action that closes an agent.

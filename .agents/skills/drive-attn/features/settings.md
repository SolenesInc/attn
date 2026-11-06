# Settings

The settings dialog and `attn settings`: home settings shared with outposts,
machine settings per machine. People change attn in a dialog that saves each
change as they make it. People and agents read and change the same settings
from the CLI. The home decides a home setting and copies it read-only to its
outposts. A machine setting, such as a harness executable or the worktree
sweep, belongs to the machine that holds it. Each section of the dialog
belongs to the feature it configures. This file covers the dialog, the CLI and
Appearance.

## Sub-features

- `settings-dialog`: the dialog, its grouped sections, search and close.
- `settings-autosave`: switches and choices save on click. Text fields save on Enter or when focus leaves them. The autosave status reads "Saving…" and "Saved", and shows Retry when the daemon refuses the value.
- `settings-cli`: `attn settings list`, `get` and `set`, with read-only computed values under `--all`.
- `home-and-machine`: home settings decided at the home, which an outpost reads from a copy and sets through the home, and machine settings per machine.
- `experimental`: the switches for features still in development, off by default, generated from the declarations in `internal/setting`. Palette commands turn each one on and off, and `attn settings list --features` lists them.
- `appearance`: theme, and text size for the app and for the garden.
- `github-hosts`: the GitHub hosts attn found.

## How to get to it

### People

- ⌘, (`ui.openSettings`, `app/src/shortcuts/registry.ts`) opens the dialog, and closes it when open. The binding is protected: the user can rebind it but never unbind it.
- With the shelf layout on, ⌥⌘− and ⌥⌘= and the shelf's width control set `narrowest_tile_cols`, and dragging the sidebar's edge, ⌥⌘] and ⌥⌘[ set `sidebar_width`. The Experimental section also shows both ([desktops.md](desktops.md), [sidebar.md](sidebar.md)).
- ⌘=, ⌘- and ⌘0 (`ui.increaseFontSize`, `ui.decreaseFontSize`, `ui.resetFontSize`) change the app's text size, which Appearance also shows.
- Palette (`app/src/application/useAppCommands.tsx`): `settings` ("Settings").
- Home screen: the gear, `[data-testid="settings-button"]` (`app/src/components/Dashboard.tsx`).
- In the dialog (`app/src/components/SettingsModal.tsx`, sections in `app/src/components/useSettingsModalState.ts`): the nav buttons `[data-testid="settings-nav-<section>"]`, "Search settings", the close button `[data-testid="settings-close"]`, the backdrop, and Escape. The sections and the feature that owns each:
  - attn: Appearance (`general`, this file). Files and locations (`desktop`: [worktrees.md](worktrees.md), [notebook.md](notebook.md), [markdown.md](markdown.md), and the Projects Directory for [pull-requests.md](pull-requests.md)). Attention queue (`hygiene`: [queue-flow.md](queue-flow.md), and muted repositories and authors for [pull-requests.md](pull-requests.md)).
  - Agents: Agents and models (`agents`) and Models (`models`), [harnesses.md](harnesses.md). Background agents (`backgroundAgents`), the garden advisor in [garden.md](garden.md) and the chief in [crew.md](crew.md). Delegation (`delegation`), [delegation.md](delegation.md).
  - Connectivity: GitHub hosts (`connectivity`), the hosts [pull-requests.md](pull-requests.md) uses. Outposts (`outposts`), shown while Outposts is on, [outposts.md](outposts.md).
  - Extensions: Plugins (`plugins`), [harnesses.md](harnesses.md).
  - System: Experimental (`experimental`), each switch with its feature. Task runner (`backgroundTasks`) and Event bus (`eventBus`), [diagnostics.md](diagnostics.md). Analytics (`analytics`), shown while analytics is on, [telemetry.md](telemetry.md).
- Where people see what agents do: a setting an agent changes with `attn settings set` shows in the open dialog as the toggle's new value. `theme` and `uiScale` are the exceptions, because the open app reads them from the daemon only once (see Gotchas).
- Their own shell: `attn settings`, below.

### Agents

- `attn settings` (`cmd/attn/settings.go`): `list` (with `--all` and `--json`), `get <key>` (with `--json`) and `set <key> <value>`. `set` to `""` restores the default where the key's description allows it. A key family such as `default_model_<harness>` is set through a concrete key, such as `default_model_claude`.
- Prompts: the attn skill tells agents to read and change daemon settings with `attn settings list`, `get` and `set`, and that settings apply across all profiles (`internal/prompts/content/skills/attn/SKILL.md`).

## Driving it

Preconditions:

- Use the baseline instance with its app. Check that `bridge --launch --wait-ready ping` answers, then run `bridge dismiss_whats_new` to dismiss What's new. The shell steps need only the daemon.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-settings.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- Make an agent folder with `A=$(mktemp -d /tmp/drive-attn-settings-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, and save this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "sweep off", "actions": [
    {"type": "attn", "args": ["settings", "set", "worktree_sweep_enabled", "false"]}]},
  {"includes": "read theme", "actions": [
    {"type": "attn", "args": ["settings", "get", "theme"]}]}
]}
```

- **List from the shell.** Run `./attn settings list`. The command prints one row per key: the key's scope, its value or `-`, and its description, such as `theme` with `App color theme: dark, light or system.`. The scope reads `home` or `this machine`, except for the profile settings `notebook.root` and `pull_requests.own_comments`, which read `profile <name>`. The command leaves out read-only keys. `./attn settings list --all` adds them, such as `pty_backend_mode` with `this machine, read-only`.
- **Set and get.** Run `./attn settings set theme light`: it prints `theme = light`. `./attn settings get theme` prints `light`, and `./attn settings get theme --json` prints the entry with `"key": "theme"` and `"value": "light"`.
- **Refusals.** Each of these commands exits 1 with `settings: ` and the reason: `./attn settings set theme purple` (`invalid theme: purple (must be dark, light, or system)`), `./attn settings set pty_backend_mode worker` (`setting pty_backend_mode is read-only`), `./attn settings set unknown.key value` (`unknown setting: unknown.key`) and `./attn settings set "default_model_<harness>" x` (`requires a concrete key; replace its placeholders`). `./attn settings set theme` exits 2 with the usage.
- **Open with the shortcut.** Press ⌘,: `bridge native_key '{"key":",","modifiers":["command"]}'`. `bridge settings_get_state` shows `open: true`, and on the first open `activeSection: "connectivity"`. Press ⌘, again: `open` is false.
- **Open from the palette.** Run `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`, `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'`, `bridge native_text '{"text":"Settings"}'` and `bridge native_key '{"key":"Enter"}'`. `bridge settings_get_state` shows `open: true`. Press Escape: `open` is false.
- **Open from the home screen.** Press ⌘⇧H (`session.goToDashboard`): `bridge native_key '{"key":"h","modifiers":["command","shift"]}'`. Resolve the point of `[data-testid="settings-button"]` with `bridge dom_native_point` and click it with `bridge native_mouse`. `bridge settings_get_state` shows `open: true`.
- **Change in the dialog, read from the shell.** Click `[data-testid="settings-nav-general"]` natively: `settings_get_state` shows `activeSection: "general"`, and `bridge dom_text '{"selector":"[aria-label=\"Theme preference\"] [aria-pressed=\"true\"]"}'` reads `Light`. Click `[aria-label="Theme preference"] button:nth-child(1)` ("Dark") natively. Re-read `./attn settings get theme` until it prints `dark`. The app saves a theme click at once and shows no "Saved".
- **Text size from the keyboard.** Press ⌘=: `bridge native_key '{"key":"=","modifiers":["command"]}'`. `./attn settings get uiScale` prints `1.1`, and `bridge dom_text '{"selector":"[data-testid=\"settings-app-font-scale-value\"]"}'` reads `110%`. Press ⌘- twice (`bridge native_key '{"key":"-","modifiers":["command"]}'`): the value reads `90%` and `uiScale` prints `0.9`. Press ⌘0 (`bridge native_key '{"key":"0","modifiers":["command"]}'`): the value reads `100%` and `uiScale` prints `1`.
- **Search.** Click `[aria-label="Search settings"]` natively and type `sweep` with `bridge native_text`. `bridge dom_text '{"selector":"[aria-label=\"Settings sections\"]"}'` reads only `attn` and `Files and locations`, and `settings_get_state` shows `search: "sweep"`.
- **A change from the shell shows in the dialog.** Click `[data-testid="settings-nav-desktop"]` natively. `bridge dom_text '{"selector":"[data-testid=\"settings-worktree-sweep-toggle\"]"}'` reads `Disable`. Run `./attn settings set worktree_sweep_enabled false`: the toggle reads `Enable`. Run `./attn settings set worktree_sweep_enabled true`: the toggle reads `Disable` again.
- **Autosave, a refusal and Retry.** With Files and locations still open, click `[data-testid="settings-projects-directory-input"]` natively. Press ⌘A (`bridge native_key '{"key":"a","modifiers":["command"]}'`), type `relative/dir` with `bridge native_text`, and press Enter. `bridge dom_text '{"selector":".settings-autosave-status"}'` reads the refusal, `projects directory must be an absolute path`, beside `Retry`, and `./attn settings get projects_directory` still prints the old value. Press ⌘A, type the proof folder's path, written out, with `bridge native_text`, and press Enter: `bridge dom_wait '{"selector":".settings-autosave-status","textIncludes":"Saved","timeoutMs":5000}'` matches, and `./attn settings get projects_directory` prints that path.
- **An agent changes a setting.** Run `wsctl add-session --cwd $A --agent claude --label settings-agent --id settings-agent --place`, find its terminal in `wsctl list`, and run `wsctl input --id <terminal> --text "sweep off" --enter`. `./attn agent peek settings-agent` shows `worktree_sweep_enabled = false`, and the open dialog's toggle reads `Enable`. `wsctl input --id <terminal> --text "read theme" --enter` shows `dark` in peek. Put the sweep back with `./attn settings set worktree_sweep_enabled true`.
- **GitHub hosts.** Clear the earlier search: click `[aria-label="Search settings"]` natively, press ⌘A (`bridge native_key '{"key":"a","modifiers":["command"]}'`), then Backspace (`bridge native_key '{"key":"Backspace"}'`). Click `[data-testid="settings-nav-connectivity"]` natively. `bridge dom_text '{"selector":"[data-testid=\"settings-section-connectivity\"]"}'` reads "GitHub Hosts" with the hosts attn found.
- **Experimental.** Click `[data-testid="settings-nav-experimental"]` natively. `bridge dom_text '{"selector":"[data-testid=\"settings-section-experimental\"]"}'` lists the opt-in switches by group, Outposts among them, each with when a change applies. Run `./attn settings list --features`: it prints the same features with their state. Drive each switch in its feature's file. Drive Outposts in [outposts.md](outposts.md).
- **Close.** Click `[data-testid="settings-close"]` natively: `settings_get_state` shows `open: false`. Open the dialog with ⌘, again, and click natively just left of the dialog, at `bridge dom_native_point '{"selector":".settings-modal","at":{"x":0,"y":0.5},"offset":{"x":-10,"y":0}}'`: the click lands on the backdrop and `open` is false. Open the dialog once more and press Escape: `open` is false, and nothing reads "Saving…".
- **Home and machine settings on an outpost.** With an outpost paired, as [outposts.md](outposts.md) describes, run on the outpost `attn settings list --all`: home settings such as `theme` carry `set at the home`. `attn settings set theme system` there prints `theme = system`, and the home's `./attn settings get theme` prints `system`. `attn settings set worktree_sweep_enabled false` there leaves the home's value alone. With the home unreachable, the outpost's home-setting write fails and names the home as unreachable. On the baseline, which has no outpost, this step fails as Gotchas says.
- **Proof.** In `$P`, keep each `./attn settings` command's output with its exit code. Keep `bridge settings_get_state` after each open, section change, search and close, and `./attn agent peek settings-agent` after each turn. Capture `bridge capture_screenshot '{"path":"<P>/settings-dialog.png"}'` with the dialog open on Files and locations, where `<P>` is the folder's path written out. Name each file for its way in, such as `settings-shortcut-state.json`.

## Contexts

- **Durability across restarts**: settings are daemon records and come back after any restart. The dialog says "Saved" only once the daemon has kept the value, and a refused change leaves the old value in place. An outpost reads its copy of the home settings while the home is unreachable.
- **Linux**: `attn settings` works the same where the daemon and CLI run on Linux. `ui.openSettings` is Ctrl+Shift+, there, and the text size keys are Ctrl+Shift+=, Ctrl+Shift+- and Ctrl+Shift+0 (`LINUX_SHORTCUTS`).
- **Outposts**: the home decides home settings. Each outpost keeps a read-only copy, and `attn settings set` on an outpost changes a home setting at the home, except `outposts_enabled`, which an outpost refuses because it reaches the home over that bridge. Machine settings (harness executables, the terminal backend, the worktree sweep, analytics recording) stay on the machine that sets them. Profile settings (`notebook.root`, `pull_requests.own_comments`) live at the home: on an outpost, `attn settings get notebook.root` exits 1 naming the home, `attn settings list` leaves it out, and `notebook.root.effective` names the outpost copy. `attn settings` acts on the session's profile inside attn and takes `--profile <name|id>` outside it when several profiles exist. After `attn enrollment leave`, the outpost uses the values it had before pairing.
- **Profiles**: every setting applies across all profiles except `notebook.root`, its read-only companion `notebook.root.effective`, and `pull_requests.own_comments`, which belong to one profile, and `attn settings list` says `home` or `this machine` for the rest. Flow (`queue_mode_enabled`) is one of the shared settings: it is one setting for all profiles.
- **Harnesses**: each harness has its own machine setting for its executable (`<harness>_executable`) and home families for its defaults (`default_model_<harness>`, `default_effort_<harness>`). See [harnesses.md](harnesses.md) for them.
- **Keyboard and native input**: ⌘, opens and closes the dialog, Escape closes it, and ⌘= ⌘- ⌘0 resize text from anywhere. No macOS menu item claims ⌘,.

## Tests

- Wire: `internal/daemon/settings_ack_wire_test.go`, `internal/daemon/settings_validation_wire_test.go`.
- Stack: `cmd/attn/settings_stack_test.go`, `cmd/attn/settings_outpost_stack_test.go`, `app/e2e/settings.spec.ts`, `app/e2e/settings-visual.spec.ts`.
- Scenario: `focusless-native` (parked macOS native input), `agent-settings`.
- App: `app/src/App.settingsAutosave.test.tsx`, `app/src/components/SettingsModal*.test.tsx`.

## Gotchas

- The dialog first opens on GitHub hosts (`connectivity`), not Appearance, and keeps the last section and the last search while the app runs. A search that leaves a section out hides its nav button: when `bridge settings_get_state` shows a `search`, clear it as the GitHub hosts step does before clicking a `settings-nav-<section>` button.
- The dialog shows the shell's `theme light` only because a fresh instance has no theme yet, and the app takes the first value it sees. On an instance that already had a theme, run the theme steps from the dialog first.
- The Outposts and Analytics sections exist only while `outposts_enabled` or `analytics.enabled` is true. `bridge settings_select_section` refuses an unknown id and needs the dialog open. Like `set_setting`, it skips native input and serves only as setup.
- `""` restores the default of a true or false setting, and of another key where its description says so. `attn settings set theme ""` is refused as an invalid theme.
- `attn settings list` prints `profile <name>` as the scope of `notebook.root` and `notebook.root.effective` (under `--all`), and `home` or `this machine` for the rest. On an outpost, a home setting adds `set at the home`.
- The home and machine step needs a paired outpost, which the baseline lacks. `cmd/attn/settings_outpost_stack_test.go` covers it.
- Working but wrong: the app takes `theme` and `uiScale` from the daemon once. After that, `attn settings set theme` or `uiScale` from a shell or an agent changes the daemon but not the open app, and Appearance still shows the old choice until the app relaunches. The daemon accepts `uiScale` from 0.5 to 2.0, while the app uses only 0.7 to 1.5 and ignores the rest.

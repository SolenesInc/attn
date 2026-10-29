# Settings

The settings dialog and `attn settings`: home settings shared with outposts,
machine settings per machine. People change attn in a dialog that saves each
change as they make it; people and agents read and change the same settings
from the CLI. A home setting is decided by the home and copied read-only to
its outposts; a machine setting, such as a harness executable or the worktree
sweep, belongs to the machine that holds it. Each section of the dialog
belongs to the feature it configures; this file covers the dialog, the CLI and
Appearance.

## Sub-features

- `settings-dialog`: the dialog, its grouped sections, search and close.
- `settings-autosave`: switches and choices save on click; text fields save on Enter or when focus leaves them, say "Saving…" and "Saved", and offer Retry when the daemon refuses the value.
- `settings-cli`: `attn settings list`, `get` and `set`, with read-only computed values under `--all`.
- `home-and-machine`: home settings decided at the home and read-only on outposts; machine settings per machine.
- `experimental`: the switches for features still in development, off by default.
- `appearance`: theme, and text size for the app and for the garden.
- `mobile-and-hosts`: Tailscale Serve for a phone's browser, and the GitHub hosts attn found.

## How to get to it

### People

- ⌘, (`ui.openSettings`, `app/src/shortcuts/registry.ts`) opens the dialog, and closes it when open. It is protected: it can be rebound but never unbound.
- ⌘=, ⌘- and ⌘0 (`ui.increaseFontSize`, `ui.decreaseFontSize`, `ui.resetFontSize`) change the app's text size, which Appearance also shows.
- Palette (`app/src/application/useAppCommands.tsx`): `settings` ("Settings").
- Home screen: the gear, `[data-testid="settings-button"]` (`app/src/components/Dashboard.tsx`).
- In the dialog (`app/src/components/SettingsModal.tsx`, sections in `app/src/components/useSettingsModalState.ts`): the nav buttons `[data-testid="settings-nav-<section>"]`, "Search settings", the close button `[data-testid="settings-close"]`, the backdrop, and Escape. The sections and the feature that owns each:
  - attn: Appearance (`general`, this file); Files and locations (`desktop`: [worktrees.md](worktrees.md), [notebook.md](notebook.md), [markdown.md](markdown.md), and the Projects Directory for [pull-requests.md](pull-requests.md)); Attention queue (`hygiene`: [queue-flow.md](queue-flow.md), and muted repositories and authors for [pull-requests.md](pull-requests.md)).
  - Agents: Agents and models (`agents`) and Models (`models`), [harnesses.md](harnesses.md); Background agents (`backgroundAgents`), the garden advisor in [garden.md](garden.md) and the chief in [crew.md](crew.md); Delegation (`delegation`), [delegation.md](delegation.md); Auto mode (`autoMode`), [auto-mode.md](auto-mode.md).
  - Connectivity: Mobile and hosts (`connectivity`), Tailscale Serve for a phone's browser (`tailscale_enabled`) and the GitHub hosts attn found for [pull-requests.md](pull-requests.md); no other file owns Tailscale, so this one does. Outposts (`outposts`), shown while Outposts is on, [outposts.md](outposts.md).
  - Extensions: Plugins (`plugins`), [harnesses.md](harnesses.md).
  - System: Experimental (`experimental`), each switch with its feature; Task runner (`backgroundTasks`) and Event bus (`eventBus`), [diagnostics.md](diagnostics.md); Analytics (`analytics`), shown while analytics is on, [telemetry.md](telemetry.md).
- Where people see what agents do: a setting an agent changes with `attn settings set` shows in the open dialog, as the toggle that reads its value.
- Their own shell: `attn settings`, below.

### Agents

- `attn settings` (`cmd/attn/settings.go`): `list` (with `--all` and `--json`), `get <key>` (with `--json`) and `set <key> <value>`. `set` to `""` restores the default where the key's description allows it. A key family such as `default_model_<harness>` is set through a concrete key, such as `default_model_claude`.
- Prompts: the attn skill tells agents to read and change daemon settings with `attn settings list`, `get` and `set`, and that settings apply across all profiles (`internal/prompts/content/skills/attn/SKILL.md`).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`). The shell steps need only the daemon.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-settings.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- An agent folder, `A=$(mktemp -d /tmp/drive-attn-settings-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, holding this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "sweep off", "actions": [
    {"type": "attn", "args": ["settings", "set", "worktree_sweep_enabled", "false"]}]},
  {"includes": "read theme", "actions": [
    {"type": "attn", "args": ["settings", "get", "theme"]}]}
]}
```

- **List from the shell.** Run `./attn settings list`. It prints one row per key with its scope, `all profiles`, its value or `-`, and its description, such as `theme` with `App color theme: dark, light or system.`. Read-only keys are left out; `./attn settings list --all` adds them, such as `pty_backend_mode` with `all profiles, read-only`.
- **Set and get.** Run `./attn settings set theme light`: it prints `theme = light`. `./attn settings get theme` prints `light`, and `./attn settings get theme --json` prints the entry with `"key": "theme"` and `"value": "light"`.
- **Refusals.** Each exits 1 with `settings: ` and the reason: `./attn settings set theme purple` (`invalid theme: purple (must be dark, light, or system)`), `./attn settings set pty_backend_mode embedded` (`setting pty_backend_mode is read-only`), `./attn settings set unknown.key value` (`unknown setting: unknown.key`) and `./attn settings set "default_model_<harness>" x` (`requires a concrete key; replace its placeholders`). `./attn settings set theme` exits 2 with the usage.
- **Open with the shortcut.** Press ⌘,: `bridge native_key '{"key":",","modifiers":["command"]}'`. `bridge settings_get_state` shows `open: true`, and on the first open `activeSection: "connectivity"`. Press ⌘, again: `open` is false.
- **Open from the palette.** Run `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`, `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'`, `bridge native_text '{"text":"Settings"}'` and `bridge native_key '{"key":"Enter"}'`. `bridge settings_get_state` shows `open: true`. Press Escape: `open` is false.
- **Open from the home screen.** Press ⌘⇧H (`session.goToDashboard`): `bridge native_key '{"key":"h","modifiers":["command","shift"]}'`. Read `[data-testid="settings-button"]` with `bridge dom_bounds`, convert it as the README says, and click it with `bridge native_mouse`. `bridge settings_get_state` shows `open: true`.
- **Change in the dialog, read from the shell.** Click `[data-testid="settings-nav-general"]` natively: `settings_get_state` shows `activeSection: "general"`, and `bridge dom_text '{"selector":"[aria-label=\"Theme preference\"] [aria-pressed=\"true\"]"}'` reads `Light`. Click `[aria-label="Theme preference"] button:nth-child(1)` ("Dark") natively. Re-read `./attn settings get theme` until it prints `dark`; a theme click saves at once and shows no "Saved".
- **Text size from the keyboard.** Press ⌘=: `bridge native_key '{"key":"=","modifiers":["command"]}'`. `./attn settings get uiScale` prints `1.1`, and `bridge dom_text '{"selector":"[data-testid=\"settings-app-font-scale-value\"]"}'` reads `110%`. Press ⌘- twice (`bridge native_key '{"key":"-","modifiers":["command"]}'`): the value reads `90%` and `uiScale` prints `0.9`. Press ⌘0 (`bridge native_key '{"key":"0","modifiers":["command"]}'`): the value reads `100%` and `uiScale` prints `1`.
- **Search.** Click `[aria-label="Search settings"]` natively and type `sweep` with `bridge native_text`. `bridge dom_text '{"selector":"[aria-label=\"Settings sections\"]"}'` reads only `attn` and `Files and locations`, and `settings_get_state` shows `search: "sweep"`.
- **A change from the shell shows in the dialog.** Click `[data-testid="settings-nav-desktop"]` natively. `bridge dom_text '{"selector":"[data-testid=\"settings-worktree-sweep-toggle\"]"}'` reads `Disable`. Run `./attn settings set worktree_sweep_enabled false`: the toggle reads `Enable`. Run `./attn settings set worktree_sweep_enabled true`: it reads `Disable` again.
- **Autosave, a refusal and Retry.** With Files and locations still open, click `[data-testid="settings-projects-directory-input"]` natively, press ⌘A (`bridge native_key '{"key":"a","modifiers":["command"]}'`), type `relative/dir` with `bridge native_text` and press Enter. `bridge dom_text '{"selector":".settings-autosave-status"}'` names the refusal, `projects directory must be an absolute path`, beside `Retry`, and `./attn settings get projects_directory` still prints the old value. Press ⌘A, type the proof folder's path, written out, with `bridge native_text`, and press Enter: `bridge dom_wait '{"selector":".settings-autosave-status","textIncludes":"Saved","timeoutMs":5000}'` matches, and `./attn settings get projects_directory` prints that path.
- **An agent changes a setting.** Run `wsctl add-session --cwd $A --agent claude --label settings-agent --id settings-agent --place`, find its terminal in `wsctl list`, and run `wsctl input --id <terminal> --text "sweep off" --enter`. `./attn agent peek settings-agent` shows `worktree_sweep_enabled = false`, and the open dialog's toggle reads `Enable`. `wsctl input --id <terminal> --text "read theme" --enter` shows `dark` in peek. Put the sweep back with `./attn settings set worktree_sweep_enabled true`.
- **Mobile and hosts.** Clear the earlier search: click `[aria-label="Search settings"]` natively, press ⌘A (`bridge native_key '{"key":"a","modifiers":["command"]}'`), then Backspace (`bridge native_key '{"key":"Backspace"}'`). Click `[data-testid="settings-nav-connectivity"]` natively. `bridge dom_text '{"selector":"[data-testid=\"settings-section-connectivity\"]"}'` reads "Mobile Web Client", with Tailscale Serve's Enable button, and "GitHub Hosts" with the hosts attn found. `./attn settings get tailscale_enabled` prints an empty line. Turning Tailscale on needs the machine's own Tailscale client signed in, which the baseline does not assume.
- **Experimental.** Click `[data-testid="settings-nav-experimental"]` natively. `bridge dom_text '{"selector":"[data-testid=\"settings-section-experimental\"]"}'` lists the opt-in switches, Outposts among them; each switch is driven in its feature's file, Outposts in [outposts.md](outposts.md).
- **Close.** Click `[data-testid="settings-close"]` natively: `settings_get_state` shows `open: false`. Open it with ⌘, again, read the dialog's box with `bridge dom_bounds '{"selector":".settings-modal"}'`, and click natively between the window's left edge and that box, at `x = box.x / 2 / window.width` and the `y` of the box's middle as the README converts it: the click lands on the backdrop and `open` is false. Open it once more and press Escape: `open` is false, and nothing reads "Saving…".
- **Home and machine settings on an outpost.** With an outpost paired, as [outposts.md](outposts.md) describes, run on the outpost `attn settings list --all`: home settings such as `theme` carry `read-only`. `attn settings set theme system` there prints `theme = system`, and the home's `./attn settings get theme` prints `system`. `attn settings set worktree_sweep_enabled false` there leaves the home's value alone. With the home unreachable, the outpost's home-setting write fails and names it unreachable. On the baseline, which has no outpost, this step fails as Gotchas says.
- **Proof.** In `$P`, keep each `./attn settings` command's output with its exit code; `bridge settings_get_state` after each open, section change, search and close; `./attn agent peek settings-agent` after each turn; and `bridge capture_screenshot '{"path":"<P>/settings-dialog.png"}'` with the dialog open on Files and locations, where `<P>` is the folder's path written out. Put the way in in each file's name, such as `settings-shortcut-state.json`.

## Contexts

- **Durability across restarts**: settings are daemon records and come back after any restart. The dialog says "Saved" only once the daemon has kept the value, and a refused change leaves the old value in place. An outpost reads its copy of the home settings while the home is unreachable.
- **Linux**: `attn settings` works the same where the daemon and CLI run on Linux. `ui.openSettings` is Ctrl+Shift+, there, and the text size keys are Ctrl+Shift+=, Ctrl+Shift+- and Ctrl+Shift+0 (`LINUX_SHORTCUTS`).
- **Outposts**: home settings are decided at the home; each outpost keeps a read-only copy, and `attn settings set` on an outpost changes a home setting at the home. Machine settings (harness executables, the terminal backend, the worktree sweep, analytics recording, `notebook.root`, `outposts_enabled`, `outpost_listen`) stay on the machine that sets them. After `attn enrollment leave`, the outpost uses the values it had before pairing.
- **Profiles**: every setting applies across all profiles, and `attn settings list` says `all profiles` for each. Flow is one of them today (`queue_mode_enabled`), though the glossary keeps it per profile.
- **Harnesses**: each harness has its own machine setting for its executable (`<harness>_executable`) and home families for its defaults (`default_model_<harness>`, `default_effort_<harness>`); [harnesses.md](harnesses.md) covers them.
- **Keyboard and native input**: ⌘, opens and closes the dialog, Escape closes it, and ⌘= ⌘- ⌘0 resize text from anywhere. No macOS menu item claims ⌘,.

## Tests

- Wire: `internal/daemon/settings_ack_wire_test.go`, `internal/daemon/settings_validation_wire_test.go`, `internal/daemon/tailscale_wire_test.go`.
- Stack: `cmd/attn/settings_stack_test.go`, `cmd/attn/settings_outpost_stack_test.go`, `app/e2e/settings.spec.ts`, `app/e2e/settings-visual.spec.ts`.
- Scenario: `agent-settings`.
- App: `app/src/App.settingsAutosave.test.tsx`, `app/src/components/SettingsModal*.test.tsx`.

## Gotchas

- The dialog first opens on Mobile and hosts (`connectivity`), not Appearance, and keeps the last section while the app runs.
- The dialog shows the shell's `theme light` only because a fresh instance has no theme yet, and the app takes the first value it sees. On an instance that already had a theme, run the theme steps from the dialog first.
- The Outposts and Analytics sections exist only while `outposts_enabled` or `analytics.enabled` is true. `bridge settings_select_section` refuses an unknown id and needs the dialog open; like `set_setting`, it skips native input and serves only as setup.
- `""` restores a default only where the key's description says so. `attn settings set theme ""` is refused as an invalid theme.
- `attn settings list` prints `all profiles` as every key's scope. It never says home or machine; the difference shows only on an outpost, as `read-only`.
- The home and machine step needs a paired outpost, which the baseline lacks; `cmd/attn/settings_outpost_stack_test.go` covers it.
- Working but wrong: the app takes `theme` and `uiScale` from the daemon once. After that, `attn settings set theme` or `uiScale` from a shell or an agent changes the daemon but not the open app, and Appearance still shows the old choice until the app relaunches. The daemon accepts `uiScale` from 0.5 to 2.0, while the app uses only 0.7 to 1.5 and ignores the rest.

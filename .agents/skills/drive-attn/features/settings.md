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
- `settings-autosave`: switches and choices save on click. Text fields save on Enter or when focus leaves them. The autosave status reads "Saving…" and "Saved". When the daemon refuses a value, its reason and Retry show beside a text field, and in the autosave status for a switch or choice. Leaving a field does not resend a refused value it still holds. Enter, Retry, closing the dialog and changing section do.
- `settings-cli`: `attn settings list`, `get` and `set`, with read-only computed values under `--all`.
- `home-and-machine`: home settings decided at the home, which an outpost reads from a copy and sets through the home, and machine settings per machine.
- `experimental`: the switches for features still in development, off by default, generated from the declarations in `internal/setting`. Palette commands turn each one on and off, and `attn settings list --features` lists them.
- `appearance`: theme, color scheme and custom colors, text size for the app and for the garden, the narrowest tile width and the sidebar width. `docs/guide/reference/colors.md` describes `color_scheme` and `color_overrides`.
- `github-hosts`: the GitHub hosts attn found.

## How to get to it

### People

- ⌘, (`ui.openSettings`, `native/crates/attn-app/src/keymap.rs`) opens the dialog, and closes it when open. The binding is protected: the user can rebind it but never unbind it.
- ⌥⌘− and ⌥⌘= and the shelf's width control set `narrowest_tile_cols`, and dragging the sidebar's edge, ⌥⌘] and ⌥⌘[ set `sidebar_width`. The Appearance section also shows both ([desktops.md](desktops.md), [sidebar.md](sidebar.md)).
- ⌘=, ⌘- and ⌘0 (`ui.increaseFontSize`, `ui.decreaseFontSize`, `ui.resetFontSize`) change the app's text size, which Appearance also shows.
- Palette (`native/crates/attn-app/src/palette/commands.rs`): `settings` ("Settings").
- Home screen: the gear, `home.settings` (`native/crates/attn-app/src/home/mod.rs`).
- In the dialog, `settings-dialog` (`native/crates/attn-app/src/settings/view.rs`, sections in `native/crates/attn-app/src/settings/sections.rs`): the nav buttons `settings.nav.<section>`, "Search settings" (`settings.search.field`), the dialog header's close button, `close`, a press outside the dialog on `settings-layer`, and Escape. The sections and the feature that owns each:
  - attn: Appearance (`general`, this file). Files and locations (`desktop`: [worktrees.md](worktrees.md), [notebook.md](notebook.md), [markdown.md](markdown.md), and the Projects Directory for [pull-requests.md](pull-requests.md)). Attention queue (`hygiene`: [queue-flow.md](queue-flow.md), and muted repositories and authors for [pull-requests.md](pull-requests.md)).
  - Agents: Agents and models (`agents`) and Models (`models`), [harnesses.md](harnesses.md). Background agents (`backgroundAgents`), session activity in [agent-state.md](agent-state.md) and headless compaction in [usage.md](usage.md). Delegation (`delegation`), [delegation.md](delegation.md).
  - Connectivity: GitHub hosts (`connectivity`), the hosts [pull-requests.md](pull-requests.md) uses. Outposts (`outposts`), [outposts.md](outposts.md).
  - Extensions: Plugins (`plugins`), [harnesses.md](harnesses.md).
  - System: Updates (`updates`), shown while Self-update is on: the install kind, the build and what is available, from `attn update status`'s facts. On any install but an outpost, the **Update attn** button, `settings.updates.start` (`native/crates/attn-app/src/settings/updates.rs`), sends `start_update`, as does the palette's `update-attn` command and the **Update** action on an `update_requested` notification (asked for with `attn update request`, whose `--reason` shows as the notification's Trigger, in the requester's profile only, one open notification per profile; **Decline** closes that profile's requests). A refused Update, Decline or Retry shows its refusal as a notice. Progress shows in the band under the title bar, `update-run`, a region named "attn update" whose value is the run's phase (`native/crates/attn-app/src/update_run.rs`). Experimental (`experimental`), each switch with its feature. Task runner (`backgroundTasks`) and Event bus (`eventBus`), [diagnostics.md](diagnostics.md). Analytics (`analytics`), [telemetry.md](telemetry.md).
- Where people see what agents do: a setting an agent changes with `attn settings set` shows in the open dialog as the control's new value. `theme`, `color_scheme`, `color_overrides` and `uiScale` repaint the open app, its terminals and its tile windows at once.
- Their own shell: `attn settings`, below.

### Agents

- `attn settings` (`cli/attn/src/commands/settings/`, `internal/catalog/content/settings.yaml`): `list` (with `--all`, `--features` and `--json`), `get <key>` and `set <key> <value>` (each with `--json`). When `agent_tools_enabled` is on, each is also the tool `settings_list`, `settings_get` or `settings_set`; a shell that shows no session may call them too. `set` to `""` restores the default of a key that has one, such as `theme` or `projects_directory`, or where the key's description allows it. A key family such as `default_model_<harness>` is set through a concrete key, such as `default_model_claude`.
- Prompts: the attn map tells agents to read and change daemon settings with `attn settings list`, `get` and `set`, (`internal/prompts/content/session/attn-map/settings.md`).

## Driving it

Preconditions:

- Use the baseline instance with its app, launched as [../SKILL.md](../SKILL.md) says, then `bridge wait '{"target":{"key":"home"}}'`. On Linux, run every command that reaches the app inside one `xvfb-run -a -s '-noreset -screen 0 1600x1000x24' bash -c '…'`. The shell steps need only the daemon. The chords below are the macOS ones. On Linux, Settings is `mod+shift+,`, the palette `mod+alt+k` and text size `mod+shift+=`, `mod+shift+-` and `mod+shift+0`.
- For the Update attn steps, install the instance from a worktree with nothing uncommitted: Update attn rebuilds a named instance from the worktree it was installed from.
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
- **Refusals.** Each of these commands exits 1 with `settings set: daemon error: ` and the reason: `./attn settings set theme purple` (`invalid theme: purple (must be dark, light, or system)`), `./attn settings set pty_backend_mode worker` (`setting pty_backend_mode is read-only`), `./attn settings set unknown.key value` (`unknown setting: unknown.key`) and `./attn settings set "default_model_<harness>" x` (`requires a concrete key; replace its placeholders`). `./attn settings set theme` exits 2 with the usage.
- **Open and close with the shortcut.** Run `bridge key '{"chord":"mod+,"}'`: `bridge wait '{"target":{"key":"settings-dialog"}}'` answers, `bridge state '{"of":"settings"}'` shows `open: true`, and on the first open `settings.heading` reads "GitHub hosts" with `activeSection: "connectivity"`. Focus is on `settings-dialog` itself, with no caret in `settings.search.field`. `Tab` puts it in the search. Press the chord again: `bridge wait '{"target":{"key":"settings-dialog"},"gone":true}'` answers.
- **Open from the palette.** Press `mod+shift+k`, run `bridge type '{"text":"Settings"}'`, wait for `palette.row.command:settings` and press `Enter`: the dialog opens. Press `Escape`: it goes.
- **Open from the home screen.** Press `mod+shift+h` (`session.goToDashboard`), then `bridge press '{"target":{"key":"home.settings"}}'`, the gear: the dialog opens.
- **Change in the dialog, read from the shell.** Press `settings.nav.general`: `bridge state '{"of":"settings","expect":{"activeSection":"general"}}'` answers. Press `{"role":"radio-button","name":"Dark"}`. Re-read `./attn settings get theme` until it prints `dark`.
- **Color scheme.** With Appearance open, `settings.color-schemes` is a radio group named "Color scheme". Press `settings.color-scheme.catppuccin`: `./attn settings get color_scheme` prints `catppuccin`, and the app and its terminals repaint in Catppuccin Mocha. Run `./attn settings set color_scheme catppuccin-frappe`: the `Catppuccin Frappé` row is checked in `bridge tree` and the app repaints. Run `./attn settings set color_overrides '{"dark":{"ui":{"accent":"#ff00aa"}}}'`: `settings.color-overrides` shows "Custom colors" with `settings.color-overrides.reset`. Press the reset: `./attn settings get color_overrides` prints nothing and `settings.color-overrides` goes. Put the scheme back with `./attn settings set color_scheme ""`.
- **Text size from the keyboard.** Press `mod+=`: `./attn settings get uiScale` prints `1.1`, and `settings.scale.app.value` reads `110%`. Press `mod+-` twice: the value reads `90%` and `uiScale` prints `0.9`. Press `mod+0`: the value reads `100%` and `uiScale` prints `1`.
- **Search.** `bridge type '{"target":{"key":"settings.search.field"},"text":"sweep"}'`: `bridge state '{"of":"settings"}'` shows `search: "sweep"`, and the only `settings.nav.<section>` left is `settings.nav.desktop`, "Files and locations". Clear the field with `Backspace`: `settings.nav.general` comes back.
- **A change from the shell shows in the dialog.** Press `settings.nav.desktop`. Run `./attn settings set worktree_sweep_enabled false`: in `bridge tree`, `settings.switch.worktree_sweep_enabled` reads `unchecked`. Press the switch: `./attn settings get worktree_sweep_enabled` prints `true`.
- **Autosave, a refusal and Retry.** Press `settings.setting.projects_directory`, press `mod+a`, type `relative/dir` and press `Enter`. `bridge wait '{"target":{"key":"settings.refusal.projects_directory"}}'` answers with the reason, `projects directory must be an absolute path`, and `settings.retry.projects_directory` is beside it. `./attn settings get projects_directory` still prints the old value. The daemon log holds one `Setting projects_directory` line, and pressing another field adds none. Press the field, press `mod+a`, type the proof folder's path, written out, and press `Enter`: `settings.saved.projects_directory` answers and `./attn settings get projects_directory` prints the path.
- **An agent changes a setting.** Run `wsctl add-session --cwd $A --agent claude --label settings-agent --id settings-agent --place`, find its terminal in `wsctl list`, and run `wsctl input --id <terminal> --text "sweep off" --enter`. `./attn agent peek settings-agent` shows `worktree_sweep_enabled = false`, and in the open dialog `settings.switch.worktree_sweep_enabled` reads `unchecked`. `wsctl input --id <terminal> --text "read theme" --enter` shows `dark` in peek. Put the sweep back with `./attn settings set worktree_sweep_enabled true`.
- **GitHub hosts.** With the search empty, press `settings.nav.connectivity`. `settings.heading` reads "GitHub hosts", and `bridge tree` lists the hosts attn found.
- **Experimental.** Press `settings.nav.experimental`. `bridge tree` lists the opt-in features by group, `settings.feature-group.<group>`, Outposts among them, each group with how many are on. Each feature's note line, `settings.feature-note.<key>`, says when a change starts if it does not apply at once. Run `./attn settings list --features`: it prints the same features with their state. Drive each switch in its feature's file. Drive Outposts in [outposts.md](outposts.md).
- **Agents and models.** Drive them in [harnesses.md](harnesses.md).
- **Close.** Press `close` (the button in the dialog header): the dialog goes, unless a field still holds a refused value, which keeps the dialog open until the field takes a valid one. Open it again and press `settings-layer` at its left edge, `{"target":{"key":"settings-layer"},"at":[0.005,0.5]}`: the dialog goes. Open it once more and press `Escape`: it goes, and `bridge state '{"of":"settings"}'` shows `saving: false`.
- **Update attn.** Run `./attn settings set self_update_enabled true`. Press `settings.nav.updates`, then `settings.updates.start`, or press `mod+shift+k`, type `update attn` and press `Enter`. `bridge wait '{"target":{"key":"update-run"}}'` answers, and `update-run.text` reads the phase, such as "Building the update.". A wait gives up after 30 s, so repeat it. Read `bridge tree` as the run moves through staging and applying.
- **The app restarts.** Applying quits the app, and the update opens it again. `bridge ping` reaches the new app. On the new app, `update-run` shows the run once it reconnects.
- **The run ends well.** When `./attn update status` shows the run done, `update-run.text` reads the outcome, such as "attn is now running 0.15.0 (…)", and `update-run.countdown` reads "Closing in Ns". The band goes after five seconds, and does not come back for that run after `./attn instance stop-app` and a relaunch. To reach this step without a build, run Update attn on an instance already at its worktree's commit: the run ends at once with "Already running …".
- **A refusal.** Start the daemon from the checkout with `./attn daemon ensure` instead of the installed app, then run Update attn: `notice.text` reads the refusal, "attn cannot update itself here: … is not inside this install's app …".
- **Only where attn can update.** Run `./attn settings set self_update_enabled false`: the palette no longer offers Update attn. Turn the setting back on.
- **Home and machine settings on an outpost.** With an outpost paired, as [outposts.md](outposts.md) describes, run on the outpost `attn settings list --all`: home settings such as `theme` carry `set at the home`. `attn settings set theme system` there prints `theme = system`, and the home's `./attn settings get theme` prints `system`. `attn settings set worktree_sweep_enabled false` there leaves the home's value alone. With the home unreachable, the outpost's home-setting write fails and names the home as unreachable. On the baseline, which has no outpost, this step fails as Gotchas says.
- **Proof.** In `$P`, keep each `./attn settings` command's output with its exit code. Keep `bridge state '{"of":"settings"}'` after each open, section change, search and close, `./attn agent peek settings-agent` after each turn, `bridge tree` at each update phase, and `./attn update status` before and after. Capture `bridge shot '{"path":"<P>/settings-dialog.png"}'` with the dialog open on Files and locations, where `<P>` is the folder's path written out. Name each file for its way in, such as `settings-shortcut-state.json`. The `native-settings` scenario runs the dialog steps, and `native-self-update` runs Update attn from Settings and the palette.

## Contexts

- **Durability across restarts**: settings are daemon records and come back after any restart. The dialog says "Saved" only once the daemon has kept the value, and a refused change leaves the old value in place. An outpost reads its copy of the home settings while the home is unreachable.
- **Linux**: `attn settings` works the same where the daemon and CLI run on Linux. `ui.openSettings` is Ctrl+Shift+, there, and the text size keys are Ctrl+Shift+=, Ctrl+Shift+- and Ctrl+Shift+0 (the `LINUX` table in `native/crates/attn-app/src/keymap.rs`).
- **Outposts**: the home decides home settings. Each outpost keeps a read-only copy, and `attn settings set` on an outpost changes a home setting at the home, except `outposts_enabled`, which an outpost refuses because it reaches the home over that bridge. Machine settings (harness executables, the terminal backend, the worktree sweep) stay on the machine that sets them. Profile settings (`notebook.root`, `pull_requests.own_comments`) live at the home: on an outpost, `attn settings get notebook.root` exits 1 naming the home, `attn settings list` leaves it out, and `notebook.root.effective` names the outpost copy. `attn settings` acts on the session's profile inside attn and takes `--profile <name|id>` outside it when several profiles exist. After `attn enrollment leave`, the outpost uses the values it had before pairing.
- **Profiles**: every setting applies across all profiles except `notebook.root`, its read-only companion `notebook.root.effective`, and `pull_requests.own_comments`, which belong to one profile, and `attn settings list` says `home` or `this machine` for the rest. Flow (`queue_mode_enabled`) is one of the shared settings: it is one setting for all profiles.
- **Harnesses**: each harness has its own machine setting for its executable (`<harness>_executable`) and home families for its defaults (`default_model_<harness>`, `default_effort_<harness>`). See [harnesses.md](harnesses.md) for them.
- **Keyboard and native input**: ⌘, opens and closes the dialog, Escape closes it, and ⌘= ⌘- ⌘0 resize text from anywhere. No macOS menu item claims ⌘,. The dialog opens with focus on itself, and Tab reaches the search.

## Tests

- Wire: `internal/daemon/agent_tools_settings_wire_test.go` (each `settings_<verb>` tool, and a second profile's caller), `internal/daemon/settings_ack_wire_test.go`, `internal/daemon/settings_validation_wire_test.go`, `native/crates/attn-app/tests/wire/settings.rs` and `native/crates/attn-app/tests/wire/settings/*.rs` (the dialog and each section, including the scheme list and Reset), `native/crates/attn-app/tests/wire/colors.rs` (the window and terminals repaint on a scheme or override, and a saved scheme attn no longer has draws attn), `native/crates/attn-app/tests/wire/update_run.rs` (the update band and Update attn).
- Stack: `cmd/attnd/update_run_stack_test.go`, `cmd/attnd/update_release_stack_test.go`, `cmd/attnd/settings_stack_test.go`, `cmd/attnd/settings_outpost_stack_test.go`, `cmd/attnd/agent_tools_outpost_stack_test.go` (the settings tool from an outpost).
- Scenario: `native-settings`, `native-agent-settings`, `native-self-update`.

## Gotchas

- The dialog first opens on GitHub hosts (`connectivity`), not Appearance, and keeps the last section and the last search while the app runs. A search that leaves a section out hides its nav button: when `bridge state '{"of":"settings"}'` shows a `search`, clear it before pressing a `settings.nav.<section>` button.
- The Outposts, Phones and Analytics sections are always in the nav. While `outposts_enabled`, `phone_access_enabled` or `analytics.enabled` is off, `settings.nav-off.<section>` marks the section and the page below its head is dimmed. The feature's switch, `settings.feature.<key>`, sits in the page head beside the title.
- `""` restores the default of a true or false setting, and of another key where its description says so. `attn settings set theme ""` is refused as an invalid theme.
- `attn settings list` prints `profile <name>` as the scope of `notebook.root` and `notebook.root.effective` (under `--all`), and `home` or `this machine` for the rest. On an outpost, a home setting adds `set at the home`.
- The home and machine step needs a paired outpost, which the baseline lacks. `cmd/attnd/settings_outpost_stack_test.go` covers it.
- Working but wrong: the daemon accepts `uiScale` from 0.5 to 2.0, while the app uses only 0.7 to 1.5 and draws any other value at 100%.

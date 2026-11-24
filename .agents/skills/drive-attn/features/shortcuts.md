# Keyboard shortcuts

Every action has a shortcut, listed in a cheat sheet and rebindable. The cheat
sheet shows the keys by category. The editor lets the user rebind an action by
recording a combination or a two-step chord, warns when the keys belong to
another action, and resets or unbinds an action. It also lets the user choose which
shortcuts the sidebar's dock shows as key hints. Rebinds and the dock are
one daemon setting, so they follow the user to every window and survive
restarts.

## Sub-features

- `cheatsheet`: every shortcut by category, with its keys.
- `shortcut-editor`: rebind by recording keys, reset one, unbind one, or restore every default.
- `conflicts`: the editor asks to Reassign when another action holds the keys, and refuses keys that a required action holds.
- `protected`: Settings, Keyboard shortcuts and Quit can be rebound but never unbound.
- `chords`: a leader combination, then one key, within 600 ms.
- `dock`: the shortcuts the sidebar dock shows, in order, and the dock hidden or shown.
- `linux-bindings`: Linux defaults, which use Ctrl+Shift where macOS uses ⌘, or Ctrl+Alt where the macOS binding already has Shift. The carry and send keys are the exception: Ctrl+Alt+Shift+arrows.
- `keybindings-setting`: rebinds and the dock as the home setting `keybindings_config`, read and reset from a shell.

## How to get to it

### People

- ⌘/ (`ui.showShortcuts`, `native/crates/attn-app/src/keymap.rs`) opens and closes the cheat sheet, `shortcuts-dialog` (`native/crates/attn-app/src/cheat_sheet.rs`). The cheat sheet's "Edit shortcuts" button, `shortcuts.edit`, opens the editor.
- Palette (`native/crates/attn-app/src/palette/commands.rs`): `keyboard-shortcuts` ("Keyboard shortcuts") and `customize-shortcuts` ("Customize keyboard shortcuts").
- What's new: "View all shortcuts →", `whats-new.shortcuts`, on every step opens the cheat sheet (`native/crates/attn-app/src/whats_new/mod.rs`).
- The editor, `shortcut-editor-dialog` (`native/crates/attn-app/src/shortcut_editor.rs`), has "Filter shortcuts", and per row the key button to record, "Record a chord", ☆ "Add to dock" or ★ "Remove from dock", ↺ to reset, ✕ to unbind, and the badges Customized, Required and Needs terminal. It also has a Dock section with "Move <label> up", "Move <label> down" and a remove button per docked shortcut, and "Restore Defaults".
- The sidebar's dock: chips for the docked shortcuts, each with its keys, and "Hide dock" or "Show dock", `sidebar.dock.toggle` (`native/crates/attn-app/src/sidebar/dock.rs`). See [sidebar.md](sidebar.md) for the sidebar.
- After a chord's leader, a hint shows "<keys> then…" until the second key or the timeout (`native/crates/attn-app/src/shell.rs`).
- Their own shell: `attn settings get keybindings_config` reads the rebinds and the dock as JSON, and `attn settings set keybindings_config ""` restores every default ([settings.md](settings.md)).

### Agents

None as a verb. An agent can only read or reset `keybindings_config` as a setting, see [settings.md](settings.md). The Chief's onboarding prompt (`internal/prompts/content/skills/attn/references/onboarding.md`) names ⌘K, ⇧⌘K, ⌘, and ⌘/, points the user to "Customize keyboard shortcuts" and has the Chief read `attn settings get keybindings_config`.

## Driving it

The editor's recorder hears only physical keys, and `bridge key` sends logical ones, so record with `xdotool` on Linux, and with real keys in the macOS VM (`scripts/vm`), where the chords are the Mac ones: Cmd+/ opens the cheat sheet. The steps below use the Linux keys.

Preconditions:

- Use the baseline instance with its app, launched as [../SKILL.md](../SKILL.md) says, then `bridge wait '{"target":{"key":"home"}}'`. On Linux, run every command that reaches the app inside one `xvfb-run -a -s '-noreset -screen 0 1600x1000x24' bash -c '…'`.
- Run `./attn settings set keybindings_config ""`.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-shortcuts.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- Send physical keys to the main window: take its `id` from `bridge native_windows` where `label` is `main`, and run `xdotool windowfocus --sync <id> key <keys>`.

- **Read a binding.** `./attn settings get keybindings_config` prints an empty line on a fresh instance.
- **The cheat sheet with its shortcut.** Send `ctrl+shift+slash`: `bridge wait '{"target":{"key":"shortcuts-dialog"}}'` answers, and `bridge find '{"target":{"key":"shortcut.ui.showShortcuts"}}'` reads "Keyboard shortcuts, Ctrl+Shift+/". Each row is keyed `shortcut.<action id>` and named for its action and keys. The rows scroll in `shortcuts.list`: `bridge scroll '{"target":{"key":"shortcuts.list"},"dy":5000}'` or sending `End` brings the last App row, `shortcut.app.quit`, inside the list's bounds, and sending `Home` takes the list back to the top. Send `ctrl+shift+slash` again: `bridge wait '{"target":{"key":"shortcuts-dialog"},"gone":true}'` answers.
- **The cheat sheet from the palette.** Press `mod+alt+k`, run `bridge type '{"text":"Keyboard shortcuts"}'`, wait for `palette.row.command:keyboard-shortcuts` and press `Enter`: `shortcuts-dialog` answers. Press `Escape`.
- **The cheat sheet from What's new.** Press `mod+alt+k`, type `whats new` and press `Enter`: `whats-new-dialog` shows. Press `whats-new.shortcuts`: the tour closes and `shortcuts-dialog` answers ([home.md](home.md)).
- **The editor from the cheat sheet.** With the cheat sheet open, press `shortcuts.edit`: `shortcut-editor-dialog` answers, the cheat sheet goes, and `shortcut-editor.filter` holds the keyboard. Press `Escape`.
- **The editor from the palette.** Press `mod+alt+k`, type `Customize keyboard shortcuts`, wait for `palette.row.command:customize-shortcuts` and press `Enter`: `shortcut-editor-dialog` answers.
- **Rebind.** `bridge type '{"target":{"key":"shortcut-editor.filter"},"text":"Keyboard shortcuts"}'`, press `shortcut-editor.keys.ui.showShortcuts` and send `ctrl+shift+y`. `shortcut-editor.badge.ui.showShortcuts.customized` and `shortcut-editor.reset.ui.showShortcuts` answer, and `./attn settings get keybindings_config` prints JSON whose `overrides` holds `ui.showShortcuts`. Press `shortcut-editor.done`, then send `ctrl+shift+y`: the cheat sheet opens and its row reads "Keyboard shortcuts, Ctrl+Shift+Y". Close it and send `ctrl+shift+slash`: `shortcuts-dialog` stays absent.
- **Conflicts.** Open the editor, filter to `Keyboard shortcuts`, press the key button and send `ctrl+alt+b`: `shortcut-editor.pending.text` names “Toggle sidebar”, with `shortcut-editor.reassign` and `shortcut-editor.cancel` beside it. Press Cancel: `shortcut-editor.pending` goes and `keybindings_config` is unchanged. Press the key button and send `ctrl+shift+comma`: `bridge wait '{"target":{"role":"alert"}}'` reads "Ctrl+Shift+, is reserved by “Settings” and can’t be reassigned." Press `shortcut-editor.filter` to stop recording.
- **A chord.** Press `shortcut-editor.chord.ui.showShortcuts` and send `ctrl+shift+y`: `shortcut-editor.then.lead` answers. Send `j`: `keybindings_config` holds a `then` for `ui.showShortcuts`. Close the editor and send `ctrl+shift+y j` in one `xdotool` call, so both keys land within the chord's window: the cheat sheet opens.
- **Unbind and reset.** Filter to `Toggle sidebar` and press `shortcut-editor.unbind.session.toggleSidebar`: `overrides` holds `session.toggleSidebar` as `null`, and `sidebar.dock.chip.session.toggleSidebar` goes. Press `shortcut-editor.reset.session.toggleSidebar`: the override goes and the chip is back.
- **The dock.** With an empty filter, `shortcut-editor.dock.list` lists the docked shortcuts, `shortcut-editor.dock.terminal.splitVertical` first. Press `shortcut-editor.dock.down.terminal.splitVertical`: `keybindings_config`'s `dock.items` holds `terminal.splitVertical` second. Press `shortcut-editor.dock.up.terminal.splitVertical`: it is first again. Press `shortcut-editor.dock.remove.session.toggleSidebar`: it leaves `dock.items` and the sidebar's `sidebar.dock.chip.session.toggleSidebar` goes. Press `shortcut-editor.dock-pin.session.toggleSidebar`, the row's star, named "Add to dock": the chip is back, last, and the star reads "Remove from dock". Close the editor. Press `sidebar.dock.toggle`, named "Hide dock": it reads "Show dock" and `keybindings_config` holds `"collapsed":true`. Press it again to show the dock.
- **Restore Defaults.** Rebind any row, then press `shortcut-editor.restore`: no `.badge.<id>.customized` is left, `keybindings_config` holds empty `overrides`, and `dock.items` is the default dock again. Send `ctrl+shift+slash`: the cheat sheet opens.
- **Restore from the shell.** `./attn settings set keybindings_config ""` prints `keybindings_config = ` and the default keys work again.
- **Proof.** In `$P`, keep `./attn settings get keybindings_config` after each change, with its exit code, `bridge find` of the cheat sheet row before and after the rebind, the pending text and the refusal, and `bridge shot '{"path":"<P>/shortcut-editor.png"}'` with the editor open on the customized row, `<P>` written out. Name each file for its way in, such as `shortcuts-palette-editor.png`. The `native-shortcut-editor` scenario runs these steps on Linux.

## Contexts

- **Durability across restarts**: rebinds and the dock live in the daemon's `keybindings_config` and come back after any restart, in every window.
- **Linux**: the `LINUX` table in `native/crates/attn-app/src/keymap.rs` holds the Linux defaults. Plain Ctrl and a letter belong to the shell, so app actions use Ctrl+Shift, or Ctrl+Alt where the macOS binding already has Shift: `ui.showShortcuts` is Ctrl+Shift+/ there. The editor stores a recorded ⌘ as the accelerator. On Linux the accelerator is Ctrl.
- **Profiles**: one set of shortcuts and one dock serve every profile.
- **Keyboard and native input**: on macOS the app adds File menu items Stop Countdown, Close Pane, Pop Out or Bring Back Tile and Quick Capture (`native/crates/attn-app/src/lib.rs`). Stop Countdown carries the keys `session.cancelCountdown` is bound to, and the window hears them first, so a rebind of it works. The app keeps ⌘W from Window > Close. While a row records, every app shortcut pauses, so Ctrl+Shift+, records instead of opening Settings. A focused field keeps `terminal.toggleZoom`, `terminal.find`, `session.prev`, `session.next`, `session.details` and the send annotations keys for itself, and on macOS ⌘B too. On macOS the editor's shortcut rows refuse Control as a modifier, but its section for Quick Capture's system-wide shortcut accepts Control, and the default is ⌃⌥⌘Space ([quick-capture.md](quick-capture.md)).
- **Other keyboard layouts**: a stored binding with Shift, or with ⌥ on macOS, runs on the physical key its `code` names, because those layers type other characters: ⌥8 is `[` on a Swedish keyboard and ⌥5 is `[` on a German one. Other bindings match the character, or the physical key when the character has no key name.
- **Compatibility across versions**: `board.open` keeps its id, so saved rebinds of the garden's key still apply. Rebinds saved under the desktop switch keys' earlier ids load under `desktop.select1` to `desktop.select9`, and the app drops ids that no longer exist. The app reads the rebinds the Tauri app saved.
- **Flow**: ⌘↑ and ⌘↓ (`session.prev`, `session.next`) move between desktops in desktop flow and between agents in the queue in queue flow. The cheat sheet notes both.

## Tests

- Wire: `native/crates/attn-app/tests/wire/keys.rs`, `native/crates/attn-app/tests/wire/shortcut_editor.rs`.
- Scenario: `native-shortcut-editor` (Linux only).

## Gotchas

- `bridge key` sends logical keys, which the editor's recorder does not hear. Record through `xdotool` or real keys. `bridge run` dispatches an action without its keys, so use it only as setup.
- A chord fires only when its second key comes within 600 ms of the leader. Send both keys in one `xdotool` call. The "then…" hint is gone before a later read arrives.
- The palette stays shut while Settings or the cheat sheet is open, so close them before a palette step.
- Chips for split, zoom and new-session shortcuts appear in the dock only while an agent is shown.
- Typing in the editor's filter while a row records would record the keys. Press the filter first. The press stops the recording.
- Working but wrong: the cheat sheet and the editor label the same actions differently in places, and keep older words for tiles and queue flow.

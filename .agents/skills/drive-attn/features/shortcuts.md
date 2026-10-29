# Keyboard shortcuts

The shortcut sheet lists keys by category. The editor rebinds keys, records
chords and chooses the sidebar dock's shortcuts. Bindings and dock choices
are shared across windows and survive restarts.

## Actions and ways in

### View shortcuts

⌘/ (`ui.showShortcuts`, `app/src/shortcuts/registry.ts`) opens or closes the sheet (`app/src/components/ShortcutsModal.tsx`); "Edit shortcuts" opens the editor. Palette commands `keyboard-shortcuts` ("Keyboard shortcuts") and `customize-shortcuts` ("Customize keyboard shortcuts") (`app/src/application/useAppCommands.tsx`) open them directly. "View all shortcuts →" in What's new also opens the sheet (`app/src/components/WhatsNewModal.tsx`).

### Customize keys and the dock

In the editor (`app/src/components/ShortcutEditorModal.tsx`), "Filter shortcuts" finds an action. Its row records a key or uses "Record a chord", resets or unbinds it, and offers "Add to dock" or "Remove from dock". Recording says "Press keys…"; Escape cancels (`app/src/components/KeyCaptureInput.tsx`). Badges mark Customized, Required, Menu bar and Needs terminal. "Restore Defaults" resets all bindings. A conflicting binding asks to Reassign; keys held by a required action are refused. Settings, Keyboard shortcuts and Quit can be rebound but never unbound.

A chord's second key follows its leader within 600 ms; "then…" shows until the key or timeout (`app/src/components/ChordLeaderHud.tsx`). The editor's Dock section offers "Move <label> up", "Move <label> down" and "Remove <label> from dock". The sidebar dock lets people click chips and use "Hide dock" or "Show dock" (`app/src/components/SidebarChrome.tsx`; [sidebar.md](sidebar.md)).

People and agents can read bindings and the dock as JSON with `attn settings get keybindings_config` and reset them with `attn settings set keybindings_config ""` ([settings.md](settings.md)).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`), except where a step replays it.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-shortcuts.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.

- **Read a binding.** Run `bridge shortcut_binding '{"shortcutId":"ui.showShortcuts"}'`. It returns `binding` with `key: "/"` and `meta: true`. `./attn settings get keybindings_config` prints an empty line on a fresh instance.
- **The cheat sheet with its shortcut.** Press ⌘/: `bridge native_key '{"key":"/","modifiers":["command"]}'`, then `bridge dom_wait '{"selector":"#shortcuts-modal-title","timeoutMs":5000}'`. `bridge dom_text '{"selector":".shortcuts-body"}'` lists the categories, "App" among them, and their rows with keys, "Settings" and "Keyboard shortcuts" among the rows. Press ⌘/ again: `bridge dom_wait '{"selector":"#shortcuts-modal-title","absent":true,"timeoutMs":5000}'` matches.
- **The cheat sheet from the palette.** Run `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`, `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'`, `bridge native_text '{"text":"Keyboard shortcuts"}'` and `bridge native_key '{"key":"Enter"}'`. `#shortcuts-modal-title` appears. Press Escape.
- **The cheat sheet from What's new.** Open the palette, type `whats new` and press Enter; the title's apostrophe would break the single-quoted JSON, and the command matches through its keywords ([home.md](home.md)). Click `.whats-new-link` ("View all shortcuts →") natively: What's new closes and `#shortcuts-modal-title` appears.
- **The editor from the cheat sheet.** With the cheat sheet open, click `.shortcuts-edit` ("Edit shortcuts") natively. `bridge dom_wait '{"selector":"#shortcut-editor-title","timeoutMs":5000}'` matches and `#shortcuts-modal-title` is gone. Press Escape.
- **The editor from the palette.** Open the palette, type `Customize keyboard shortcuts` and press Enter. `bridge dom_wait '{"selector":"[aria-label=\"Filter shortcuts\"]","focused":true,"timeoutMs":5000}'` matches.
- **Rebind.** In the editor, type `Keyboard shortcuts` with `bridge native_text`: one row is left. Click `.shortcut-editor-row .key-capture-button` natively; `bridge dom_wait '{"selector":".key-capture--recording","timeoutMs":5000}'` matches and reads "Press keys…". Press ⌘⇧Y: `bridge native_key '{"key":"y","modifiers":["command","shift"]}'`. The row shows the Customized badge, `bridge shortcut_binding '{"shortcutId":"ui.showShortcuts"}'` returns a Y binding with `meta` and `shift`, and `./attn settings get keybindings_config` prints JSON whose `overrides` holds `ui.showShortcuts`. Press Escape to close the editor. Press ⌘⇧Y: the cheat sheet opens. Close it and press ⌘/: `#shortcuts-modal-title` stays absent.
- **Conflicts.** Open the editor, filter to `Keyboard shortcuts`, click the key button and press ⌘,: `bridge dom_text '{"selector":".shortcut-editor-row-error"}'` ends with `is reserved by “Settings” and can’t be reassigned.`. Click the key button again and press ⌘B: `bridge dom_text '{"selector":".shortcut-editor-reassign-text"}'` names “Toggle sidebar”, with Reassign and Cancel beside it. Click `.shortcut-editor-reassign .shortcut-editor-btn:not(.shortcut-editor-btn--primary)` ("Cancel") natively: `shortcut_binding` still returns the Y binding.
- **A chord.** Click `.shortcut-editor-row [aria-label="Record a chord"]` natively: the row reads "Press leader…". Press ⌘⇧Y, then `bridge native_key '{"key":"j"}'`. `bridge shortcut_binding '{"shortcutId":"ui.showShortcuts"}'` returns a `leader` with Y, `meta` and `shift`, and a `then` with `j`. Firing it is in Gotchas.
- **Unbind and reset.** Click `[aria-label="Filter shortcuts"]` natively, press ⌘A (`bridge native_key '{"key":"a","modifiers":["command"]}'`) and type `PRs drawer`, so its row is the only one. Click `.shortcut-editor-row [title="Unbind"]` natively. The row's key button reads "Unassigned", `bridge shortcut_binding '{"shortcutId":"dock.attention"}'` returns `binding: null`, and the "PRs" chip leaves `.sidebar-dock-items`. Click `.shortcut-editor-row [title^="Reset to"]` (↺) natively: `shortcut_binding` returns `p` with `meta` and `shift` again, and the chip is back.
- **Restore from the shell.** Close the editor and run `./attn settings set keybindings_config ""`. It prints `keybindings_config = `. `bridge shortcut_binding '{"shortcutId":"ui.showShortcuts"}'` returns `/` with `meta` again, and ⌘/ opens the cheat sheet.
- **The dock.** `bridge dom_text '{"selector":".sidebar-dock-items"}'` lists the default chips, "PRs" first. Open the editor with an empty filter and click `[aria-label="Move PRs drawer down"]` natively: `bridge dom_text '{"selector":".shortcut-editor-dock-list"}'` lists "PRs drawer" second, and `./attn settings get keybindings_config` shows `dock.items` with `dock.attention` second. The sidebar may still show "PRs" first, because the split chips ahead of it hide while no agent is shown. Click `[aria-label="Remove PRs drawer from dock"]` natively: "PRs" leaves `.sidebar-dock-items`, and `dock.items` no longer holds `dock.attention`. Type `PRs drawer` in the filter and click `.shortcut-editor-row [aria-label="Add to dock"]` natively: the chip is back, last. Close the editor and click `[aria-label="Hide dock"]` natively: `[aria-label="Show dock"]` replaces it and `keybindings_config` holds `"collapsed":true`. Click it to show the dock again.
- **Restore Defaults.** Rebind any row, then click `.shortcut-editor-footer .shortcut-editor-btn:not(.shortcut-editor-btn--primary)` ("Restore Defaults") natively. No row shows Customized, the dock holds its default chips in order, and `keybindings_config` holds empty `overrides`.
- **Linux bindings.** On a Linux app, `bridge shortcut_binding '{"shortcutId":"ui.showShortcuts"}'` returns `/` with `meta` and `shift`, where `meta` means Ctrl. Scenario `linux-shortcuts` drives them through xdotool; a Mac run cannot.
- **Proof.** In `$P`, keep `bridge shortcut_binding` before the rebind, after it, after the chord and after the restore; `./attn settings get keybindings_config` after each change, with its exit code; `bridge dom_text` of the row error and the reassign prompt; and `bridge capture_screenshot '{"path":"<P>/shortcut-editor.png"}'` with the editor open on the customized row, and `<P>/cheatsheet.png` with the cheat sheet open, where `<P>` is the folder's path written out. Put the way in in each file's name, such as `shortcuts-palette-editor.png`.

## Contexts

- **Durability across restarts**: rebinds and the dock live in the daemon's `keybindings_config` and come back after any restart, in every window.
- **Linux**: `LINUX_SHORTCUTS` holds the Linux defaults. Plain Ctrl and a letter belong to the shell, so app actions use Ctrl+Shift, or Ctrl+Alt where the macOS binding already has Shift: `ui.showShortcuts` is Ctrl+Shift+/ there. A recorded ⌘ is stored as the accelerator, which is Ctrl on Linux.
- **Profiles**: one set of shortcuts and one dock serve every profile.
- **Keyboard and native input**: macOS menu items take some keys before the app sees them. ⌘. belongs to the File menu's Stop Countdown, so `session.cancelCountdown` shows the Menu bar badge and cannot be rebound. ⌘W belongs to the File menu's item for closing the active tile, which closes it whatever `session.close` is bound to. Quit (⌘Q), Hide (⌘H) and Minimize (⌘M) stay with the menu. While a row records, every app shortcut pauses, so ⌘, records instead of opening Settings. A focused editor or input keeps ⌘F, ⌘↑, ⌘↓, ⌘B, ⌘⇧Z and ⌘Enter for itself. On macOS the editor refuses Control as a modifier.
- **Compatibility across versions**: `board.open` keeps its id, so saved rebinds of the garden's key still apply. Rebinds saved under the desktop switch keys' earlier ids load under `desktop.select1` to `desktop.select9`, and ids that no longer exist are dropped.
- **Flow**: ⌘↑ and ⌘↓ (`session.prev`, `session.next`) move between desktops in desktop flow and between agents in the queue in queue flow; the cheat sheet notes both.

## Tests

- Stack: `app/e2e/keyboard-shortcuts.spec.ts`.
- Scenario: `session-creation-shortcuts`, `linux-shortcuts`.
- App: `app/src/App.keyboardShortcuts.test.tsx`, `app/src/App.shortcutCheatsheet.test.tsx`, `app/src/App.shortcutEditor.test.tsx`, `app/src/shortcuts/*.test.ts`.

## Gotchas

- What's new on a fresh app holds the navigation shortcuts and the palette until it is dismissed; only the app-wide keys, such as ⌘/, ⌘, and the text size keys, still reach the app. The palette also stays shut while Settings or the cheat sheet is open, so close them before a palette step.
- A chord fires only when its second key comes within 600 ms of the leader. Each `bridge native_key` is its own process, so two calls usually miss the window, and the "then…" hint is gone before a read arrives. Prove a chord's recording here, and its firing through `app/src/App.shortcutEditor.test.tsx` or a scenario.
- `shortcut_binding` reports what the app resolved, not what a key does. Prove a rebind by pressing the new keys and the old ones.
- `dispatch_shortcut` and `open_shortcut_editor` skip native input; use them only as setup.
- Chips for split, zoom and new-session shortcuts appear in the dock only while an agent is shown.
- Typing in the editor's filter while a row records would record the keys; clicking the filter stops the recording first.
- Working but wrong: the editor accepts keys a macOS menu owns, such as ⌘M or ⌘H, and the action never fires. Rebinding `session.close` away from ⌘W leaves ⌘W closing tiles through the menu. The cheat sheet builds its rows once per app launch, so after a rebind it can show the old keys until the app relaunches. The cheat sheet and the editor label the same actions differently in places, and keep older words for tiles and queue flow.

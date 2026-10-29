# Quick capture

Send a message and attachments to the chief or crew from anywhere. attn
wakes an asleep crew member if needed and types the message into the
recipient's conversation.
The macOS panel keeps unsent drafts and shows recent messages. Quick capture
is experimental and off by default.

## Actions and ways in

### Enable and open

Turn on "Quick Capture" in Settings, Experimental (`[role="switch"][aria-label="Quick Capture"]`, `app/src/components/SettingsModalSections.tsx`). Open the panel with the global shortcut, ⌃⌥⌘Space in production and unbound on named instances (`app/src-tauri/core/src/quick_capture.rs`), or File → Quick Capture (`app/src-tauri/core/src/lib.rs`), which has no key equivalent. The menu is enabled only while quick capture is on.

Rebind or disable the global shortcut in the keyboard shortcuts editor's "Global Quick Capture shortcut" section (`app/src/components/QuickCaptureShortcutEditor.tsx`, `app/src/components/ShortcutEditorModal.tsx`). It is stored as `capture.shortcut`; an empty value turns it off.

### Compose and send

In the panel (`app/src/components/QuickCapture.tsx`), type in "Message" and pick "To: <mailbox>" (`[aria-label="Send to"]`, ⌘K). ⌘1 to ⌘4 pick the first four mailboxes. Enter sends; Shift+Enter inserts a line. Escape closes the mailbox list, then history, then the panel. Paste or drop files and images to attach them; previews appear in `app/src/components/QuickCaptureAttachmentPreview.tsx`. The draft and files survive hiding the panel and restarting the app.

A sent capture enters the mailbox's inbox. A ring types it as a user message without the 📬 line, waking an asleep member first without counting against the wake limit (`internal/daemon/quick_captures.go`; `docs/inbox.md`). "Recent messages" (`app/src/components/QuickCaptureHistory.tsx`) marks a message once retrieved; the chief's or member's terminal shows the typed message.

### Receive an attachment

The receiving agent saves a file with `attn agent attachment <capture-id> <attachment-id> --out <path>` (`cmd/attn/agent_attachment.go`). Agents launched with quick capture on receive its guidance (`internal/prompts/content/session/quick-capture.md`); the message and file lines come from `session/quick-capture-item.md`, `quick-capture-file.md` and `quick-capture-file-saved.md`.

## Driving it

Preconditions:

- macOS. The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`).
- A crew member, Fern, made as [crew.md](crew.md)'s preconditions say, with `./attn crew set fern --cwd $C --agent claude`, where `$C` holds this fixture as `$C/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "sent through Quick Capture", "actions": [
    {"type": "reply", "text": "got the capture"}]}
]}
```

- A proof folder, `P=$(mktemp -d /tmp/drive-attn-quick-capture.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- The panel is a window of its own, `capture`. `bridge native_key`, `native_text` and `native_mouse` reach it with `"window":"capture"` in their JSON. `bridge capture_state`, `capture_binding` and `capture_dismiss` read and drive it.
- The repository's input driver (`app/scripts/real-app-harness/InputDriver.swift`) posts keys to the whole system, as the `quick-capture` scenario does to press the global shortcut. Add it to the env file as a function:

```bash
cat >> $DRIVE_ENV <<'EOF'
input_driver() { node --input-type=module -e "import { createWindowDriver } from './app/scripts/real-app-harness/platform.mjs'; console.log(await createWindowDriver().runInputDriverCapture(process.argv.slice(1)))" "$@"; }
EOF
```

- The input driver compiles on its first call and needs macOS Accessibility permission, like the scenarios: `input_driver permissions` prints `"accessibility":true`. Without it, a key fails naming `Accessibility permission is required for the real app harness input driver.` The display must be awake and unlocked.

- **Turn it on.** Press ⌘, (`ui.openSettings`), click `[data-testid="settings-nav-experimental"]` natively, then click `[role="switch"][aria-label="Quick Capture"]` natively. It reads `Disable`, and `./attn settings get quick_capture_enabled` prints `true`. Close Settings with Escape.
- **Bind the shortcut.** Run `bridge capture_binding '{"binding":"Control+Alt+B"}'`. It answers `{"binding": "Control+Alt+B"}`, and `bridge capture_state` shows `binding: "Control+Alt+B"`. Run `bridge capture_binding '{"binding":"Shift+B"}'`: a binding without Command, Control or Option is refused with `Use Command, Control or Option with another key for a system-wide shortcut.`, and `bridge capture_state` still shows `Control+Alt+B`.
- **Open with the global shortcut.** Press the bound shortcut while another app is in front. Run `input_driver frontmost`: it prints the app in front, not the instance's (`./attn instance resolve --field bundleId`). Run `input_driver global_key --key b --modifiers control,option`. `bridge capture_state '{"visible":true}'` returns with `visible: true` and `focused: true`, and `input_driver frontmost` prints the same app as before: the panel opens over it and leaves it in front.
- **Open from the File menu.** File, Quick Capture opens the same panel. `bridge` cannot reach the menu bar, and no scenario drives this item; check it by hand and say so in the report.
- **Write and pick a mailbox.** With the panel open, run `bridge native_text '{"window":"capture","text":"Check the drive notes."}'`. Run `bridge native_key '{"window":"capture","key":"2","modifiers":["command"]}'`: the roster lists the chief first, then the profile's members, so ⌘2 picks Fern. `bridge capture_state '{"mailbox":"fern"}'` returns with `mailbox: "fern"` and `text: "Check the drive notes."`.
- **Send.** Run `bridge native_key '{"window":"capture","key":"Enter"}'`. `bridge capture_state` lists the message under `saved` with `mailbox: "fern"`. Fern was asleep, so attn wakes the member first: `./attn crew list` shows `awake`, and `./attn agent peek fern` shows the typed `Message from the user, sent through Quick Capture.` with `Check the drive notes.` and the reply `got the capture`.
- **Recent messages.** Read `controls.recent` from `bridge capture_state`, then run `bridge native_mouse '{"window":"capture","action":"click","x":<x>,"y":<y>}'` with those values. `bridge capture_state '{"view":"recent"}'` returns `recentRows` with the message's text. Escape closes the history.
- **Hide and keep the draft.** Type `bridge native_text '{"window":"capture","text":"Draft for later"}'`, then `bridge native_key '{"window":"capture","key":"Escape"}'`. `bridge capture_state '{"visible":false}'` returns. Open the panel again as above: `bridge capture_state` shows `text: "Draft for later"`. `bridge capture_dismiss` hides it from the harness when a step needs it closed.
- **A file for the agent.** Put an image on the clipboard, as ⌃⇧⌘4 and a selection does. Run `osascript -e "set the clipboard to (read (POSIX file \"$PWD/docs/banner.png\") as «class PNGf»)"`, open the panel as above, then run `bridge native_key '{"window":"capture","key":"v","modifiers":["command"]}'`. `bridge capture_state '{"attachmentCount":1,"settled":true}'` returns with one file. Send it. The typed message carries a line `attn agent attachment <capture-id> <attachment-id> --out <path>`; an agent's fixture that runs that line saves the file, and the history marks the message retrieved. A file dropped on the panel attaches the same way, but the drive has no app to drag one from: drop one from Finder by hand and say so in the report.
- **Delivery to the chief.** Make a chief as [crew.md](crew.md) does, open the panel, leave the mailbox on `chief` (⌘1) and send. Peek on the chief shows the typed message. Without a chief, the message waits for the next one.
- **Proof.** In `$P`, keep `bridge capture_state` after each step (its `latencyMs` times each opening), `input_driver frontmost` before and after each opening, `./attn crew list --json` and `./attn agent peek fern` after the send, and `./attn settings get quick_capture_enabled`. Put the way in in each file's name, such as `quick-capture-shortcut-state.json`.

## Contexts

- **Durability across restarts**: the draft, its files and the binding survive app restarts, and a sent message is a durable inbox item that waits for its mailbox's session.
- **Linux**: the panel, the global shortcut and the File menu item are macOS only; the `quick-capture` scenario skips Linux. Messages already in an inbox, and `attn agent attachment`, work where the daemon and CLI run on Linux.
- **Profiles**: drafts are kept per profile, and the panel offers the chief and the crew members of the profile the app shows. `attn agent attachment` reads files only from the session's profile; outside a session, `--profile` picks one when there are several.
- **Performance and idle cost**: the panel opens with the message field focused; the scenario records the time from the key press to focus in `opening-latency.json`. Nothing measures the hidden panel's idle cost; when a change touches it, check it with `docs/perf-testing.md`.
- **Harnesses**: the message is typed as the user's message in every harness, so harness auto-mode reviewers count an approval in it as the user's.
- **Keyboard and native input**: the global shortcut, Enter, Shift+Enter, ⌘K with arrows and Enter, ⌘1 to ⌘4 and Escape reach every action; the panel shares the app's text size shortcuts. The message field holds the keyboard while the panel shows, including after a click elsewhere in the panel.

## Tests

- Wire: `internal/daemon/quick_captures_wire_test.go`.
- Stack: `cmd/attn/quick_captures_stack_test.go`, `app/e2e/quick-capture-history.spec.ts`.
- Scenario: `quick-capture`.
- App: `app/src/App.quickCapture.test.tsx`.

## Gotchas

- Quick capture is off on a fresh instance. Until it is on, the shortcut does nothing and the File menu item is disabled. A named instance also starts with no shortcut, because only production defaults to ⌃⌥⌘Space: bind one before looking for the panel.
- `bridge` reaches only the app's own windows: it cannot press the global shortcut, open the menu bar or drag a file in from another app. The File menu item and a dropped file have no live recipe; the `quick-capture` scenario drops files from its own fixture app.
- `input_driver global_key` posts to the whole system. While quick capture is off or no shortcut is bound, ⌃⌥B lands in the app in front, such as the terminal you drive from. Turn quick capture on and bind the shortcut first.
- macOS refuses shortcuts the system already uses (`macOS already uses this system shortcut`), and a refused binding keeps the previous one.
- `bridge capture_state` waits for the expectations you pass, such as `{"visible":true}` or `{"mailbox":"fern"}`, and fails when they never hold. Call it without them for a plain read.
- Agents launched before quick capture turned on lack its guidance, but still get the typed messages.
- A capture is typed only after 30 seconds of quiet in the agent's terminal; `wsctl input` to that agent restarts the wait.
- Working but wrong: "Saved for <mailbox>" means attn stored the message, not that the agent saw it, and the history's retrieved mark says "not necessarily understood or acted upon". The proof is the typed text on the agent's screen.

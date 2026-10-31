# Quick capture

Send a message with attachments to the chief or crew from anywhere, with a
global shortcut, like macOS Quick Note. A small panel opens over whatever app
the user is in. They write a note, attach files or screenshots, pick the chief
or a crew member, and press Enter. attn types the message into that agent's
conversation as the user's message, waking an asleep member first. The
panel keeps a draft between openings and lists recent messages with whether
their agent has read them. Quick capture is an experimental setting, off by
default, and the panel is macOS only.

## Sub-features

- `capture-shortcut`: the global shortcut that opens the panel from any app, ⌃⌥⌘Space by default in production and unbound on a named instance, rebindable or off.
- `capture-panel`: the panel itself: the message, the mailbox picker, Enter to send, Escape to hide.
- `capture-mailbox`: send to the chief or one of the profile's crew members.
- `capture-files`: attach files and images by paste or drop; the agent saves each with `attn agent attachment`.
- `capture-draft`: the unsent text and files survive hiding the panel and restarting the app.
- `capture-history`: recent messages, each marked once its agent retrieved it.
- `capture-delivery`: the message lands as a user message in the mailbox's session, waking an asleep member.
- `capture-guidance`: what attn tells agents about messages from quick capture.

## How to get to it

### People

- The global shortcut, ⌃⌥⌘Space by default in production and unbound on a named instance, registered with the system by the app (`app/src-tauri/core/src/quick_capture.rs`). It is rebound in the keyboard shortcuts editor's "Global Quick Capture shortcut" section (`app/src/components/QuickCaptureShortcutEditor.tsx`, inside `app/src/components/ShortcutEditorModal.tsx`), and kept in the `capture.shortcut` setting, where empty turns it off.
- Native menu: File, Quick Capture (`app/src-tauri/core/src/lib.rs`), enabled while quick capture is on. It has no key equivalent.
- The panel (`app/src/components/QuickCapture.tsx`): the "Message" field, "To: <mailbox>" (`[aria-label="Send to"]`, ⌘K) with its list, ⌘1 to ⌘4 for the first four mailboxes, Enter to send, Shift+Enter for a new line, Escape to close the list, then history, then the panel. Dragging the panel's surface outside the message field and its controls moves it; in history, only the heading drags. Every opening centers it again on the screen with the pointer. "Recent messages" shows the history (`app/src/components/QuickCaptureHistory.tsx`), and attached files show as previews (`app/src/components/QuickCaptureAttachmentPreview.tsx`).
- Settings, Experimental: the "Quick Capture" switch (`[role="switch"][aria-label="Quick Capture"]`, `app/src/components/SettingsModalSections.tsx`).
- Where people see what agents do with it: the message appears in the chief's or member's terminal as typed text, and the history marks it once the agent retrieved it.

### Agents

- `attn agent attachment <capture-id> <attachment-id> --out <path>` saves a file the user attached (`cmd/attn/agent_attachment.go`). Agents never send a quick capture; they receive one.
- Prompts: the quick capture guidance in the session instructions of agents launched while it is on (`internal/prompts/content/session/quick-capture.md`); the typed message and its file lines (`session/quick-capture-item.md`, `quick-capture-file.md`, `quick-capture-file-saved.md`).

### Triggers

- A sent capture is a user message in the chosen mailbox's inbox: a ring types it at once, without the 📬 line, and an asleep member is woken first without counting against the wake limit (`internal/daemon/quick_captures.go`; the rules are in `docs/internal/inbox.md`).

## Driving it

Preconditions:

- macOS. The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`).
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-quick-capture.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- A crew member, Fern, made as [crew.md](crew.md)'s preconditions say, with `./attn crew set fern --cwd $C --agent claude`, where `$C` holds this fixture as `$C/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "attn agent attachment", "actions": [
    {"type": "capture", "name": "capture", "pattern": "attn agent attachment (\\S+)"},
    {"type": "capture", "name": "attachment", "pattern": "attn agent attachment \\S+ (\\S+)"},
    {"type": "attn", "args": ["agent", "attachment", "{{capture}}", "{{attachment}}", "--out", "<P written out>/received.png"]},
    {"type": "reply", "text": "got the capture"}]},
  {"includes": "sent through Quick Capture", "actions": [
    {"type": "capture", "name": "message", "pattern": "sent through Quick Capture[^\\n]*\\n([^\\n]+)"},
    {"type": "exec", "cmd": "printf", "args": ["got the capture: %s\\n", "{{message}}"]}]}
]}
```

- Replace `<P written out>` with the proof folder's absolute path. Fern starts asleep; if continuing the crew recipe, put the member to sleep before its next launch reads this fixture.
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
- **Open from the File menu.** Hide the panel with `bridge capture_dismiss`, then run `input_driver menu --bundle-id "$(./attn instance resolve --field bundleId)" --path "File>Quick Capture"`. `bridge capture_state '{"visible":true}'` returns with the same panel open. The input driver reaches the native menu through Accessibility.
- **Write and pick a mailbox.** With the panel open, run `bridge native_text '{"window":"capture","text":"Check the drive notes."}'`. Run `bridge native_key '{"window":"capture","key":"2","modifiers":["command"]}'`: the roster lists the chief first, then the profile's members, so ⌘2 picks Fern. `bridge capture_state '{"mailbox":"fern"}'` returns with `mailbox: "fern"` and `text: "Check the drive notes."`.
- **Send.** Run `bridge native_key '{"window":"capture","key":"Enter"}'`. `bridge capture_state` lists the message under `saved` with `mailbox: "fern"`. Fern was asleep, so attn wakes the member first: `./attn crew list` shows `awake`, and `./attn agent peek fern` shows `got the capture: Check the drive notes.`. The fixture echoes the received message, including when it arrives in the wake opening instead of being echoed as terminal input.
- **Recent messages.** Send hides the panel. Reopen it with `input_driver global_key --key b --modifiers control,option`, then wait with `bridge capture_state '{"visible":true}'`. Read `controls.recent` from `bridge capture_state`, then run `bridge native_mouse '{"window":"capture","action":"click","x":<x>,"y":<y>}'` with those values. `bridge capture_state '{"view":"recent"}'` returns `recentRows` with the message's text. Escape closes the history.
- **Move the panel.** With the panel open, `input_driver windowstack` lists its frame (the entry whose `id` is `input_driver windowid --window-title "Quick Capture"`). Drag its top edge with `input_driver drag --window-title "Quick Capture" --relative-x 0.5 --relative-y 0.02 --to-relative-x 0.3 --to-relative-y -0.21`, up and left, away from the parked main window. The frame moves by the pointer's travel and `bridge capture_state` still shows `focused: true`. While another app is in front, the first press only makes attn active and the drag works from the second, so click the panel first. macOS holds a dragged window where its edge meets another window's edge until the pointer pulls about 30px past it, so a path that brings the panel's edge to another window's edge comes up short. The drag moves the real pointer, so it runs only on a dedicated display; on a shared Mac, ask a person to drag it.
- **Hide and keep the draft.** Type `bridge native_text '{"window":"capture","text":"Draft for later"}'`, then `bridge native_key '{"window":"capture","key":"Escape"}'`. `bridge capture_state '{"visible":false}'` returns. Open the panel again as above: `bridge capture_state` shows `text: "Draft for later"`. `bridge capture_dismiss` hides it from the harness when a step needs it closed.
- **A file for the agent.** Put an image on the clipboard, as ⌃⇧⌘4 and a selection does. Run `osascript -e "set the clipboard to (read (POSIX file \"$PWD/docs/banner.png\") as «class PNGf»)"`, open the panel as above, then run `bridge native_key '{"window":"capture","key":"v","modifiers":["command"]}'`. `bridge capture_state '{"attachmentCount":1,"settled":true}'` returns with one file. Send resets the mailbox to chief, so select Fern again with `bridge native_key '{"window":"capture","key":"2","modifiers":["command"]}'` and check `bridge capture_state '{"mailbox":"fern"}'` before pressing Enter. The fixture runs the message's `attn agent attachment` command; `./attn agent peek fern` shows `Saved file to <P>/received.png`, and `file "$P/received.png"` identifies the PNG. Reopen the panel and its history to see the message marked retrieved. A file dropped on the panel attaches the same way, but the drive has no app to drag one from: drop one from Finder by hand and say so in the report.
- **Delivery to the chief.** Make a chief as [crew.md](crew.md) does, open the panel, leave the mailbox on `chief` (⌘1) and send. Peek on the chief shows the typed message. Without a chief, the message waits for the next one.
- **Proof.** In `$P`, keep `bridge capture_state` after each step (its `latencyMs` times each opening), `input_driver frontmost` before and after each opening, `./attn crew list --json` and `./attn agent peek fern` after the send, and `./attn settings get quick_capture_enabled`. Put the way in in each file's name, such as `quick-capture-shortcut-state.json`.

## Contexts

- **Durability across restarts**: the draft, its files and the binding survive app restarts, and a sent message is a durable inbox item that waits for its mailbox's session.
- **Linux**: the panel, the global shortcut and the File menu item are macOS only; the `quick-capture` scenario skips Linux. Messages already in an inbox, and `attn agent attachment`, work where the daemon and CLI run on Linux.
- **Profiles**: drafts are kept per profile, and the panel offers the chief and the crew members of the profile the app shows. `attn agent attachment` reads files only from the session's profile; outside a session, `--profile` picks one when there are several.
- **Performance and idle cost**: the panel opens with the message field focused; the scenario records the time from the key press to focus in `opening-latency.json`. Nothing measures the hidden panel's idle cost; when a change touches it, check it with `docs/internal/perf-testing.md`.
- **Harnesses**: the message is typed as the user's message in every harness, so harness auto-mode reviewers count an approval in it as the user's.
- **Keyboard and native input**: the global shortcut, Enter, Shift+Enter, ⌘K with arrows and Enter, ⌘1 to ⌘4 and Escape reach every action; the panel shares the app's text size shortcuts. The message field holds the keyboard while the panel shows, including after a click elsewhere in the panel.

## Tests

- Wire: `internal/daemon/quick_captures_wire_test.go`.
- Stack: `cmd/attn/quick_captures_stack_test.go`, `app/e2e/quick-capture-history.spec.ts`.
- Scenario: `quick-capture`.
- App: `app/src/App.quickCapture.test.tsx`.

## Gotchas

- Quick capture is off on a fresh instance. Until it is on, the shortcut does nothing and the File menu item is disabled. A named instance also starts with no shortcut, because only production defaults to ⌃⌥⌘Space: bind one before looking for the panel.
- Opening the panel leaves the other app in front, but a click or drag on the panel makes attn the active app. Check `input_driver frontmost` before the first press, not after it.
- `bridge` reaches only the app's own windows. The input driver presses the global shortcut and the File menu item. A dropped file still has no live recipe here; the `quick-capture` scenario drops files from its own fixture app.
- Send hides the panel and resets its mailbox to chief. Reopen before reading history and select Fern again before a later send intended for the member.
- The attachment turn must precede the generic Quick Capture reply in the fixture. The first matching turn wins; a reply alone never retrieves the file.
- Mock `reply` text is literal. The fixture uses `exec` with `printf` to echo its captured message; that action expands captures in its arguments.
- `input_driver global_key` posts to the whole system. While quick capture is off or no shortcut is bound, ⌃⌥B lands in the app in front, such as the terminal you drive from. Turn quick capture on and bind the shortcut first.
- macOS refuses shortcuts the system already uses (`macOS already uses this system shortcut`), and a refused binding keeps the previous one.
- `bridge capture_state` waits for the expectations you pass, such as `{"visible":true}` or `{"mailbox":"fern"}`, and fails when they never hold. Call it without them for a plain read.
- Agents launched before quick capture turned on lack its guidance, but still get the typed messages.
- A capture is typed only after 30 seconds of quiet in the agent's terminal; `wsctl input` to that agent restarts the wait.
- Working but wrong: "Saved for <mailbox>" means attn stored the message, not that the agent saw it, and the history's retrieved mark says "not necessarily understood or acted upon". The proof is the typed text on the agent's screen.

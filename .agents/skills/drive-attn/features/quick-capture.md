# Quick capture

Send a message with attachments to the chief, a crew member or any agent session,
from anywhere. The panel opens with a global shortcut, like macOS Quick Note. A small panel opens over whatever app
the user is in. They write a note, attach files or screenshots, pick the chief
or a crew member, and press Enter. attn types the message into that agent's
conversation as the user's message, starting a session for a member between sessions first. The
panel keeps a draft between openings. It also lists recent messages and shows
whether the agent has retrieved each message from its inbox. The panel is
macOS only. A paired program sends with `attn send` without the panel.

## Sub-features

- `capture-shortcut`: the global shortcut that opens the panel from any app, ⌃⌥⌘Space by default in production and unbound on a named instance, rebindable or off.
- `capture-panel`: the panel itself: the message, the mailbox picker, Enter to send, Escape to hide.
- `capture-mailbox`: send to the chief, one of the profile's crew members, or (with `attn send`) any agent session; a shell is refused.
- `capture-files`: attach files and images by paste or drop. The agent saves each file with `attn agent attachment`.
- `capture-draft`: the unsent text and files survive hiding the panel and restarting the app.
- `capture-history`: recent messages, each marked "Read" once its agent retrieved the message from its inbox, and "Sent" before and again if a Claude mod gives back the ring that carried it.
- `capture-delivery`: the message lands as user input in the mailbox's session, starting a session for a member between sessions.
- `capture-guidance`: what attn tells agents about messages from quick capture.
- `capture-send`: a paired program sends files and a message to any running session, local or on an outpost, with `attn send`. It lands like a panel capture, as user input.
- `capture-refine`: a paired program refines dictated text with `attn refine`, the refinement the app's dictation uses.
- `capture-senders`: pairing a program, the approval dialog that allows or denies it, and listing and removing paired senders.

## How to get to it

### People

- The global shortcut, ⌃⌥⌘Space by default in production and unbound on a named instance. The native app binds it once the panel's window runs (`native/crates/attn-app/src/quick_capture/shortcut.rs`, `quick_capture/mac.rs`), and refuses a chord another app or macOS holds, keeping the previous one. Rebind it in the keyboard shortcuts editor's "Quick Capture · system-wide" section (`shortcut-editor.capture` in `native/crates/attn-app/src/shortcut_editor.rs`): its recorder (`shortcut-editor.capture.keys`), Reset and Turn off. The `capture.shortcut` setting stores the binding, and an empty value turns the shortcut off.
- Native menu: File, Quick Capture (`native/crates/attn-app/src/lib.rs`). It has no key equivalent.
- The panel (`native/crates/attn-app/src/quick_capture/view.rs`, its keyboard shortcuts in `quick_capture/mod.rs`): the message field (`capture.message`), "To: <mailbox>" (`capture.to`, ⌘K) with its list (`capture.mailboxes`), ⌘1 to ⌘4 for the first four mailboxes, Enter to send, Shift+Enter for a new line, ⌘W or Escape to hide. Escape closes the list first, then the history, then the panel. Each time the panel opens, it centers on the screen that has the pointer. "Recent messages" (`capture.recent`) shows the history (`capture.history`), and attached files show as chips (`capture.files`) with a remove button each (`capture.file.<id>.remove`, "Remove <name>").
- The sender approval dialog: it opens in the Tauri app's main window whenever a program asks to pair, shows the program's name, the profile it will belong to and a 4-digit code, and offers Deny and Allow. The native app has no such dialog yet; pair with the Tauri app.
- **Settings → Paired senders** (`native/crates/attn-app/src/settings/senders.rs`): each paired program with when it paired and was last used, and Remove.
- Where people see what agents do with it: the message appears in the chief's or member's terminal as typed text, and the history marks it "Read" once the agent retrieved it.

### Programs

- `attn send pair --name <name> [--json]` asks the user to pair the program and waits for the approval dialog. It prints the code, then a token once (`cmd/attnd/send.go`, `internal/daemon/senders.go`).
- `attn send <session> --token-stdin [--file <path>]... [-m <text>] [--json]` uploads the files and sends the capture to that session. Without a paired sender's token, the daemon refuses.
- `attn send sessions --token-stdin [--json]` lists the live agent sessions of the sender's profile, never a shell, with `last_focused_at`, which `terminal_focus` records (`internal/daemon/senders.go`).
- `attn refine --token-stdin [--duration-ms <n>] [--format plain|markdown] [--json]` refines the transcript after the token line and prints the refined text; past the limit, `--json` carries `limit_ms`. Stopping it cancels the harness (`cli/attn/src/commands/refine.rs`, `handleSenderRefine` in `internal/daemon/senders.go`). Tests: `internal/daemon/sender_refine_wire_test.go`, `cmd/attnd/refine_stack_test.go`.
- `attn send senders [--profile <name|id>] [--json]` and `attn send unpair <name|id> [--profile <name|id>]` list and remove one profile's paired senders.

### Agents

- `attn agent attachment <capture-id> <attachment-id> --out <path>` saves a file the user attached (`cmd/attnd/agent_attachment.go`). Agents never send a quick capture. They receive one.
- Prompts: the quick capture guidance in the session instructions of the chief and crew members, and of no other agent (`internal/prompts/content/session/quick-capture.md`). The typed message and its file lines are in `session/quick-capture-item.md`, `quick-capture-file.md`, and `quick-capture-file-saved.md`.

### Triggers

- A sent capture is user input in the chosen mailbox's inbox. A ring types the capture at once, without the 📬 line. If the member is between sessions, attn starts a session for it first, and that start does not count against the mail-start limit (`internal/daemon/quick_captures.go`). The rules are in `docs/internal/inbox.md`.

## Driving it

Preconditions:

- Run on macOS, in the macOS VM: the panel, the global shortcut, the File menu and sending need system input and focus, which a shared Mac forbids. Install the instance with `APP=native scripts/vm up`, or `make install INSTANCE=<name> APP=native`, and launch as [../SKILL.md](../SKILL.md) says for a native instance. `scripts/vm up` does not rebuild after a change under `native/` alone (seed `s-dwktd8`); run `make build-app INSTANCE=<name> APP=native` and `macvm install-app native/target/staged/attn-<name>.app` instead.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-quick-capture.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- Make a crew member, Fern, as [crew.md](crew.md)'s preconditions say: its home in the instance's `crew/fern` with a `CHARTER.md` before the daemon starts. Then run `./attn settings set codex_executable "$(node -p "require.resolve('./app/scripts/real-app-harness/mockAgent.mjs')")"` and `./attn crew set fern --cwd $C --agent codex`, where `$C` holds this fixture as `$C/.attn-mock-agent.json`:

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

- Replace `<P written out>` with the proof folder's absolute path. Fern starts between sessions. Run `./attn settings set codex_executable ""` when you are done.
- The panel is a window of its own with its own automation server. `bridge --capture` reaches it: `bridge --capture state '{"of":"capture"}'` reads the `capture` slice (visible, `nativeFocused`, `focused`, `captureId`, text, mailbox, view, files, `attachmentCount`, `imageCount`, `settled`, `fontScale`, error and `recentRows`), `bridge --capture run '{"name":"dismiss"}'` hides the panel, and `find`, `wait` and `press` take its element keys. The main window's `bridge run '{"name":"capture.shortcut=<binding>"}'` binds the shortcut, and `bridge state '{"of":"capture-shortcut"}'` reads `{binding, error}`. `docs/internal/native-app.md` lists the actions.
- The repository's input driver (`app/scripts/real-app-harness/InputDriver.swift`) posts keys to the whole system, as the `native-quick-capture` scenario does. In the VM, compile it in the guest, where it has Accessibility, and call it as `input_driver`. Its key names are lowercase: `escape`, `enter`, `arrowdown`, `a`. It has no Backspace.
- A native click on the panel lands on its window, so aim it at an element: read `rect` of the element and of `{"key":"capture"}` with `bridge --capture find`, and click `input_driver --bundle-id <app> click --window-title "Quick Capture" --relative-x <x> --relative-y <y>` at the element's center as a fraction of the panel.

- **Bind the shortcut from the shell.** Run `./attn settings set capture.shortcut Control+Alt+KeyB`. `bridge state '{"of":"capture-shortcut","expect":{"binding":"Control+Alt+KeyB"}}'` returns it. `./attn settings set capture.shortcut ""` turns it off, and the slice shows `binding: ""`. `bridge run '{"name":"capture.shortcut=Shift+KeyB"}'` is refused with `Use Command, Control or Option with another key for a system-wide shortcut.`, and the binding stays.
- **Rebind in the shortcut editor.** Open the palette with `bridge key '{"chord":"mod+shift+k"}'`, `bridge type '{"text":"Customize keyboard shortcuts"}'` and `bridge key '{"chord":"enter"}'`. `bridge wait '{"target":{"key":"shortcut-editor.capture"}}'` finds the "Global Quick Capture shortcut" region. `bridge press '{"target":{"key":"shortcut-editor.capture.keys"}}'` starts recording. The recorder reads physical keys, which `bridge key` does not send, so bring attn to the front (`input_driver --bundle-id <app> activate`) and press `input_driver global_key --key j --modifiers control,option,command`. The capture-shortcut slice and `./attn settings get capture.shortcut` show `Super+Control+Alt+KeyJ`, and `shortcut-editor.capture.active` reads `Active: Super+Control+Alt+KeyJ`. `bridge press '{"target":{"key":"shortcut-editor.capture.off"}}'` turns it off and empties the setting. Bind Control+Alt+KeyB again for the steps below and close the editor with Escape.
- **Open with the global shortcut.** Bring another app to the front and note `input_driver frontmost`. Run `input_driver global_key --key b --modifiers control,option`. `bridge --capture state '{"of":"capture","expect":{"visible":true,"focused":true}}'` returns, and `input_driver frontmost` prints the same app as before: the panel opens over it and leaves it in front.
- **Hide with Escape.** `input_driver global_key --key escape`, then `bridge --capture state '{"of":"capture","expect":{"visible":false}}'`. The app that was in front stays there.
- **Open from the File menu.** `input_driver menu --bundle-id <app> --path "File>Quick Capture"`. The slice shows `visible: true`. The menu makes attn the active app.
- **Write.** Open the panel with the shortcut and type with `input_driver global_text --text "Check the drive notes."`. The slice shows the text, and ⌘A before typing replaces a draft.
- **Pick a mailbox with ⌘1 to ⌘4.** The roster lists the chief first, then the profile's members. `input_driver global_key --key 2 --modifiers command`: the slice shows `mailbox: "fern"`. ⌘1 picks `chief`. ⌘3 and ⌘4 do nothing while the roster has two mailboxes.
- **Pick with ⌘K.** `input_driver global_key --key k --modifiers command` opens the list: `bridge --capture wait '{"target":{"key":"capture.mailboxes"}}'` finds it, with Chief ⌘1 and Fern ⌘2. ↓ (`arrowdown`) and Enter pick Fern and close the list.
- **Paste a file.** `osascript -e "set the clipboard to (read (POSIX file \"$PWD/docs/banner.png\") as «class PNGf»)"`, then `input_driver global_key --key v --modifiers command`. The slice shows `attachmentCount: 1`, `imageCount: 1`, `settled: true`.
- **Drop a file.** The drop needs an app to drag from: launch the `CaptureFixture.app` a `native-quick-capture` run built, with `CAPTURE_FIXTURE_DIR` set to a folder, wait for `fixture-ready` there, bring it to the front, open the panel and run `input_driver --bundle-id <fixture> drag_between --relative-x 0.1 --text <app>`. The slice counts one more image.
- **Send.** The drag leaves the fixture in front, and the panel takes the keyboard back: the slice shows `nativeFocused: true` and `focused: true`. Press `input_driver global_key --key enter`. The slice shows `visible: false` and `attachmentCount: 0`. Fern was between sessions, so attn starts a session first: `./attn crew list` shows Fern in session, and `./attn agent peek fern` shows `Saved file to <P>/received.png` and `got the capture`. `file "$P/received.png"` identifies the PNG.
- **Recent messages.** Reopen the panel and `bridge --capture press '{"target":{"key":"capture.recent"}}'`. `bridge --capture state '{"of":"capture","expect":{"view":"recent"},"includes":"Check the drive notes."}'` returns `recentRows` with the message, its `captureId` and `read`. Escape goes back to compose.
- **Restart with a draft.** ⌘A, type `Draft for later`, Escape. Quit the app (`osascript -e 'quit app "attn-<name>"'`) and launch it again. The capture-shortcut slice shows Control+Alt+KeyB again, and the reopened panel shows `text: "Draft for later"`.
- **Delivery to the chief, guidance at launch, and pairing a program.** These run as before through the CLI and the daemon: send with the Chief picked (⌘1) and peek on the Chief; `grep -c "The user can send you messages from Quick Capture" $C/.attn-mock-agent/*.jsonl` prints 1 for Fern's session and 0 for a plain agent's; `./attn send pair`, `send`, `sessions`, `senders` and `unpair` as [Programs](#programs) lists them. Pairing needs the Tauri app's approval dialog.
- **Proof.** In `$P`, keep each step's commands with their output and a screenshot of the guest's screen (`screencapture -x` in the guest; `scripts/vm shot` captures only the main window). Keep `input_driver frontmost` before and after each opening, `./attn crew list` and `./attn agent peek fern` after the send. Name each file for its way in, such as `04-global-shortcut.txt`.

## Contexts

- **Durability across restarts**: the draft, its files and the binding survive app restarts, and a sent message is a durable inbox item that waits for its mailbox's session.
- **Linux**: the panel, the global shortcut and the File menu item are macOS only. The `quick-capture` and `native-quick-capture` scenarios skip Linux. Messages already in an inbox, and `attn agent attachment`, work where the daemon and CLI run on Linux.
- **Outposts**: `attn send` targets a session on an outpost by its id. That session's `attn agent attachment` forwards to the home daemon, which holds the files. Pairing and paired senders live on the home. Each paired sender belongs to the profile the app showed when the user allowed it.
- **Flow**: with queue flow on, a capture makes the run it starts or reaches the user's, so the agent's stop opens a turn with quiet notifications on (`TestAPairedSendersQuestionPutsTheAgentsReplyInTheQueue`). In the send recipe above, settle the agent first, then check `./attn agent list` shows it owed once it stops. A chief never owes a turn.
- **Profiles**: attn keeps drafts per profile, and the panel offers the chief and the crew members of the profile the app shows. `attn agent attachment` reads files only from the session's profile. Outside a session, `--profile` picks one when there are several.
- **Performance and idle cost**: the panel opens with the message field focused. The scenarios record the time from the key press to focus in `opening-latency.json`; in the VM the native panel took 45 ms, 97 ms median and 527 ms at most over 17 openings (2026-11-16). Nothing measures the hidden panel's idle cost. When a change touches the panel, check the panel with `docs/internal/perf-testing.md`.
- **Harnesses**: the message is typed as the user's message in every harness, so harness auto-mode reviewers count an approval in it as the user's.
- **Keyboard and native input**: the global shortcut, Enter, Shift+Enter, ⌘K with arrows and Enter, ⌘1 to ⌘4 and Escape reach every action. The panel shares the app's text size shortcuts. The message field keeps focus while the panel shows, including after a click elsewhere in the panel; the native panel loses it on a press outside its controls (seed `s-x0jmn8`).

## Tests

- Wire: `internal/daemon/quick_captures_wire_test.go`, `native/crates/attn-app/tests/wire/quick_capture.rs` (the native panel, its keys, its automation and the shortcut editor's capture section).
- Stack: `cmd/attnd/quick_captures_stack_test.go`, `cmd/attnd/send_stack_test.go` (pairing, sending to a local and an outpost session, unpair), `app/e2e/quick-capture-history.spec.ts`.
- Scenario: `quick-capture` (Tauri), `native-quick-capture` (native, macOS only; it reports what it found broken in the native panel together at its end).
- App: `app/src/App.quickCapture.test.tsx`.

## Gotchas

- A named instance starts with no shortcut, because only production defaults to ⌃⌥⌘Space: bind one before looking for the panel.
- Opening the panel leaves the other app in front, but a click or drag on the panel makes attn the active app. Check `input_driver frontmost` before the first press, not after it.
- On a shared Mac, only the shell binding runs. The editor's recorder reads physical keys, the global shortcut is system-wide input and the File menu item is in the menu bar, so everything else runs in the VM. The guidance step still runs once a member's session starts. The editor's "Reset" binds ⌃⌥⌘Space, and a running production attn already holds that key. Leave "Reset" to the VM.
- `bridge` reaches the main window; `bridge --capture` reaches the panel. Neither sends physical keys or presses outside the app: the input driver presses the global shortcut, the File menu item and the editor's recorder keys, and drags files in from the fixture app.
- After a send, attn hides the panel and resets its mailbox to chief. Reopen the panel before reading history and select Fern again before a later send intended for the member.
- The attachment turn must precede the generic Quick Capture reply in the fixture. The first matching turn wins, so a reply alone never retrieves the file.
- Mock `reply` text is literal. The fixture uses `exec` with `printf` to echo the captured message. The `exec` action expands captures in its arguments.
- `input_driver global_key` posts to the whole system. While no shortcut is bound, ⌃⌥B lands in the app in front, such as the terminal you drive from. Bind the shortcut first.
- The native app refuses a shortcut another app or macOS holds with `Cannot register <binding>: ... Previous shortcut remains active.`, naming the owner when Rune can tell, and keeps the previous one.
- `bridge --capture state` waits for the `expect` you pass, such as `{"visible":true}` or `{"mailbox":"fern"}`, and fails after 30 s when it never holds. Call it without one for a plain read.
- A macOS notification banner, such as "See what's new in macOS Tahoe" on a fresh VM, covers the screen at a layer above the panel. Click it away before driving.
- attn types a capture only after 30 seconds of quiet in the agent's terminal. `wsctl input` to that agent restarts the wait.
- Working but wrong: "Saved for <mailbox>" means attn stored the message, not that the agent saw it, and the history's "Read" mark has the tooltip "Retrieved from the inbox; not necessarily understood or acted upon." The proof is the typed text on the agent's screen.

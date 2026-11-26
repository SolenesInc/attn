# Annotations

Select text in a live terminal or a rendered markdown file, comment on it,
and send the batch to a session as one message. The user marks an agent's
reply while reading it, or a plan an agent opened in a markdown tile, with a
comment, a quick label or a deletion, adds a note, and sends everything at
once. The agent receives the batch as the user's message. The message
quotes each marked passage with what the user said about it.

## Sub-features

- `terminal-annotations`: ⌥-drag across an agent's finished message in its terminal to mark it, then label it, comment on it or remove the mark.
- `markdown-annotations`: select text in a markdown or seed tile to comment on it, label it or mark it for deletion, and add an overall note.
- `quick-labels`: one-click labels such as Verify this, Show the receipt, Give me an example and Consider alternatives. Some carry instructions for the agent.
- `annotation-drafts`: attn saves marks as drafts, and the drafts survive an app relaunch and the agent's next turn. Two seed tiles showing one seed share its draft mark by mark.
- `review-notes`: the list of a tile's marks, to open a mark again, edit it or remove it.
- `send-batch`: ⌘↩ or the send button sends every mark, with the note, as one message, and clears the marks it sent.
- `send-destination`: a markdown tile sends to its bound session or another session on the desktop. A seed tile sends to the seed's tender, or leaves a note on the seed when nobody tends it (see [garden.md](garden.md) for the seed).

## How to get to it

### People

- Keys (`native/crates/attn-app/src/keymap.rs`): ⌘↩ sends a terminal's marks (`terminal.sendAnnotations`) or a markdown tile's (`markdown.sendAnnotations`), whichever tile holds the keyboard. Inside a comment box, ⌘↩ saves the comment instead.
- In an agent's terminal (`native/crates/attn-app/src/desktop/annotating.rs`): ⌥-drag across its message. The mark's popup has "More labels", "Write a comment" and "Remove this annotation", and, once a comment is being written, the comment box with its Comment button. The panel at the terminal's bottom right lists the marks, each with a remove button, and has "Note sent with these annotations" and "Send all".
- In a markdown or seed tile (`native/crates/attn-app/src/markdown/annotating.rs`): select text, then press Delete, Comment, Quick label or a label's emoji on the toolbar.
- The tile header (`native/crates/attn-app/src/markdown/sending.rs`): "Overall note", "Notes <n>", which opens the "Review notes" list, the send button ("Send <n>" to the bound session, or "Note on seed <n>"; "Review <n>" on a review document, which opens the send sheet ([markdown.md](markdown.md))), and "Change annotation destination" with its menu of this desktop's sessions.
- Where people see what agents do: the batch arrives in the agent's conversation as the user's message, and the agent's reply follows in its terminal.

### Agents

- Prompts: agents have no verb. They receive annotations. The terminal batch starts "Feedback on your last message." and quotes each mark with its label and comment (`internal/prompts/content/annotations/terminal-*.md`). The markdown batch starts "# Markdown Annotations", names the file, and lists each mark by line (`internal/prompts/content/annotations/markdown-*.md`). On a review document, a question arrives alone as "# A question from the user's review" with the `attn review answer` command, and the send arrives as "# Review" with the marks in reading order, then the edits the user saved to the files with the text before and after, then the verdict. Label instructions come from `internal/prompts/content/annotations/labels/`.

## Driving it

Preconditions:

- Use the baseline instance with its app, launched as [../SKILL.md](../SKILL.md) says. On Linux, run every command that reaches the app inside one `xvfb-run`.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-annotations-proof.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- Make an agent folder with `A=$(mktemp -d /tmp/drive-attn-annotations.XXXX) && echo "export A=$A" >> $DRIVE_ENV`. Save `plan.md` (`# Plan`, then the paragraph `Cache the tile layout before the first paint.`) in it, and this fixture as `$A/.attn-mock-agent.json`. Give the `attn open` action the absolute path of `plan.md`:

```json
{"version": 1, "turns": [
  {"includes": "Feedback on your last message", "actions": [
    {"type": "reply", "text": "got terminal feedback"}]},
  {"includes": "Markdown Annotations", "actions": [
    {"type": "reply", "text": "got markdown feedback"}]},
  {"includes": "say-prose", "actions": [
    {"type": "reply", "text": "A retry wrapper protects idempotent operations from duplicate network effects."}]},
  {"includes": "show-plan", "actions": [
    {"type": "attn", "args": ["open", "plan.md"]}]}
]}
```

- Start the agent on the current desktop with `wsctl add-session --cwd $A --agent codex --label anno-agent --id anno-agent --place`. Press `sidebar.session.anno-agent`. `<agent>` is the `runtime_id` in `bridge state '{"of":"desktops"}'`.

Steps:

- **Mark an agent's message.** Run `wsctl input --id <agent> --text "say-prose" --enter`, and wait with `bridge state '{"of":"terminal:<agent>","includes":"duplicate network effects."}'`. The app reads the message from the daemon a moment later: take its `key` from `wsctl request --json '{"cmd":"session_messages_get","session_id":"anno-agent","request_id":"m1"}' --event session_messages_get_result`, run again until the message is there, and wait for the app to hold it with `bridge state '{"of":"terminal-view:<agent>","expect":{"annotations":{"messages":{"$some":"<key>"}}}}'`. The terminal rows give the row and column of `retry wrapper`. In `bridge state '{"of":"terminal-view:<agent>"}'`, `caret` is the cell named by `cursor`, in points from the terminal's top-left, so it gives the cell size and the grid's origin. Turn the phrase's first cell into a fraction of the size in `bridge wait '{"target":{"key":"terminal.<agent>"}}'`'s `rect`. Run `bridge hold '{"mods":"alt"}'`, then `bridge drag '{"target":{"key":"terminal.<agent>"},"at":[<fx>,<fy>],"by":[<12 cells wide>,0]}'`, then `bridge hold '{"mods":""}'`. In `terminal-view:<agent>`, `annotations.marks` holds one mark quoting `retry wrapper`, `annotations.popup` is open, and violet washes cover the phrase.
- **Label and comment.** Press `terminal.<agent>.annotation.label.clarify-this`. `wsctl request --json '{"cmd":"session_annotations_get","request_id":"r1","session_id":"anno-agent"}' --event session_annotations_get_result` holds the mark with that label at generation 1. ⌥-click the mark the same way to open it, press `terminal.<agent>.annotation.write`, `bridge type '{"text":"Which operations are idempotent?"}'` and `bridge key '{"chord":"ctrl+Enter"}'` (`mod+Enter` on macOS): the stored mark carries the comment.
- **Relaunch keeps it.** Run `./attn instance stop-app`, launch again and press the session. `terminal-view:<agent>` lists the mark, and the washes draw again.
- **Note and send.** Press `terminal.<agent>.annotation.note.field` and type `Keep it short.`. `terminal-view:<agent>` shows the note once the typing pauses. Press `ctrl+Enter` in the note, `ctrl+shift+Enter` from the terminal on Linux, or `mod+Enter` on macOS, or press `terminal.<agent>.annotation.send`. `annotations.outcome` reads `{"kind":"sent","count":1}` for about two seconds, and the stored draft is empty. The mock's terminal does not draw a reply that arrives through its app server, so read it with `wsctl request --json '{"cmd":"session_messages_get","request_id":"r2","session_id":"anno-agent"}' --event session_messages_get_result`, which holds `got terminal feedback`.
- **Mark a document.** Widen the window as [markdown.md](markdown.md) says, and run `wsctl input --id <agent> --text "show-plan" --enter`. `<doc>` is `tile.<tile_id>.document`, and the document's blocks are keyed `b1`, `b2` and on. A block's box spans the tile, so aim a drag at the start of its text: `bridge drag '{"target":{"key":"b1"},"at":[0.01,0.5],"by":[100,0]}'`. `<doc>.toolbar` appears. Press `<doc>.toolbar.label.clarify-this`: the header's `Notes 1` button counts it. Select again, press `<doc>.toolbar.comment`, type with `bridge type` and press `ctrl+Enter` (`mod+Enter` on macOS): `Notes 2`. Typing over a selection opens the comment box with what was typed. Select a third passage and press `<doc>.toolbar.delete`: a line runs through its words and the header reads `Notes 3`. Scroll the document under an open toolbar or comment box: they move with their passage.
- **Overall note and review notes.** Press `<doc>.overall`, type `Looks right overall.` and press `ctrl+Enter`: `Notes 4`. Press `<doc>.notes`: `<doc>.notes.dialog` lists the marks in reading order. Escape closes it.
- **Change the destination.** Press `<doc>.send.menu`: `<doc>.send.menu.list-menu` is a menu of this desktop's sessions, keyed `<doc>.send.menu.<n>`, the bound one checked. Down reaches the first. Escape closes it.
- **Send the document's marks.** Press `<doc>.send`, labelled `Send 4 to anno-agent`: it reads `Sent ✓`, the stored draft is empty in `wsctl request --json '{"cmd":"markdown_annotations_get","request_id":"d1","document_uri":"attn://file/<plan.md's absolute path, percent-encoded>","source_kind":"file","path":"<plan.md's absolute path>"}' --event markdown_annotations_get_result`, and peek shows `got markdown feedback`. With a new mark and the document pressed, `ctrl+shift+Enter` (`mod+Enter` on macOS) does the same.
- **Mark inside a block.** After the send, append `- A list item takes a quick label.`, `> A quoted line.` and a `<details open><summary>Summary words</summary>` block with a body paragraph and `</details>` to `plan.md`. A list's `bN` box spans the tile, so aim at the item's own text: `bridge drag '{"target":{"role":"label","name":"A list item takes a quick label."},"at":[0.01,0.5],"by":[100,0]}'`. `<doc>.toolbar` appears. Press `<doc>.toolbar.label.clarify-this`: `Notes 1`, and once the draft saves, `markdown_annotations_get` holds a mark whose `anchor.block_id` ends in `-list-item`. A passage in a quote, an alert, a footnote or the summary marks the same way, anchored to its own block.
- **Mark a seed.** Show a seed whose body has a paragraph with `./attn open <seed id>`. `<doc>` is the seed tile's `tile.<tile_id>.document`, and the plan's blocks are keyed as a file's. Mark a passage as above. Untended, the header's button reads `Note on seed 1`. Pressing it leaves the batch on the seed's log (`./attn seed notes <seed id>`) and reads `Noted ✓`. With the seed tended (`./attn seed tend <seed id>` from an agent on the desktop), it reads `Send 1` and goes to that agent, and `<doc>.send.menu` offers `Note on seed`. The draft's document URI is `attn://seed/<seed id>`.
- **Proof.** In `$P`, keep `terminal-view:<agent>` and the stored draft after each step, `./attn agent peek anno-agent` after each send, and `./attn seed notes <seed id>` after the seed note. Capture `bridge shot '{"path":"<P>/annotations-<step>.png"}'` with the popup open, while writing, after the relaunch, with the panel, and with the document's marks and the review notes open, where `<P>` is the folder's path written out. The shot shows the terminal's marks. Name each file for its way in, such as `annotations-terminal-send-peek.txt`.

The label emoji draw from the system's emoji font, so a Linux machine without one draws them as boxes.

## Contexts

- **Durability across restarts**: the daemon keeps drafts for each session and each document, and they come back after an app or daemon restart and across the agent's next turn. A send clears the marks it sent. Marks made while a send was in flight stay. A send the agent's terminal does not take within 5 s fails, keeps the marks and shows the reason, and other terminals keep taking input meanwhile.
- **Linux**: the send key is Ctrl+Shift+Enter there (`LINUX` in `native/crates/attn-app/src/keymap.rs`). Inside a comment box or the terminal's note, Ctrl+Enter saves or sends. Marking a terminal is an Alt-drag.
- **Outposts**: while an outpost's host is offline, its agent's terminal still takes marks, and the panel says `<host> is unreachable` in place of "Send all".
- **Profiles**: marks go to a session of the profile the app shows. A seed's marks go only to a seed in that profile.
- **Harnesses**: terminal marks attach to the agent's messages as its harness records them, so a mark lands only on a finished message's prose. A shell has no such messages. Markdown marks work the same for every harness.
- **Keyboard and native input**: ⌘↩ sends from the tile that holds the keyboard, and only one tile receives the key at a time. Inside a comment box, ⌘↩ saves the comment, and inside the terminal's note, ⌘↩ sends. In a focused document, Escape closes the review notes and the comment box before it leaves focus. A press outside every terminal, on the sidebar for example, leaves a terminal mark's popup open.

## Tests

- Wire: `native/crates/attn-app/tests/wire/terminal_annotations.rs`, `native/crates/attn-app/tests/wire/markdown_annotations.rs`, `internal/daemon/session_annotations_wire_test.go`, `internal/daemon/annotation_delivery_wire_test.go`, `internal/daemon/annotation_drafts_wire_test.go`, `internal/daemon/markdown_annotations_wire_test.go`.
- Scenario: `native-terminal-annotations`, `native-markdown-annotations`.

## Gotchas

- A terminal mark needs ⌥ down for the whole drag and prose from a finished message. A drag over the prompt, tool output or a shell opens nothing, and a plain drag only selects text to copy.
- The marked text must still line up with the message, and the app must hold the message's key. Mark after the reply has settled and `terminal-view:<agent>` lists the key in `annotations.messages`. Read rows from `terminal:<agent>`, never from a screenshot.
- attn refuses to send to a terminal session that waits on an approval, and says so, because the Enter that sends would answer the approval. Answer the approval, then send again. A Sigma session takes the send during an approval: the agent reads it after the approval is answered, or at once when only a subagent waits.
- ⌘↩ sends only from the tile that holds the keyboard. With focus elsewhere it does nothing. Press into the tile or use the send button.
- attn removes a mark that has no label or comment when the mark's popup closes. Give a mark a label before you mark the next passage.
- A reply whose text repeats an earlier reply in the same conversation cannot be marked: the drag shows "Only what the agent wrote can be annotated". The mock's fixed replies repeat, so mark each reply text once per session, or start another session.
- Working but wrong: the destination menu lists only sessions on the tile's desktop, so a session on another desktop cannot receive a document's marks without moving the session first. A mark made while a send is in flight stays behind with a notice, on purpose. The mark is not a lost send.
- A tile 720 points across or less has no room for "Overall note" in its header. Widen the window before you press `<doc>.overall`.

# Annotations

Select text in a live terminal or a rendered markdown file, comment on it,
and send the batch to a session as one message. The user marks an agent's
reply while reading it, or a plan an agent opened in a markdown tile, with a
comment, a quick label or a deletion, adds a note, and sends everything at
once. The agent receives the batch as the user's message, quoting each
marked passage with what the user said about it.

## Sub-features

- `terminal-annotations`: ⌥-drag across an agent's finished message in its terminal to mark it, then label it, comment on it or remove the mark.
- `markdown-annotations`: select text in a markdown or seed tile to comment on it, label it or mark it for deletion, and add an overall note.
- `quick-labels`: one-click labels such as Verify this, Show the receipt, Give me an example and Consider alternatives; some carry instructions for the agent.
- `annotation-drafts`: marks are saved as drafts, and survive an app relaunch and the agent's next turn.
- `review-notes`: the list of a tile's marks, to open one again, edit it or remove it.
- `send-batch`: ⌘↩ or the send button sends every mark, with the note, as one message, and clears what went.
- `send-destination`: a markdown tile sends to its bound session or another session on the desktop; a seed tile sends to the seed's tender, or leaves a note on the seed when nobody tends it ([garden.md](garden.md) owns the seed).

## How to get to it

### People

- Keys (`app/src/shortcuts/registry.ts`): ⌘↩ sends a terminal's marks (`terminal.sendAnnotations`) or a markdown tile's (`markdown.sendAnnotations`), whichever tile is active. Inside a comment box, ⌘↩ saves the comment instead.
- In an agent's terminal (`app/src/components/TerminalAnnotations/AnnotatedTerminal.tsx`): ⌥-drag across its message; the mark's popup with "More labels", "Write a comment" and "Remove this annotation", and, once a comment is being written, the "Annotation comment" box with its Comment button; the "Annotations" panel with "Remove annotation", "Note sent with these annotations" and "Send all".
- In a markdown or seed tile (`app/src/components/MarkdownReader/annotations/SelectionToolbar.tsx`, `AnnotationPopover.tsx`, `AnnotationSidebar.tsx`): select text, then Delete, Comment, Quick label or a label's emoji; the "Review notes" list.
- The tile header (`app/src/components/SessionTerminalDesktop/TileAnnotationControls.tsx`): "Overall note", "Notes <n>", the send button ("Send <n>" to the bound session, or "Note on seed <n>"), and "Change annotation destination" with its "Send annotations to session" menu.
- Where people see what agents do: the batch lands in the agent's conversation as the user's message, and the agent's reply follows in its terminal.

### Agents

- Prompts: agents have no verb; they receive annotations. The terminal batch starts "Feedback on your last message." and quotes each mark with its label and comment (`internal/prompts/content/annotations/terminal-*.md`). The markdown batch starts "# Markdown Annotations", names the file, and lists each mark by line (`internal/prompts/content/annotations/markdown-*.md`). Label instructions come from `internal/prompts/content/annotations/labels/`.

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`).
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-annotations-proof.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- An agent folder, `A=$(mktemp -d /tmp/drive-attn-annotations.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, holding `plan.md` (`# Plan`, then the paragraph `Cache the tile layout before the first paint.`) and this fixture as `$A/.attn-mock-agent.json`:

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

- The agent on the current desktop: `wsctl add-session --cwd $A --agent codex --label anno-agent --id anno-agent --place`. `<agent>` is its `runtime_id` in `wsctl list`. Select it for setup with `bridge select_session '{"sessionId":"anno-agent"}'`.

- **Mark an agent's message.** Run `wsctl input --id <agent> --text "say-prose" --enter`, and wait until `./attn agent peek anno-agent` shows the sentence. In `bridge read_pane_text '{"sessionId":"anno-agent"}'`, find the row and columns of `retry wrapper`. Take the centers of its first and last cells from `bridge get_pane_cell_rect '{"sessionId":"anno-agent","cell":{"col":<c>,"row":<row>}}'`, convert both as the README says, and run `bridge native_mouse '{"action":"drag","x":<x1>,"y":<y1>,"toX":<x2>,"toY":<y2>,"modifiers":["option"]}'`. `bridge get_annotation_state` shows `popupOpen: true`, the quick labels in `labels`, and `annotations` holding one entry whose `quote` reads `retry wrapper`. The popup shows the quote only once you write a comment.
- **Comment on it.** Click `[aria-label="Write a comment"]` natively: `get_annotation_state` shows `commentFocused: true` and `popupQuote` reading `retry wrapper`. Run `bridge native_text '{"text":"Which operations are idempotent?"}'` and `bridge native_key '{"key":"Enter","modifiers":["command"]}'`. `annotations` holds one entry whose `quote` is `retry wrapper` and whose `comment` is the question.
- **Label another passage.** ⌥-drag across `duplicate network effects` the same way, click `[aria-label="More labels"]`, then `[data-quick-label-id="show-the-receipt"]`, natively. `annotations` holds two entries, the second with `emoji` `🧾`.
- **Drafts survive a relaunch.** Run `bridge --fresh-launch --wait-ready ping` and select the agent. `get_annotation_state` still lists both marks.
- **Send with ⌘↩.** Run `bridge select_session '{"sessionId":"anno-agent"}'` so focus is in the agent's terminal, not in the popup or the note, then run `bridge native_key '{"key":"Enter","modifiers":["command"]}'`. `./attn agent peek anno-agent` shows `got terminal feedback`, and `get_annotation_state` shows `annotations` empty. The panel's "Send all", clicked natively, does the same.
- **Mark a document.** Run `wsctl input --id <agent> --text "show-plan" --enter`, then `bridge dom_wait '{"selector":"[data-tile-kind=\"markdown\"] .md-reader--annotating p","timeoutMs":5000}'`. Read the paragraph's box with `bridge dom_bounds '{"selector":"[data-tile-kind=\"markdown\"] .md-reader-doc p"}'` and drag natively across its left half. `bridge dom_wait '{"selector":".md-selection-toolbar","timeoutMs":5000}'` matches. Click `.md-selection-toolbar [title="Comment"]` natively, run `bridge native_text '{"text":"Measure it first."}'` and `bridge native_key '{"key":"Enter","modifiers":["command"]}'`. `bridge markdown_get_annotations_state` lists one annotation with that `text`.
- **Overall note and review notes.** Click `[data-tile-kind="markdown"] .desktop-dock-tile-review-button--overall` natively, type `Looks right overall.` with `native_text`, and press ⌘↩. `bridge dom_text '{"selector":"[data-tile-kind=\"markdown\"] .desktop-dock-tile-review-count"}'` reads `2`. Click `[aria-label="Notes 2"]` natively: `bridge dom_wait '{"selector":"dialog.md-annotations-sidebar","timeoutMs":5000}'` matches. Press Escape.
- **Send the document's marks.** Click `[data-tile-kind="markdown"] .desktop-dock-tile-send-button` natively; its label reads `Send 2 to anno-agent`. It turns to `Sent ✓`, `markdown_get_annotations_state` lists none, and peek shows `got markdown feedback`. Pressing ⌘↩ with focus inside the tile does the same.
- **Change the destination.** Add a mark again, then click `[aria-label="Change annotation destination"]` natively: `bridge dom_wait '{"selector":"[role=\"menu\"][aria-label=\"Send annotations to session\"]","timeoutMs":5000}'` matches and lists the sessions on this desktop.
- **A seed nobody tends.** Run `./attn seed plant "Annotate me" -m "Mark this sentence."` and `./attn open <seed>`. In the seed tile, mark a passage with a comment as above: the send button reads `Note on seed 1`. Click it: it reads `Noted ✓`, and `./attn seed notes <seed> --limit 1` shows the batch as a note.
- **Proof.** In `$P`, keep `bridge get_annotation_state` after each mark, after the relaunch and after the send; `bridge markdown_get_annotations_state` before and after the document's send; `./attn agent peek anno-agent` after each send; and `./attn seed notes <seed>` after the seed note. Capture `bridge capture_screenshot '{"path":"<P>/annotations-markdown.png"}'` with the document's marks and the review notes open, `<P>` written out; terminal marks do not show in that capture, so their proof is `get_annotation_state`. Put the way in in each file's name, such as `annotations-terminal-send-peek.txt`.

## Contexts

- **Durability across restarts**: drafts are kept by the daemon for each session and each document, and come back after an app or daemon restart and across the agent's next turn. What was sent is cleared; marks made while a send was in flight stay.
- **Linux**: the send key is Ctrl+Shift+Enter there (`LINUX_SHORTCUTS`); marking a terminal is an Alt-drag.
- **Outposts**: while an outpost's host is offline, its agent's terminal refuses new marks and says `<host> is unreachable`.
- **Profiles**: marks go to a session of the profile the app shows, and a seed's marks only to a seed in that profile.
- **Harnesses**: terminal marks attach to the agent's messages as its harness records them, so a mark lands only on a finished message's prose; a shell has none. Markdown marks work the same for every harness.
- **Keyboard and native input**: ⌘↩ sends from the active tile and only one tile owns it at a time; inside a comment box it saves the comment, and inside the terminal's note it sends. In a focused document, Escape closes the review notes and the comment box before it leaves focus.

## Tests

- Wire: `internal/daemon/session_annotations_wire_test.go`, `internal/daemon/annotation_delivery_wire_test.go`, `internal/daemon/annotation_drafts_wire_test.go`, `internal/daemon/markdown_annotations_wire_test.go`.
- Stack: `app/e2e/terminal-annotations.spec.ts`, `app/e2e/markdown-annotation-tiles.spec.ts`.
- Scenario: `terminal-annotations`, `markdown-opener`.
- App: `app/src/App.sessionAnnotations.test.tsx`, `app/src/App.markdownAnnotations.test.tsx`, `app/src/components/MarkdownReader/anchoring/*.test.*`, `app/src/annotations/quickLabelPlacement.test.ts`, `app/src/utils/terminalAnnotations.gate.test.ts`, `app/src/utils/terminalMessageAlign.projection.test.ts`.

## Gotchas

- A terminal mark needs ⌥ down for the whole drag and prose from a finished message; a drag over the prompt, tool output or a shell opens nothing, and a plain drag only selects text to copy.
- The marked text must still line up with the message. Retry after the agent's reply has settled on screen, and read rows from `read_pane_text`, never from a screenshot.
- A send to a session that waits on an approval is not sent and says so, because the Enter that sends would answer the approval. Answer it, then send again.
- ⌘↩ sends only from the active tile. With focus elsewhere it does nothing; click into the tile or use the send button.
- `bridge drag_pane_selection` and `dom_click` skip native input; use them to set up, never as proof of a person's mark.
- `bridge capture_screenshot` leaves out terminal content and its marks. Prove terminal marks with `get_annotation_state`.
- Working but wrong: the destination menu lists only sessions on the tile's desktop, so a session on another desktop cannot receive a document's marks without moving it first. A mark made while a send is in flight stays behind with a notice; that is intended, not a lost send.

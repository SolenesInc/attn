# Annotations

Annotations let you answer an agent the way you would mark up a draft. Select
a passage, label it or comment on it, do the same for as many passages as you
like, and send the whole batch to the agent as one message. The agent gets
each quote with your label and comment beside it, instead of a reply that
says "the third point, the one about caching".

You can annotate two things: what an agent wrote in its terminal, and a
markdown file or seed shown in a tile.

## Labels and comments

Every annotation has a label, a comment, or both. Three labels sit on the
toolbar: 👍 I agree, ❌ This is wrong and ❓ Clarify this. ⚡ opens the rest:
💯 Exactly this, 😕 I don't love this, 🔍 Verify this, 🧾 Show the receipt,
🔬 Give me an example, 🔄 Consider alternatives, 🪓 Cut this, 🪙 Your call and
🙋 Ask me first.

Four labels also tell the agent what to do:

- 🔍 Verify this: check the claim against the actual code before going on.
- 🧾 Show the receipt: name the file and line, measurement or command output
  behind it, or say it is an assumption.
- 🔬 Give me an example: show a before and after, a sample, or a scenario.
- 🔄 Consider alternatives: propose two or three approaches with trade-offs.

## In a terminal

Hold Option (Alt on Linux) and drag across text the agent wrote. A small
popup opens on the selection: pick a label, click ⚡ for more labels, click 💬 to
write a comment, or click 🗑 to drop the annotation. In the comment box, Cmd+Enter (Ctrl+Enter on Linux)
saves the comment. The passage stays highlighted. Option-click the passage (Alt-click
on Linux) to change the annotation.

The **Annotations** panel collects the batch for that agent. Each card shows
the quote, its label and comment. Click a card to edit it, or ✕ to remove it. The
box at the bottom ("Anything else to say with these?") adds a note that goes
with the batch.

Send the batch with **Send all**, or Cmd+Enter (Ctrl+Shift+Enter on Linux)
while the agent's tile is active. The agent receives the batch as your message. The message starts with "Feedback on your last message.", then your note, then each annotation in order. Each annotation shows its number, its label, the quote, and your comment.

You can annotate only text the agent wrote. Attn finds the text in the agent's
own transcript, so if you drag over your prompt, the harness's own interface,
or a turn that has scrolled out of reach, Attn shows a short notice instead.

## In a markdown file or seed

A markdown tile shows a file rendered. Open a markdown tile by Cmd-clicking (Ctrl-clicking
on Linux) a markdown path in a terminal, or with `attn open <file.md>`, which
is how agents show you a file. `attn open <seed-id>` shows a seed the same
way.

Select text in the tile. No modifier is needed. A toolbar opens with the
three labels, ⚡ for more, a comment button and a delete button, which marks
the passage for removal. With the toolbar open:

- Start typing to write a comment. Cmd+Enter (Ctrl+Enter on Linux) saves it.
- Option+1 through Option+9 (Alt on Linux) pick the labels behind ⚡, in order.
- Escape closes the toolbar.

The tile header has **Overall note** for feedback on the whole document, and
**Notes** with a count, which opens Review notes: every annotation in the
file, in order. If the file changes and a passage moves, its note is marked
"⚠ moved" and still goes with the batch.

The send button in the tile header shows where the batch goes. A markdown
tile sends to its agent, the session that opened the file. **To** and ▾ pick
another agent on the same desktop. A seed tile sends to the agent tending the
seed, and ▾ offers **Note on seed**, which adds the batch to the seed's log
instead. A seed nobody tends offers only the note. Cmd+Enter (Ctrl+Shift+Enter
on Linux) sends while the tile is active.

The agent receives a message headed "Markdown Annotations" that gives the file's path. The message then lists each annotation with its line numbers, the quote, the label and the comment, and ends with a count of the labels you used.

## Details

<details>
<summary>Why wasn't my batch sent?</summary>

When the agent is waiting on an approval prompt, Attn does not send: the Enter that submits the batch would answer the prompt. The batch stays as it is, and the panel or tile header says why. Answer the prompt, then send again.
</details>

<details>
<summary>Do unsent annotations survive a restart?</summary>

Yes. Attn keeps the batch for each agent and each file until you send the batch or remove its annotations, so closing the tile or restarting the app doesn't lose the batch. Sending clears what was sent. Tiles showing the same seed share its annotations, so a mark you add in one stays when you send from the other.
</details>

<details>
<summary>Does sending annotations count as prompting the agent?</summary>

Yes. The batch arrives as your message, and in queue flow [auto-settle](queue.md#settle) treats it like a prompt you typed.
</details>

<details>
<summary>Can I annotate a shell?</summary>

No. Attn finds the text an agent wrote in the agent's transcript, and a shell has no transcript.
</details>

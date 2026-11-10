# Code review

An agent can present its change to you as a review: a document that tells the
story of the change and shows the code it is about, in a tile beside the
agent. You read it, mark lines with notes and questions, fix a line yourself,
and send the agent one review with a verdict. You never leave attn, and the code
comes in the order that explains the change, not in file order.

Code review is experimental and off by default.

## Turn it on

Open **Settings → Experimental** and turn on **Code review**, or run:

```sh
attn settings set code_review_enabled true
```

Agents you start or reload after that know how to present a change.

## Ask for a review

Tell the agent "present the change" or "let's review it". The agent writes a
review document, a markdown file with `attn: review` in its frontmatter, and
opens it in a tile next to its session. The review covers everything the change
touched: each file is either shown or listed under **Not shown** with the
reason.

When the agent changes the code again and presents the same document, the
review starts where your last sent review ended, so you see only what changed
since your notes.

## Read the review

A review is made of steps. Each step is a short explanation followed by stops.
A stop shows a range of one file with the change drawn in: added lines,
removed lines, and the agent's notes under the lines they explain. A note marked
as the place to look is the line the agent most wants you to check. A stop on a
markdown file shows the text rendered, with the changed words marked.

The review always shows the files as they are now. If you or the agent edit a
file while you read, the stop follows its lines.

Click the review, then move with the keyboard. The key line at the bottom of the
tile lists the keys for what you are looking at, and `?` shows them all.

| Key | Does |
|---|---|
| ↑ ↓ | Move by line in code and by block in prose |
| ← → | The previous or next stop |
| Enter | Open the whole file at the cursor, with the stop's lines marked |
| `e` | Show 10 more lines around the stop |
| `z` | Fill the tile with the file |
| `/` | Search the repository: files by name, lines by text |
| ⌃O | Go back to the file you came from |
| Escape | Close the file view, the search or the key sheet |

In the file view, ← and → move to the previous or next change. In a stop on a
markdown file on this computer, `o` opens the file in an editor tile.

## Switch the frame

One review has three frames. The agent picks the one that suits the change, and
`1`, `2` and `3` switch:

- **Brief** (`1`) reads the document top to bottom.
- **Walkthrough** (`2`) fills the tile with the file and steps through the
  stops, with the agent's explanation beside it. `z` hides the explanation.
- **Map** (`3`) draws the change as a sequence diagram. ← and → step through
  its messages, each beside the code that sends it. When the review has a
  diagram before and after the change, `b` switches between them.

A frame the review cannot fill, such as Map for a review with no diagram, is
left out.

## Mark lines

Put the cursor on a line, or select several lines with ⇧↑ and ⇧↓. Then:

- `c` writes a note. Notes wait until you send the review.
- `a` asks the agent a question. The question goes at once, and the answer
  shows under the line when the agent replies. The question stays in the review.
- `i` edits the line and saves the file. In a string or comment that spans
  several lines, `i` takes all of it. In a markdown stop, it takes the
  paragraph. ⌥↑ and ⌥↓ take in one more line. `u` on the line undoes the edit.

If the file changed underneath you since you opened the edit, attn writes
nothing and says so.

When a line you marked changes, the mark moves to **Moved notes** at the end of
the review and still goes with it.

Selecting text with the mouse and typing still writes a comment, as in any
[markdown tile](annotations.md#in-a-markdown-file-or-seed).

## Send the review

Press ⌘Enter. The send sheet lists your notes and edits. Pick a verdict with
`a` (approve), `r` (request changes) or `c` (comment), then press Enter.

The agent receives one message: your notes in reading order with the lines they
are on, your edits with the text before and after, and the verdict.

- **Approve**: the agent addresses any notes and carries on, without presenting
  again.
- **Request changes**: the agent addresses each note and presents the change
  again.
- **Comment**: the agent replies to your notes.

## Code navigation

On a line of code, in a stop or the file view:

- `f` goes to the definition of a name on the line. When the line has several
  names, each gets a label; type it. On a definition, `f` lists its uses.
- `r` lists the uses of a name. ↑ and ↓ pick one, and Enter opens it.
- `h` shows the type of a name.

An answer that comes after you moved on, or while search or an edit box is
open, waits in a list instead of moving you. A definition outside the
repository opens for reading only: you cannot mark or edit it.

A language server on the machine that holds the files answers. attn starts it
when the review opens and stops it after five minutes with no review shown
(`code_navigation_idle_stop_seconds`). The key line shows each language's
server: loading, ready, failed, or no server. Built-in definitions cover
Go (`gopls`), TypeScript (`typescript-language-server`), Rust
(`rust-analyzer`) and Python (`pyright-langserver`). attn looks for the program
in its own code navigation folder, then on `PATH`.

A language with no server still reads; only the jumps are missing. To set one
up, run these from the repository:

- `attn nav status` lists each language's server and why one is missing.
- `attn nav link <program>` links a program into attn's code navigation
  folder, such as a server your editor installed outside `PATH`.
- `attn nav set <language> <definition>` sets how to start a server for this
  repository. `attn nav --help` describes the definition.
- `attn nav check <path>:<line>` asks for a definition on that line. Setup is
  done when it answers.

## Outposts

A review presented by an agent on an [outpost](using-outposts.md) reads,
searches, saves edits and runs language servers on the outpost, where the files
are. The editor tile is not offered for an outpost's file.

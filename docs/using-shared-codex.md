# Using shared Codex

Shared Codex is experimental. With it on, your Codex terminals talk to one
Codex app-server per profile instead of each running a Codex of their own. That
is what lets a conversation outlive the terminal that showed it: when you
switch a terminal to another conversation, the one you left keeps running as a
hidden session instead of closing.

It runs the stock `codex` you already use, with your Codex account, model,
settings and conversations. attn was checked against Codex 0.160.0.

## Turn it on

Open Settings → Experimental and enable **Shared Codex**. It is off by default
and applies to new Codex terminals only. A session keeps the mode it launched
with, including when you reopen it.

Automations don't use shared Codex yet; they keep launching a plain Codex.

## One conversation, one session

- attn starts the app-server the first time a shared terminal needs it and runs
  it as a terminal of its own, so it keeps running through app and daemon
  restarts. The terminals reconnect and resume the conversations they show.
- Each conversation is one session, with its own name, queue state, inbox and
  usage. A Codex terminal shows one conversation at a time.
- When no open shared session and no shared terminal needs the server, it
  stops. The next shared Codex terminal starts it again.

## Switching conversations

`/new`, `/resume` and `/agents` change the conversation a terminal shows. attn
notices when you send the next prompt there. The terminal then shows the new
conversation's session, and the session you left stays open and **hidden**:

- It keeps its state, so a session that was waiting for you still waits.
- Annotations, messages from other agents and inbox notifications still reach
  it, through the app-server. The terminal's composer, with whatever you were
  typing, isn't touched.
- It shows in the Sessions ledger marked `hidden`, and `attn session list` and
  `attn session show` say `(hidden)`.

Several terminals can show the same conversation. They show one session.

## Hidden sessions in the queue

Hidden sessions queue like any other, so a hidden session that needs an answer
appears in the queue without a desktop chip. Choose it and attn opens it in a
tile on the current desktop, resuming the conversation there. If it was waiting
on an approval, the approval is waiting in the tile; answer it as usual.

To keep hidden sessions out of the queue, turn off Settings → Experimental →
**Show hidden sessions in the queue**. They stay in the Sessions ledger.

## Names

The session's name in attn and the conversation's name in Codex follow each
other: rename in either place and the other updates. If both change, attn's
name wins.

## Closing and resuming

Closing a tile (⌘W on macOS) ends only that terminal while another tile shows
the same session. Closing the last tile of a session archives its conversation
in Codex, settles its usage and closes the session into the ledger. A hidden
session closes the same way.

To pick it up again, reopen the session from the Sessions ledger. attn
unarchives the conversation and resumes it in a new tile.

## Turn it off

Disable **Shared Codex** in Settings → Experimental. New Codex terminals launch
a plain Codex again. Sessions that already run shared stay shared until they
close.

## Limits

- Automations launch a plain Codex; they don't use shared Codex yet.
- The app-server stops when nothing uses it, and the next shared terminal
  starts it again.
- A plain Codex can't resume a conversation that a shared session holds. attn
  refuses and names the session; open that session instead, or close it first.
- If attn can't reach the app-server when a conversation starts, that
  conversation isn't written to disk and can't be resumed after a restart.
- A crash of the app-server stops the turns running in it. attn starts it
  again on the next connection, and the terminals reconnect and resume the
  conversation they show.
- A subagent's activity counts for the session of the conversation that
  started it.

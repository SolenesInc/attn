# Using shared Codex

Shared Codex is experimental. With it on, your Codex terminals talk to one
Codex app-server per profile instead of each running a Codex of their own. That
is what lets a conversation outlive the terminal that showed it: when you
switch a terminal to another conversation, the one you left keeps running as a
hidden session instead of closing.

It runs the stock `codex` you already use, with your Codex account, model,
settings and conversations. The app-server starts with the Codex executable set
in Settings. New and reloaded shared terminals run the same Codex as the
app-server while it is still installed, even after you change the executable in
Settings. After you install another Codex, attn moves the app-server to it, as
[Codex updates](#codex-updates) describes. attn was checked against Codex
0.160.0.

## Turn it on or off

**Shared Codex** in Settings → Experimental is off by default. It decides how
new Codex terminals, and sessions you resume from the Sessions ledger, launch.
That includes the Codex sessions automations and delegations start. A running
session, hidden ones included, stays shared or plain until it closes or you
move its conversation yourself, for example with `/resume` in a Codex of the
other kind.

An automation that starts shared Codex runs with its own model, effort and
auto-approval, as a plain one does. An automation that names its own Codex
executable runs a plain Codex, since the app-server runs one Codex for the
whole profile.

## Folder trust

A shared Codex terminal asks whether to trust its folder unless you trusted
that exact folder, so it asks in every new worktree, even of a repository you
trusted. attn answers yes for it when attn has had a session in that folder
before, or in any worktree of its repository, with any agent or a shell. It
also answers for every automation. A folder attn has never had a session in
still asks the first time. attn writes nothing to your Codex config when it
answers. A folder you marked untrusted stays untrusted. One you never answered
for gets saved as trusted by Codex itself, on the main repository for a
worktree, when a conversation that can write there starts.

## One conversation, one session

- attn starts the app-server the first time a shared terminal needs it and runs
  it as a terminal of its own, so it keeps running through app and daemon
  restarts. The terminals reconnect and resume the conversations they show.
- Each conversation is one session, with its own name, queue state, inbox and
  usage. A Codex terminal shows one conversation at a time. Launching Codex on
  a conversation an open session holds opens that session in a new tile
  rather than a second session.
- When no open shared session and no shared terminal needs the server, it
  stops. The next shared Codex terminal starts it again.

## Codex updates

Once a day attn asks `codex doctor` whether a newer Codex is out. When one
is, a banner at the top of attn offers it with **Install**. Close the banner
and the same offer stays under the bell in the sidebar. Install opens a
terminal tile running Codex's own update, so what it prints, a failure
included, stays in that tile. Shared Codex terminals don't show Codex's own
update prompt, since its Update now would exit the terminal, which closes the
session and archives its conversation.

attn reads which Codex is installed when a shared terminal opens, when the
app-server starts, once a day, and when an update it ran finishes. When that
isn't the Codex the app-server runs, attn restarts the app-server on it once
no conversation runs a turn, including one that waits on an approval or on
your answer to a question, and every shared terminal has finished starting.
The banner names the sessions it waits on, and you can jump to each.

- First attn starts the installed Codex on the side for a moment. If it
  refuses to start, for example over a setting in your Codex config that the
  new version no longer supports, the app-server keeps running the Codex it
  ran and nothing is interrupted. The banner shows what Codex printed, with
  **Retry**. attn also tries again once the installed Codex changes.
- **Restart now** doesn't wait. It first lists what each session would lose,
  then stops turns waiting on an approval or a question, gives running turns a
  minute to finish and stops what still runs. Terminals still starting
  relaunch.
- The terminals lose their connection during the restart and reconnect to the
  new app-server on their own. What you typed and haven't sent stays where it
  was. The old app-server first shuts down the MCP servers its conversations
  started, so with a few of them the terminals wait up to about 20 seconds.
- A terminal still starting, or asking whether to trust its folder, couldn't
  reconnect, so the restart waits until it shows its conversation.
- Until the restart, a new or reloaded terminal runs the app-server's Codex
  while it is still installed. Codex's standalone installer keeps every
  release. npm and Homebrew replace the old one, so after an update through
  them new terminals run the new Codex against the old app-server until it
  restarts, and Codex warns about it in its footer.
- A terminal keeps the Codex it started with, so after the restart the ones
  that started before it run an older Codex. The banner offers **Reload
  tiles**, which restarts them on the new Codex in the same conversations; text
  you typed in them and haven't sent is lost. An older terminal doesn't know
  what the newer app-server added; an approval of a kind it doesn't know is
  denied, and the turn carries on.

The banner's actions are also in the command palette while they apply:
**Install Codex update**, **Restart shared Codex now** and **Reload old Codex
tiles**. From a terminal:

- `attn codex status` shows the Codex the app-server runs, the installed and
  latest ones, a restart and what it waits on, and the tiles still on an
  older Codex.
- `attn codex update` runs Codex's own update in your terminal, then has attn
  read the installed Codex. After a plain `codex update`, attn notices the new
  Codex at its next read; `attn codex restart` reads it at once.
- `attn codex restart` restarts the app-server once no turn runs, also on the
  Codex it already runs. `attn codex restart --now` does what Restart now
  does, without first listing what each session would lose.

## Switching conversations

`/new`, `/resume` and `/agents` change the conversation a terminal shows, and
its tile shows that conversation's session at once, with its name and state.
The session you left stays open and **hidden**:

- It keeps its state, so a session that was waiting for you still waits.
  From then on the app-server tells attn when its turn runs, waits on an
  approval or asks you a question, and when that turn ends. Switch a terminal
  back to it with `/resume` or `/agents` and the terminal takes over again: an
  approval it was waiting on shows there, and you answer it as usual.
- Annotations, messages from other agents and inbox notifications still reach
  it, through the app-server. The terminal's composer, with whatever you were
  typing, isn't touched.
- It shows in the Sessions ledger marked `hidden`, and `attn session list` and
  `attn session show` say `(hidden)`.

Several terminals can show the same conversation. They show one session.

A conversation you start with `/new` is a session of its own right away, and
stays open and hidden if you leave it without a prompt, like any other. A new
tile is the exception: until its first prompt, switching it to another
conversation leaves no empty session behind.

One switch shows late. When `/agents` takes a terminal back to a conversation
it started that you never prompted, Codex doesn't tell the app-server, so the
tile shows that conversation's session at your first prompt there.

A session keeps the approval mode it launched with, YOLO or auto-approve,
wherever its conversation opens: in any shared terminal with `/resume` or
`/agents`, when attn resumes it to deliver something while it's hidden, or
when you resume it from a stopped tile or the Sessions ledger. A conversation
you start with `/new` takes the mode of the session its terminal shows.

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

Closing a tile ends only its terminal when another tile shows the same
session. Closing the last tile of a session archives its conversation in Codex,
settles its usage and closes the session into the ledger. A hidden session
closes the same way.

To pick it up again, resume the session from the Sessions ledger. attn
unarchives the conversation and resumes it in a new tile, shared or plain as
the setting says.

## Limits

- Codex lets one process write a conversation at a time. While the app-server
  holds a conversation, `/resume` of it in a plain Codex fails with `already
  has an active writer`. It works once the app-server lets the conversation
  go, about a minute after no terminal shows it; the session then runs plain
  in that terminal.
- If attn can't reach the app-server when a conversation starts, that
  conversation isn't written to disk and can't be resumed after a restart.
- A crash of the app-server stops the turns running in it. attn starts it
  again on the next connection, and the terminals reconnect and resume the
  conversation they show. Hidden sessions whose turns stopped go idle at once;
  a terminal shows its stopped turn as interrupted once it reconnects.
- A subagent's activity counts for the session of the conversation that
  started it.
- In a shared terminal where you picked permissions with `/permissions`,
  Codex refuses `/resume` with `Permission overrides are not supported when
  resuming a remote task.` Resume the conversation from another shared
  terminal.

# Using shared Codex

Shared Codex is experimental. With it on, your Codex terminals talk to one Codex
app-server per profile instead of each running a Codex of their own. Sharing the
app-server lets a conversation outlive the terminal that showed it. When you
switch a terminal to another conversation, the conversation you left keeps
running as a hidden session instead of closing.

Shared Codex runs the stock `codex` you already use, with your Codex account,
model, settings and conversations. The app-server starts with the Codex
executable set in Settings. New and reloaded shared terminals run the same Codex
as the app-server while that Codex is still installed, even after you change the
executable in Settings. After you install another Codex, Attn moves the
app-server to the new Codex. [Codex updates](#codex-updates) describes how. Attn
was checked against Codex 0.160.0.

## Turn it on or off

**Shared Codex** in **Settings → Experimental** is off by default. The setting
decides how new Codex terminals, and sessions you resume from the Sessions
ledger, launch. The setting also applies to the Codex sessions that automations
and delegations start. A running session, hidden ones included, stays shared or
plain until it closes or you move its conversation yourself, for example with
`/resume` in a Codex of the other kind.

An automation that starts shared Codex runs with its own model, effort and
auto-approval, as with plain Codex. An automation that names its own Codex
executable runs a plain Codex, since the app-server runs one Codex for the
whole profile.

## Folder trust

Codex asks whether to trust a folder before it works there. A shared Codex
terminal accepts only trust saved for that exact folder, so it asks in every new
worktree, even of a repository you trusted, unless the repository has a
`.codex/` folder.

Attn answers yes to the trust question in these cases:

- An agent session in this profile already started a conversation in that
  folder. A shell, or a launch that stopped before the conversation started,
  doesn't count.
- The folder is at the top of a worktree, and an agent session in this profile
  already started a conversation at the top of any other worktree of the same
  repository that still exists. The repository's main checkout counts as one of
  those worktrees.
- Your Codex config saves the folder's repository as trusted.

Any other folder still asks. An automation never stops at the question: Attn
decides before it starts Codex, as [Automations](automations.md) describes
under "Does a Codex run trust its folder?". Attn writes nothing to your Codex
config when it answers. A folder you marked untrusted stays untrusted. Codex
itself saves a folder you never answered for as trusted when a conversation that
can write there starts. If the folder is inside a repository, Codex saves the
whole repository.

## One conversation, one session

- Attn starts the app-server in a terminal of its own the first time a shared
  terminal needs the server, so the app-server keeps running through app and
  daemon restarts. The terminals reconnect and resume the conversations they
  show.
- Each conversation is one session, with its own name, queue state, inbox and
  usage. A Codex terminal shows one conversation at a time. Launching Codex on
  a conversation an open session holds opens that session in a new tile
  rather than a second session.
- When no open shared session and no shared terminal needs the server, the
  server stops. The next shared Codex terminal starts the server again.

## Codex updates

Once a day Attn asks `codex doctor` whether a newer Codex is out. When a newer
Codex is out, a banner at the top of Attn offers it with **Install**. Close the
banner and the same offer stays under the bell in the sidebar. Install opens a
terminal tile running Codex's own update, so whatever the update prints, a
failure included, stays in that tile. Shared Codex terminals don't show Codex's
own update prompt, since Codex's Update now would exit the terminal, and exiting the terminal closes the session and archives the session's conversation.

Attn reads which Codex is installed when a shared terminal opens, when the
app-server starts, once a day, and when an update it ran finishes. If the installed Codex is not the Codex the app-server runs, Attn restarts the app-server on the installed Codex. Attn restarts it when no conversation is running a turn, including a turn that waits on an approval or on your answer to a question, and every shared terminal has finished starting. The banner names the sessions Attn waits on, and you can jump to each one.

- First Attn starts the installed Codex separately for a moment. If that Codex
  refuses to start, for example over a setting in your Codex config that the new
  version no longer supports, the app-server keeps running the Codex the
  app-server already ran and nothing is interrupted. The banner shows what Codex
  printed, with **Retry**. Attn also tries again once the installed Codex
  changes.
- **Restart now** doesn't wait. It first lists what each session would lose,
  then stops turns waiting on an approval or a question, gives running turns a
  minute to finish and stops the turns that still run. Terminals still starting
  relaunch.
- The terminals lose their connection during the restart and reconnect to the
  new app-server on their own. What you typed and haven't sent stays where it
  was. The old app-server first shuts down the MCP servers its conversations
  started, so with a few MCP servers the terminals wait up to about 20 seconds.
- A terminal that is still starting, or that is asking whether to trust its
  folder, can't reconnect. Attn waits to restart until that terminal shows its
  conversation.
- Until the restart, a new or reloaded terminal runs the app-server's Codex
  while that Codex is still installed. Codex's standalone installer keeps every
  release. npm and Homebrew replace the old release, so after an update through
  npm or Homebrew, new terminals run the new Codex against the old app-server
  until the app-server restarts. Codex warns about the mismatch in its footer.
- A terminal keeps the Codex it started with, so after the restart, terminals
  that started before the restart run an older Codex. The banner offers **Reload
  tiles**, which restarts those terminals on the new Codex in the same
  conversations. Text you typed in them and haven't sent is lost. An older
  terminal doesn't know what the newer app-server added. An approval of a kind
  the terminal doesn't know is denied, and the turn carries on.

The banner's actions are also in the command palette while they apply:
**Install Codex update**, **Restart shared Codex now** and **Reload old Codex
tiles**. From a terminal:

- `attn codex status` shows the Codex the app-server runs, the installed and
  latest ones, a restart and what the restart waits on, and the tiles still on
  an older Codex.
- `attn codex update` runs Codex's own update in your terminal, then has Attn
  read the installed Codex. After a plain `codex update`, Attn notices the new Codex at its next read.
- `attn codex restart` reads the installed Codex at once, then restarts the app-server once no turn runs, even if the installed Codex is the one the app-server already runs.
  `attn codex restart --now` does what Restart now does, without first listing
  what each session would lose.

## Switching conversations

`/new`, `/resume` and `/agents` change the conversation a terminal shows, and the terminal's tile shows that conversation's session at once, with the session's name and state.
The session you left stays open and **hidden**:

- The hidden session keeps its state, so a session that was waiting for you
  still waits. From then on the app-server tells Attn when the session's turn starts, when the turn waits on an approval or asks you a question, and when the turn ends.
  Switch a terminal back to the session with `/resume` or `/agents` and the
  terminal takes over again. An approval the session was waiting on shows in the
  terminal, and you answer it as usual.
- Annotations, messages from other agents and inbox notifications still reach
  the session, through the app-server. Attn doesn't touch the terminal's
  composer or whatever you were typing in it.
- The Sessions ledger shows the session marked `hidden`, and `attn session list`
  and `attn session show` say `(hidden)`.

Several terminals can show the same conversation. They show one session.

A conversation you start with `/new` is a session of its own right away, and
stays open and hidden if you leave it without a prompt, like any other session.
A new tile is the exception: until its first prompt, switching it to another
conversation leaves no empty session behind.

One switch shows its session late. When `/agents` takes a terminal back to a conversation that this terminal started and that you never prompted, Codex doesn't tell the app-server about the switch. The tile therefore shows that conversation's session only when you send the first prompt there.

A session keeps the approval mode it launched with, YOLO or auto-approve,
wherever its conversation opens: in any shared terminal with `/resume` or
`/agents`, when Attn resumes the session to deliver something while the session
is hidden, or when you resume the session from a stopped tile or the Sessions
ledger. A conversation you start with `/new` takes the mode of the session its
terminal shows.

## Hidden sessions in the queue

Hidden sessions queue like any other session, so a hidden session that needs an
answer appears in the queue without a desktop chip. Choose that session and Attn
opens the session in a tile on the current desktop, resuming the conversation
there. If the session was waiting on an approval, the approval waits in the
tile. Answer it as usual.

To keep hidden sessions out of the queue, turn off **Settings → Experimental → Show
hidden sessions in the queue**. They stay in the Sessions ledger.

## Names

The session's name in Attn and the conversation's name in Codex stay in sync: rename in either place and the other updates. If both change, Attn's name replaces Codex's.

## Closing and resuming

Closing a tile ends only its terminal when another tile shows the same
session. Closing the last tile of a session archives its conversation in Codex,
settles its usage and closes the session into the ledger. A hidden session
closes the same way.

To pick the session up again, resume the session from the Sessions ledger. Attn
unarchives the conversation and resumes it in a new tile, shared or plain as
the setting says.

## Limits

- Codex lets one process write a conversation at a time. While the app-server
  holds a conversation, `/resume` of it in a plain Codex fails with
  `already has an active writer`. `/resume` works once the app-server releases the conversation, about a minute after no terminal shows the conversation. The
  session then runs plain in that terminal.
- If Attn can't reach the app-server when a conversation starts, that
  conversation isn't written to disk and can't be resumed after a restart.
- A crash of the app-server stops the turns running in it. Attn starts the
  app-server again on the next connection, and the terminals reconnect and
  resume the conversation they show. Hidden sessions whose turns stopped go idle
  at once. A terminal shows its stopped turn as interrupted once it reconnects.
- A subagent's activity belongs to the session of the conversation that started it.
- In a shared terminal where you picked permissions with `/permissions`,
  Codex refuses `/resume` with `Permission overrides are not supported when
  resuming a remote task.` Resume the conversation from another shared
  terminal.

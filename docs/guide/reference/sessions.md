# Sessions

A session is Attn's record of one conversation with an agent. Attn keeps every
session, live and closed, so you can always find an agent again and bring it
back.

## Opening an agent

Cmd+N (Ctrl+Shift+N on Linux) opens the picker. Pick a harness or a plain shell,
a directory, and whether to work in a new worktree or the main checkout.

Attn runs Claude Code and Codex, and Pi through its plugin. You get
each harness as its makers built it, and you can still use its bare CLI whenever
you want.

## Worktrees

Worktrees let many agents work on the same codebase in parallel without
stepping on each other: each agent gets its own isolated checkout.

Attn creates worktrees for your agents and removes them automatically once
nothing would be lost: no agent running in it, no open seed pointing at it, no
uncommitted, unmerged or unpushed work, and two weeks without activity.

You can see and manage your worktrees in the ledger's Worktrees tab, and keep
one forever from there.

![The ledger's Worktrees tab lists each worktree with its branch and why it's still around, and every row has a Keep button](../images/ledger-worktrees.png)

## The terminal

- Cmd+F (Ctrl+Shift+F on Linux) searches the whole scrollback.
- Cmd-click (Ctrl-click on Linux) a path or URL to open it. A `.md` file opens
  in an adjacent tile.
- If your shell marks commands (fish does by default), click a command's output
  to select the whole block. Cmd+C (Ctrl+Shift+C on Linux) copies the command
  and its output, Cmd+Shift+C (Ctrl+Alt+C on Linux) only the command.

## Durability

Restarting the app or the daemon never interrupts your agents. After your
machine reboots, your sessions come back, and each one restarts when you open
it.

## The ledger

Cmd+Shift+L (Ctrl+Alt+L on Linux) opens the ledger: every session, live and
closed, plus the worktrees and conversations they used. When you close an agent,
it waits here.

![The ledger icon is the third one in the row of icons at the top of the sidebar](../images/ledger-button.png)

## Resume

Resume brings a closed session back as itself, in a tile, with its conversation.

Attn keeps its own copy of conversations a harness would delete. Keep one
forever with "Keep conversation" on its session in the ledger, and forget it from
the Conversations tab.

## Reload

"Reload this agent" in the command palette restarts an agent in place with its
saved conversation. Running work is interrupted and non-persistent state may
be lost.

`attn agent reload <session-or-member-or-seed>...` does the same from the CLI.
Targets resolve inside the caller's profile. A crew name follows the member's
current session, including after a rename. Sleeping members stay asleep.
A session may reload itself and its delegates, including delegates of delegates.
The Chief may reload any agent in the profile, or use `--all` to include every
agent there. Shells are excluded. Run `attn agent reload --help` for flags and
batch results.

## Details

<details>
<summary>Why did my worktree disappear?</summary>

The sweep removed it once it had gone 14 days without activity with nothing left to lose. The sweep runs every hour and covers every worktree of a repository where Attn has made one, including worktrees you added with git yourself. Removing a worktree also deletes its local branch, unless an open seed can still continue from it, and leaves a note on the seeds that worked there. The Worktrees tab's Removed list shows when it went and why, and resuming a session that ran there offers "Recreate the worktree" while the branch is still local, or "Fetch, recreate, reopen" when only a remote has it.
</details>

<details>
<summary>Does closing an agent's tile stop the agent?</summary>

Yes. Cmd+W (Ctrl+Shift+W on Linux) on an agent's tile, or Close session in the ••• menu on its sidebar row, stops the agent and puts its session in the ledger as closed by you, ready to resume. On a tile that isn't a terminal, such as markdown or a browser, Cmd+W closes only that tile, and a crew member's session (the Chief's included) refuses to close until you put the member to sleep. An agent that exits cleanly closes its session the same way. When it crashes or something kills it, its tile stays and says "Agent stopped", with Resume to continue the conversation and Close to close the session.
</details>

<details>
<summary>Why can't I resume a closed session?</summary>

Resume starts the session's own harness with the model and effort it launched with, in its saved directory, and warns you when that directory is on a different branch now. The ledger refuses and says why when the harness isn't installed or can't resume, when the harness no longer has the conversation and Attn has no copy (the reason says when a copy was deleted), or when the session's profile was deleted. When the directory is gone, it offers "Recreate the worktree" while the branch is still local, or "Fetch, recreate, reopen" when only a remote has it, and nothing when it wasn't a worktree or the branch is gone or checked out somewhere else. `attn session show <id>` prints the same verdict, and `attn session reopen <id> --action recreate_worktree_and_reopen` (or `fetch_recreate_and_reopen`) runs those actions from the terminal.
</details>

<details>
<summary>Why is Attn keeping my worktree?</summary>

Its row in the Worktrees tab, and `attn worktree list`, names the first thing keeping it. In order, Attn checks whether you kept it forever, an agent is running in it, an open seed points at it, it has uncommitted, untracked or stashed changes, a detached HEAD holds commits that are on no branch, its branch hasn't merged, or it has commits the merge doesn't account for. A branch counts as merged when GitHub shows its pull request merged, or when its work is already in the branch the repository merges into (named next to the repository), squash merges included. Idle time starts at the newest file change, commit or creation, and a younger worktree shows a "removing in" countdown that only means Attn looks closer when it ends, so anything above can still keep it.
</details>

<details>
<summary>What happens to my agents when the daemon restarts or my machine reboots?</summary>

Your agents run outside the daemon, so a daemon restart leaves them running unless Settings > Terminal shows "Embedded in daemon". Attn falls back to that mode, with a warning, when its terminal workers fail to start, and in it a daemon restart stops every agent. After a reboot, or after an embedded daemon restarts, each session stays on its desktop marked recoverable, and opening it starts its harness again with the conversation resumed.
</details>

<details>
<summary>Which conversations does Attn keep a copy of?</summary>

Only Claude Code's, since Claude Code prunes its conversations after 30 days and the other harnesses keep their own. Attn copies a conversation while it belongs to the latest delegation of an open seed, or once you keep it with Keep conversation on its row in the Sessions tab or `attn conversation keep <session-id>`, waiting until a live conversation has been quiet for a day. The copies live compressed in `~/.attn/conversations/`, and when you resume a conversation Claude Code no longer has, Attn puts its copy back first. Once no open seed or pin holds a copy, Attn deletes it 14 days later (the Conversations tab shows the date), and Forget deletes it right away without touching Claude Code's files, except while an open seed still needs it.

If a crew home or memory cannot be read, Attn keeps copying and listing other conversations and pauses expiry. A warning names the unavailable paths. Expiry resumes once they are readable again.
</details>

<details>
<summary>How do I keep a worktree forever, or let it go again?</summary>

Keep on its row in the Worktrees tab, or `attn worktree keep <path>`, pins it so the sweep never removes it, and Unpin (`attn worktree unkeep <path>`) lets the next pass decide again. Turning off Worktree sweep in Settings > Files and locations stops automatic removals everywhere and keeps your pins. Worktrees it would remove then say "eligible now; the sweep is off". The refresh link at the bottom of the Worktrees tab, or `attn worktree refresh`, runs a pass now instead of waiting for the hourly one.
</details>

<details>
<summary>How do I find an old session in the ledger?</summary>

The Sessions tab shows Live, Closed or All, and the filter narrows it with `repo:name`, `profile:name`, `today`, `yesterday`, `7d`, `30d`, `from:2026-09-01 to:2026-09-05`, `dir:path` or plain words. Words and `dir:` search only the rows already loaded, so pull in older ones from the "older" link in the status bar first. Your Live, Closed or All choice, repository, profile and dates stay set the next time you open the ledger, and `attn session list --closed --last 7d` reads the same ledger from the terminal. Inside the ledger, / jumps to the filter, [ and ] switch tabs, Enter runs a row's main action, . shows its other actions, and ? lists every key.
</details>

<details>
<summary>What happens when I delete a worktree myself?</summary>

Delete on its row in the Worktrees tab, or `attn worktree delete <path>`, removes the checkout and closes every session still running in it. It also deletes the worktree's local branch, even when the branch isn't merged, unless an open seed can still continue from it. A worktree with uncommitted changes asks you to confirm "Delete, losing changes" (`--force` on the CLI), and the Removed list records it as deleted at your request.
</details>

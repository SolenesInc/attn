# Sessions

A session is Attn's record of one conversation with an agent. Attn keeps every
session, live and closed, so you can always find an agent again and bring it
back.

## Opening an agent

Cmd+N (Ctrl+Shift+N on Linux), or New at the top of the sidebar, opens the
picker. Pick a harness or a plain shell, then a directory. In a git repository,
Tab moves on to where the agent works: a new worktree (type to name its
branch), the main checkout, or a worktree that already exists. Enter starts it.

Attn runs Claude Code and Codex, and Pi through its plugin. You get each harness
as its makers built it, and you can still use its bare CLI whenever you want.
Nisse is experimental and off by default. See [Using Nisse](using-nisse.md).

## Worktrees

Worktrees let many agents work on the same codebase in parallel without
interfering with each other: each agent gets its own isolated checkout.

Attn creates worktrees for your agents. Attn removes a worktree automatically
once nothing would be lost, which means the worktree has no agent running in it,
no open seed pointing at it, no uncommitted, unmerged or unpushed work, and two
weeks without activity.

You can see and manage your worktrees in the ledger's Worktrees tab, and keep
one forever from there.

![The ledger's Worktrees tab lists each worktree with its branch and why it's still around, and every row has a Keep button](../images/ledger-worktrees.png)

## The terminal

- Cmd+F (Ctrl+Shift+F on Linux) searches the whole scrollback.
- Agents like Claude Code track the mouse, so a plain drag goes to the agent.
  Hold Option (Alt on Linux) while you drag to select text instead.
- When an agent copies text for you, Attn honors the terminal's OSC 52
  clipboard sequence and puts the text on your clipboard, even from a remote machine.
- Cmd-click (Ctrl-click on Linux) a path or URL to open it. A `.md` file opens
  in a markdown tile next to the agent.
- If your shell marks commands (fish does by default), click a command's output
  to select the whole block. Cmd+C (Ctrl+Shift+C on Linux) copies the command
  and its output, Cmd+Shift+C (Ctrl+Alt+C on Linux) only the command.
  Right-click a block for Copy output, Filter block output, and scrolling to
  its top or bottom.

## Durability

Restarting the app or the daemon doesn't interrupt your agents. After your
machine reboots, your sessions come back, and each one restarts when you open
it.

## The ledger

Cmd+Shift+L (Ctrl+Alt+L on Linux), or the Ledger icon at the top of the
sidebar, opens the ledger: every session, live and closed, plus the worktrees
and conversations they used, in the Sessions, Worktrees and Conversations tabs.
When you close an agent, it waits here.

![The ledger icon is the third one in the row of icons at the top of the sidebar](../images/ledger-button.png)

## Resume

Resume brings a closed session back as itself, in a tile, with its
conversation. The ledger labels this action **Reopen**. The session opens on
the desktop it was last on. When that desktop is gone, the FAQ "Where does an
agent open when I resume it?" in [Desktop flow](desktops.md) says where it
opens.

Attn keeps its own copy of conversations a harness would delete. To keep a copy
forever, click Keep conversation on the session in the ledger. To forget a copy,
open the Conversations tab.

## Reload

Reload restarts an agent in place with its saved conversation. "Reload this
agent" in the command palette (Cmd+Shift+K, or Ctrl+Alt+K on Linux), and Reload
session in the ••• menu on the agent's sidebar row, both run it. Reload
interrupts running work, and non-persistent state may be lost.

Agents can do the same with `attn agent reload <session-or-member>...`. A
session may reload itself and the agents it delegated to, including delegates
of delegates, and the Chief may reload any agent in its profile, or
every agent there with `--all`. A crew member's name reaches the member's
current session, and a sleeping member stays asleep. Shells are never reloaded. Run
`attn agent reload --help` for the details.

## Details

<details>
<summary>Why did my worktree disappear?</summary>

The sweep removed it once it had gone 14 days without activity with nothing left to lose. The sweep runs every hour and covers every worktree of a repository where Attn has made one, including worktrees you added with git yourself. Removing a worktree also deletes its local branch, unless an open seed can still continue from the branch, and leaves a note on the seeds that worked there. The Worktrees tab's Removed list shows when the worktree went and why. When you resume a session that ran there, the ledger offers "Recreate the worktree" while the branch is still local, or "Fetch, recreate, reopen" when only a remote has it.
</details>

<details>
<summary>Does closing an agent's tile stop the agent?</summary>

Yes, when it is the agent's only tile. Cmd+W (Ctrl+Shift+W on Linux) on it, or Close session in the ••• menu on its sidebar row, stops the agent and puts its session in the ledger as closed by you, ready to resume. When another tile still shows the same agent, Cmd+W closes only the tile you're in. On a tile that isn't a terminal, such as markdown or a browser, Cmd+W closes only that tile. Attn refuses to close the Chief's session until you remove its chief role (see [the Chief](chief.md)), and refuses to close a crew member's session until you put the member to sleep. An agent that exits cleanly closes its session the same way. When the agent crashes or something kills it, the agent's tile stays and says "Agent stopped", with Resume to continue the conversation and Close to close the session.
</details>

<details>
<summary>Why can't I resume a closed session?</summary>

Resume starts the session's own harness with the model and effort it launched with, in its saved directory, and warns you when that directory is on a different branch now. The ledger refuses and says why when the harness isn't installed or can't resume, when Attn saved no launch settings or conversation id for the session, when the harness no longer has the conversation and Attn has no copy (the reason says when a copy was deleted), or when the session's profile was deleted. When the directory is gone, the ledger offers "Recreate the worktree" while the branch is still local, or "Fetch, recreate, reopen" when only a remote has it, and offers neither action when the directory wasn't a worktree, when the branch is gone, or when the branch is checked out somewhere else. `attn session show <id>` prints the same verdict, and `attn session reopen <id> --action recreate_worktree_and_reopen` (or `fetch_recreate_and_reopen`) runs those actions from the terminal.
</details>

<details>
<summary>Why is Attn keeping my worktree?</summary>

The worktree's row in the Worktrees tab, and `attn worktree list`, name the first thing keeping the worktree, and selecting the row shows the full reason. Attn checks these conditions in order:

1. You kept the worktree forever.
2. An agent is running in the worktree.
3. An open seed points at the worktree.
4. The worktree has uncommitted, untracked or stashed changes.
5. A detached HEAD holds commits that are on no branch.
6. The worktree's branch hasn't merged.
7. The worktree has commits the merge doesn't account for.

A branch counts as merged when GitHub shows its pull request merged, or when its work is already in the branch the repository merges into (named next to the repository), squash merges included. Idle time starts at the newest file change, commit, or creation. A younger worktree shows a "removing in" countdown. The countdown only means that Attn looks more closely when it ends, so anything above can still keep the worktree.
</details>

<details>
<summary>What happens to my agents when the daemon restarts or my machine reboots?</summary>

Your agents run outside the daemon, so a daemon restart leaves them running. If the daemon can't start its terminal workers, it falls back to running terminals inside itself and the app shows a warning that says "Falling back to embedded". In that mode a daemon restart stops every agent. After a reboot, or after an embedded daemon restarts, each session stays on its desktop marked recoverable, and opening it starts its harness again with the conversation resumed.
</details>

<details>
<summary>Which conversations does Attn keep a copy of?</summary>

Only Claude Code's, since Claude Code prunes its conversations after 30 days and the other harnesses keep their own. Attn copies a conversation while the conversation belongs to the latest delegation of an open seed, while a crew member's letter points at the conversation's session (only letters filed with Crew v2 on record their session), or once you keep the conversation with Keep conversation on its row in the Sessions tab, Keep forever in the Conversations tab, or `attn conversation keep <session-id>`. Attn waits until a live conversation has been quiet for a day before copying. The copies live compressed in `~/.attn/conversations/`, and when you resume a conversation Claude Code no longer has, Attn puts the copy back first. Once nothing holds a copy, Attn deletes the copy 14 days later (the Conversations tab shows the date). Forget deletes the copy right away without touching Claude Code's files. Attn refuses to forget a copy while an open seed still needs the copy, and when crew letters hold the copy, Attn names the letters. `attn conversation forget <id> --unlink-crew-letters` forgets the copy anyway and leaves the letters in place.

If a crew member's home or letters cannot be read, Attn keeps copying and listing other conversations and pauses expiry, and Forget refuses until they are back. A warning names the unavailable paths. Expiry resumes once they are readable again.
</details>

<details>
<summary>How do I keep a worktree forever, or let it go again?</summary>

Keep on the worktree's row in the Worktrees tab, or `attn worktree keep <path>`, pins the worktree so the sweep never removes it, and Unpin (`attn worktree unkeep <path>`) lets the next sweep decide again. Turning off Worktree sweep in **Settings → Files and locations** stops automatic removals everywhere and keeps your pins. Worktrees the sweep would remove then say "eligible now; the sweep is off". The refresh link at the bottom of the Worktrees tab, or `attn worktree refresh`, runs a sweep now instead of waiting for the hourly one.
</details>

<details>
<summary>How do I find an old session in the ledger?</summary>

The Sessions tab shows Live, Closed or All, and the filter narrows it with `repo:name`, `profile:name`, `today`, `yesterday`, `7d`, `30d`, `from:2026-09-01 to:2026-09-05`, `dir:path` or plain words. Words and `dir:` search only the rows already loaded, so load older rows from the "older" link in the status bar first. Your Live, Closed or All choice, repository, profile and dates stay set the next time you open the ledger, and `attn session list --closed --last 7d` reads the same ledger from the terminal.

Keys in the ledger:

- `/` jumps to the filter.
- `[` and `]` switch tabs.
- `Enter` runs a row's main action.
- `.` shows the row's other actions.
- `1` to `9` run the nth action.
- `y` copies the row's path.
- `?` lists every key.
</details>

<details>
<summary>What happens when I delete a worktree myself?</summary>

Delete… on its row in the Worktrees tab, Cmd+Backspace (Ctrl+Backspace on Linux) on it in the picker, or `attn worktree delete <path>`, removes the checkout and closes every session still running in it. Attn also deletes the worktree's local branch, even when the branch isn't merged, unless an open seed can still continue from the branch. For a worktree with uncommitted changes, Attn asks you to confirm "Delete, losing changes" (`--force` on the CLI), and the Removed list records the deletion as made at your request.
</details>

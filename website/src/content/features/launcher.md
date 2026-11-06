---
title: The launcher
summary: Pick a project, a worktree and a machine in one dialog, and start an agent or a terminal.
order: 4
guide:
  - link: sessions#the-launcher
    label: The launcher, the full guide
  - link: sessions#opening-an-agent
    label: Opening an agent
  - link: sessions#worktrees
    label: Worktrees
  - link: using-outposts#start-on-an-outpost-from-the-launcher
    label: Start on an outpost from the launcher
---

## What it does for you

Type a project or a path, choose where the agent works, and press Enter. You pick the path, the repository and the worktree in a single dialog.

## How you use it

Search for "launcher" in Settings and turn it on first.

Cmd+N (Ctrl+Shift+N on Linux) opens the launcher for a new agent. Cmd+T (Ctrl+Shift+T on Linux) opens it with a shell picked.

- ↑ and ↓ move through the list, and Enter starts on the selected row.
- For an agent, Tab on a repository opens its worktree choices. Type to name a new worktree, or pick the main checkout or a worktree that already exists.
- Tab on any other row, and on every row in a terminal launcher, puts the row's address in the input.
- Cmd+Enter (Ctrl+Enter on Linux) opens a terminal at the selected place instead of starting an agent.
- Esc goes back a step, or closes the launcher.

With an [outpost](/features/outposts) added, recent projects from every machine share one list, and each row shows its machine. An address names a machine and a place on it, the way ssh writes it:

```text
ai-sandbox:~/projects/    lists folders on ai-sandbox
ai-sandbox:attn           searches recent projects and starts the result on ai-sandbox
```

If the machine is unreachable, or lacks the harness you picked, the launcher refuses the start and shows the reason.

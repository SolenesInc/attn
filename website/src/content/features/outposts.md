---
title: Outposts
summary: Run agents and shells on another computer, in tiles beside your local ones.
order: 5
guide:
  - link: using-outposts#add-an-outpost
    label: Add an outpost
  - link: using-outposts#run-agents-on-an-outpost
    label: Run agents on an outpost
  - link: using-outposts#work-while-an-outpost-is-offline
    label: Work while an outpost is offline
  - link: using-outposts#notebook-sync
    label: Notebook sync
---

## What it does for you

An outpost is another computer running Attn, such as a Linux machine or a cloud VM, that your home connects to. Your home is the Attn on the Mac where you use the app.

Agents run on the outpost. Their tiles sit beside your local terminals, with the outpost's name under the title, and the agents' state, turns, cost and PR links reach the Mac. A lost connection leaves a shell running, and shells survive a restart of either daemon. Your Notebook folders sync both ways, and an agent on the outpost works in its copy.

## How you use it

An outpost is a Linux amd64 or arm64 machine that you can reach with your own `ssh` command.

1. Run `ssh <destination>` in your terminal and answer any authentication or host-key prompts.
2. Open **Settings → Outposts**, enter the **ssh destination**, and click **Add outpost**.
3. Install and log in to Claude Code or Codex on the outpost.

Attn installs your home's Linux build, starts the daemon over ssh, and pairs the two. No systemd service or root access is needed.

Then start work there from the [launcher](/features/launcher), or from an agent at the home:

```sh
attn terminal open --host ai-sandbox
attn delegate --seed s-example --role builder --host ai-sandbox --cwd '~/project'
```

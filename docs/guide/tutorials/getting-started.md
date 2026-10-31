# Getting started

From a fresh install to your first agent.

## Before you install

You need:

- At least one agent CLI installed: Claude Code, Codex or Pi. Attn runs them as
  they are, so install and log into the one you use. Pi runs through the
  attn-pi plugin that ships with Attn: once Attn is installed, open
  **Settings → Plugins** and install it.
- The [GitHub CLI](https://cli.github.com/) (`gh`) 2.81 or newer, if you want
  the PR features.

## Install

### Mac (Apple Silicon)

Use Homebrew or the direct installer. We recommend Homebrew.

```bash
brew tap victorarias/attn https://github.com/victorarias/attn
brew install --cask victorarias/attn/attn
```

Or grab the DMG from the
[latest release](https://github.com/victorarias/attn/releases/latest), open it
and drag Attn to Applications.

To update:

```bash
brew update && brew upgrade --cask victorarias/attn/attn
```

The app tells you when a new release is out. When a new version needs to
upgrade the database, Attn first saves a snapshot in `~/.attn/backups/`. Attn
is designed to restart without interrupting you or your agents, so you can
update at any time.

### Linux

The Linux app is still in progress, and releases don't package it yet, so build
it from source. We test on Ubuntu 24.04.

You need Go 1.27.1 or newer, Rust through rustup (the repository pins the
version it builds with), Node.js 26 or newer, pnpm 9 or newer, bun 1.3.14 or
newer, and these packages:

```bash
sudo apt install build-essential pkg-config libwebkit2gtk-4.1-dev \
  libayatana-appindicator3-dev librsvg2-dev libssl-dev libxdo-dev \
  desktop-file-utils xdg-utils
```

Then:

```bash
git clone https://github.com/victorarias/attn.git && cd attn
make
```

`make` builds the app and the daemon, installs them in `~/.local/share/attn`
(`$XDG_DATA_HOME/attn` if you set it), and launches the app. `make install`
skips the launch; start the app later with `~/.local/share/attn/bin/attn-app`.
To update, pull and run `make` again; source builds don't check for releases.

## The home screen

Welcome to Attn! On first start, Attn opens a short intro to profiles,
desktops, the queue and the palettes. You can replay it from the banner on the
home screen, or with **What's new** in the command palette (Cmd+Shift+K,
Ctrl+Alt+K on Linux).

Then you see your home screen. Cmd+Shift+H (Ctrl+Alt+H on Linux) brings you
back to it at any time.

![The home screen, with the sidebar on the left, then your sessions grouped by state, the Chief of Staff card and your pull requests](../images/home-screen.png)

This is where you keep an eye on all your agents and your open PRs:

- **Sessions** lists every agent, grouped by state. Click one to go to it, or
  click **+ New** to start one.
- **Chief of Staff** shows your Chief's session once you have one. The Chief
  is an agent that keeps track of your other agents, delegates work for you and
  journals your day in the Notebook.
  Make any agent your Chief with **Make chief of staff** in its ••• menu, and
  take the role away with **Remove chief role**.
- **Pull Requests** lists your PRs and the ones waiting for your review, with
  their CI status. It needs `gh`.

The gear at the top right opens Settings (Cmd+, or Ctrl+Shift+, on Linux).

The sidebar on the left lists your desktops and the tiles on them. Cmd+B
(Ctrl+Alt+B on Linux) collapses and expands it.

## Start an agent

Cmd+N (Ctrl+Shift+N on Linux) starts an agent: pick a harness or a plain shell,
pick a directory, go. It opens on the desktop you're on, beside the tile you're
looking at. Cmd+Shift+N (Ctrl+Alt+N on Linux) opens it below instead. Cmd+T
(Ctrl+Shift+T on Linux) opens the same picker with a plain shell chosen.

## Know who needs you

Attn was built to help you run many agents in parallel while keeping the mental
cost low. It does that by removing every bit of friction it can, so you spend
your energy on the decisions that matter.

The most basic way it helps is using colors to signal state. Instead of jumping
between agents to find out whether they need you, Attn tracks each agent's state
and shows it as a colored dot, so you scan for colors instead of reading
terminals:

| Dot                 | State          | What it means                                                       |
| ------------------- | -------------- | ------------------------------------------------------------------- |
| 🟢 Green            | Working        | Busy. Leave it alone.                                               |
| 🟡 Amber            | Waiting        | It ended its turn asking you something, or it stopped on an error.  |
| 🟡 Yellow, flashing | Needs approval | Blocked on a prompt you must answer, like a tool approval. Go answer it. |
| 🔵 Blue, slow pulse | Scheduled      | Parked on a schedule it set. It will continue by itself.            |
| ⚪ Gray             | Idle           | Finished, with nothing to ask you.                                  |
| ⚪ Gray             | Recoverable    | Stopped, and Attn can bring it back.                                |
| 🟣 Purple           | Unknown        | Attn can't tell what it's doing; it may be an error. Hover the dot to see why. |
| An emoji            | Launching      | Starting up.                                                        |

The home screen groups your agents by the same states.

The colors work the same in desktop flow, the default, and in queue flow. Queue
flow goes one step further: it takes "which agent do I go to next?" off your
plate by bringing you the next one that needs you. Less deciding, more
throughput. See [queue](../reference/queue.md).

## Where next

You can now use Attn as a basic orchestrator, congrats! Now comes the fun part:
getting a lot more done by orchestrating dozens of agents at the same time.

- Arrange your work on [desktops](../reference/desktops.md), and find, resume
  and close agents with [sessions](../reference/sessions.md).
- Hand work off to other agents with [delegation](../reference/delegation.md).
- Get much more done with delegation roles:
  [building with roles](building-with-roles.md).
- Go hardcore with the agent [queue](../reference/queue.md).

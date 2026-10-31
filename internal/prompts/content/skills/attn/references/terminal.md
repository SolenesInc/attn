# Terminals beside your agent

Use `attn terminal` inside an attn agent to open shell splits, inspect terminals,
type into them, wait for a command or terminal to finish, move terminals between
desktops, and close shells.
Run `attn terminal --help` for the full syntax and supported key names.

```sh
attn terminal open -- pnpm dev
attn terminal ls --json
attn terminal read <id>
attn terminal send <id> --keys C-c
attn terminal run <id> "make test" --wait
attn terminal read <id> --last
attn terminal move <id> 8
attn terminal close <id>
```

## Placement and lifetime

`open` keeps the user's focus where it was. Your first terminal goes to your
right, and `--down` puts it below you. Later terminals stack below your most
recently opened live terminal on that desktop. With `--desktop <ref>`, the
first split starts at that desktop's active tile. `--cwd <dir>` selects the
directory on the target machine. attn creates a missing numbered `--desktop`.

`open` defaults to your session's machine and directory. `--host <name|id>`
selects an outpost, and `--host home` selects the home. If you change the
machine and omit `--cwd`, the shell starts in that machine's home directory.

`open -- command` waits for the shell's first OSC 133 prompt before it types
the command. It refuses known unsupported shells before opening. If the wait
for the first prompt exceeds 30 seconds, the command fails with the terminal's
id, leaves the terminal open, and types nothing later. In the startup spike,
first prompts took between 9 and 176 ms across fish, zsh and bash. The
30-second limit catches broken startup or integration that startup files
disabled. Use `send` to continue in that terminal. For a shell without command
marks, open without a command.

An opened terminal is an ordinary interactive shell. The command enters the
shell's history, so the user can recall it with ↑. The terminal records you as
its parent and keeps running after your session closes. Close the terminal
when its work is done.

## Reach

A target is a terminal id, or a unique prefix of one, as printed by `ls`.
`open` prints the new terminal id. A session id or prefix, or an awake crew
member's name, also works when that session shows in exactly one terminal.
Shared Codex can show one session in several tiles. When it does, choose a
terminal id. Every target belongs to your profile. A crew name follows the
member's current session and never wakes a sleeping member.

If your session has tiles on several desktops, pass `--desktop` when you list,
type, or close. If your session has several tiles on one desktop, `open` also
needs `--desktop` to select that desktop's active tile as the anchor.

- `ls` shows your desktop. `--all` shows all desktops in your profile.
- `read` and `wait` can inspect any terminal in your profile. `move` also
  reaches every desktop without `--desktop`.
- `open`, `run`, `send` and `close` on another desktop require an explicit
  `--desktop <ref>`. A refusal prints the value to pass.
- Any agent can type into or move a shell. You can type into or move an agent
  only when the agent is you or below you in your delegation chain, including
  delegates' delegates. Your profile's chief can type into or move any agent in
  the profile.
- `close` closes shells. Use `attn agent close` to close an agent.

Typing goes straight to the terminal, as keyboard input does. `send --text`
writes literal bytes and does not press Enter for you. To submit text, use
`send --keys Enter`. To run a shell command, use `run`. `send --keys C-c`
interrupts the foreground program.

## Move

`attn terminal move [<terminal>] <desktop>` moves one terminal and keeps the
terminal's process running. Without a target, the command moves your session's
terminal. A session shown in several terminals needs an explicit terminal id.

```sh
attn terminal move 8
attn terminal move <shell-id> 8
```

The arguments work in either order. A `<desktop>` is the desktop's shortcut
digit, its name as shown (case-insensitive), or its id. attn creates a missing
numbered desktop on its shortcut slot. Listing, typing, and closing with
`--desktop` require an existing desktop.

The terminal goes beside the destination's active tile. Your profile's current
desktop never changes. The moved tile becomes active at the destination only
when it was active at the source and the destination is not current. Moving an
agent whose tile was removed gives the agent's existing terminal a tile at the
destination.

## Reading and waiting

`read` returns the rendered screen. `read --scrollback` includes retained
history, and `--lines <n>` selects the last n lines of that history.
`read --last` returns the last completed command's output. When output has been
discarded from scrollback, `read` fails with an explicit error.

`run` requires a shell at its prompt. If a program is running, use `send` to
talk to the program. `run --wait` reads the next completed command block and
exits with the block's exit code. To attribute a block to a command, avoid
concurrent writers to that shell. Shells without OSC 133 command marks do not
support `run --wait`, `read --last`, or `wait --until prompt`. In those shells,
use plain `run` and screen reads.

`wait --until prompt` waits for the shell prompt. `wait --until exit` waits
for the terminal process to exit. `--timeout <seconds>` bounds either wait and
`run --wait`. When the timeout expires, the command exits 124. Without
`--timeout`, waiting continues until the condition is met or your CLI
connection closes. If the worker restarts or disconnects, the call fails
visibly. It never leaves a command queued for later.

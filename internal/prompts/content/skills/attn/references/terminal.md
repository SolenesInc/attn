# Terminals

Use `attn terminal` inside an attn agent to open terminals, inspect them, type into them, wait for a command or terminal to finish, move terminals between desktops, and close shells.

## Placement and lifetime

`attn terminal open` keeps the user's focus where it was. A terminal goes to the end of the desktop's row. `--down` stacks it below you, or below the terminal you most recently opened in your column. `--desktop <ref>` opens it on that desktop, and with `--down` it stacks below that desktop's active tile. attn creates a missing numbered `--desktop`. `--cwd <dir>` selects the directory on the target machine.

`attn terminal open` defaults to your session's machine and directory. `--host <name|id>` selects an outpost, and `--host home` selects the home. If you change the machine and omit `--cwd`, the shell starts in that machine's home directory. Claude, Codex and shells run on outposts. An agent's attn commands reach the home. Spawning chief, crew and plugin agents on outposts is unavailable.

`attn terminal open` with a command waits for the shell's first OSC 133 prompt, then types the command. Before it opens a terminal, `attn terminal open` refuses known unsupported shells. If the first prompt takes more than 30 seconds, `attn terminal open` fails and reports the terminal's id. It leaves the terminal open and types nothing. A healthy start takes far less: first prompts took between 9 and 176 ms across fish, zsh and bash. The 30-second limit catches a broken startup, or startup files that turned the shell integration off. To continue in that terminal after a failure, use `send`. For a shell without command marks, open without a command.

An opened terminal is an ordinary interactive shell. The command enters the shell's history, so the user can recall it with ↑. The terminal records you as its parent and keeps running after your session closes. Close the terminal when its work is done.

    attn terminal open [--host <outpost|home>] [--down] [--desktop <ref>] [--cwd <dir>] [-- <command>...] [--json]

Opens a shell at the end of your desktop's row, keeping the user's focus where it was, and returns its terminal id. `--down` stacks it below you, or below the terminal you most recently opened in your column. `--desktop` opens it on that desktop, creating a missing numbered one. `--host` selects an outpost by name or id, or home; `--cwd` names a directory on that machine, and without it a shell on another machine starts in its home directory. With the words after `--`, open waits up to 30 s for the shell's first OSC 133 prompt, then types the command.

    attn terminal open -- pnpm dev

    attn terminal close <terminal> [--desktop <ref>] [--json]

Closes the shell `<terminal>`. It closes shells only; close an agent with `attn agent close`.

## Reach

A target is a terminal id, or a unique prefix of one, as printed by `ls`. `attn terminal open` prints the new terminal id. A session id or prefix, or the name of a crew member in session, also works when that session shows in exactly one terminal. Shared Codex can show one session in several tiles. When it does, choose a terminal id. Every target belongs to your profile. A crew name follows the member's current session and never starts a session for a member between sessions.

If your session has tiles on several desktops, pass `--desktop` when you list, type, or close. If your session has several tiles on one desktop, `attn terminal open` also needs `--desktop` to select that desktop's active tile as the anchor.

- `ls` shows your desktop. `--all` shows all desktops in your profile.
- `read` and `wait` can inspect any terminal in your profile. `move` also reaches every desktop without `--desktop`.
- `attn terminal open`, `run`, `send` and `close` on another desktop require an explicit `--desktop <ref>`. A refusal prints the value to pass.
- Any agent can type into or move a shell. You can type into or move an agent only when the agent is you or below you in your delegation chain, including delegates' delegates. Your profile's chief can type into or move any agent in the profile.
- `close` closes shells. Use `attn agent close` to close an agent.

Typing goes straight to the terminal, as keyboard input does. `send --text` writes literal bytes and does not press Enter for you. To submit text, use `send --keys Enter`. To run a shell command, use `run`. `send --keys C-c` interrupts the foreground program.

Keys: C-a through C-z, C-\, C-], C-^, C-_, Escape, Enter, Tab, Backspace, Space, Up, Down, Left, Right, Home, End, Delete, Insert, PageUp, PageDown, F1 through F12. send writes bytes directly, as a keyboard does.

    attn terminal ls [--desktop <ref>] [--all] [--json]

Lists the terminals of your desktop, or of the desktop `--desktop` names, or with `--all` of every desktop in your profile: each one's id, name, agent, state, desktop, position and the session that opened it.

    attn terminal send <terminal> [--text "<text>" | --keys <key>...] [--desktop <ref>] [--json]

Types into `<terminal>` as a keyboard does: `--text` as literal bytes, without Enter, or `--keys` by name: C-a through C-z, C-\\, C-], C-^, C-_, Escape, Enter, Tab, Backspace, Space, Up, Down, Left, Right, Home, End, Delete, Insert, PageUp, PageDown, F1 through F12. Anyone may type into a shell; into an agent only when it is you or below you in your delegation chain, or you are the profile's chief.

    attn terminal send 4f2a --keys C-c

## Move

`move` moves one terminal and keeps the terminal's process running. Without a target, the command moves your session's terminal. A session shown in several terminals needs an explicit terminal id.

A `<desktop>` is the desktop's shortcut digit, its name as shown (case-insensitive), or its id. attn creates a missing numbered desktop on its shortcut slot. Listing, typing, and closing with `--desktop` require an existing desktop.

The terminal goes beside the destination's active tile. Your profile's current desktop never changes. The moved tile becomes active at the destination only when it was active at the source and the destination is not current. Moving an agent whose tile was removed gives the agent's existing terminal a tile at the destination. Move yourself and a shell with two calls:

    attn terminal move 8
    attn terminal move <shell-id> 8

    attn terminal move [<terminal>] <desktop> [--json]

Moves `<terminal>`, or your own session's terminal, to `<desktop>`: its shortcut digit, its name as shown or its id. A missing numbered desktop is created. The terminal keeps running beside the destination's active tile. Anyone may move a shell; an agent only when it is you or below you in your delegation chain, or you are the profile's chief.

    attn terminal move 8

## Reading and waiting

`read` returns the rendered screen. `read --scrollback` includes retained history, and `--lines <n>` selects the last n lines of that history. `read --last` returns the last completed command's output. When output has been discarded from scrollback, `read` fails with an explicit error.

`run` requires a shell at its prompt. If a program is running, use `send` to talk to the program. `run --wait` reads the next completed command block and exits with the block's exit code. To attribute a block to a command, avoid concurrent writers to that shell. Shells without OSC 133 command marks do not support `run --wait`, `read --last`, or `wait --until prompt`. In those shells, use plain `run` and screen reads.

`wait --until prompt` waits for the shell prompt. `wait --until exit` waits for the terminal process to exit. `--timeout <seconds>` bounds either wait and `run --wait`. When the timeout expires, the command exits 124. Without `--timeout`, waiting continues until the condition is met or your CLI connection closes. If the worker restarts or disconnects, the call fails visibly. It never leaves a command queued for later.

    attn terminal read <terminal> [--last | --scrollback] [--lines <n>] [--json]

Returns the rendered screen of `<terminal>`; with `--last` or `--scrollback`, the retained scrollback, of which `--lines` selects the last n lines, or the last completed command's block, which needs OSC 133 shell integration.

    attn terminal run <terminal> "<command>" [--wait] [--timeout <seconds>] [--desktop <ref>] [--json]

Types `"<command>"` into the shell `<terminal>` at its prompt, through the shell, preserving history, and returns the id of the last completed block without waiting for the command. The shell needs OSC 133 shell integration. Read the command's output with `attn terminal read` and its `last` mode once it is done. Anyone may run a command in a shell.

    attn terminal run 4f2a "make test"

    attn terminal wait <terminal> --until prompt|exit [--timeout <seconds>] [--json]

Waits until `<terminal>` shows its shell prompt, or until its process exits, and exits with the exit code. `--timeout` gives up after that many seconds and exits 124.

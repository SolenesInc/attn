# Terminals beside your agent

Use `attn terminal` inside an attn agent to open shell splits, inspect terminals,
type into them, wait for a command or terminal to finish, and close shells.
Run `attn terminal --help` for the full syntax and supported key names.

```sh
attn terminal open -- pnpm dev
attn terminal ls --json
attn terminal read <id>
attn terminal send <id> --keys C-c
attn terminal run <id> "make test" --wait
attn terminal read <id> --last
attn terminal close <id>
```

## Placement and lifetime

Opening preserves the user's focus. Your first terminal goes to your right;
`--down` puts it below you. Later ones stack below your most recently opened
live terminal on that desktop. With `--desktop <ref>`, the first split starts
at that desktop's active tile. `--cwd <dir>` selects the directory; otherwise
it uses your session's directory.

`open -- command` waits for the shell's first OSC 133 prompt before typing.
Known unsupported shells are refused before opening. A first-prompt wait
exceeding 30 seconds fails with the terminal's id, leaving it open and typing
nothing later. First prompts measured between 9 and 176 ms across fish, zsh and bash in
the startup spike; this tripwire catches broken startup or integration that
startup files disabled. Use `send` to continue in that terminal. Open without
a command for a shell without command marks.

An opened terminal is an ordinary interactive shell. The command enters its
history, so the user can recall it with ↑. It records you as its parent and
continues running after your session closes. Close it when its work is done.

## Reach

A target is a terminal id or unique terminal-id prefix printed by `ls`.
`open` prints the new terminal id. A session id/prefix or awake crew member's
name also works when that session shows in exactly one terminal. Shared Codex
can show one session in several tiles; choose a terminal id when it does.
Every target belongs to your profile. Crew names follow their current session
and never wake a sleeping member.

If your session has tiles on several desktops, pass `--desktop` for listing
and actions. If it has several tiles on one desktop, `open` also needs
`--desktop` to select that desktop's active tile as the anchor.

- `ls` shows your desktop; `--all` shows all desktops in your profile.
- `read` and `wait` can inspect any terminal in your profile.
- `open`, `run`, `send` and `close` on another desktop require an explicit
  `--desktop <ref>`. A refusal prints the value to pass.
- Any agent can type into a shell. To type into an agent, it must be you or
  below you in your delegation chain, including delegates' delegates. Your
  profile's Chief can type into any agent in the profile.
- `close` closes shells. Use `attn agent close` to close an agent.

Typing goes straight to the terminal, as keyboard input does. `send --text`
writes literal bytes; it does not press Enter for you. Use `send --keys Enter`
to submit text, or `run` for a shell command. `send --keys C-c` interrupts the
foreground program.

## Reading and waiting

`read` returns the rendered screen. `read --scrollback` includes retained
history; `--lines <n>` selects its last n lines. `read --last` returns the last
completed command's output. Output discarded from scrollback is unavailable
and produces an explicit error.

`run` requires a shell at its prompt. If a program is running, use `send` to
talk to it. `run --wait` reads the next completed command block and exits with
its exit code. Avoid concurrent writers to that shell when attributing a
block to a command. Shells without OSC 133 command marks cannot support
`run --wait`, `read --last`, or `wait --until prompt`; use plain `run` and
screen reads there.

`wait --until prompt` waits for the shell prompt. `wait --until exit` waits
for the terminal process to exit. `--timeout <seconds>` bounds either wait
(and `run --wait`); expiration exits 124. Without it, waiting continues until
the condition is met or your CLI connection closes. A restart or disconnected
worker fails the call visibly; it never leaves a command queued for later.

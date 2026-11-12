# Using Sigma

Sigma is experimental. It runs Pi Durable conversations behind an app-server:
one Sigma server per Sigma home stores every conversation, and the Sigma
terminal view and Attn are both clients of that server. A run keeps going when
the terminal that started it closes, and a restarted server resumes the work it
had not finished.

Attn runs the `sigma` on your PATH, or the one `ATTN_SIGMA_EXECUTABLE` names.
Attn uses `SIGMA_HOME` from your login shell as the Sigma home, by default
`~/.sigma`. Keep that path short: the server's socket lives in it. Attn needs
Sigma protocol 1.

## Turn it on or off

**Sigma** in **Settings → Experimental** is off by default. While the setting is
off, the harness pickers don't offer Sigma and Attn refuses Sigma launches,
resumes from the Sessions ledger included.

## How a session runs

A Sigma session runs as a Nisse session does, as
[Using Nisse](using-nisse.md#how-a-session-runs) describes, with these
differences:

- Attn launches `sigma --attach --conversation <id>`. Attn starts the server
  with `sigma serve` in a terminal of its own, and the view never starts a
  second one.
- Calls that change things, such as shell commands and file writes, wait in
  the terminal for you to allow them, unless the session started with YOLO.
- Coding tools run on this computer. Other execution targets come later.

## State

The session's state comes from the server, as for Nisse. Every outcome the
server reports carries a number. Attn saves the newest number it applied
together with the state it showed, and gives that number back whenever it
reconnects. So a run that ended while Attn was not looking, even one Attn never
saw start, still reaches you, and a question you already handled does not come
back after a reconnect or a restart.

## Messages, closing and resuming

These work as for Nisse. A retried delivery is one follow-up: the server
answers a repeated delivery with the input it already stored.

## Agents using Attn

Every command that a Sigma agent runs has `SIGMA_ROOT_CONVERSATION` set. Attn
finds the session that holds that conversation and runs the `attn` command as
that session.

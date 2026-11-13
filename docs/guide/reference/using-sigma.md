# Using Sigma

Sigma is an installable harness plugin. It runs Pi Durable conversations behind an app-server:
one Sigma server per Sigma home stores every conversation, and the Sigma
terminal view and Attn are both clients of that server. A run keeps going when
the terminal that started it closes, and a restarted server resumes the work it
had not finished.

Attn uses a session's explicit executable first, then the `sigma_executable`
machine setting, then `ATTN_SIGMA_EXECUTABLE`, then `sigma` on PATH.
Attn uses `SIGMA_HOME` from your login shell as the Sigma home, by default
`~/.sigma`. Keep that path short: the server's socket lives in it. Attn needs
Sigma protocol 1.

## Install the plugin

Extract `attn-sigma-0.1.0.tgz` and run
`attn plugin install --path /path/to/attn-sigma-0.1.0`.
The package includes its install and upgrade instructions. Installation requires
Bun and an Attn build containing the Sigma plugin cutover. Without the package,
the picker omits Sigma.

When upgrading from the built-in harness, stage the package with that command
before updating Attn. The old build reserves `sigma`, so registration starts
after the update. Keep the same Sigma home, server and executable override.
Existing sessions and ledger entries retain their native conversation IDs,
profiles, execution targets and applied completion numbers.

If you upgrade without the package, install it to reconnect existing sessions
and restore ledger resumes. History remains available while the plugin is
absent. Do not change `SIGMA_HOME` or create replacement conversations to recover.

Close every open Sigma session before `attn plugin uninstall sigma`. The runs
continue on the server. Reinstalling the package restores availability.

## How a session runs

A Sigma session runs as a Nisse session does, as
[Using Nisse](using-nisse.md#how-a-session-runs) describes, with these
differences:

- Attn launches `sigma --attach --conversation <id>`, with `--target <id>` when
  the session names an execution target. Attn starts the server
  with `sigma serve` in a terminal of its own, and the view never starts a
  second one.
- Calls that change things, such as shell commands and file writes, wait in
  the terminal for you to allow them, unless the session started with YOLO.
- Coding tools run where the session's execution target says: `local`, this
  computer, by default, or `devvm`, the project's DevVM VM. The target is
  chosen when the conversation is created and never changes. A client picks it
  with `execution_target` when it launches the session; the app offers no
  picker yet. The Sigma view
  shows it when it opens and when it reattaches.
- When the target cannot be reached, for example because the folder has no
  DevVM VM yet, the view says why and exits. The session never runs on this
  computer instead: resuming it tries the same target again.

## State

The session's state comes from the server, as for Nisse. Every outcome the
server reports carries a number. Attn saves the newest number it applied
together with the state it showed, and gives that number back whenever it
reconnects. So a run that ended while Attn was not looking, even one Attn never
saw start, still reaches you, and a question you already handled does not come
back after a reconnect or a restart.

## Messages, closing and resuming

Messages work as for Nisse. A retried delivery is one follow-up: the server
answers a repeated delivery with the input it already stored.

Closing a session never stops its conversation's run. Whether you close its
tile in Attn or leave the Sigma view with Ctrl-D or `/detach`, the Attn session
closes and the run keeps going on the server. Resume the session from the
Sessions ledger to open the conversation again, with its run still going. To
stop the run, interrupt it, or leave the view with `/close`.

## Agents using Attn

Every command that a Sigma agent runs has `SIGMA_ROOT_CONVERSATION` set. Attn
finds the session that holds that conversation and runs the `attn` command as
that session.

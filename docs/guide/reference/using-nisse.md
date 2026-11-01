# Using Nisse

Nisse is experimental. It is a coding agent whose conversations live in a server
rather than in the terminal that shows them: one Nisse server per Nisse home
holds every conversation. The Nisse terminal UI, `nisse exec` and attn are all
clients of that server. A run keeps going when the terminal that started the run
closes, and a server that crashed resumes the runs the crash interrupted.

attn runs the `nisse` on your PATH, or the one `ATTN_NISSE_EXECUTABLE` names.
The Nisse home is `NISSE_HOME` from your login shell, by default `~/.nisse`, the
same home the bare `nisse` uses, so attn and your terminal see the same
conversations, sign-ins and config. attn needs Nisse protocol 7 or later.

## Turn it on or off

**Nisse** in Settings → Experimental is off by default. While the setting is
off, the harness pickers don't offer Nisse and attn refuses Nisse launches,
resumes from the Sessions ledger included.

## How a session runs

- A Nisse session is a terminal tile running the Nisse TUI on one conversation.
  attn names a new session's conversation after the session's terminal and
  launches `nisse --conversation <id>`, with the initial prompt, model, effort
  and YOLO the launch asked for. A model pin of the form `provider/model`, such
  as `openai/gpt-5.5`, also picks the provider. YOLO starts the conversation
  without asking before calls that change things.
- A new conversation starts with attn's instructions, as other harnesses get
  them at launch. Nisse keeps them with the conversation, so a resumed session
  keeps the instructions it started with.
- The model pickers list the models of every provider the Nisse server has, as
  `provider/model`. Listing the models starts the server if none runs, and attn
  releases the server again when no Nisse session is open.
- attn starts the Nisse server with `nisse serve` in a terminal of its own the
  first time a session needs the server, so the server keeps running through app
  and daemon restarts. If a server already serves the home, for example one your
  own `nisse` started, attn uses that one.
- attn holds one connection to the server while any Nisse session is open. When
  the last session closes, attn closes the connection, and the server exits on
  its own once no client is left and no run is working.

## State

The session's state comes from the server, not from the terminal:

- **working** while a run is active in the conversation or in any subagent
  under the conversation.
- **needs approval** while a call there waits for someone to allow it, a
  subagent's call included.
- When the conversation's run ends with an answer, attn reads the conversation's
  last message to tell **waiting** from **idle**, as attn does for other
  harnesses.
- A run that fails, or that crashed the server three times in a row, leaves
  the session **waiting** with the reason.
- An interrupted run leaves the session **idle**.

A run that ends while attn is down settles the same way once attn is back, from
how the server says the conversation's last run ended. A session that was
already waiting or idle keeps its state through the restart.

## Messages

Annotations, messages from other agents and inbox notifications reach the
conversation as follow-ups through the server, never typed into the terminal.
A message sent while a run works waits in the conversation's queue and runs
next. A retried delivery is one follow-up.

## Closing and resuming

Closing a session interrupts its conversation's run, subagents included, and
closes the session into the ledger. Quitting the TUI with `^C` closes the
session the same way, as quitting any other harness does. Resume the session
from the Sessions ledger to open its conversation in a new tile.

A Nisse conversation belongs to the profile whose session holds it. attn refuses
to resume the conversation in another profile while that session is open.

## Agents using attn

A command a Nisse agent runs, in a subagent too, carries
`NISSE_ROOT_CONVERSATION`, and attn reads the variable as the session holding
that conversation. So `attn` commands from Nisse agents act as their session, as
they do from other harnesses.

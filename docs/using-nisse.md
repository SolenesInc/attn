# Using Nisse

Nisse is experimental. It is a coding agent whose conversations live in a
server rather than in the terminal that shows them: one Nisse server per Nisse
home holds every conversation, and its terminal UI, `nisse exec` and attn are
all clients of it. A run keeps going when the terminal that started it closes,
and a server that crashed resumes the runs it cut off.

attn runs the `nisse` on your PATH, or the one `ATTN_NISSE_EXECUTABLE` names.
Its home is `NISSE_HOME` from your login shell, by default `~/.nisse`, the same
one the bare `nisse` uses, so attn and your terminal see the same
conversations, sign-ins and config. attn needs Nisse protocol 5 or later.

## Turn it on or off

**Nisse** in Settings → Experimental is off by default. While it is off, the
harness pickers don't offer Nisse and attn refuses Nisse launches, resumes from
the Sessions ledger included.

## How a session runs

- A Nisse session is a terminal tile running the Nisse TUI on one
  conversation. attn names a new session's conversation after its terminal and
  launches `nisse --conversation <id>`, with the initial prompt, model, effort
  and YOLO the launch asked for. A model pin of the form `provider/model`, such
  as `openai/gpt-5.5`, also picks the provider. YOLO starts the conversation
  without asking before calls that change things.
- attn starts the Nisse server with `nisse serve` in a terminal of its own the
  first time a session needs it, so it keeps running through app and daemon
  restarts. If a server already serves the home, for example one your own
  `nisse` started, attn uses that one.
- attn holds one connection to the server while any Nisse session is open.
  When the last one closes, attn lets go, and the server exits on its own once
  no client is left and no run is working.

## State

The session's state comes from the server, not from the terminal:

- **working** while a run in the conversation, or in any subagent under it,
  works;
- **needs approval** while a call there waits for someone to allow it, a
  subagent's call included;
- when the conversation's run ends with an answer, attn reads its last message
  to tell **waiting** from **idle**, as it does for other harnesses;
- a run that fails, or that crashed the server three times in a row, leaves
  the session **waiting** with the reason;
- an interrupted run leaves it **idle**.

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

A Nisse conversation belongs to the profile whose session holds it. Resuming
it in another profile is refused while that session is open.

## Limits

- Agents running in Nisse can't use `attn` commands yet; nothing tells attn
  which session a Nisse tool call belongs to.
- attn adds no launch instructions to a Nisse conversation yet, and its model
  pickers don't list Nisse's models.

# Using Nisse

Nisse is experimental. It is a coding agent whose conversations are stored in a server, not in the terminal that shows them. One Nisse server per Nisse home stores every conversation. The Nisse terminal UI, `nisse exec` and Attn are all
clients of that server. A run keeps going when the terminal that started the run
closes, and a server that crashed resumes the runs the crash interrupted.

Attn runs the `nisse` on your PATH, or the one `ATTN_NISSE_EXECUTABLE` names.
Attn uses `NISSE_HOME` from your login shell as the Nisse home, by default `~/.nisse`. The bare `nisse` uses the same home, so Attn and your terminal see the same conversations, sign-ins and config. Attn needs Nisse protocol 7 or later.

## Turn it on or off

**Nisse** in **Settings → Experimental** is off by default. While the setting is
off, the harness pickers don't offer Nisse and Attn refuses Nisse launches,
resumes from the Sessions ledger included.

## How a session runs

- A Nisse session is a terminal tile running the Nisse TUI on one conversation.
  Attn names a new session's conversation after the session's terminal and
  launches `nisse --conversation <id>`, with the initial prompt, model, effort and YOLO setting that you chose at launch. When you write a model as `provider/model`, such as `openai/gpt-5.5`, you also pick the provider. With YOLO, the conversation starts in a mode where calls that change things run without asking you first.
- A new conversation starts with Attn's instructions, as other harnesses get
  them at launch. Nisse stores those instructions in the conversation, so a resumed session
  keeps the instructions it started with.
- The model pickers list the models of every provider the Nisse server has, as
  `provider/model`. To list the models, Attn starts the server if none runs. Attn stops using the server when no Nisse session is open.
- Attn starts the Nisse server with `nisse serve` in a terminal of its own the
  first time a session needs the server, so the server keeps running through app
  and daemon restarts. If a server already serves the home, for example one your
  own `nisse` started, Attn uses that one.
- Attn holds one connection to the server while any Nisse session is open. When
  the last session closes, Attn closes the connection, and the server exits on
  its own once no client is left and no run is working.

## State

The session's state comes from the server, not from the terminal:

- **working** while a run is active in the conversation or in any subagent
  under the conversation.
- **needs approval** while a call there waits for someone to allow it, a
  subagent's call included.
- When the conversation's run ends with an answer, Attn reads the conversation's
  last message to tell **waiting** from **idle**, as Attn does for other
  harnesses.
- If a run fails, or has crashed the server three times in a row, Attn sets the
  session to **waiting** and shows the reason.
- If a run is interrupted, Attn sets the session to **idle**.

If a run ends while Attn is down, Attn sets the session's state when Attn is back. Attn uses what the server reports about how the conversation's last run ended. A session that was
already waiting or idle keeps its state through the restart.

## Messages

Annotations, messages from other agents and inbox notifications reach the
conversation as follow-ups through the server, never typed into the terminal.
A message sent while a run works waits in the conversation's queue until the
current run ends, then becomes the next run. A retried delivery is one follow-up.

## Closing and resuming

Closing a session interrupts its conversation's run, subagents included, and
closes the session into the ledger. Quitting the TUI with `^C` closes the
session the same way, as quitting any other harness does. Resume the session
from the Sessions ledger to open its conversation in a new tile.

A Nisse conversation belongs to the profile whose session holds it. Attn refuses
to resume the conversation in another profile while that session is open.

## Agents using Attn

Every command that a Nisse agent runs, including in a subagent, has `NISSE_ROOT_CONVERSATION` set. Attn finds the session that holds that conversation and runs the `attn` command as that session, as it does for other harnesses.

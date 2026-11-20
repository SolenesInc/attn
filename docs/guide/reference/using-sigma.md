# Using Sigma

Sigma is a harness built into Attn. Its conversations live in a server,
independently of the terminals that show them. One server per Sigma home serves
the Sigma terminal UI, `sigma exec` and Attn. If the server crashes, it resumes
the runs the crash interrupted.

Sigma is experimental. Turn on **Sigma** in **Settings → Experimental** to offer
it in the picker, for delegations and in `attn delegate --agent sigma`. Turning
it off stops new Sigma sessions; the ones that are open keep working.

Attn runs the `sigma` that `attn settings set sigma_executable <path>` names,
else the one `ATTN_SIGMA_EXECUTABLE` names, else the `sigma` on your login
shell's PATH. When Attn finds none, Sigma shows as unavailable. Attn uses `SIGMA_HOME` from your login shell as the Sigma home, by
default `~/.sigma`. The bare `sigma` uses the same home, so Attn and your
terminal see the same conversations, sign-ins and config. Attn needs Sigma
protocol 1 or later, built at or after the commit that gave every change an
author with an id and a name.

Sigma used to be a plugin. If you installed the `sigma` plugin, close its
sessions and run `attn plugin uninstall sigma`: while it is installed, Attn
refuses its registration, because Attn has the harness of that name.

## How a session runs

- A Sigma session is a terminal tile running the Sigma TUI on one conversation.
  Attn names a new session's conversation after the session's terminal and
  launches `sigma --conversation <id>`, with the initial prompt, model, effort
  and YOLO setting that you chose at launch. When you write a model as
  `provider/model`, such as `openai/gpt-5.5`, you also pick the provider. With
  YOLO, the conversation starts with approvals `auto`: every call runs without
  asking you first.
- **Auto-approve** leaves Sigma sessions alone. Claude Code and Codex put a
  reviewer between the agent and its calls, and Sigma has none, so a Sigma
  session asks you as Sigma does unless you launch it with YOLO.
- An [automation](automations.md) run has nobody to ask, so a Sigma run starts
  with approvals `auto`, as YOLO does, and runs every call without review.
- A new conversation starts with Attn's instructions, as other harnesses get
  them at launch. Sigma stores those instructions in the conversation, so a
  resumed or reloaded session keeps the instructions it started with, as a
  Claude Code session does.
- A delegation role that needs the attn-workflow skill installs it where Sigma
  finds skills: `skills` in the Sigma home, `skills` in `CLAUDE_CONFIG_DIR` (by
  default `~/.claude`), and `skills` in the folder of each Claude provider in
  Sigma's `config.toml`, from its `config_dir` or `env.CLAUDE_CONFIG_DIR`. Attn
  reads `config.toml` at each launch. A provider that names a relative folder
  stops the launch until you make the folder absolute; `config_dir` may start
  with `~/`.
- The model pickers list the models of every provider the Sigma server has, as
  `provider/model`. To list the models, Attn starts the server if none runs.
  Attn stops using the server when no Sigma session is open.
- Attn starts the Sigma server with `sigma serve` in a terminal of its own the
  first time a session needs the server, so the server keeps running through
  app and daemon restarts. If a server already serves the home, for example one
  your own `sigma` started, Attn uses that one.
- Attn holds one connection to the server while any Sigma session is open. When
  the last session closes, Attn closes the connection, and the server exits on
  its own once no client is left and no run is working.
- Sigma sessions run at the home. Attn refuses to start one on an outpost.

## State

The session's state comes from the server, not from the terminal:

- **working** while a run is active in the conversation or in any subagent
  under the conversation.
- **needs approval** while a call there waits for someone to allow it, a
  subagent's call included.
- When the conversation's run ends with an answer, Attn reads the
  conversation's last message to tell **waiting** from **idle**, as Attn does
  for other harnesses.
- If a run fails, or has crashed the server three times in a row, Attn sets the
  session to **waiting** and shows the reason.
- If a run is interrupted, Attn sets the session to **idle**.

If a run ends while Attn is down, Attn sets the session's state when Attn is
back. Attn uses what the server reports about how the conversation's last run
ended. A session that was already waiting or idle keeps its state through the
restart.

## Messages

Annotations, messages from other agents and inbox notifications reach the
conversation as follow-ups through the server, never typed into the terminal.
What you send arrives under your user name, and what Attn and other agents send
arrives from `attn`. An annotation sent while a run works reaches that run at
its next step, as a steer; one sent while a call waits for your approval waits
in Sigma until you answer the approval, and never answers it. Messages from
other agents and inbox notifications wait for the run to end, as they do for
other harnesses. Whatever arrives while no run works starts one. A retried
delivery is one follow-up.

## Closing and resuming

Closing a session interrupts its conversation's run, subagents included, and
closes the session into the ledger. Quitting the TUI with `^C` closes the
session the same way, as quitting any other harness does. Resume the session
from the Sessions ledger to open its conversation in a new tile.

An agent can compact its conversation just before it closes itself, as
[Seeds on standby](delegation.md#seeds-on-standby) describes. Attn waits for
the run to end, then asks Sigma to compact the conversation. When Sigma finds
nothing to compact, the session closes as if it had compacted.

A Sigma conversation belongs to the profile whose session holds it. Attn refuses
to resume the conversation in another profile while that session is open.

## Agents using Attn

Every command that a Sigma agent runs, including in a subagent, has
`SIGMA_ROOT_CONVERSATION` set. Attn finds the session that holds that
conversation and runs the `attn` command as that session, as it does for other
harnesses.

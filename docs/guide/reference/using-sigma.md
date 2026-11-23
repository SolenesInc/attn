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
terminal see the same conversations, sign-ins and config. Attn needs a Sigma
that speaks protocol 2. A Sigma server that started before you updated Sigma
still speaks protocol 1: Attn refuses it, with a message that names the server
process to stop, and starts a new server once that one has stopped.

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
- **With Auto-approve on, which is the default, every Sigma session runs every
  call without review.** Claude Code and Codex put a reviewer between the agent
  and its calls in that mode. Sigma has none, so Attn starts the conversation
  with approvals `auto`, as YOLO does. Turn off **Auto-approve**
  (`auto_approve_enabled`) to have Sigma ask you. An
  [automation](automations.md) run always starts this way, because it has
  nobody to ask.
- A new conversation starts with Attn's instructions, as other harnesses get
  them at launch. Sigma stores those instructions in the conversation, so a
  resumed or reloaded session keeps the instructions it started with, as a
  Claude Code session does.
- `/fork` in a Sigma tile moves the tile to a new session for the fork, as
  `/new` does in shared Codex. The tile moves when the fork starts work. If
  you reload the tile, or send its session annotations, a review or a prompt
  from your phone before then, Attn moves the tile first, so they reach the
  fork. On an outpost, a reload before then still reopens the conversation
  you forked from. The conversation you forked from stays with its session,
  which stays open and hidden. The new session's transcript follows the forked
  conversation (including the history it inherited up to the fork), while its
  title comes from the first prompt you type in the fork and its usage, recent
  files, and pull requests start at the fork.
- A delegation role that needs the attn-workflow skill installs it where Sigma
  finds skills: `skills` in the Sigma home, `skills` in `CLAUDE_CONFIG_DIR` (by
  default `~/.claude`), and `skills` in the folder of each Claude provider in
  Sigma's `config.toml`, from its `config_dir` or `env.CLAUDE_CONFIG_DIR`. Attn
  reads `config.toml` at each launch. A provider that names a relative folder
  stops the launch until you make the folder absolute; `config_dir` may start
  with `~/`.
- The model pickers list the models of every provider the Sigma server has, as
  `provider/model`. The models of a provider you are not signed in to show
  disabled. After you sign in, in Sigma or elsewhere such as Claude Code, press
  **Refresh** in the picker or on Sigma's card in **Settings → Models** to list
  them. To list the models, Attn starts the server if none runs.
  Attn stops using the server when no Sigma session is open.
- Attn starts the Sigma server with `sigma serve` in a terminal of its own the
  first time a session needs the server, so the server keeps running through
  app and daemon restarts. If a server already serves the home, for example one
  your own `sigma` started, Attn uses that one.
- Attn holds one connection to the server while any Sigma session is open. When
  the last session closes, Attn closes the connection, and the server exits on
  its own once no client is left and no run is working.
- A Sigma session on an outpost runs on that outpost's own Sigma server, which
  the outpost starts and stops the same way. Its model list comes from that
  server. Install Sigma and sign in to its providers on the outpost. See
  [Using outposts](using-outposts.md#run-agents-on-an-outpost).

## Crew, the Chief and settings

A crew member or the Chief can run on Sigma: run
`attn crew set <member> --agent sigma`, or in the native app pick Sigma in the
Chief's setup or under "Harness and model…". The member's orientation reaches
Sigma as the conversation's instructions. Sigma's tools reach any folder, so a
member's awareness folders need nothing more.

In the native app, **Settings → Agents** lists Sigma beside Claude Code and
Codex, with its executable, default model and default effort. Sigma takes no
context cap, so its card has none and the palette offers no cap for a Sigma
session. The automation form offers Sigma. With harness logos on, a Sigma
session's row in the sidebar shows Sigma's mark.

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

## What Attn reads from the conversation

Attn follows each open Sigma session's conversation on the server, so a session
whose tile is closed, or whose TUI has not come back yet, still reports:

- the session's title, from the first prompt you type, or for a conversation
  started outside Attn, from its first prompt;
- `attn session transcript` and the last reply in `attn agent peek`;
- the activity line, and the agent's replies you can select to annotate;
- usage and cost, from the totals Sigma keeps for each model across the
  conversation and its subagents, counting only what the session spent after it
  resumed a conversation;
- markdown files the agent or its subagents write or edit, in recent files, and
  the pull requests their `gh pr create` calls open, on the session;
- **working** while Sigma compacts the conversation.

Attn picks up where it left off after an Attn or Sigma server restart, and
counts nothing twice. Sigma has no tool that sends you a file, so a Sigma agent sends no
files to a tile.

## Background model calls

Sigma answers the model calls Attn makes in the background, as Claude Code and
Codex do. Sigma titles its own sessions and judges how their turns end. In
Settings, you can also choose Sigma for the activity line, the Garden advisor
and dictation, and `attn model call` takes `"harness": "sigma"`. So a machine
with Sigma alone needs neither Claude Code nor Codex. Each call is one prompt
that Sigma answers on its server with no tools, and Sigma keeps nothing of it:
no conversation and no usage. Attn starts the server for a call if none runs.
While Sigma is off in **Settings → Experimental**, Attn offers it for none of
these choices, and its open sessions are still titled and judged by Sigma.

Unless you choose a model, a call uses the first light model of a provider
that can answer it, such as `claude/haiku` or `openai/gpt-6-luna`: one you are
signed in to, and not `agy`, whose models answer only in a conversation. When
Attn cannot read the server's list, the call goes to Sigma's default model,
and `attn model log` names the model that answered. Settings refuse a model
that cannot answer for the activity line or the Garden advisor, and their
pickers show it disabled.

## Messages

Annotations, messages from other agents and inbox notifications reach the
conversation as follow-ups through the server, never typed into the terminal.
What you send arrives under your user name, and what Attn and other agents send
arrives from `attn`. An annotation sent while a run works reaches that run at
its next step, as a steer. An annotation never answers an approval. While the
conversation's own run waits for your approval, the annotation waits in Sigma
until you answer. While only a subagent waits, the conversation is free: the
annotation reaches its run at the next step, or starts a run if the
conversation's run had ended, and the subagent's approval stays open. Messages
from other agents and inbox notifications wait for the run to end, as they do
for other harnesses. Whatever arrives while no run works starts one. A retried
delivery is one follow-up.

## Closing and resuming

Closing a session interrupts its conversation's run, subagents included, and
closes the session into the ledger. Quitting the TUI with `^C` closes the
session the same way, as quitting any other harness does. Resume the session
from the Sessions ledger to open its conversation in a new tile. If the server is
running and no longer holds a conversation Attn has followed, Attn says so
instead of offering a resume.

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

With **Agent tools** on in **Settings → Experimental** (`agent_tools_enabled`),
a Sigma agent also gets Attn's tools, such as `seed_note`, as the MCP server
`attn`. Attn registers the server with the Sigma server each time it connects.
It starts each new conversation with `--mcp-server attn=<description>` and
appends the intent map, the text that says when to reach for each tool, to the
conversation's instructions. Sigma sends the root conversation's id with every
tool call, so a call acts as the session that holds the conversation, and a
subagent's call as its root's. Attn gives the credential to the Sigma server
alone: the `sigma` client, its arguments and Sigma's database never hold it.

Sigma lists a conversation's tools when the conversation starts and keeps the
list. A conversation started with the setting off therefore has no Attn tools
for its whole life, and turning the setting on affects new conversations only.
Turning it off ends the registration, so the calls of a conversation that has
the tools fail until you turn it on again.
Attn refuses the calls of a conversation no Attn session holds, such as one you
start with your own `sigma --mcp-server attn=…`.

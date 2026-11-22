# Sigma harness behavior

How Sigma behaves where attn meets it. The rules for adding a claim are in
[Harness behavior](../harnesses.md).

Sigma was named Nisse until 2026-11-14. Entries were read in the source at
SolenesInc/sigma 6fb87ec, except the entries on authors, instructions and
skills (72006f8); on steering, compaction, watching, entries, tools, forks and
tasks (a903aa0, where a probe on 2026-11-20 checked forks, tasks and an idle
watch); and on MCP servers (69daaf7). An entry that names its own commit or
measurement uses that one.

- One server per Sigma home holds every conversation. Clients reach the server
  over `SIGMA_HOME/sigma.sock`, and the home defaults to `~/.sigma`.
  `sigma serve` refuses a home another server already serves
  (`packages/sigma/src/main.ts`).
- `initialize` answers `{version, protocol, readOnly}` with protocol 2 to a
  client that asks for 1 or 2, and no server name (43ebbba,
  `packages/protocol/src/index.ts`). A server started from an older build
  answers protocol 1.
- The TUI and `sigma exec` start a detached server when none answers. A server
  exits 30 seconds after its last client leaves and its last run settles.
- `sigma --conversation <id>` opens that conversation, and starts a
  conversation with that id when none exists. A prompt after the flags is sent
  once the conversation opens.
- Every call that changes a conversation, `conversation/submit` and
  `conversation/interrupt` among them, requires `author: {id, name}`. Sigma
  stores both on the change's entry, shows the name and never reads the id. A
  string author, or none, fails to decode; the protocol version stayed 1 when
  this changed (dff03a5). The TUI and `sigma exec` act as `SIGMA_AUTHOR_ID` and
  `SIGMA_AUTHOR_NAME`, each defaulting to the system's user name
  (`packages/sigma/src/author.ts`).
- `conversation/submit` is idempotent by its `requestId`: a retried submit is
  one input. With `steer: true` the input reaches the run in flight at its next
  step instead of waiting for the run to end; with no run working it starts one.
  Input waits in the conversation's `pi.inbox` until the run takes it, and only
  `approval/resolve` answers an approval; what a steer does while a call waits
  on an approval is not probed (`packages/protocol/src/index.ts`,
  `packages/sigma/src/server.ts`).
- `conversation/compact {conversation, author, instructions?}` answers once the
  compaction has ended, with `outcome` `compacted`, `nothing_to_compact`,
  `failed` (with a `message`) or `stopped`. Behind a run in flight it waits for
  the run's next step or its end.
- Every `bash` call, and Claude Code's process, runs with
  `SIGMA_ROOT_CONVERSATION` set to the id of the conversation people started.
  Every MCP request carries the registered headers and
  `Sigma-Root-Conversation` with the same root: the id the client chose, or
  Sigma's internal number for a conversation started without one, such as by
  `sigma exec` or `/fork` (measured on 69daaf7: a bare `sigma exec` sent `149`,
  a fork `173`).
- `conversation/start` takes `instructions`, kept with the conversation and
  not given to its subagents: OpenAI is told them after the `AGENTS.md` files,
  and Claude Code appends them to its own system prompt. They are fixed once the
  conversation starts, and `conversation/start` with an existing id changes
  nothing (413f38d).
- A server's `instructions` reach the model by provider. On the Claude path
  Sigma hands Claude Code each server as an in-process MCP server with the
  instructions the server gave in `initialize` (`packages/claude/src/process.ts`),
  and Claude Code puts them under `# MCP Server Instructions` in the messages.
  On Sigma's own loop, the one OpenAI models take, they are only in
  `describeNamespace()` (`packages/sigma/src/tools/mcp.ts`). attn appends its
  intent map to the conversation's `instructions` (D22 of `s-ct56cj`), so on
  the Claude path the model reads it twice: measured 2026-11-21 on 69daaf7
  with claude-haiku-5-5, 1,337 bytes in the system prompt and again in the
  messages.
- Skills: an OpenAI model finds them in `.agents/skills` and `.claude/skills`
  of the project folders and in `skills` of the Sigma home
  (`packages/sigma/src/workspace.ts`). Claude Code finds its own in its
  `CLAUDE_CONFIG_DIR`, by default `~/.claude`. `config.toml` in the Sigma home
  may name more Claude providers, each with a `config_dir` (which may start with
  `~/`) or an `env.CLAUDE_CONFIG_DIR` that becomes that Claude Code's config
  folder (`packages/sigma/src/config.ts`, `providers.ts`).
- `models/list` answers each provider's models, with the efforts each takes.
- `status/watch` opens with the busy conversation trees and then sends one
  status per change of a tree (working, waiting on an approval, idle), with
  how its root's run ended. Subagents are part of their root's tree.
- `conversation/list` carries `ended` on each conversation that has run: how
  its last run ended, the same as `status/watch` says.
- Sigma's tools refuse no path outside the conversation's folder. A path
  resolves from `~`, a `file://` URL or an absolute path with no containment
  check (`packages/env/src/local.ts`, read at eb70be3). Approvals are per tool,
  not per path (`asks` in `packages/harness/src/types.ts`). Claude Code's own
  permission asks go to Sigma's approvals through `canUseTool`
  (`packages/claude/src/process.ts`): a read outside the folder waits for an
  approval in `ask` mode and runs in `auto` mode, and is never refused.
- Prompt cache lifetime, measured 2026-11-20 on Sigma eb70be3 with real
  accounts: each probe sent a first prompt carrying a random nonce, waited, then
  sent a second prompt in the same conversation. Under the `claude` provider,
  Claude Code writes only one-hour cache (`ephemeral_1h_input_tokens`, never
  `5m`, in its own transcripts); the second prompt read the nonce turn back
  after 55 minutes and read only the shared prefix after 65. Under the `openai`
  provider, the second prompt read the nonce turn back after every gap tried,
  up to 75 minutes. So a Sigma crew member's cache lasts at least an hour on
  either provider, and the crew's default of 3600 s fits it.
- `conversation/watch` streams a view of the newest entries and the
  conversation's docs, then each change as ops on that view. Past `max`
  entries, twice `min` and 100 by default, the window drops its oldest down to
  `min` and sets `before`. A slow reader gets the ops folded, or the whole
  view again. With `window.from`, the view starts at that entry when at most
  `max` entries follow it. A watch sends nothing while the conversation is
  idle (probe).
- Entry ids ascend across the server, not within one conversation.
  `conversation/entries` pages a conversation's entries in either order.
- A prompt is a `sigma.user` entry, whose data names its author. A reply or a
  tool call is a `pi.assistant` entry, and a tool's output a `pi.tool-result`
  entry. `pi.usage` holds each model's token and cost totals, and `pi.live`
  the compactions running.
- A subagent is a conversation of its own, with its own entries and its own
  `pi.usage`, which counts only that conversation's spend; the parent gets a
  `sigma.subagent` entry pointing at it, and `conversation/list` gives each
  subagent's `lineage` (`parent`, `root`) (`packages/kernel/docs/spec.md`,
  `packages/protocol/src/index.ts`).
- A conversation's `title` in `conversation/list` is a subagent's title, or
  else the first line of the conversation's first prompt.
- Sigma's own tools are `read`, `write` and `edit`, which take a `path`,
  `apply_patch`, which takes a `patch` in Codex's format, `bash`, `grep`,
  `glob` and tool search (`packages/tools/src`). Sigma has no tool that sends
  the user a file.
- `/fork` in the TUI starts a new conversation that inherits the entries up to
  a point. The original conversation's watch gets nothing. The fork's first
  entry is `sigma.forked`, naming the client's `SIGMA_AUTHOR_ID`, and
  `conversation/list` gives the fork's `fork.of` (probe).
- `tasks/watch` shows only `claude.run` tasks, from pending to running to gone
  at the run's end. Sigma sleeps only for a retry's backoff or a deferred poll
  inside a run, so no task outlives a run (probe, `packages/harness/src/live.ts`).
- MCP servers (D19 of `s-ct56cj`): a connected client registers a server with
  `mcp/register {name, url, headers}`, a stream the server holds in memory for
  as long as the client stays connected. `sigma --mcp-server <name>=<description>`
  and `conversation/start`'s `mcpServers` name the servers a new conversation
  uses (`packages/sigma/src/main.ts`). Sigma lists each server's tools when the
  conversation starts and stores the listing in the conversation's `pi.agent`
  document; subagents and forks inherit it and never list again. A server that
  does not list then gives that conversation no tools for its whole life, with
  a `sigma.mcp_unlisted` entry naming the reason (`packages/sigma/src/mcp.ts`).
  The registered header values stay in memory: after attn's registration and
  tool calls, no file in the Sigma home held attn's credential or `Bearer`
  (measured on 69daaf7).
- An HTTP 401 from an MCP server, or a session the server no longer knows,
  reaches the conversation as pi-mcp's text, such as
  `MCP server requires authentication`, followed since f6be0c3 by the first 500
  characters of what the server answered, in the `sigma.mcp_unlisted` reason or
  the call's error. For attn's refusal it reads `…: invalid token: no session
  of sigma holds conversation <id>` (measured on f6be0c3; 69daaf7 dropped the
  answer).
- `SIGMA_MODEL_SCRIPT` names a JSON list of steps for the `script` provider,
  read when the server starts; the model `script/script-code-mode` takes only
  code mode, as ChatGPT's code-mode models do (`packages/models/src/script.ts`).
  It drives Sigma's own loop, the path OpenAI models take, without an account.

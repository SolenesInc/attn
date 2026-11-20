# Sigma harness behavior

How Sigma behaves where attn meets it. The rules for adding a claim are in
[Harness behavior](../harnesses.md).

Sigma was named Nisse until 2026-11-14. Read in the source at SolenesInc/sigma
6fb87ec, the entries on authors, instructions and skills at 72006f8, and those
on steering and compaction at a903aa0.

- One server per Sigma home holds every conversation. Clients reach the server
  over `SIGMA_HOME/sigma.sock`, and the home defaults to `~/.sigma`.
  `sigma serve` refuses a home another server already serves
  (`packages/sigma/src/main.ts`).
- `initialize` answers `{version, protocol, readOnly}` with protocol 1, and no
  server name (`packages/protocol/src/index.ts`).
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
  Input waits in the conversation's `pi.inbox` until the run takes it, so input
  sent while a call waits on an approval reaches the run after the approval.
  Only `approval/resolve` answers an approval (`packages/protocol/src/index.ts`,
  `packages/sigma/src/server.ts`).
- `conversation/compact {conversation, author, instructions?}` answers once the
  compaction has ended, with `outcome` `compacted`, `nothing_to_compact`,
  `failed` (with a `message`) or `stopped`. Behind a run in flight it waits for
  the run's next step or its end.
- Every `bash` call, and Claude Code's process, runs with
  `SIGMA_ROOT_CONVERSATION` set to the id of the conversation people started.
- `conversation/start` takes `instructions`, kept with the conversation and
  not given to its subagents: OpenAI is told them after the `AGENTS.md` files,
  and Claude Code appends them to its own system prompt. They are fixed once the
  conversation starts, and `conversation/start` with an existing id changes
  nothing (413f38d).
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

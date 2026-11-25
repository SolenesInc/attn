# Sigma harness behavior

Sigma (named Nisse until 2026-11-14; repo `SolenesInc/sigma`). Rules for
adding claims: [Harness behavior](../harnesses.md).

## Server, protocol and authors

- **Server & socket:** one server per `SIGMA_HOME` (default `~/.sigma`) on
  `SIGMA_HOME/sigma.sock`; `sigma serve` refuses an already-served home
  `[6fb87ec]`. TUI and `sigma exec` auto-start a detached server that exits
  30 s after its last client leaves and last run settles.
- **Handshake & queries:** `initialize` returns `{version, protocol, readOnly}`
  with protocol 2 (older builds return 1) `[43ebbba]`. `conversation/list`
  requires a `null` payload and rejects `{}` (`Expected null`) `[43ebbba]`.
- **Authors:** every mutating RPC (`conversation/submit`,
  `conversation/interrupt`, `conversation/compact`, …) requires
  `author: {id, name}` (string or omitted fails decode) `[dff03a5]`. TUI and
  `sigma exec` read `SIGMA_AUTHOR_ID`/`SIGMA_AUTHOR_NAME`. The server ignores
  author ids, and the TUI uses them only to decide which queued prompts are
  editable/restored on interrupt `[43ebbba]`—so attn sets
  `attn-terminal:<terminal>` on tiles and matches that id for user-voice
  inputs (prompts queued before a tile reload cannot be edited from the new
  tile).
- **Instructions & skills:** `conversation/start` `instructions` are fixed for
  the conversation's life (not passed to subagents; workers on the same
  provider inherit them, cross-provider workers get none) `[413f38d, a9b3461]`.
  OpenAI models read skills from `.agents/skills`, `.claude/skills`, and
  `SIGMA_HOME/skills`; Claude providers read `CLAUDE_CONFIG_DIR` (or per-provider
  `config_dir` / `env.CLAUDE_CONFIG_DIR` in `SIGMA_HOME/config.toml`)
  `[72006f8]`.

## Input, steering, interrupt and compaction

- **`conversation/submit`:** idempotent by `requestId`. `steer: true` injects
  at the next step of a running turn (or starts one if idle); approvals are
  answered only by `approval/resolve` `[a903aa0]`.
- **`conversation/interrupt`:** stops only the named conversation's run (when
  busy) or its own hosted background jobs (when idle), never crossing worker
  boundaries `[e69ff71]`. Atomically withdraws queued human inputs and returns
  their text only (images and named skills are lost when re-submitted); inputs
  arriving mid-stop are not withdrawn `[9fbd582]`.
- **`conversation/compact`:** waits for the next step or end of a running turn
  and returns `outcome: compacted | nothing_to_compact | failed | stopped`
  `[a903aa0]`.

## Status, display, entries and lineage

- **`status/watch`:** streams initial busy trees + current `shown` pairs, then
  changes per tree (`working`, approval wait, `idle` with `ended`) and
  `{_tag: "shown", author, conversation}` when a client opens `client/shows`
  (no end event; forgotten on server restart; TUI opens it once on screen)
  `[e69ff71, 40fb517]`. `conversation/list` also carries `ended`.
- **`conversation/watch` & entries:** entry ids ascend server-wide, not per
  conversation; idle watches send nothing `[a903aa0]`. Entry types:
  `sigma.user`, `sigma.mail` (`kind: new_task|message|final_answer`),
  `sigma.forked`, `sigma.subagent`, `sigma.mcp_unlisted`, `pi.assistant`,
  `pi.tool-result`, `pi.usage` (per-conversation only), `pi.live` `[e69ff71]`.
- **Lineage (`conversation/list` `lineage.{parent,root,title,task}`):**
  - **Worker** (`start_worker`): has `lineage.task`, `lineage.root == id`, own
    status tree, takes direct input, outlives parent turns (`fork_turns: "all"`
    forks parent) `[e69ff71]`.
  - **Hosted subagent** (Claude Code subagent): no `task`, `lineage.root` is
    nearest worker or person-started root, rolls up into root's tree, refuses
    direct input (`TakesNoInput`), stops with parent `[e69ff71]`.
- **Forks (`/fork`):** emits `shown`, writes `sigma.forked` (`fork.of`,
  `fork.at` in `conversation/list`), emits no tree status until it works or
  waits `[a903aa0]`.
- **Tasks & tools:** `tasks/watch` shows only `claude.run` tasks (no task
  outlives a run). Built-in tools (`read`, `write`, `edit`, `apply_patch`,
  `bash`, `grep`, `glob`, tool search) enforce no path containment; approvals
  are per-tool (Claude Code reads outside cwd ask in `ask` mode, run in `auto`)
  `[eb70be3]`.

## Providers, models, ephemeral calls and cache

- **`providers/list` & `models/list`:** `models/list` includes signed-out
  providers. `providers/list` reports `health.status` (`ready`, `signed_out`,
  `unavailable`) and `ephemeral` (every driver except `agy`) `[30eaeca]`.
  Claude provider health/models are cached from one Claude start until
  `refresh: true` (~0.8–1.1 s) or Sigma sign-in `[9fbd582]`.
- **`ephemeral/run`:** runs one tool-free prompt committing nothing to
  `conversation/list` or `usage/get`; supports concurrent calls; errors tag
  `EphemeralFailed`, `UnknownProvider`, `UnknownEffort`, `ReadOnlyServer`
  `[43ebbba]`.
- **Prompt cache lifetime:** ≥1 hour on both `claude` (1h cache only) and
  `openai` (tested to 75 min), matching crew's 3600 s default `[eb70be3]`.
- **Testing without an account:** set `SIGMA_MODEL_SCRIPT` (JSON step list) and
  model `script/script-code-mode` to drive Sigma's own loop `[a903aa0]`.

## attn tools over MCP

`[69daaf7, e69ff71, f6be0c3]`

- `mcp/register {name, url, headers}` holds the registration and headers in
  memory for the stream's life (never written to disk). New conversations name
  servers via `--mcp-server <name>=<desc>` or `conversation/start`
  `mcpServers`.
- Sigma lists tools once at conversation start and stores them in `pi.agent`
  (inherited by subagents and forks; failures record `sigma.mcp_unlisted` and
  give no tools for the conversation's life).
- Every bash call, Claude/`agy` process, and MCP request carries the tree root
  in `SIGMA_ROOT_CONVERSATION` / `Sigma-Root-Conversation` (MCP clients are
  keyed by tree root).
- On the Claude path, Sigma passes MCP `initialize` `instructions` into
  `# MCP Server Instructions` (on Sigma's own OpenAI loop, only in
  `describeNamespace()`), so appending attn's map to conversation
  `instructions` delivers it twice on the Claude path.
- **Tool results `[2ebc9c6]`:** the model reads the text blocks, and the JSON of
  `structuredContent` only when there is no content. Code-mode programs read
  `structuredContent` when the tool has an `outputSchema`.
- HTTP 401/errors surface up to 500 chars of the server body in
  `sigma.mcp_unlisted` / tool error `[f6be0c3]`.

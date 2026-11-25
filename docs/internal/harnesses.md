# Harness behavior

External behavior of each harness where attn meets it. Every claim must be
checked in the harness's source or with a binary probe and tagged with its
version; never add or rely on a claim from memory. Group facts by topic and
replace superseded findings rather than appending probe logs.

## Per-harness references

- [Claude Code](harnesses/claude.md): hooks, idle compaction, trust, OSC 7501,
  MCP, mods, agent view, model call spare.
- [Codex](harnesses/codex.md): plain TUI, hooks, `/side`, rollouts, local
  memories, updates, `codex exec`.
- [Codex app-server](harnesses/codex-app-server.md): shared app-server,
  `--remote` TUI, drain/restarts, trust, MCP, ephemeral model calls.
- [Sigma](harnesses/sigma.md): Sigma server (formerly Nisse), authors,
  steering/interrupt, lineage, MCP, ephemeral runs.

## Cross-harness invariants

- **Caller identity at the boundary:**
  - Terminal-bound harnesses: `ATTN_TERMINAL_ID` (legacy `ATTN_SESSION_ID`);
    CLI commands resolve to the terminal's current conversation when the
    command starts, hooks when the report arrives.
  - Shared Codex (server-bound): hooks carry `session_id` on stdin, shell tools
    get `CODEX_THREAD_ID`, MCP `tools/call` gets `_meta.threadId`. Ephemeral
    `/side` shell tools get the terminal's `ATTN_TERMINAL_ID` and credential;
    its hooks resolve to no session.
  - Sigma (server-bound): shell tools get `SIGMA_ROOT_CONVERSATION`, MCP
    requests carry header `Sigma-Root-Conversation` (root of the conversation
    or worker tree).
  - Plugin drivers retain `session_id` as the wire spelling for the terminal.
- **Agent credential never appears in argv:** any local user can read `ps`.
  Pass the credential only via env (`ATTN_AGENT_CREDENTIAL` expanded by Claude
  `${ATTN_AGENT_CREDENTIAL}` and plain Codex `bearer_token_env_var`) or over
  the server channel (shared Codex per-thread `config`, Sigma in-memory
  `mcp/register`). Guarded by
  `TestNoProcessALaunchStartsHoldsTheAgentCredentialInItsArguments` (`cmd/attnd`).
- **Running version in transcripts** (stays accurate after on-disk updates):
  - Claude Code `[2.1.302–310]`: top-level `version` on every JSONL line
    (newest `version` within 38.8 KB of EOF across 300 transcripts).
  - Codex `[0.162.1]`: first line is `session_meta` with `payload.cli_version`
    (~23 KB).

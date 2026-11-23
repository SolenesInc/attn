# Harnesses

Attn hosts coding agent CLIs as they are. Claude Code, Codex, and Sigma are built into Attn. You can add other harnesses with plugins.

## Capability table

<!-- BEGIN GENERATED HARNESS TABLE -->
| Capability | Claude Code | Codex | Sigma | Plugin harness |
|---|---|---|---|---|
| Offered by default | Yes | Yes | Experimental (`sigma_enabled`) | While plugin is connected |
| Executable setting | `claude_executable` (`ATTN_CLAUDE_EXECUTABLE`, default `claude`) | `codex_executable` (`ATTN_CODEX_EXECUTABLE`, default `codex`) | `sigma_executable` (`ATTN_SIGMA_EXECUTABLE`, default `sigma`) | Owned by plugin (optional `<harness>_executable`) |
| Resume closed session | Yes | Yes | Yes | Declared (`resume`) |
| YOLO approval bypass | Yes | Yes | Yes | Declared (`yolo`) |
| Auto-approve | Yes | Yes | Yes (unreviewed) | No |
| Automation executable override | Yes | No | Yes | No |
| Initial prompt and delegation | Yes | Yes | Yes | Declared (`initial_prompt`) |
| Launch guidance | Yes | Yes | Yes | Declared (`launch_instructions`) |
| Model pin | Yes | Yes | Yes | Declared (`model_pin`) |
| Effort pin | Yes | Yes | Yes | Declared (`effort_pin`) |
| Model discovery | Yes | Yes | Yes | Declared (`model_discovery` or `server_binding`) |
| Context window cap | Yes | Yes | No | No |
| One-shot calls (background tasks and `attn model`) | Yes | Yes | Yes | No |
| Session activity lines | Yes | Yes | Yes | With `server_binding` and `watch` ability |
| Lost-terminal state recovery | From terminal claims | From terminal claims | From server | From terminal claims, or from server (`server_binding`) |
| Folder trust pre-check | Yes | Yes | No | No |
| Default crew prompt cache TTL | 1h | 30m | 1h | 1h |
<!-- END GENERATED HARNESS TABLE -->

Override any harness's crew prompt cache TTL with `crew.cache_ttl_seconds.<harness>` (or `crew.cache_ttl_seconds` for all harnesses).

Compact on close (`attn agent close --self --compact` and standby compaction) runs over a harness's direct channel or server: the [Claude mod](using-claude-mod.md) for Claude Code, [shared Codex](using-shared-codex.md) for Codex, the [Sigma](using-sigma.md) server for Sigma, a plugin that declares `message_delivery` and `compaction`, or a server-backed plugin that declares the `compact` ability.

Kept conversation copies in the Ledger are supported for Claude Code (see [Sessions](sessions.md)).

## Choosing a harness

Attn requires an explicit harness choice and does not fall back to a built-in default when you start a session, add a crew member, delegate from a shell, or configure an automation or background agent. Pick a harness in **Settings → Agents and models**, in the picker or launcher, in crew setup, in automations, and in **Settings → Background agents**.

For how delegations choose a harness (including delegating from a shell), see [Delegation](delegation.md).

## Harness-specific pages

- [Using the Claude mod](using-claude-mod.md): direct delivery and compaction for Claude Code sessions.
- [Using shared Codex](using-shared-codex.md): Codex terminals sharing one app-server, updates, and folder trust.
- [Using Sigma](using-sigma.md): experimental server-backed Sigma sessions, providers, and approvals.
- [Writing a harness plugin](writing-harness-plugins.md): custom harnesses with `driver.register`, capabilities, logos, and `attn-app-server-v2`.

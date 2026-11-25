# Codex harness behavior

Plain Codex TUI, rollouts, memories, updates and `codex exec`. For the shared
app-server and `--remote` TUI, see [Codex app-server](codex-app-server.md).
Rules for adding claims: [Harness behavior](../harnesses.md).

## Lifecycle, hooks and TUI

- **SessionStart:** sources are `startup`, `resume`, `clear`, `compact`, `fork`
  `[60947e2341]`. A new chat fires SessionStart lazily at the first turn,
  before UserPromptSubmit. `/new` reports `startup`; `/clear` reports `clear`.
- **Sticky instructions:** `-c developer_instructions=...` stays in force after
  `/clear` and `/new` `[0.166.0]`.
- **Subagents & `/agents`:** `/agents` switches the terminal among root
  sessions (including sessions from other terminals); `/multi-agents` switches
  subagents. Subagents fire `SubagentStart`, never `SessionStart`/`SessionEnd`
  `[60947e2341]`.
- **Hook execution & types:** only `command` and `mcp_tool` hooks run (`prompt`
  and `agent` are skipped) `[0.164]`. Plain PTY hooks inherit `ATTN_TERMINAL_ID`;
  `--remote` hooks run in the app-server and cannot name the terminal. When
  Stop fires, all `token_count` events are already in the rollout (only
  `task_complete` follows) `[0.164.0]`.
- **Idle startup signal:** launched with no prompt in a trusted folder, the TUI
  sets its title and emits OSC 7501 `state=idle` once chat starts (~2.7 s)
  `[0.164.0]`.
- **Single-writer lock screen:** plain `codex resume <id>` or `/resume <id>` on
  a conversation held by another process shows a lock screen (`r` retry, `f`
  fork, Esc/`^C`/`q` exit) without firing hooks or writing. `f` forks with
  `session_meta.forked_from_id` (attn's SessionStart matcher omits `fork`, so
  attn learns of it at UserPromptSubmit). Esc exits 0 `[0.162.1]`.

## `/side` ephemeral conversations

`[0.167.0]`

- `/side` forks an ephemeral thread in-process: own `session_id`, no rollout,
  `transcript_path: null` in every hook. `CODEX_THREAD_ID` is set in shell
  tools, unset in hooks.
- Fires SessionStart `fork`, UserPromptSubmit, PreToolUse/PostToolUse, Stop.
  `ctrl-/` switches without hooks; `ctrl-c` discards without SessionEnd/Stop.
  Parent and side can run turns concurrently; TUI status follows whichever is on
  screen.
- `shell_environment_policy.set.<NAME>` in the ephemeral `thread/fork` `config`
  reaches shell tools (merged with server env), not hooks.

## Rollouts and local memories

- **Rollout format `[0.167.0]`:** no rollout is written until the first prompt
  (filename has launch time, `session_meta` has first-prompt time).
  `session_meta.history_mode` is `paginated` (default in 0.167.0) or `legacy`
  (never mixed in one file):
  - `paginated`: writes `event_msg` `item_completed` (`item.type: "UserMessage"`,
    etc.), never `user_message`/`agent_message`/`agent_reasoning`.
  - `legacy`: writes `user_message`, `agent_message`, `agent_reasoning`.
  - In both modes, `response_item` `role: "user"` also holds `AGENTS.md` and
    environment context, not just user prompts.
- **Disabling local memories `[4aa94dce]`:** set both `features.memories=false`
  (disables instructions + startup pipeline) and
  `memories.generate_memories=false` (prevents later sessions from extracting
  from this conversation). `memories.use_memories=false` alone leaves the
  pipeline running.

## MCP over HTTP

`[0.167.0–0.171.0]`

- Sends `initialize` (`2025-06-18`), `notifications/initialized`, `tools/list`,
  no GET. A turn's tool list waits for starting servers up to
  `startup_timeout_sec` (default 30 s) unless a cached catalog exists `[0.164]`.
- `-c mcp_servers.<name>.bearer_token_env_var="ATTN_AGENT_CREDENTIAL"` sends
  `Authorization: Bearer <env>` without exposing the token in argv.
  `default_tools_approval_mode = "approve"` approves direct and subagent calls.

## Updates and versions

`[0.162.1–0.163.0]`

- `codex --version` prints `codex-cli <version>` in 10–30 ms.
- Only TUIs write `<CODEX_HOME>/version.json` (non-atomic, refreshed in
  background if missing or >20 h old). `-c check_for_update_on_startup=false`
  disables the update prompt, banner and `version.json` refresh.
- `codex doctor --json` takes 10–14 s (2–3.5 s CPU) and exits 1 if any check
  fails while still printing full JSON to stdout (`codexVersion`,
  `checks["updates.status"].details["latest version"]`; skips npm `latest`
  check on npm installs).
- `codex update` supports only npm (`CODEX_MANAGED_BY_*`), standalone
  (`<CODEX_HOME>/packages/standalone/releases/`), and macOS Homebrew
  (`/opt/homebrew`, `/usr/local`); Linuxbrew and `app-server-daemon` binaries
  exit 1. It refuses `--remote`. Only standalone keeps old version directories;
  npm and brew cask delete the old binary after install.

## Headless runs (`codex exec`)

`[0.167.0–0.171.0]`

- `codex exec -` reads stdin up to 1,048,576 chars (1 char more fails before
  any request with `input_too_large`).
- `-c model_instructions_file="<path>"` replaces base instructions without
  putting text in `ps` or hitting Linux's 128 KiB argv limit (tested to 1 MiB).
- **Tool/context stripping:** set `features.view_image=false`,
  `features.sleep_tool=false`,
  `tools.experimental_request_user_input.enabled=false`, `agents.enabled=false`,
  `skills.include_instructions=false`, `include_permissions_instructions=false`,
  `include_apps_instructions=false`, `include_collaboration_mode_instructions=false`,
  `include_environment_context=false`.
- **Unremovable tools & quirks:** models with `tool_mode: code_mode_only` still
  declare sandboxed `exec` and `wait` (in `additional_tools`, not top-level
  `tools`). Models listing `send_message_to_user_async` print its output as an
  `agent_message` line on stdout. A catalog entry declaring zero tools causes
  `0.171.0` to send `model.base_instructions` twice.
- **`CODEX_HOME/AGENTS.md`:** always sent as a user message in every root
  conversation, even under `--ignore-user-config`; no setting disables it
  `[0.171.0]`.

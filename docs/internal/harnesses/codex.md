# Codex harness behavior

How Codex behaves where attn meets it, apart from [its
app-server](codex-app-server.md). The rules for adding a claim are in [Harness
behavior](../harnesses.md).

Read in the source at openai/codex 60947e2341.

- SessionStart sources are `startup`, `resume`, `clear`, `compact` and `fork`
  (`codex-rs/hooks/src/events/session_start.rs`).
- A new chat's SessionStart fires lazily, at the start of its first turn,
  followed by that turn's UserPromptSubmit (`core/src/session/turn.rs`).
- `/new` starts a new chat, after a worktree picker when managed worktrees are
  available. Its SessionStart reports `startup`. `/clear` clears the terminal
  and starts a new chat. Its SessionStart reports `clear`.
- Launch instructions from `-c developer_instructions=...` stay in force
  after `/clear` and `/new` (probed on 0.166.0 with the real model).
- `/agents` opens the agent command center. The command center lists **root
  sessions**, including sessions running in other terminals, and switches this
  terminal to the session picked (`tui/src/app/agents_overview.rs`). The
  command center does not switch subagents.
- `/multi-agents` switches between this session's subagents. Subagent threads
  fire no SessionStart or SessionEnd. A spawned subagent thread fires
  SubagentStart (`core/src/hook_runtime.rs`).
- Codex supports only `command` and `mcp_tool` hooks; `prompt` and `agent`
  parse but are skipped (0.164, `codex-rs/config/src/hook_config.rs`).
- An HTTP MCP server gets `initialize` at protocol 2025-06-18, then
  `notifications/initialized` and `tools/list`, and no GET (probed on 0.171.0
  with `codex exec`; captures on `s-vadq83`). `bearer_token_env_var` names the
  environment variable that holds the bearer token, and
  `default_tools_approval_mode = "approve"` runs the server's tools without
  asking.
- Hooks run in the process that runs the turn. For a PTY launch, that process
  is the terminal's own Codex process, so `ATTN_TERMINAL_ID` names the terminal
  (older launches use `ATTN_SESSION_ID` with the same meaning). For a
  `--remote` TUI, that process is the app-server, so a hook there cannot name
  the terminal.
- When Stop fires, the rollout already holds every `token_count` event of the
  turn. Only `task_complete` comes after them (probed on 0.164.0 in
  `codex exec`, with a Stop hook that copied the rollout; the TUI was not
  probed).

Launched with no prompt in a trusted folder, the TUI sets its terminal title and
reports `state=idle` with OSC 7501 once its chat starts, 2.7 s after launch in one
probe, so attn hears from it without a first turn. Probed on 0.164.0 on Linux
through a PTY with an isolated `CODEX_HOME`.

Probed on 0.162.1 with a mock model and an isolated `CODEX_HOME`:

- A TUI writes no rollout until its first prompt. The rollout's file name
  carries the launch time. The `session_meta` timestamp is the first prompt's.
- A plain TUI refuses a conversation another process has open, both as
  `codex resume <id>` and with `/resume <id>` inside a running TUI. It shows
  "This conversation is open in another app. Close it there and press R to
  continue here." with keys `r` retry, `f` fork, and Esc, `^C` or `q` to exit.
  No hook fires and nothing is written.
- `r` keeps the lock while the holder lives. Once the holder exits, `r` loads
  the conversation. The first prompt of the conversation then fires
  SessionStart `resume`, then UserPromptSubmit, and the turn appends to the
  conversation's rollout.
- `f` starts a new conversation whose `session_meta` has `forked_from_id` and
  which carries the old history. Its SessionStart `fork` fires on the first
  prompt. attn's SessionStart matcher leaves `fork` out, so under attn's hooks
  attn first learns of the fork from UserPromptSubmit.
- Esc on the lock screen exits the whole process with status 0 and prints "To
  continue this session, run: codex resume <id>", even when that process never
  held the conversation.

Probed on 0.167.0 with a mock model and read in the source at `rust-v0.167.0`:

- `/side` forks an ephemeral thread in the same process
  (`tui/src/app/side.rs`). It has its own `session_id`, writes no rollout, and
  reports `transcript_path` as `null` in every hook. `CODEX_THREAD_ID` is set in
  its shell tools and unset in hooks.
- Its first prompt fires SessionStart `fork`, then UserPromptSubmit. Its tool
  calls fire PreToolUse and PostToolUse, and each turn ends with Stop.
- ctrl-/ switches between the side and its parent and fires no hook. ctrl-c in
  the side discards it and fires no SessionEnd or Stop.
- A parent and its side can run turns at the same time.
- The TUI's reported status follows the thread on screen.
- A `shell_environment_policy.set.<NAME>` key in the `config` of an ephemeral
  `thread/fork` reaches the side's shell tools and merges with the same table
  set when the app-server started. It does not reach hooks.

## Rollout events

Read in the source at openai/codex 6aaac69480 (`rollout/src/policy.rs`) and
seen in rollouts written by 0.167.0:

- `session_meta.history_mode` is `legacy` or `paginated`. Each of the 130
  rollouts from 0.167.0 checked on ai-sandbox on 2026-11-17 was `paginated`.
- A `paginated` rollout records each completed item as an `event_msg` of type
  `item_completed`. A user's message is
  `{"type":"item_completed","item":{"type":"UserMessage","content":[{"type":"text","text":"..."}]}}`.
  It writes none of the legacy `user_message`, `agent_message` or
  `agent_reasoning` events, so one rollout never holds both shapes.
- A `legacy` rollout writes `user_message`, `agent_message` and
  `agent_reasoning`, and keeps `item_completed` only for items with no legacy
  equivalent.
- In both modes, `response_item` messages with role `user` also carry AGENTS.md
  and environment context, so they are not the user's prompts.

## Local memories

Read in the source at openai/codex 4aa94dce and checked against the
[configuration reference](https://developers.openai.com/codex/config-reference/):

- `features.memories=false` disables memory instructions and the startup
  memory pipeline (`features/src/lib.rs`, `core/src/config/mod.rs`,
  `memories/write/src/start.rs`).
- `memories.generate_memories=false` makes Codex record on each new
  conversation that memory generation is off (`config/src/types.rs`,
  `core/src/session/session.rs`). The setting also keeps a later session's
  memory pipeline from extracting memories from those conversations.
- `memories.use_memories=false` makes Codex skip memory instructions and leaves
  the feature and its startup pipeline enabled.

attn sets `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` for a crew member's Claude and
sets both `features.memories=false` and `memories.generate_memories=false` for
their Codex. Existing harness memory files stay in place.


## Codex updates

Read in the source at openai/codex rust-v0.163.0 and probed on 0.162.1 and
0.163.0, except where noted.

- `codex --version` prints `codex-cli <version>` in 10–30 ms.
- Only TUIs write `<CODEX_HOME>/version.json` (`latest_version`,
  `last_checked_at`, `dismissed_version`), and the write is not atomic. A TUI
  refreshes the file in the background at startup when the file is missing or
  over 20 h old, so that run still shows the old value. The latest version
  comes from a source that follows the install method: the cask API for brew,
  GitHub releases checked against the npm `latest` tag for npm, GitHub
  releases otherwise.
- `codex doctor --json` prints one JSON object after 10–14 s, of which
  2–3.5 s is CPU, probed on 0.162.1. When any check fails, `codex doctor` still
  prints the whole report, then exits 1, so read stdout regardless of the exit
  status. `codexVersion` is the binary's own version. `checks` is keyed by
  check id, and `checks["updates.status"].details["latest version"]` holds the
  newest release, checked live against the same source as a TUI's refresh,
  except that for npm installs `codex doctor` skips the check against the npm
  `latest` tag.
- At startup, `--remote` TUIs included, a TUI with a newer version cached shows
  a full-screen update prompt and an in-transcript banner. An initial prompt on
  the command line skips only the prompt. "Update now" exits the TUI and runs
  the installer in the same terminal, which exits 0 when the update succeeds.
  `-c check_for_update_on_startup=false` turns off the prompt, the banner and
  the `version.json` refresh.
- `codex update` picks the install method from the `CODEX_MANAGED_BY_*` that
  npm's `bin/codex.js` sets, then from the binary's path: standalone under
  `<CODEX_HOME>/packages/standalone/releases/`, brew on macOS under
  `/opt/homebrew` or `/usr/local`. Anything else fails with exit 1, including
  Linuxbrew and the binaries Codex's managed app-server daemon installs under
  `<CODEX_HOME>/packages/app-server-daemon/`. `codex update` runs the installer
  without checking versions first, prompts for nothing itself, and refuses
  `--remote`.
- Whether an old binary stays launchable after an update depends on the
  install:
  - Standalone: each version keeps its own
    `<CODEX_HOME>/packages/standalone/releases/<version>-<target>/`, nothing
    prunes them, and `current` and `~/.local/bin/codex` are swapped
    atomically.
  - npm: the native binary's path has no version in it. The install moves the
    old package aside, extracts the new package at the same path, then deletes
    the old package.
  - brew cask, read in its source only: `brew upgrade` renames
    `Caskroom/codex/<old>` aside before installing and deletes it once the new
    version is in.

## Headless runs

attn runs `codex exec` for headless tasks and cold model calls
(`internal/agent/codex.go`). Probed on 0.167.0 with attn's arguments, a
loopback Responses API set as `openai_base_url`, and the model catalog of a
signed-in `CODEX_HOME` passed as `model_catalog_json`, 2026-11-20 (seed
`s-63n4ng`, whose C13 evidence holds the captures):

- `codex exec -` reads the prompt from stdin. A prompt of 1,048,576
  characters is sent whole as the user message. One character more fails
  before any request with `input_too_large`: "Input exceeds the maximum length
  of 1048576 characters".
- `-c instructions="..."` replaces Codex's base instructions. The request
  carries them as its first developer message; the top-level `instructions`
  field stays empty, as it does for Codex's own prompt.
- Tools travel in an `additional_tools` input item, not the top-level `tools`
  array, which stays empty. Without the settings that follow, a tool-free run on
  gpt-5.6-terra declared `exec`, `wait`, `request_user_input` and six
  multi-agent tools; gpt-6.2-sol added `request_user_input_async`,
  `send_message_to_user_async` and `sleep`.
- These remove tools or context blocks: `features.view_image=false`,
  `features.sleep_tool=false`,
  `tools.experimental_request_user_input.enabled=false`, `agents.enabled=false`
  (the multi-agent tools), `skills.include_instructions=false` (the skills
  list, which names the user's skill folders), and
  `include_permissions_instructions`, `include_apps_instructions`,
  `include_collaboration_mode_instructions` and `include_environment_context`
  set to `false`. With them, the request holds only the instructions as a
  developer message and the user message.
- What no setting removes comes from the model catalog. Every model there has
  `tool_mode: code_mode_only`, so every request declares `exec` and `wait`.
  `exec` runs JavaScript in a V8 isolate with no file system, network or
  console, and calls only nested tools, of which a tool-free run has none.
  gpt-6.2-sol and gpt-6.2-astra list `send_message_to_user_async` in
  `experimental_supported_tools`. Under `codex exec` that tool's call returns
  `{"accepted":true}` and prints its message as an `agent_message` line on
  stdout; the provider saw nothing else. attn takes the last `agent_message`
  line as the answer, so the message stays in the call's output.
- A catalog entry with `tool_mode: direct`, no `experimental_supported_tools`
  and no `apply_patch_tool_type` declares no tools. Codex reads the catalog
  from its private model cache.

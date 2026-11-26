# Codex app-server and `--remote` behavior

Shared app-server and `--remote` TUI behavior. For plain Codex, see
[Codex](codex.md). Rules for adding claims: [Harness behavior](../harnesses.md).

## Socket, startup and hooks

- **Socket path:** `codex app-server --listen unix://PATH` serves WebSocket
  (`GET /rpc`); `PATH` is a symlink into `/tmp/codex-daemon-<uid>/` named by
  SHA-256 of canonicalized `PATH` `[0.158.0–0.167.0-alpha.4]`. Resolve the
  symlink before dialing long paths. On Linux, `systemd-tmpfiles` can delete
  the aged symlink while the listening socket stays reachable `[0.164.0]`.
- **Hooks:** server `-c` hooks are session-flag hooks trusted via
  `hooks.state."/<session-flags>/config.toml:<event>:0:0".trusted_hash`. They
  run in the server's env with `cwd` = conversation cwd and stdin carrying
  `session_id`, `transcript_path`, `cwd` `[0.160.0]`. Shell tools inherit
  `CODEX_THREAD_ID` and server `-c shell_environment_policy.set.*` across all
  conversations `[0.160.0]`.
- **Open-file limit:** Codex never raises `RLIMIT_NOFILE`; processes launched
  from a macOS app inherit 256 and fail under concurrent `command/exec` with
  `os error 24` (`openai/codex#44682`) `[0.162.1]`.
- **Sandbox + attn socket:** under `workspace-write`, `on-request` and code
  mode, `exec_command` fails to dial attn's Unix socket (`operation not
  permitted`) unless allowed via `prefix_rule` in
  `<CODEX_HOME>/rules/default.rules` `[0.165.0]`.
- **Server version:** `initialize` returns `userAgent` as
  `<originator>/<version> (...)`, where `originator` is fixed to the first
  client's `clientInfo.name` `[0.162.1]`.

## Conversations, resume, permissions and subagents

- **Disk persistence trap:** `thread/start` writes nothing to disk until
  `thread/inject_items` (developer message) writes it; `thread/resume` fails
  with `no rollout found` if never written, breaking TUI reconnects `[0.160.0]`.
  With a read-only `sessions/`, `thread/inject_items` fails (`Permission
  denied`) while `turn/start` still runs without saving a rollout, so attn must
  refuse a new conversation whose inject fails `[0.166.0]`.
- **Single writer:** while an app-server holds a conversation loaded, another
  app-server or plain `codex resume` gets `already has an active writer` until
  it unloads (~60 s after going idle with no subscribers) `[0.160.0]`.
- **`thread/resume` settings traps:**
  - Always ignores `developerInstructions` (a conversation keeps its initial
    instructions) `[0.160.0]`.
  - Applies `approvalPolicy`, `approvalsReviewer`, `sandbox`, and `config`
    **only** when loading an unloaded or unsubscribed idle conversation;
    ignored if any connection is subscribed or a turn is running `[0.162.1]`.
  - Without those fields, restores approval policy and reviewer, but restores
    sandbox only from a permission profile (otherwise falls back to the
    server's configured sandbox, e.g. `read-only`) `[0.162.1]`.
  - Rewrites any non-running in-progress turn as interrupted `[1ffa95e]`.
- **`--remote` TUI permission traps `[0.160.0–0.163.0]`:**
  - Refuses `--add-dir` (use experimental `runtimeWorkspaceRoots` on
    `thread/start`, which replaces `[cwd]`; `/tmp` stays writable).
  - Refuses to resume with `"Permission overrides are not supported when
    resuming a remote task."` if launched with
    `--dangerously-bypass-approvals-and-sandbox`, `-c` approval/sandbox flags,
    or after `/permissions`.
- **Archive & rename:** `thread/archive` shuts down a loaded conversation
  (aborting running turns/compacts) and moves the rollout to flat
  `archived_sessions/`; `thread/unarchive` restores it `[0.164.0]`.
  `thread/name/set` appends to `$CODEX_HOME/session_index.jsonl` (visible to
  all servers on that `CODEX_HOME`) `[0.162.1]`.
- **Subagents & `/agents`:**
  - Server-side `spawn_agent` emits `thread/status/changed` (no
    `thread/started`) plus child turn/item/hook notifications; `thread/read`
    returns `parentThreadId` and survives daemon restarts (`thread/loaded/list`
    still lists the child without `thread/started` replay) `[0.165.0]`.
  - `/agents` resumes the picked root conversation, except an unprompted
    conversation the TUI itself started, where the TUI stays subscribed and
    switches silently until the first `turn/start` `[0.162.1]`.
  - `/new` and `/clear` call `thread/start` before the first prompt, queuing
    SessionStart until the first turn `[0.162.1]`.

## Turns, steering, interrupt and status

- **Turn control `[0.167.0]`:**
  - `turn/steer` requires `expectedTurnId`; refuses compact turns with
    `activeTurnNotSteerable` (`thread/compact/start` runs as a turn; TUI queues
    user prompts until it ends).
  - `turn/interrupt {threadId, turnId}` replies `{}` before the turn finishes;
    `thread/status/changed` `idle` and `turn/completed` `interrupted` follow
    within milliseconds. Interrupted commands stay alive as background
    terminals.
  - `turn/start` while active returns the running turn's id without starting a
    new turn; call after `idle`.
- **Notifications & approvals:** every connection gets `thread/status/changed`
  (`idle`, `notLoaded`, `active` with `waitingOnApproval` /
  `waitingOnUserInput`), `thread/name/updated`, `thread/closed`. Only
  subscribers get turns, items, and approvals (resuming replays pending
  approvals with the same id; any subscriber settles them) `[0.160.0]`.
- **Stop hook ordering:** Stop runs inside the turn before `idle`, so `idle`
  without Stop means interrupted or cut off `[1ffa95e]`.
- **OSC 7501 & guardian:** TUI emits `app=codex` `idle|working|blocked`
  (`kind=permission|question` in builds after `#54114`), ignoring
  `thread/status/changed`. Under the guardian reviewer, `PermissionRequest`
  fires before review with no answer hook, and the TUI stays `working`
  (guardian-approved background terminals may emit no `PostToolUse`)
  `[0.163.0–0.164.0]`.
- **Background terminals:** conversation is `idle` while post-turn background
  terminals run (`thread/backgroundTerminals/list`), while the TUI reports
  `working` until they exit `[0.164.0]`.
- **Stuck MCP startup round `[0.164.0–0.167.0]`:** TUI reports `working` until
  every MCP server from its launch config leaves `starting`
  (`mcpServer/startupStatus/updated`, not replayed, 30 s default
  `startup_timeout_sec`). Joining a conversation mid-round or removing an MCP
  server from `config.toml` after TUI launch can leave the TUI permanently
  reporting `working` across later turns while prompts still run.

## Folder trust

`[0.164.0–0.167.0]`

- App-server and plain TUI resolve trust from the exact folder or its git repo
  root (main repo for worktrees), never other ancestors.
- Both `--remote` and plain TUIs ask the server via `config/read`, so `-c
  projects."<folder>".trust_level` on the TUI does not bypass the prompt. Plain
  TUI prompts only inside a project (git repo or `project_root_markers`);
  `--remote` prompts inside and outside projects `[0.167.0]`.
- On a non-default daemon socket, `--remote` checks only the exact folder
  (prompting in every new worktree unless a `.codex/` directory exists under
  the trusted repo root). Explicitly untrusted folders cause `--remote` to exit
  at launch.
- While `"Trust this folder?"` (`"1. Trust and continue"`) is open, the TUI
  sets no terminal title (OSC 0/2) and sends no OSC 7501 `[0.167.0]`; losing
  the server leaves the TUI unable to reconnect (`remote app-server worker
  channel is closed` on accept).

## Drain, shutdown and TUI reconnect

`[0.162.1–0.167.0]`

- **Drain:** first SIGTERM/SIGINT (or SIGHUP, which can never force exit)
  starts an unbounded drain—rejecting `turn/start`, `turn/steer`,
  `thread/{start,resume,fork,archive}` with `-32600` (`data.reason:
  "serverShuttingDown"` in `0.163.0+`), while reads, `turn/interrupt` and
  approvals still work. Pending approvals, questions, and title generation keep
  the drain open. Second SIGTERM/SIGINT forces exit.
- **Shutdown:** closes socket and drops clients with raw EOF (no WS close
  frame), then shuts down conversations (up to 10 s each; slow when MCP servers
  are running, 9–16.5 s observed) while ignoring signals.
- **Process-group / npm / PTY host traps:**
  - npm's `bin/codex.js` forwards SIGTERM to the native binary, so one SIGTERM
    to the process group hits the server twice and forces exit; a second
    SIGTERM to node alone is not forwarded.
  - If a PTY host dies, SIGHUP leaves an active app-server draining forever
    reparented to launchd—attn must kill the terminal's process group when its
    host/worker dies.
- **TUI reconnect `[0.163.0]`:** retries at 0, 1, 2, 4 s then every 8 s up to
  120 s (keeping typed drafts; reporting `working` during reconnect), then
  stays permanently offline. Pending approvals do not survive server restart.
  Against a draining server, `turn/steer` exits 1, `turn/start` drops the
  prompt, and a new `--remote` TUI exits 1. A TUI waiting on `thread/start`
  tolerates a 60 s server hold `[0.166.0]`.

## attn tools over MCP and ephemeral model calls

`[0.171.0]`

- **Per-conversation MCP config:** pass `mcp_servers.<name>.{url,
  http_headers.Authorization, tools.<tool>.approval_mode}` (the per-tool key
  measured on 0.167.0) on `thread/start` (inherited by subagents; pass on
  `thread/fork` for `/side`), optionally with `omit_tools_from=["deferred"]`. Kept for the conversation's
  loaded lifetime (later subscribed `thread/resume` ignores `config`).
- **`tools/call` caller identity:** `_meta.threadId` is the calling
  conversation (subagent's own id, or ephemeral `/side` id), matching
  `CODEX_THREAD_ID`; `_meta.sessionId` is the root conversation. Server `name`
  hyphens become underscores in code-mode tool names. Deferred namespace
  listings truncate `initialize` `instructions` to the first line (max 250
  chars). In code mode, `outputSchema` TypeScript generation expands at most 32
  `$ref`s per schema (property-name order) and emits `unknown` for later ones
  `[0.167.0]`. MCP calls run in the app-server, outside the sandbox.
- **Ephemeral model calls (`internal/harness/internal/codex/oneshot.go`):** run
  as an ephemeral `thread/start` on the shared app-server with
  `features.hooks=false`, `mcp_servers.<name>.enabled=false` for user MCP
  servers, and the `codex exec` tool-stripping flags. Keeps user `config.toml`
  settings (unlike `codex exec --ignore-user-config`), never writes to disk,
  and unloads after closing the connection. First turn still pays ~0.5 s for an
  unskippable git-attribution HTTP fetch (~2.95 s median vs ~4.48 s via
  `codex exec`).

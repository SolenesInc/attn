# Claude Code harness behavior

Rules for adding claims: [Harness behavior](../harnesses.md).

## Lifecycle, hooks and transcripts

`[2.1.288–319]`

| Action | Hooks (in order) | Conversation id |
|---|---|---|
| Launch `--session-id <uuid>` | SessionStart `startup` | that id; no transcript until first prompt |
| `/clear` | SessionEnd `clear`, SessionStart `clear` | new |
| `/resume <id>` or picker | SessionEnd `resume`, SessionStart `resume` | target (same id twice is a no-op) |
| `/branch` | SessionEnd `resume`, SessionStart `fork` | new |
| `/compact` | PreCompact, SessionStart `compact`, PostCompact | unchanged |
| `claude -r <id>`, `--continue` | SessionStart `resume` | unchanged (`--fork-session` gives `fork` + new id) |
| `/rewind` | none | unchanged |
| Plan mode "clear context and proceed" | SessionEnd `clear`, SessionStart `clear`, then turn + Stop (no UserPromptSubmit) | new |

- **Hook execution:** SessionStart blocks Claude until exit; SessionEnd is cut
  off after ~1.5 s and may never arrive. `command` hooks run under `/bin/sh`
  regardless of `$SHELL` `[2.1.319]`. `--settings <JSON>` accepts inline hooks
  `[2.1.296]`.
- **Transcript path & timing:** `<config>/projects/<cwd-slug>/<id>.jsonl`. When
  Stop fires, the final assistant message and usage are already written (only
  `stop_hook_summary`, `last-prompt`, `cost-state` follow) `[2.1.316]`.
  However, a Bash tool that terminates the session (e.g. filing a crew letter)
  can exit before Claude persists that final tool call `[2.1.310]`.
- **Forks & duplicate writers:** `--resume <id> --fork-session` copies prior
  assistant messages preserving `message.id` and usage `[2.1.303]`. Opening the
  same conversation in a second process is not refused; both append separate
  `parentUuid` branches to `<id>.jsonl` with the same conversation id
  `[2.1.309]`.
- **Sticky launch instructions:** `--append-system-prompt` stays active after
  `/clear` `[2.1.314]`. Subagents do not receive it (they use their agent
  definition); forks inherit their parent's system prompt including the
  appended text `[2.1.332]`.
- **Interactive paste wrapping:** with `tengu_virtual_pancake` enabled, pasted
  input may be wrapped in `\n\n<pasted_content id="hhhh">\n...\n</pasted_content id="hhhh">\n`;
  parsers must handle both wrapped and plain text `[2.1.310]`.

## Terminal boot, input, trust and config

- **Raw mode & boot timing:** after boot, CR submits and LF inserts a newline;
  until Claude enters raw mode, the PTY translates CR to LF `[2.1.318]`. Lines
  typed before the terminal title appears (including at SessionStart) are lost
  or left unsubmitted; typing once the title or UserPromptSubmit arrives
  submits reliably `[2.1.319]`.
- **Composer suggestions:** indistinguishable from a typed draft in plain text,
  but dim with cursor on the first cell in cell styles (same as the empty
  `Try "…"` placeholder) `[2.1.319]`. Enter alone leaves a suggestion unsent;
  paste+Enter replaces a suggestion but appends to a typed draft `[2.1.318]`.
  Disable via `CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false` or
  `promptSuggestionEnabled: false` `[2.1.310]`.
- **Config locking (`.claude.json`):** Claude updates `.claude.json` under
  `<file>.lock` (stale after 10 s; observed max hold 407 ms; attn waits 2 s)
  and rereads inside the lock. SIGKILL during startup leaves the lock behind;
  SIGTERM (~0.8 s) and EOF on stdin (~1.3 s) clean it up `[2.1.319]`.
- **Folder trust:** resolved at the main git repo root (stopping at repo root),
  covering subfolders and worktrees; trust on a worktree covers only that
  worktree; trust on a parent covers non-git subfolders only. Answering the
  dialog (`"Yes, I trust this folder"`, appears 0.8–2.2 s after start; no title
  or SessionStart fired while open; `"No, exit"` exits 1) writes
  `projects[<main repo>].hasTrustDialogAccepted` `[2.1.319]`. Mirror this in
  `internal/harness/internal/claude/trust.go`.
- **Isolated `CLAUDE_CONFIG_DIR`:** starts logged out and opens the theme
  picker unless `<config>/.claude.json` sets `hasCompletedOnboarding: true`,
  `theme: "dark"`, and `projects[<cwd>].hasTrustDialogAccepted: true`
  `[2.1.310]`.
- **Auxiliary traffic & memory:** `--name <name>` suppresses auxiliary Haiku
  title calls (`ATTN_HEADLESS_TASKS=off` does not) `[2.1.310]`.
  `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` disables auto memory for one process
  (a new cwd alone does not) `[2.1.310]`.
- **CLI arg traps:** `--mcp-config` is variadic; place a following flag (e.g.
  `--setting-sources ''`) before any subcommand so `--mcp-config` does not
  consume it `[2.1.310]`. `--setting-sources ''` can still list skills/commands
  in print-mode init metadata; `--disable-slash-commands` empties them
  `[2.1.310]`.

## Program status (OSC 7501)

`[2.1.295–319]`

- At startup Claude sends `OSC 7501 ; ?` before DA1 and emits reports only if a
  reply starting with `?` arrives before DA1 (never re-queries).
  `CLAUDE_CODE_DISABLE_TERMINAL_TITLE` disables both titles and OSC 7501.
- Reports are edge-triggered (no heartbeat) on `app=claude-code`; background
  agents emit child records (`id=<agent id>`), which attn ignores. Exit/suspend
  emits `state=clear`.
- Mapping: busy or queued prompt → `working`; permission/sandbox/worker/goal →
  `blocked kind=permission`; AskUserQuestion/MCP elicitation →
  `blocked kind=question` (~20 ms after PreToolUse, PermissionRequest ~40 ms);
  other dialog → `blocked`; login failure → `blocked kind=auth`; turn done →
  `done`; fresh/interrupted → `idle`; failure → `error`.
- Interactive menus (`/model`, `/resume`, `/config`, `/permissions`, Esc-Esc
  rewind) emit neither OSC 7501 nor hooks `[2.1.303]`.

## Idle auto-compaction

`[2.1.318–319]`

- Fires 54 min after the last main-thread request (6 min before 1h cache
  expiry); Stop and unsent drafts do not reset it.
- Skips silently (no hooks) if auto-compact/`idleCompaction` is off, prefix
  changed, cache is not 1h or expired, timer >60 s late, context <
  `max(100000, CLAUDE_CODE_IDLE_COMPACT_MIN_TOKENS ?? 200000)`, newer request
  exists, or rate-limit status is not `allowed` (e.g. `allowed_warning` from
  `anthropic-ratelimit-unified-*-surpassed-threshold` or pacing table).
- Hooks on success: PreCompact `auto`, SessionStart `compact`, PostCompact
  `auto` (~6.5 s, carrying last turn's `prompt_id`), no UserPromptSubmit/Stop.
  On failure: PreCompact only, no screen error. OSC 7501 transitions `working`
  → `done` either way (so `done` without PostCompact = failed compact). Unsent
  composer drafts survive.
- Local verification: `go run ./scripts/fakeclaudeapi check-idle` (~58 min real
  timer; requires `ENABLE_PROMPT_CACHING_1H=1` for API-key sessions, which
  ignore rate-limit headers). Re-run after Claude updates and update this doc,
  the fake API and attn together on failure.

## Agent view (background daemon)

`[2.1.312]`

- `←` on an empty prompt or `/background` (`/bg`) forks the conversation into
  Claude's per-config daemon (`/tmp/cc-daemon-<uid>/<sha256[:8]>`) and switches
  the terminal to `claude agents`. Workers inherit the daemon starter's env
  (wrong `ATTN_TERMINAL_ID`) and attach/detach fires no hooks.
- attn launches Claude with `CLAUDE_CODE_DISABLE_AGENT_VIEW=1` (also inherited
  by nested `claude` calls) so conversations cannot escape into Claude's daemon.

## attn tools over MCP and auto mode

`[2.1.330]`

- HTTP MCP opens with `server/discover` (protocol `2026-07-28`); on `-32601`,
  Claude falls back to `initialize` (`2025-11-25`) + `notifications/initialized`,
  accepts `405` on GET, and lists/calls tools.
- Inline `--mcp-config` expands `Bearer ${ATTN_AGENT_CREDENTIAL}` from env
  without exposing the token in `ps`. `permissions.allow: ["mcp__attn__*"]`
  approves direct calls in auto mode. Background subagents inherit the parent
  credential; the server survives `/clear` and daemon restarts without
  reconnecting.
- **First-turn MCP race `[2.1.329–332]`:** interactive Claude sends its first
  request without waiting for MCP servers to connect, so CLI or immediate
  prompts can miss deferred MCP tools (`alwaysLoad: true`,
  `MCP_CONNECTION_NONBLOCKING=0`, `SessionStart` after `tools/list`, and
  `UserPromptSubmit`/`prompt.submit` waits do not prevent this). A mod's
  `session.start` hook holds CLI/composer prompts up to 10 s (not
  `$.prompt.submit`); awaiting `$.mcp.connect('attn')` there gives the first
  turn the tools (~0.15 s idle; refused/500 fails in ~7.6–8.1 s, 401 in ~0.1 s,
  unresponsive servers hang >80 s, unlisted resolves immediately). attn caps
  that wait at 8 s before starting its poll, and says in the debug log
  (`--debug-file`) when the cap or a refusal ended it; a rejected bearer token
  reads `failed`, not `auth` `[2.1.329–332]`. `$.mcp.connect` waits for a pending
  connection on 2.1.329, the oldest probed; on an older one that answers at
  once, the mod logs that answer and starts the turn, so it keeps the race
  (read from the mod, not probed). A session without the mod keeps it too.
- Under `CLAUDE_CODE_EXPERIMENTAL_CODE_MODE=1`, auto mode checks each REPL
  script once before execution and does not apply `mcp__attn__*` allow rules to
  calls inside the script.
- Auto mode sends permission rules to its classifier `[2.1.332]`: passing
  `--disallowed-tools ListAgents` causes shell `attn agent list` calls to be
  refused as `[Undermining Oversight]`, so attn disallows no tool and relies on
  launch instructions instead. Auto mode also refuses shell `attn ask close` +
  `attn seed harvest` for asks not created in the session's transcript (the
  `ask_close` MCP tool succeeds).
- `autoMode.allow` merges policy + `--settings` + user settings (not project
  settings); omitting `"$defaults"` when setting `allow` drops Claude's 16
  built-in defaults (`claude [--settings <json>] auto-mode config` prints the
  merged list).
- Fresh configs prompt `"Make auto mode your default permission mode?"`
  (`hasSeenAutoDefaultNotice` alone does not skip it when `defaultMode` is
  `default`). Under `--permission-mode auto`, a blanket `Bash` allow rule does
  not bypass auto-mode checks unless `permissions.disableAutoMode: "disable"`
  `[2.1.318–319]`.

## Mods (`--plugin-dir` TypeScript hooks)

`[2.1.316–330]`

- **Loading & lifecycle:** `--plugin-dir` / `CLAUDE_CODE_PLUGIN_DIRS` load
  without consent; content edits (not `touch`) reload the module and re-run
  `session.start` (invalid syntax keeps the old module running). `$.state`
  survives reloads; module globals reset. `/clear` and `/resume` fire
  `session.end` without re-running `session.start`; `$.session.id()` updates.
  First poll arrives 0.7–5.5 s after launch in trusted folders, and never
  before the folder-trust prompt is answered.
- **Sockets & env:** `$.http.fetch` with `socketPath` rejects after 30 s with
  no response (aborted on reload/`/exit`). `$.env.get` returns a Promise in
  worker-hosted modules. `$.ui.log` replaces newlines with U+FFFD.
- **`$.prompt.submit`:**
  - `{ text, asUser: true }`: starts a turn between turns in the user's voice;
    auto mode's classifier treats consent in it as user consent `[2.1.330]`.
  - `{ text }` (without `asUser`): starts a turn prefixed `"The attn plugin
    sent a message:"`; not treated as user voice `[2.1.330]`.
  - Calling `$.prompt.submit` during a turn waits for the turn to end (and
    rejects immediately if called inside a `tool.call` hook).
  - After user Esc during a turn, `$.prompt.submit` drops with `"the session
    was stopped and nobody has sent input since"` indefinitely until the user
    types input `[2.1.317]`.
- **`$.session.append` (steering) `[2.1.326+]`:**
  - Appends after a main-loop `tool.call` hook's `next(e)` reach the next model
    request (appends inside `turn.step` miss that request).
  - Since `2.1.326`, appended rows travel in a `role: "system"` message
    prefixed `"Note from the attn plugin, not from the user:"` (never treated
    as user consent by auto mode's classifier). In Code Mode, `tool.call` hooks
    fire only when the REPL script settles `[2.1.330]`.
- **`$.turn.abort({ turnId })` (interrupt) `[2.1.330]`:**
  - Called in `tool.call` after `next(e)`, ends the turn (`turn.complete`
    `isAborted: true`, no Stop settings hook) **without** stopping the session
    like Esc does, so `$.prompt.submit` starts the next turn immediately
    (~75–120 ms). Running Code Mode background commands keep running until the
    model stops them.
- **Permissions:** denying a permission prompt (Esc or "No") sets
  `next.signal.aborted` and ends the turn with `reason: "answer"` (unlike Esc
  during a tool, which gives `user-cancel` / `reason: "aborted"` and stops the
  session); the two denial keys are indistinguishable to a mod. Permission
  dialogs ignore keys briefly after drawing (~1 s) `[2.1.318]`.
- **Compaction (`$.session.compact()`):**
  - Compacts between turns (`trigger: "manual"`), rejecting during a turn
    (`a turn is running`, etc.) or if a turn starts mid-compact (`a newer turn
    began before the compaction could be applied`). Call without awaiting so
    the poll loop keeps running.
  - The Stop settings hook fires while Claude still considers the turn running
    (compact placed then fails intermittently); wait for the mod's
    `turn.complete` instead `[2.1.319]`.
  - Compact boundary entries flush to the transcript JSONL only on exit
    (SIGTERM) `[2.1.318]`.

## Model call spare and headless print

`[2.1.328–330]`

- **Warm spare (`internal/harness/internal/claude/spare.go`):** `--print
  --input-format stream-json --output-format stream-json --verbose` with no prompt argument
  waits on stdin (~200–300 MB RSS). Unstopped, it burns idle CPU (~1.6–1.9 s /
  10 min even with `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`).
- **Ready probe + SIGSTOP:** send `control_request` `subtype: mcp_status`
  (`get_settings` answers too early, before `session.start`) and SIGSTOP the
  process group on reply. Stopped spares use 0 CPU, still answer after 2h+
  idle (~50 ms overhead over model time vs ~1.2–4 s cold), and exit within
  200 ms if the daemon dies via SIGKILL (orphaned stopped process group gets
  SIGHUP+SIGCONT).
- **Late config & single-use:** send `control_request` `initialize` (with
  `systemPrompt`—required or it answers as Claude Code with tools) then
  `set_model`. A spare keeps conversation history across messages, so use each
  spare for one call and exit.
- **Tools & schemas:** `--disallowedTools '*'` avoids ~19.3k tokens of tool
  definitions; `jsonSchema` on `initialize` returns `structured_output` only
  under `--allowedTools`. Under `-p --output-format json --json-schema`,
  `--verbose` wraps output in an event array whose last `type: "result"` holds
  `structured_output`, `usage`, `modelUsage`, `total_cost_usd` `[2.1.310]`.
- **Large cold inputs:** `claude --print` reads stdin whole (tested to 1 MiB)
  and `--system-prompt-file` carries instructions whole (tested to 1 MiB)
  `[2.1.329–330]`.

# Harness behavior

How each harness behaves where attn meets it: which hooks fire, when, and
which conversation id they carry, what its app-server and TUI do, and how it
updates itself. Designs that react to a harness lean on these facts.

Every entry was checked against the harness's source or a probe, and names
the version it was checked on. Do not add or rely on a claim from memory:
read the source or probe the binary first, then record it here.

## attn identities at the harness boundary

`ATTN_TERMINAL_ID` identifies the launched terminal. Older launches carry the
same identity under `ATTN_SESSION_ID`. A terminal can change conversations with
`/clear` or `/resume`. CLI commands address the conversation shown when the
command starts; hooks apply to the conversation shown when the report arrives.
Under shared Codex, hooks and commands run in the app-server and name their
conversation instead: a hook by the `session_id` on its stdin, a command by
`CODEX_THREAD_ID`. Native harness conversation ids remain separate text values.

A session is attn's record of one conversation in one profile. A terminal
shows one session at a time, and a session can show in any number of
terminals. Removing a placement keeps the terminal addressable, including
after daemon restart. Closing a session ends its terminal associations. When a
terminal resumes the conversation of a session whose agent no longer runs, the
session moves into that terminal and its old tile goes away.

Plugin driver payloads retain the `session_id` wire spelling for the terminal.
Close notifications, including those completed after restart, use the original
terminal identity.

## The running version in the transcript

Each harness writes the version of the running process into its transcript, so
it stays right after the binary on disk updates. Probed on the user's own
transcripts on 2026-10-24:

- Claude Code (2.1.302, 2.1.310): every line has a top-level `version`. The
  newest one is at most 38.8 KB from the end of the file, across 300
  transcripts with lines up to 1.8 MB.
- Codex (0.162.1): the first line is `session_meta`, with
  `payload.cli_version`; that line is about 23 KB.
- Pi: the first line's `version` is the transcript format (3), not Pi's
  version.

## Claude Code

Probed on 2.1.288 with a mock API.

| Action | Hooks, in order | Conversation id |
|---|---|---|
| Launch with `--session-id <uuid>` | SessionStart `startup` | that id; no transcript until the first prompt |
| `/clear` | SessionEnd `clear`, SessionStart `clear` | new |
| `/resume <id>` or the picker | SessionEnd `resume`, SessionStart `resume` | the target; the same id twice is a no-op |
| `/branch` | SessionEnd `resume`, SessionStart `fork` | new |
| `/compact` | PreCompact, SessionStart `compact`, PostCompact | unchanged |
| `claude -r <id>`, `--continue` | SessionStart `resume` | unchanged; `--fork-session` gives `fork` and a new id |
| `/rewind` | none | unchanged |

- SessionStart blocks Claude until the hook exits. SessionEnd is cut off
  after about 1.5 s and may never arrive.
- Plan mode's "clear context and proceed" is SessionEnd `clear`, SessionStart
  `clear`, then a turn and Stop on the new id with no UserPromptSubmit.
- The transcript is `<config>/projects/<cwd-slug>/<id>.jsonl`; attn reads the
  conversation id from that name.
- `--settings <JSON>` accepts inline hook settings (probed on 2.1.296 on
  macOS and Linux). SessionStart, UserPromptSubmit and Stop use the
  `--session-id` conversation id.

Probed on 2.1.309 with a mock API and an isolated `CLAUDE_CONFIG_DIR`:

- Nothing stops a second process from opening a conversation another process
  has open. `claude -r <id>` and `/resume <id>` both fire SessionStart
  `resume` with that id, with no refusal and no fork.
- Both processes then append to the one `<id>.jsonl`, each from its own
  history in memory, and neither sees the other's turns. Each entry's
  `parentUuid` continues its own process's branch, so the transcript becomes a
  tree with two branches. A third process resuming the conversation loads the
  branch with the newest leaf.
- Every hook from either process carries the same conversation id.

Claude Code falls short of attn's session model here. When a terminal resumes
the conversation of a session whose agent still runs, the second process gets
its own session on the same conversation, so two open sessions hold it. Both
tiles warn that the other terminal has the conversation open until one process
exits or moves to another conversation.

Probed on 2.1.303 with synthetic prompts and a local mock API:

- `--resume <id> --fork-session` copies the prior assistant messages into the
  new conversation's transcript, preserving their `message.id` and usage.
  A message id therefore identifies the same request across root transcripts.

### Program status (OSC 7501)

Probed on 2.1.295 under a scratch PTY, and read in its bundled source.

- At startup Claude sends `OSC 7501 ; ?` before its DA1 query. It reports
  only when the 7501 reply arrives before the DA1 reply; otherwise it never
  asks again in that process. `CLAUDE_CODE_DISABLE_TERMINAL_TITLE` turns the
  reports off along with the title.
- It reports the root record with `app=claude-code`:

  | Claude | Report |
  |---|---|
  | busy, or idle with a queued prompt | `working`, with `progress` and `msg` |
  | permission, sandbox, worker or goal prompt | `blocked kind=permission` |
  | MCP elicitation | `blocked kind=question` |
  | other open dialog | `blocked` without a kind |
  | login failure | `blocked kind=auth` |
  | turn completed | `done` |
  | interrupted, or a fresh session | `idle` |
  | turn failed | `error`, the failure in `msg` |

- Background agents arrive as child records (`id=<agent id>`) with their own
  `working` or `blocked`; attn reads only the root record.
- A report is sent only when it changes; there is no heartbeat. When Claude
  hands the terminal back, on exit or suspend, it sends `state=clear`.
- Claude keeps setting its title glyphs. Once a terminal has a root record,
  attn ignores the glyphs until a `clear`.

Probed on 2.1.303 under a scratch PTY with a local mock API, in auto and
default permission mode:

- AskUserQuestion fires PreToolUse, then `blocked kind=question` about 20 ms
  later, then a PermissionRequest for `AskUserQuestion` about 40 ms after
  PreToolUse.
- With `/model`, `/resume`, `/config`, `/permissions` or the Esc-Esc rewind
  menu open, Claude sends no report and fires no hook.

## Codex

Read in the source at openai/codex 60947e2341.

- SessionStart sources are `startup`, `resume`, `clear`, `compact` and `fork`
  (`codex-rs/hooks/src/events/session_start.rs`).
- A new chat's SessionStart fires lazily, at the start of its first turn,
  followed by that turn's UserPromptSubmit (`core/src/session/turn.rs`).
- `/new` starts a new chat, after a worktree picker when managed worktrees are
  available; its SessionStart reports `startup`. `/clear` clears the terminal
  and starts a new chat; it reports `clear`.
- `/agents` opens the agent command center. It lists **root sessions**,
  including ones running in other terminals, and switches this terminal to the
  one picked (`tui/src/app/agents_overview.rs`). It does not switch subagents.
- `/multi-agents` switches between this session's subagents. Subagent threads
  fire no SessionStart or SessionEnd; a spawned one fires SubagentStart
  (`core/src/hook_runtime.rs`).
- Hooks run in the process that runs the turn: the terminal's own Codex
  process for a PTY launch, so `ATTN_TERMINAL_ID` names the terminal (older launches use
  `ATTN_SESSION_ID` with the same meaning); the
  app-server for a `--remote` TUI, so a hook there cannot name the terminal.

Probed on 0.162.1 with a mock model and an isolated `CODEX_HOME`:

- A plain TUI refuses a conversation another process has open, both as
  `codex resume <id>` and with `/resume <id>` inside a running TUI. It shows
  "This conversation is open in another app. Close it there and press R to
  continue here." with keys `r` retry, `f` fork, and Esc, `^C` or `q` to exit.
  No hook fires and nothing is written.
- `r` keeps the lock while the holder lives. Once it exits, `r` loads the
  conversation; its first prompt fires SessionStart `resume`, then
  UserPromptSubmit, and the turn appends to the conversation's rollout.
- `f` starts a new conversation whose `session_meta` has `forked_from_id` and
  which carries the old history. Its SessionStart `fork` fires on the first
  prompt; attn's SessionStart matcher leaves `fork` out, so under attn's hooks
  the fork first reports itself through UserPromptSubmit.
- Esc on the lock screen exits the whole process with status 0 and prints "To
  continue this session, run: codex resume <id>", even when that process never
  held the conversation.

### Codex app-server and `--remote`

Probed on 0.160.0 with a mock model. Shared Codex relies on these.

- `codex app-server --listen unix://PATH` speaks WebSocket (`GET /rpc`) on a
  socket that `PATH` links to under `/tmp/codex-daemon-<uid>/`; a client must
  resolve the link before connecting when `PATH` is long.
- Hooks given to the server with `-c` are session-flag hooks; a
  `hooks.state."/<session-flags>/config.toml:<event>:0:0".trusted_hash` given
  the same way trusts them. They run in the server's environment and working
  directory set to the conversation's; stdin carries `session_id` (the
  conversation), `transcript_path` and `cwd`.
- `thread/start` writes nothing to disk. `thread/inject_items` with a
  developer message writes the conversation at once; its first turn still
  fires SessionStart `startup`, then UserPromptSubmit.
- `thread/resume` of a conversation not on disk fails with `no rollout found`,
  even while the server holds it in memory. A `--remote` TUI that loses its
  socket reconnects and resumes the conversation it shows, so one never
  written cannot come back.
- `thread/resume` ignores `developerInstructions`: a conversation keeps the
  ones it started with. Its `config` (such as `model_auto_compact_token_limit`)
  applies when the resume loads the conversation.
- Every connection hears `thread/status/changed` (`idle`, `notLoaded`, or
  `active` with `waitingOnApproval`), `thread/name/updated` and `thread/closed`
  for every conversation; only subscribers hear turns, items and approvals.
  A conversation with no subscriber unloads about 60 s after it goes idle.
- `turn/start` runs a turn with no terminal attached; `turn/steer` adds input
  to the running turn and needs its id as `expectedTurnId`.
- A connection that resumes a conversation gets its pending approval again,
  with the same request id; any subscriber's answer settles it for all.
- After the first prompt the TUI names the conversation with an ephemeral
  `thread_title` conversation, then `thread/name/set` on the real one;
  `thread/name/set` from any connection renames it in every TUI.
- A conversation's shell tools get `CODEX_THREAD_ID` (the conversation id) and
  the server's `-c shell_environment_policy.set.*` values, in every
  conversation, including ones another client resumed.
- One process writes a conversation at a time: while an app-server holds it
  loaded, another app-server or a plain `codex resume` fails with `already has
  an active writer`. Either can take it once the holder unloads it. On 0.162.1,
  while a `--remote` TUI holds it, a plain TUI shows the lock screen described
  above, both as `codex resume <id>` and with `/resume <id>`, and writes
  nothing.
- `thread/archive` moves the rollout to `archived_sessions/` (flat, no dated
  directories) and refuses later resumes; `thread/unarchive` moves it back.

Read in the source at openai/codex rust-v0.162.1:

- Resuming appends to the existing rollout. A fork creates a new thread id
  and writes its own `session_meta` before inherited history
  (`core/src/session/session.rs`, `thread-store/src/local/create_thread.rs`,
  `rollout/src/recorder.rs`). `thread/revert` can create a new rollout file
  while retaining the thread id (`rollout/src/recorder.rs`).
- In a `--remote` TUI, `/new` and `/clear` send `thread/start` before the
  first prompt (`tui/src/app/event_dispatch.rs`,
  `tui/src/app/session_lifecycle.rs`). SessionStart remains queued until the
  first turn, before UserPromptSubmit (`core/src/session/session.rs`,
  `core/src/session/turn.rs`). attn admits the successor when those hooks
  report its conversation. That first prompt makes it working; the previous
  conversation's program status does not carry over.
- `thread/name/set` appends the name to `$CODEX_HOME/session_index.jsonl`,
  and the latest entry for a thread id is its name
  (`rollout/src/session_index.rs`,
  `thread-store/src/local/update_thread_metadata.rs`). Every app-server on one
  `CODEX_HOME` sees a rename made through any of them.

Probed on 0.163.0 with a real model:

- A `--remote` TUI refuses `--add-dir` and exits at once. Extra writable
  directories come from `runtimeWorkspaceRoots` (experimental) on
  `thread/start`, which replaces the default `[cwd]`. With `workspace-write`, a
  conversation started with `[cwd, dir]` writes in `dir`; one started without
  it cannot. `/tmp` stays writable either way. A `thread/resume` without
  `runtimeWorkspaceRoots` restores the ones the conversation saved; this one
  is read in the source at 6ae8b8aa7c, not probed
  (`app-server/src/request_processors/thread_processor.rs`).

Probed on 0.162.1 against the app-server alone:

- `thread/start` and `thread/resume` take `approvalPolicy`,
  `approvalsReviewer` and `sandbox`. Like its `config`, a `thread/resume`
  applies them only when it loads the conversation: it loads one that is not
  loaded, and loads again one that is loaded with no subscriber. While any
  connection is subscribed, the one resuming included, it ignores them and the
  conversation keeps its own. It also ignores them while a turn runs; this one
  is read in the source at rust-v0.160.0, not probed
  (`app-server/src/request_processors/thread_processor.rs`).
- A `thread/resume` without them that loads the conversation restores the
  approval policy and reviewer it last ran with, but a sandbox only from a
  permission profile (the reviewer is read in the source at rust-v0.160.0,
  `app-server/src/request_processors/persisted_resume_settings.rs`). One that
  ran under a `sandbox` mode loads under the server's configured sandbox: a
  conversation started with `never` and `danger-full-access` comes back
  `never` and `read-only` in an untrusted folder.
- Codex never raises its open-file limit (openai/codex#44682): the app-server
  and the commands it runs keep the soft limit it was launched with, which is
  256 for anything a macOS app starts. At 256, 65 of 200 concurrent
  `command/exec` calls failed with `Too many open files (os error 24)`.

Probed on 0.162.1 and 0.163.0 with a mock model, and read in the source at
rust-v0.163.0. Stopping and restarting the shared Codex app-server relies on
these.

- `initialize` answers `userAgent` as `<originator>/<version> (<os>; <arch>)
  ...`. The version is the first token after the first `/`, the number
  `codex --version` prints after `codex-cli `. The originator is the first
  client's `clientInfo.name` for the life of the process; the rest of the
  string changes with each client that initializes. A `--remote` TUI sends its
  own version as `clientInfo.version`, probed on 0.162.1 (`tui/src/lib.rs`).
- The first SIGTERM or SIGINT starts a drain. SIGHUP starts one too but can
  never force it. During the drain connections stay up and new ones are
  accepted, but `turn/start`, `turn/steer`, `thread/start`, `thread/resume`,
  `thread/fork` and `thread/archive`, among others, fail with -32600 `Server is
  draining; retry after reconnecting`. 0.163.0 adds `data.reason:
  "serverShuttingDown"`; 0.162.1 sends no data. `thread/read`, `thread/list`,
  `turn/interrupt` and approval answers still work. Nothing announces the
  drain to clients.
- The drain ends when no turn is running and no request admitted before it is
  still preparing or submitting work, and has no timeout of its own. A turn
  waiting on an approval or a question is still running, and so is a turn's
  title generation. A second SIGTERM or SIGINT forces the exit. With neither
  left, the drain ends at once (`ShutdownState::update` in
  `app-server/src/lib.rs`).
- At the end the server stops listening, removes its socket and drops every
  connection without a WebSocket close frame, so clients see EOF before the
  process exits. It then shuts its conversations down, which can take about
  20 s, and reads no signals while it does (read in the source, not probed:
  `app-server/src/lib.rs`).
- While a server answers on `PATH`, a second one there exits 1 with
  `app-server control socket is already in use`. Once the process is gone, a
  new server can listen on the same `PATH` at once, even after SIGKILL.
- A conversation cut off by a forced exit or SIGKILL can still be resumed on
  the next server.
- Installed with npm, `codex` is node running `bin/codex.js`, which forwards
  SIGTERM to the native binary. One SIGTERM to the process group therefore
  reaches the server twice and forces its exit. A second SIGTERM sent to the
  node process alone is not forwarded.

Probed on 0.163.0 with a `--remote` TUI, and read in the source at
rust-v0.163.0:

- When its server goes away it shows `Reconnecting to server…` and retries
  after 0, 1, 2 and 4 s, then every 8 s, until 120 s after the disconnect. Then
  it stays open offline and never retries (`tui/src/app/reconnect.rs`).
- What the user typed survives the reconnect. Offline, typing works and Enter
  does nothing.
- Against a draining server, a prompt sent into a running turn (`turn/steer`)
  makes the TUI exit 1. One sent to an idle conversation shows `Failed to start
  turn` and is dropped. A TUI started during a drain exits 1. A reconnect whose
  `thread/resume` is refused shows the conversation as unavailable, with input
  paused, and does not retry.
- A pending approval does not survive a server restart.
- The TUI never refuses a server version. When it is a stable release newer
  than its server, it adds a warning to its footer and its F2 viewer, and
  nowhere else; `tui.show_server_version_notice` turns it off. A TUI older than
  its server drops notifications it does not know and answers unknown server
  requests with -32601, which the server takes as a denied approval; the turn
  carries on.
- One still at the folder trust prompt when its server goes away never
  reconnects, and choosing to trust then fails with `config/batchWrite ...
  remote app-server worker channel is closed`, probed on 0.162.1.

Read in the source at openai/codex rust-v0.160.0:

- A `--remote` TUI sends its `--model`, `model_reasoning_effort`,
  `approval_policy`, `approvals_reviewer` and a `sandbox` mode, never a
  permission profile, on `thread/start`, and no approval or sandbox settings
  on `thread/resume` (`tui/src/app_server_session.rs`). It takes the approval
  policy and reviewer the server answers with and sends them with each turn;
  it sends a sandbox with a turn only after the user picks one in it
  (`tui/src/chatwidget/session_flow.rs`, `tui/src/app/thread_routing.rs`).
- A `--remote` TUI refuses to resume a conversation with `Permission
  overrides are not supported when resuming a remote task.` when it was given
  `--dangerously-bypass-approvals-and-sandbox` or `-c` for an approval or
  sandbox setting such as `approval_policy` or `approvals_reviewer`, or once
  the user picked permissions in it with `/permissions`. As `codex resume <id>`
  it exits. On `/resume`, or `/agents` to a conversation that is not loaded,
  it shows the error and stays on its conversation (`tui/src/app/startup.rs`,
  `tui/src/app/config_persistence.rs`, `tui/src/resume_permissions.rs`).
- Before its first `thread/start` or `thread/resume`, a `--remote` TUI given
  `-C` asks the server whether that folder is trusted with `config/read`, so
  `-c projects."<folder>".trust_level` given to the TUI does not count. It
  asks the trust question when that reply has no trust level under that exact
  folder, and not for one saved untrusted or inside a folder saved untrusted.
  Answering it writes
  `projects."<folder>".trust_level = "trusted"` with `config/batchWrite`
  (`tui/src/onboarding/directory_trust.rs`, `tui/src/config_update.rs`).
- `thread/start` in a folder with no saved trust level saves it as trusted when
  the conversation's sandbox can write there
  (`app-server/src/request_processors/thread_processor.rs`).

Read in the source at openai/codex 1ffa95e:

- `active` carries `waitingOnUserInput` while a turn waits on the user's
  answer (`app-server-protocol/src/protocol/v2/thread.rs`).
- Stop hooks run inside the turn, and the turn waits for them, so a
  conversation goes `idle` only after its Stop hook has exited
  (`core/src/session/turn.rs`). A conversation that goes idle with no Stop
  ended its turn another way: interrupted, or cut off.
- `thread/resume` of a conversation that is not running rewrites its
  in-progress turns as interrupted
  (`app-server/src/request_processors/thread_lifecycle.rs`).
- The TUI reports its state with OSC 7501 `state=idle|working|blocked` and
  `app=codex` (`tui/src/terminal_program_status.rs`), and ignores
  `thread/status/changed` for its own state. While it reconnects to a lost
  app-server it keeps reporting `working`; once it resumes, it replays the
  conversation's turns, so a turn the lost server was running ends as
  interrupted (`tui/src/app/reconnect.rs`, `tui/src/chatwidget/replay.rs`).

Read in the source at openai/codex rust-v0.163.0 and c3bb20602d:

- The TUI reports `blocked` only while a question, an approval overlay or an
  MCP elicitation waits on the user. A guardian review shows as a footer
  status, so the TUI reports `working` while the guardian reviews
  (`tui/src/chatwidget/status_surfaces.rs`, `tui/src/bottom_pane/mod.rs`).
- rust-v0.163.0 sends `blocked` without a kind. Later builds (#54114) add
  `kind=permission` for approvals and `kind=question` for questions and
  elicitations.

Read in the source at openai/codex rust-v0.162.1, and the switches probed on
it with a model on a closed port. A shared Codex tile follows the conversation
its Codex shows on these:

- A `--remote` TUI starts, forks or resumes a conversation on its own
  connection only to show it, or an `ephemeral` one for a helper such as
  automatic titles and recaps. Its task tools that reach other conversations,
  `create_thread`, `fork_thread` and `send_message_to_thread`, need an MCP
  server it starts only for a local daemon or an embedded app-server, so it
  refuses them (`tui/src/app/startup.rs`, `tui/src/app/app_server_events.rs`,
  `tui/src/dynamic_tools.rs`, `tui/src/app/thread_title.rs`).
- `/agents` reads the root conversation picked with `thread/read` and resumes
  it with `thread/resume`, even one it showed before. The exception is a
  conversation it started itself and has not prompted yet: it stays subscribed
  to it when it leaves and goes back to it without a request, so the first
  sign is the `turn/start` of the next prompt there
  (`tui/src/app/agents_overview.rs`, `tui/src/app/thread_routing.rs`).
- Picking a subagent that is not attached resumes it
  (`tui/src/app/session_lifecycle.rs`), and on reconnect it resumes the
  conversation it displays, a subagent's when it shows one, never one another
  client loaded (`tui/src/app/reconnect.rs`).
- The `thread` of a `thread/start`, `thread/fork` or `thread/resume` reply
  carries `path` even before the conversation is on disk, and
  `parentThreadId` only for a subagent
  (`app-server-protocol/src/protocol/v2/thread_data.rs`,
  `app-server/src/request_processors/thread_processor.rs`).

### Codex updates

Read in the source at openai/codex rust-v0.163.0 and probed on 0.162.1 and
0.163.0, except where noted.

- `codex --version` prints `codex-cli <version>` in 10–30 ms.
- Only TUIs write `<CODEX_HOME>/version.json` (`latest_version`,
  `last_checked_at`, `dismissed_version`), and the write is not atomic. A TUI
  refreshes it in the background at startup when it is missing or over 20 h
  old, so that run still shows the old value. The source follows the install
  method: the cask API for brew, GitHub releases checked against the npm
  `latest` tag for npm, GitHub releases otherwise.
- `codex doctor --json` prints one JSON object after 10–14 s, of which
  2–3.5 s is CPU, probed on 0.162.1. When any check fails it still prints the
  whole report, then exits 1, so read stdout whatever the exit status.
  `codexVersion` is the binary's own version. `checks` is keyed by check id, and
  `checks["updates.status"].details["latest version"]` holds the newest
  release, checked live against the same source as a TUI's refresh, except
  that for npm installs it skips the check against the npm `latest` tag.
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
  `<CODEX_HOME>/packages/app-server-daemon/`. It runs the installer without checking
  versions first, prompts for nothing itself, and refuses `--remote`.
- Whether an old binary stays launchable after an update depends on the
  install:
  - Standalone: each version keeps its own
    `<CODEX_HOME>/packages/standalone/releases/<version>-<target>/`, nothing
    prunes them, and `current` and `~/.local/bin/codex` are swapped
    atomically.
  - npm: the native binary's path has no version in it. The install moves the
    old package aside, extracts the new one at the same path, then deletes the
    old one.
  - brew cask, read in its source only: `brew upgrade` renames
    `Caskroom/codex/<old>` aside before installing and deletes it once the new
    version is in.

## Pi

Read in the installed 0.85.1 source and probed through its session SDK with
synthetic messages:

- `SessionManager.createBranchedSession` and `forkFrom` create a new session
  file while preserving the inherited message entries' `id` and usage
  (`dist/core/session-manager.js`). An entry id can therefore occur in more
  than one session file.

Read in the installed 1.3.0 source and probed through its session SDK, with
two `SessionManager`s on one file that share nothing but the file:

- Nothing stops a second process from opening a session file another process
  has open. `SessionManager` holds no lock; once the file exists it appends
  each entry with `appendFileSync` (`dist/core/session-manager.js`).
- Each writer continues its own branch from its own entries in memory, so the
  file becomes a tree, and neither sees the other's entries. Opening the file
  again loads the branch ending at the last entry written.

Pi falls short of attn's session model too. attn does not key Pi sessions by
conversation (Pi has no built-in driver), so each Pi terminal is its own
session, and two Pi terminals on one conversation are two sessions. The file
forks, and no tile warns.

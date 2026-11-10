# Codex app-server and `--remote` behavior

How the Codex app-server and `--remote` behave where attn meets them. [Codex
harness behavior](codex.md) covers plain Codex. The rules for adding a claim are
in [Harness behavior](../harnesses.md).

Probed on 0.165.0 with a local mock model, an isolated `CODEX_HOME` and a
non-production attn daemon:

- A server-side `spawn_agent` sends `thread/status/changed` for the child to
  an initialized control connection, but no `thread/started` for that child.
  The connection also receives the child's turn, item and hook notifications.
  `thread/read` returns the child's `parentThreadId` while the child's turn
  runs.
- Restarting attn's daemon leaves the same app-server and child turn running.
  On the new control connection, `thread/loaded/list` includes the child and
  `thread/read` still returns its parent. Reconnecting sends no
  `thread/started` replay.
- With `workspace-write`, `on-request` approvals and code mode enabled,
  `tools.exec_command({cmd: "attn seed show ..."})` fails to connect to attn's
  Unix socket with `operation not permitted`. Adding
  `prefix_rule(pattern = ["attn", "seed", "show"], decision = "allow")` to
  `<CODEX_HOME>/rules/default.rules` lets the same command connect and read
  the seed. The tool call uses default sandbox permissions in both runs.

Probed on 0.160.0 with a mock model. Shared Codex relies on these.

- `codex app-server --listen unix://PATH` speaks WebSocket (`GET /rpc`) on a
  socket. `PATH` is a symlink to that socket, under `/tmp/codex-daemon-<uid>/`.
  When `PATH` is long, a client must resolve the symlink before connecting.
- From 0.158.0 through at least 0.167.0-alpha.4, the socket's name is the
  SHA-256 of `PATH` with its parent folder canonicalized. On Linux,
  `systemd-tmpfiles` removes an aged symlink but keeps a listening socket, so
  the server stays reachable at that name after `PATH` is gone. Probed with the
  link and its folder removed on 0.164.0 (Linux) and 0.166.0 (macOS).
- Hooks given to the server with `-c` are session-flag hooks. A
  `hooks.state."/<session-flags>/config.toml:<event>:0:0".trusted_hash` given
  the same way trusts them. They run in the server's environment, with the
  working directory set to the conversation's. Stdin carries `session_id` (the
  conversation), `transcript_path` and `cwd`.
- `thread/start` writes nothing to disk. `thread/inject_items` with a
  developer message writes the conversation at once. The first turn of that
  conversation still fires SessionStart `startup`, then UserPromptSubmit.
- `thread/resume` of a conversation not on disk fails with `no rollout found`,
  even while the server holds the conversation in memory. A `--remote` TUI that
  loses its socket reconnects and resumes the conversation it shows, so a
  conversation never written to disk cannot come back.
- On 0.166.0 with a read-only `sessions/`, `thread/inject_items` fails with
  `Permission denied`, but a `turn/start` after it still runs and completes. It
  writes no rollout and only warns at turn completion. `thread/resume` then
  fails, even on a fresh server after permissions are repaired. attn therefore
  refuses a new conversation whose write fails.
- A `--remote` TUI on 0.166.0 waited the full 60s attn holds the `thread/start`
  reply, without a limit of its own firing.
- `thread/resume` ignores `developerInstructions`: a conversation keeps the
  instructions it started with. The `config` that `thread/resume` takes (such
  as `model_auto_compact_token_limit`) applies when the resume loads the
  conversation.
- Every connection receives `thread/status/changed` (`idle`, `notLoaded`, or
  `active` with `waitingOnApproval`), `thread/name/updated` and `thread/closed`
  for every conversation. Only subscribers receive turns, items and approvals.
  A conversation with no subscriber unloads about 60 s after it goes idle.
- `turn/start` runs a turn with no terminal attached. `turn/steer` adds input
  to the running turn and needs the turn's id as `expectedTurnId`.
- A connection that resumes a conversation gets the conversation's pending
  approval again, with the same request id. Any subscriber's answer settles the
  approval for all subscribers.
- After the first prompt the TUI names the conversation with an ephemeral
  `thread_title` conversation, then `thread/name/set` on the real conversation.
  `thread/name/set` from any connection renames the conversation in every TUI.
- A conversation's shell tools get `CODEX_THREAD_ID` (the conversation id) and
  the server's `-c shell_environment_policy.set.*` values, in every
  conversation, including ones another client resumed.
- One process writes a conversation at a time: while an app-server holds the
  conversation loaded, another app-server or a plain `codex resume` fails with
  `already has an active writer`. Either can take the conversation once the
  holder unloads it. On 0.162.1, while a `--remote` TUI holds the conversation,
  a plain TUI shows the lock screen that
  [Codex harness behavior](codex.md) describes, both as
  `codex resume <id>` and with `/resume <id>`, and writes nothing.
- `thread/archive` moves the rollout to `archived_sessions/` (flat, no dated
  directories) and refuses later resumes. `thread/unarchive` moves the rollout
  back.

Read in the source at openai/codex rust-v0.164.0:

- `thread/archive` first shuts down a loaded thread, which aborts its running
  turn, a compact included, and then moves the rollout
  (`app-server/src/request_processors/thread_processor.rs`,
  `core/src/session/handlers.rs`).

Read in the source at openai/codex rust-v0.162.1:

- Codex appends to the existing rollout when it resumes a conversation. For a
  fork, Codex creates a new thread id and writes the fork's own `session_meta`
  before inherited history (`core/src/session/session.rs`,
  `thread-store/src/local/create_thread.rs`, `rollout/src/recorder.rs`).
  `thread/revert` can create a new rollout file while retaining the thread id
  (`rollout/src/recorder.rs`).
- In a `--remote` TUI, `/new` and `/clear` send `thread/start` before the
  first prompt (`tui/src/app/event_dispatch.rs`,
  `tui/src/app/session_lifecycle.rs`). SessionStart remains queued until the
  first turn, before UserPromptSubmit (`core/src/session/session.rs`,
  `core/src/session/turn.rs`). attn admits the successor when those hooks
  report the successor's conversation. That first prompt makes the successor
  working. The previous conversation's program status does not carry over.
- `thread/name/set` appends the name to `$CODEX_HOME/session_index.jsonl`,
  and the latest entry for a thread id is the thread's name
  (`rollout/src/session_index.rs`,
  `thread-store/src/local/update_thread_metadata.rs`). Every app-server on one
  `CODEX_HOME` sees a rename made through any of them.

Read in the source at openai/codex rust-v0.167.0:

- `thread/compact/start` runs the compact as a turn. `turn/steer` refuses it
  with `cannot steer a compact turn` and the `activeTurnNotSteerable` error
  (`core/src/session/turn_input.rs`,
  `app-server/src/request_processors/turn_processor.rs`). A TUI whose user
  submits a prompt during that turn keeps the prompt in its queue
  (`tui/src/chatwidget/input_restore.rs`, `turn_runtime.rs`), so the
  conversation takes it after the compact, as a new turn.

Probed on 0.163.0 with a real model:

- A `--remote` TUI refuses `--add-dir` and exits at once. Extra writable
  directories come from `runtimeWorkspaceRoots` (experimental) on
  `thread/start`, which replaces the default `[cwd]`. With `workspace-write`, a
  conversation started with `[cwd, dir]` writes in `dir`. A conversation
  started without `dir` cannot. `/tmp` stays writable either way. A
  `thread/resume` without `runtimeWorkspaceRoots` restores the roots the
  conversation saved. This behavior is read in the source at 6ae8b8aa7c and not
  probed (`app-server/src/request_processors/thread_processor.rs`).

Probed on 0.162.1 against the app-server alone:

- `thread/start` and `thread/resume` take `approvalPolicy`,
  `approvalsReviewer` and `sandbox`. `thread/resume` also takes `config`.
  `thread/resume` applies these values and the `config` only when it loads the
  conversation. It loads a conversation that is not loaded, and it loads again a
  conversation that is loaded and has no subscriber. While any connection is
  subscribed, the resuming connection included, `thread/resume` ignores these
  values and the conversation keeps its own. `thread/resume` also ignores them
  while a turn runs. The case of a running turn is read in the source at
  rust-v0.160.0 and not probed
  (`app-server/src/request_processors/thread_processor.rs`).
- When `thread/resume` loads a conversation and receives none of these values,
  it restores the approval policy and reviewer that the conversation last ran
  with, but restores a sandbox only from a permission profile (the reviewer is
  read in the source at rust-v0.160.0,
  `app-server/src/request_processors/persisted_resume_settings.rs`). A
  conversation that ran under a `sandbox` mode loads under the server's
  configured sandbox. For example, a conversation started with `never` and
  `danger-full-access` comes back `never` and `read-only` in an untrusted
  folder.
- Codex never raises its open-file limit (openai/codex#44682): the app-server
  and the commands the app-server runs keep the soft limit that the app-server
  was launched with, which is 256 for anything a macOS app starts. At 256, 65
  of 200 concurrent `command/exec` calls failed with
  `Too many open files (os error 24)`.

Probed on 0.162.1 and 0.163.0 with a mock model, and read in the source at
rust-v0.163.0. Stopping and restarting the shared Codex app-server relies on
these.

- `initialize` answers `userAgent` as `<originator>/<version> (<os>; <arch>)
  ...`. The version is the first token after the first `/`, the number
  `codex --version` prints after `codex-cli `. The originator is the first
  client's `clientInfo.name` for the life of the process. The rest of the
  string changes with each client that initializes. A `--remote` TUI sends its
  own version as `clientInfo.version`, probed on 0.162.1 (`tui/src/lib.rs`).
- The first SIGTERM or SIGINT starts a drain. SIGHUP starts a drain too but can
  never force it. During the drain connections stay up and new connections are
  accepted, but `turn/start`, `turn/steer`, `thread/start`, `thread/resume`,
  `thread/fork` and `thread/archive`, among others, fail with -32600 `Server is
  draining; retry after reconnecting`. 0.163.0 adds `data.reason:
  "serverShuttingDown"`. 0.162.1 sends no data. `thread/read`, `thread/list`,
  `turn/interrupt` and approval answers still work. Nothing announces the
  drain to clients.
- The drain ends when no turn is running and no request admitted before the
  drain is still preparing or submitting work. The drain has no timeout of its
  own. A turn waiting on an approval or a question is still running, and so is
  a turn's title generation. A second SIGTERM or SIGINT forces the exit. When no
  turn is running and no admitted request is pending at the first signal, the
  drain ends at once (`ShutdownState::update` in `app-server/src/lib.rs`).
- At the end the server stops listening, removes its socket and drops every
  connection without a WebSocket close frame, so clients see EOF before the
  process exits. The server then drains its background tasks and shuts all its
  conversations down at once, giving each up to 10 s. The server reads no
  signals during that shutdown (`app-server/src/lib.rs`, `shutdown_all_threads_bounded` in
  `core/src/thread_manager.rs`, called from
  `request_processors/thread_processor.rs`).
- While a server answers on `PATH`, a second server there exits 1 with
  `app-server control socket is already in use`. Once the process is gone, a
  new server can listen on the same `PATH` at once, even after SIGKILL.
- A conversation cut off by a forced exit or SIGKILL can still be resumed on
  the next server.
- Installed with npm, `codex` is node running `bin/codex.js`, which forwards
  SIGTERM to the native binary. One SIGTERM to the process group therefore
  reaches the server twice and forces its exit. A second SIGTERM sent to the
  node process alone is not forwarded.

Probed on 0.167.0 on macOS, with the server as a shared PTY host's terminal
and a model that never answers:

- When the host dies, the server, the session leader of its PTY, gets the
  hangup's SIGHUP and starts a drain. With no turn running it exits at once.
  With a turn running it drains with no end, reparented to launchd: servers
  a demo run left behind still answered `Server is draining` hours later.
  attn therefore kills a terminal's process group when its PTY host or worker
  dies.

Probed on 0.162.1 and 0.164.0 on a copy of a real `CODEX_HOME`, with its
plugins and MCP servers, and with a 0.162.1 `--remote` TUI:

- A second app-server on another socket with the same `CODEX_HOME` starts and
  answers `initialize` in 125–325 ms while the first runs. Stopping the second
  server leaves the first serving.
- Shutting conversations down is what makes a stop slow. With one
  conversation a TUI started, the drain ended at once (socket gone after
  0.16 s) and the process exited 9–16.5 s after SIGTERM, while its MCP servers
  stopped. With conversations only resumed and no MCP server running, the
  process exited 22–533 ms after SIGTERM.
- A new `--remote` TUI starts its conversation by itself a few seconds after
  launch, before any prompt. A TUI that asks whether to trust its folder starts
  the conversation once the user answers. Before the conversation starts,
  losing the connection makes the TUI exit 1, except at the trust question,
  where the TUI stays open and never reconnects, as described below.
- After a `--remote` TUI reconnects, the TUI shows busy in its title for a few
  seconds: about 2 s when its server restarted, 7 s and more after attn's
  daemon restarted.

Probed on 0.163.0 with a `--remote` TUI, and read in the source at
rust-v0.163.0:

- When its server stops, the TUI shows `Reconnecting to server…` and retries
  after 0, 1, 2 and 4 s, then every 8 s, until 120 s after the disconnect. Then
  the TUI stays open offline and never retries (`tui/src/app/reconnect.rs`).
- What the user typed survives the reconnect. Offline, typing works and Enter
  does nothing.
- Against a draining server, a prompt sent into a running turn (`turn/steer`)
  makes the TUI exit 1. A prompt sent to an idle conversation shows `Failed to
  start turn` and is dropped. A TUI started during a drain exits 1. When the
  `thread/resume` of a reconnect is refused, the TUI shows the conversation as
  unavailable, with input paused, and does not retry.
- A pending approval does not survive a server restart.
- The TUI never refuses a server version. When the TUI is a stable release
  newer than its server, the TUI adds a warning to its footer and its F2
  viewer, and nowhere else. `tui.show_server_version_notice` turns the warning
  off. A TUI older than its server drops notifications of types it does not recognize and
  answers unknown server requests with -32601, which the server takes as a
  denied approval. The turn carries on.
- A TUI still at the folder trust prompt when its server goes away never
  reconnects, and choosing to trust then fails with `config/batchWrite ...
  remote app-server worker channel is closed`, probed on 0.162.1.

Read in the source at openai/codex rust-v0.160.0:

- A `--remote` TUI sends its `--model`, `model_reasoning_effort`,
  `approval_policy`, `approvals_reviewer` and a `sandbox` mode, never a
  permission profile, on `thread/start`, and no approval or sandbox settings
  on `thread/resume` (`tui/src/app_server_session.rs`). The TUI takes the
  approval policy and reviewer that the server answers with and sends them with
  each turn. The TUI sends a sandbox with a turn only after the user picks a
  sandbox in the TUI (`tui/src/chatwidget/session_flow.rs`,
  `tui/src/app/thread_routing.rs`).
- A `--remote` TUI refuses to resume a conversation with `Permission
  overrides are not supported when resuming a remote task.` when it was given
  `--dangerously-bypass-approvals-and-sandbox` or `-c` for an approval or
  sandbox setting such as `approval_policy` or `approvals_reviewer`, or once
  the user picked permissions in the TUI with `/permissions`. As
  `codex resume <id>`, the TUI exits. On `/resume`, or `/agents` to a
  conversation that is not loaded, the TUI shows the error and stays on its
  conversation (`tui/src/app/startup.rs`, `tui/src/app/config_persistence.rs`,
  `tui/src/resume_permissions.rs`).

Read in the source at openai/codex rust-v0.164.0:

- The app-server and a plain TUI take a folder's trust from that folder, else
  from the root of its repository (the main repository for a worktree), and
  from no other parent folder (`config/src/config_toml.rs`,
  `git-utils/src/trust.rs`).
- Before its first `thread/start` or `thread/resume`, a `--remote` TUI given
  `-C` asks the server whether that folder is trusted with `config/read`, so
  `-c projects."<folder>".trust_level` given to the TUI does not count. On any
  socket but Codex's own daemon socket the TUI looks only at the exact folder
  in the reply, so the TUI asks in every new worktree of a trusted repository.
  The TUI doesn't ask for a folder saved untrusted, or when the reply has an
  enabled project layer: a `.codex/` folder, even one without `config.toml`,
  that the server trusts through the repository. Inside a folder saved
  untrusted with neither, the TUI exits at launch with `remote project
  directory is inside an explicitly untrusted project`. Answering the question
  makes the TUI write `projects."<folder>".trust_level = "trusted"` with
  `config/batchWrite` (`tui/src/config_update.rs`,
  `tui/src/onboarding/directory_trust.rs`, `tui/src/lib.rs`,
  `config/src/loader/mod.rs`).
- Declining the question saves nothing, so the next launch there asks again
  (`tui/src/onboarding/trust_directory.rs`).
- `thread/start` in a folder with no saved trust level saves trust when the
  conversation's sandbox can write there: on the root of its repository (the
  main repository for a worktree), or on the folder outside git
  (`app-server/src/request_processors/thread_processor.rs`).

Probed on 0.166.0, a plain TUI in a PTY with its own `CODEX_HOME`, and read in
the source at rust-v0.166.0:

- The folder trust question reads "Trust this folder?", with "1. Trust and
  continue" selected.
- A plain TUI asks its app-server about trust too, with or without
  `--no-daemon`, so `-c projects."<folder>".trust_level="trusted"` does not
  stop the question (`tui/src/onboarding/directory_trust.rs`).
- Trust saved on a repository's root covers its worktrees and subfolders: the
  TUI does not ask there.
- In a folder saved untrusted, the TUI asks a different question, "Open
  restricted" or "Quit".
- The TUI sends no OSC 7501 while the question is open. It reports
  `state=idle` once its chat starts, 3.6 s after Enter in one probe, because
  only the chat widget reports status (`tui/src/chatwidget.rs`).

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
  `thread/status/changed` for its own state. While the TUI reconnects to a lost
  app-server it keeps reporting `working`. Once the TUI resumes the
  conversation, it replays the conversation's turns, so a turn the lost server
  was running ends as interrupted (`tui/src/app/reconnect.rs`,
  `tui/src/chatwidget/replay.rs`).

Read in the source at openai/codex rust-v0.164.0 and probed on 0.164.0 with a
frame tap on attn's view proxy. These decide when a shared Codex tile leaves
`working`:

- The TUI counts an MCP startup round as a running task, so it reports
  `working`, with no status row while no turn runs. A round ends only when every
  server enabled in the TUI's config has reported a state other than `starting`
  (`tui/src/chatwidget/mcp_startup.rs`,
  `tui/src/chatwidget/status_surfaces.rs`).
- `mcpServer/startupStatus/updated` goes only to the connections subscribed to
  the conversation, with no replay. A TUI that joins a conversation another
  connection loaded misses what the servers already reported. If a later server
  still reports, that round starts in the TUI and has no way to end, so the TUI
  keeps reporting `working` after its turn completes.
- `thread/resume` can start a round, even for a conversation already loaded.
  `turn/start` can start a round that reports only `ready`.
- The TUI reads the servers it waits for from its config at launch, and `/new`
  reads them again. A server removed from `config.toml` after launch leaves any
  later round without an end. Restarting the app-server under that TUI starts
  such a round, and the TUI reports `working` from then on, through later turns
  too, probed with a real model. Typed prompts still run.
- `thread/backgroundTerminals/list` names the running background terminals of a
  loaded conversation. The conversation is `idle` while one runs after its turn
  ends, and the TUI correctly reports `working` until the terminal exits, then
  `done`.
- A healthy TUI and the server agree within 103 ms when a turn starts and 30 ms
  when it ends.

Probed on 0.167.0 with a stdio MCP server that answers after 12 s and a frame
tap on attn's view proxy, restarting the app-server under a live TUI:

- `starting` for every server reaches the TUI within 100 ms of `thread/resume`,
  and the TUI reports `working` within 100 ms of those frames. The conversation
  stays `idle` throughout. The TUI reports `idle` within 3 ms of the last
  `ready`.
- With a server removed from `config.toml` after launch, the same frames arrive
  and every server reaches `ready`, but the TUI stays on `working`. A frame tap
  therefore tells a server that is starting from a round with no end.
- A server's startup is bound by `startup_timeout_sec`, 30 s by default
  (`codex-mcp/src/rmcp_client.rs`).
- A new conversation of a fresh TUI has no attn conversation while its first
  round runs, so `thread/read` on the session cannot run yet.

Read in the source at openai/codex rust-v0.163.0 and c3bb20602d:

- The TUI reports `blocked` only while a question, an approval overlay or an
  MCP elicitation waits on the user. A guardian review shows as a footer
  status, so the TUI reports `working` while the guardian reviews
  (`tui/src/chatwidget/status_surfaces.rs`, `tui/src/bottom_pane/mod.rs`).
- rust-v0.163.0 sends `blocked` without a kind. Later builds (#54114) add
  `kind=permission` for approvals and `kind=question` for questions and
  elicitations.

Seen in attn's production log on 0.164.0, a shared Codex tile with the guardian
reviewer:

- PermissionRequest fires before the guardian reviews, and no hook reports the
  guardian's answer. A command that the guardian let through and that kept
  running as a background terminal sent no PostToolUse. The next hook came with
  the agent's next tool call, 214 s later, while the TUI kept reporting
  `working`.

Read in the source at openai/codex rust-v0.162.1, with the switches probed on
it using a model on a closed port. These behaviors decide which conversation a
shared Codex tile follows:

- A `--remote` TUI starts, forks or resumes a conversation on its own
  connection only to show the conversation, or starts an `ephemeral`
  conversation for a helper such as automatic titles and recaps. The TUI's task
  tools that reach other conversations, `create_thread`, `fork_thread` and
  `send_message_to_thread`, need an MCP server that the TUI starts only for a
  local daemon or an embedded app-server, so the TUI refuses them
  (`tui/src/app/startup.rs`, `tui/src/app/app_server_events.rs`,
  `tui/src/dynamic_tools.rs`, `tui/src/app/thread_title.rs`).
- `/agents` reads the root conversation picked with `thread/read` and resumes
  the conversation with `thread/resume`, even a conversation the TUI showed
  before. The exception is a conversation the TUI started itself and has not
  prompted yet. When the TUI leaves that conversation, the TUI stays subscribed to the
  conversation and goes back to it without a request. attn therefore first sees
  the `turn/start` of the next prompt on that conversation (`tui/src/app/agents_overview.rs`,
  `tui/src/app/thread_routing.rs`).
- Picking a subagent that is not attached resumes the subagent
  (`tui/src/app/session_lifecycle.rs`). On reconnect, the TUI resumes the
  conversation it displays, a subagent's conversation when it shows one, never
  a conversation another client loaded (`tui/src/app/reconnect.rs`).
- The `thread` of a `thread/start`, `thread/fork` or `thread/resume` reply
  carries `path` even before the conversation is on disk, and
  `parentThreadId` only for a subagent
  (`app-server-protocol/src/protocol/v2/thread_data.rs`,
  `app-server/src/request_processors/thread_processor.rs`).

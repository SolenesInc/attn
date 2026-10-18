# Harness behavior

What each harness's own commands look like to attn: which hooks fire, when,
and which conversation id they carry. Designs that react to a harness lean on
these facts.

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

Removing a placement keeps the terminal addressable, including after daemon
restart. Closing a session ends its terminal associations. A session is shown
in at most one terminal: when another terminal resumes its conversation, that
terminal takes the session over and the previous one stops showing it. Shared
Codex is the exception: several terminals can show one conversation, and so one
session.

Plugin driver payloads retain the `session_id` wire spelling for the terminal.
Close notifications, including those completed after restart, use the original
terminal identity.

## Launch instructions after a fresh conversation

Probed outside attn in tmux on 2026-10-08 (Claude and Codex) and
2026-10-10 (Copilot). Each launch channel held a codeword; the prompt asked
for only that word, before and after clearing.

| Harness | Launch channel | Result |
|---|---|---|
| Claude Code 2.1.296 | `--append-system-prompt` | PELICAN-42 before and after `/clear`; the screen showed a fresh conversation |
| Codex 0.162.0 | `-c developer_instructions=...` | HERON-17 before, after `/clear`, and after `/new` |
| Copilot 1.0.82 | `COPILOT_CUSTOM_INSTRUCTIONS_DIRS`, with `attn.instructions.md` | OTTER-63 before and after `/clear`; the cleared screen showed only the new question and answer |

A crew member's launch instructions can therefore ask them to run
`attn crew prime` when their conversation starts empty. Copilot keeps its
session binding across `/clear`; Claude and Codex move it to the new session.
Pi's launch-instruction delivery remains a separate follow-up.

## Claude Code

### Trusting a crew home

Read in Claude Code 2.1.296's bundled source on 2026-10-10. Accepting
folder trust sets `projects[<path>].hasTrustDialogAccepted` to `true` in
`$CLAUDE_CONFIG_DIR/.claude.json`, or `~/.claude.json` when that variable is
unset. The path is canonical and NFC-normalized; for a Git project Claude
uses its repository root. An existing `.config.json` inside Claude's config
directory takes precedence over `.claude.json`. Config writers coordinate through a directory at
the config file's path plus `.lock`. Claude's `proper-lockfile` defaults
expire a lock after 10 seconds without an update and renew it every 5 seconds.

attn trusts a member's home when it launches the member there, and the
Chief's working directory (the Notebook root). An already-trusted Claude
folder needs no config lock or write.
New trust writes preserve the config's mode and symlink target, sync the file,
and replace it atomically. Lock contention follows Claude's retry delays
(200, 400, 800, 1600, 3200, 4000 ms), bounded by its 1.5-second startup-write
budget.
An unrelated working directory still needs the user's trust decision.

Probed Claude Code 2.1.302 on 2026-10-18 in the copied Notebook used by
the packaged Chief scenario: its real interactive CLI opened at the prompt
without a folder-trust dialog, after attn had trusted that working directory.

### Conversation commands

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

## Codex

### Crew-home trust on resume

Probed Codex 0.162.0 outside attn on 2026-10-10. A private config copy had
its saved project-trust entries removed, and the non-repository working
directory held a `CHARTER.md`. Both `codex resume <id>` and a fresh `codex`
launch opened without a folder-trust prompt, with either the auto reviewer
or user reviewer. The resumed conversation retained its earlier answer.

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
  one picked, resuming it first when it is not loaded
  (`tui/src/app/agents_overview.rs`). It does not switch subagents.
- `/multi-agents` switches between this session's subagents. Subagent threads
  fire no SessionStart or SessionEnd; a spawned one fires SubagentStart
  (`core/src/hook_runtime.rs`).
- Hooks run in the process that runs the turn: the terminal's own Codex
  process for a PTY launch, so `ATTN_TERMINAL_ID` names the terminal (older launches use
  `ATTN_SESSION_ID` with the same meaning); the
  app-server for a `--remote` TUI, so a hook there cannot name the terminal.

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
  an active writer`. Either can take it once the holder unloads it.
- `thread/archive` moves the rollout to `archived_sessions/` (flat, no dated
  directories) and refuses later resumes; `thread/unarchive` moves it back.

Read in the source at openai/codex rust-v0.162.1:

- In a `--remote` TUI, `/new` and `/clear` send `thread/start` before the
  first prompt (`tui/src/app/event_dispatch.rs`,
  `tui/src/app/session_lifecycle.rs`). SessionStart remains queued until the
  first turn, before UserPromptSubmit (`core/src/session/session.rs`,
  `core/src/session/turn.rs`). attn admits the successor when those hooks
  report its conversation. That first prompt makes it working; the previous
  conversation's program status does not carry over.

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

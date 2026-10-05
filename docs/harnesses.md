# Harness behavior

What each harness's own commands look like to attn: which hooks fire, when,
and which conversation id they carry. Designs that react to a harness lean on
these facts.

Every entry was checked against the harness's source or a probe, and names
the version it was checked on. Do not add or rely on a claim from memory:
read the source or probe the binary first, then record it here.

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
  one picked, resuming it first when it is not loaded
  (`tui/src/app/agents_overview.rs`). It does not switch subagents.
- `/multi-agents` switches between this session's subagents. Subagent threads
  fire no SessionStart or SessionEnd; a spawned one fires SubagentStart
  (`core/src/hook_runtime.rs`).
- Hooks run in the process that runs the turn: the terminal's own Codex
  process for a PTY launch, so `ATTN_SESSION_ID` names the terminal; the
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
- `thread/archive` moves the rollout to `archived_sessions/` (flat, no dated
  directories) and refuses later resumes; `thread/unarchive` moves it back.

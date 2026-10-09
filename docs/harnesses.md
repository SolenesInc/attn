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
Native harness conversation ids remain separate text values.

Removing a placement keeps the terminal addressable, including after daemon
restart. Closing a session ends its terminal associations. When several
terminals show one session, ending one leaves the others running.

Plugin driver payloads retain the `session_id` wire spelling for the terminal.
Close notifications, including those completed after restart, use the original
terminal identity.

## Claude Code

Probed on 2.1.288 with a mock API.

Model discovery and effort were probed on 2.1.295 with the real harness on
2026-10-09. `haiku` reports Haiku 5.5 with medium effort supported. A headless
`--model haiku --effort medium` call, with tools, hooks, MCP servers and session
persistence disabled, completed successfully. The full Haiku 4.5 ID
`claude-haiku-4-5-20251001` reports unknown effort metadata; an isolated call
with medium also succeeds. These probes check acceptance of the effort flag; it does not measure how much reasoning the model performs.

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

Model discovery was probed on 0.162.0 on 2026-10-09. Both reported Luna models,
`gpt-6-luna` and `gpt-5.6-luna`, support `xhigh`. Their effort levels are low,
medium, high, xhigh and max; the reported Sol, Astra and Terra models also
support ultra.

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

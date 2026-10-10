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
restart. Closing a session ends its terminal associations. A session is shown
in at most one terminal: when another terminal resumes its conversation, that
terminal takes the session over and the previous one stops showing it.

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
uses its repository root. Config writers coordinate through a directory at
the config file's path plus `.lock`.

attn trusts the member home it creates, including a chief's home. Crew
launches into that home also receive the harness's directory-trust setting.
An unrelated working directory still needs the user's trust decision.

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

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

With `crew_v2_enabled`, attn sets `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` for
a crew member's Claude and sets both `features.memories=false` and
`memories.generate_memories=false` for their Codex. Existing harness memory
files stay in place. Changing the setting takes effect at the member's next
launch.


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

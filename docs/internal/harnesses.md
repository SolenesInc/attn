# Harness behavior

How each harness behaves where attn meets it: which hooks fire and when, which
conversation id the hooks carry, what the harness's app-server and TUI do, and
how the harness updates itself. Designs that react to a harness depend on these facts.

Every entry was checked against the harness's source or a probe, and names
the version it was checked on. Do not add or rely on a claim from memory:
read the source or probe the binary first, then record it here.

## attn identities at the harness boundary

`ATTN_TERMINAL_ID` identifies the launched terminal. Older launches carry the
same identity under `ATTN_SESSION_ID`. A terminal can change conversations with
`/clear` or `/resume`. CLI commands address the conversation shown when the
command starts. Hooks apply to the conversation shown when the report arrives.
Under shared Codex, hooks and commands run in the app-server and name their
conversation instead: a hook by the `session_id` on its stdin, a command by
`CODEX_THREAD_ID`. Nisse runs commands in its server, and names the
conversation people started by `NISSE_ROOT_CONVERSATION`. Native harness
conversation ids remain separate text values.

A session is attn's record of one conversation in one profile. A terminal
shows one session at a time, and a session can show in any number of
terminals. Removing a placement keeps the terminal addressable, including
after daemon restart. Closing a session ends its terminal associations. When a
terminal resumes the conversation of a session whose agent no longer runs, the
session moves into that terminal and the session's old tile goes away.

Showing one session in many terminals needs the session to run over a link
whose server every terminal of the conversation joins, like shared Codex's
app-server. A terminal on a link that resumes the conversation of a live
session shows that session.

An integration without a link starts one process per terminal. Claude Code
lets a second process open a conversation that another process still runs.
The second terminal keeps its own session on the conversation, so two open
sessions hold one conversation. The tiles of every live session on that
conversation warn until one session remains. Plain Codex refuses the second
process.

Plugin driver payloads retain the `session_id` wire spelling for the terminal.
Close notifications, including those completed after restart, use the original
terminal identity.

## The running version in the transcript

Each harness writes the version of the running process into its transcript, so
the version stays right after the binary on disk updates. Probed on the user's
own transcripts on 2026-10-24:

- Claude Code (2.1.302, 2.1.310): every line has a top-level `version`. The
  newest `version` is at most 38.8 KB from the end of the file, across 300
  transcripts with lines up to 1.8 MB.
- Codex (0.162.1): the first line is `session_meta`, with
  `payload.cli_version`. That line is about 23 KB.

## Claude Code

Probed on 2.1.288 with a mock API.

| Action | Hooks, in order | Conversation id |
|---|---|---|
| Launch with `--session-id <uuid>` | SessionStart `startup` | that id. No transcript until the first prompt |
| `/clear` | SessionEnd `clear`, SessionStart `clear` | new |
| `/resume <id>` or the picker | SessionEnd `resume`, SessionStart `resume` | the target. The same id twice is a no-op |
| `/branch` | SessionEnd `resume`, SessionStart `fork` | new |
| `/compact` | PreCompact, SessionStart `compact`, PostCompact | unchanged |
| `claude -r <id>`, `--continue` | SessionStart `resume` | unchanged. `--fork-session` gives `fork` and a new id |
| `/rewind` | none | unchanged |

- SessionStart blocks Claude until the hook exits. SessionEnd is cut off
  after about 1.5 s and may never arrive.
- Claude also compacts by itself while it sits at its prompt. Probed on 2.1.318
  with Opus 5.5 and a subscription login, without attn, in six conversations on
  the 1-hour prompt cache. Four of them ran behind a recording proxy, set with
  `HTTPS_PROXY` and `NODE_EXTRA_CA_CERTS`.
  - Claude checks 54 minutes after the last main-thread request was sent, with
    6 minutes of cache left, within 0.1 s in all six. Stop and an unsent draft
    typed later do not move the check.
  - The check does nothing when auto-compact or `idleCompaction` is off, the
    prefix changed, the cache is not 1-hour, the cache lapsed or the check runs
    more than 60 s late, context is under
    `max(100000, CLAUDE_CODE_IDLE_COMPACT_MIN_TOKENS ?? 200000)` tokens, a newer
    request exists, or the rate limit status is not `allowed`. The list comes
    from the bundle. A skip has no hook and no screen change. Three were seen:
    168k tokens skipped under the default threshold, 168k passed that gate with
    the threshold at 100000, and 245k skipped while responses carried
    `anthropic-ratelimit-unified-status: allowed_warning` at 76% of the 7-day
    limit. An account past that warning gets no idle compaction. The bundle also
    has a variant that precomputes the summary, which was not seen.
  - A compact's hooks are PreCompact `auto`, SessionStart `compact` and
    PostCompact `auto`, over about 6.5 s, all carrying the last turn's
    `prompt_id`. No UserPromptSubmit, Stop or Notification comes with them.
  - A normal turn opens with `thread: {type: "create"}`. Its later requests use
    `{type: "continue", previous_message_id}` and carry only the new messages,
    with no `tools`. A compact request has no `thread` and sends
    `x-claude-code-request-class: compaction` with the whole conversation and
    its summary prompt. Its usage at 245k was about 40k read from the cache and
    207k uncached. The next turn opens a new thread with about 41k of context.
    The request bodies were recorded on a manual `/compact`, because the
    recorded idle checks all skipped. The usage is from idle compacts.
  - The screen shows `Compacting conversation…` above the composer. An unsent
    draft stays in the composer, and Enter then submits it as a normal turn on
    top of the summary. That was seen on the default renderer only. After 75
    minutes the footer shows `new task? /clear to save <n>k tokens`.
- Plan mode's "clear context and proceed" is SessionEnd `clear`, SessionStart
  `clear`, then a turn and Stop on the new id with no UserPromptSubmit.
- The transcript is `<config>/projects/<cwd-slug>/<id>.jsonl`. attn reads the
  conversation id from that name.
- Launch instructions from `--append-system-prompt` stay in force after
  `/clear`: the new conversation still follows them (probed on 2.1.314 with
  the real model).
- `--settings <JSON>` accepts inline hook settings (probed on 2.1.296 on
  macOS and Linux). SessionStart, UserPromptSubmit and Stop use the
  `--session-id` conversation id.
- When Stop fires, the transcript already holds the turn's final assistant
  message with its usage. Only `stop_hook_summary`, `last-prompt` and
  `cost-state` lines come after it (probed on 2.1.316 in `claude -p`, with a
  Stop hook that copied the transcript; the interactive TUI was not probed).

Read in the installed 2.1.310 source and checked with unpaid CLI probes:

- A fresh `CLAUDE_CONFIG_DIR` uses a separate configuration and projects
  directory. On macOS, `auth status` reported `loggedIn: false` there while
  the default configuration was authenticated. An isolated run needs its own
  authentication setup. A fresh interactive launch opens the theme picker.
  With `<config>/.claude.json` prefilled with `hasCompletedOnboarding: true`,
  `theme: "dark"`, and `projects[<absolute cwd>].hasTrustDialogAccepted: true`,
  Claude skips onboarding and the folder-trust dialog. In a real crew launch
  with no token, Claude with that config reaches the normal prompt with “Not
  logged in · Please run /login”, without making a model call (2.1.310).
- `--mcp-config` takes multiple values. When a wrapper puts `--mcp-config`
  before a subcommand, a following option must end the values of
  `--mcp-config`. Otherwise `--mcp-config` consumes the subcommand's words.
  `--mcp-config <json> --setting-sources '' auth status` preserves the
  subcommand.
- `CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false` disables composer suggestions
  without replacing inline hook settings. `promptSuggestionEnabled: false`
  is also a supported setting. Both switches were read in 2.1.310's source.
  The environment switch was used in the real crew lifecycle probe below.
- `--name <name>` supplies the native session title. Without `--name`, a local
  mock provider recorded four auxiliary Haiku title requests across a crew
  wake, two inputs, and a sleep. With `--name Member` and suggestions
  disabled, the same real-binary lifecycle made only the six expected Opus
  member requests (2.1.310). attn's `ATTN_HEADLESS_TASKS=off` does not disable
  Claude's native title generation.
- `-p --output-format json --json-schema ...` returns a result object by
  default and an array of events with `--verbose`. The array's last
  `type: "result"` event carries `structured_output`, `usage`, `modelUsage`
  and `total_cost_usd`. Both forms ran in real simulator and judge print calls
  against a local provider mock on 2.1.310.
- `--setting-sources ''` can still leave skills and slash commands listed in
  print-mode init metadata. That catalogue alone does not show whether the
  contents of the skills and commands reached the model.
  `--disable-slash-commands` produced empty skill and command lists in the
  probe. Fresh working and config directories, together with the memory-off
  switch, kept synthetic personal-context canaries out of the simulator and
  judge requests (2.1.310).
- A Bash tool filing the crew member's letter can close the session
  before Claude persists the final tool call to JSONL. In the 2.1.310 local
  provider probe, the fifth member request closed the session while the file
  retained only the four work requests. Persisted closure records therefore
  do not guarantee a complete trace of that final call.
- Enter depends on the byte and on when it arrives. After boot, CR alone
  submits and LF alone inserts a newline. attn does not change the terminal's
  modes, so the PTY translates CR to LF until Claude switches it to raw: a line
  typed during boot lands in the composer and is not submitted. Probed on
  2.1.318 through a PTY, with the turn read from the transcript.
- An interactive paste can be stored as `\n\n<pasted_content id="hhhh">\n`,
  the pasted text, and `\n</pasted_content id="hhhh">\n`, with the same four
  hexadecimal characters in both tags. A 2.1.310 paid eval trace preserved
  the inner text exactly while adding this container. The installed source gates wrapping on `tengu_virtual_pancake`. An offline probe needs
  that cached flag enabled and `CLAUDE_CODE_GB_DISK_CACHE_WHEN_TELEMETRY_OFF=1`
  while telemetry is disabled. Plain text remains possible, so code that
  matches native user input must account for both forms.

Probed on 2.1.309 with a mock API and an isolated `CLAUDE_CONFIG_DIR`:

- Nothing stops a second process from opening a conversation another process
  has open. `claude -r <id>` and `/resume <id>` both fire SessionStart
  `resume` with that id, with no refusal and no fork.
- Both processes then append to the one `<id>.jsonl`, each from its own
  history in memory, and neither sees the other's turns. Each process writes
  entries whose `parentUuid` continues the branch of that process, so the
  transcript becomes a tree with two branches. A third process resuming the conversation loads the
  branch with the newest leaf.
- Every hook from either process carries the same conversation id.

Claude Code has no link, so a terminal that resumes a live session's
conversation gets its own session on that conversation, and both tiles warn
until one process exits or moves to another conversation.

Probed on 2.1.303 with synthetic prompts and a local mock API:

- `--resume <id> --fork-session` copies the prior assistant messages into the
  new conversation's transcript, preserving their `message.id` and usage.
  A message id therefore identifies the same request across root transcripts.

### Agent view

Probed on 2.1.312 with real Claude in a fresh `CLAUDE_CONFIG_DIR` against a
local mock API, and read in its bundled source.

- ← on an empty prompt (the footer shows "← for agents") and `/background`
  (`/bg`) move the conversation into Claude's background daemon. The terminal's
  process becomes the agent view, `claude agents`.
- Moving forks the conversation: the daemon runs it with `--fork-session
  --resume` under a new id, and SessionStart `fork` fires with that id. The
  fork's transcript repeats every entry of the original with the same `uuid`s.
  The original transcript ends with
  `{"type":"continued-in","continuedInSessionId":"<fork>"}`.
- The agent view also starts a draft for its new-session prompt. SessionStart
  `startup` fires with a third id, whose transcript is written only once the
  draft gets a prompt.
- Esc, or Enter on the row, shows the fork in the terminal again and fires no
  hook. The next UserPromptSubmit carries the fork's id.
- One daemon serves one config directory. Its sockets live under
  `/tmp/cc-daemon-<uid>/<first 8 hex digits of sha256(config dir)>`, its log at
  `<config>/daemon.log`, its workers in `<config>/daemon/roster.json`. The
  first client that needs the daemon starts the daemon, and the daemon exits
  once no worker or client is left. `claude daemon status` shows the daemon.
  `claude daemon stop --any` stops the daemon with its workers.
- Workers inherit the daemon's environment, which is the environment of the
  client that started the daemon. A worker keeps the flags of the process that
  its conversation left, `--settings` included, so the worker's hooks run the
  same commands as before. `ATTN_TERMINAL_ID` in the worker's environment names
  the terminal that started the daemon, which may be another terminal.
  `CLAUDE_CODE_SESSION_ID` in a tool's environment is the running
  conversation's id.
- The agent view in any terminal lists every conversation the daemon runs for
  the config directory, and opening a conversation attaches it in that
  terminal, so two terminals can show one worker. Attaching fires no hook, and
  the worker's hooks still run the commands of the terminal the worker left.
- `claude -r <id>` of a conversation a worker runs exits 1: "That session is
  running in the background (<id>). Run `claude attach <id>` to open it, or
  `claude stop <id>` first to resume it here." `claude -r` of the original id
  opens the history from before the move.
- Closing the terminal that shows a worker leaves the worker running.
- `CLAUDE_CODE_DISABLE_AGENT_VIEW=1`, or `"disableAgentView": true` in inline
  `--settings`, turns the agent view off for that process: the footer drops the hint, ←
  does nothing, `/background` and `/bg` are unknown commands, and no fork or
  draft starts. Claude describes the switch as disabling "`claude agents`,
  `--bg`, /background, the on-demand daemon".

Claude's daemon lets several terminals show one conversation, as a link does,
but nothing tells attn which terminal shows which worker. attn launches Claude
with `CLAUDE_CODE_DISABLE_AGENT_VIEW=1` instead. Without the variable, a
conversation moved into the daemon no longer runs in the terminal attn
launched. attn would open one session for the draft and another for the fork.
The agent's attn commands would name another terminal. Reload session could not
resume the conversation. The variable also reaches `claude` run from the
agent's tools, so a `claude` that the agent runs cannot start conversations in
the daemon. Such conversations would carry the agent's attn identity. Claude
that the user starts by hand in a terminal running a shell keeps its agent
view. That terminal has no
`ATTN_TERMINAL_ID`, so the conversation carries no attn identity.

### Auto memory

The [Claude memory documentation](https://code.claude.com/docs/en/memory)
defines `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` to disable auto memory for one
process. The switch is present in the installed 2.1.310 binary. The switch
leaves the user's stored memory files in place. A new working directory alone
does not turn auto memory off (checked with unpaid CLI probes on 2.1.310).

### Program status (OSC 7501)

Probed on 2.1.295 under a scratch PTY, and read in its bundled source.

- At startup Claude sends `OSC 7501 ; ?` before its DA1 query. Claude reports
  only when the 7501 reply arrives before the DA1 reply. Otherwise Claude never
  asks again in that process. `CLAUDE_CODE_DISABLE_TERMINAL_TITLE` turns the
  reports off along with the title.
- Claude reports the root record with `app=claude-code`:

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
  `working` or `blocked`. attn reads only the root record.
- Claude sends a report only when the reported state changes. There is no
  heartbeat. When Claude hands the terminal back, on exit or suspend, it sends
  `state=clear`.
- Claude keeps setting its title glyphs. Once a terminal has a root record,
  attn ignores the glyphs until a `clear`.

Probed on 2.1.303 under a scratch PTY with a local mock API, in auto and
default permission mode:

- AskUserQuestion fires PreToolUse, then `blocked kind=question` about 20 ms
  later, then a PermissionRequest for `AskUserQuestion` about 40 ms after
  PreToolUse.
- With `/model`, `/resume`, `/config`, `/permissions` or the Esc-Esc rewind
  menu open, Claude sends no report and fires no hook.

### Mods

A mod is a plugin whose `hooks/hooks.json` names a TypeScript hooks module.
The module's API is typed in the `plugin-authoring` skill's
`types/claude-code.d.ts`. Probed on 2.1.316 under a scratch PTY against a
local mock API, launched with attn's environment, `--settings` hooks and
`--permission-mode auto`:

- `--plugin-dir <folder>` and `CLAUDE_CODE_PLUGIN_DIRS=<folder>` both load the
  module in an interactive session with no consent question. Claude watches
  the folder: a change to the module's content reloads it, which runs
  `session.start` again. On 2.1.317, touching the file does not reload it.
- A `session.start` hook can keep a `$.process.spawn` child for the session's
  life, as a direct child of the `claude` process. The child survives
  `/clear`, `/compact` and `/resume`. A reload, `/exit` and a SIGKILL of
  `claude` end it. Its stdin is written once and closed.
- `$.prompt.submit({ text, asUser: true })` on an idle session starts a turn
  with the text bare, as typed. UserPromptSubmit and Stop fire with the current
  conversation id. The transcript keeps a `user` row with the text as a string
  and `origin: {"kind":"plugin","name":<plugin>,"asUser":true}`. The screen
  shows "› Prompt from the <plugin> plugin" above the prompt. The call resolves
  `{ text, origin }` as the turn starts.
- `$.prompt.submit` during a turn resolves only when that turn completes, then
  starts its own turn. It never reaches the running turn.
- A prompt typed into the PTY during a turn reaches the running turn: the next
  model request carries it after the tool result, as "The user sent a new
  message while you were working:" and the text. The `prompt.submit` hooks see
  it with `origin: {"kind":"composer"}` and the running `turnId`.
- `$.session.append({ message: { type: "user", content: [text block] } })`
  during a tool call adds the text, bare, to the running turn's next model
  request, after the tool result. The transcript keeps it as an `isMeta` user
  row with the plugin's origin. The screen does not show it.
- A permission question replaces the prompt, and the `AbovePrompt` band is not
  drawn until the question closes (2.1.318). Refusing the question, with Esc or
  with "No", leaves `next.signal.aborted` true in that `tool.call` hook and ends
  the turn with `isAborted` false and `reason: "answer"`. A mod cannot tell the
  two keys apart: the hook events, the abort reason (`The operation was
  aborted`) and the transcript's `toolDenialKind` (`user-rejected`) are the same.
  Neither stops the session, so a `$.prompt.submit` afterwards starts a turn. Esc
  during a running tool differs in all but `toolDenialKind`: the abort reason is
  `user-cancel`. The question ignores keys for a moment after it draws, with no
  signal for when it starts taking them: a key sent as soon as the screen shows
  the question did nothing, on Esc and on the number for "No", and one second
  later it worked. The `permission_prompt` Notification hook fires about 6 s after
  the question draws, when keys work.
- An append made inside a `turn.step` hook, before `next(e)`, misses that
  request: it reaches the model only on a later request.
- A `turn.complete` hook that calls `$.prompt.submit` starts a new turn right
  after an answered turn ends.
- `$.session.send` to the session's own id fails: `isDelivered: false`, "no
  live session on this machine has id <id>".
- `/clear` raises `session.end` with `reason: "clear"` and no `session.start`;
  `$.session.id()` returns the new id from then on. `/resume <id>` raises
  `session.end` with `reason: "resume"`. Both still fire the SessionStart
  settings hook.

Probed on 2.1.317 the same way, in auto, default and bypass permission modes:

- The terminal raises no `ui.render` for `UserMessage` rows, so a mod cannot
  redraw a prompt row or remove the "Prompt from the <plugin> plugin" line. A
  `$.session.append` of a `system` row is stored but not drawn.
  `$.ui.log(text)` draws a transcript line as `<plugin>: <text>`, which the
  model never reads.
- `$.env.get(name)` reads the `claude` process's environment.
- A loop started from `session.start` can call `$.http.fetch` with
  `socketPath` on a Unix socket. A call with no complete answer after 30 s
  rejects: "aborted: no complete answer within 30000ms". A reload aborts the
  old module's call in flight, and `/exit` closes it.
- Appends made after a main-loop `tool.call` hook's `next(e)` resolves reach
  the next model request, all of them, after the tool result. This holds when
  the tool call waited on a permission question that the user approved.
- Esc during a tool call resolves that `next(e)` with `next.signal.aborted`
  true, and `turn.complete` reports `reason: "aborted"`. An append made then is
  stored and reaches the model only with the next turn. Until the user sends
  input, `$.prompt.submit` resolves `{ drop: "the session was stopped and
  nobody has sent input since" }` and starts no turn. The stop does not
  expire: submits 5 s, 65 s and 185 s after Esc were dropped, and so was a
  submit from the aborted turn's own `turn.complete` hook before `next(e)`. A
  prompt typed into the PTY during the turn is sent as its own turn as soon as
  Esc stops it, and typed input lifts the stop.
- Denying a permission question also sets `next.signal.aborted`, but
  `turn.complete` reports `reason: "answer"`, and a `$.prompt.submit` right
  after starts a turn.
- The Agent tool runs a subagent in the background: the main turn completes
  while the screen shows "Waiting for 1 background agent to finish". A
  `$.prompt.submit` or a typed prompt in that time starts a main turn at once.
  When the subagent finishes, a main turn starts with empty text. The
  subagent's `turn.start`, `tool.call` and `turn.complete` carry its `agentId`.

Measured on 2.1.318 through attn's mod, with the real binary and a mock API:

- The findings above hold, with these additions.
- `$.state` survives a reload that lands during a turn, and the new module's
  `tool.call` hooks fire for that turn. Module variables start over, so a mod
  that must know a turn is running keeps that in `$.state`.
- `$.prompt.read()` from a stopped session returns the composer as it stands:
  `''` after Left, Right, Home, End, PageUp, Shift+Tab or a draft erased with
  Backspace, and the text of a typed draft or one recalled with Up.
- A reload draws `attn: reloaded (5 hooks: ...)` in the transcript, named after
  the plugin. The model does not read it.
- A save that does not parse keeps the old module running and polling. A save
  of a valid module that does nothing ends its polls.
- `/exit` ends the session in attn at once. A delivery to it then fails with
  `unknown session`, so nothing is left to type into.
- `/clear` keeps the module and its poll loop, and the terminal moves to a new
  session id.
- A tool result followed by several appended rows reaches the model as one user
  message, each row a text block. Every block but the last gains a trailing
  newline.
- On a fresh config, the first launch asks "Make auto mode your default
  permission mode?". attn reports the session as needing approval until the user
  answers, so a delivery waits. `hasSeenAutoDefaultNotice` alone does not skip it
  with `permissions.defaultMode` set to `default`.
- In a folder with no trust recorded, `claude` makes its first poll only after
  the user answers the trust question: 0.3 to 1.4 s after the answer in 4 runs,
  and never before it. In a trusted folder, launched by attn, the first poll
  came 0.71 to 5.54 s after launch (12 launches, median 0.9 s).
- `$.session.compact()` with no argument compacts with the engine's own prompt.
  An idle compact resolved `{ messages, tokensBefore, tokensAfter }` 5 times out
  of 5, in 8 to 18 s at up to 82k tokens, and it resolves `{ skip }` when a hook
  vetoes it. While a turn runs it rejects within 1 ms with a message that says
  the conversation compacts between turns: `a turn is running (<id>)`,
  `a turn is in flight`, or `an external turn is driving the conversation`. A
  turn that begins during the compact makes it reject with
  `a newer turn began before the compaction could be applied`. It also rejects
  on an empty conversation (`Not enough messages to compact`) and with
  `DISABLE_COMPACT` set. A poll loop keeps running through a compaction, so the
  mod calls it without awaiting. PreCompact and PostCompact fire with
  `trigger: "manual"`.
- A prompt typed during that compact waits, and Claude runs it on top of the
  summary once the compact ends.
- The compact's `system`/`compact_boundary` and `isCompactSummary` entries reach
  the transcript file only when Claude exits, which attn's close does with
  SIGTERM. `claude --resume` loads from them.

## Codex

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

### Local memories

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

### Codex app-server and `--remote`

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
  a plain TUI shows the lock screen described above, both as
  `codex resume <id>` and with `/resume <id>`, and writes nothing.
- `thread/archive` moves the rollout to `archived_sessions/` (flat, no dated
  directories) and refuses later resumes. `thread/unarchive` moves the rollout
  back.

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

### Codex updates

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

## Nisse

Read in the source at victorarias/nisse 062ef0b.

- One server per Nisse home holds every conversation. Clients reach the server
  over `NISSE_HOME/nisse.sock`. `nisse serve` refuses a home another server
  already serves (`packages/nisse/src/main.ts`).
- The TUI and `nisse exec` start a detached server when none answers. A server
  exits 30 seconds after its last client leaves and its last run settles.
- `nisse --conversation <id>` opens that conversation, and starts a
  conversation with that id when none exists. A prompt after the flags is sent
  once the conversation opens.
- `conversation/submit` is idempotent by its `requestId`: a retried submit is
  one input.
- Every `Bash` call, and Claude Code's process, runs with
  `NISSE_ROOT_CONVERSATION` set to the id of the conversation people started.
- `conversation/start` takes `instructions`, kept with the conversation and
  not given to its subagents: OpenAI is told them after the `AGENTS.md` files,
  and Claude Code appends them to its own system prompt.
- `models/list` answers each provider's models, with the efforts each takes.
- `status/watch` opens with the busy conversation trees and then sends one
  status per change of a tree (working, waiting on an approval, idle), with
  how its root's run ended. Subagents are part of their root's tree.
- `conversation/list` carries `ended` on each conversation that has run: how
  its last run ended, the same as `status/watch` says.

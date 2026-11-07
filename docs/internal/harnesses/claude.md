# Claude Code harness behavior

How Claude Code behaves where attn meets it. The rules for adding a claim are in
[Harness behavior](../harnesses.md).

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
- Claude also compacts by itself while it sits at its prompt. Probed with Opus
  5.5 and subscription logins, without attn, in seven conversations on the
  1-hour prompt cache: two on 2.1.318, and five on 2.1.319 behind a recording
  proxy, set with `HTTPS_PROXY` and `NODE_EXTRA_CA_CERTS`. The rules below were
  read in the 2.1.318 and 2.1.319 bundles.
  - Claude checks 54 minutes after the last main-thread request was sent, with
    6 minutes of cache left, within 0.2 s in every run. Stop and an unsent draft
    typed later do not move the check.
  - The check does nothing when auto-compact or `idleCompaction` is off, the
    prefix changed, the cache is not 1-hour, the cache lapsed or the check runs
    more than 60 s late, context is under
    `max(100000, CLAUDE_CODE_IDLE_COMPACT_MIN_TOKENS ?? 200000)` tokens, a newer
    request exists, or the rate limit status is not `allowed`. The bundle also
    has a variant that precomputes the summary, which was not seen. A skip has no
    hook and no screen change. Claude's telemetry reported two skip reasons:
    `small` for 168k tokens under the default threshold, and `near_limit` for
    245k, and for 168k with the threshold at 100000. At 238k on another account
    with status `allowed`, it reported `started`.
  - There is no fixed usage cutoff. The bundle sets the status to
    `allowed_warning` when a response reports
    `anthropic-ratelimit-unified-<window>-surpassed-threshold` for any window, or
    when its pacing table matches. In the 5-hour window, that is 90% used with at
    most 72% elapsed. In the 7-day window, it is 75% with at most 60% elapsed, 50%
    with at most 35%, or 25% with at most 15%. The `near_limit` skips came from the
    header: `7d-surpassed-threshold: 0.75` at 76% used and 66% elapsed, which the
    table alone would not match. Where the server sets its thresholds was not
    seen.
  - A compact's hooks are PreCompact `auto`, SessionStart `compact` and
    PostCompact `auto`, over about 6.5 s, all carrying the last turn's
    `prompt_id`. No UserPromptSubmit, Stop or Notification comes with them. When
    the compact fails, PreCompact is the only hook and the screen shows no error.
    That was seen against a local server that answered without a summary.
  - Program status goes `working`, then `done`, around an idle compact, whether
    or not it succeeds. PreCompact and PostCompact finish before `done` is sent.
    So a `done` with no PostCompact before it means the compact failed. The next
    prompt then runs as a normal turn. Probed on 2.1.319.
  - A normal turn opens with `thread: {type: "create"}`. Its later requests use
    `{type: "continue", previous_message_id}` and carry only the new messages,
    with no `tools`. A compact request has no `thread`, sends
    `x-claude-code-request-class: compaction` and
    `x-claude-code-compaction: auto`, and carries the whole conversation and its
    summary prompt. Its usage at about 240k was 32k to 40k read from the cache and
    207k uncached. The next turn opens a new thread with 34k to 41k of context.
  - The screen shows `Compacting conversation…` above the composer. An unsent
    draft stays in the composer on the default and fullscreen renderers, and
    Enter then submits it as a normal turn on top of the summary. After 75 minutes
    the footer shows `new task? /clear to save <n>k tokens`.
  - Against a local server with `ANTHROPIC_BASE_URL` and an API key, Claude sends
    no `thread` and no compaction headers, and uses the 5-minute cache, so the
    timer never arms. With `ENABLE_PROMPT_CACHING_1H=1` and a response reporting
    245k of usage, the same idle compact runs at 54 minutes with the same hooks.
    The summary prompt contains
    `create a detailed summary of the conversation so far`, and the response must
    hold a `<summary>`. An API-key session ignores the rate limit headers (read in
    the bundle), so a local server cannot reach `near_limit`.
  - `go run ./scripts/fakeclaudeapi check-idle` checks the timing, the size and
    cache skips, and the compact and failed-compact hooks against the installed
    Claude, with no account or tokens, in about 58 minutes. It cannot run faster,
    because the timer is a real one and needs exactly the 1-hour cache. Run it
    after Claude updates, and update this section, the fake API and attn together
    when it fails. `go run ./scripts/fakeclaudeapi serve` runs the same fake on its
    own, with a `/control` endpoint for context size and for holding or failing
    compacts.
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
- A composer suggestion is Claude's guess at the user's next prompt, not input.
  Plain text cannot tell a suggestion from a typed draft: both read
  `❯ keep the gate, harvest it`. In cell styles, a suggestion is dim with the
  cursor on its first letter, and a typed draft is normal text with the cursor
  after it. `attn agent peek` labels a suggestion by those cells, read from the
  screen snapshot. The `Try "…"` placeholder of an empty composer has the same
  cells, so peek labels it too (2.1.319). An idle compact leaves both in place.
  Enter alone leaves a suggestion unsent. A paste and Enter replace a suggestion,
  so the model sees only the pasted text. With a typed draft, the model sees the
  draft and the pasted text as one prompt. Probed on 2.1.318 with a local fake
  API, under tmux, with `CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=true`. Claude asks
  for a suggestion with a `[SUGGESTION MODE` request after a turn stops, and not
  before the conversation has two assistant messages (read in the bundle). With
  attn's hooks on 2.1.319, Claude dropped a suggestion that a local API answered
  at once, and showed it when the answer came 3 s later or after attn saw the
  turn end. Under attn, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` leaves
  suggestions on.

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
- Read in the 2.1.319 bundle: Claude rewrites `.claude.json` under a lock directory
  at `<file>.lock` and rereads the file inside the lock, so a writer that takes the
  same lock loses nothing. A lock with no holder to refresh it counts as stale after
  10 s. Sampled every 1 ms for 90 s on a Mac running many agents, Claude held the
  lock 12 times, the longest for 407 ms; attn waits 2 s (`claudeConfigLockWait`).
  SIGKILL during startup left the lock behind when attn's model discovery did it.
  SIGTERM exits in about 0.8 s and closing stdin in about 1.3 s, and neither left a
  lock in 6 runs each.
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
  modes, so the PTY translates CR to LF until Claude switches it to raw. Probed
  on 2.1.318 through a PTY, with the turn read from the transcript.
- A line typed before Claude reads its terminal does not become a turn. Launched
  with no prompt, the line lands in the composer and is not submitted (4 of 4
  launches, typed at 0.05 to 0.15 s). `--resume` with no prompt does the same.
  Launched with a prompt, as every crew wake
  is, the line reached neither the screen nor the transcript in 9 of 10 launches
  typed at 0.05 to 0.35 s. The tenth, typed at 0.35 s, became a turn, as both
  lines typed at 0.45 s did, in a boot that showed its prompt at about 0.5 s.
  A line typed as soon as the terminal title appeared became a turn in 7 of 7
  launches, and one typed at UserPromptSubmit in 3 of 3. SessionStart is too
  early: typed then, the line was lost in 1 of 3 launches, and a 1.5 s
  SessionStart hook does not hold the boot back. At the folder trust question
  Claude sets no title and fires no SessionStart (2 of 2 launches). Probed on
  2.1.319 on Linux through a PTY with a mock API, with the turn read from the
  requests.
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

## Agent view

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

## Auto memory

The [Claude memory documentation](https://code.claude.com/docs/en/memory)
defines `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` to disable auto memory for one
process. The switch is present in the installed 2.1.310 binary. The switch
leaves the user's stored memory files in place. A new working directory alone
does not turn auto memory off (checked with unpaid CLI probes on 2.1.310).

## Program status (OSC 7501)

Probed on 2.1.295 under a scratch PTY, and read in its bundled source.

- At startup Claude sends `OSC 7501 ; ?` before its DA1 query. Claude reports
  only when a reply starting with `?` arrives before the DA1 reply, so
  Ghostty's `?:states=…:kinds=…` counts (read in 2.1.319's bundle). Otherwise
  Claude never asks again in that process. `CLAUDE_CODE_DISABLE_TERMINAL_TITLE` turns the
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

## Mods

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
  model never reads. On 2.1.319 it stores and draws U+FFFD in place of each
  newline, so it cannot show more than one line.
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

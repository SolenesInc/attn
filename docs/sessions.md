# Sessions

## Agent conversation

The provider-owned history hosted by an attn session: its native id, transcript,
and resume target. The session's workspace, pane, seed binding, PTY, and turns
can continue while that conversation changes. Codex `/new`, for example, starts
a new rollout inside the same session.

A conversation change invalidates the prior transcript watcher, activity line,
and activity cursor. Reload and other readers resolve the newly bound native
id. A [conversation session](#conversation-session) is the headless runtime
that can host such a history.

## Turns and the queue

A **turn** is attention the user owes an agent. It opens when a session reaches
a state that wants the user and closes when the user steers, approves,
snoozes, or settles it. Looking at an agent does not close its turn.
`internal/attention` owns these predicates. Broadcasts derive `turn_owed` from
persisted `turn_opened_at` and `turn_settled_at` stamps; it is never stored.

**Auto-settle** closes a turn after the agent positively takes user conversation
input. The session must stay `working` through an invisible arm delay, a visible
countdown, and settlement. Heartbeats, work-item nudges, peer messages, generic
`working` observations, and unclassified input grant no credit. Several inputs
can contribute credit to one uninterrupted working stretch. Auto-settle has the
same session exclusions as the queue.

A **standing dismissal** cancels the session's next auto-settle for the current
working stretch, whether the countdown has appeared yet or not. It expires at
the end of that stretch and is exposed as `auto_settle_dismiss_armed`. Dismissal
leaves the turn owed.

The **queue** is the sidebar order built on turns. Queue mode is off by default.
Its order is the chief's anchored slot, owed turns (oldest first), settled
sessions, pinned agents, pinned workspaces, and muted items.

**Pinning** lets the user choose when to return. A pinned agent
(`sessions.pinned_at`) leaves the queue while its siblings remain. Pinning a
workspace removes all its agents. Turns keep accruing while pinned, so
unpinning restores outstanding turns at their original age.

A **satellite** is a shell split from an agent, recorded at spawn through
`sessions.parent_session_id`. It appears as a pane under that agent and has no
separate sidebar row. Splitting from a satellite keeps the same agent parent.
At read time, the parent must still exist in the same workspace. If the agent
closes or the pane moves away, the satellite gets its own row. A satellite
without a live parent is an **orphan**; orphans keep their rows.

## Session input

The daemon boundary for mutations of a live session. It handles user PTY bytes,
replay and automation bytes, paste plus Enter, conversation-host messages, and
plugin-driver messages. Product domains choose the content, when it becomes
obsolete, and when to retry. Session input owns ordering, the current route,
input-safety checks, retry mechanics within an attempt, and evidence that the
agent took the submitted input.

| Evidence stage | Meaning |
| --- | --- |
| **deferred** | The attempt did not mutate the target. |
| **placed** | An adapter owns the complete input; this does not prove the model read it. |
| **taken** | A positive observation proves the agent started reading this input. |
| **indeterminate** | The target may have changed, but the result cannot be attributed safely. |

Each taken input has an origin: user conversation, user control, attn
maintenance, peer agent, or unknown. Only user conversation grants auto-settle
credit. The origin belongs to the input even when several inputs share one
working stretch. Positive request-start observations advance the durable,
monotonic `last_model_request_at` clock; state and classifier timestamps cannot.

## Session activity and presence

A session's **activity** is one present-tense line describing what the agent is
doing, such as "running the frontend test suite". A non-interactive agent
writes it from the session transcript. attn stores the line and generation
time in `sessions.activity` and displays it under the session name on home.
Activity generation is off by default because each refresh costs money.

The **activity cursor** (`sessions.activity_cursor`) records the transcript
position used for that line. If the transcript has not advanced, attn keeps the
line and skips generation, so blocked and finished agents incur no refresh cost.

The **presence tier** describes how much attention the user is giving attn.
Clients report window visibility, whether home is showing, and time since input.
The daemon uses the highest tier reported by any connected client:

- **watching**: the app is visible and showing home; activity refreshes fastest.
- **present**: the user recently gave input in the app, with home off screen.
- **away**: nobody can see activity; generation stops. Returning to a higher tier
  refreshes stale activity when it becomes relevant again.

Clients renew presence with heartbeats. An expired report becomes `away`, so a
crashed client cannot leave generation running indefinitely.

`internal/daemon/presence.go` also tracks **last user activity** from UI-origin
commands. That answers whether the user acted on the daemon; it cannot detect
someone reading the screen.

## Recoverable

A session is **recoverable** when its runtime is gone but attn can restore its
conversation. The sidebar offers Reload, and mounting its pane revives it.
Recovery requires a durable launch intent and a restoration target still on
disk: a driver-recognized native resume id, a conversation host's session file,
the launch intent's source conversation or as-yet-unsaid initial prompt, or a
plugin's persisted session handle.

The session's last activity state does not decide whether it survives.
`idle`, `working`, `waiting_input`, and `pending_approval` sessions can all be
restored after a crash. If restoration is impossible, the session is **reaped**:
attn removes its row and pane.

## Conversation session

A **conversation session** runs a headless agent in a process attn spawns.
It has the same workspace, pane, state, turns, and seed binding as a PTY session.
Its **host** exchanges envelopes and verbs with the daemon, with no terminal
grid. The daemon owns the host's lifetime, signals it to shut down, and kills
its process group as a fallback so child processes cannot outlive the session.

An **envelope** carries a session id, monotonic sequence number, kind, and body.
Its kinds form two families:

- **Declarations** tell the daemon about session events: `session_ready`,
  `run_started`, `run_settled`, `tool_started`, and `tool_finished`.
- **Renderings** describe what the app draws: `message_start`, `message_delta`,
  `message_end`, `queue_update`, `tool_detail`, `conversation_page`, `notice`,
  and `model_changed`. The daemon forwards them without interpretation except
  for `model_changed`, which records the model for relaunch.

A **state declaration** carries the attn state for a run boundary. The daemon
reads that state directly, so `working`, `idle`, and future states such as
`pending_approval` use the same path. Tool-boundary declarations describe an
action without changing the session state.

A **tool call** appears as a card with the tool name, a brief description of its
target, and its result. **Detail** is the content it read, wrote, or printed;
the host sends that only when a reader opens the card.

A **run** covers one prompt and the agent's response from `run_started` to
`run_settled`. Its boundaries follow the same turn rules as PTY agents.

Three input verbs determine when a message is read:

- A **prompt** opens a run. The composer uses it while the agent is idle.
- A **steer** arrives at the next agent turn boundary. Work-item nudges, chief
  nudges, and Present notices use it for conversation sessions.
- A **follow-up** waits for current work to finish and is read before the run
  settles, so the agent cannot stop with a follow-up unread.

A steer or follow-up starts a run when none is open. The **queue** holds sent,
unread messages. The host reports them as queued and then seen; clearing the
queue drops every pending message. Clients display the host's reported queue.

A **conversation snapshot** contains the newest transcript stretch, whether a
run is open, and the queue. The host builds it from memory, including unfinished
messages that have not reached the session file. This lets a newly connected
client draw the current conversation.

Older transcript content is **scroll-back**, served by the host one **page** at
a time as a reader scrolls. A snapshot's **epoch** identifies the host process.
Clients splice snapshots from the same epoch into their loaded scroll-back and
replace it when a new host supplies a different epoch.

The host bounds its retained transcript. A client may still show older pages
it loaded before the host discarded them; the host leaves those client copies
alone, and later page requests can return empty. When the host has dropped the
start of a conversation and no older page remains, the transcript shows a row
counting the missing items.

**Resuming** copies an existing conversation into a new session's storage. The
source stays unchanged, and two sessions never write the same history.
Reloading a [recoverable](#recoverable) session reopens its own history.

A **notice** explains an interruption such as compaction or retry. The same row
changes from in progress to finished.

Conversation history lives under attn's data directory. Reloading after a host
or daemon failure starts a replacement host against that session file. A
history that does not end with the agent speaking returns as `waiting_input`;
a completed one returns as `idle`. Both open a turn and accept nudges, while
`waiting_input` tells the user the agent stopped before finishing.

A **launch prompt** is the first message supplied when a session opens, often a
delegation brief. The daemon stores it on the session and gives it to every
replacement host. The host submits it only when the reopened history is empty,
covering both first launch and a crash before it was said. Any existing history
prevents replay. Only conversation sessions store a launch prompt; PTY agents
resume their native transcripts without replaying the brief.

A plugin driver registers the `conversation` capability for this runtime and
uses the same `driver.spawn` call to provide argv, environment, and working
directory. It does not declare the PTY `resume` capability, which describes
resuming through an argv flag; the host resumes from its own session file.

## nisse

**nisse** is attn's own agent and its first conversation agent. Pi's SDK runs
the model loop. attn owns the host process, its lifetime, protocol, delegation,
and the pane where the user reads it.

A nisse is the Scandinavian household spirit that keeps a house going while
everyone sleeps, as long as you leave out its porridge. attn is the house,
nisse lives in it, and your attention is the porridge.

Use **nisse** for the agent, **host** for the process running a conversation
agent, and **pi** for its engine. The host can run other conversation agents.
The CLI and protocol name the agent `nisse`; its launch environment uses
`ATTN_NISSE_*`. `plugins/attn-pi` registers both nisse and the PTY-backed pi agent.

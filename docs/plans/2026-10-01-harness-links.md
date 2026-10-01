# Plan: harness links

attn talks to the harnesses it runs through their terminals: it pastes text
into the PTY and reads the screen and window title to guess state. This plan
adds a structured side channel, the **harness link**, that carries what attn
sends and what it needs to know. The PTY goes back to being the user's
terminal: it renders the harness natively and carries the user's keystrokes.

The core speaks one harness-neutral contract. Adapters translate it to each
harness's own mechanism. A future attn-native harness implements the contract
directly, with no change to the core.

Status: proposal. Opt-in per harness; the PTY route stays the default.

## Goals

- attn's own traffic stops going through the PTY: doorbells, crew heartbeats,
  annotations, nudges.
- State comes from the harness, not from the screen: turn start and end,
  pending approval, interrupt.
- Harness knowledge stays in adapters. `internal/daemon` never learns
  JSON-RPC methods, socket frames or plugin APIs.
- The contract can express what today's adapters cannot yet do (attn speaking
  as itself rather than as the user, content delivery) without requiring it.
- No idle cost: no polling and no process per hook. Daemon restarts reattach.
  Daemon code stays portable to Linux.
- The doorbell policy does not change: content-free, the agent pulls with
  `attn agent inbox`.

## Non-goals

- Forking a harness.
- Answering approvals from attn.
- Delivering message content instead of doorbells.
- Copilot. It stays on the PTY route.

## Principle: custody, not receipts

A delivery has one result: the synchronous answer to "did the harness take
custody of this input?" After that, attn does nothing more for it. It does not
correlate later events with the input, match echoed text, read the transcript
or start acknowledgement timers.

The reason is that a receipt only earns its keep if attn can act on a failure
better than the user can, and it cannot:

- **Annotations:** the user sees what arrived and can resend.
- **Doorbells:** the mailbox stays unread and re-rings.
- **Heartbeats:** a missed one is retried on the next cycle.

A harness mechanism qualifies as a link only if it can answer custody
synchronously. Each adapter's answer:

| Adapter | Custody answer |
|---|---|
| Codex app-server | the `turn/start` response |
| Claude inbox socket | the socket write succeeded |
| Claude plugin | the plugin's reply after `$.prompt.submit` |
| pi | the `driver.deliver_message` result |

## The contract

```go
// package harnesslink: no imports from internal/daemon.

type Voice uint8

const (
	VoiceUser Voice = iota // the user's words: annotations, user conversation
	VoiceAttn              // attn's own: doorbells, heartbeats, nudges
)

type Placement uint8 // today's sessionInputPlacement, moved here

const (
	WhenPromptReady Placement = iota
	AtTurnBoundary
)

type Input struct {
	ID        string // idempotency key: the attempt id
	Text      string
	Voice     Voice
	Placement Placement
}

type Custody struct {
	Taken      bool
	At         time.Time
	Reason     string        // when not taken
	RetryAfter time.Duration // zero means do not retry
}

type Link interface {
	Voices() []Voice // the voices this link can deliver
	Deliver(ctx context.Context, in Input) Custody
	Close() error
}

// Events is implemented by the core and handed to an adapter when it attaches.
type Events interface {
	TurnStarted(at time.Time)
	TurnEnded(at time.Time, cause EndCause) // completed | interrupted | failed
	ApprovalPending(at time.Time)
	ApprovalResolved(at time.Time)
	Down(err error) // reattach is the adapter's job; the core defers deliveries meanwhile
}
```

- **Voice.** Voice is the extension point. On stock Codex both voices
  arrive as user messages. The Claude inbox socket only carries `VoiceAttn`,
  since the model reads it as "another session, not your user". A native
  harness can render `VoiceAttn` distinctly.
- **Placement.** The core keeps phase gating. `WhenPromptReady` needs idle or
  waiting_input, and pending_approval defers everything, as today. The spike
  showed Codex handles input during an approval safely, but loosening that
  rule is a separate decision.

## Core changes

### Session input (`internal/daemon/session_input.go`)

- **Route choice.** Pick the route per input: the session's link if it
  supports the input's voice, otherwise the PTY. The route is fixed for a
  session run. If the link is down, deliveries defer with a retry instead of
  falling back to the PTY mid-run.
- **The link path is short:**
  1. phase gate;
  2. `Deliver`;
  3. map custody onto today's results: taken becomes *placed*, not taken
     becomes *deferred* with the retry delay.
  
  It has no quiet window, no screen safety, no held Enter, no
  composer-occupancy check and no user generation. The spike showed attn's
  input does not touch the user's draft.
- **The PTY path moves into `session_input_pty.go` unchanged.** It keeps
  only the checks that protect the user (the quiet window, selector and
  approval detection, holding Enter). It is retired harness by harness.
- **pi's plugin route becomes the first link adapter.** The `message_delivery`
  driver capability maps to a link with both voices. This is the
  no-behaviour-change step that proves the seam.

### Receipts

Drop "taken" from link routes entirely, and propose dropping it from the PTY
route too. The consumers move as follows:

| Consumer | Today | With custody |
|---|---|---|
| Doorbell (`agent_mailbox.go`) | placed or taken stamps notified | custody stamps notified |
| Annotations (`ws_session_annotations.go`, `ws_markdown_annotations_submit.go`) | delivered on placement | unchanged: custody means "sent", and the user sees the result |
| Crew heartbeat (`crew_lifecycle.go`) | waits up to 3s for taken, records `takenAt` | records `Custody.At`; no wait |
| Owed input (`recordPlacedInputOwed`) | cleared when the prompt text is matched | link routes never set it; on the PTY route, cleared by the next turn start, matching no content |
| User-input auto-settle | armed on taken | armed on custody of a `VoiceUser` input |

Once the PTY route follows, these go away:
- `observePromptTaken` suffix matching
- the pending-candidates list
- `sessionInputTakenWindow`
- `await`
- the indeterminate stage, as a receipt outcome
- `report_input_taken` for pi

That is its own PR, after the link routes ship.

### State (`internal/daemon/session_evidence.go`, `internal/sessionstate`)

- **Link events become evidence** through the same entry points hooks use
  today (`recordBracketEvidence`, the stop path), with a new source,
  `link`. `TurnEnded` runs today's stop path, so the classifier still
  decides idle versus waiting_input.
- **A linked session is launched without the hooks and title signals the link
  replaces.** For Codex: no state hooks and `HarnessSignalsNone`. This is
  decided at launch, so the resolver needs no precedence rules between
  sources.
- **What stays:** hooks the link does not replace (`SessionStart` for the
  transcript path, `PostToolUse` for edits and PRs).

### Launch and lifecycle

- **The adapter shapes the launch.** A new optional driver method returns
  extra argv, env and settings, plus the link endpoint to attach to.
- **The endpoint lives with the PTY run, not the daemon.** It is created by
  the PTY worker's wrapper and dies with the session, so a daemon restart
  reattaches instead of relaunching.
- **The run record stores the link kind and endpoint path** (DB migration),
  so a restarted daemon knows what to reattach.
- **Opt-in** is a daemon setting per harness, read at launch: `link.codex`
  (`off` | `app-server`) and `link.claude` (`off` | `inbox` | `plugin`).
  Running sessions keep the route they started with.

## Adapters

### Codex: app-server (validated by the spike)

- **Launch.** The wrapper starts `codex app-server --listen
  unix://<run>/codex.sock` and runs the TUI in the PTY as `codex --remote
  unix://<run>/codex.sock`.
- **Attach.** The adapter connects as a second client and runs `initialize`,
  then `thread/loaded/list` and `thread/resume`.
- **Deliver.** `turn/start` starts the thread when idle and steers when busy.
  `clientUserMessageId` carries the input ID for logs only. Both voices.
- **Events.** It maps notifications for the session's thread only, ignoring
  side threads such as title generation:
  - `thread/status/changed` → `waitingOnApproval` / idle / active
  - `turn/started` / `turn/completed` → turn start and end, including interrupted
- **Settings move.** Model, effort and approval policy move from `-c` argv to
  `thread/start` / `thread/settings/update`, because settings belong to the
  thread and an app-server `-c` did not change an existing thread.
- **Down.** The TUI reconnects on its own after an app-server restart (spike:
  about 6s). The adapter reattaches the same way.

### Claude: inbox socket (documented, no plugin needed)

- **Launch:** `--messaging-socket-path <run>/claude.sock` and
  `--settings '{"crossSessionInbound":"accept"}'`.
- **Deliver.** One NDJSON line,
  `{"type":"user","message":{"role":"user","content":…},"priority":"next","uuid":…}`.
  Custody is the write succeeding. `VoiceAttn` only.
- **No receiving side.** The socket can send back frames saying a message was
  refused or held; the adapter doesn't read them, and the accept setting
  prevents holds.
- **Events: none.** Claude's hooks stay authoritative for state.
- This gives Claude doorbells, heartbeats and nudges without the PTY.
  Annotations stay on the PTY route.

### Claude: plugin (experimental; the API is early access)

- **What attn ships.** A function-hooks plugin, loaded with `--plugin-dir`,
  next to the attn skill.
- **Daemon channel.** A long-lived connection to the daemon socket, using
  `$.process.spawn` of `attn _claude-link` or a held `$.http.fetch` over the
  socket. It is not polling.
- **Deliver.** `$.prompt.submit({text, asUser})`, where `asUser` follows the
  voice. Both voices.
- **Events:** `turn.start`, `turn.complete` and `classic.PermissionRequest`.
  They replace the state hooks.
- **Down.** When the plugin seam is unavailable (remote gate, policy, API
  break), the session falls back at launch to the inbox link plus hooks.

## Phases

1. **The seam, with no behaviour change.** Create `internal/harnesslink`, move
   the PTY path into its own file, and turn pi's route into the first link.
2. **Codex app-server link (opt-in).** Launch shaping, the adapter, link
   events as evidence, and thread settings.
3. **Claude inbox link (opt-in).**
4. **Retire receipt matching on the PTY route.**
5. **Claude plugin link (opt-in, experimental).**
6. **Later:** content delivery, a distinct `VoiceAttn` rendering, an attn-native
   harness.

Phases 2 and 3 are independent once 1 lands. Phase 4 can follow 1.

## Tests (per docs/testing.md)

- **Wire (Codex).** Teach `internal/fakeagent` the app-server subset (`initialize`,
  `thread/loaded/list`, `thread/resume`, `turn/start`, status and turn
  notifications). Then assert:
  - a doorbell reaches a linked session and writes nothing to its PTY;
  - link events drive session state.
- **Wire (Claude).** A fake Claude binds the inbox socket. Assert that a
  doorbell arrives as one line.
- **Stack.** Restart the daemon with a linked session running. Assert that the
  link reattaches and the next doorbell arrives.
- **Performance.** An idle linked session adds no wakeups.

## Open questions

- **Codex main thread.** How the adapter recognises the TUI's thread among
  side threads. Check `thread/started` for a source or ephemeral marker
  before falling back to "the first non-ephemeral thread".
- **Codex initial prompt.** Is it best passed as argv to `codex --remote`, or as
  the first `turn/start` after attach?
- **Codex app-server lifetime.** Is the per-session app-server best owned by
  the wrapper or by `internal/ptyworker`?
- **Claude inbox rendering.** Spike it against a mock API. Does a raw line wake
  an idle session, and how does the TUI render it?
- **Claude plugin channel.** Does `$.process.spawn` give a writable stdin, or
  does the channel need a held fetch?

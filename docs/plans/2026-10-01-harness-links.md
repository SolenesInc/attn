# Plan: harness links

attn talks to the harnesses it runs through their terminals: it pastes text
into the PTY and reads the screen and window title to guess state. This plan
adds a structured side channel, the **harness link**, that carries what attn
sends and what it needs to know. The PTY goes back to being the user's
terminal: it renders the harness natively and carries the user's keystrokes.
The core speaks one harness-neutral contract; adapters translate it to each
harness's own mechanism; a harness built for attn implements it directly.

Status: plan, revised after a review against the `epic/shared-codex` branch
and the GA Claude Mods API. Step 1 is in progress. Opt-in per harness; the PTY
route stays the default.

## Goals

- attn's own traffic leaves the PTY: rings, crew heartbeats, annotations,
  nudges.
- State comes from the harness, not the screen: turns, approvals, questions,
  interrupts.
- Harness knowledge stays in adapters. `internal/daemon` never learns
  JSON-RPC methods, socket frames or plugin APIs, and never asks "is this
  Codex?".
- No idle cost: no polling, no process per hook. Restarts reattach. Daemon
  code stays portable to Linux.
- The contract can express what today's adapters cannot yet do (attn speaking
  as itself, one harness process carrying many agents) without requiring it.

## Non-goals

- Message content instead of rings: the ring stays content-free and the agent
  pulls with `attn agent inbox`.
- Answering approvals from attn. Copilot, which stays on the PTY route.

## Principles

**Custody, not receipts.** A delivery has one result: the synchronous answer
to "did the harness take custody of this input?" After that attn does nothing
for it: no echo matching, transcript correlation or acknowledgement timers.
A receipt only earns its keep if attn can act on a failure better than the
user can, and it cannot. The user sees what arrived and resends an annotation;
an unread inbox re-rings; a heartbeat not taken into custody is tried again,
and one taken is not repeated for the same cache generation. A harness
mechanism qualifies as a link only if it answers custody synchronously. An
uncertain input is never resent automatically.

**One native conversation is one session.** A terminal shows a session. A new
conversation (Claude `/clear`, Codex `/new`, a fork) opens a new session in
that terminal. Switching to an existing conversation (Claude `/resume`, Codex
`/agents`) makes the terminal show another session. Whether the session left
behind keeps running depends only on the link (see Vocabulary).

**The PTY is the user's terminal.** What a harness can take through a link
stays off it.

## Vocabulary

Reuses the glossary, which defines harness link, voice and custody as of
step 1. "Host" (remote hosts) and "binding" (crew) are not used. The rows
below are the target meanings; Conversation and Terminal enter the glossary
with step 4 and Multi with step 6.

| Term | Meaning |
|---|---|
| Session | The attn agent: ledger entry, inbox address, crew day, usage, name. |
| Conversation | The harness's native id (Claude session id, Codex thread id), held in `sessions.resume_session_id`, unique among open sessions. One conversation, one session. |
| Terminal | A PTY runtime that panes place. Its id is stable; the session it shows can change. A session has 0..n terminals. Kinds: agent terminal, link process, shell. |
| Link | The structured channel to a harness process. It can carry one session or many. It is **Multi** when its conversations stay alive while a terminal switches away (Codex app-server, a fork). |

Step 4 updates the glossary: "Agent conversation" no longer says a session
can start a new conversation; a new conversation is a new session.

## The contract

`internal/harness` imports nothing from `internal/daemon`, `internal/store`
or `internal/protocol`. Step 1 lands only `Voice`, `Input{Session, ID, Text,
Voice}`, `Custody{Taken, Reason}` and `Link{Voices, Deliver}`. Every other
part lands with its first consumer: `Events` with `Turn`, and
`Shape.Provides`, in step 3; the session and terminal ids, `Opened`,
`Shows`, `Up`, `Down` and `End` in step 4; `Custody.At` in step 5; `Shaper`
and the Multi parts with the Codex adapter in step 6; `Placement` with the
first link that places at a turn boundary itself (the Claude mod, step 8).
Until then core's phase gate applies placement before the route choice, and
placement stays daemon-side.

```go
type SessionID, TerminalID, ConversationID string
type Voice uint8     // VoiceUser | VoiceAttn
type Placement uint8 // WhenPromptReady | AtTurnBoundary (today's sessionInputPlacement)

type Input struct {
	ID, Text  string // a link may pass ID on; after step 5 core never matches on it
	Session   SessionID
	Voice     Voice
	Placement Placement
}
type Custody struct { // the only result a delivery has
	Taken  bool
	At     time.Time
	Reason string // when not taken; shown as is
}
type Link interface {
	Voices() []Voice
	Deliver(ctx context.Context, in Input) Custody // any error, timeout or ctx expiry is Taken=false
	End(ctx context.Context, s SessionID) error     // ends the agent, not a terminal
}

// Launch shaping. Launch carries SessionID and TerminalID.
type Shaper interface{ Shape(l Launch) (Shape, error) } // error: plain PTY launch
type Shape struct {
	Argv, Env []string
	Settings  map[string]any // merged into the driver's own settings file, never a second --settings
	Endpoint  string         // stored on the run record; a restarted daemon reattaches here
	Provides  Evidence       // Turns|Approvals|Questions|Names|Conversations guaranteed at launch
	Multi     bool           // conversations stay alive while a terminal switches away
	Titles    bool           // core feeds raw terminal titles to the link
}

// Optional capabilities, found by type assertion. Absent means core's current behaviour.
type Attacher interface{ Attach(ctx context.Context, c []Carried, blobs Blobs) error } // daemon start, after Down
type Carried struct { Session SessionID; Conversation ConversationID; Launch Launch; Terminals []TerminalID }
type Blobs interface{ Get(SessionID) []byte; Put(SessionID, []byte) } // the adapter's private state, kept by core's DB
type TitleObserver interface{ ObserveTitle(t TerminalID, title string, gen uint64, at time.Time) }
type Views interface{ OpenView(ctx context.Context, s SessionID) (Shape, error) } // Multi only
type Names interface{ Rename(ctx context.Context, s SessionID, name string) error } // core skips its titler
type Reconfigure interface{ Reconfigure(ctx context.Context, s SessionID, l Launch) error } // else core respawns
type Transcripts interface{ Transcripts(s SessionID, c ConversationID) []string }

// Events is implemented by core and given to each link at attach. Calls are
// addressed by session; the adapter keeps its own native maps.
type Events interface {
	Up(s SessionID, provides Evidence) // Provides declared at connect
	Down(s SessionID, err error)       // link-wide loss: one Down per carried session
	Opened(from SessionID, t TerminalID, c ConversationID) (SessionID, Launch, func(commit bool), error)
	Shows(t TerminalID, s SessionID, st ViewState) // shown | unknown | detached
	Turn(s SessionID, at time.Time, e TurnEvent)   // Started | Ended{completed|interrupted|failed, answer} | Approval | Question
	Named(s SessionID, name string, rev uint64)    // Names links only
}
```

Core rules. These are the only places harness behaviour reaches core.

1. **Identity.** `Opened` reserves a new session before the harness creates
   the conversation, so env and instructions can be injected; `commit(false)`
   releases it. `Shows` points a terminal at an existing session. On a
   single-session link the session left behind stops and becomes recoverable.
   On a Multi link it keeps running, hidden. Core owns the terminal registry
   and the terminal-to-session map, in memory, behind their own lock.
2. **Route.** A session uses its link once it has come Up in this run, if the
   link supports the voice. Before the first Up it uses the PTY, except on a
   Multi link, which never has a PTY route: deliveries defer. After a Down
   they defer. A session hidden in a Multi link has no terminal, so it defers.
   The run record stores link kind, endpoint and whether the link has been Up,
   so a daemon restart neither strands a never-Up link nor sends a Multi
   session to the PTY.
3. **Delivery.** Phase gate, then the quiet window (on every route for now,
   keyed to the terminals showing the session), then `Deliver`. Taken becomes
   *placed*; not taken becomes *deferred*, and core's existing cadences (ring,
   heartbeat) decide the next try. The call is bounded by
   `pluginDeliverMessageTimeout`; that bound is not an ack timer, and expiry
   means not taken. This is the target. Today the quiet window is checked
   only on the PTY route, as pi's route never had it (see Decisions taken,
   2). Since step 5 nothing follows custody. For auto-settle, a user-voice
   input credits the turn that takes it: the run going, or the next turn
   start when it waits in the composer or no run is going.
4. **Evidence.** Turn events become one `link` evidence source (the epic's
   `NativeRoot`, renamed, with its precedence). One rule: for each kind a link
   provides, link evidence wins while the link is Up; hooks and title signals
   win otherwise. `Shape.Provides` is guaranteed at launch, so core installs
   no hooks for it (Codex). `Up(provides)` is declared at connect, so hooks
   stay installed and are masked while Up (the Claude mod, available only at
   runtime). Hooks the link does not replace stay (transcript path, edits).
5. **Close.** Closing the last terminal that resolvably shows a session ends
   it: drain the transcript watcher, call `End`, close the ledger entry.
   Other terminals showing it are only detached. `End` is refused while any
   terminal on the link is unresolved (`unknown`), because it may be showing
   the session. When a link process exits, the sessions on it become
   recoverable and their usage is marked incomplete.
6. **Approvals.** There is no answer verb: an approval for a hidden session
   is answered by opening a view on it, and an adapter must not swallow the
   request. Stock Codex must replay it to a later view (open questions).

**Wire form.** The same contract is JSON-RPC to a harness that speaks it, on
an endpoint scoped to the link process (`ATTN_LINK_ENDPOINT` plus a token).
Harness to daemon: `link.hello{token, voices, provides, multi}`, `link.opened`
(reply: session id, env, instructions), `link.shows`, `link.turn`,
`link.named`. Daemon to harness: `link.deliver` (reply `{taken, reason}`),
`link.end`, `link.rename`, `link.open_view` (reply: argv), `link.reconfigure`,
`link.attach`. Launch shaping reuses pi's `driver.spawn`. Core spawns one link
process per (driver, executable) and instance in a PTY worker, so a Multi
harness needs only a driver entry. Designed for, deferred (step 8).

## Adapters

| Adapter | Carries | Voices | Custody is | Provides | Multi |
|---|---|---|---|---|---|
| PTY fallback | 1 | both | bytes written and submitted | nothing | no |
| pi | many (one plugin connection) | both | `driver.deliver_message` reply | Turns, Approvals | no |
| Codex shared app-server | many | both | the `turn/start` or `turn/steer` reply | Turns, Approvals, Questions, Names, Conversations | yes |
| Claude inbox | 1 | attn | the socket write | nothing | no |
| Claude mod | 1 | both | `$.prompt.submit` accepted | declared at hello | no |
| attn Codex fork | many | both | `link.deliver` reply | declared at hello | yes |

**PTY fallback.** Not a `Link`: it is what a session uses when no link
takes its voice. Step 1 moved today's path into `session_input_pty.go`
as `sessionInputModule` methods (`placePTYLocked`, `ptySafetyLocked`),
keeping the checks that protect the user. Step 5 retired echo matching:
custody is the paste and Enter written, or Enter held for a prompt that
appeared after the paste. A turn-boundary paste occupies the composer until
the next turn start or the user's typing. When `End` lands in step 4, ending
a PTY session closes its terminal.

**pi.** The attn-pi plugin's `message_delivery` becomes the first link. The
plugin connection is one link carrying every pi session, with `Up(s)` and
`Down(s)` driven by each pi process's suite connecting and disconnecting.
`report_state` and `report_stop` become `Turn` events,
`report_metadata.resume_session_id` becomes `Opened` or `Shows`, and
`report_input_taken` is retired (step 5; the plugin relay still acknowledges
it from suites loaded before an upgrade). `driver.spawn`, `classify_stop`
and the auto-mode and amendment reports stay plugin-driver methods.

**Codex shared app-server (stock).** An in-process adapter in
`internal/harness/codexapp`. One link per (driver, executable) and instance;
its process is a link-process terminal in a PTY worker, so it outlives
terminals and the daemon.
- Shape: `codex --remote <terminal socket> -c tui.terminal_title=["thread-id"]`,
  Multi, Titles.
- Deliver: `turn/start` when idle, `turn/steer` with `expectedTurnId` when
  active; custody is the RPC reply.
- Which thread a terminal shows: the OSC title, fed through `TitleObserver`,
  corroborated by proxied `thread/resume` and thread-scoped writes; the
  adapter emits `Shows`. Lifecycle replies discover threads but do not say
  which one is displayed; an upstream ask to OpenAI for a native signal is
  pending.
- `Opened`: intercepting the TUI's later `thread/start` and `thread/fork`.
  `Attach` re-resumes every carried root with its launch context and holds
  hidden roots. `Down`: loss of the control connection.
- `End`: `thread/archive`. `Views`: `--remote ... resume <root>`. `Names`:
  `thread/name/set`. `Reconfigure`: `thread/resume` with injected config.
  `Transcripts`: the live rollout plus `archived_sessions`.

**Claude inbox** (documented, no mod needed). Shape adds
`--messaging-socket-path <run>/claude.sock` and sets `crossSessionInbound` to
`accept` in the settings file attn already generates; a second `--settings`
would replace attn's hooks. Deliver writes one NDJSON line, `VoiceAttn` only.
Up when the socket is connectable after `SessionStart`. A project or local
`refuse` makes write-custody lossy, so Shape detects it and declines the
route. Provides nothing; hooks stay. Annotations stay on the PTY route.

**Claude mod** (early access API, remote kill switch; off the Codex path).
- Shape adds `--plugin-dir <data>/claude-link/<attn-version>` (an immutable
  copy, so hot reload never fires) plus the endpoint and token, and declines
  when managed settings reject sideloading.
- The mod spawns `attn link relay`. Its stdout carries daemon-to-mod traffic;
  short `$.http.fetch` POSTs carry mod-to-daemon traffic (fetch aborts at
  30 s and spawn has no stdin). Up is the helper's daemon connection.
- Deliver: the mod rejects empty text and a leading `/`, then awaits
  `$.prompt.submit` under the delivery deadline; a `{drop}` or rejection is
  not taken. `VoiceUser` sets `asUser`; `VoiceAttn` omits it, and the model
  reads "The plugin sent a message". `AtTurnBoundary` uses `$.session.append`,
  and submit when the session has gone idle, since append never wakes it.
- Turns come from `turn.start` and `turn.complete`, whose answer feeds the
  classifier. `Up` provides Approvals only if `classic.PermissionRequest` is
  visible: on a managed machine the `sec-default` mod hides classic hooks, so
  hooks stay the approval source. Conversation changes come from
  `session.end(clear|resume)` and a re-read of `$.session.id()`.

**attn Codex fork.** Speaks the wire form: Multi, `link.opened` and
`link.shows` sent directly, no title trick, no proxy. Its driver entry is
argv; core and daemon gain no code for it.

## What stays inside adapters

| Adapter | Private knowledge |
|---|---|
| Codex | JSON-RPC; the websocket proxy and its injection into `thread/start`, `resume` and `fork` (hooks config, trust keys, developer instructions, cwd, model, sandbox); the OSC title override; launch reservations; holding hidden roots; pending initial names; archive and rollout relocation; version assumptions behind a probe |
| Claude | plugin-dir materialization and version gate; the managed-settings check; the leading-`/` guard; submit versus append; the 104-byte socket path limit |
| PTY | selector and approval screen checks, held Enter, composer occupancy |

## Strategy for the epic

`epic/shared-codex` has the right runtime model and a Codex-shaped
abstraction: about 49 sites in 16 generic daemon files branch on the Codex
owner, and Codex names are durable (migration 161, `codex_mode`,
`codex_resolution`) and on the wire. **It is not merged as it stands.**
Releases are cut from `next`, so what lands there is a promise, and renaming
later costs a migration, a protocol bump and a rewrite of those sites. This
rests on that cost and on refactoring first, not on performance.

Refactoring in place on the epic is credible for the Codex runtime itself:
drift is cheap (a trial merge had 7 conflicts) and a re-port of about 4.7k
lines with its crash-point recovery would lose the wire tests that prove them.
So:

1. Land harness-neutral foundations on `next` as small PRs (steps 1 to 5).
   Each has value alone and none carries Codex names.
2. Rebase the epic on `next` and refactor its Codex runtime in place onto
   those seams, under its own wire tests (step 6), then merge once.
3. Multi-terminal identity seams (registry beyond one session per terminal,
   `Shows`, `Views`, `Attach`) land with their first real consumer, in step 6,
   not as dead code ahead of it. Claude work is off this path.

The epic's neutral-verb wire tests (`AskApproval`, `NativeSystemError`,
`DisconnectNativeControl`, `NativeName`) become the link contract suite. These
promises must pass unchanged against the adapter: close crash points, terminal
and session identity, no resend of uncertain input, state precedence, name
authority. Tests asserting `codex_*` names are rewritten, not repaired.

## Phases

Each step is PR-sized unless noted. Verification follows `docs/instances.md`.

| # | Step | Behavior change |
|---|---|---|
| 1 | **In progress.** `internal/harness` with `Voice`, `Input`, `Custody` and `Link`; the PTY path moved unchanged into `session_input_pty.go`; pi's plugin delivery as the first link, chosen per delivery from the session's plugin driver run record and the plugin registry, with the PTY as the fallback when no link takes the voice; any error maps to deferred. Pending candidates, receipt matching and the PTY-only quiet window are unchanged (step 5 retired the receipt matching). | None |
| 2 | The epic's neutral wins, one PR each, parallel to 1: PTY worker teardown hardening (`instance clean` refuses to wipe while a worker survives); `SessionLedgerEntry.usage` with the inspector and `attn session show` lines (protocol bump); the ledger focus-trap fix; a decision on the PTY Backend settings card the epic deleted. | `instance clean` exits non-zero and keeps data when a worker cannot be reaped; ledger and CLI show usage for every harness |
| 3 | `link` evidence source and the precedence rule; pi's `report_state` and `report_stop` move to `Turn`; `Shape.Provides` drops hooks at launch. Needs 1. | None intended; a pi session's state origin reads `link` |
| 4 | The identity rule: terminal registry with ids distinct from session ids; `Opened` and `Shows` for single-session links, with Claude `/clear` and `/resume` and pi as consumers; terminal-to-session resolution for PTY exit, state, input and layout. Existing rows keep `runtime_id` = session id. Daemon and app may split. Needs 1. | Claude `/clear` opens a new session; the old one becomes recoverable |
| 5 | **Done.** Retire receipts on the PTY route: `observePromptTaken` matching, pending candidates, `sessionInputTakenWindow`, `await`, the indeterminate stage, `report_input_taken`. Crew heartbeat records `Custody.At` with no wait; owed input clears on the next turn start. Needs 1. | Heartbeats stop waiting up to 3 s; annotation results are taken or not taken |
| 6 | Rebase the epic; refactor in place (several reviewable commits, one merge): neutral names and migration (see below); Codex into `codexapp`; the capabilities with a consumer (`Attacher`, `TitleObserver`, `Views`, `Names`, `Reconfigure`, `Transcripts`, Multi `Opened` and `Shows`); core owns registry, close rule and attach-view command; app gets 0..n terminals per session; name write failures warn and retry, never blocking delivery; the quiet window keyed to terminals; setting under Experimental; docs and glossary neutral. Needs 1, 3, 4. | Opt-in shared Codex becomes available, default off |
| 7 | Claude inbox link under Experimental. Needs 1; parallel to 4 to 6. | Opted-in Claude rings, heartbeats and nudges leave the PTY |
| 8 | Link wire protocol, `attn link relay`, the Claude mod with its version gate; pi's deliver and state subset moves to `link.*` under an `attn_api_version` bump (migrate attn-pi in the same PR). Deferred. | Opted-in Claude gets both voices and turn state; hooks remain the fallback |
| 9 | Later: content delivery, the fork, a hidden-session budget, removing the quiet window. | |

Step 6 is the long one. It starts when 1, 3 and 4 are merged; steps 2, 5, 7
and 8 do not gate it.

## Decisions taken

1. One native conversation is one session: Claude `/clear` opens a new
   session, as do Codex `/new` and forks; switching to an existing
   conversation makes the terminal show another session. This removes the
   split between single-session and Multi identity.
2. The 30 s quiet window stays on every route for now; on a link it is
   courtesy, since a ring starts a turn that would hijack the user's next
   instruction. Removing it is a follow-up. Today it guards only the PTY
   route; it moves ahead of the route choice when the first new link lands
   (step 6 or 7), which also starts applying it to pi, a behavior change that
   step names.
3. A name write failure warns and retries; it never blocks delivery. The
   epic's first-work name gate goes.
4. Dev instances that ran epic builds are disposable. Wipe them before
   `next`-based builds. Production `~/.attn` is never touched.
5. A Claude link declares what it provides when it connects. Hooks remain the
   approval source wherever the mod cannot see them, as on a managed machine.

## Open questions

- **Roles on a new conversation.** What the new session inherits on `/clear`:
  crew day, seed claims, session-addressed inbox items, an owed turn. Lean:
  the daemon hands them over as it does for a nap. Decide in step 4.
- **Hidden approvals.** Does stock Codex replay an approval raised in a turn
  started over the control connection to a view attached later? If not, add
  an `Answer` capability or never deliver over that connection. Probe first.
- **Proxy ownership.** Should Codex's websocket proxy move from the daemon
  into the link-process worker, so a daemon restart no longer severs views?
- **Hidden-session memory.** Hidden roots stay loaded in the app-server. A
  budget, such as archiving a session hidden for N hours with reopen as the
  reversal, is undecided.
- **Custody deadline.** Is bounding the call by the pi deadline acceptable as
  not being a receipt timer?
- **Claude inbox rendering.** Does a line with priority `next` wake an idle
  session, and how does the TUI render it? Spike against a mock API.

## Durable-promise rules

Nothing from the epic is promised: it is on neither `next` nor `main`.

- **No `codex_*` names** in tables, wire fields, settings, notification kinds
  or state reasons. Wire fields are neutral: `view_state` (an enum documented
  as extensible), `launched_for`, a typed `Session.link`. Generation stays
  daemon-side; `codex_revision` is dropped. Opening a view gets its own
  command, not an overloaded reopen result.
- **Migrations** number from `next` when the step lands (it is at 162, so 163
  or later), with all DDL in migration SQL, never an in-loop `ALTER`. The
  epic's migration 161 (`codex_owners`, `codex_views`, the pane columns) is
  never landed. The conversation id stays in `sessions.resume_session_id`;
  the neutral tables are `terminal_views` and a link kind and endpoint on the
  run record. The adapter's blob uses tagged JSON fields.
- **Protocol** bumps once per step from `next`'s 328; the epic's 329 and 330
  are never reused.
- **Settings:** one family, `link.<harness>` (`link.codex`: `off` |
  `app-server`; `link.claude`: `off` | `inbox` | `mod`), rendered generically
  under Experimental. Running sessions keep the route they started with. The
  launch intent stores one neutral link kind, omitted for PTY sessions.
- **State reasons** are neutral (`link`, `initial_name_failed`). **Files:**
  `<data>/links/<link-id>/rpc.sock` plus short hashed per-terminal sockets,
  under the 104-byte macOS path limit.
- **Changelog fragments** for the `instance clean` exit code and attach-view.
- **Trap.** An instance that ran an epic build has `schema_migrations` at 161
  with the epic's tables. Migrations apply by `MAX(version)`, so a `next`
  build silently skips its own 161 (external wrapper identity). Those data
  dirs are disposable: wipe them before switching.

# Plan: terminals and the identity rule

Step 4 of [harness links](2026-10-01-harness-links.md). Status: plan; PR 1
lands with this document.

## Summary

A terminal is a PTY runtime that a pane places. Its id is fixed when it launches, and it is the id the harness process carries: `ATTN_SESSION_ID`, the hook arguments, and pi's run id. A session is the attn agent: its ledger entry, inbox address, crew day, usage and name.

When the harness in a terminal reports a conversation that is not its session's own, core does one of two things:

- **Opened.** For a new conversation, core opens a new session in that terminal.
- **Shows.** For a conversation another session already holds, the terminal shows that session.

Either way the session that was in the terminal becomes the predecessor of the session now there, its successor. Clearing clears the identity: the successor is a new agent with a new name and nothing attached. The predecessor closes into the ledger whole, keeping its name, conversation, usage, and everything attached to it (crew day aside, §3.4). Reopening it from the ledger brings it back in a new pane with its conversation and attachments, and what queued for it while closed is delivered then.

This changes today's behavior, where `/clear` keeps the attn session and only its conversation id moves.

Step 4 adds:

- no terminal table: panes already persist `runtime_id → session_id`;
- one column, `sessions.succeeds`;
- one protocol bump, 331, which skips the epic's 329 and 330.

New terminals get their own ids from the registry PR onwards, so the existing suites check the identity split before any feature depends on it.

---

## 1. Rejected alternatives

- **A `terminal_views` table.** Pane rows already persist
  `runtime_id → session_id` through the one layout write path.
- **A testworld mode that changes id allocation.** The harness never sets
  attn's own state. New terminals get ids distinct from their sessions in
  production from PR 2, so the existing suites check the split.
- **Resolving every reference through the `succeeds` chain at read time.** It
  would touch every identity comparison. State moves once, at write time, and
  the chain serves only callers naming a stale id.

A remote endpoint needs no home-side handling for PTY routing; only the
references the home holds move, in PR 6.

---

## 2. Decisions

1. **A process speaks as its terminal.** The terminal id is fixed at launch.
   - When `addWorkspaceSessionPane` places a new pane, it allocates a fresh UUID for it. Claude's `--session-id` needs a UUID.
   - Panes from before the upgrade keep `runtime_id = session id`.
   - The env var keeps its name, `ATTN_SESSION_ID`; it now names the terminal.
2. **Panes persist the terminal-to-session map.** The in-memory registry has its own lock. It is rebuilt from pane rows at startup and updated by the one daemon function that saves layouts. All 15 or so callers of `store.SaveWorkspaceLayout` move to that function.
3. **Producers report conversations; core decides by ownership.** Nothing changes on the harness side: no `source` and no plugin API change. Only in-order reports can change identity (§3.2). Spawn persists what it launches.
4. **Succession clears the identity** (§3.4). The successor takes only the terminal; everything else stays with the predecessor.
   - The predecessor is closed through the normal close, with the reason "cleared; its terminal moved on to `<S2>`". Its obligations wait for it as on any close, so Reopen brings them back.
   - `sessions.succeeds` points at it.
   - The successor gets a new name, as any new session in that pane would.
5. **The daemon resolves caller ids.** It tries a terminal, then an open session. A closed session's id is not followed to its successor: a message to a cleared agent is refused as for any closed session, not rerouted to an agent without its context. There is no new CLI command and no extra round trip, and hooks already resolve on the daemon side.
6. **One protocol bump for the step, 331.** Every wire change lands in PR 1.
7. **Codex's `/clear` and `/new` follow the same rule as Claude's `/clear`** (PR 3). Codex reports the new thread on the first turn of the new chat (SessionStart, then UserPromptSubmit), so the pane switches when the user first prompts it. **Unchanged in step 4:** Copilot, shell sessions, and bare-CLI wrapper sessions.

---

## 3. The identity rule

### 3.1 Terminals

```go
// internal/daemon/terminals.go: own RWMutex; readers never touch SQLite
type terminal struct {
	mu       sync.Mutex        // the router holds it across a handover; exit takes it to mark the terminal dead
	shows    harness.SessionID
	live     bool
	lastKey  time.Time         // quiet window, per terminal (plan rule 3)
	launched bool              // set by spawn; reload and exit consult it
}
func (r *terminals) Showing(t harness.TerminalID) (harness.SessionID, bool)
func (r *terminals) Of(s harness.SessionID) []harness.TerminalID       // most recently shown first
func (r *terminals) Primary(s harness.SessionID) (harness.TerminalID, bool) // live, most recent
```

**Lifecycle.**
- Spawn places the terminal before the worker starts. It uses the session's placed terminal if that terminal has no live worker (revive, reload, reopen into its pane). Otherwise it adds a pane.
- Closing a pane closes its terminal. If that was the session's last terminal, the session ends, as today.
- At recovery, every live worker that no pane places is an orphan and is pruned.
- A session is live if any of its terminals is live.

### 3.2 The router

Conversation reports reach the router from three places:

- Claude's and Codex's **SessionStart** and **UserPromptSubmit** hooks. These are synchronous, so the harness's next hook arrives only after the handover commits.
- pi's suite hello, through `Events.Conversation`.
- Stop and transcript reads. These only confirm a conversation and never change identity: a late Stop carrying C1 cannot flap S1 back. `resolveStopTranscriptPath` already rejects foreign paths.

```
conversationIn(t, c, transcript):
  cur, ok := Showing(t)
  if !ok || !participates(cur)  → today's in-session observe; return   // bare CLI, Copilot
  lock lifecycle(cur) → lock terminal(t)                                // fixed order; exit takes only the terminal lock
  if !t.live || !open(cur) || Showing(t) != cur → drop and log; return  // late hook after an exit or close
  switch {
  case conv(cur) == c:   ensure the watcher path                         // startup, compact, reload, reconnect
  case owner(c) == "" && conv(cur) == "":  adopt c     // picker launch, fresh pi, a fresh Codex chat's first turn
  case owner(c) == "":   opened(t, cur, c)
  case !showable(owner): move c within cur           // live in another workspace, or only in a bare-CLI wrapper
  default:               shows(t, cur, owner)        // open owner first, else the most recently closed; a live one keeps its terminals
  }
```

`participates` is true for Claude and Codex, through a driver capability flag, and for plugin drivers with `resume`.

### 3.3 Launches never look like switches

Spawn persists the conversation it launches **before the worker starts**:

- for a fresh Claude, the terminal id it passes as `--session-id`;
- for a resume, the resume id;
- for a picker, none, which clears a stale id.

So every launch report is either a no-op or an adopt. This also covers a reload whose transcript is missing: the fresh launch's id is already the session's, and the picker fallback adopts. If the probe shows that `claude -r` forks the id, spawn persists "adopt the first report" instead. That is the only probe result that changes this design.

### 3.4 Succession: what moves and what stays

The successor takes what places the agent in the user's workspace:

- the pane and terminal, its workspace place, and the driver run (plugin, run id, seq);
- a launch intent of its own: resume = C2, no initial prompt, so a respawn resumes the new conversation.

Everything else stays with the predecessor, closed by the normal close path (`recordSessionClose`): ledger entry and final cost, transcript and watcher (drained first), its launch intent for Reopen, seed tender claims and watches, unread `session:` inbox items, PR watches, satellites, notes, presentations, annotation drafts, and kept-conversation pins.

The crew day is the exception, because the close releases it: a crew member's `/clear` ends its day, and the Chief's ends the Chief role. The successor is unbound. The close guard that refuses closing a bound session from the UI does not apply; the harness already cleared.

What a closed session leaves stranded for the parties waiting on it is today's close behavior and out of scope here.

### 3.5 The handover (opened and shows)

These steps run under `lifecycle(cur)`, then `terminal(t)`, plus `lifecycle(owner)` for shows, with locks taken in id order.

1. Drain the predecessor's transcript watcher, so the final usage is complete, and capture its Garden snapshot.
2. Build the document commits: the crew member doc, the tended seeds, and the dispatch pair. Revisions are checked; a conflict retries the whole handover up to 3 times.
3. Run one transaction: `store.CommitSuccession`.
   - Insert the successor, or reopen the owner in place, with conversation c, the transcript path, and `succeeds = cur`.
   - Give the successor its own intent, move the driver run, and point t's pane rows at the successor (§3.4).
   - Close the predecessor through the normal close.
   - Finalize the cost.
   - Append the facts in the same transaction.
4. Still under the terminal lock:
   - set the registry entry for t to the successor;
   - associate the successor with t's workspace;
   - start the successor's evidence from t's latest PTY observation (so plan-mode "clear and proceed" is not stamped idle), its transcript watcher and cost tracking, and pi's silence watch.
5. Unlock. Then:
   - forget the predecessor's session-keyed runtime state;
   - dissociate it; the workspace keeps the successor, so it is never torn down;
   - publish in this order: `SessionRegistered(successor)` → layout updated → `SessionClosed` and `SessionUnregistered(predecessor)`.

**Shows only.** A closed owner reopens in place; an open one's dead panes are dropped. Either way the owner moves to t's workspace, keeps its own name and launch intent, and gets `succeeds = cur`, so the app follows the terminal. An owner live in another terminal of t's workspace (Codex `/agents`, or `/resume` of a session running elsewhere) keeps it and dead panes alone drop, so it runs in two terminals and t, shown most recently, is its primary. When either terminal later moves on, the session stays open in the other: the succession skips the close and every step 5 does to the predecessor. A session belongs to one workspace, so for an owner live in another workspace, or kept alive only by its bare-CLI wrapper, t's session takes the conversation over in place, as before PR 4.

### 3.6 Resolution by category (PR 2)

| Category | Rule |
|---|---|
| PTY commands from the app and hub (attach, input, resize, snapshot, kitty, pointer, detach) | `msg.ID` is a terminal. Session effects (auto-settle hold, revive) resolve through `Showing`. |
| Backend callbacks (exit, state, recovery, `SessionIDs` loops at `transcript_watcher.go:339`, `automations_*`, `model_capture.go:86`, `ws_pty.go:793`) | Resolve the terminal to its session. `FactSessionPTYExited` has the session as subject and the terminal in its payload. |
| Session to PTY (input lanes, snapshots, build info, crew geometry, kill, reload) | `Primary(s)` and `Of(s)`. Teardown kills `Of(s)`. |
| Hooks (`state`, `stop`, `hook_*`, `files_edited`, `open_sent_files`, `set_session_resume_id`) | Resolve the terminal at dispatch. Conversation reports go to the router. |
| Plugins | `driver.spawn` gets the terminal id. `authorizePluginSessionReport` resolves terminal → session and checks the run. Deliveries address `Primary(s)`, so pi needs no change. |
| Callers (wrapper `SeedReady`, `CrewPrime`, `RegisterAsMember`; agent msg, inbox, close; seed; workflow; handoff; pr; automode) | `d.caller(id)`. |
| Liveness (prune, `alreadyLive`, `sessionHasLiveWorker`, reconcile) | A live terminal shows the session. Reconcile kills only workers that no pane places. |

---

## 4. Contract additions (Go sketch)

```go
// internal/harness/ids.go: harness still imports nothing from daemon, store or protocol
type SessionID string      // the attn agent: ledger entry, inbox address, crew day, usage, name
type TerminalID string     // a PTY runtime a pane places; what ATTN_SESSION_ID carries
type ConversationID string // the harness's native id; sessions.resume_session_id

// internal/harness/events.go
type Events interface {
	Turn(s SessionID, at time.Time, e TurnEvent) // retyped from string
	// Conversation: the harness in t now holds c. Core keeps t on its session, adopts c,
	// shows the session that holds c, or opens a new one.
	Conversation(t TerminalID, c ConversationID)
}
```

These are internal to the daemon; step 6 lifts `opened` and `shows` into `Events` once Codex calls them:

```go
func (d *Daemon) conversationIn(t harness.TerminalID, c harness.ConversationID, transcript string)
func (d *Daemon) opened(t harness.TerminalID, from harness.SessionID, c harness.ConversationID, transcript string) error
func (d *Daemon) shows(t harness.TerminalID, from, to harness.SessionID) error
func (d *Daemon) caller(id string) (harness.SessionID, bool)

// internal/store/succession.go
type Succession struct {
	From, To, Terminal string
	Open   *protocol.Session // the new session; nil when To is reopened or already open
	Launch LaunchIntent      // To's copy of From's intent, with resume = the conversation
	Close  SessionClose      // otherwise
}
func (s *Store) CommitSuccession(sc Succession, docs []DocumentCommit, facts []BusEvent) error
```

The plan lists `Launch`, `commit(bool)`, `ViewState`, `Up`, `Down` and `End` for step 4. They are deferred to step 6, which has their first callers. The end-versus-close split needs no API: a succession close never kills a terminal, and teardown kills `Of(s)`.

---

## 5. Data and protocol

**Migration 163 (PR 3):**
```sql
ALTER TABLE sessions ADD COLUMN succeeds TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_sessions_succeeds ON sessions(succeeds) WHERE succeeds != '';
```

- There is no terminal table, and pane rows are unchanged. A database from before the upgrade needs no rewrite, and an older daemon can still read it.
- `sessions.agent_driver_*` stays on the session row and moves in the identity transaction.

**Protocol 331 (PR 1).** This number skips the epic's 329 and 330, as the plan's durable-promise rule requires.

- `SessionExitedMessage.session_id: string`: the session the terminal showed when it exited.
- `Session.succeeds?: string`: the session this one replaced in its terminal.
  - The app uses it, with the layout, to move navigation to the successor.
  - The home mirror uses it to move the references it holds (PR 6).
- No shape change elsewhere: the `id` of `attach_session`, `detach_session`, `pty_input`, `attach_result`, `pty_desync`, `pty_resized` and `runtime_respawned` is now documented as the terminal (runtime) id.

**Docs.**
- PR 2: in `CLAUDE.md`, the PTY log is `<terminal>.log`, the pane's `runtime_id`.
- PR 3, glossary:
  - Agent conversation: "one conversation is one session; a new conversation opens a new session in the same terminal; switching shows the session that holds it".
  - New entries: Terminal and Successor. Restart request already uses "successor" for the next crew day, so scope the new entry or reword that one.
  - Closed session: add "or whose terminal moved on to another conversation".

---

## 6. Behavior per harness

| Harness | New conversation | Switch to an existing conversation | Launch, compact, reconnect |
|---|---|---|---|
| Claude, attn PTY | `/clear` and plan-mode "clear and proceed" → Opened; S2's state comes from the hooks that follow | `/resume` → Shows (reopens a closed owner in place, takes a recoverable one's place, or shows one running in another pane of the workspace in this terminal too). Unknown to attn → Opened, which imports it | No-op. `-r` picker → adopt |
| pi | `/new`, fork → Opened | `/resume` → Shows, or Opened if unknown | Reconnect and reload restate the same id → no-op. Fresh → adopt |
| Codex, attn PTY | `/clear` and `/new` → Opened, on the new chat's first turn | `/resume` and `/agents` → Shows, as for Claude, once Codex reports the thread (at the latest, on its first prompt). A thread no session holds → Opened | No-op |
| Copilot | Unchanged: launch claim plus transcript binding | | |
| Shell | No conversation | | |
| Bare-CLI wrapper | Unchanged: it is not a registered terminal, so today's observe applies (PR 7) | | |
| Endpoint (remote) daemon | Same as local, on the endpoint. The home follows `succeeds` for the references it holds (PR 6) | | |

---

## 7. PR breakdown

**Garden plot:** "Step 4: terminals and the identity rule". The plan goes in the body, PRs 0–7 as children, with `blocks` edges 0→3, 0→4, 1→2, 2→3, 3→4, 3→5, 3→6, 3→7. PR 0 can run as a parallel delegation alongside PR 1, and PRs 4, 5 and 6 can run in parallel after PR 3.

| # | Scope | Behavior change | Tests (and the promise each guards) | Verification |
|---|---|---|---|---|
| **0** | **Probe real Claude** (spike, not committed). Hook inputs for `/clear`, `/resume <id>` and the picker, plan-mode clear-and-proceed, `/compact`, `claude -r <id>` (same id or fork), `--session-id`. Does UserPromptSubmit follow clear-and-proceed? Findings go in the plot and shape the fake agent in PRs 3 and 4. | None | None | n/a |
| **1** | **refactor(protocol, app): the view follows the terminal.**<br>• Protocol 331 (§5); the daemon fills `session_id`.<br>• App runtime lifecycle is keyed only by layouts and PTY events: drop `clearRuntime(session.id)` on unregister and the `runtime_id === sessionID` invalidation; keep the prune backstop.<br>• The exit handler and reload marker use `session_id`.<br>• A session that `succeeds` another takes over its active selection, recents, history, focus request and sidebar position.<br>• Audit app PTY calls for session-id keys. | None (the daemon still sends equal ids and no `succeeds`) | App wire, acting as the daemon:<br>(a) pane R moves S1→S2 with `succeeds`, then S1 unregisters → no detach or attach for R, keys reach R, S2 is active and in recents, queue mode does not advance (screen and keyboard);<br>(b) a clean exit `{id: R, session_id: S}` closes S (lifecycle);<br>(c) after a reconnect, the app does not reattach a terminal whose pane and session ended while it was away (memory). | App tests and packaged-app CI green. No recording. |
| **2** | **refactor(daemon): terminals are not sessions.**<br>• `harness` id types, with ptybackend taking `TerminalID`.<br>• The registry and one layout-save function.<br>• New panes get fresh terminal ids; spawn places the terminal first and persists the conversation it launches before the worker starts.<br>• §3.6 by category, and `caller()`.<br>• Quiet window per terminal; pending queue by terminal.<br>• Reconcile, prune, `alreadyLive`, reload and reopen go through the registry; reopen picks a fresh pane id when `pane-<id>` is taken.<br>• testworld resolves a session's terminal from the layout on the wire.<br>• `CLAUDE.md` diagnostics. | None that users see. For new terminals, `ATTN_SESSION_ID` and PTY log names now carry the terminal id. | • The existing wire and stack suites, now running with T ≠ S. Tests that assumed `runtime_id == session_id` depended on internals and are replaced or deleted.<br>• **Stack:** a CLI call carrying a terminal's id speaks as the session it shows (a CLI promise agents rely on).<br>• The historical PTY upgrade stack test stays green: terminals from before the upgrade keep `runtime_id = session id`. | Packaged-app CI green: PTY and lifecycle scenarios run on distinct ids. Linux cross-compile. No new goroutines or timers. |
| **3** | **feat: `/clear` (Claude, Codex) and `/new` (Codex) open a new session.**<br>• The router's no-op, adopt and Opened branches, for Claude and Codex. A conversation another session holds keeps today's in-session move until PR 4.<br>• Migration 163, `CommitSuccession` (§3.4), removal of an empty predecessor, `succeeds` on the wire, the glossary.<br>• The fake Claude matches the probe; the fake Codex `/new` reports the new thread on its next turn. | `/clear` and `/new` give a new session with a new name in the same pane. The old one is a closed ledger entry that keeps its attachments, and Reopen brings it back. | **Wire:**<br>(1) the same pane and terminal show S2 (`succeeds` S1) under a new name; S1 is closed with its usage; typing and state follow S2; reopening S1 resumes C1 in a new pane while T keeps S2, and T's exit makes S2, not S1, recoverable;<br>(2) a crew member's `/clear` ends its day: S2 is unbound;<br>(3) a delegate tending a seed `/clear`s → S1 still tends it, `attn agent msg <S1>` is refused as closed, and reopening S1 delivers what queued for it;<br>(4) after `w.restart()`, T shows S2 and a reply sets S2's state (durability);<br>(5) two `/clear`s in a row leave one closed row;<br>(6) Codex `/new` opens S2 on the new chat's first prompt.<br>**Rewritten** (their promise changed): `TestARespawnResumesTheConversationClaudeStartedWithClear`, `TestALineAboutAClearedConversationNeverLandsOnTheNewOne`, the `/clear` cases in `conversation_ledger_wire_test`, and the Codex `/new` test. | New packaged-app scenario: `/clear` in a focused Claude pane keeps focus and the terminal, the sidebar row is replaced in place, typing continues, and the ledger lists the old session. **Recording.** |
| **4** | **feat: `/resume` shows the session that holds the conversation** (Claude and Codex; the router is harness-neutral).<br>• `shows()`: a closed owner reopens in place; an open owner with no live terminal moves in and its dead panes drop; an owner live in another terminal keeps it, and the conversation moves within t's session as before.<br>• `opened` and `shows` share one handover; `CommitSuccession` inserts a new successor or takes over an existing one, saving every changed layout in the same transaction.<br>• Fake Claude `/resume <id>` as the probe found it; fake Codex `/resume <id>` reports on the next turn. | `/resume` switches the pane to the session that holds that conversation, under its own name; the session left behind closes into the ledger. | **Wire:**<br>(1) `/clear` then `/resume` back reopens S1 in the same terminal and closes S2, leaving only S1 open; the app hears registered → layout → closed → unregistered, so it follows the terminal; typing, state and usage follow S1;<br>(2) `/resume` to a recoverable session's conversation moves that session into this pane, drops its dead pane, and typing and state follow it;<br>(3) Codex `/resume` reopens the closed owner on the next prompt. | No packaged-app scenario or recording (decided); the app follows the terminal with no change. |
| **5** | **feat: pi `/new`, fork and `/resume` through the rule.**<br>• `Events.Conversation` from pi's hello, applied before `ApplyAgentDriverMetadata`.<br>• The run moves, and the silence watch restarts on the successor.<br>• Fake pi `/new` and `/resume`. | pi's new conversations open sessions. | **Wire:**<br>(1) pi `/new` → S2: a state report sets S2's state, a ring to S2 is delivered over the plugin link, and reopening S1 relaunches pi on C1;<br>(2) `/resume` to a closed owner reopens it in place;<br>(3) a reconnect that restates the conversation opens nothing. | Packaged-app CI green. No recording (same screens as PR 3). |
| **6** | **feat: the home follows successions on endpoints.**<br>• A mirrored session with `succeeds = X`, where X is no longer mirrored, makes the home move the references it holds from X: Chief, remote Garden executions, inbox.<br>• This happens once per pair per run, is idempotent, and starts through `d.life`.<br>• Harness: a local fake `ssh` that runs the endpoint daemon (it models the remote machine). | A remote Claude's `/clear` keeps home-held roles and claims. | **Stack:** a remote Chief `/clear`s → the home's Chief is the successor; a seed dispatched to an endpoint follows `/clear`. | Linux CI stack (see Q6). |
| **7** | **Follow-up: bare-CLI wrapper sessions** (Q7). | | | |

**Experience testing with Victor** comes after PR 4, and again for pi after PR 5. Prepare an instance from the branch with realistic data, and check:

- the latency of `/clear` (the hook round trip) and that the pane does not flicker;
- focus and keyboard continuity, and the sidebar row staying in place;
- `/resume` back and forth;
- reopening the old session from the ledger while the new one runs;
- queue mode right after `/clear`;
- `/clear` by a crew member and by the Chief, which ends the day;
- plan-mode clear-and-proceed.

---

## 8. Open questions for Victor (each with a proposed default)

1. **Closed or recoverable for the session left behind?** **Decided: closed into the ledger**, with a new name for the successor. Reopen brings it back.
2. **What follows a new conversation?** **Decided: nothing but the terminal.** Clearing clears the identity; the agent after `/clear` knows nothing of the old one's obligations, so they stay with the closed predecessor and come back with it on Reopen. A crew member's `/clear` ends its day.
3. **No `terminal_views` in step 4.** The plan names it as the neutral table. **Default: panes persist the map.** `terminal_views` lands in step 6, when a terminal needs state a pane cannot hold: resolution, raw title, generation.
4. **Contract.** **Default:** `Events.Conversation(t, c)` plus typed ids in step 4. `Opened`, `Shows`, `Up`, `Down` and `End` move to step 6, which has their callers.
5. **Protocol.** **Default: one bump to 331 for the whole step, with every wire change in PR 1.** `succeeds` lands one PR ahead of its daemon producer but has a tested app consumer.
6. **Remote verification and release gating.** **Default:** PR 6 builds the two-daemon stack with a fake `ssh`, and lands before the first release cut after PR 3. If the hub's bootstrap makes the harness too costly, verify on a real Linux endpoint and ask before merging.
7. **Bare-CLI wrapper sessions.** **Default: a follow-up after step 4.** Until then they keep moving the conversation within the session. The terminal mapping rides on the `external_process` receipt that already persists, so no table is needed.
8. **Codex `/new`.** **Decided: same as `/clear`, in PR 3.**
## Probe findings (real Claude Code 2.1.288)

Observed with a mock API; these shape the router and the fake agent in PRs 3 and 4.

| Action | Hooks, in order | Conversation id |
|---|---|---|
| Launch with `--session-id <uuid>` | SessionStart `startup` | that id; no transcript until the first prompt |
| `/clear` | SessionEnd `clear`, SessionStart `clear` | new |
| `/resume <id>` or the picker | SessionEnd `resume`, SessionStart `resume` | the target; same id twice means no-op |
| `/branch` | SessionEnd `resume`, SessionStart `fork` | new: route like `/clear` |
| `/compact` | PreCompact, SessionStart `compact`, PostCompact | unchanged |
| `claude -r <id>`, `--continue` | SessionStart `resume` | unchanged; `--fork-session` gives `fork` and a new id |
| `/rewind` | none | unchanged |

- Identity comes from SessionStart only. SessionStart blocks Claude until the hook exits; SessionEnd is cut off after about 1.5 s and may never arrive.
- Plan mode's "clear context and proceed" is SessionEnd `clear`, SessionStart `clear`, then a turn and Stop on the new id with no UserPromptSubmit, so the successor's turn must open from evidence other than that hook.
- The transcript path is `<config>/projects/<cwd-slug>/<id>.jsonl` and changes exactly when the id does.

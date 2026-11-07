# Daemon wire tests

A daemon wire test drives a real daemon as a protocol client.
[Testing](testing.md) is the strategy around it, and `internal/daemon/AGENTS.md`
says which app flow such a test follows.

Daemon wire tests live in `package daemon_test` in `internal/daemon`, so they
can reach the daemon only through the protocol, the CLI client, and the hooks
`export_test.go` exposes. Those hooks make the daemon's external dependencies fail.
The one way to reach inside is a pause point, below.

## The world

- `newWorld(t)` runs a production daemon in its own data directory.
  `w.restart()` replaces it with a new daemon over the same data.
  `newWorldInLongestDataDir(t)` does the same in a data directory as long as
  the world's own sockets allow, for promises about socket paths.
- `w.App()` connects as the app. `w.Client()` returns a CLI client.
- `w.PauseAt(name)` holds the daemon the first time each daemon run reaches
  the pause point `name`. `w.PauseEvery(name)` holds it every time. Both return
  the same `testworld.Pause` that a stack test gets.
- A test can arm a point at any time after the world starts. To arm a point for
  a step the daemon takes on startup, call `inBubbleHeldAt(t, points, script)`,
  which arms the points before a bubbled world's daemon starts.
- `Await` fails the test if nothing arrives within `fakeagent.HangGuard`.
  Inside a bubble, that limit is bubble time. When the step of a
  `pausepoint.Done` point failed, `Await` fails at once with the step's error.
- Teardown and `w.restart()` release every hold before the daemon stops.
- Points match by name. Arm a point only while one operation of its kind is in
  flight.
- Only one world in a test binary can hold points at a time.
- `testworld.Await`, `testworld.Request` and `testworld.AwaitSession` read what
  the daemon sends.

## Agents

- Name the agents the test spawns, as in `newWorld(t, fakeagent.Claude, ...)`.
  The test then plays the model behind each one.
- `w.Spawn` starts a session. `w.Launched` returns the `fakeagent.Run` for its
  agent, once the agent's terminal is raw and reads input. Call it before typing
  at a new agent: a line typed earlier does not submit to Claude, as
  `docs/internal/harnesses/claude.md` says.
  Before delivering to a new agent, await its session's `idle` state, not text
  on its screen. attn holds deliveries until the harness reports, and on the
  shared host that report can reach the daemon a frame after the screen. A
  delivery in that gap returns `skipped_pending_approval`.
- `w.RequestSpawn` sends the same requests and returns the spawn result with
  the desktop and tile it added, so the test can close a refused spawn's
  tile the way the app does.
- `w.HoldNextBoot()` holds the next agent at the start of its boot, before
  the agent paints its resting title or reads input. It returns a
  `testworld.Pause`: `Await` returns once the agent is held, and `Release`
  lets the agent boot. A boot hold that no agent reaches fails the test at
  teardown.
- `w.ExitAtNextBoot(code, screen)` makes the next agent print `screen` and
  exit with `code` before it starts, the way a harness that cannot start does.
- `w.RefusePlugLaunches(reason)` makes the plugin driver refuse every launch and
  resume with `reason` until the test calls the returned function. Setting
  `fakeagent.PlugCapabilitiesEnv` (such as `launch_instructions,resume=false`)
  before the world starts changes the capabilities the plugin driver registers.
  `fakeagent.PlugAgentEnv` registers the driver under another harness name, and
  `fakeagent.PlugModelsEnv` holds the catalog JSON it answers `driver.models` with.

## Playing the model

On a `fakeagent.Run`:

- `Prompted` returns the prompt the agent received and moves `ConversationID`
  to the agent's current conversation. In Claude, `/clear` starts a new
  conversation and `/resume <id>` switches to the conversation `<id>`. Codex
  switches on the first prompt after `/new`, `/clear` or `/resume <id>`. In the
  plugin harness `plug`, `/new` and `/resume <id>` switch conversations, and the
  harness says hello to its driver again.
- `Reply` ends the turn with text carrying the `<!-- attn:state=... -->`
  marker. `ReplyAfterStop` writes that reply only after the Stop hook.
- `Exit` quits with an exit code.
- `StopReadingTerminal` stops reading input, so what the daemon types backs
  up in the terminal the way it does for a frozen agent.
- `Halt` writes the harness's own record of the user interrupting the turn
  (Claude and Codex).
- Claude only: `Stream` writes part of a reply that the next `Reply` revises
  under the same message. `Subagent` writes a subagent's transcript, and
  `DeleteSubagentTranscripts` removes those transcripts.
  A bare `-r` makes Claude open its resume picker. The picker starts a new
  conversation and sets `ResumePicker` on the `Run`.
- Claude launched with `--plugin-dir` also runs a fake mod that polls the
  daemon's mod socket, as a session with `claude_mod_enabled` on does. After
  `Prompted`, `ThroughMod` says whether the prompt came through the mod.
  `ModNext` returns what the mod did next with a message it could not submit:
  held it, refused it as stopped, or lost its poll to a newer one.
  `ModToolBoundary` appends what it holds, `ModEsc` stops the session and returns
  what it held, `ModReload` draws a new instance id, and `ModStopPolling` stops
  its polls. Wait for `daemon.ClaudeModPollHoldTimer` to know a poll is open.
  The mod answers a compact request with `busy` while a turn runs, and
  otherwise with `compacted` after Claude's PreCompact, SessionStart and
  PostCompact hooks. `ModSkipNextCompact`, `ModFailNextCompact` and
  `ModBusyNextCompact` script the next answer, `ModHoldNextCompact` holds it
  (`arrived` returns once the compact is under way, `release` lets it finish),
  `ModCompactRequests` counts the requests, and `ModNextCompactReport` returns
  the next answer once the daemon has accepted it.
- Headless tasks, the one-shot model calls such as titles and turn verdicts,
  are off unless the test sets `ATTN_HEADLESS_TASKS=on`. When they are on,
  `w.HeadlessTask()` returns the next task. The task carries its `Harness`,
  `Model`, `Effort`, and `Prompt`, and the `Argv`, `Env`, and working `Dir` the
  harness process started with. The test answers the task with `Answer` or
  `Fail`. A task the test never takes fails the test.

## Waiting for results

- With a Stop hook (Claude and Codex), `Reply` and `ReplyAfterStop` return
  once the daemon has recorded the turn's end. Every other write returns once
  written, and the daemon reads the transcript independently.
- Either way, await the resulting event on a peer that connected before the
  call. A peer that connects later sees the result in its initial state, and the
  peer cannot await that state.
- `testworld.AwaitSession` also accepts an update from before the call that
  the peer has not read yet. To wait for a state that follows a state the
  test already saw, use `testworld.AwaitStateAfter`.

## Timers

- For behavior on a timer, write the test as
  `inBubble(t, func(t *testing.T, w *world) {...})`, which runs the world
  under `synctest`, and move the clock with `w.advance(d)`.
- A world from `inBubble` cannot run agents or watch folders. Every world sets
  its notebook root to `<w.Dir>/notebook` and does not create the folder. A
  test that needs the folder creates it, as `inCrewBubble` does for the crew
  homes.
- A world outside a bubble gives its daemon a stepped clock instead. Timers on
  that clock fire only when the test calls `w.advance(d)`. Examples are a
  terminal's kill escalation, a folder watch's window, the background job
  runner's schedule, and the daemon's database backup. `w.awaitTimer(name)` waits until the timer with that
  `stepclock.Name`, such as `notebook.WatchDebounceTimer`, is armed. Each
  package that arms a stepped timer gives the timer a `stepclock.Name`, so a
  wait never matches another operation's timer of the same duration.
- Transcript watchers poll on that clock too. Whenever a fake agent writes a
  transcript, the poll (`sessionmonitor.TranscriptPoll`) fires without moving
  the clock. The poll fires before the write returns to the agent, so the
  daemon reads the transcript with no advance. A test that writes a transcript
  itself calls `w.readTranscripts()`. The discovery grace and the quiet window still depend
  on time. When a test depends on the discovery grace or the quiet window, the
  test advances the clock past that duration, for example with
  `w.advance(sessionmonitor.TranscriptDiscoveryGrace)`.
- A test about outside edits to watched folders calls `w.closeWatchWindows()`,
  which closes each folder watch's window as soon as it opens.
- A test about scheduled automations calls `w.tickSchedulesUntil(what, done)`.
  `w.tickSchedulesUntil` repeats a tick until `done` returns true. Each tick
  waits until the job runner is idle, moves the clock one schedule tick, and
  waits until the runner is idle again, so `done` reads settled state. It fails
  after `scheduleTickLimit` ticks.
- `inBubbleWithAgents` gives the world fake terminals, `w.terms`. A terminal's
  `WorkerReportsItsProcessRunning()` sends what a PTY worker sends once the
  daemon watches it, before the agent has started. Each hold on a terminal
  returns a `testworld.Pause`. `Await` returns once the daemon has
  reached the hold, and `Release` lets the daemon go on. A hold the daemon
  never reaches fails the test at teardown.
  - `Stall(alive, err)` makes the terminal's worker stop answering. Recovery
    then waits out the worker's deadline and counts the terminal missing, and
    the liveness probe answers `alive, err`. The daemon reaches the hold at the
    first liveness probe, and releasing the hold makes the worker answer again.
  - `HoldRemoval()` stops the next removal halfway. At that point the
    terminal's streams have ended and its worker no longer answers, but the
    backend has not forgotten the terminal yet.
  - `HoldExitDelivery()` holds the report of the terminal's exit to the
    daemon, for a test about an exit that arrives late.

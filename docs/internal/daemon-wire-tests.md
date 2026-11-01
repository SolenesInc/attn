# Daemon wire tests

A daemon wire test drives a real daemon as a protocol client.
[Testing](testing.md) is the strategy around it, and `internal/daemon/AGENTS.md`
says which app flow such a test follows.

Daemon wire tests live in `package daemon_test` in `internal/daemon`, so they
can reach the daemon only through the protocol, the CLI client, and the hooks
`export_test.go` exposes. Those hooks make the things the daemon depends on outside itself fail.
The one way to reach inside is a pause point, below.

## The world

- `newWorld(t)` runs a production daemon in its own data directory.
  `w.restart()` replaces it with a new daemon over the same data.
  `newWorldInLongestDataDir(t)` does the same in a data directory as long as
  the world's own sockets allow, for promises about socket paths.
- `w.App()` connects as the app. `w.Client()` returns a CLI client.
- `w.PauseAt(name)` holds the daemon the first time each daemon run reaches
  the pause point `name`, and `w.PauseEvery(name)` holds it every time. Both
  return the `testworld.Pause` a stack test gets. Any world can arm a point after
  the world starts. `inBubbleHeldAt(t, points, script)` arms points before a
  bubbled world's daemon starts, for a step the daemon takes on startup. `Await`
  fails the test if nothing arrives within `fakeagent.HangGuard` (bubble time
  inside a bubble). It fails at once with the step's error when a `Done` point's
  step failed. Teardown and `w.restart()` release every hold before the daemon
  stops. Points match by name, so arm a point only while a single operation of
  its kind is in flight. One world per test binary can hold points at a time.
- `testworld.Await`, `testworld.Request` and `testworld.AwaitSession` read what
  the daemon sends.

## Agents

- Name the agents the test spawns, as in `newWorld(t, fakeagent.Claude, ...)`.
  The test then plays the model behind each one.
- `w.Spawn` starts a session. `w.Launched` returns the `fakeagent.Run` for its
  agent.
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
- `w.RefusePiLaunches(reason)` makes the Pi driver refuse every launch and
  resume with `reason` until the test calls the returned function. Setting
  `fakeagent.PiCapabilitiesEnv` (such as `launch_instructions,resume=false`)
  before the world starts changes the capabilities the Pi driver registers.
  `fakeagent.PiAgentEnv` registers the driver under another harness name, and
  `fakeagent.PiModelsEnv` holds the catalog JSON it answers `driver.models` with.

## Playing the model

On a `fakeagent.Run`:

- `Prompted` returns the prompt the agent received and moves `ConversationID`
  to the agent's current conversation. In Claude, `/clear` starts a new
  conversation and `/resume <id>` switches to the conversation `<id>`. Codex
  switches on the first prompt after `/new`, `/clear` or `/resume <id>`. In Pi,
  `/new` and `/resume <id>` switch conversations, and Pi sends its hello to the
  driver again, as a reloaded suite does.
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
  A Claude launched with a bare `-r` opens its resume picker, which starts a
  new conversation and sets `ResumePicker` on its `Run`.
- Headless tasks, the one-shot model calls such as titles and turn verdicts,
  are off unless the test sets `ATTN_HEADLESS_TASKS=on`. Then `w.HeadlessTask()`
  returns the next task's `Harness`, `Model`, `Effort` and `Prompt` to `Answer`
  or `Fail`, and a task the test never takes fails the test. The task also carries
  the `Argv`, `Env` and working `Dir` the harness process was started with.

## Waiting for results

- With a Stop hook (Claude and Codex), `Reply` and `ReplyAfterStop` return
  once the daemon has recorded the turn's end. Every other write returns once
  written, and the daemon reads the transcript on its own.
- Either way, await the resulting event on a peer that connected before the
  call. A peer that connects later sees the result in its initial state, and the
  peer cannot await that state.
- `testworld.AwaitSession` also accepts an update from before the call that
  the peer has not read yet. To wait for a state that follows one the test
  already saw, use `testworld.AwaitStateAfter`.

## Timers

- For behavior on a timer, write the test as
  `inBubble(t, func(t *testing.T, w *world) {...})`, which runs the world
  under `synctest`, and move the clock with `w.advance(d)`.
- A world from `inBubble` cannot run agents or watch folders. Its notebook at
  `<w.Dir>/notebook` exists only if a test outside a bubble creates it.
- A world outside a bubble gives its daemon a stepped clock instead. Timers on
  it, such as a terminal's kill escalation, a folder watch's window, the
  background job runner's schedule and the daemon's database backup, fire
  only when the test calls
  `w.advance(d)`. `w.awaitTimer(name)` first waits until the timer with that
  `stepclock.Name`, such as `notebook.WatchDebounceTimer`, is armed. The package
  that arms each stepped timer names it, so a wait never matches another
  operation's timer of the same duration.
- Transcript watchers, including a driver plugin's usage watcher, poll on
  that clock too. Whenever a fake agent writes a transcript, the poll
  (`sessionmonitor.TranscriptPoll`) fires without moving the clock. The poll
  fires before the write returns to the agent, so the daemon reads the
  transcript with no advance. A test that writes a transcript itself calls
  `w.readTranscripts()`. The discovery grace and the quiet window still depend
  on time. A test that depends on either advances past it, such as
  `w.advance(sessionmonitor.TranscriptDiscoveryGrace)`.
- A test about outside edits to watched folders calls `w.closeWatchWindows()`,
  which closes each folder watch's window as soon as it opens.
- A test about scheduled automations calls `w.tickSchedulesUntil(what, done)`.
  On each tick, `w.tickSchedulesUntil` waits until the job runner is idle, moves
  the clock one schedule tick, and waits until the runner is idle again, so
  `done` reads settled state. It fails after `scheduleTickLimit` ticks.
- `inBubbleWithAgents` gives the world fake terminals, `w.terms`. Each hold on
  a terminal returns a `testworld.Pause`. `Await` returns once the daemon has
  reached the hold, and `Release` lets the daemon go on. A hold the daemon
  never reaches fails the test at teardown.
  - `Stall(alive, err)` makes the terminal's worker stop answering. Recovery
    then waits out the worker's deadline and counts the terminal missing, and
    the liveness probe answers `alive, err`. The pause arrives at the first
    liveness probe, and releasing the pause makes the worker answer again.
  - `HoldRemoval()` stops the next removal halfway. At that point the
    terminal's streams have ended and its worker no longer answers, but the
    backend has not forgotten the terminal yet.
  - `HoldExitDelivery()` holds the report of the terminal's exit to the
    daemon, for a test about an exit that arrives late.

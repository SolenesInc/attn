# CLI stack tests

A CLI stack test runs the built `attn` binary and its daemon as separate
processes. [Testing](testing.md) is the strategy around it.

CLI stack tests live in `package main_test` in `cmd/attn`, whose `TestMain`
returns `testworld.Main(m)`.

- `testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude, ...))`
  prepares a data directory for the built `attn` binary.
- `s.Start()` runs `attn daemon` and returns once it signals ready. `s.Stop()`
  ends it. A `Start` after `Stop` restarts over the same data.
  The stack holds its WebSocket listener until cleanup, including while the
  daemon is stopped, so another process cannot take its address.
- The historical PTY upgrade test uses port `0`. Older binaries cannot inherit
  that listener. After the daemon's bind-ready log, `lsof` finds the assigned
  port of the captured PID, without releasing a reservation or retrying startup.
- For a promise about a moment between two steps of one operation, call
  `s.PauseAt(point)` to hold the daemon or a worker the first time it reaches a
  pause point named in its code (`internal/pausepoint`, `ATTN_PAUSE_AT`).
  `s.PauseEvery` holds the process each time. Arm the pause before `Start`, or
  start with `s.StartHeldAt(pause)`. `Await` returns the held process's pid and
  `Release` lets the process go. This is the same `testworld.Pause` a daemon
  wire test gets. Its `Await` fails at once with the step's error when the step
  of a `Done` point failed. `s.Stop()` releases every hold before the daemon
  stops, and the next start holds afresh.
- For a promise about a crash mid-operation, `s.StartCrashingAt(point)` runs
  a daemon that kills itself with SIGKILL at a crash point named in the
  daemon (`ATTN_CRASH_AT`). `s.AwaitCrash()` returns once the daemon has
  crashed, and a later `Start` recovers from the state the real daemon left
  behind. The crash is immediate. To delay it, arm `s.PauseAt(point)` with the
  same name before starting. The daemon then holds at the crash point and dies
  only when the test releases it, so a request still in flight, such as a hook
  waiting for its reply, can first get its answer. `AwaitCrash` never releases
  that hold. A later `Start` without the crash does not stop at that point.
- The world helpers from daemon wire tests work here too.
- `s.Attn(args...)` runs a CLI command to completion. `s.Run` takes an
  `Invocation` when the command needs stdin, a session, extra env, or another
  binary.
- `s.Launch` starts a command that waits on something the test does next,
  such as a long-running watch or a request the test answers as the app.
  Await its output with `AwaitStdout` or `AwaitStderr`, or its result with
  `Wait`. The stack interrupts it at cleanup.
- `s.Advance(d)` moves the stepped clock of that daemon and of every PTY worker
  the daemon started. The clock drives the bridge's stall, loss, retry, and
  dial-answer timers, a terminal's kill escalation, a folder watch's window, the
  background job runner's schedule, the transcript watchers' discovery grace and
  quiet window, and the daemon's periodic loops: database backup, GitHub hosts
  refresh, PR poll, and branch monitor. Each transcript write a fake agent
  makes fires the transcript poll without moving the clock, so the daemon
  reads the write with no advance. Every stack uses this stepped clock. The
  shared PTY host is not Go and keeps real time. A test that waits for one of
  those timers without advancing fails at the hang guard.
  Heartbeats and kernel socket deadlines keep real time, so a dial may still
  fail without an advance. Await the connected or stalled row before you
  advance the liveness timers.
- `s.AwaitTimer(name)` waits until the timer with that `stepclock.Name`, such
  as `bridge.RetryTimer` or `pty.KillDeadlineTimer`, is pending in the daemon or
  one of its workers. The timer must have been armed or reset after the last
  `Advance` or `AwaitTimer`. Use `AwaitTimer` before you advance an asynchronous
  dial, retry, or kill, because the row or the close can arrive before the timer
  is armed. A failure names the requested timer and the pending timers, with
  their durations.
- `s.CloseWatchWindows()` closes each folder watch's window as soon as the
  window opens, until the test calls the returned function. Each close moves the
  whole clock, the bridge's timers included, so use it only around one wait for
  a watched change.
- To step PTY workers that a test starts without a stack, use
  `testworld.StepChildren(t)`. Its `Advance` and `AwaitTimer` work the same
  way. The test process itself keeps real time.

If the daemon misses its ready guard or `s.Run` exceeds its hang guard, the
stack sends SIGQUIT to capture all goroutines before terminating the process.
Each CLI invocation has its own process group, so a shell wrapper and its Go
children receive the same signal.
Failure logs include CPU and I/O pressure from `/proc/pressure` when available.
The stack keeps the beginning and end of an oversized diagnostic stream and
reports the byte limit, the original size, and the omitted bytes.

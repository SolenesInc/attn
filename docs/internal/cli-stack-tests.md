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
- The historical PTY upgrade test uses port `0`: older binaries cannot inherit
  that listener. After the daemon's bind-ready log, `lsof` discovers the captured
  PID's assigned port without releasing a reservation or retrying startup.
- For a promise about a moment between two steps of one operation,
  `s.PauseAt(point)` holds the daemon or a worker the first time it reaches a
  pause point named in its code (`internal/pausepoint`, `ATTN_PAUSE_AT`);
  `s.PauseEvery` holds it each time. Arm it before `Start`, or start with
  `s.StartHeldAt(pause)`. `Await` returns the held process's pid and `Release`
  lets it go. It is the same `testworld.Pause` a daemon wire test gets, and
  its `Await` fails at once with the step's error when a `Done` point's step
  failed.
- For a promise about a crash mid-operation, `s.StartCrashingAt(point)` runs
  a daemon that kills itself with SIGKILL at a crash point named in the
  daemon (`ATTN_CRASH_AT`). `s.AwaitCrash()` returns once it has, and a later
  `Start` recovers from the state the real daemon left behind. The crash is
  immediate unless the test also arms `s.PauseAt(point)` with the same name
  before starting: then the daemon holds at the crash point and dies only once
  the test releases it, so the test can first let a request still in flight,
  such as a hook waiting for its reply, get its answer. `AwaitCrash` never
  releases that hold. A later `Start` without the crash does not stop there.
- The world helpers from daemon wire tests work here too.
- `s.Attn(args...)` runs a CLI command to completion. `s.Run` takes an
  `Invocation` when the command needs stdin, a session, extra env, or another
  binary.
- `s.Launch` starts a command that waits on something the test does next,
  such as a long-running watch or a request the test answers as the app.
  Await its output with `AwaitStdout` or `AwaitStderr`, or its result with
  `Wait`. The stack interrupts it at cleanup.
- `s.Advance(d)` moves the stepped clock of that daemon and of every PTY worker
  it started: the bridge's stall, loss, retry and dial-answer timers, a
  terminal's kill escalation, a folder watch's window and the background job
  runner's schedule. Every stack uses this stepped clock; the shared PTY host,
  which is not Go, keeps real time. A test waiting for one of those timers
  without advancing fails at the hang guard.
  Heartbeats and kernel socket deadlines keep real time, so a dial may still
  fail without an advance. Await the connected or stalled row before advancing
  its liveness timers.
- `s.AwaitTimer(d)` waits until a timer of `d`, armed or reset after the last
  `Advance` or `AwaitTimer`, is pending in the daemon or one of its workers.
  Use it before advancing an asynchronous dial, retry or kill: its row or
  close can arrive before its timer is armed. A failure names the requested
  duration and the timers pending.
- `s.CloseWatchWindows()` closes each folder watch's window as soon as it
  opens, until the test calls the returned function. Each close moves the
  whole clock, the bridge's timers included, so keep it to one wait for a
  watched change.
- A test that starts PTY workers without a stack steps them with
  `testworld.StepChildren(t)`, whose `Advance` and `AwaitTimer` work the same
  way. The test process itself keeps real time.

If the daemon misses its ready guard or `s.Run` exceeds its hang guard, the
stack sends SIGQUIT to capture all goroutines before terminating the process.
CLI invocations have their own process group so shell wrappers and their Go
children receive the same signal.
Failure logs include CPU and I/O pressure from `/proc/pressure` when available.
Oversized diagnostic streams retain their beginning and end and report the
byte limit, original size and omitted bytes.

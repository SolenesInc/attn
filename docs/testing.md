# Testing

Tests guard the promises attn makes. To check your own work, run it: the
daemon over `wsctl`, the CLI, a non-production instance, a throwaway test. Keep
those scratch checks out of commits.

## Promises

A promise is behavior that someone outside the code relies on: the user, the
agents attn runs, the daemon's clients, or a later version of attn
reading today's data. attn's promises include:

- **Protocol**: the commands, responses, and events exchanged between the
  daemon and its clients: the app, the CLI, and remote daemons.
- **Durability**: sessions, terminals, and the database survive app, daemon,
  and machine restarts, and upgrades between versions.
- **CLI**: commands, output, and exit codes that users and agents rely on.
- **Screen**: what the user sees, and what keyboard and pointer input does.
- **Performance**: idle attn stays quiet, and memory does not creep.
  Benchmarks and memory scenarios track it; see
  [Performance testing](perf-testing.md).
- **Agent prompts**: the instructions attn sends to the agents it runs. Their
  compatibility fixtures change only for intentional wording edits, per
  [Prompt authoring](prompt-authoring.md#verify).
- **Specified logic**: rules with large input spaces that other promises
  depend on, such as state classification or the wire feed rewrite.

Most behavior is observable through the protocol, so daemon and app tests
both run against it.

## Depend only on promises

A committed test depends only on promises, never on internals. To check,
imagine rewriting the code from scratch with the same behavior. If the test
would break, it depends on internals.

- Instead of writing a session's state to the store, spawn a fake agent and
  have it `Reply` with the state marker.
- Instead of writing rows to set up state from before a restart, create it
  through the protocol, call `w.restart()`, and check it on the new daemon.
- Instead of asserting a log line says mail was claimed, await the mail event
  on a connected peer.
- Instead of sleeping until a periodic tick runs, run in `inBubble` and
  `w.advance` the clock.
- Instead of reading a daemon struct to check a result, request it over the
  protocol or through the CLI.
- Instead of poking launch internals to catch an agent mid-boot, call
  `w.HoldNextBoot()`, which holds the fake agent, not the daemon.

When a promise cannot be reached or observed this way, extend the harness
(`testworld`, `fakeagent`, the real-app harness) instead of reaching inside
from the test. A harness capability models something outside attn, such as
an agent, the user, the clock, or the network. It never sets attn's own state.

Kernel tests build their function's input directly, since the function is
itself a promise.

## Kinds of test

A kind names where the test enters and what it may fake. Fake nothing else.

- **Kernel** enters and observes through a function with a written
  specification. Real: the function. Faked: nothing.
- **Wire** enters and observes through the protocol, from one side. Real:
  everything on the other side of the protocol. Faked: agent binaries,
  external services, clock, network; the browser and Tauri host for the app.
- **Stack** enters through the CLI, the protocol, or a browser page, across
  processes. Real: daemon, PTY workers and host, CLI, frontend. Faked: agent
  binaries, external services such as GitHub; the PTY in the browser suite.
- **Scenario** enters through native input and observes the screen, the
  protocol, and the CLI. Real: everything attn ships, packaged as shipped.
  Faked: agent binaries, external services such as GitHub.

### Wire

Wire tests are the default, and come in two forms:

- A daemon wire test runs a real daemon with a real store, bus, and PTY layer,
  and acts as a protocol client.
- An app wire test renders the real app with its real socket client and acts
  as the daemon. It sends protocol messages and asserts on what renders and
  what the app sends back. It runs in a simulated browser with the Tauri host
  stubbed; everything else attn ships in the frontend is real.

Both use the generated protocol types, so a protocol change breaks both at
once.

A fake agent binary behaves like the real harness from the outside: same
hooks, transcript files, and terminal output. Tests control time instead of
waiting for it, with `synctest` in Go and Vitest fake timers in the app. To
simulate a network failure, wrap the client's `net.Conn` inside the bubble. No
test sleeps or polls.

#### Writing a daemon wire test

Daemon wire tests live in `package daemon_test` in `internal/daemon`, so they
can reach the daemon only through the protocol and the CLI client.

The world:

- `newWorld(t)` runs a production daemon in its own data directory.
  `w.restart()` replaces it with a new daemon over the same data.
- `w.App()` connects as the app. `w.Client()` returns a CLI client.
- `testworld.Await`, `testworld.Request` and `testworld.AwaitSession` read what
  the daemon sends.

Agents:

- Name the agents the test spawns, as in `newWorld(t, fakeagent.Claude, ...)`.
  The test then plays the model behind each one.
- `w.Spawn` starts a session. `w.Launched` returns the `fakeagent.Run` for its
  agent.
- `w.RequestSpawn` sends the same requests and returns the spawn result with
  the workspace and pane it added, so the test can close a refused spawn's
  pane the way the app does.
- `w.HoldNextBoot()` keeps the next agent booting, before it paints its
  resting title or reads input, until the test calls the returned function.
- `w.ExitAtNextBoot(code, screen)` makes the next agent print `screen` and
  exit with `code` before it starts, the way a harness that cannot start does.
- `w.RefusePiLaunches(reason)` makes the Pi driver refuse every launch and
  resume with `reason` until the test calls the returned function. Setting
  `fakeagent.PiCapabilitiesEnv` (such as `launch_instructions,resume=false`)
  before the world starts changes the capabilities the Pi driver registers.
  `fakeagent.PiAgentEnv` registers the driver under another harness name, and
  `fakeagent.PiModelsEnv` holds the catalog JSON it answers `driver.models` with.

Playing the model, on a `fakeagent.Run`:

- `Prompted` returns the prompt the agent received and moves `ConversationID`
  to the agent's current conversation (`/clear` starts a new one).
- `Reply` ends the turn with text carrying the `<!-- attn:state=... -->`
  marker. `ReplyAfterStop` writes that reply only after the Stop hook.
- `Exit` quits with an exit code.
- `StopReadingTerminal` stops reading input, so what the daemon types backs
  up in the terminal the way it does for a frozen agent.
- `Halt` writes the harness's own record of the user interrupting the turn
  (Claude, Codex and Copilot).
- Claude only: `Stream` writes part of a reply that the next `Reply` revises
  under the same message. `Subagent` writes a subagent's transcript, and
  `DeleteSubagentTranscripts` removes those transcripts.
  A Claude launched with a bare `-r` opens its resume picker, which starts a
  new conversation and sets `ResumePicker` on its `Run`.
- Headless tasks, the one-shot model calls such as titles and turn verdicts,
  are off unless the test sets `ATTN_HEADLESS_TASKS=on`. Then `w.HeadlessTask()`
  returns the next task's `Harness`, `Model`, `Effort` and `Prompt` to `Answer`
  or `Fail`, and a task the test never takes fails it. The task also carries
  the `Argv`, `Env` and working `Dir` the harness process was started with.

Waiting for results:

- With a Stop hook (Claude and Codex), `Reply` and `ReplyAfterStop` return
  once the daemon has recorded the turn's end. Every other write returns once
  written, and the daemon reads the transcript on its own.
- Either way, await the resulting event on a peer that connected before the
  call. A peer that connects later sees the result in its initial state, which
  it cannot await.
- `testworld.AwaitSession` also accepts an update from before the call that
  the peer has not read yet. To wait for a state that follows one the test
  already saw, use `testworld.AwaitStateAfter`.

Timers:

- For behavior on a timer, write the test as
  `inBubble(t, func(t *testing.T, w *world) {...})`, which runs the world
  under `synctest`, and move the clock with `w.advance(d)`.
- A bubbled world cannot run agents or watch folders. Its notebook at
  `<w.Dir>/notebook` exists only if a test outside a bubble creates it.

### Stack

Stack tests cover what only exists between processes: restarts, reconnects,
signals, PTY ownership, CLI behavior, and Linux paths. The browser end-to-end
suite is also a stack test: a real daemon serves the frontend to a browser.
It mocks the PTY unless a test needs real terminals.

#### Writing a CLI stack test

CLI stack tests live in `package main_test` in `cmd/attn`, whose `TestMain`
returns `testworld.Main(m)`.

- `testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude, ...))`
  prepares a data directory for the built `attn` binary.
- `s.Start()` runs `attn daemon` and returns once it signals ready. `s.Stop()`
  ends it. A `Start` after `Stop` restarts over the same data.
  The stack holds its WebSocket listener until cleanup, including while the
  daemon is stopped, so another process cannot take its address.
- For a promise about a crash mid-operation, `s.StartCrashingAt(point)` runs
  a daemon that kills itself with SIGKILL at a crash point named in the
  daemon (`ATTN_CRASH_AT`). `s.AwaitCrash()` returns once it has, and a later
  `Start` recovers from the state the real daemon left behind.
- The world helpers from daemon wire tests work here too.
- `s.Attn(args...)` runs a CLI command to completion. `s.Run` takes an
  `Invocation` when the command needs stdin, a session, extra env, or another
  binary.
- `s.Launch` starts a command that waits on something the test does next,
  such as a long-running watch or a request the test answers as the app.
  Await its output with `AwaitStdout` or `AwaitStderr`, or its result with
  `Wait`. The stack interrupts it at cleanup.

If the daemon misses its ready guard or `s.Run` exceeds its hang guard, the
stack sends SIGQUIT to capture all goroutines before terminating the process.
CLI invocations have their own process group so shell wrappers and their Go
children receive the same signal.
Failure logs include CPU and I/O pressure from `/proc/pressure` when available.
Oversized diagnostic streams retain their beginning and end and report the
byte limit, original size and omitted bytes.

### Scenario

Scenarios run the packaged app in CI under Xvfb; see the
[verification requirements](instances.md#verification-requirements) and the
[harness guide](../app/scripts/real-app-harness/AGENTS.md). They cover
rendering, focus, native keyboard and pointer input, menus, scrolling, and
whole-product behavior such as queue mode. Their verdicts come from the screen,
the protocol, or the CLI, never from the daemon's database.

### Kernel

Write a kernel test only when both hold:

- The function has a specification in domain terms, independent of how it is
  written. Examples: the state classifier, turn accounting, parsers, the
  terminal wire rewrite, migrations.
- Its input space is too large to cover through the protocol.

Kernel tests are tables, corpora, or `rapid` properties; a single
hand-picked example does not count. A migration test's input is a database
with an older version's schema and data, built however the test needs.

## Choosing a kind

Use the first kind in this order that can observe the behavior:

1. Wire, by default.
2. Stack, when the behavior crosses a process boundary.
3. Scenario, when the behavior is pixels, focus, or native input.
4. Kernel, only under the conditions above.

A bug fix adds a regression test at the first kind that reproduces what the
user saw.

## Budgets

- A wire test finishes well under a second.
- A stack test finishes in a few seconds.
- Scenarios run in CI; run them locally only to reproduce a failure or
  develop a scenario.

A test over its budget belongs to a slower kind, or its harness needs work.

## When a test breaks

If a change keeps behavior and a test breaks, the test depended on
internals. Delete it, or replace it with a test of the right kind. Do not
repair it to match the new internals.

## Isolation

Every test that reaches config paths follows the
[test safety contract](../AGENTS.md#test-safety).

# Testing

Tests guard the promises attn makes. They are not how an agent checks its own
work. Check work by running it: the daemon over `wsctl`, the CLI, a
non-production instance, a throwaway test. Keep scratch checks out of commits,
the same way spikes stay out.

## Promises

A promise is behavior that someone outside the code relies on: the user, the
agents attn runs, the daemon's clients, app authors, or a later version of attn
reading today's data. attn's promises include:

- **Protocol**: the commands, responses, and events exchanged between the
  daemon and its clients: the app, the CLI, and remote daemons.
- **Durability**: sessions, terminals, and the database survive app, daemon,
  and machine restarts, and upgrades between versions.
- **CLI**: commands, output, and exit codes that users and agents rely on.
- **Screen**: what the user sees, and what keyboard and pointer input does.
- **App SDK**: the API that apps and their views build against. Its declared
  types must match the protocol shapes they mirror; a check comparing the two
  guards both promises.
- **Performance**: idle attn stays quiet, and memory does not creep. Benchmarks
  and memory scenarios track it, as described in
  [Performance testing](perf-testing.md).
- **Agent prompts**: the instructions attn sends to the agents it runs. Their
  compatibility fixtures change only for intentional wording edits, per
  [Prompt authoring](prompt-authoring.md#verify).

The protocol is the main seam. Most behavior is observable there, and both
the daemon and the app are tested against it.

## The rewrite rule

Before committing a test, ask: could everything behind the boundary it drives
be rewritten from scratch, behavior preserved, without editing the test? If
not, the test pins the implementation. Do not commit it.

For wire, stack, and scenario tests:

- They do not call unexported functions, read internal structs, or assert on
  log lines.
- They do not seed state by writing to the store or calling internals. They
  reach a state the way a client would, through the protocol, the CLI, or a
  prior daemon run.
- Only what the table below lists as faked is faked. Everything else behind
  the boundary is real.

A kernel test's boundary is its function, so it builds the function's input
directly.

## Kinds of test

Each kind is defined by where the test enters and what it may fake.

| Kind     | Enters and observes through                                | Real                                         | Faked                                                                                     |
| -------- | ---------------------------------------------------------- | -------------------------------------------- | ----------------------------------------------------------------------------------------- |
| Kernel   | A function with a written specification                    | The function                                 | Nothing                                                                                   |
| Wire     | The protocol, from one side                                | Everything on the other side of the protocol | Agent binaries, external services, clock, network; the browser and Tauri host for the app |
| Stack    | The CLI, the protocol, or a browser page, across processes | Daemon, PTY workers and host, CLI, frontend  | Agent binaries, external services such as GitHub; the PTY in the browser suite            |
| Scenario | Native input; observes the screen, the protocol, the CLI   | Everything attn ships, packaged as shipped   | Agent binaries, external services such as GitHub                                          |

### Wire

Wire tests are the default. A daemon wire test runs a real daemon with a real
store, bus, and PTY layer, and acts as a protocol client. An app wire test
renders the real app with its real socket client, and acts as the daemon,
sending protocol messages and asserting on what renders and what the app sends
back. Both sides use the generated protocol types, so a protocol change breaks
both at once. App wire tests run in a simulated browser with the Tauri host
stubbed; everything attn itself ships in the frontend runs for real.

Fake agent binaries follow the real harness's observable contract: its hooks,
transcript files, and terminal output. Time is controlled, never waited on:
`synctest` in Go, Vitest fake timers in the app. Network failures wrap the
client's `net.Conn` inside the bubble. No test sleeps or polls.

#### Writing a daemon wire test

Daemon wire tests live in `package daemon_test` in `internal/daemon`, so they
reach the daemon only through the protocol and the CLI client. `newWorld(t)`
runs a production daemon in its own data directory; `w.restart()` replaces it
with a new daemon over the same data. `w.App()` connects as the app and
`w.Client()` returns a CLI client; `testworld.Await`, `testworld.Request` and
`testworld.AwaitSession` read what the daemon sends. Name the agents when the
test spawns sessions, as in `newWorld(t, fakeagent.Claude, ...)`: `w.Spawn`
starts one and `w.Launched` returns the `fakeagent.Run` for its agent;
`w.RequestSpawn` sends the same requests and returns the spawn result with the
workspace and pane it added, so a refused spawn's pane can be closed as the app
closes it. The test plays the model behind that agent.
`Prompted` returns the prompt the agent received and moves `ConversationID` to
the conversation the agent is in, which `/clear` replaces; `Reply` ends the
turn with text that carries the `<!-- attn:state=... -->` marker,
`ReplyAfterStop` writes that reply only after the Stop hook, and `Exit` quits
with an exit code. For Claude, `Stream` writes part of the reply that the next
`Reply` revises under the same message, `Subagent` writes a subagent's
transcript, and `DeleteSubagentTranscripts` removes those transcripts.
`Halt` writes the harness's own record of the user interrupting the turn
(Claude, Codex and Copilot). Where the harness has a Stop hook (Claude and
Codex), `Reply` and `ReplyAfterStop` return once the daemon holds the turn's
end; every other write returns once written, and the daemon reads the
transcript on its own. Either way, await the resulting event on a peer
connected before the call: a new peer's initial state is not an event it can
await. `testworld.AwaitSession` accepts any update the peer has not yet
consumed, including one from before the call, so await a state the session
enters after one the test already saw with `testworld.AwaitStateAfter`.
`w.HoldNextBoot()` keeps the next
agent to launch booting, before it paints its resting title or reads input,
until the returned function runs. For behavior on a timer, write the test as
`inBubble(t, func(t *testing.T, w *world) {...})`, which runs the world under
`synctest`, and move the clock with `w.advance(d)`. Bubbled worlds cannot run
agents or watch folders, so a world's notebook at `<w.Dir>/notebook` exists only
once a test outside a bubble creates it.

### Stack

Stack tests cover what only exists between processes: restarts, reconnects,
signals, PTY ownership, CLI behavior, and Linux paths. The browser end-to-end
suite is a stack test: a real daemon serving the frontend in a browser. It
mocks the PTY unless a test needs real terminals.

#### Writing a CLI stack test

CLI stack tests live in `package main_test` in `cmd/attn`, whose `TestMain`
returns `testworld.Main(m)`. `testworld.NewStack(t,
testworld.WithAgents(fakeagent.Claude, ...))` prepares a data directory for the
built `attn` binary; `s.Start()` runs `attn daemon` and returns once it signals
ready, and `s.Stop()` ends it, so a `Start` after `Stop` restarts over the same
data. The world helpers of a daemon wire test work here too. `s.Attn(args...)`
runs a CLI command to completion; `s.Run` takes an `Invocation` for stdin, a
session, extra env, or another binary. `s.Launch` starts a command that
must wait on something the test does next, such as a long-running watch or a
request the test answers as the app; the test awaits its output with
`AwaitStdout` or `AwaitStderr`, or its result with `Wait`, and the stack
interrupts it at cleanup.

### Scenario

Scenarios run the packaged app in CI under Xvfb, per the
[verification requirements](instances.md#verification-requirements) and the
[harness guide](../app/scripts/real-app-harness/AGENTS.md). They cover
rendering, focus, native keyboard and pointer input, menus, scrolling, and
whole-product behavior such as queue mode.
Their verdicts come from the screen, the protocol, or the CLI, never from the
daemon's database.

### Kernel

A kernel test is justified only when both hold:

- the function has a specification in domain terms, independent of how it is
  written: the state classifier, turn accounting, parsers, the terminal wire
  rewrite, migrations;
- its input space is too large to cover through the protocol.

Kernel tests are tables, corpora, or `rapid` properties. One hand-picked
example per function is not a kernel test. A migration's input is a database
in an older version's schema and data, built however the test needs.

## Choosing a kind

Use the lowest kind that can observe the behavior:

1. Wire, by default.
2. Stack, when the behavior crosses a process boundary.
3. Scenario, when the behavior is pixels, focus, or native input.
4. Kernel, only under the conditions above.

A bug fix adds a regression test at the lowest kind that reproduces what the
user saw.

## Budgets

- A wire test finishes well under a second.
- A stack test finishes in a few seconds.
- Scenarios run in CI; run them locally only to reproduce a failure or
  develop a scenario.

A test that needs more than its budget belongs to a higher kind, or its
harness needs work.

## When a test breaks

If a change preserves behavior and a test breaks, the test pinned the
implementation. Delete it, or replace it with a test at the right kind. Do not
repair it to match the new internals.

## Isolation

Every test that reaches config paths follows the
[test safety contract](maintainer-contracts.md#test-safety).

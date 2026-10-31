---
name: write-test
description: Use when writing, changing or deleting a test in attn, including a regression test for a bug and a fix for a failing or flaky test.
---

# Write a test

A committed test guards a promise attn makes, enters where its kind enters
and fakes only what that kind may fake. `docs/internal/testing.md` is the
strategy. This skill points you at the part of it you need, and lists the
habits that most often break it.

## Decide

- Name the promise and who relies on it: the user, the agents attn runs, a
  client, or a later version reading today's data. When you cannot, check
  your change by running it and commit no test.
- Pick the kind: the first in "Choosing a kind" that can observe the
  behavior. For a bug, the first that reproduces what the user saw.
- Search the suite for a test of the same promise through the same entry:
  the same command, event, CLI verb or world helper. Extend it instead of
  adding another.
- When the harness cannot reach or observe the promise, extend it with
  something outside attn: an agent, the user, the clock, the network. When
  the promise is about a moment between two steps of one action, add a pause
  or crash point instead.

## Read

Read `docs/internal/testing.md`, then the page for your kind:

| Kind | Read |
|---|---|
| Daemon wire | `docs/internal/daemon-wire-tests.md` |
| App wire | `docs/internal/app-wire-tests.md` |
| CLI stack | `docs/internal/cli-stack-tests.md` |
| Browser end-to-end | the specs in `app/e2e` near yours |
| Scenario | `app/scripts/real-app-harness/AGENTS.md` |

## Gotchas

Each is a habit that works elsewhere and breaks a promise-only test here.

### Time

- Sleeping, or polling for a result the test could be told about, in a Go or
  app test. Wait for the signal that it happened: the event on a peer that
  connected before the call (`testworld.Await`, `testworld.AwaitSession`),
  the CLI's output, or a pipe the process opens when ready. Browser
  end-to-end specs are the exception `testing.md` names.
- Waiting for a timer to fire. Move the clock: `w.advance(d)` in a world,
  `s.Advance(d)` in a stack. When the timer is armed asynchronously, wait
  for it first: `w.awaitTimer(d)` in a world outside a bubble,
  `s.AwaitTimer(d)` in a stack.
- `time.After(5 * time.Second)` as a deadline, in a `select` or stored first.
  A short one fails on a loaded machine, and a long one is a sleep. The only
  real-time bound is `fakeagent.HangGuard`, for a signal that never arrives.
- Waiting a while to show that something never happens. Run in `inBubble`
  and advance past the moment it would have happened: under `synctest`,
  "nothing happened" is exact.
- Flagging `time.Now()` inside `inBubble` or `synctest.Test` as a live-clock
  bug. It reads the bubble's clock, which moves only while every goroutine
  in the bubble waits, so a `time.Since(before) != 0` in a bubble means the
  call waited on a timer.
- Testing Library's `waitFor` and `findBy*`, `vi.waitFor` and `expect.poll`
  poll on an interval. The scripted daemon installs fake timers:
  `gesture(daemon, action)` or `await daemon.idle()` settles the app,
  `daemon.received(cmd)` waits for a request, and
  `await act(() => vi.advanceTimersByTimeAsync(ms))` moves time.
- `await new Promise((r) => setTimeout(r, ms))`, `Bun.sleep(ms)` or a
  `Date.now()` deadline loop in a plugin or SDK test. Have the fake resolve a
  promise when the call the test waits for arrives, as `daemon.received`
  does.

### Internals

- Writing store rows, reading daemon structs, or writing the test in
  `package daemon` to set up or check state. Write it in `package
  daemon_test`: to set a session's state, spawn a fake agent and have it
  `Reply` with the state marker; for state from before a restart, create it
  through the protocol and call `w.restart()`; to check a result, request it
  over the protocol or through the CLI.
- Asserting on a log line, such as one saying mail was claimed. Await the
  event on a connected peer.
- Poking launch internals to catch an agent mid-boot. `w.HoldNextBoot()`
  holds the fake agent, not the daemon.
- Avoiding a pause or crash point because it lives in attn's code, or calling
  it a harness that sets attn's state. When the promise is about a moment
  between two steps of one action, such as a change landing while a request
  is held, `w.daemon.PauseNextHeldAction`, `s.PauseAt` or `s.StartCrashingAt`
  is the way to reach it. A new point is the way to reach another such
  moment.
- `vi.mock('../Component')`, or mocking any other module of attn's own. An
  app wire test fakes the daemon, which it scripts, and only the other fakes
  `docs/internal/app-wire-tests.md` lists. Render the real module through
  `renderApp`.
- `render(<Component …/>)` with hand-built props depends on those props,
  which a rewrite changes. Render the app with `renderApp` and reach the
  component the way the user does.

### Flaky tests

- Retrying a flaky test, or giving it a longer timeout. Find the step it
  races, usually a wait with no signal behind it, and give that step a
  signal or a stepped clock.

## Finish

Run the verify-tests skill on the change before it lands. It runs each test
under the race detector repeatedly, checks budgets, and has you break the
behavior to see the test fail.

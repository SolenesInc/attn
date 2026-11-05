---
name: write-test
description: Use when writing, changing or deleting a test in attn, including a regression test for a bug and a fix for a failing or flaky test.
---

# Write a test

A committed test guards a promise attn makes, enters where its kind enters
and fakes only what that kind may fake. `docs/internal/testing.md` is the
strategy. This skill points you at the part of the strategy you need, and
lists the habits that most often break the strategy.

## Decide

- Name the promise and who relies on it: the user, the agents attn runs, a
  client, or a later version reading today's data. When you cannot name the
  promise, check your change by running it and commit no test.
- Pick the kind: the first in "Choosing a kind" that can observe the
  behavior. For a bug, pick the first kind that reproduces what the user saw.
- Search the suite for a test of the same promise through the same entry:
  the same command, event, CLI verb or world helper. Extend that test
  instead of adding another.
- When the harness cannot reach or observe the promise, extend the harness with
  something outside attn: an agent, the user, the clock, the network. For a
  promise about a moment between two steps of one action, use a pause or
  crash point, as Internals describes.

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
  app test. Wait for the signal that the result happened: the event on a peer
  that connected before the call (`testworld.Await`,
  `testworld.AwaitSession`), the CLI's output, or a pipe the process opens
  when ready. Browser end-to-end specs are the exception `testing.md` names.
- Waiting for a timer to fire. Move the clock: `w.advance(d)` in a world,
  `s.Advance(d)` in a stack. When the timer is armed asynchronously, wait
  for the timer first, by the name its package gives it: `w.awaitTimer(name)`
  in a world outside a bubble, `s.AwaitTimer(name)` in a stack.
- `time.After(5 * time.Second)` as a deadline, in a `select` or stored first.
  A short deadline fails on a loaded machine, and a long deadline is a sleep.
  The only real-time bound is `fakeagent.HangGuard`, for a signal that never
  arrives.
- Waiting a while to show that something never happens. Run in `inBubble`
  and advance past the moment it would have happened. Under `synctest`,
  "nothing happened" is exact.
- Flagging `time.Now()` inside `inBubble` or `synctest.Test` as a live-clock
  bug. `time.Now()` reads the bubble's clock, which moves only while every
  goroutine in the bubble waits. So a `time.Since(before) != 0` in a bubble
  means the call waited on a timer.
- Testing Library's `waitFor` and `findBy*`, `vi.waitFor` and `expect.poll`
  poll on an interval. Use the scripted daemon instead, which installs fake
  timers. `gesture(daemon, action)` or `await daemon.idle()` settles the app,
  `daemon.received(cmd)` waits for a request, and
  `await act(() => vi.advanceTimersByTimeAsync(ms))` moves time.
- `await new Promise((r) => setTimeout(r, ms))`, `Bun.sleep(ms)` or a
  `Date.now()` deadline loop in a plugin or SDK test. Have the fake resolve a
  promise when the call the test waits for arrives, as `daemon.received`
  does.

### Internals

- Writing store rows, reading daemon structs, or writing the test in
  `package daemon` to set up or check state. Write the test in
  `package daemon_test`. To set a session's state, spawn a fake agent and
  have the agent `Reply` with the state marker. For state from before a
  restart, create the state through the protocol and call `w.restart()`. To
  check a result, request the result over the protocol or through the CLI.
- Asserting on a log line, such as one saying mail was claimed. Await the
  event on a connected peer.
- Poking launch internals to catch an agent mid-boot. `w.HoldNextBoot()`
  holds the fake agent, not the daemon, and orders a daemon step only if that
  step waits for the agent every time. To fault a daemon step, hold the step
  at its own pause point, arm the fault, then release the step.
- Avoiding a pause or crash point because the point lives in attn's code, or
  calling the point a harness that sets attn's state. When the promise is
  about a moment between two steps of one action, such as a change landing
  while a request is held, reach the moment with `w.PauseAt`, `s.PauseAt` or
  `s.StartCrashingAt`. To reach another such moment, add a new point:
  `pausepoint.At` for a moment, and `pausepoint.Done` for a finished step, so
  that a failed step never looks finished.
- Awaiting a state that may already hold as proof that your action was
  processed. Await a receipt that only the awaited step can produce, such as
  a fresh label.
- `vi.mock('../Component')`, or mocking any other module of attn's own. An
  app wire test fakes the daemon, which the test scripts, and the other fakes
  that `docs/internal/app-wire-tests.md` lists. Nothing else is faked. Render
  the real module through `renderApp`.
- `render(<Component …/>)` with hand-built props depends on those props,
  which a rewrite changes. Render the app with `renderApp` and reach the
  component the way the user does.

### Flaky tests

- Retrying a flaky test, or giving the test a longer timeout. Find the step
  the test races, usually a wait with no signal behind it, and give that step
  a signal or a stepped clock.
- A bubble test that hangs until the test alarm, with a daemon goroutine in
  `sync.Mutex.Lock` in the dump. `synctest.Wait` does not count a mutex wait as
  durably blocked, so it never returns while the mutex's holder waits for the
  test. When the daemon holds a lock across a call the test answers, such as a
  plugin's `driver.deliver_message`, make that lock a channel, as
  `inboxDeliveryState.lock` does. Never add a timeout to the test.

## Finish

Before the change lands, run the verify-tests skill on the change. The skill
has you run each test repeatedly under the race detector, check the budgets,
and break the behavior to see the test fail.

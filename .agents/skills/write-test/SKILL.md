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

Read `docs/internal/testing.md`, and inspect the harness package for your kind
(doc comments and neighboring tests are the API reference):

| Kind | Harness |
|---|---|
| Daemon wire | `internal/daemon/world_test.go`, `internal/testworld/`, `internal/fakeagent/`, `internal/daemon/AGENTS.md` |
| App wire | `native/crates/attn-testkit/` |
| CLI stack | `internal/testworld/` (`Stack`), `cmd/attnd/` |
| Scenario | `app/scripts/real-app-harness/AGENTS.md` |

## Gotchas

Each is a habit that works elsewhere and breaks a promise-only test here.

### Harness traps

- **Typing or delivering to a new agent too early.** Call `w.Launched` before
  typing at a new fake agent (a line typed before the terminal is raw does not
  submit to Claude). Before delivering to a new agent, await its session's
  `idle` state, not text on its screen, or delivery can return
  `skipped_pending_approval`.
- **`inBubble` cannot run real agents or watch folders.** Use a stepped-clock
  world outside a bubble (`w.advance`, `w.awaitTimer(name)`), or
  `inBubbleWithAgents` for fake terminals.
- **Stack tests need `scripts/test-go.sh`.** It supplies `ATTN_TEST_PTY_HOST`
  and `ATTN_NATIVE_APP_BIN` from the build store. A test calling
  `Stack.StartNativeApp` must be named `TestTheNativeApp*`.
- **Native wire tests (`attn-testkit`).** Build protocol messages with
  `InitialStateMessage::new()` (not `Default::default()`, which leaves `event`
  empty). `Target::role("status")` without a name always matches Rune's hidden
  announcement element. Omit empty optional fields in command assertions
  (`request.get("field").is_none()`). End each test with
  `assert!(world.runtime_is_at_rest())`. Measure per-frame work with
  `attn_testkit::allocations::made_here()`, never wall time.
- **Stepping a connected home/outpost pair in a stack.** Advance in steps
  shorter than `bridge.StallAfter` and `AwaitTimer(bridge.StallTimer)` after
  each, or use `s.FireTimers(bridge.RetryTimer)` when a redial retry may or may
  not be armed.

### Time

- Sleeping, or polling for a result the test could be told about, in a Go or
  app test. Wait for the signal that the result happened: the event on a peer
  that connected before the call (`testworld.Await`,
  `testworld.AwaitSession`), the CLI's output, or a pipe the process opens
  when ready.
- Connecting a peer after the step it then waits for. The daemon sends an
  event only to clients connected at that moment, so an update from a daemon's
  startup work is lost to a peer that connects after `w.restart()` or
  `s.Start()`, and the wait hangs. Connect first when the test starts the
  step. Otherwise accept what the peer learned on connecting:
  `testworld.AwaitSessionFromConnect` for a session, `p.Initial` before an
  `Await` for anything else. A refresh that finds no change publishes nothing,
  so it cannot stand in for the lost event.
- Waiting for one signal and then asserting a state that comes after it.
  The wait passes on a precursor and the read races: the first
  `settings_updated` with `theme=dark` before the frame carrying
  `default_model_claude`, `session_unregistered` before the arrangement
  without the pane, an earlier toast still showing, half a terminal row.
  Make the wait the assertion: await every value the test then reads, in
  `testworld.Await`'s predicate, in the native driver's `state` `expect`,
  or in a scenario's `waitForPaneState` predicate.
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
- `await new Promise((r) => setTimeout(r, ms))`, `Bun.sleep(ms)` or a
  `Date.now()` deadline loop in a plugin or SDK test. Have the fake resolve a
  promise when the call the test waits for arrives.

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

Before landing or opening a pull request, run the `verify-tests` skill on the
change. When landing on `main`, `land-on-main` runs `verify-tests` as an
independent review.

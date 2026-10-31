# App wire tests

An app wire test renders the real app with its real socket client and plays
the daemon. [Testing](testing.md) is the strategy around it. The harness lives
in `app/src/test/`; paths below are relative to `app/src`.

## The app and its daemon

- `renderApp({ initialState, script })` installs a scripted daemon, renders
  `App` and returns once the app has connected and the handshake has settled.
  `initialState` overrides what `client_hello` answers; `false` leaves the
  handshake unanswered. `script` registers the daemon's answers before the
  app connects.
- `restartApp(running)` unmounts the app, resets its in-memory stores and
  renders it again, as a relaunch does.
- The Tauri host is the only other fake. `test/setup.ts` mocks its modules;
  `fetch` rejects, because app wire tests reach no network.
- Domain helpers in `test/` script whole features, such as
  `delegationDaemon.ts` for delegation settings. Reuse or extend one before
  scripting the same commands again in a test.

## Playing the daemon

On the `ScriptedDaemon` that `renderApp` returns:

- `on(cmd, reply)` answers each `cmd` the app sends; the reply gets the
  request's `request_id`. Returning nothing holds the request unanswered.
- `answerInTurn(daemon, cmd, answers)` answers successive requests in order.
  `HOLD` leaves one unanswered, and a request past the last answer fails the
  test.
- `emit(event)` pushes an event, and `replyTo(command, reply)` answers a
  request the test held.
- `disconnect()` drops the connection, and `reconnect()` returns once the app
  has connected again and settled its handshake.
- The test fails at its end when the app broke the daemon's script, such as
  by sending a command before `client_hello`.

## Waiting and time

`renderApp` installs Vitest fake timers, and nothing in the app waits on real
time.

- `gesture(daemon, action)` runs a user action and returns once the app and
  the daemon have stopped exchanging messages. `daemon.idle()` waits for the
  same without an action.
- `daemon.received(cmd, matches)` returns the first request that matches,
  including one already sent.
- `await act(() => vi.advanceTimersByTimeAsync(ms))` moves time for the
  app's own timers.

## What to assert

- What renders, read through Testing Library's `screen` queries.
- The exact requests the app sends once render settles, from `daemon.sent`
  or `daemon.sentOf(cmd)`, to catch fetch loops.

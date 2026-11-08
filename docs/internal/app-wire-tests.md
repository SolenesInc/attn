# App wire tests

An app wire test renders the real app with its real socket client and plays
the daemon. [Testing](testing.md) is the strategy around it. The harness lives
in `app/src/test/`. Paths below are relative to `app/src`.

## The app and its daemon

- `renderApp({ initialState, script })` installs a scripted daemon, renders
  `App`, and returns once the app has connected and the handshake has settled.
  `initialState` overrides what `client_hello` answers. `false` leaves the
  handshake unanswered. `script` registers the daemon's answers before the
  app connects.
- `restartApp(running)` unmounts the app, resets the app's in-memory stores, and
  renders the app again, as a relaunch does.
- Besides the daemon, the app sees only these fakes. `test/setup.ts` mocks the
  modules of the Tauri host and loads the terminal's WebAssembly from disk.
  Fakes also stand in for the pieces the simulated browser lacks, such as canvas
  and WebGL contexts, `ResizeObserver`, and `matchMedia`. The simulated
  browser cannot open a second window, so `letTileWindowsOpen()` in
  `test/tileWindows.ts` makes `window.open` return a detached one for tile
  windows.
  `installScriptedDaemon` replaces `WebSocket` and `fetch`. `fetch` rejects,
  because app wire tests reach no network.
- Domain helpers in `test/` script whole features, such as
  `delegationDaemon.ts` for delegation settings. Reuse or extend one before
  scripting the same commands again in a test.

## Playing the daemon

On the `ScriptedDaemon` that `renderApp` returns:

- `on(cmd, reply)` answers each `cmd` the app sends. The reply gets the
  request's `request_id`. If the reply returns nothing, the request stays
  unanswered.
- `answerInTurn(daemon, cmd, answers)` answers successive requests in order.
  `HOLD` leaves a request unanswered, and a request past the last answer fails
  the test.
- `emit(event)` pushes an event, and `replyTo(command, reply)` answers a
  request the test held.
- `disconnect()` drops the connection, and `reconnect()` returns once the app
  has connected again and settled its handshake.
- The test fails at its end if the app broke the daemon's script, for example
  by sending a command before `client_hello`.

## Waiting and time

`renderApp` installs Vitest fake timers, and nothing in the app waits on real
time.

- `gesture(daemon, action)` runs a user action and returns once the app and
  the daemon have stopped exchanging messages. `daemon.idle()` waits for the
  same quiet without running an action.
- `daemon.received(cmd, matches)` returns the first request that matches,
  including one already sent.
- `await act(() => vi.advanceTimersByTimeAsync(ms))` moves time for the
  app's own timers.

## Shared modules

`app/vite.config.ts` puts each test file in one of two Vitest projects by
reading the file:

- A file that calls `vi.mock`, `vi.doMock`, `vi.hoisted`, or `vi.resetModules`
  runs in the `isolated` project, with a fresh set of modules.
- Every other file runs in the `shared` project. One worker loads the app's
  modules once and runs many files, so a file costs its tests and not the load.

No list needs editing, because adding `vi.mock` to a test file moves that file.
Keep `vi.mock` in test files. The scan does not read helpers, so a mock in a
helper reaches every later file in a shared worker.

`test/setup.ts` resets what shared files have in common before and after each
test: the DOM, the clipboard, `localStorage`, the app's stores, shortcut
overrides, the Tauri fakes, timers, stubbed globals and environment variables,
and mocks. When a new kind of state outlives a test, add it to that reset.

CI runs the full suite with `--sequence.shuffle.files`, so a dependence on file
order fails there. If a file passes alone with `vitest run <file>` and fails in
the suite, the cause is order. To rerun an order, pass the seed that the run
printed. Use one worker, because parallel workers can hide a leak:

```bash
pnpm --dir app exec vitest run --maxWorkers=1 --sequence.shuffle.files --sequence.seed=<seed>
```

## What to assert

- Assert what renders, through Testing Library's `screen` queries.
- Assert the exact requests the app sends once rendering settles, read from
  `daemon.sent` or `daemon.sentOf(cmd)`, to catch fetch loops.

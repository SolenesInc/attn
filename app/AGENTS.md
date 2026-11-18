# The Tauri app (the rollback)

`app/src` and `app/src-tauri` hold the Tauri app, the rollback for the native
app in `native/`. `make install INSTANCE=<name> APP=tauri` builds it, and a
hotfix release can ship it again, as
[Making a release](../docs/internal/making-a-release.md#the-macos-app) says.
Keep it building and its tests green. App work lands in `native/`:
[Native app](../docs/internal/native-app.md). Seed `s-yxa3tt` deletes this app
one release after the cut-over.

`app/` also holds what both apps use: the real-app harness and the scenarios
of both apps, in `app/scripts/real-app-harness`, and the onboarding rehearsal.

## Daemon connection

Paths are relative to `app/src`.

- Only `App` calls `hooks/useDaemonSocket.ts`; components use `useDaemonApi()`.
- Correlate requests through `hooks/daemonPendingRequests.ts`.
- Domain event handlers live in `hooks/daemon<Domain>Events.ts`.

## Tests

- Name tests `Source.concern.test.tsx`.
- Follow [Testing](../docs/internal/testing.md). App wire tests render the real
  app against the scripted daemon in `src/test/`, as
  [App wire tests](../docs/internal/app-wire-tests.md) describes.
- Real browser APIs need a Playwright harness under `test-harness/harnesses/`,
  registered in `index.ts`, with a spec under `e2e/`.

## Terminal and GPU

- Resize order: model, renderer, paint, `onResize`, PTY SIGWINCH.
- Avoid continuous paint/layout animations in persistent UI, especially
  `box-shadow` and `width`/`height` countdown transitions. Keep decoration static;
  animate `opacity` or `transform` (countdown fills use `scaleX` with the correct
  origin). Verify the whole effect in a fully visible running app and measure
  idle/active CPU; transform alone does not prove it is cheap.
- Release hidden panes with `setSurfaceReleased(true)`. Reuse the WebGL context
  when font metrics change; recreating it exhausts WKWebView's pool.
- Each PTY attachment deadline belongs to one pending waiter. Clear the deadline
  when its waiter settles: a stale timer that deletes a newer waiter blocks every
  later attachment for that session.
- Measure memory with `scenario-perf-baseline`; `ps` RSS misses graphics memory.

## Shortcuts

- macOS menu accelerators can swallow keys before the DOM sees them; remove
  conflicting predefined items in `src-tauri/core/src/lib.rs`. Handle Cmd+C through
  `GhosttyTerminal`'s `copy` event.
- Regression tests for native keyboard input must go through AppKit's shortcut
  dispatch. Keys delivered straight to the webview can skip it, as Ctrl+Return
  did.
- On Linux, plain Ctrl+letter belongs to the shell. App actions use Ctrl+Shift,
  or Ctrl+Alt when the macOS binding already has Shift.

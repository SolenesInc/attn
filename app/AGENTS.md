# Frontend (Tauri + React)

## Daemon connection

Paths are relative to `app/src`.

- Only `App` calls `hooks/useDaemonSocket.ts`; components use `useDaemonApi()`.
- Correlate requests through `hooks/daemonPendingRequests.ts`.
- Domain event handlers live in `hooks/daemon<Domain>Events.ts`.

## Tests

- Name tests `Source.concern.test.tsx`.
- Follow [Testing](../docs/testing.md). App wire tests render the real app
  with its real socket client against the scripted daemon in `src/test/`,
  which speaks the generated protocol types.
- Assert the exact requests the app sends after render settles, to catch
  fetch loops.
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
- PTY attachment deadlines belong to one pending waiter. Clear them when it settles;
  a stale timer deleting a newer waiter blocks every later attachment for that session.
- Measure memory with `scenario-perf-baseline`; `ps` RSS misses graphics memory.

## Shortcuts

- macOS menu accelerators can swallow keys before the DOM sees them; remove
  conflicting predefined items in `src-tauri/core/src/lib.rs`. Handle Cmd+C through
  `GhosttyTerminal`'s `copy` event.
- On Linux, plain Ctrl+letter belongs to the shell. App actions use Ctrl+Shift,
  or Ctrl+Alt when the macOS binding already has Shift.

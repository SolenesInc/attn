# Frontend (Tauri + React)

## Daemon connection

Paths are relative to `app/src`.

- Only `App` calls `hooks/useDaemonSocket.ts`; components use `useDaemonApi()`.
- Correlate requests through `hooks/daemonPendingRequests.ts`.
- Domain event handlers live in `hooks/daemon<Domain>Events.ts`.

## Tests

- Follow [Testing](../docs/testing.md). App wire tests render the real app
  with its real socket client against a scripted daemon that speaks the
  generated protocol types. The first test that needs that daemon builds it in
  `src/test/`.
- `createMockDaemon()` stubs `DaemonApi` above the socket. Do not add tests
  on it; tests that use it move to wire tests when their area is cleaned up.
- Name tests `Source.concern.test.tsx`.
- Assert the exact requests the app sends after render settles, to catch
  fetch loops.

## Terminal and GPU

- Resize order: model, renderer, paint, `onResize`, PTY SIGWINCH.
- Release hidden panes with `setSurfaceReleased(true)`. Reuse the WebGL context
  when font metrics change; recreating it exhausts WKWebView's pool.
- Measure memory with `scenario-perf-baseline`; `ps` RSS misses graphics memory.

## Shortcuts

- macOS menu accelerators can swallow keys before the DOM sees them; remove
  conflicting predefined items in `src-tauri/src/lib.rs`. Handle Cmd+C through
  `GhosttyTerminal`'s `copy` event.
- On Linux, plain Ctrl+letter belongs to the shell. App actions use Ctrl+Shift,
  or Ctrl+Alt when the macOS binding already has Shift.

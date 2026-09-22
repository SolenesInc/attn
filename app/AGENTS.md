# Frontend (Tauri + React)

## Daemon connection

Paths are relative to `app/src`.

- Only `App` calls `hooks/useDaemonSocket.ts`; components use `useDaemonApi()`.
- Correlate requests through `hooks/daemonPendingRequests.ts`.
- Domain event handlers live in `hooks/daemon<Domain>Events.ts`.

## Tests

- Name tests `Source.concern.test.tsx` and use `createMockDaemon()` from
  `src/test/mocks/daemon.ts`, as in `PresentRoot.test.tsx`.
- Assert exact daemon calls after render settles to catch fetch loops.

## Terminal and GPU

- Resize order: model, renderer, paint, `onResize`, PTY SIGWINCH.
- Release hidden panes with `setSurfaceReleased(true)`; reuse WebGL contexts,
  since WKWebView's pool runs out.
- Measure memory with `scenario-perf-baseline`; `ps` RSS misses graphics memory.

## Shortcuts

- macOS menu accelerators can swallow keys before the DOM sees them; remove
  conflicting predefined items in `src-tauri/src/lib.rs`. Handle Cmd+C through
  `GhosttyTerminal`'s `copy` event.
- On Linux, plain Ctrl+letter belongs to the shell. App actions use Ctrl+Shift,
  or Ctrl+Alt when the macOS binding already has Shift.

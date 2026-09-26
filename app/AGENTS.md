# Frontend (Tauri + React)

Daemon connection paths are relative to `app/src`.

## Daemon connection

- Only `App` calls `hooks/useDaemonSocket.ts`; components use `useDaemonApi()`
  from `contexts/DaemonApiContext.tsx`.
- Correlate commands/results through `hooks/daemonPendingRequests.ts`:
  `sendRequest` for fresh ids, `sendKeyedRequest` for last-writer-wins,
  `settlePendingRequest` for typed results.
- Add domain handlers in `hooks/daemon<Domain>Events.ts` and the socket switch's
  `default` chain.
- Markdown annotations use `<op>:<documentUri>` keys and `request_id` checks.
  The daemon validates typed file/seed source fields; URIs confer no authority.
- `store/daemonSessions.ts` holds session/PR state; `pty/` owns PTY transport.

## Tests

- Name tests `Source.concern.test.tsx`.
- Use `createMockDaemon()` from `src/test/mocks/daemon.ts`; helpers live in
  `src/test/utils.ts`. Assert exact calls after render settles to catch fetch loops.
  Example: `src/components/PresentRoot/PresentRoot.test.tsx`.
- Real browser APIs need a Playwright harness under `test-harness/harnesses/`,
  registered in `index.ts`, with a spec under `e2e/`.

## Terminal and GPU

- Resize order: model, renderer, paint, `onResize`, PTY SIGWINCH.
- Limit `will-change` and similar layer hints to visible components.
- Disable unused WebGL depth/stencil attachments.
- Hidden canvases retain buffers. Release inactive panes via
  `setSurfaceReleased(true)`; restore in a layout effect before reveal.
- Reuse WebGL contexts when changing font metrics; recreation exhausts WKWebView's pool.
- Measure `scenario-perf-baseline`'s `APP FOOTPRINT` and `paneSizedSurfaces`;
  `ps` RSS excludes graphics memory.

## Small traps

- `sendCreateWorktreeFromBranch` and local `sendCreateWorktree` share `_local`
  pending-action keys; do not run both concurrently.
- Reconnect's circuit breaker stays open until the user clicks retry.

## macOS shortcuts

- Native menu accelerators can consume keys before DOM keydown.
- Handle Cmd+C through `GhosttyTerminal`'s DOM `copy` event; verify with
  `real-app:scenario-terminal-block-copy`.
- Check `Menu::default`; remove conflicting predefined items in
  `src-tauri/src/lib.rs` so WebView rebindings work.
- Use `dispatch_native_shortcut` only for required visible native menu items;
  it hardcodes the action.

## Diagnostics

Use prefixed console logs/DevTools. For intermittent bugs, write
`$APPLOCALDATA/debug/<name>.jsonl` following `terminalDiagnosticsLog.ts` or
`terminalLinkHitTestLog.ts`; remove temporary instrumentation after the fix.

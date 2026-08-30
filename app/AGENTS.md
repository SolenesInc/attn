# Frontend (Tauri + React)

The root [AGENTS.md](../AGENTS.md) covers repository-wide safety and verification.

## Daemon connection

Connection paths below are relative to `app/src`.

- `hooks/useDaemonSocket.ts` owns connection, reconnect, the event switch,
  and every `send*` command. `App` is its only caller and publishes the returned
  API through `contexts/DaemonApiContext.tsx`. Components use `useDaemonApi()`.
- `hooks/daemonPendingRequests.ts` correlates commands with `*_result` events.
  `settlePendingRequest` settles the typed pending request. `sendRequest` uses a
  fresh request id; `sendKeyedRequest` uses a caller-supplied key for last-writer-wins
  commands. Both reject on a disconnected socket and time out on a silent daemon.
- `hooks/daemon<Domain>Events.ts` (`Fs`, `Notebook`, `MarkdownAnnotation`)
  holds the event handlers reached through the switch's `default`. Search a
  wire name such as `fs_write_result` to find its module. Add a domain with a
  new module and an entry in that chain.
- Markdown annotations use `<op>:<documentUri>` keys, last-writer-wins
  superseding, and `request_id` checks. File and seed messages carry typed
  source fields; the daemon validates those fields and never infers authority
  from the URI. `daemonMarkdownAnnotationEvents.ts` only decodes results.
- `store/daemonSessions.ts` holds session and PR state in Zustand.
- `pty/` handles transport, attach planning, binary frames, and runtime lifecycle.

Name tests `Source.concern.test.tsx`, with the suffix describing the behavior.

## Daemon calls in tests

Components read the daemon through `useDaemonApi()`. In tests,
`createMockDaemon()` (`src/test/mocks/daemon.ts`) hands out `create*()`
factories that record every call; `src/test/utils.ts` re-exports
testing-library plus `setupDefaultResponses()`, `waitForCalls()`, and
`assertNoMoreCalls()`. Assert exact call counts after a render settles; that
is how a fetch loop or a duplicate request shows up. Canonical example:
`src/components/PresentRoot/PresentRoot.test.tsx`. A component that needs real
browser APIs (shadow DOM, `adoptedStyleSheets`, drag and drop) gets a
Playwright harness under `test-harness/harnesses/`, registered in its
`index.ts`, with its own spec under `e2e/`; `test-harness/types.ts` documents
`window.__HARNESS__`.

## Terminal

In `GhosttyTerminal.tsx` a resize goes model, then renderer, then paint, then
`onResize` to the PTY. The PTY's SIGWINCH is the last step.

## GPU memory

- `will-change` on an always-mounted component. The right dock mounts all
  five panels and only toggles a class, so a permanent hint gives every closed
  one a full-height backing store it never draws. Promote under the open state,
  and check whether the component is conditionally rendered before reaching
  for the hint at all. The same applies to `translateZ`, `backdrop-filter`,
  `isolation`, and `contain`.
- A WebGL context hands you attachments you did not ask for. `depth` is
  `true` unless you say otherwise, and every attachment is another
  drawing-buffer-sized allocation per canvas. Ask for what you read; the
  terminal renderer reads neither depth nor stencil.
- A hidden canvas still owns its drawing buffer. `display:none` hides an
  element; the buffer is sized by the canvas's width/height attributes. An
  inactive session's panes hand theirs back via `setSurfaceReleased(true)` on
  the terminal handle, driven from `SessionTerminalWorkspace`. A new surface
  that survives being off-screen gets the same treatment, restored in a layout
  effect so the repaint precedes the frame that reveals it.

Never resolve one of these by tearing down and rebuilding a WebGL context.
WKWebView's live-context pool is small enough that rebuilding every mounted
pane loses contexts and permanently breaks panes. The font-size effect in
`GhosttyTerminal.tsx` re-metrics in place for this reason.

Measure with `scenario-perf-baseline`'s `APP FOOTPRINT` and its
`paneSizedSurfaces` histogram. `ps` RSS cannot see graphics memory at all.

## Small traps

- `sendCreateWorktreeFromBranch` hardcodes the `_local` pending-action key
  that `sendCreateWorktree` derives from the endpoint, so the two collide on a
  local repo. Do not run both at once.
- The reconnect circuit breaker opens after repeated failures and stays open
  until the user clicks retry. Nothing resets it on a timer.

## macOS shortcuts

Packaged-app menu accelerators can consume shortcuts before DOM keydown.

- Handle Cmd+C through the DOM `copy` event in `GhosttyTerminal`; keydown alone
  misses it. Verify with `real-app:scenario-terminal-block-copy`.
- Check new shortcuts against `Menu::default` accelerators.
- Remove conflicting predefined menu items in `src-tauri/src/lib.rs` so the
  WebView resolver handles rebindings.
- Use `dispatch_native_shortcut` only for a required visible or relabeled
  native menu item; it hardcodes the action.

## Diagnostics

Use prefixed console logging and Tauri DevTools. For hard-to-reproduce UI bugs,
write JSONL under `$APPLOCALDATA/debug/<name>.jsonl`, following
`terminalDiagnosticsLog.ts` or `terminalLinkHitTestLog.ts`. Remove temporary
instrumentation after fixing the bug.

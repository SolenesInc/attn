# Frontend (Tauri + React)

The root `AGENTS.md` maps `app/src` (socket, stores, event modules, tests).
This file holds what the code does not say.

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

## GPU surfaces outnumber what you can see

Three traps, each worth over 100MB at rest. Receipts and the measurement
recipe:
[docs/plans/2026-08-14-app-memory-floor.md](../docs/plans/2026-08-14-app-memory-floor.md).

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

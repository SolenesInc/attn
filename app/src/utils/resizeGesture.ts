import { lockTextSelection } from './dragLock';

const TERMINAL_MOUSE_SUPPRESSION_MS = 1_500;

const TERMINAL_MOUSE_RELEASE_GUARD_MS = 150;

let lastResizeToken = 0;

export function suppressTerminalMouseDuringResize(
  durationMs = TERMINAL_MOUSE_SUPPRESSION_MS,
): void {
  document.documentElement.dataset.attnDesktopMouseSuppressUntil = String(
    Date.now() + durationMs,
  );
}

export function beginPointerResize(
  element: HTMLElement,
  pointerId: number,
  cursor: string,
): () => void {
  if (typeof element.setPointerCapture === 'function') {
    try {
      element.setPointerCapture(pointerId);
    } catch {}
  }
  lastResizeToken += 1;
  const resizeToken = String(lastResizeToken);
  suppressTerminalMouseDuringResize();
  document.documentElement.dataset.attnDesktopResizing = '1';
  document.documentElement.dataset.attnDesktopResizeToken = resizeToken;
  const releaseSelectionLock = lockTextSelection(cursor);
  return () => {
    if (
      typeof element.hasPointerCapture === 'function' &&
      typeof element.releasePointerCapture === 'function' &&
      element.hasPointerCapture(pointerId)
    ) {
      try {
        element.releasePointerCapture(pointerId);
      } catch {}
    }
    if (document.documentElement.dataset.attnDesktopResizeToken === resizeToken) {
      delete document.documentElement.dataset.attnDesktopResizing;
      delete document.documentElement.dataset.attnDesktopResizeToken;
    }
    suppressTerminalMouseDuringResize(TERMINAL_MOUSE_RELEASE_GUARD_MS);
    releaseSelectionLock();
  };
}

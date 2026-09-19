import { useEffect, useLayoutEffect, useRef, type RefObject } from 'react';

// Every Escape dismiss handler must go through this hook, or LIFO ordering breaks.
// Capture phase is deliberate: it beats terminal and element-level handlers to the key.

interface EscapeEntry {
  handler: () => void;
  consume: boolean;
}

const stack: Array<RefObject<EscapeEntry>> = [];

let installedListener: ((e: KeyboardEvent) => void) | null = null;

function ensureInstalled() {
  if (installedListener) return;
  installedListener = (e: KeyboardEvent) => {
    if (e.key !== 'Escape') return;
    for (const entry of [...stack].reverse()) {
      const { handler, consume } = entry.current;
      if (consume) {
        e.preventDefault();
        e.stopPropagation(); // Prevent terminal and other element handlers from also seeing it.
      }
      handler();
      if (consume) return;
    }
  };
  window.addEventListener('keydown', installedListener, true); // Capture phase fires before terminal input.
}

export function useEscapeStack(handler: () => void, enabled: boolean, options?: { consume: boolean }): void {
  // Stable ref so the stack entry never needs replacing when handler changes.
  const consume = options?.consume ?? true;
  const ref = useRef({ handler, consume });
  useLayoutEffect(() => { ref.current = { handler, consume }; }, [handler, consume]);

  useEffect(() => {
    ensureInstalled();
    if (!enabled) return;
    stack.push(ref);
    return () => {
      const i = stack.lastIndexOf(ref);
      if (i !== -1) stack.splice(i, 1);
    };
  }, [enabled]); // only re-register when open/closed, not on every handler change
}

/** Exposed for test teardown only. Do not call in production code. */
export function _resetEscapeStackForTest(): void {
  stack.length = 0;
  if (installedListener) {
    window.removeEventListener('keydown', installedListener, true);
    installedListener = null;
  }
}

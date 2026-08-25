import { useEffect, useRef } from 'react';

// Every Escape dismiss handler must go through this hook, or LIFO ordering breaks.
// Capture phase is deliberate: it beats terminal and element-level handlers to the key.

const stack: Array<() => void> = [];

let installedListener: ((e: KeyboardEvent) => void) | null = null;

function ensureInstalled() {
  if (installedListener) return;
  installedListener = (e: KeyboardEvent) => {
    if (e.key !== 'Escape') return;
    const top = stack[stack.length - 1];
    if (top) {
      e.preventDefault();
      e.stopPropagation(); // Prevent terminal and other element handlers from also seeing it.
      top();
    }
  };
  window.addEventListener('keydown', installedListener, true); // Capture phase fires before terminal input.
}

export function useEscapeStack(handler: () => void, enabled: boolean): void {
  // Stable ref so the stack entry never needs replacing when handler changes.
  const ref = useRef(handler);
  ref.current = handler;

  useEffect(() => {
    ensureInstalled();
    if (!enabled) return;
    const fn = () => ref.current();
    stack.push(fn);
    return () => {
      const i = stack.lastIndexOf(fn);
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

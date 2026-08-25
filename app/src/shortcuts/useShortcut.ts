import { useEffect, useRef } from 'react';
import { SHORTCUTS, ShortcutId, matchesShortcut, isChord } from './registry';
import { resolvedShortcutEntries } from './resolver';
import { enterLeader, resolvePendingThen } from './chordState';
import { matchChordLeader } from './chordDispatch';

type Handler = () => void;
const NATIVE_SHORTCUT_EVENT = 'attn:native-shortcut';

const handlers = new Map<ShortcutId, Set<Handler>>();

export function triggerShortcut(id: ShortcutId): boolean {
  const shortcutHandlers = handlers.get(id);
  if (!shortcutHandlers || shortcutHandlers.size === 0) {
    return false;
  }
  for (const handler of shortcutHandlers) {
    handler();
  }
  return true;
}

/** Whether any component currently has a handler registered for this id. */
export function hasHandler(id: ShortcutId): boolean {
  const set = handlers.get(id);
  return !!set && set.size > 0;
}

// While the shortcut editor is capturing a keystroke the global dispatcher must stand down, so
// recording a combo never fires its action. Registration order can't be relied on.
let captureSuspended = false;
export function setShortcutCaptureSuspended(suspended: boolean): void {
  captureSuspended = suspended;
}

let listenerInstalled = false;

function installGlobalListener() {
  if (listenerInstalled) return;
  listenerInstalled = true;

  window.addEventListener('keydown', (e: KeyboardEvent) => {
    if (captureSuspended) return;

    // A pending leader owns the next keystroke entirely: always consume, so it can't fall through
    // to a single combo or leak into the terminal PTY.
    const pendingThen = resolvePendingThen(e);
    if (pendingThen.kind !== 'none') {
      e.preventDefault();
      e.stopPropagation();
      if (pendingThen.kind === 'fired') triggerShortcut(pendingThen.id);
      return;
    }

    const editableTarget = isNonTerminalEditableTarget(e.target);
    const terminalTarget = isTerminalTarget(e.target);
    for (const [id, def] of resolvedShortcutEntries()) {
      if (id === 'terminal.close' && !terminalTarget) {
        continue;
      }
      if (isChord(def)) {
        continue; // chord leaders are armed in the pass below
      }
      if (matchesShortcut(e, def)) {
        if (editableTarget && def.editableTarget === 'native') {
          continue;
        }
        const shortcutHandlers = handlers.get(id);
        if (shortcutHandlers && shortcutHandlers.size > 0) {
          if (id === 'session.refreshPRs' && terminalTarget) {
            return;
          }
          e.preventDefault();
          e.stopPropagation();
          triggerShortcut(id);
          return;
        }
      }
    }

    // A bound leader is always consumed even with no handler, except in non-terminal editable
    // targets, where it would swallow a keystroke meant for an input.
    if (!editableTarget) {
      const chord = matchChordLeader(e);
      if (chord) {
        const fireable = chord.candidates.filter((c) => hasHandler(c.id));
        if (fireable.length > 0) {
          enterLeader(chord.leader, fireable);
        }
        e.preventDefault();
        e.stopPropagation();
        return;
      }
    }
  }, true); // Capture phase to get events before terminal input.

  window.addEventListener(NATIVE_SHORTCUT_EVENT, (event) => {
    const shortcutId = (event as CustomEvent<unknown>).detail;
    if (
      typeof shortcutId === 'string'
      && Object.prototype.hasOwnProperty.call(SHORTCUTS, shortcutId)
    ) {
      triggerShortcut(shortcutId as ShortcutId);
    }
  });
}

function isTerminalTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.closest('.terminal-container, .session-terminal-workspace') !== null;
}

function isNonTerminalEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement) || target.closest('.terminal-container')) {
    return false;
  }
  return target.closest('input, textarea, select, [contenteditable]:not([contenteditable="false"])') !== null;
}

export function useShortcut(id: ShortcutId, handler: Handler, enabled = true): void {
  const handlerRef = useRef(handler);
  handlerRef.current = handler;

  useEffect(() => {
    installGlobalListener();

    if (!enabled) return;

    const wrappedHandler = () => handlerRef.current();

    if (!handlers.has(id)) {
      handlers.set(id, new Set());
    }
    handlers.get(id)!.add(wrappedHandler);

    return () => {
      handlers.get(id)?.delete(wrappedHandler);
    };
  }, [id, enabled]);
}

import { useEffect, useRef } from 'react';
import { SHORTCUTS, ShortcutId, matchesShortcut, isChord } from './registry';
import { resolvedShortcutEntries } from './resolver';
import { isMacLikePlatform, isShellCtrlLetter } from './platform';
import { enterLeader, resolvePendingThen } from './chordState';
import { matchChordLeader } from './chordDispatch';
import { isRetainedTerminalEvent } from '../store/pendingTerminalInput';
import { recordAppAction } from '../analytics/appActions';
import { isDomInstance } from '../utils/realm';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { SHORTCUT_META } from './metadata';
import { claimPaletteFocus } from '../components/palette/paletteClaim';

export interface ShortcutSource {
  desktopId: string;
  leafId: string;
}

export interface ShortcutScope {
  desktopId?: string;
  leafId?: string;
  active: boolean;
}

type Handler = (source?: ShortcutSource) => void;
interface Registration {
  handler: Handler;
  scope?: ShortcutScope;
}
const NATIVE_SHORTCUT_EVENT = 'attn:native-shortcut';

const handlers = new Map<ShortcutId, Set<Registration>>();

function eligible(id: ShortcutId, source?: ShortcutSource): Registration[] {
  return [...handlers.get(id) ?? []].filter(({ scope }) => (
    source ? (!!scope
      && (!scope.desktopId || scope.desktopId === source.desktopId)
      && (!scope.leafId || scope.leafId === source.leafId)) : (!scope || scope.active)
  ));
}

function isTileShortcut(id: ShortcutId): boolean {
  return SHORTCUT_META[id].category === 'panes'
    || id === 'markdown.sendAnnotations' || id === 'terminal.sendAnnotations';
}

export function dispatchShortcut(id: ShortcutId, source: ShortcutSource | undefined, target: Window): void {
  if (!source || isTileShortcut(id)) {
    triggerShortcut(id, source);
    return;
  }
  if (id === 'file.open' && claimPaletteFocus(target.document.activeElement)) return;
  void invoke('tile_window_raise_main').then(() => triggerShortcut(id)).catch((error) => {
    console.error('[TileWindow] could not raise main window:', error);
  });
}

export function triggerShortcut(id: ShortcutId, source?: ShortcutSource): boolean {
  const shortcutHandlers = eligible(id, source);
  if (shortcutHandlers.length === 0) {
    return false;
  }
  recordAppAction('shortcut', { id });
  for (const { handler } of shortcutHandlers) {
    handler(source);
  }
  return true;
}

/** Whether any component currently has a handler registered for this id. */
export function hasHandler(id: ShortcutId, source?: ShortcutSource): boolean {
  return eligible(id, source).length > 0;
}

// While the shortcut editor is capturing a keystroke the global dispatcher must stand down, so
// recording a combo never fires its action. Registration order can't be relied on.
let captureSuspended = false;
export function setShortcutCaptureSuspended(suspended: boolean): void {
  captureSuspended = suspended;
  if (isTauri() && isMacLikePlatform()) {
    void invoke('browser_shortcut_capture', { suspended }).catch((error) => {
      console.error('[BrowserHost] Failed to change shortcut capture:', error);
    });
  }
}

function dispatchKeydown(e: KeyboardEvent, target: Window, source?: ShortcutSource) {
    if (captureSuspended || isRetainedTerminalEvent(e)) return;

    // A pending leader owns the next keystroke entirely: always consume, so it can't fall through
    // to a single combo or leak into the terminal PTY.
    const pendingThen = resolvePendingThen(e);
    if (pendingThen.kind !== 'none') {
      e.preventDefault();
      e.stopPropagation();
      if (pendingThen.kind === 'fired') dispatchShortcut(pendingThen.id, source, target);
      return;
    }

    const terminalTarget = isTerminalTarget(e.target);
    if (terminalTarget && isShellCtrlLetter(e)) {
      return;
    }

    const editableTarget = isNonTerminalEditableTarget(e.target);
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
        if (hasHandler(id, source && isTileShortcut(id) ? source : undefined)) {
          if (id === 'session.refreshPRs' && terminalTarget) {
            return;
          }
          e.preventDefault();
          e.stopPropagation();
          dispatchShortcut(id, source, target);
          return;
        }
      }
    }

    // A bound leader is always consumed even with no handler, except in non-terminal editable
    // targets, where it would swallow a keystroke meant for an input.
    if (!editableTarget) {
      const chord = matchChordLeader(e);
      if (chord) {
        const fireable = chord.candidates.filter((c) => hasHandler(c.id, source && isTileShortcut(c.id) ? source : undefined));
        if (fireable.length > 0) {
          enterLeader(chord.leader, fireable);
        }
        e.preventDefault();
        e.stopPropagation();
        return;
      }
    }
}

/** Shortcuts reach a popped-out tile window only through its own keydown listener. */
export function listenForShortcuts(target: Window, source?: () => ShortcutSource | undefined): void {
  target.addEventListener('keydown', (event) => dispatchKeydown(event, target, source?.()), true);
  target.addEventListener(NATIVE_SHORTCUT_EVENT, (event) => {
    const shortcutId = (event as CustomEvent<unknown>).detail;
    if (
      typeof shortcutId !== 'string'
      || !Object.prototype.hasOwnProperty.call(SHORTCUTS, shortcutId)
    ) return;
    const origin = source?.();
    dispatchShortcut(shortcutId as ShortcutId, origin, target);
  });
}

let listenerInstalled = false;

function installGlobalListener() {
  if (listenerInstalled) return;
  listenerInstalled = true;

  listenForShortcuts(window);
}

function isTerminalTarget(target: EventTarget | null): boolean {
  if (!isDomInstance(target, 'HTMLElement')) return false;
  return target.closest('.terminal-container, .session-terminal-desktop') !== null;
}

function isNonTerminalEditableTarget(target: EventTarget | null): boolean {
  if (!isDomInstance(target, 'HTMLElement') || target.closest('.terminal-container')) {
    return false;
  }
  return target.closest('input, textarea, select, [contenteditable]:not([contenteditable="false"])') !== null;
}

export function useShortcut(id: ShortcutId, handler: Handler, enabled = true, scope?: ShortcutScope): void {
  const handlerRef = useRef(handler);
  handlerRef.current = handler;
  const scopeRef = useRef(scope);
  scopeRef.current = scope;

  useEffect(() => {
    installGlobalListener();

    if (!enabled) return;

    const registration: Registration = {
      handler: (source) => handlerRef.current(source),
      get scope() { return scopeRef.current; },
    };

    if (!handlers.has(id)) {
      handlers.set(id, new Set());
    }
    handlers.get(id)!.add(registration);

    return () => {
      handlers.get(id)?.delete(registration);
    };
  }, [id, enabled]);
}

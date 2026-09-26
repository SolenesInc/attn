import { useEffect } from 'react';
import { isAccelKeyPressed } from '../../shortcuts/platform';
import type { DraftDesktopView, DraftView } from './migrationDraft';
import { useLatest } from './useLatest';

export interface PickerKeyboard {
  active: boolean;
  view: DraftView | null;
  selected: string | null;
  select: (groupId: string) => void;
  requestMove: (desktop: DraftDesktopView) => void;
  keepSelected: () => void;
  openMove: (groupId: string) => void;
  undo: () => void;
}

function isTextEntry(target: EventTarget | null): boolean {
  const tag = (target as HTMLElement | null)?.tagName ?? '';
  return tag === 'INPUT' || tag === 'SELECT' || tag === 'TEXTAREA';
}

function stepSelection(state: PickerKeyboard, view: DraftView, down: boolean) {
  const rows = view.groups;
  const index = rows.findIndex((row) => row.group.group_id === state.selected);
  const nextId = rows[Math.max(0, Math.min(rows.length - 1, index + (down ? 1 : -1)))]?.group.group_id;
  if (!nextId) return;
  state.select(nextId);
  document.querySelector<HTMLElement>(`[data-select-group="${CSS.escape(nextId)}"]`)?.focus();
}

function handleKey(state: PickerKeyboard, view: DraftView, event: KeyboardEvent): boolean {
  const key = event.key.toLowerCase();
  if (isAccelKeyPressed(event) && !event.altKey && !event.shiftKey && key === 'z') {
    state.undo();
    return true;
  }
  if (event.metaKey || event.ctrlKey || event.altKey) return false;
  if (/^[1-9]$/.test(event.key)) {
    const desktop = view.slots.find((entry) => entry.desktop.shortcut_slot === Number(event.key));
    if (desktop) state.requestMove(desktop);
    return Boolean(desktop);
  }
  if (key === 'k') {
    state.keepSelected();
    return true;
  }
  if (key === 'm' && state.selected) {
    state.openMove(state.selected);
    return true;
  }
  if (event.key === 'ArrowUp' || event.key === 'ArrowDown') {
    stepSelection(state, view, event.key === 'ArrowDown');
    return true;
  }
  return false;
}

export function usePickerKeyboard(keyboard: PickerKeyboard) {
  const latest = useLatest(keyboard);
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      const state = latest.current;
      if (!state.active || !state.view || event.defaultPrevented || isTextEntry(event.target)) return;
      if (handleKey(state, state.view, event)) event.preventDefault();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [latest]);
}

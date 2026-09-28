import type { Desktop, LeafMoved } from '../types/generated';
import { leafSlotId } from '../types/desktop';
import { desktopLeaves } from './activeLeaf';

export const LEAF_HISTORY_LIMIT = 1024;

export type LeafHistoryDirection = 'back' | 'forward';

export interface LeafRef {
  leafId: string;
  lastKnownDesktopId: string;
}

export interface LeafHistoryState {
  entries: LeafRef[];
  cursor: number;
}

export interface LeafHistoryMove {
  state: LeafHistoryState;
  cursor: number;
  target: LeafRef | null;
}

export function createLeafHistory(): LeafHistoryState {
  return { entries: [], cursor: -1 };
}

function sameRef(a: LeafRef | undefined, b: LeafRef): boolean {
  return a?.leafId === b.leafId && a.lastKnownDesktopId === b.lastKnownDesktopId;
}

export function recordLeafVisit(state: LeafHistoryState, ref: LeafRef): LeafHistoryState {
  if (sameRef(state.entries[state.cursor], ref)) return state;
  const entries = [...state.entries.slice(0, state.cursor + 1), ref];
  const discarded = Math.max(0, entries.length - LEAF_HISTORY_LIMIT);
  return {
    entries: discarded > 0 ? entries.slice(discarded) : entries,
    cursor: entries.length - 1 - discarded,
  };
}

export function remapLeafHistory(state: LeafHistoryState, moved: LeafMoved): LeafHistoryState {
  let changed = false;
  const entries = state.entries.map((entry) => {
    if (entry.leafId !== moved.from_leaf_id || entry.lastKnownDesktopId !== moved.from_desktop_id) return entry;
    changed = true;
    return { leafId: moved.to_leaf_id, lastKnownDesktopId: moved.to_desktop_id };
  });
  return changed ? { entries, cursor: state.cursor } : state;
}

function locate(ref: LeafRef, desktops: Desktop[]): LeafRef | null {
  const known = desktops.find((desktop) => desktop.id === ref.lastKnownDesktopId);
  if (known && desktopLeaves(known).some((leaf) => leafSlotId(leaf) === ref.leafId)) return ref;
  const holders = desktops.filter((desktop) => desktopLeaves(desktop).some((leaf) => leafSlotId(leaf) === ref.leafId));
  return holders.length === 1 ? { leafId: ref.leafId, lastKnownDesktopId: holders[0].id } : null;
}

export function reconcileLeafHistory(state: LeafHistoryState, desktops: Desktop[]): LeafHistoryState {
  if (state.entries.length === 0) return state.cursor === -1 ? state : createLeafHistory();
  const located = state.entries.map((entry) => locate(entry, desktops));
  const entries = located.filter((entry): entry is LeafRef => entry !== null);
  if (entries.length === 0) return createLeafHistory();
  const before = state.cursor < 0 ? 0 : located.slice(0, state.cursor).filter(Boolean).length;
  const cursor = Math.min(before, entries.length - 1);
  const unchanged = cursor === state.cursor
    && entries.length === state.entries.length
    && entries.every((entry, index) => entry === state.entries[index]);
  return unchanged ? state : { entries, cursor };
}

export function moveLeafHistory(
  state: LeafHistoryState,
  direction: LeafHistoryDirection,
  desktops: Desktop[],
  resumeCurrent: boolean,
): LeafHistoryMove {
  const reconciled = reconcileLeafHistory(state, desktops);
  const none = { state: reconciled, cursor: reconciled.cursor, target: null };
  if (reconciled.entries.length === 0) return none;
  if (resumeCurrent) {
    return direction === 'back'
      ? { state: reconciled, cursor: reconciled.cursor, target: reconciled.entries[reconciled.cursor] ?? null }
      : none;
  }
  const cursor = reconciled.cursor + (direction === 'back' ? -1 : 1);
  if (cursor < 0 || cursor >= reconciled.entries.length) return none;
  return { state: reconciled, cursor, target: reconciled.entries[cursor] };
}

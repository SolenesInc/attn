import { create } from 'zustand';
import type { Desktop, Setup } from '../types/generated';
import { persistSelectedSetupId } from '../utils/selectedSetup';

export interface SetupsState {
  setups: Setup[];
  selectedSetupId: string | null;
  desktops: Desktop[];
  previousDesktopId: string | null;
  selection: PendingSelection | null;
  selectionStarted: (setupId: string) => void;
  enterScope: (setups: Setup[] | undefined, selectedSetupId: string | undefined, desktops: Desktop[] | undefined) => void;
  selectedSetupChanged: (setup: Setup, desktops: Desktop[]) => void;
  setupsChanged: (setups: Setup[]) => void;
  arrangementChanged: (setup: Setup, desktops: Desktop[], deletedDesktopIds: string[] | undefined) => void;
}

interface PendingSelection {
  setupId: string;
  earlySetup: Setup | null;
  earlyDesktops: Desktop[];
  earlyDeletedIds: string[];
}

function currentDesktopIdOf(setups: Setup[], setupId: string | null): string | null {
  return setups.find((setup) => setup.id === setupId)?.current_desktop_id ?? null;
}

function followCurrentDesktop(
  state: SetupsState,
  setups: Setup[],
  desktops: Desktop[],
): Pick<SetupsState, 'previousDesktopId'> {
  const before = currentDesktopIdOf(state.setups, state.selectedSetupId);
  const after = currentDesktopIdOf(setups, state.selectedSetupId);
  const previous = before && after && before !== after ? before : state.previousDesktopId;
  const stillExists = previous !== null && previous !== after && desktops.some((desktop) => desktop.id === previous);
  return { previousDesktopId: stillExists ? previous : null };
}

function withSetup(setups: Setup[], setup: Setup): Setup[] {
  const index = setups.findIndex((entry) => entry.id === setup.id);
  if (index < 0) return [...setups, setup];
  const next = setups.slice();
  next[index] = setup;
  return next;
}

function newerOf(held: Desktop, incoming: Desktop | undefined): Desktop {
  return incoming && incoming.revision >= held.revision ? incoming : held;
}

function mergeDesktops(current: Desktop[], changed: Desktop[], deletedIds: string[] | undefined): Desktop[] {
  const deleted = new Set(deletedIds ?? []);
  const changedById = new Map(changed.map((desktop) => [desktop.id, desktop]));
  const merged = current
    .filter((desktop) => !deleted.has(desktop.id))
    .map((desktop) => newerOf(desktop, changedById.get(desktop.id)));
  const known = new Set(merged.map((desktop) => desktop.id));
  return [...merged, ...changed.filter((desktop) => !known.has(desktop.id) && !deleted.has(desktop.id))];
}

function scopeTo(setups: Setup[], setupId: string | null, desktops: Desktop[]): Partial<SetupsState> {
  persistSelectedSetupId(setupId ?? undefined);
  return { setups, selectedSetupId: setupId, desktops, previousDesktopId: null, selection: null };
}

function heldForSelection(selection: PendingSelection, setup: Setup, desktops: Desktop[], deletedIds: string[] | undefined): PendingSelection {
  return {
    ...selection,
    earlySetup: setup,
    earlyDesktops: mergeDesktops(selection.earlyDesktops, desktops, undefined),
    earlyDeletedIds: [...selection.earlyDeletedIds, ...(deletedIds ?? [])],
  };
}

function withEarlyArrival(selection: PendingSelection | null, setup: Setup, desktops: Desktop[]): { setup: Setup; desktops: Desktop[] } {
  if (!selection || selection.setupId !== setup.id) return { setup, desktops };
  return {
    setup: selection.earlySetup ?? setup,
    desktops: mergeDesktops(desktops, selection.earlyDesktops, selection.earlyDeletedIds),
  };
}

export const useSetupsStore = create<SetupsState>((set) => ({
  setups: [],
  selectedSetupId: null,
  desktops: [],
  previousDesktopId: null,
  selection: null,

  selectionStarted: (setupId) =>
    set({ selection: { setupId, earlySetup: null, earlyDesktops: [], earlyDeletedIds: [] } }),

  enterScope: (setups, selectedSetupId, desktops) =>
    set(() => scopeTo(setups ?? [], selectedSetupId || null, desktops ?? [])),

  selectedSetupChanged: (setup, desktops) =>
    set((state) => {
      const settled = withEarlyArrival(state.selection, setup, desktops);
      return scopeTo(withSetup(state.setups, settled.setup), settled.setup.id, settled.desktops);
    }),

  setupsChanged: (setups) =>
    set((state) => ({ setups, ...followCurrentDesktop(state, setups, state.desktops) })),

  arrangementChanged: (setup, desktops, deletedDesktopIds) =>
    set((state) => {
      if (setup.id !== state.selectedSetupId && state.selection?.setupId === setup.id) {
        return { selection: heldForSelection(state.selection, setup, desktops, deletedDesktopIds) };
      }
      if (setup.id !== state.selectedSetupId) {
        const selectedStillLive = state.setups.some((entry) => entry.id === state.selectedSetupId);
        return selectedStillLive ? state : scopeTo(withSetup(state.setups, setup), setup.id, desktops);
      }
      const setups = withSetup(state.setups, setup);
      const merged = mergeDesktops(state.desktops, desktops, deletedDesktopIds);
      return { setups, desktops: merged, ...followCurrentDesktop(state, setups, merged) };
    }),
}));

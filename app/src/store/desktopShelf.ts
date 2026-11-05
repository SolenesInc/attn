import { create } from 'zustand';
import type { FirstColumn } from '../components/SessionTerminalDesktop/rowLayout';
import type { UISessionState } from '../types/sessionState';
import type { AskHolderSession } from '../utils/asks';
import { useProfilesStore } from './profiles';

export interface ShelfTile {
  leafId: string;
  kind: string;
  state?: UISessionState;
  offline?: boolean;
  askHolder?: AskHolderSession;
}

export interface ShelfColumn {
  key: string;
  goLeafId: string;
  label: string;
  tiles: ShelfTile[];
}

export interface PublishedShelf {
  before: ShelfColumn[];
  screen: ShelfColumn[];
  after: ShelfColumn[];
  activeLeafId: string;
  narrowestCols: number;
}

interface DesktopShelfState {
  firstColumnByDesktop: Readonly<Record<string, FirstColumn>>;
  shelfByDesktop: Readonly<Record<string, PublishedShelf>>;
  setFirstColumn: (desktopId: string, firstColumn: FirstColumn) => void;
  publishShelf: (desktopId: string, shelf: PublishedShelf | null) => void;
  forgetDesktop: (desktopId: string) => void;
}

function without<T>(record: Readonly<Record<string, T>>, key: string): Readonly<Record<string, T>> {
  if (!(key in record)) return record;
  const { [key]: _removed, ...rest } = record;
  return rest;
}

export const useDesktopShelfStore = create<DesktopShelfState>((set) => ({
  firstColumnByDesktop: {},
  shelfByDesktop: {},
  setFirstColumn: (desktopId, firstColumn) =>
    set((state) => {
      const current = state.firstColumnByDesktop[desktopId];
      if (current?.leafId === firstColumn.leafId && current.index === firstColumn.index) return state;
      return { firstColumnByDesktop: { ...state.firstColumnByDesktop, [desktopId]: firstColumn } };
    }),
  publishShelf: (desktopId, shelf) =>
    set((state) => {
      const current = state.shelfByDesktop[desktopId];
      if (!shelf) return current ? { shelfByDesktop: without(state.shelfByDesktop, desktopId) } : state;
      if (current && JSON.stringify(current) === JSON.stringify(shelf)) return state;
      return { shelfByDesktop: { ...state.shelfByDesktop, [desktopId]: shelf } };
    }),
  forgetDesktop: (desktopId) =>
    set((state) => ({
      firstColumnByDesktop: without(state.firstColumnByDesktop, desktopId),
      shelfByDesktop: without(state.shelfByDesktop, desktopId),
    })),
}));

useProfilesStore.subscribe((state, previous) => {
  if (state.desktops === previous.desktops) return;
  const current = new Set(state.desktops.map((desktop) => desktop.id));
  const { firstColumnByDesktop, shelfByDesktop, forgetDesktop } = useDesktopShelfStore.getState();
  for (const desktopId of new Set([...Object.keys(firstColumnByDesktop), ...Object.keys(shelfByDesktop)])) {
    if (!current.has(desktopId)) forgetDesktop(desktopId);
  }
});

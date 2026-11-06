import { create } from 'zustand';
import type { FirstColumn } from '../components/SessionTerminalDesktop/rowLayout';
import type { UISessionState } from '../types/sessionState';
import type { AskHolderSession } from '../utils/asks';
import { useProfilesStore } from './profiles';

export interface ShelfTile {
  leafId: string;
  kind: string;
  title: string;
  terminalId?: string;
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
  tileHeightPx: number;
}

export interface DocumentReturn {
  firstColumn: FirstColumn;
  shownAtLeafId: string;
  activeLeafId: string;
}

const NONE: readonly string[] = [];

function withEntry<T>(record: Readonly<Record<string, T>>, key: string, value: T | undefined): Readonly<Record<string, T>> {
  return value === undefined ? without(record, key) : { ...record, [key]: value };
}

function listWithout(list: readonly string[] | undefined, drop: (id: string) => boolean): readonly string[] | undefined {
  const kept = (list ?? NONE).filter((id) => !drop(id));
  return kept.length ? kept : undefined;
}

export interface ShelfDropMark {
  desktopId: string;
  anchorId: string;
  edge: 'left' | 'right';
}

export interface DesktopRowActions {
  beginChipDrag: (column: { key: string; leafId: string }, event: React.PointerEvent) => void;
  clearDraggedWidths: () => void;
}

export interface ShelfPeekAnchor {
  left: number;
  right: number;
  top: number;
  bottom: number;
}

export interface ShelfPeek {
  desktopId: string;
  leafId: string;
  anchor: ShelfPeekAnchor | null;
}

interface DesktopShelfState {
  firstColumnByDesktop: Readonly<Record<string, FirstColumn>>;
  shelfByDesktop: Readonly<Record<string, PublishedShelf>>;
  peek: ShelfPeek | null;
  setPeek: (peek: ShelfPeek | null) => void;
  heldByDesktop: Readonly<Record<string, readonly string[]>>;
  unseenByDesktop: Readonly<Record<string, readonly string[]>>;
  flashingByDesktop: Readonly<Record<string, readonly string[]>>;
  documentReturnsByDesktop: Readonly<Record<string, Readonly<Record<string, DocumentReturn>>>>;
  leafDragging: boolean;
  setLeafDragging: (dragging: boolean) => void;
  shelfDrop: ShelfDropMark | null;
  setShelfDrop: (mark: ShelfDropMark | null) => void;
  rowActionsByDesktop: Readonly<Record<string, DesktopRowActions>>;
  setRowActions: (desktopId: string, actions: DesktopRowActions | null) => void;
  setFirstColumn: (desktopId: string, firstColumn: FirstColumn) => void;
  publishShelf: (desktopId: string, shelf: PublishedShelf | null) => void;
  forgetDesktop: (desktopId: string) => void;
  holdLeaf: (desktopId: string, leafId: string) => void;
  releaseLeaf: (desktopId: string, leafId: string) => void;
  markUnseen: (desktopId: string, leafId: string) => void;
  flash: (desktopId: string, leafIds: readonly string[]) => void;
  flashEnded: (desktopId: string, leafIds: readonly string[]) => void;
  saveDocumentReturn: (desktopId: string, leafId: string, documentReturn: DocumentReturn) => void;
  settleLeaves: (desktopId: string, row: ReadonlySet<string>, onScreen: ReadonlySet<string>, activeLeafId: string) => void;
}

function without<T>(record: Readonly<Record<string, T>>, key: string): Readonly<Record<string, T>> {
  if (!(key in record)) return record;
  const { [key]: _removed, ...rest } = record;
  return rest;
}

export const useDesktopShelfStore = create<DesktopShelfState>((set) => ({
  firstColumnByDesktop: {},
  shelfByDesktop: {},
  peek: null,
  setPeek: (peek) => set({ peek }),
  heldByDesktop: {},
  unseenByDesktop: {},
  flashingByDesktop: {},
  documentReturnsByDesktop: {},
  leafDragging: false,
  setLeafDragging: (leafDragging) => set({ leafDragging }),
  shelfDrop: null,
  setShelfDrop: (mark) =>
    set((state) => {
      const current = state.shelfDrop;
      if (current?.desktopId === mark?.desktopId && current?.anchorId === mark?.anchorId && current?.edge === mark?.edge) return state;
      return { shelfDrop: mark };
    }),
  rowActionsByDesktop: {},
  setRowActions: (desktopId, actions) =>
    set((state) => ({ rowActionsByDesktop: withEntry(state.rowActionsByDesktop, desktopId, actions ?? undefined) })),
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
      peek: state.peek?.desktopId === desktopId ? null : state.peek,
      heldByDesktop: without(state.heldByDesktop, desktopId),
      unseenByDesktop: without(state.unseenByDesktop, desktopId),
      flashingByDesktop: without(state.flashingByDesktop, desktopId),
      documentReturnsByDesktop: without(state.documentReturnsByDesktop, desktopId),
    })),
  holdLeaf: (desktopId, leafId) =>
    set((state) => ({
      heldByDesktop: { ...state.heldByDesktop, [desktopId]: [...(state.heldByDesktop[desktopId] ?? NONE), leafId] },
      flashingByDesktop: { ...state.flashingByDesktop, [desktopId]: [...(state.flashingByDesktop[desktopId] ?? NONE), leafId] },
    })),
  releaseLeaf: (desktopId, leafId) =>
    set((state) => ({
      heldByDesktop: withEntry(state.heldByDesktop, desktopId, listWithout(state.heldByDesktop[desktopId], (id) => id === leafId)),
    })),
  markUnseen: (desktopId, leafId) =>
    set((state) => ({
      unseenByDesktop: { ...state.unseenByDesktop, [desktopId]: [...(state.unseenByDesktop[desktopId] ?? NONE), leafId] },
    })),
  flash: (desktopId, leafIds) =>
    set((state) => (leafIds.length
      ? { flashingByDesktop: { ...state.flashingByDesktop, [desktopId]: [...(state.flashingByDesktop[desktopId] ?? NONE), ...leafIds] } }
      : state)),
  flashEnded: (desktopId, leafIds) =>
    set((state) => ({
      flashingByDesktop: withEntry(state.flashingByDesktop, desktopId, listWithout(state.flashingByDesktop[desktopId], (id) => leafIds.includes(id))),
    })),
  saveDocumentReturn: (desktopId, leafId, documentReturn) =>
    set((state) => ({
      documentReturnsByDesktop: {
        ...state.documentReturnsByDesktop,
        [desktopId]: { ...state.documentReturnsByDesktop[desktopId], [leafId]: documentReturn },
      },
    })),
  settleLeaves: (desktopId, row, onScreen, activeLeafId) =>
    set((state) => {
      const held = state.heldByDesktop[desktopId];
      const unseen = state.unseenByDesktop[desktopId];
      const flashing = state.flashingByDesktop[desktopId];
      const returns = state.documentReturnsByDesktop[desktopId];
      const heldGone = held?.some((id) => !row.has(id) || id === activeLeafId);
      const unseenGone = unseen?.some((id) => onScreen.has(id) || !row.has(id));
      const flashGone = flashing?.some((id) => !row.has(id));
      const returnGone = returns && Object.keys(returns).some((id) => !row.has(id));
      if (!heldGone && !unseenGone && !flashGone && !returnGone) return state;
      const keptReturns = returns && Object.fromEntries(Object.entries(returns).filter(([id]) => row.has(id)));
      return {
        heldByDesktop: withEntry(state.heldByDesktop, desktopId, listWithout(held, (id) => !row.has(id) || id === activeLeafId)),
        unseenByDesktop: withEntry(state.unseenByDesktop, desktopId, listWithout(unseen, (id) => onScreen.has(id) || !row.has(id))),
        flashingByDesktop: withEntry(state.flashingByDesktop, desktopId, listWithout(flashing, (id) => !row.has(id))),
        documentReturnsByDesktop: withEntry(state.documentReturnsByDesktop, desktopId, keptReturns && Object.keys(keptReturns).length ? keptReturns : undefined),
      };
    }),
}));

useProfilesStore.subscribe((state, previous) => {
  if (state.desktops === previous.desktops) return;
  const current = new Set(state.desktops.map((desktop) => desktop.id));
  const { firstColumnByDesktop, shelfByDesktop, heldByDesktop, unseenByDesktop, flashingByDesktop, documentReturnsByDesktop, forgetDesktop } = useDesktopShelfStore.getState();
  const known = [firstColumnByDesktop, shelfByDesktop, heldByDesktop, unseenByDesktop, flashingByDesktop, documentReturnsByDesktop];
  for (const desktopId of new Set(known.flatMap((record) => Object.keys(record)))) {
    if (!current.has(desktopId)) forgetDesktop(desktopId);
  }
});

export function bringAnchor(leaf: { desktopId: string; leafId: string }): string | null {
  const shelf = useDesktopShelfStore.getState().shelfByDesktop[leaf.desktopId];
  if (!shelf?.activeLeafId) return null;
  const offScreen = [...shelf.before, ...shelf.after].some((column) => column.tiles.some((tile) => tile.leafId === leaf.leafId));
  return offScreen ? shelf.activeLeafId : null;
}

export function oldestUnseenDocument(desktopId: string | null): string | null {
  if (!desktopId) return null;
  return useDesktopShelfStore.getState().unseenByDesktop[desktopId]?.[0] ?? null;
}

export function stackRoomPx(desktopId: string): number | null {
  return useDesktopShelfStore.getState().shelfByDesktop[desktopId]?.tileHeightPx ?? null;
}

import { useMemo, type PointerEvent as ReactPointerEvent } from 'react';
import { useDesktopShelfStore, type PublishedShelf, type ShelfColumn } from '../../store/desktopShelf';

const OPEN_DELAY_MS = 180;
const SWITCH_DELAY_MS = 60;
const CLOSE_DELAY_MS = 160;

let openTimer: number | undefined;
let closeTimer: number | undefined;

function clearTimers() {
  window.clearTimeout(openTimer);
  window.clearTimeout(closeTimer);
}

export function closeShelfPeek() {
  clearTimers();
  useDesktopShelfStore.getState().setPeek(null);
}

export function keepShelfPeekOpen() {
  clearTimers();
}

export function leaveShelfPeek() {
  clearTimers();
  if (document.activeElement?.closest('[data-testid="shelf-peek"]')) return;
  closeTimer = window.setTimeout(() => useDesktopShelfStore.getState().setPeek(null), CLOSE_DELAY_MS);
}

function arriveAtAnchor(desktopId: string, leafId: string, element: HTMLElement) {
  clearTimers();
  const { peek, setPeek, leafDragging } = useDesktopShelfStore.getState();
  if (leafDragging || peek?.leafId === leafId) return;
  openTimer = window.setTimeout(() => {
    const { left, bottom } = element.getBoundingClientRect();
    setPeek({ desktopId, leafId, anchor: { left, bottom } });
  }, peek ? SWITCH_DELAY_MS : OPEN_DELAY_MS);
}

export function offScreenColumns(shelf: PublishedShelf | undefined): ShelfColumn[] {
  return shelf ? [...shelf.before, ...shelf.after] : [];
}

function memberUnder(event: ReactPointerEvent<HTMLElement>): string | undefined {
  return (event.target as Element).closest<HTMLElement>('[data-shelf-member]')?.dataset.shelfMember;
}

function hoverHandlers(desktopId: string, defaultLeafId: string) {
  let arrivedAt: string | null = null;
  return {
    onPointerEnter: () => {
      arrivedAt = null;
    },
    onPointerMove: (event: ReactPointerEvent<HTMLElement>) => {
      const member = memberUnder(event);
      if (arrivedAt !== null && (member === undefined || member === arrivedAt)) return;
      arrivedAt = member ?? defaultLeafId;
      arriveAtAnchor(desktopId, arrivedAt, event.currentTarget);
    },
    onPointerLeave: leaveShelfPeek,
  };
}

export function useShelfPeekHover(desktopId: string, defaultLeafId: string) {
  const offScreen = useDesktopShelfStore((state) =>
    offScreenColumns(state.shelfByDesktop[desktopId]).some((column) => column.tiles.some((tile) => tile.leafId === defaultLeafId)));
  return useMemo(() => (offScreen ? hoverHandlers(desktopId, defaultLeafId) : undefined), [desktopId, defaultLeafId, offScreen]);
}

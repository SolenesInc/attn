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

function enterAnchor(desktopId: string, leafId: string, element: HTMLElement) {
  clearTimers();
  const { peek, setPeek, leafDragging } = useDesktopShelfStore.getState();
  if (leafDragging || peek?.leafId === leafId) return;
  openTimer = window.setTimeout(() => {
    const { left, right, top, bottom } = element.getBoundingClientRect();
    setPeek({ desktopId, leafId, anchor: { left, right, top, bottom } });
  }, peek ? SWITCH_DELAY_MS : OPEN_DELAY_MS);
}

export function offScreenColumns(shelf: PublishedShelf | undefined): ShelfColumn[] {
  return shelf ? [...shelf.before, ...shelf.after] : [];
}

function hoverHandlers(desktopId: string, leafId: string) {
  return {
    onPointerEnter: (event: ReactPointerEvent<HTMLElement>) => enterAnchor(desktopId, leafId, event.currentTarget),
    onPointerLeave: leaveShelfPeek,
  };
}

export function useShelfPeekHover(desktopId: string, leafId: string) {
  const offScreen = useDesktopShelfStore((state) =>
    offScreenColumns(state.shelfByDesktop[desktopId]).some((column) => column.tiles.some((tile) => tile.leafId === leafId)));
  return useMemo(() => (offScreen ? hoverHandlers(desktopId, leafId) : undefined), [desktopId, leafId, offScreen]);
}

function offScreenLeafOfSession(shelves: Readonly<Record<string, PublishedShelf>>, sessionId: string) {
  for (const [desktopId, shelf] of Object.entries(shelves)) {
    for (const column of offScreenColumns(shelf)) {
      const tile = column.tiles.find((candidate) => candidate.askHolder?.id === sessionId);
      if (tile) return `${desktopId}\n${tile.leafId}`;
    }
  }
  return null;
}

export function useSessionShelfPeekHover(sessionId: string) {
  const target = useDesktopShelfStore((state) => offScreenLeafOfSession(state.shelfByDesktop, sessionId));
  return useMemo(() => {
    if (!target) return undefined;
    const [desktopId, leafId] = target.split('\n');
    return hoverHandlers(desktopId, leafId);
  }, [target]);
}

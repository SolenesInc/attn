import { useCallback, useEffect, useRef, useState } from 'react';
import type { NormalizedPaneBounds } from '../../types/desktop';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { useDesktopShelfStore } from '../../store/desktopShelf';
import { lockTextSelection } from '../../utils/dragLock';
import type { DockTarget } from './dockTarget';
import { startLeafDrag, type RowDrops, type ShelfDropTarget } from './leafDrag';
import { shelfDropTargetAt } from './shelfDropTarget';
import { closeShelfPeek } from './useShelfPeekHover';
import type { SessionTerminalDesktopProps } from './desktopTypes';
type Options = Pick<
  SessionTerminalDesktopProps,
  | 'getActiveLeafDropSnapshot'
  | 'onLeafDragStart'
  | 'onLeafDragGhostMove'
  | 'onLeafDragPreview'
  | 'onLeafDragEnd'
  | 'onMoveLeaf'
  | 'leafDragPreview'
>
  & { renderedPaneBounds: Map<string, NormalizedPaneBounds> }
  & { row?: RowDragOptions };

interface RowDragOptions {
  desktopId: string;
  panesContainer: () => HTMLElement | null;
  showing: () => boolean;
  onTileToShelf: (leafId: string, target: ShelfDropTarget) => void;
  onChipToShelf: (leafId: string, target: ShelfDropTarget) => void;
  onChipToTile: (leafId: string, target: DockTarget) => void;
}

export interface ChipDragSource {
  key: string;
  leafId: string;
}

function shelfDrops(row: RowDragOptions, ownColumnKey: string | null, centreReplaces: () => boolean, onShelfDrop: RowDrops['onShelfDrop']): RowDrops {
  return {
    centreReplaces,
    shelfTargetAt: (x, y) => shelfDropTargetAt(row.desktopId, ownColumnKey, x, y),
    onShelfPreview: (target) => useDesktopShelfStore.getState().setShelfDrop(target && { desktopId: row.desktopId, ...target }),
    onShelfDrop,
  };
}

export function useDesktopLeafDrag({
  renderedPaneBounds,
  getActiveLeafDropSnapshot,
  onLeafDragStart,
  onLeafDragGhostMove,
  onLeafDragPreview,
  onLeafDragEnd,
  onMoveLeaf,
  leafDragPreview,
  row,
}: Options) {
  const [draggingLeafId, setDraggingLeafId] = useState<string | null>(null);
  const [dockTarget, setDockTarget] = useState<DockTarget | null>(null);
  const [ghostPos, setGhostPos] = useState<{ x: number; y: number } | null>(null);
  const tileDragCleanupRef = useRef<(() => void) | null>(null);

  const startDrag = useCallback(
    (
      leafId: string,
      point: { clientX: number; clientY: number },
      container: HTMLElement,
      drop: (id: string, target: DockTarget) => void,
      wholeColumn: boolean,
      rowDrops: RowDrops | undefined,
    ) => {
      // The press only becomes a drag past startLeafDrag's activation threshold,
      // so every visual side effect is deferred to onActivate.
      let releaseSelectionLock: (() => void) | null = null;
      const teardown = startLeafDrag(
        leafId,
        point.clientX,
        point.clientY,
        container,
        renderedPaneBounds,
        {
          onActivate: () => {
            releaseSelectionLock = lockTextSelection('grabbing');
            if (rowDrops) {
              closeShelfPeek();
              useDesktopShelfStore.getState().setLeafDragging(true);
            }
            onLeafDragStart?.(leafId, wholeColumn);
            setDraggingLeafId(leafId);
          },
          onGhostMove: (x, y) => {
            setGhostPos({ x, y });
            onLeafDragGhostMove?.(x, y);
          },
          onPreview: (target) => {
            setDockTarget(target);
            onLeafDragPreview?.(target);
          },
          onDrop: drop,
          onCleanup: () => {
            releaseSelectionLock?.();
            releaseSelectionLock = null;
            if (rowDrops) {
              const shelf = useDesktopShelfStore.getState();
              shelf.setLeafDragging(false);
              shelf.setShelfDrop(null);
            }
            setDraggingLeafId(null);
            setDockTarget(null);
            setGhostPos(null);
            tileDragCleanupRef.current = null;
            onLeafDragEnd?.();
          },
        },
        getActiveLeafDropSnapshot,
        rowDrops,
      );
      tileDragCleanupRef.current = teardown;
    },
    [
      getActiveLeafDropSnapshot,
      onLeafDragEnd,
      onLeafDragGhostMove,
      onLeafDragPreview,
      onLeafDragStart,
      renderedPaneBounds,
    ],
  );

  const beginLeafDrag = useCallback(
    (leafId: string, event: React.PointerEvent<HTMLDivElement>) => {
      if (event.button !== 0) {
        return;
      }
      event.preventDefault();
      const container = (event.target as HTMLElement).closest(
        '.session-terminal-panes',
      ) as HTMLElement | null;
      if (!container) {
        return;
      }
      startDrag(
        leafId,
        event,
        container,
        (id, target) => onMoveLeaf?.(id, target.anchorId, target.edge, target.ratio),
        false,
        row && shelfDrops(row, null, () => false, row.onTileToShelf),
      );
    },
    [onMoveLeaf, row, startDrag],
  );

  const beginChipDrag = useCallback(
    (source: ChipDragSource, event: React.PointerEvent) => {
      const container = row?.panesContainer();
      if (event.button !== 0 || !row || !container) {
        return;
      }
      startDrag(source.leafId, event, container, row.onChipToTile, true, shelfDrops(row, source.key, row.showing, row.onChipToShelf));
    },
    [row, startDrag],
  );

  const effectiveDraggingLeafId = leafDragPreview?.draggingLeafId ?? draggingLeafId;
  const effectiveDockTarget = leafDragPreview?.dockTarget ?? dockTarget;
  const effectiveGhostPos = leafDragPreview?.ghostPos ?? ghostPos;

  useEscapeStack(() => tileDragCleanupRef.current?.(), Boolean(row && draggingLeafId));
  useEffect(() => () => tileDragCleanupRef.current?.(), []);
  return { beginLeafDrag, beginChipDrag, effectiveDraggingLeafId, effectiveDockTarget, effectiveGhostPos };
}

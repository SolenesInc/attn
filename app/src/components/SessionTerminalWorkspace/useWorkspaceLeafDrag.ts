import { useCallback, useEffect, useRef, useState } from 'react';
import type { NormalizedPaneBounds } from '../../types/workspace';
import { lockTextSelection } from '../../utils/dragLock';
import type { DockTarget } from './dockTarget';
import { startLeafDrag } from './leafDrag';
import type { SessionTerminalWorkspaceProps } from './workspaceTypes';
type Options = Pick<
  SessionTerminalWorkspaceProps,
  | 'getActiveLeafDropSnapshot'
  | 'onLeafDragStart'
  | 'onLeafDragGhostMove'
  | 'onLeafDragPreview'
  | 'onLeafDragEnd'
  | 'onMoveLeaf'
  | 'leafDragPreview'
> & { renderedPaneBounds: Map<string, NormalizedPaneBounds> };
export function useWorkspaceLeafDrag({
  renderedPaneBounds,
  getActiveLeafDropSnapshot,
  onLeafDragStart,
  onLeafDragGhostMove,
  onLeafDragPreview,
  onLeafDragEnd,
  onMoveLeaf,
  leafDragPreview,
}: Options) {
  const [draggingLeafId, setDraggingLeafId] = useState<string | null>(null);
  const [dockTarget, setDockTarget] = useState<DockTarget | null>(null);
  const [ghostPos, setGhostPos] = useState<{ x: number; y: number } | null>(null);
  const tileDragCleanupRef = useRef<(() => void) | null>(null);
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
      // The press only becomes a drag past startLeafDrag's activation threshold,
      // so every visual side effect is deferred to onActivate.
      let releaseSelectionLock: (() => void) | null = null;
      const teardown = startLeafDrag(
        leafId,
        event.clientX,
        event.clientY,
        container,
        renderedPaneBounds,
        {
          onActivate: () => {
            releaseSelectionLock = lockTextSelection('grabbing');
            onLeafDragStart?.(leafId);
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
          onDrop: (id, target) => onMoveLeaf?.(id, target.anchorId, target.edge, target.ratio),
          onCleanup: () => {
            releaseSelectionLock?.();
            releaseSelectionLock = null;
            setDraggingLeafId(null);
            setDockTarget(null);
            setGhostPos(null);
            tileDragCleanupRef.current = null;
            onLeafDragEnd?.();
          },
        },
        getActiveLeafDropSnapshot,
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
      onMoveLeaf,
    ],
  );

  const effectiveDraggingLeafId = leafDragPreview?.draggingLeafId ?? draggingLeafId;
  const effectiveDockTarget = leafDragPreview?.dockTarget ?? dockTarget;
  const effectiveGhostPos = leafDragPreview?.ghostPos ?? ghostPos;

  useEffect(() => () => tileDragCleanupRef.current?.(), []);
  return { beginLeafDrag, effectiveDraggingLeafId, effectiveDockTarget, effectiveGhostPos };
}

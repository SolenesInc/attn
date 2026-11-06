import type { NormalizedPaneBounds } from '../../types/desktop';
import { computeDockTarget, computeContainerSides, NO_CONTAINER_SIDES, type DockTarget } from './dockTarget';

// Without a threshold, clicking a pane header — which sits in the desktop's top
// perimeter band — computes a drop target at the press position and splits the container.
export const DRAG_ACTIVATION_PX = 4;

interface LeafDragHandlers {
  onActivate: () => void;
  onPreview: (target: DockTarget | null) => void;
  onGhostMove: (clientX: number, clientY: number) => void;
  onDrop: (leafId: string, target: DockTarget) => void;
  onCleanup: () => void;
}

export interface ShelfDropTarget {
  anchorId: string;
  edge: 'left' | 'right';
}

export interface RowDrops {
  centreReplaces: () => boolean;
  shelfTargetAt: (clientX: number, clientY: number) => ShelfDropTarget | null;
  onShelfPreview: (target: ShelfDropTarget | null) => void;
  onShelfDrop: (leafId: string, target: ShelfDropTarget) => void;
  fitDockTarget?: (target: DockTarget, anchorBounds: NormalizedPaneBounds, containerHeightPx: number) => DockTarget;
}

export interface LeafDropSnapshot {
  container: HTMLElement;
  paneBounds: Map<string, NormalizedPaneBounds>;
}

export function startLeafDrag(
  leafId: string,
  clientX: number,
  clientY: number,
  container: HTMLElement,
  paneBounds: Map<string, NormalizedPaneBounds>,
  handlers: LeafDragHandlers,
  getDropSnapshot?: () => LeafDropSnapshot | null,
  rowDrops?: RowDrops,
): () => void {
  const fallbackSnapshot = { container, paneBounds };

  const computeTarget = (x: number, y: number): DockTarget | null => {
    const snapshot = getDropSnapshot?.() ?? fallbackSnapshot;
    const containerRect = snapshot.container.getBoundingClientRect();
    const leafRects: Array<{ leafId: string; bounds: NormalizedPaneBounds }> = [];
    const allBounds: NormalizedPaneBounds[] = [];
    for (const [slotId, bounds] of snapshot.paneBounds) {
      allBounds.push(bounds);
      if (slotId !== leafId) {
        leafRects.push({ leafId: slotId, bounds });
      }
    }
    const containerSides = rowDrops ? NO_CONTAINER_SIDES : computeContainerSides(allBounds);
    const target = computeDockTarget(x, y, containerRect, leafRects, containerSides, rowDrops?.centreReplaces());
    const anchorBounds = target && snapshot.paneBounds.get(target.anchorId);
    return anchorBounds && rowDrops?.fitDockTarget ? rowDrops.fitDockTarget(target, anchorBounds, containerRect.height) : target;
  };

  const pastThreshold = (x: number, y: number): boolean =>
    Math.hypot(x - clientX, y - clientY) >= DRAG_ACTIVATION_PX;

  let activated = false;

  const onMove = (ev: PointerEvent) => {
    if (!activated) {
      if (!pastThreshold(ev.clientX, ev.clientY)) {
        return;
      }
      activated = true;
      handlers.onActivate();
    }
    handlers.onGhostMove(ev.clientX, ev.clientY);
    const shelfTarget = rowDrops?.shelfTargetAt(ev.clientX, ev.clientY) ?? null;
    rowDrops?.onShelfPreview(shelfTarget);
    handlers.onPreview(shelfTarget ? null : computeTarget(ev.clientX, ev.clientY));
  };
  const teardown = () => {
    window.removeEventListener('pointermove', onMove);
    window.removeEventListener('pointerup', onUp);
    window.removeEventListener('pointercancel', onCancel);
    window.removeEventListener('blur', onCancel);
    handlers.onCleanup();
  };
  const onUp = (ev: PointerEvent) => {
    // Resolve a drop only if the gesture became a drag; a fast drag's moves may have been
    // coalesced, and a press-and-release in place stays a click.
    const isDrag = activated || pastThreshold(ev.clientX, ev.clientY);
    const shelfTarget = isDrag ? rowDrops?.shelfTargetAt(ev.clientX, ev.clientY) ?? null : null;
    const finalTarget = isDrag && !shelfTarget ? computeTarget(ev.clientX, ev.clientY) : null;
    teardown();
    if (shelfTarget) {
      rowDrops?.onShelfDrop(leafId, shelfTarget);
    } else if (finalTarget) {
      handlers.onDrop(leafId, finalTarget);
    }
  };
  const onCancel = () => {
    teardown();
  };

  window.addEventListener('pointermove', onMove);
  window.addEventListener('pointerup', onUp);
  window.addEventListener('pointercancel', onCancel);
  window.addEventListener('blur', onCancel);
  return teardown;
}

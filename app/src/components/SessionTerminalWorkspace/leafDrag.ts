import type { NormalizedPaneBounds } from '../../types/workspace';
import { computeDockTarget, computeContainerSides, type DockTarget } from './dockTarget';

// Without a threshold, clicking a pane header — which sits in the workspace's top
// perimeter band — computes a drop target at the press position and splits the container.
const DRAG_ACTIVATION_PX = 4;

export interface LeafDragHandlers {
  onActivate: () => void;
  onPreview: (target: DockTarget | null) => void;
  onGhostMove: (clientX: number, clientY: number) => void;
  onDrop: (leafId: string, target: DockTarget) => void;
  onCleanup: () => void;
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
    const containerSides = computeContainerSides(allBounds);
    return computeDockTarget(x, y, containerRect, leafRects, containerSides);
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
    handlers.onPreview(computeTarget(ev.clientX, ev.clientY));
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
    const finalTarget = isDrag ? computeTarget(ev.clientX, ev.clientY) : null;
    teardown();
    if (finalTarget) {
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

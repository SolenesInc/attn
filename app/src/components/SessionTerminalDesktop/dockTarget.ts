import type { TerminalDockEdge } from '../../types/desktop';
import type { NormalizedPaneBounds } from '../../types/desktop';
import { TILE_MIN_HEIGHT } from './desktopLayout';

const DOCK_F_MIN = 0.15;
const DOCK_F_MAX = 0.5;
const DOCK_SNAP_POINTS = [0.25, 1 / 3, 0.5];
const DOCK_SNAP_TOLERANCE = 0.045;
const DOCK_CENTRE_MARGIN = 0.25;

export interface DockNormalizedRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

export interface DockTarget {
  anchorId: string;
  edge: TerminalDockEdge;
  ratio: number;
  rect: DockNormalizedRect;
  replacesAnchor?: boolean;
  limitMessage?: string;
}

function snapDockFraction(fraction: number): number {
  for (const point of DOCK_SNAP_POINTS) {
    if (Math.abs(fraction - point) <= DOCK_SNAP_TOLERANCE) {
      return point;
    }
  }
  return fraction;
}

function leafBandRect(bounds: NormalizedPaneBounds, edge: TerminalDockEdge, fraction: number): DockNormalizedRect {
  const bandW = bounds.width * fraction;
  const bandH = bounds.height * fraction;
  switch (edge) {
    case 'left':
      return { left: bounds.left, top: bounds.top, width: bandW, height: bounds.height };
    case 'right':
      return { left: bounds.right - bandW, top: bounds.top, width: bandW, height: bounds.height };
    case 'top':
      return { left: bounds.left, top: bounds.top, width: bounds.width, height: bandH };
    default:
      return { left: bounds.left, top: bounds.bottom - bandH, width: bounds.width, height: bandH };
  }
}

export function fitColumnOnEdgeDrop(
  target: DockTarget,
  anchorBounds: NormalizedPaneBounds,
  containerHeightPx: number,
  columnNeedPx: number,
): DockTarget {
  if (target.replacesAnchor || (target.edge !== 'top' && target.edge !== 'bottom')) return target;
  const anchorPx = anchorBounds.height * containerHeightPx;
  const neededPx = columnNeedPx + TILE_MIN_HEIGHT;
  if (neededPx > anchorPx) {
    return {
      ...target,
      edge: 'right',
      ratio: DOCK_F_MAX,
      rect: leafBandRect(anchorBounds, 'right', DOCK_F_MAX),
      limitMessage: `Stacking here needs ${Math.ceil(neededPx)}px to keep every tile at the ${TILE_MIN_HEIGHT}px minimum. This tile has ${Math.floor(anchorPx)}px, so it moved beside instead.`,
    };
  }
  const ratio = Math.max(target.ratio, columnNeedPx / anchorPx);
  return { ...target, ratio, rect: leafBandRect(anchorBounds, target.edge, ratio) };
}

export function computeDockTarget(
  clientX: number,
  clientY: number,
  containerRect: { left: number; top: number; width: number; height: number },
  leafRects: Array<{ leafId: string; bounds: NormalizedPaneBounds }>,
  centreReplaces = false,
): DockTarget | null {
  if (containerRect.width <= 0 || containerRect.height <= 0) {
    return null;
  }
  const nx = (clientX - containerRect.left) / containerRect.width;
  const ny = (clientY - containerRect.top) / containerRect.height;

  for (const { leafId, bounds } of leafRects) {
    if (nx < bounds.left || nx > bounds.right || ny < bounds.top || ny > bounds.bottom) {
      continue;
    }
    const px = (nx - bounds.left) / Math.max(bounds.width, 1e-6);
    const py = (ny - bounds.top) / Math.max(bounds.height, 1e-6);
    const distances: Array<{ edge: TerminalDockEdge; dist: number }> = [
      { edge: 'left', dist: px },
      { edge: 'right', dist: 1 - px },
      { edge: 'top', dist: py },
      { edge: 'bottom', dist: 1 - py },
    ];
    distances.sort((a, b) => a.dist - b.dist);
    const { edge, dist } = distances[0];
    if (centreReplaces && dist >= DOCK_CENTRE_MARGIN) {
      return { anchorId: leafId, edge: 'left', ratio: DOCK_F_MAX, rect: { left: bounds.left, top: bounds.top, width: bounds.width, height: bounds.height }, replacesAnchor: true };
    }
    const fraction = snapDockFraction(Math.min(Math.max(dist, DOCK_F_MIN), DOCK_F_MAX));
    return { anchorId: leafId, edge, ratio: fraction, rect: leafBandRect(bounds, edge, fraction) };
  }
  return null;
}

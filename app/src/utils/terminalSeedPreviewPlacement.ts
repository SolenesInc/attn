export interface TerminalSeedAnchor {
  left: number;
  right: number;
  top: number;
  bottom: number;
  bounds: { left: number; right: number; top: number; bottom: number };
}

export type TerminalSeedPreviewSide = 'right' | 'left' | 'below' | 'above';

export interface TerminalSeedPreviewPlacement {
  left: number;
  top: number;
  side: TerminalSeedPreviewSide;
  entryX: number;
  entryY: number;
  path: string;
  source: { x: number; y: number };
  destination: { x: number; y: number };
}

interface PreviewSize {
  width: number;
  height: number;
}

interface ViewportSize {
  width: number;
  height: number;
}

interface Bounds {
  left: number;
  right: number;
  top: number;
  bottom: number;
}

interface Candidate {
  side: TerminalSeedPreviewSide;
  pressure: number;
  available: number;
  required: number;
  order: number;
}

export const TERMINAL_SEED_PREVIEW_FALLBACK_SIZE = { width: 390, height: 215 };

const VIEWPORT_MARGIN = 8;
const CARD_GAP = 42;
const SOURCE_GAP = 7;
const ENTRY_MARGIN = 28;

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), Math.max(min, max));
}

function viewportBounds(viewport: ViewportSize): Bounds {
  return { left: 0, top: 0, right: viewport.width, bottom: viewport.height };
}

function intersectWithViewport(bounds: Bounds, viewport: ViewportSize): Bounds {
  return {
    left: clamp(bounds.left, 0, viewport.width),
    right: clamp(bounds.right, 0, viewport.width),
    top: clamp(bounds.top, 0, viewport.height),
    bottom: clamp(bounds.bottom, 0, viewport.height),
  };
}

function positioningBounds(
  anchorBounds: Bounds,
  viewport: ViewportSize,
  size: PreviewSize,
): Bounds {
  const available = intersectWithViewport(anchorBounds, viewport);
  const fallback = viewportBounds(viewport);
  return {
    left: available.right - available.left >= size.width + VIEWPORT_MARGIN * 2
      ? available.left
      : fallback.left,
    right: available.right - available.left >= size.width + VIEWPORT_MARGIN * 2
      ? available.right
      : fallback.right,
    top: available.bottom - available.top >= size.height + VIEWPORT_MARGIN * 2
      ? available.top
      : fallback.top,
    bottom: available.bottom - available.top >= size.height + VIEWPORT_MARGIN * 2
      ? available.bottom
      : fallback.bottom,
  };
}

function candidateSides(
  anchor: TerminalSeedAnchor,
  bounds: Bounds,
  size: PreviewSize,
): Candidate[] {
  const width = Math.max(1, size.width);
  const height = Math.max(1, size.height);
  const candidates: Candidate[] = [
    {
      side: 'right',
      pressure: Math.max(0, anchor.left - bounds.left) / width,
      available: bounds.right - VIEWPORT_MARGIN - anchor.right - CARD_GAP,
      required: width,
      order: 0,
    },
    {
      side: 'left',
      pressure: Math.max(0, bounds.right - anchor.right) / width,
      available: anchor.left - CARD_GAP - (bounds.left + VIEWPORT_MARGIN),
      required: width,
      order: 1,
    },
    {
      side: 'below',
      pressure: Math.max(0, anchor.top - bounds.top) / height,
      available: bounds.bottom - VIEWPORT_MARGIN - anchor.bottom - CARD_GAP,
      required: height,
      order: 2,
    },
    {
      side: 'above',
      pressure: Math.max(0, bounds.bottom - anchor.bottom) / height,
      available: anchor.top - CARD_GAP - (bounds.top + VIEWPORT_MARGIN),
      required: height,
      order: 3,
    },
  ];
  return candidates.sort((a, b) => a.pressure - b.pressure || a.order - b.order);
}

function selectSide(candidates: Candidate[]): TerminalSeedPreviewSide {
  const fitting = candidates.find((candidate) => candidate.available >= candidate.required);
  if (fitting) return fitting.side;
  return [...candidates].sort((a, b) => (
    b.available / b.required - a.available / a.required || a.order - b.order
  ))[0].side;
}

function horizontalPath(
  source: { x: number; y: number },
  destination: { x: number; y: number },
  direction: 1 | -1,
): string {
  const run = Math.abs(destination.x - source.x);
  const elbowX = source.x + direction * Math.min(18, run * 0.32);
  const approachX = destination.x - direction * Math.min(13, run * 0.24);
  const stepY = destination.y + (source.y <= destination.y ? -9 : 9);
  return [
    `M ${source.x} ${source.y}`,
    `H ${elbowX}`,
    `L ${elbowX + direction * 7} ${source.y - 7}`,
    `V ${stepY}`,
    `L ${elbowX + direction * 14} ${destination.y}`,
    `H ${approachX}`,
    `L ${destination.x} ${destination.y}`,
  ].join(' ');
}

function verticalPath(
  source: { x: number; y: number },
  destination: { x: number; y: number },
  direction: 1 | -1,
): string {
  const run = Math.abs(destination.y - source.y);
  const leadY = source.y + direction * Math.min(16, run * 0.32);
  const approachY = destination.y - direction * Math.min(10, run * 0.22);
  const stepX = destination.x + (source.x <= destination.x ? -8 : 8);
  return [
    `M ${source.x} ${source.y}`,
    `V ${leadY}`,
    `L ${source.x + 7} ${leadY + direction * 7}`,
    `H ${stepX}`,
    `L ${destination.x} ${approachY}`,
    `V ${destination.y}`,
  ].join(' ');
}

export function terminalSeedPreviewPlacement(
  anchor: TerminalSeedAnchor,
  size: PreviewSize,
  viewport: ViewportSize,
): TerminalSeedPreviewPlacement {
  const edgeBounds = intersectWithViewport(anchor.bounds, viewport);
  const bounds = positioningBounds(edgeBounds, viewport, size);
  const side = selectSide(candidateSides(anchor, edgeBounds, size));
  const minLeft = bounds.left + VIEWPORT_MARGIN;
  const maxLeft = bounds.right - size.width - VIEWPORT_MARGIN;
  const minTop = bounds.top + VIEWPORT_MARGIN;
  const maxTop = bounds.bottom - size.height - VIEWPORT_MARGIN;
  const anchorMidX = (anchor.left + anchor.right) / 2;
  const anchorMidY = (anchor.top + anchor.bottom) / 2;

  let left: number;
  let top: number;
  let source: { x: number; y: number };
  let destination: { x: number; y: number };
  let path: string;

  if (side === 'right' || side === 'left') {
    left = clamp(
      side === 'right' ? anchor.right + CARD_GAP : anchor.left - CARD_GAP - size.width,
      minLeft,
      maxLeft,
    );
    top = clamp(anchorMidY - 43, minTop, maxTop);
    const entryY = clamp(anchorMidY - top, ENTRY_MARGIN, size.height - ENTRY_MARGIN);
    source = {
      x: side === 'right' ? anchor.right + SOURCE_GAP : anchor.left - SOURCE_GAP,
      y: anchorMidY,
    };
    destination = {
      x: side === 'right' ? left : left + size.width,
      y: top + entryY,
    };
    path = horizontalPath(source, destination, side === 'right' ? 1 : -1);
  } else {
    left = clamp(anchorMidX - size.width / 2, minLeft, maxLeft);
    top = clamp(
      side === 'below' ? anchor.bottom + CARD_GAP : anchor.top - CARD_GAP - size.height,
      minTop,
      maxTop,
    );
    const entryX = clamp(anchorMidX - left, ENTRY_MARGIN, size.width - ENTRY_MARGIN);
    source = {
      x: anchorMidX,
      y: side === 'below' ? anchor.bottom + SOURCE_GAP : anchor.top - SOURCE_GAP,
    };
    destination = {
      x: left + entryX,
      y: side === 'below' ? top : top + size.height,
    };
    path = verticalPath(source, destination, side === 'below' ? 1 : -1);
  }

  return {
    left,
    top,
    side,
    entryX: destination.x - left,
    entryY: destination.y - top,
    path,
    source,
    destination,
  };
}

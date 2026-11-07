
import { isSuspiciousTerminalSize } from '../utils/terminalDebug';
import type { TerminalDimensions } from '../utils/ghosttyResize';

export function fitRequiresTerminalResize(
  current: TerminalDimensions,
  next: TerminalDimensions,
): boolean {
  return current.cols !== next.cols || current.rows !== next.rows;
}

// Only daemon-authoritative geometry can overflow; fit() floors. The 1px slack
// absorbs fractional container heights.
export function geometryOverflowsContainer(
  rows: number,
  cellHeight: number,
  clientHeight: number,
): boolean {
  if (rows <= 0 || cellHeight <= 0 || clientHeight <= 0) {
    return false;
  }
  return rows * cellHeight > clientHeight + 1;
}

// A PTY another client sized smaller than this pane is drawn whole, scaled up
// to fill the pane and centered, instead of reflowed into it.
export function scaledToFitTransform(
  cols: number,
  rows: number,
  cellWidth: number,
  cellHeight: number,
  clientWidth: number,
  clientHeight: number,
): string {
  const room = {
    cols: Math.floor(clientWidth / cellWidth),
    rows: Math.floor(clientHeight / cellHeight),
  };
  const fitsWithRoomToSpare = cols <= room.cols && rows <= room.rows && (cols < room.cols || rows < room.rows);
  if (!fitsWithRoomToSpare) return '';
  const width = cols * cellWidth;
  const height = rows * cellHeight;
  const scale = Math.min(clientWidth / width, clientHeight / height);
  const x = (clientWidth - width * scale) / 2;
  const y = (clientHeight - height * scale) / 2;
  return `translate(${x}px, ${y}px) scale(${scale})`;
}

export function drawnScale(canvas: HTMLElement, drawn: DOMRect): number {
  return canvas.offsetWidth > 0 ? drawn.width / canvas.offsetWidth : 1;
}

// The bail protects a healthy grid only: refused when the live model already
// overflows, or is itself suspicious and `dims` would not shrink it.
export function fitShouldBailAsSuspicious(
  paneKind: string | undefined,
  dims: TerminalDimensions,
  modelCols: number,
  modelRows: number,
  cellWidth: number,
  cellHeight: number,
  clientWidth: number,
  clientHeight: number,
): boolean {
  if (paneKind !== 'agent') {
    return false;
  }
  if (!isSuspiciousTerminalSize(dims.cols, dims.rows)) {
    return false;
  }
  const overflows = geometryOverflowsContainer(modelRows, cellHeight, clientHeight)
    || geometryOverflowsContainer(modelCols, cellWidth, clientWidth);
  if (overflows) {
    return false;
  }
  const shrinksModel = dims.cols < modelCols || dims.rows < modelRows;
  return !(isSuspiciousTerminalSize(modelCols, modelRows) && !shrinksModel);
}

export function recoveryDelayMs(attempt: number): number | null {
  const schedule = [250, 1500, 5000];
  return schedule[attempt - 1] ?? null;
}


import { isSuspiciousTerminalSize } from '../utils/terminalDebug';
import type { TerminalDimensions } from '../utils/ghosttyResize';

export function isWorkspaceResizeDragActive(element: HTMLElement | null): boolean {
  if (document.documentElement.dataset.attnWorkspaceResizing === '1') {
    return true;
  }
  return Boolean(element?.closest('.session-terminal-panes[data-resizing-split-id]'));
}

// A fold eases the pane frame down to its 34px sliver; a fit mid-transition
// would shrink the PTY to a few columns and truncate every row of scrollback.
export function isWorkspaceSuspensionAnimating(element: HTMLElement | null): boolean {
  return Boolean(element?.closest('.session-terminal-panes[data-suspension-animating]'));
}

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

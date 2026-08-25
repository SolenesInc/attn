// Inline-rendering TUIs such as codex re-anchor at a tiny SIGWINCH size and never
// recover, so agent panes suppress SIGWINCH below these dimensions.
export const MIN_USABLE_TERMINAL_COLS = 20;
export const MIN_USABLE_TERMINAL_ROWS = 10;

export function isSuspiciousTerminalSize(cols: number, rows: number): boolean {
  return cols <= MIN_USABLE_TERMINAL_COLS || rows <= MIN_USABLE_TERMINAL_ROWS;
}

export interface ResizeDiagnostics {
  containerWidth: number;
  containerHeight: number;
  availableWidth: number;
  availableHeight: number;
  cellWidth: number;
  cellHeight: number;
  cellSource: 'renderer' | 'measured';
  dpr: number;
}

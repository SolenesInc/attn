
export type GridLayout =
  | { mode: 'auto' }
  | { mode: 'fixed'; rows: number; cols: number };

export const AUTO_LAYOUT: GridLayout = { mode: 'auto' };

// Picker bounds. The WebGL grid renderer was proven to ~25 live tiles (5×5) in the
// grid spike, so that is the offered ceiling.
export const MAX_GRID_ROWS = 5;
export const MAX_GRID_COLS = 5;

export function autoGrid(n: number): { rows: number; cols: number } {
  if (n <= 1) return { rows: 1, cols: 1 };
  const cols = Math.ceil(Math.sqrt(n));
  const rows = Math.ceil(n / cols);
  return { rows, cols };
}

export interface ResolvedGridLayout {
  rows: number;
  cols: number;
  capacity: number;
}

export function resolveGridLayout(tileCount: number, layout: GridLayout): ResolvedGridLayout {
  if (layout.mode === 'fixed') {
    const rows = clampDim(layout.rows, MAX_GRID_ROWS);
    const cols = clampDim(layout.cols, MAX_GRID_COLS);
    return { rows, cols, capacity: rows * cols };
  }
  const { rows, cols } = autoGrid(tileCount);
  return { rows, cols, capacity: tileCount };
}

function clampDim(value: number, max: number): number {
  if (!Number.isFinite(value)) return 1;
  return Math.max(1, Math.min(max, Math.round(value)));
}

const GRID_LAYOUT_STORAGE_KEY = 'attn.grid.layout';

export function readGridLayout(): GridLayout {
  try {
    const raw = window.localStorage.getItem(GRID_LAYOUT_STORAGE_KEY);
    if (!raw) return AUTO_LAYOUT;
    const parsed = JSON.parse(raw) as Partial<GridLayout> | null;
    if (parsed && parsed.mode === 'fixed') {
      return {
        mode: 'fixed',
        rows: clampDim(Number(parsed.rows), MAX_GRID_ROWS),
        cols: clampDim(Number(parsed.cols), MAX_GRID_COLS),
      };
    }
    return AUTO_LAYOUT;
  } catch {
    return AUTO_LAYOUT;
  }
}

export function persistGridLayout(layout: GridLayout): void {
  try {
    window.localStorage.setItem(GRID_LAYOUT_STORAGE_KEY, JSON.stringify(layout));
  } catch (err) {
    console.warn('[grid] Failed to persist layout preference:', err);
  }
}

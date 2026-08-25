// One shared glyph atlas backs every tile: font, size and dpr must stay
// identical across tiles.
import {
  FONT_FAMILY,
  TERMINAL_SCROLLBACK_BYTES,
} from '../../utils/terminalSizing';

export { FONT_FAMILY, TERMINAL_SCROLLBACK_BYTES };

export const FONT_SIZE = 13;

export const TILE_COLS = 80;
export const TILE_ROWS = 24;

export interface CellMetrics {
  cellWidth: number;
  cellHeight: number;
  baseline: number;
}

// Mirrors WebGlTerminalRenderer's constructor math; the two must move together.
export function measureCanonicalCell(
  fontSize: number = FONT_SIZE,
  fontFamily: string = FONT_FAMILY,
): CellMetrics {
  const canvas = document.createElement('canvas');
  const ctx = canvas.getContext('2d');
  if (!ctx) throw new Error('grid: unable to measure terminal font');
  ctx.font = `${fontSize}px ${fontFamily}`;
  return {
    cellWidth: Math.max(1, Math.ceil(ctx.measureText('M').width)),
    cellHeight: Math.max(1, Math.ceil(fontSize * 1.45)),
    baseline: Math.ceil(fontSize * 1.1),
  };
}

export function colorNumber(hex: string): number {
  return Number.parseInt(hex.slice(1), 16);
}

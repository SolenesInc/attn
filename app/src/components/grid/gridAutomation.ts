import type { TerminalGridStats, GridTileSummary } from './TerminalGrid';

export interface GridAutomationState {
  active: boolean;
  tileCount: number;
  zoomedId: string | null;
  layout: { rows: number; cols: number };
  stats: TerminalGridStats | null;
  tiles: GridTileSummary[];
}

export interface GridAutomationHandle {
  getState(): GridAutomationState;
  getTileText(id: string): string | null;
  zoom(id: string | null): void;
  hitTest(x: number, y: number): string | null;
  // Returns false if there is no stage to receive the keys.
  sendText(text: string): boolean;
}

let handle: GridAutomationHandle | null = null;

export function setGridAutomationHandle(next: GridAutomationHandle | null): void {
  handle = next;
}

export function getGridAutomationHandle(): GridAutomationHandle | null {
  return handle;
}

// State when grid mode isn't mounted — so callers get a stable shape either way.
export const INACTIVE_GRID_STATE: GridAutomationState = {
  active: false,
  tileCount: 0,
  zoomedId: null,
  layout: { rows: 0, cols: 0 },
  stats: null,
  tiles: [],
};

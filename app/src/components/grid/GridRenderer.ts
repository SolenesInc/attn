import type { GhosttyTerminal } from '../../ghostty';
import type { UISessionState } from '../../types/sessionState';

export interface TileModel {
  id: string;
  model: GhosttyTerminal;
}

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface TileFrame {
  id: string;
  rect: Rect;
  scale: number;
  alpha: number;
  attention: number;
  state: UISessionState;
  hidden: boolean;
  focused: boolean;
}

export interface GridRenderStats {
  drawCalls: number;
  quads: number;
  atlasUploads: number;
  atlasResets: number;
  liveContexts: number;
  cpuSubmitMs: number;
}

export interface GridRenderer {
  readonly name: string;
  mount(container: HTMLElement): void;
  setTiles(tiles: TileModel[]): void;
  frame(frames: TileFrame[], now: number): GridRenderStats;
  dispose(): void;
}

export const EMPTY_STATS: GridRenderStats = {
  drawCalls: 0,
  quads: 0,
  atlasUploads: 0,
  atlasResets: 0,
  liveContexts: 0,
  cpuSubmitMs: 0,
};

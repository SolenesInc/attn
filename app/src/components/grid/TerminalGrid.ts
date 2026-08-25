// The grid is an OBSERVER of the PTY stream: it drains terminal responses and
// must never echo them back, which would inject phantom input into the session.
import type { Ghostty, GhosttyTerminal } from '../../ghostty';
import type { UISessionState } from '../../types/sessionState';
import type { GridRenderer, GridRenderStats, Rect, TileFrame } from './GridRenderer';
import { TILE_COLS, TILE_ROWS, type CellMetrics } from './gridConfig';

const GAP = 12;
const REFLOW_MS = 280;
const ZOOM_MS = 300;
const ATTENTION_RATE = 5; // per second toward target
const STATS_WINDOW_MS = 1000;

const MAX_SEED_BUFFER = 512;

type TerminalConfig = Parameters<Ghostty['createTerminal']>[2];

// `id` is the session's PTY runtimeId: the key PTY output events arrive under.
export interface GridTileSpec {
  id: string;
  attention: boolean;
  state: UISessionState;
}

export interface TerminalGridStats extends GridRenderStats {
  fps: number;
  frameIntervalMsP50: number;
  frameIntervalMsP95: number;
  frameIntervalMsMax: number;
  slowFrameIntervals: number;
  tileCount: number;
  framePending: boolean;
  renderIdleMs: number;
}

export interface GridTileSummary {
  id: string;
  attention: number;
  hidden: boolean;
  focused: boolean;
  cols: number;
  rows: number;
  nonEmpty: boolean;
}

interface Tile {
  id: string;
  model: GhosttyTerminal;
  state: UISessionState;
  attention: number;
  attentionTarget: number;
  hidden: boolean;
  phase: 'seeding' | 'live';
  // lastSeq: -1 means no watermark yet, so everything applies.
  lastSeq: number;
  pending: Array<{ data: Uint8Array; seq: number | undefined }>;
}

interface Layout {
  rows: number;
  cols: number;
}

const easeOutCubic = (t: number): number => 1 - (1 - t) ** 3;
const clamp01 = (t: number): number => (t < 0 ? 0 : t > 1 ? 1 : t);
const lerp = (a: number, b: number, t: number): number => a + (b - a) * t;
const lerpRect = (a: Rect, b: Rect, t: number): Rect => ({
  x: lerp(a.x, b.x, t),
  y: lerp(a.y, b.y, t),
  w: lerp(a.w, b.w, t),
  h: lerp(a.h, b.h, t),
});

export class TerminalGrid {
  private readonly renderer: GridRenderer;
  private readonly ghostty: Ghostty;
  private readonly container: HTMLElement;
  private readonly metrics: CellMetrics;
  private readonly modelOptions: TerminalConfig;

  private tiles: Tile[] = [];
  private tileIndex = new Map<string, Tile>();
  private layout: Layout = { rows: 1, cols: 1 };

  private rafId: number | null = null;
  private started = false;
  private lastNow = 0;

  private reflowFrom = new Map<string, Rect>();
  private reflowStart = -1;

  private zoomId: string | null = null;
  private zoomFrom = 0;
  private zoomTarget = 0;
  private zoomStart = -1;
  private zoomT = 0;

  private lastFrames: TileFrame[] = [];
  private renderedAt: number[] = [];
  private frameIntervals: Array<{ at: number; ms: number }> = [];
  private lastRenderedAt: number | null = null;
  private lastStats: GridRenderStats | null = null;
  private statsEmitAt = 0;
  private visualDirty = true;

  onStats: ((stats: TerminalGridStats) => void) | null = null;

  constructor(
    renderer: GridRenderer,
    ghostty: Ghostty,
    container: HTMLElement,
    metrics: CellMetrics,
    modelOptions: TerminalConfig,
  ) {
    this.renderer = renderer;
    this.ghostty = ghostty;
    this.container = container;
    this.metrics = metrics;
    this.modelOptions = modelOptions;
    renderer.mount(container);
  }

  setLayout(rows: number, cols: number): void {
    if (rows === this.layout.rows && cols === this.layout.cols) return;
    this.beginReflow();
    this.layout = { rows, cols };
    this.visualDirty = true;
    this.requestFrame();
  }

  // Call this BEFORE mutating this.layout or this.tiles.
  private beginReflow(): void {
    this.reflowFrom.clear();
    this.tiles.forEach((tile, i) => {
      this.reflowFrom.set(tile.id, this.baseRect(i, this.layout, tile.model.cols, tile.model.rows).rect);
    });
    this.reflowStart = performance.now();
  }

  syncTiles(specs: GridTileSpec[]): void {
    const nextIds = new Set(specs.map((s) => s.id));
    // The rAF loop is render-on-demand: a membership change under an unchanged
    // grid shape dirties nothing, leaving the removed tile's frame on screen.
    const membershipChanged =
      this.tiles.length > 0 &&
      (specs.length !== this.tiles.length || specs.some((s, i) => this.tiles[i]?.id !== s.id));
    if (membershipChanged) this.beginReflow();

    for (const tile of this.tiles) {
      if (!nextIds.has(tile.id)) {
        tile.model.free();
        if (this.zoomId === tile.id) this.cancelZoom();
      }
    }

    const next: Tile[] = specs.map((spec) => {
      const existing = this.tileIndex.get(spec.id);
      if (existing) {
        if (existing.state !== spec.state || existing.attentionTarget !== (spec.attention ? 1 : 0)) {
          this.visualDirty = true;
        }
        existing.state = spec.state;
        existing.attentionTarget = spec.attention ? 1 : 0;
        return existing;
      }
      this.visualDirty = true;
      const model = this.ghostty.createTerminal(TILE_COLS, TILE_ROWS, this.modelOptions);
      return {
        id: spec.id,
        model,
        state: spec.state,
        attention: 0,
        attentionTarget: spec.attention ? 1 : 0,
        hidden: false,
        phase: 'live',
        lastSeq: -1,
        pending: [],
      };
    });

    this.tiles = next;
    this.tileIndex = new Map(next.map((t) => [t.id, t]));
    this.renderer.setTiles(this.tiles.map((t) => ({ id: t.id, model: t.model })));
    if (this.visualDirty || membershipChanged) this.requestFrame();
  }

  // An undefined `seq` (resets, replays) cannot be proven stale, so it always
  // applies.
  writeBytes(id: string, data: Uint8Array, seq?: number): void {
    const tile = this.tileIndex.get(id);
    if (!tile) return;
    if (tile.phase === 'seeding') {
      tile.pending.push({ data, seq });
      if (tile.pending.length > MAX_SEED_BUFFER) tile.pending.shift();
      return;
    }
    this.applyBytes(tile, data, seq);
  }

  beginSeeding(id: string): void {
    const tile = this.tileIndex.get(id);
    if (!tile || tile.phase === 'seeding') return;
    tile.phase = 'seeding';
    tile.lastSeq = -1;
    tile.pending = [];
  }

  // The snapshot is an absolute repaint at the session's real cols/rows, so the
  // model must be resized first or it clamps the paint.
  seedTile(id: string, snapshot: Uint8Array, lastSeq: number, cols?: number, rows?: number): void {
    const tile = this.tileIndex.get(id);
    if (!tile || tile.phase !== 'seeding') return;
    if (cols && rows && (tile.model.cols !== cols || tile.model.rows !== rows)) {
      tile.model.resize(cols, rows);
    }
    this.applyBytes(tile, snapshot, undefined);
    tile.lastSeq = lastSeq;
    tile.phase = 'live';
    this.flushPending(tile);
  }

  cancelSeeding(id: string): void {
    const tile = this.tileIndex.get(id);
    if (!tile || tile.phase !== 'seeding') return;
    tile.phase = 'live';
    this.flushPending(tile);
  }

  resizeTile(id: string, cols: number, rows: number): void {
    const tile = this.tileIndex.get(id);
    if (!tile || cols <= 0 || rows <= 0) return;
    if (tile.model.cols === cols && tile.model.rows === rows) return;
    tile.model.resize(cols, rows);
    this.visualDirty = true;
    this.requestFrame();
  }

  hasTile(id: string): boolean {
    return this.tileIndex.has(id);
  }

  toggleHide(id: string): void {
    const tile = this.tileIndex.get(id);
    if (!tile) return;
    tile.hidden = !tile.hidden;
    this.visualDirty = true;
    this.requestFrame();
  }

  zoomTo(id: string | null): void {
    const now = performance.now();
    if (id) {
      this.zoomId = id;
      this.zoomFrom = this.zoomT;
      this.zoomTarget = 1;
    } else {
      this.zoomFrom = this.zoomT;
      this.zoomTarget = 0;
    }
    this.zoomStart = now;
    this.visualDirty = true;
    this.requestFrame();
  }

  isZoomed(): boolean {
    return this.zoomId !== null && this.zoomTarget === 1;
  }

  zoomedId(): string | null {
    return this.zoomTarget === 1 ? this.zoomId : null;
  }

  inputTarget(): GhosttyTerminal | null {
    const id = this.zoomedId();
    const tile = id ? this.tileIndex.get(id) : null;
    return tile?.model ?? null;
  }

  getStats(): TerminalGridStats | null {
    return this.lastStats ? this.summarizeStats(performance.now()) : null;
  }

  currentLayout(): Layout {
    return { ...this.layout };
  }

  tileSummaries(): GridTileSummary[] {
    const focusedById = new Map(this.lastFrames.map((f) => [f.id, f.focused]));
    return this.tiles.map((tile) => ({
      id: tile.id,
      attention: tile.attention,
      hidden: tile.hidden,
      focused: focusedById.get(tile.id) ?? false,
      cols: tile.model.cols,
      rows: tile.model.rows,
      nonEmpty: this.modelNonEmpty(tile.model),
    }));
  }

  getTileText(id: string): string | null {
    const tile = this.tileIndex.get(id);
    if (!tile) return null;
    return this.modelText(tile.model);
  }

  // Recomputes the static placement, so unlike hitTest it is correct without a
  // running render loop.
  tileAt(clientX: number, clientY: number): { id: string; rect: Rect } | null {
    const box = this.container.getBoundingClientRect();
    const x = clientX - box.left;
    const y = clientY - box.top;
    for (let i = 0; i < this.tiles.length; i += 1) {
      const tile = this.tiles[i];
      if (tile.hidden) continue;
      const { rect } = this.baseRect(i, this.layout, tile.model.cols, tile.model.rows);
      if (x >= rect.x && x <= rect.x + rect.w && y >= rect.y && y <= rect.y + rect.h) {
        return { id: tile.id, rect };
      }
    }
    return null;
  }

  hitTest(clientX: number, clientY: number): string | null {
    const box = this.container.getBoundingClientRect();
    const x = clientX - box.left;
    const y = clientY - box.top;
    // Iterate in reverse so the zoomed/topmost tile wins.
    for (let i = this.lastFrames.length - 1; i >= 0; i -= 1) {
      const f = this.lastFrames[i];
      if (f.hidden || f.alpha <= 0.02) continue;
      if (x >= f.rect.x && x <= f.rect.x + f.rect.w && y >= f.rect.y && y <= f.rect.y + f.rect.h) {
        return f.id;
      }
    }
    return null;
  }

  start(): void {
    if (this.started) return;
    this.started = true;
    this.lastNow = performance.now();
    this.requestFrame();
  }

  invalidate(): void {
    this.visualDirty = true;
    this.requestFrame();
  }

  dispose(): void {
    this.started = false;
    if (this.rafId !== null) cancelAnimationFrame(this.rafId);
    this.rafId = null;
    this.tiles.forEach((tile) => tile.model.free());
    this.tiles = [];
    this.tileIndex.clear();
    this.renderer.dispose();
  }

  private cancelZoom(): void {
    this.zoomId = null;
    this.zoomT = 0;
    this.zoomTarget = 0;
  }

  private requestFrame(): void {
    if (!this.started || this.rafId !== null) return;
    this.rafId = requestAnimationFrame(() => {
      this.rafId = null;
      this.tick();
    });
  }

  private applyBytes(tile: Tile, data: Uint8Array, seq: number | undefined): void {
    if (seq !== undefined && tile.lastSeq >= 0 && seq <= tile.lastSeq) return;
    tile.model.write(data);
    while (tile.model.hasResponse()) tile.model.readResponse();
    if (seq !== undefined) tile.lastSeq = seq;
    this.visualDirty = true;
    this.requestFrame();
  }

  private flushPending(tile: Tile): void {
    const pending = tile.pending;
    tile.pending = [];
    for (const chunk of pending) this.applyBytes(tile, chunk.data, chunk.seq);
  }

  private modelText(model: GhosttyTerminal): string {
    model.update();
    const { cols, rows } = model;
    const cells = model.getViewport(); // reused pool — consume before any other getViewport()
    const lines: string[] = [];
    for (let row = 0; row < rows; row += 1) {
      let line = '';
      for (let col = 0; col < cols; col += 1) {
        const cell = cells[row * cols + col];
        if (!cell || cell.width === 0) continue;
        line += cell.codepoint && cell.codepoint !== 0 ? String.fromCodePoint(cell.codepoint) : ' ';
      }
      lines.push(line.replace(/\s+$/, ''));
    }
    return lines.join('\n').replace(/\n+$/, '');
  }

  private modelNonEmpty(model: GhosttyTerminal): boolean {
    model.update();
    const { cols, rows } = model;
    const cells = model.getViewport();
    for (let i = 0; i < rows * cols; i += 1) {
      const cell = cells[i];
      if (cell && cell.width > 0 && cell.codepoint && cell.codepoint !== 32) return true;
    }
    return false;
  }

  private tick(): void {
    const now = performance.now();
    const dt = Math.min(0.1, (now - this.lastNow) / 1000);
    this.lastNow = now;

    let attentionAnimating = false;
    let attentionChanged = false;
    for (const tile of this.tiles) {
      const delta = tile.attentionTarget - tile.attention;
      if (Math.abs(delta) <= 0.001) {
        if (tile.attention !== tile.attentionTarget) {
          tile.attention = tile.attentionTarget;
          attentionChanged = true;
        }
        continue;
      }
      const next = tile.attention + delta * Math.min(1, dt * ATTENTION_RATE);
      tile.attention = Math.abs(tile.attentionTarget - next) <= 0.001
        ? tile.attentionTarget
        : next;
      attentionChanged = true;
      if (tile.attention !== tile.attentionTarget) attentionAnimating = true;
    }

    let zoomChanged = false;
    if (this.zoomStart >= 0) {
      const t = clamp01((now - this.zoomStart) / ZOOM_MS);
      this.zoomT = lerp(this.zoomFrom, this.zoomTarget, easeOutCubic(t));
      zoomChanged = true;
      if (t >= 1) {
        this.zoomStart = -1;
        if (this.zoomTarget === 0) this.zoomId = null;
      }
    }
    const hadReflow = this.reflowStart >= 0;
    const reflowActive = hadReflow && now - this.reflowStart < REFLOW_MS;
    if (hadReflow && !reflowActive) this.reflowStart = -1;

    const shouldRender = hadReflow || zoomChanged || attentionChanged || this.visualDirty;
    let rendered = false;
    if (shouldRender) {
      this.visualDirty = false;
      const frames = this.computeFrames(now, reflowActive);
      this.lastFrames = frames;
      this.lastStats = this.renderer.frame(frames, now);
      this.recordRenderedFrame(now);
      rendered = true;
    }

    if (rendered && now - this.statsEmitAt > 200) {
      this.statsEmitAt = now;
      this.onStats?.(this.summarizeStats(now));
    }

    if (reflowActive || this.zoomStart >= 0 || attentionAnimating) this.requestFrame();
  }

  private computeFrames(now: number, reflowActive: boolean): TileFrame[] {
    const reflowT = reflowActive ? easeOutCubic(clamp01((now - this.reflowStart) / REFLOW_MS)) : 1;
    const zoomT = easeOutCubic(clamp01(this.zoomT));

    return this.tiles.map((tile, i): TileFrame => {
      const tileCols = tile.model.cols;
      const tileRows = tile.model.rows;
      const base = this.baseRect(i, this.layout, tileCols, tileRows);
      let rect = base.rect;
      let scale = base.scale;

      if (reflowActive) {
        const from = this.reflowFrom.get(tile.id);
        if (from) {
          rect = lerpRect(from, base.rect, reflowT);
          const native = tileCols * this.metrics.cellWidth;
          scale = rect.w / native;
        }
      }

      let alpha = 1;
      let focused = false;
      if (this.zoomId) {
        if (tile.id === this.zoomId) {
          const full = this.fullRect(tileCols, tileRows);
          rect = lerpRect(base.rect, full.rect, zoomT);
          scale = lerp(base.scale, full.scale, zoomT);
          focused = zoomT > 0.5;
        } else {
          alpha = lerp(1, 0.16, zoomT);
        }
      }

      return {
        id: tile.id,
        rect,
        scale,
        alpha,
        attention: tile.id === this.zoomId ? 0 : tile.attention,
        state: tile.state,
        hidden: tile.hidden,
        focused,
      };
    });
  }

  private baseRect(index: number, layout: Layout, nativeCols: number, nativeRows: number): { rect: Rect; scale: number } {
    const { rows, cols } = layout;
    const W = this.container.clientWidth;
    const H = this.container.clientHeight;
    const slotW = (W - (cols - 1) * GAP) / cols;
    const slotH = (H - (rows - 1) * GAP) / rows;
    const r = Math.floor(index / cols);
    const c = index % cols;
    const slotLeft = c * (slotW + GAP);
    const slotTop = r * (slotH + GAP);
    return this.fitInto(slotLeft, slotTop, slotW, slotH, nativeCols, nativeRows);
  }

  private fullRect(nativeCols: number, nativeRows: number): { rect: Rect; scale: number } {
    return this.fitInto(0, 0, this.container.clientWidth, this.container.clientHeight, nativeCols, nativeRows);
  }

  private fitInto(
    left: number,
    top: number,
    slotW: number,
    slotH: number,
    nativeCols: number,
    nativeRows: number,
  ): { rect: Rect; scale: number } {
    const nativeW = Math.max(1, nativeCols) * this.metrics.cellWidth;
    const nativeH = Math.max(1, nativeRows) * this.metrics.cellHeight;
    const scale = Math.max(0.01, Math.min(slotW / nativeW, slotH / nativeH));
    const w = nativeW * scale;
    const h = nativeH * scale;
    return {
      rect: { x: left + (slotW - w) / 2, y: top + (slotH - h) / 2, w, h },
      scale,
    };
  }

  private recordRenderedFrame(now: number): void {
    if (this.lastRenderedAt !== null && now > this.lastRenderedAt) {
      this.frameIntervals.push({ at: now, ms: now - this.lastRenderedAt });
    }
    this.lastRenderedAt = now;
    this.renderedAt.push(now);
    this.pruneStats(now);
  }

  private pruneStats(now: number): void {
    const cutoff = now - STATS_WINDOW_MS;
    while (this.renderedAt.length > 0 && this.renderedAt[0] < cutoff) this.renderedAt.shift();
    while (this.frameIntervals.length > 0 && this.frameIntervals[0].at < cutoff) this.frameIntervals.shift();
  }

  private summarizeStats(now: number): TerminalGridStats {
    this.pruneStats(now);
    const intervals = this.frameIntervals.map((sample) => sample.ms);
    const sorted = [...intervals].sort((a, b) => a - b);
    const n = sorted.length;
    const pct = (p: number) => (n ? sorted[Math.min(n - 1, Math.floor(p * n))] : 0);
    const stats = this.lastStats ?? {
      drawCalls: 0, quads: 0, atlasUploads: 0, atlasResets: 0, liveContexts: 0, cpuSubmitMs: 0,
    };
    return {
      ...stats,
      fps: this.renderedAt.length,
      frameIntervalMsP50: pct(0.5),
      frameIntervalMsP95: pct(0.95),
      frameIntervalMsMax: sorted[n - 1] ?? 0,
      slowFrameIntervals: intervals.filter((value) => value > 32).length,
      tileCount: this.tiles.filter((t) => !t.hidden).length,
      framePending: this.rafId !== null,
      renderIdleMs: this.lastRenderedAt === null ? 0 : Math.max(0, now - this.lastRenderedAt),
    };
  }
}

import { describe, it, expect, vi } from 'vitest';
import { TerminalGrid, type GridTileSpec } from './TerminalGrid';
import { EMPTY_STATS, type GridRenderer, type TileFrame, type TileModel } from './GridRenderer';

// Minimal fakes: the terminal grid takes its renderer + ghostty + container as
// injected deps, so we can exercise all of its logic without WebGL or the WASM
// VT engine. Only the handful of model methods the terminal grid actually calls are
// implemented.

interface FakeCell {
  codepoint: number;
  width: number;
}

function viewportFromText(cols: number, rows: number, text: string): FakeCell[] {
  const lines = text.split('\n');
  const cells: FakeCell[] = [];
  for (let r = 0; r < rows; r += 1) {
    const line = lines[r] ?? '';
    for (let c = 0; c < cols; c += 1) {
      const ch = line[c];
      cells.push({ codepoint: ch ? (ch.codePointAt(0) ?? 32) : 32, width: 1 });
    }
  }
  return cells;
}

class FakeModel {
  writes: Uint8Array[] = [];
  responses: string[] = [];
  resizes: Array<{ cols: number; rows: number }> = [];
  modes: Record<number, boolean> = {};
  freed = false;
  viewport: FakeCell[];

  constructor(public cols: number, public rows: number, text = '') {
    this.viewport = viewportFromText(cols, rows, text);
  }

  setText(text: string) {
    this.viewport = viewportFromText(this.cols, this.rows, text);
  }

  write(data: Uint8Array | string) {
    this.writes.push(data as Uint8Array);
  }
  resize(cols: number, rows: number) {
    this.cols = cols;
    this.rows = rows;
    this.resizes.push({ cols, rows });
    this.viewport = viewportFromText(cols, rows, '');
  }
  update() {
    return 0;
  }
  markClean() {}
  getViewport() {
    return this.viewport;
  }
  getCursor() {
    return { x: 0, y: 0, visible: false };
  }
  getMode(mode: number) {
    return this.modes[mode] ?? false;
  }
  hasResponse() {
    return this.responses.length > 0;
  }
  readResponse() {
    return this.responses.shift() ?? null;
  }
  getGraphemeString() {
    return '';
  }
  getScrollbackLength() {
    return 0;
  }
  free() {
    this.freed = true;
  }
}

class FakeRenderer implements GridRenderer {
  readonly name = 'fake';
  tiles: TileModel[] = [];
  frames: TileFrame[][] = [];
  disposed = false;
  mount() {}
  setTiles(tiles: TileModel[]) {
    this.tiles = tiles;
  }
  frame(frames: TileFrame[]) {
    this.frames.push(frames);
    return EMPTY_STATS;
  }
  dispose() {
    this.disposed = true;
  }
}

function makeTerminalGrid() {
  const created: FakeModel[] = [];
  const ghostty = {
    createTerminal(cols: number, rows: number) {
      const model = new FakeModel(cols, rows);
      created.push(model);
      return model;
    },
  };
  const container = {
    clientWidth: 800,
    clientHeight: 600,
    getBoundingClientRect: () => ({ left: 0, top: 0, right: 800, bottom: 600, width: 800, height: 600 }),
  };
  const metrics = { cellWidth: 8, cellHeight: 16, baseline: 12 };
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const grid = new TerminalGrid(new FakeRenderer(), ghostty as any, container as any, metrics, {} as any);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const renderer = (grid as any).renderer as FakeRenderer;
  return { grid, renderer, created };
}

function tileSpec(
  id: string,
  overrides: Partial<Omit<GridTileSpec, 'id'>> = {},
): GridTileSpec {
  return { id, attention: false, state: 'working', ...overrides };
}

describe('TerminalGrid', () => {
  it('creates one model per tile spec and registers them with the renderer', () => {
    const { grid, renderer, created } = makeTerminalGrid();
    grid.syncTiles([tileSpec('a'), tileSpec('b', { attention: true, state: 'waiting_input' })]);

    expect(created).toHaveLength(2);
    expect(renderer.tiles.map((t) => t.id)).toEqual(['a', 'b']);
    expect(grid.hasTile('a')).toBe(true);
    expect(grid.hasTile('b')).toBe(true);
  });

  it('preserves existing models across syncs and frees only removed ones', () => {
    const { grid, created } = makeTerminalGrid();
    grid.syncTiles([tileSpec('a'), tileSpec('b')]);
    const [modelA, modelB] = created;

    grid.syncTiles([tileSpec('a')]); // drop b

    expect(created).toHaveLength(2); // no new model created for the kept tile
    expect(modelA.freed).toBe(false);
    expect(modelB.freed).toBe(true);
    expect(grid.hasTile('b')).toBe(false);
  });

  it('forces a render when a tile is removed even if the grid shape is unchanged', () => {
    // Regression: the rAF loop is render-on-demand (it paints only when a model
    // is dirty or an animation is live). Fake models never report dirty, so this
    // isolates the membership path: removing a tile while the resolved shape stays
    // 2×2 must still trigger a render, or the removed tile's stale frame lingers
    // until the next dirtying event ("only every other hide actually hides").
    const { grid, renderer } = makeTerminalGrid();
    const spec = (id: string) => tileSpec(id);
    grid.syncTiles(['a', 'b', 'c', 'd'].map(spec));
    grid.setLayout(2, 2);

    // Settle the mount/layout reflow so the renderer is idle, then confirm an idle
    // tick draws nothing (the render-on-demand baseline this bug violated).
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const internal = grid as any;
    internal.reflowStart = -1;
    internal.tick();
    const before = renderer.frames.length;
    internal.tick();
    expect(renderer.frames.length).toBe(before);

    // Remove one tile; the resolved shape is still 2×2, so setLayout no-ops.
    grid.syncTiles(['a', 'b', 'c'].map(spec));
    grid.setLayout(2, 2);
    internal.tick();

    expect(renderer.frames.length).toBeGreaterThan(before);
    const last = renderer.frames[renderer.frames.length - 1];
    expect(last.map((f) => f.id)).toEqual(['a', 'b', 'c']);
  });

  it('renders state-only changes even when the terminal model is clean', () => {
    const { grid, renderer } = makeTerminalGrid();
    const internal = grid as any;
    grid.syncTiles([tileSpec('a')]);
    internal.reflowStart = -1;
    internal.tick();
    const before = renderer.frames.length;

    grid.syncTiles([tileSpec('a', { state: 'idle' })]);
    internal.tick();

    expect(renderer.frames.length).toBe(before + 1);
    expect(renderer.frames[renderer.frames.length - 1]?.[0]).toMatchObject({
      state: 'idle',
    });
  });

  it('renders waiting input once, then becomes quiescent', () => {
    const { grid, renderer } = makeTerminalGrid();
    const internal = grid as any;
    grid.syncTiles([tileSpec('a', { state: 'waiting_input', attention: false })]);
    internal.reflowStart = -1;
    internal.tick();
    const before = renderer.frames.length;

    internal.tick();

    expect(renderer.frames.length).toBe(before);
  });

  it('renders a scheduled tile once, then becomes quiescent', () => {
    const { grid, renderer } = makeTerminalGrid();
    const internal = grid as any;
    grid.syncTiles([tileSpec('a', { state: 'scheduled', attention: false })]);
    internal.reflowStart = -1;
    internal.tick();
    const before = renderer.frames.length;

    internal.tick();

    expect(renderer.frames.length).toBe(before);
  });

  it('drains at rest, wakes for output, and stops after attention settles', () => {
    const callbacks: FrameRequestCallback[] = [];
    let now = 0;
    const request = vi.spyOn(globalThis, 'requestAnimationFrame').mockImplementation((callback) => {
      callbacks.push(callback);
      return callbacks.length;
    });
    const cancel = vi.spyOn(globalThis, 'cancelAnimationFrame').mockImplementation(() => undefined);
    const clock = vi.spyOn(performance, 'now').mockImplementation(() => now);

    try {
      const { grid, renderer } = makeTerminalGrid();
      grid.syncTiles([tileSpec('a')]);
      grid.start();
      expect(callbacks).toHaveLength(1);

      now = 16;
      callbacks.shift()?.(now);
      expect(renderer.frames).toHaveLength(1);
      expect(callbacks).toHaveLength(0);

      grid.writeBytes('a', new Uint8Array([104, 105]));
      expect(callbacks).toHaveLength(1);
      now = 32;
      callbacks.shift()?.(now);
      expect(renderer.frames).toHaveLength(2);
      expect(callbacks).toHaveLength(0);

      grid.syncTiles([tileSpec('a', { attention: true })]);
      let transitionFrames = 0;
      while (callbacks.length > 0 && transitionFrames < 20) {
        now += 100;
        callbacks.shift()?.(now);
        transitionFrames += 1;
      }
      expect(transitionFrames).toBeGreaterThan(0);
      expect(callbacks).toHaveLength(0);
      expect(grid.tileSummaries()[0]?.attention).toBe(1);

      now += 1001;
      expect(grid.getStats()).toMatchObject({
        fps: 0,
        frameIntervalMsP50: 0,
        frameIntervalMsP95: 0,
        frameIntervalMsMax: 0,
        slowFrameIntervals: 0,
        framePending: false,
        renderIdleMs: 1001,
      });

      grid.dispose();
    } finally {
      request.mockRestore();
      cancel.mockRestore();
      clock.mockRestore();
    }
  });

  it('routes live bytes to the matching model and drains responses without echoing them', () => {
    const { grid, created } = makeTerminalGrid();
    grid.syncTiles([tileSpec('a')]);
    const model = created[0];
    model.responses = ['\x1b[0n', 'extra'];

    grid.writeBytes('a', new Uint8Array([104, 105]));

    expect(model.writes).toHaveLength(1);
    expect(Array.from(model.writes[0])).toEqual([104, 105]);
    // Observer must consume responses (so the engine doesn't buffer) but never
    // send them anywhere — the real pane answers terminal queries.
    expect(model.responses).toEqual([]);

    expect(() => grid.writeBytes('unknown', new Uint8Array([1]))).not.toThrow();
  });

  it('exposes a tile\'s visible screen text and reports emptiness', () => {
    const { grid, created } = makeTerminalGrid();
    grid.syncTiles([tileSpec('a')]);
    const model = created[0];

    expect(grid.getTileText('a')).toBe('');
    expect(grid.tileSummaries()[0].nonEmpty).toBe(false);

    model.setText('hello world');
    expect(grid.getTileText('a')).toBe('hello world');
    expect(grid.getTileText('missing')).toBeNull();

    const summary = grid.tileSummaries()[0];
    expect(summary).toMatchObject({ id: 'a', cols: 80, rows: 24, nonEmpty: true });
  });

  it('tracks zoom state and cancels zoom when the zoomed tile disappears', () => {
    const { grid } = makeTerminalGrid();
    grid.syncTiles([tileSpec('a'), tileSpec('b')]);

    expect(grid.isZoomed()).toBe(false);
    grid.zoomTo('a');
    expect(grid.isZoomed()).toBe(true);
    expect(grid.zoomedId()).toBe('a');

    grid.syncTiles([tileSpec('b')]); // remove the zoomed tile
    expect(grid.zoomedId()).toBeNull();
    expect(grid.isZoomed()).toBe(false);
  });

  it('returns the zoomed tile model as the input target', () => {
    const { grid, created } = makeTerminalGrid();
    grid.syncTiles([tileSpec('a'), tileSpec('b')]);

    expect(grid.inputTarget()).toBeNull();

    grid.zoomTo('a');
    expect(grid.inputTarget()).toBe(created[0]);

    grid.zoomTo('b');
    expect(grid.inputTarget()).toBe(created[1]);
  });

  it('toggles tile visibility', () => {
    const { grid } = makeTerminalGrid();
    grid.syncTiles([tileSpec('a')]);
    expect(grid.tileSummaries()[0].hidden).toBe(false);
    grid.toggleHide('a');
    expect(grid.tileSummaries()[0].hidden).toBe(true);
  });

  it('resolves the resting tile + rect under a pointer, on demand without a render loop', () => {
    const { grid } = makeTerminalGrid(); // container 800×600, cell 8×16, models 80×24
    grid.syncTiles([tileSpec('a')]);
    grid.setLayout(1, 1);

    // A single 80×24 tile fits 800×600 at scale 1.25 → 800×480, centred vertically
    // (y inset (600-480)/2 = 60). Centre of the grid hits it.
    const hit = grid.tileAt(400, 300);
    expect(hit?.id).toBe('a');
    expect(hit?.rect).toMatchObject({ x: 0, w: 800, h: 480 });
    expect(hit?.rect.y).toBeCloseTo(60);

    // Above the letterboxed tile (y < 60) is empty space.
    expect(grid.tileAt(400, 30)).toBeNull();
  });

  it('frees every model on dispose', () => {
    const { grid, renderer, created } = makeTerminalGrid();
    grid.syncTiles([tileSpec('a'), tileSpec('b')]);
    grid.dispose();
    expect(created.every((m) => m.freed)).toBe(true);
    expect(renderer.disposed).toBe(true);
  });
});

describe('TerminalGrid seeding + sequence dedup', () => {
  const bytes = (...vals: number[]) => new Uint8Array(vals);
  const written = (model: FakeModel) => model.writes.map((w) => Array.from(w));

  it('applies live bytes immediately and drops chunks at or below the watermark', () => {
    const { grid, created } = makeTerminalGrid();
    grid.syncTiles([tileSpec('a')]);
    const model = created[0];

    grid.writeBytes('a', bytes(1), 5); // first seq sets the watermark
    grid.writeBytes('a', bytes(2), 3); // stale: already painted, dropped
    grid.writeBytes('a', bytes(3), 6); // advances past watermark, applied
    grid.writeBytes('a', bytes(4)); // seq-less: cannot be proven stale, applied

    expect(written(model)).toEqual([[1], [3], [4]]);
  });

  it('buffers live bytes while seeding, then paints the snapshot before flushing newer ones', () => {
    const { grid, created } = makeTerminalGrid();
    grid.syncTiles([tileSpec('a')]);
    const model = created[0];

    grid.beginSeeding('a');
    grid.writeBytes('a', bytes(9), 8); // will be <= seed watermark -> dropped
    grid.writeBytes('a', bytes(11), 11); // newer than the snapshot -> kept
    expect(model.writes).toHaveLength(0); // nothing painted until the seed lands

    grid.seedTile('a', bytes(100, 101), 10, 80, 24);

    // Snapshot paints first; only the buffered chunk past seq 10 follows.
    expect(written(model)).toEqual([[100, 101], [11]]);
  });

  it('resizes the model to the snapshot geometry so an absolute repaint is not clamped', () => {
    const { grid, created } = makeTerminalGrid();
    grid.syncTiles([tileSpec('a')]);
    const model = created[0];
    expect([model.cols, model.rows]).toEqual([80, 24]);

    grid.beginSeeding('a');
    grid.seedTile('a', bytes(1), 0, 120, 40);

    expect([model.cols, model.rows]).toEqual([120, 40]);
    expect(model.resizes).toContainEqual({ cols: 120, rows: 40 });
  });

  it('flushes buffered bytes best-effort when seeding is cancelled (no snapshot)', () => {
    const { grid, created } = makeTerminalGrid();
    grid.syncTiles([tileSpec('a')]);
    const model = created[0];

    grid.beginSeeding('a');
    grid.writeBytes('a', bytes(1));
    grid.writeBytes('a', bytes(2));
    expect(model.writes).toHaveLength(0);

    grid.cancelSeeding('a');

    expect(written(model)).toEqual([[1], [2]]);
  });

  it('ignores seedTile unless the tile is awaiting a seed', () => {
    const { grid, created } = makeTerminalGrid();
    grid.syncTiles([tileSpec('a')]);
    const model = created[0];

    // Tiles default to live; a stray seed must not clobber live content.
    grid.seedTile('a', bytes(1), 5);
    expect(model.writes).toHaveLength(0);
  });

  it('tracks live geometry via resizeTile and no-ops on unchanged or invalid sizes', () => {
    const { grid, created } = makeTerminalGrid();
    grid.syncTiles([tileSpec('a')]);
    const model = created[0];

    grid.resizeTile('a', 100, 30);
    expect([model.cols, model.rows]).toEqual([100, 30]);

    grid.resizeTile('a', 100, 30); // unchanged
    grid.resizeTile('a', 0, 0); // invalid
    expect(model.resizes).toHaveLength(1);
  });
});

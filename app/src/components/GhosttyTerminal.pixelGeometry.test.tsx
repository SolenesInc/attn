// Renderer cell metrics are CSS pixels; the PTY's ws_xpixel/ws_ypixel are device
// pixels. Reporting CSS pixels halves the reported pane size on retina.
import { act, render, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const CELL_W = 9;
const CELL_H = 23;
const FIT_COLS = 40;
const FIT_ROWS = 12;

const mocks = vi.hoisted(() => {
  const rendererConfig = { dpr: 2, cols: 40, rows: 12 };

  const createTerminal = () => ({
    cols: 80,
    rows: 24,
    write() {},
    resize(cols: number, rows: number) {
      this.cols = cols;
      this.rows = rows;
    },
    getMode: () => false,
    getScrollbackLength: () => 0,
    getViewport: () => [],
    getScrollbackLine: () => [],
    getGraphemeString: () => '',
    getScrollbackGraphemeString: () => '',
    getCursor: () => ({ x: 0, y: 0 }),
    hasResponse: () => false,
    readResponse: () => null,
    free: () => undefined,
    isAlternateScreen: () => false,
    hasMouseTracking: () => false,
  });

  class MockRenderer {
    readonly cellWidth = 9;
    readonly cellHeight = 23;
    readonly dpr = rendererConfig.dpr;

    fitDimensions() {
      return { cols: rendererConfig.cols, rows: rendererConfig.rows };
    }

    resize() {}
    render() {
      return { quads: 0, cellsArrayLen: 0, printableSkippedNull: 0, printableSkippedZeroWidth: 0 };
    }
    invalidateGlyphCache() {}
    setFontSize() {}
    dispose() {}
  }

  return { MockRenderer, createTerminal, rendererConfig };
});

vi.mock('../ghostty/wasm', () => ({ loadGhostty: async () => ({ createTerminal: mocks.createTerminal }) }));
vi.mock('./GhosttyWebGlRenderer', () => ({ WebGlTerminalRenderer: mocks.MockRenderer }));
vi.mock('../utils/terminalIconFont', () => ({
  ensureTerminalIconFont: () => new Promise<void>(() => undefined),
}));
vi.mock('../utils/terminalDiagnosticsLog', () => ({
  TERMINAL_DIAGNOSTICS_FILE: 'terminal-diagnostics.jsonl',
  disposePaneDiagnostics: () => undefined,
  noteModelFault: () => undefined,
  noteRecovery: () => undefined,
  noteResize: () => undefined,
  recordDiag: () => undefined,
  recordPaint: () => undefined,
  registerRenderProbe: () => undefined,
}));
vi.mock('../utils/uiDiagnosticsLog', () => ({
  captureUiSnapshot: () => ({}),
  recordUiDiag: () => undefined,
  UI_DIAGNOSTICS_FILE: 'diagnostics.jsonl',
}));
vi.mock('../utils/terminalPerf', () => ({ registerTerminalPerfGetter: () => () => undefined }));

import { GhosttyTerminal, type GhosttyTerminalHandle } from './GhosttyTerminal';

// The suite-wide ResizeObserver stub is a vi.fn(), which `new` does not build
// into an observer the component can hold, so the pane never reaches onReady.
const resizeCallbacks: ResizeObserverCallback[] = [];
beforeEach(() => {
  resizeCallbacks.length = 0;
  mocks.rendererConfig.cols = FIT_COLS;
  mocks.rendererConfig.rows = FIT_ROWS;
  globalThis.ResizeObserver = class {
    constructor(callback: ResizeObserverCallback) { resizeCallbacks.push(callback); }
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

async function fitOnce(dpr: number) {
  mocks.rendererConfig.dpr = dpr;
  const onResize = vi.fn();
  let ready: GhosttyTerminalHandle | null = null;
  render(
    <GhosttyTerminal
      fontSize={14}
      debugName="pixel-geometry-test"
      onInput={vi.fn()}
      onReady={(terminal) => { ready = terminal; }}
      onResize={onResize}
    />,
  );
  await waitFor(() => expect(ready).not.toBeNull());
  await act(async () => {
    (ready as unknown as GhosttyTerminalHandle).fit();
  });
  await waitFor(() => expect(onResize).toHaveBeenCalled());
  return onResize;
}

describe('GhosttyTerminal fit pixel geometry', () => {
  it('reports the pane total in device pixels on a retina display', async () => {
    const onResize = await fitOnce(2);

    expect(onResize).toHaveBeenLastCalledWith(FIT_COLS, FIT_ROWS, expect.objectContaining({
      reason: 'ghostty_fit',
      xpixel: FIT_COLS * CELL_W * 2,
      ypixel: FIT_ROWS * CELL_H * 2,
    }));
  });

  it('measures observer updates after layout and coalesces a window resize without a divider drag', async () => {
    const onResize = await fitOnce(2);
    vi.useFakeTimers();
    const frames = new Map<number, FrameRequestCallback>();
    let frameId = 0;
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
      frames.set(++frameId, callback);
      return frameId;
    });
    vi.stubGlobal('cancelAnimationFrame', (id: number) => frames.delete(id));
    const frame = async () => act(async () => {
      const callbacks = [...frames.values()];
      frames.clear();
      callbacks.forEach((callback) => callback(performance.now()));
    });
    await act(async () => { vi.advanceTimersByTime(300); });
    onResize.mockClear();
    mocks.rendererConfig.cols = 45;
    resizeCallbacks[0]([], {} as ResizeObserver);
    expect(onResize).not.toHaveBeenCalled();
    // React projects the surviving pane before the queued fit reads its bounds.
    mocks.rendererConfig.cols = 55;
    await frame();
    expect(onResize).toHaveBeenCalledExactlyOnceWith(55, 12, expect.anything());

    mocks.rendererConfig.cols = 60;
    resizeCallbacks[0]([], {} as ResizeObserver);
    await frame();
    mocks.rendererConfig.cols = 70;
    resizeCallbacks[0]([], {} as ResizeObserver);
    await frame();
    expect(onResize).toHaveBeenCalledTimes(1);
    await act(async () => { vi.advanceTimersByTime(250); });
    expect(onResize).toHaveBeenCalledTimes(2);
    expect(onResize).toHaveBeenLastCalledWith(70, 12, expect.objectContaining({ xpixel: 1260 }));
  });

  it('reports CSS pixels unchanged on a 1x display', async () => {
    const onResize = await fitOnce(1);

    expect(onResize).toHaveBeenLastCalledWith(FIT_COLS, FIT_ROWS, expect.objectContaining({
      xpixel: FIT_COLS * CELL_W,
      ypixel: FIT_ROWS * CELL_H,
    }));
  });
});

import { act, fireEvent, render, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const COLS = 80;
const ROWS = 24;

const mocks = vi.hoisted(() => {
  const state = { responses: [] as string[] };

  const createTerminal = () => ({
    cols: COLS,
    rows: ROWS,
    write() {},
    resize(cols: number, rows: number) {
      this.cols = cols;
      this.rows = rows;
    },
    // 1006 is SGR mouse encoding; every other queried mode is off.
    getMode: (mode: number) => mode === 1006,
    getScrollbackLength: () => 0,
    getViewport: () => [],
    getActiveLine: () => [],
    getScrollbackLine: () => [],
    rowWrapsIntoNext: () => false,
    getGraphemeString: () => '',
    getHyperlinkUri: () => null,
    getScrollbackGraphemeString: () => '',
    getCursor: () => ({ x: 0, y: 0 }),
    hasResponse: () => state.responses.length > 0,
    readResponse: () => state.responses.shift() ?? null,
    encodeKey: () => 'a',
    free: () => undefined,
    isAlternateScreen: () => false,
    hasMouseTracking: () => true,
    adoptSnapshot: () => ({ rows: 0, next: () => null, close: () => undefined }),
  });

  class MockRenderer {
    readonly cellWidth = 8;
    readonly cellHeight = 16;
    readonly dpr = 1;

    fitDimensions() {
      return { cols: COLS, rows: ROWS };
    }

    resize() {}
    render() {
      return { quads: 0, cellsArrayLen: 0, printableSkippedNull: 0, printableSkippedZeroWidth: 0 };
    }
    invalidateGlyphCache() {}
    setFontSize() {}
    dispose() {}
  }

  return { MockRenderer, createTerminal, state };
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
vi.mock('../utils/terminalLinkHitTestLog', () => ({
  TERMINAL_LINK_HIT_TEST_FILE: 'terminal-link-hit-test.jsonl',
  recordTerminalLinkHitTestEvent: () => undefined,
}));
vi.mock('../utils/uiDiagnosticsLog', () => ({
  captureUiSnapshot: () => ({}),
  recordUiDiag: () => undefined,
  UI_DIAGNOSTICS_FILE: 'diagnostics.jsonl',
}));
vi.mock('../utils/terminalPerf', () => ({ registerTerminalPerfGetter: () => () => undefined }));

import { GhosttyTerminal, type GhosttyTerminalHandle } from './GhosttyTerminal';

const frames: FrameRequestCallback[] = [];

beforeEach(() => {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
  frames.length = 0;
  globalThis.requestAnimationFrame = ((callback: FrameRequestCallback) => frames.push(callback)) as typeof requestAnimationFrame;
  globalThis.cancelAnimationFrame = (() => undefined) as typeof cancelAnimationFrame;
  mocks.state.responses.length = 0;
  // jsdom lays nothing out, so the pointer would always land outside the canvas.
  Element.prototype.getBoundingClientRect = () => ({
    x: 0, y: 0, left: 0, top: 0, right: COLS * 8, bottom: ROWS * 16,
    width: COLS * 8, height: ROWS * 16, toJSON: () => ({}),
  }) as DOMRect;
});

async function mountTerminal() {
  let ready: GhosttyTerminalHandle | null = null;
  const onInput = vi.fn();
  const view = render(
    <GhosttyTerminal
      fontSize={14}
      debugName="input-source-test"
      onInput={onInput}
      onReady={(terminal) => { ready = terminal; }}
      onResize={vi.fn()}
    />,
  );
  await waitFor(() => expect(ready).not.toBeNull());
  const surface = view.container.querySelector('.terminal-container');
  if (!surface) throw new Error('terminal surface never rendered');
  return { handle: ready as unknown as GhosttyTerminalHandle, surface, onInput };
}

describe('GhosttyTerminal input source', () => {
  it('tags a mouse report as pointer input', async () => {
    const { surface, onInput } = await mountTerminal();

    await act(async () => {
      fireEvent.mouseDown(surface, { button: 0, buttons: 1, clientX: 24, clientY: 32 });
    });

    expect(onInput).toHaveBeenCalledWith('\x1b[<0;4;3M', 'pointer');
  });

  it('tags a forwarded terminal response as response input', async () => {
    const { handle, onInput } = await mountTerminal();
    mocks.state.responses.push('\x1b[0n');

    await act(async () => {
      await handle.write('anything');
    });

    expect(onInput).toHaveBeenCalledWith('\x1b[0n', 'response');
  });

  it('still tags a keystroke as user input', async () => {
    const { surface, onInput } = await mountTerminal();

    await act(async () => {
      fireEvent.keyDown(surface, { key: 'a', code: 'KeyA' });
    });

    expect(onInput).toHaveBeenCalledWith('a', 'user');
  });
});

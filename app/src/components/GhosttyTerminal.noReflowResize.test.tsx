// A client that re-wrapped its history would hold a different frame from the worker whose
// rows every placement is numbered in. Fails if the branch goes back to `terminal.resize`.
import { act, render, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

type ModelOp = { kind: 'write'; data: string } | { kind: 'resize'; cols: number; rows: number };

const mocks = vi.hoisted(() => {
  const control = { wraparound: true, fit: { cols: 80, rows: 24 } };
  const terminals: Array<{ ops: ModelOp[] }> = [];

  const createTerminal = () => {
    const decoder = new TextDecoder();
    const terminal = {
      cols: 80,
      rows: 24,
      ops: [] as ModelOp[],
      write(data: string | Uint8Array) {
        terminal.ops.push({
          kind: 'write',
          data: typeof data === 'string' ? data : decoder.decode(data),
        });
      },
      resize(cols: number, rows: number) {
        terminal.cols = cols;
        terminal.rows = rows;
        terminal.ops.push({ kind: 'resize', cols, rows });
      },
      // DEC mode 7 (autowrap).
      getMode: (mode: number) => (mode === 7 ? control.wraparound : false),
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
    };
    terminals.push(terminal);
    return terminal;
  };

  class MockRenderer {
    readonly cellWidth = 8;
    readonly cellHeight = 16;
    readonly dpr = 1;

    fitDimensions() {
      return control.fit;
    }

    resize() {}
    render() {
      return { quads: 0, cellsArrayLen: 0, printableSkippedNull: 0, printableSkippedZeroWidth: 0 };
    }
    invalidateGlyphCache() {}
    setFontSize() {}
    dispose() {}
  }

  return { MockRenderer, control, createTerminal, terminals };
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

beforeEach(() => {
  mocks.control.fit = { cols: 80, rows: 24 };
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
});

async function mountTerminal(): Promise<{
  handle: GhosttyTerminalHandle;
  model: { ops: ModelOp[] };
  onResize: ReturnType<typeof vi.fn>;
}> {
  mocks.terminals.length = 0;
  let ready: GhosttyTerminalHandle | null = null;
  const onResize = vi.fn();
  render(
    <GhosttyTerminal
      fontSize={14}
      debugName="no-reflow-resize-test"
      onInput={vi.fn()}
      onReady={(terminal) => { ready = terminal; }}
      onResize={onResize}
    />,
  );
  await waitFor(() => expect(ready).not.toBeNull());
  const model = mocks.terminals[0];
  model.ops.length = 0;
  return { handle: ready as unknown as GhosttyTerminalHandle, model, onResize };
}

const noReflowRecipe = (cols: number, rows: number): ModelOp[] => [
  { kind: 'write', data: '\x1b[?7l' },
  { kind: 'resize', cols, rows },
  { kind: 'write', data: '\x1b[?7h' },
];

describe('GhosttyTerminal no-reflow resize', () => {
  it('keeps the model at the old width until the daemon streams the resize', async () => {
    mocks.control.fit = { cols: 100, rows: 30 };
    const { handle, model, onResize } = await mountTerminal();

    act(() => handle.fit());

    expect(model.ops).toEqual([]);
    expect(onResize).toHaveBeenCalledWith(100, 30, {
      reason: 'ghostty_fit',
      xpixel: 800,
      ypixel: 480,
    });

    await act(async () => {
      await handle.resizeLocal(100, 30);
    });
    expect(model.ops).toEqual(noReflowRecipe(100, 30));
  });

  it('drives the daemon resize echo through the mode-7 recipe', async () => {
    mocks.control.wraparound = true;
    const { handle, model } = await mountTerminal();

    await act(async () => {
      await handle.resizeLocal(100, 30);
    });

    expect(model.ops).toEqual(noReflowRecipe(100, 30));
  });

  it('applies a streamed resize between the adjacent byte chunks', async () => {
    mocks.control.wraparound = true;
    const { handle, model } = await mountTerminal();

    await act(async () => {
      await Promise.all([
        handle.write(new TextEncoder().encode('before')),
        handle.resizeLocal(100, 30),
        handle.write(new TextEncoder().encode('after')),
      ]);
    });

    expect(model.ops).toEqual([
      { kind: 'write', data: 'before' },
      { kind: 'write', data: '\x1b[?2027h' },
      ...noReflowRecipe(100, 30),
      { kind: 'write', data: 'after' },
      { kind: 'write', data: '\x1b[?2027h' },
    ]);
  });

  it('resizes plainly when the program already turned wraparound off', async () => {
    // Writing the mode back on would enable wrapping the program disabled.
    mocks.control.wraparound = false;
    const { handle, model } = await mountTerminal();

    await act(async () => {
      await handle.resizeLocal(100, 30);
    });

    expect(model.ops).toEqual([{ kind: 'resize', cols: 100, rows: 30 }]);
  });

  it('keeps the restore resize on the same path', async () => {
    mocks.control.wraparound = true;
    const { handle, model } = await mountTerminal();

    await act(async () => {
      await handle.resizeLocal(90, 20, { restore: true });
    });

    expect(model.ops).toEqual(noReflowRecipe(90, 20));
  });
});

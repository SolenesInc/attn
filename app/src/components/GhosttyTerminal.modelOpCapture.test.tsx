import { act, render, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { base64ToBytes, type ModelFaultCapture } from '../utils/ghosttyModelOpRing';

const mocks = vi.hoisted(() => {
  const control = { failNextRender: false, fitCols: 80 };
  const noteModelFaultCalls: Array<[string, { capture?: unknown; operation: string }]> = [];
  const terminals: Array<{ writes: string[] }> = [];

  const createTerminal = () => {
    const decoder = new TextDecoder();
    const terminal = {
      cols: 80,
      rows: 24,
      writes: [] as string[],
      write(data: string | Uint8Array) {
        terminal.writes.push(typeof data === 'string' ? data : decoder.decode(data));
      },
      resize(cols: number, rows: number) {
        terminal.cols = cols;
        terminal.rows = rows;
      },
      getMode: () => true,
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
      adoptSnapshot: () => ({ rows: 0, next: () => null, close: () => undefined }),
    };
    terminals.push(terminal);
    return terminal;
  };

  const renderers: MockRenderer[] = [];
  class MockRenderer {
    readonly id = renderers.length;
    readonly cellWidth = 8;
    readonly cellHeight = 16;

    constructor() {
      renderers.push(this);
    }

    fitDimensions() {
      return { cols: control.fitCols, rows: 24 };
    }

    resize() {}
    render() {
      if (control.failNextRender) {
        control.failNextRender = false;
        throw new Error('Out of bounds memory access');
      }
      return { quads: 0, cellsArrayLen: 0, printableSkippedNull: 0, printableSkippedZeroWidth: 0 };
    }
    invalidateGlyphCache() {}
    setFontSize() {}
    dispose() {}
  }

  return {
    MockRenderer,
    control,
    createTerminal,
    noteModelFault: (...args: unknown[]) => {
      noteModelFaultCalls.push(args as [string, { capture?: unknown; operation: string }]);
    },
    noteModelFaultCalls,
    renderers,
    terminals,
  };
});

vi.mock('../ghostty/wasm', () => ({ loadGhostty: async () => ({ createTerminal: mocks.createTerminal }) }));
vi.mock('./GhosttyWebGlRenderer', () => ({ WebGlTerminalRenderer: mocks.MockRenderer }));
vi.mock('../utils/terminalIconFont', () => ({
  ensureTerminalIconFont: () => new Promise<void>(() => undefined),
}));
vi.mock('../utils/terminalDiagnosticsLog', () => ({
  TERMINAL_DIAGNOSTICS_FILE: 'terminal-diagnostics.jsonl',
  disposePaneDiagnostics: () => undefined,
  noteModelFault: mocks.noteModelFault,
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

const decoder = new TextDecoder();

function writeText(capture: ModelFaultCapture, index: number): string {
  const op = capture.ops[index];
  if (op.kind !== 'write') throw new Error(`op ${index} is ${op.kind}, not a write`);
  return decoder.decode(base64ToBytes(op.b64));
}

describe('GhosttyTerminal model-op capture', () => {
  it('attaches the inputs the faulted model was fed to the model_fault record', async () => {
    const originalResizeObserver = globalThis.ResizeObserver;
    const resizeCallbacks: ResizeObserverCallback[] = [];
    globalThis.ResizeObserver = class {
      constructor(callback: ResizeObserverCallback) {
        resizeCallbacks.push(callback);
      }

      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver;

    let handle: GhosttyTerminalHandle | null = null;
    try {
      render(
        <GhosttyTerminal
          fontSize={14}
          debugName="capture-test"
          onInput={vi.fn()}
          onReady={(terminal) => { handle = terminal; }}
          onResize={vi.fn()}
        />,
      );
      await waitFor(() => expect(handle).not.toBeNull());
      const terminal = handle as unknown as GhosttyTerminalHandle;

      await act(async () => {
        await terminal.restoreSnapshot(new Uint8Array([0x64, 0x75, 0x6d, 0x70]));
        await terminal.write('live output');
        terminal.reset();
        await terminal.drain();
      });
      mocks.control.fitCols = 81;
      await act(async () => {
        resizeCallbacks[0]([], {} as ResizeObserver);
        await terminal.drain();
      });
      await act(async () => {
        await terminal.write('after resize');
      });

      mocks.control.failNextRender = true;
      await act(async () => {
        await terminal.write('trapping write');
      });

      await waitFor(() => expect(mocks.noteModelFaultCalls.length).toBeGreaterThan(0));
      const capture = mocks.noteModelFaultCalls[0][1].capture as ModelFaultCapture;
      expect(capture).toBeDefined();

      expect(capture.snapshot).toMatchObject({ len: 4, truncated: false });
      expect(decoder.decode(base64ToBytes(capture.snapshot!.b64))).toBe('dump');
      expect(capture.snapshotTruncated).toBe(false);

      expect(capture.ops.map((op) => op.kind)).toEqual([
        'write',
        'reset',
        'write',
        'resize',
        'write',
        'write',
      ]);
      expect(writeText(capture, 0)).toBe('live output');
      expect(writeText(capture, 2)).toBe('\x1bc');
      expect(capture.ops[3]).toMatchObject({ kind: 'resize', cols: 81, rows: 24, noReflow: true });
      expect(writeText(capture, 4)).toBe('after resize');
      expect(writeText(capture, 5)).toBe('trapping write');
      expect(capture.droppedOps).toBe(0);
      expect(capture.droppedForRecordBudget).toBe(0);

      expect(mocks.terminals[0].writes).toContain('\x1b[?7l');
      const capturedWrites = capture.ops
        .map((op, index) => (op.kind === 'write' ? writeText(capture, index) : ''))
        .join('');
      expect(capturedWrites).not.toContain('[?7l');
    } finally {
      globalThis.ResizeObserver = originalResizeObserver;
    }
  });
});

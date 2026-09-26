import { describe, expect, it } from 'vitest';
import type { Osc133Marker } from './terminalOsc133';
import {
  blockViewportSpanAnchored,
  extractBlock,
  reanchorDelta,
  RESIZE_REANCHOR_SCAN_ROWS,
  TerminalBlockStore,
  type BlockRowAccess,
  type BlockViewportSpan,
  type SeededBlock,
} from './terminalBlocks';

type Recorded = [command: string, promptRow: number, outputStartRow: number, endRow: number];

function access(rows: string[]): BlockRowAccess {
  return { totalRows: () => rows.length, rowText: (row) => rows[row] ?? '' };
}

function record(rows: string[], blocks: Recorded[]): TerminalBlockStore {
  const store = new TerminalBlockStore();
  const rowText = (row: number) => rows[row] ?? '';
  for (const [command, promptRow, outputStartRow, endRow] of blocks) {
    store.applyMarker({ kind: 'prompt-start' }, { row: promptRow, col: 0 }, rowText);
    store.applyMarker({ kind: 'input-start' }, { row: promptRow, col: 0 }, rowText);
    store.applyMarker({ kind: 'pre-exec', cmdline: command }, { row: outputStartRow, col: 0 }, rowText);
    store.applyMarker({ kind: 'command-end', exitCode: 0 }, { row: endRow, col: 0 }, rowText);
  }
  return store;
}

function lines(count: number, prefix: string): string[] {
  return Array.from({ length: count }, (_, i) => `${prefix}-${String(i).padStart(4, '0')}`);
}

const ECHO = ['prompt> echo hello', 'hello', 'world  ', '', 'prompt> '];
const ECHO_BLOCK: Recorded = ['echo hello', 0, 1, 3];
const LONG_PROMPT = ['prompt> seq 1 200; echo RESIZE_TOKEN_LONG_ENOUGH_TO_EXCEED_NARROW_WIDTH', 'output line'];
const TWO = ['prompt> a', 'out-a', 'prompt> b', 'out-b'];
const TWO_BLOCKS: Recorded[] = [['a', 0, 1, 2], ['b', 2, 3, 4]];

describe('finding a recorded block in the live buffer', () => {
  it.each<[string, string[], Recorded, string[], number | undefined, number | null]>([
    ['the buffer is unchanged', ECHO, ECHO_BLOCK, ECHO, undefined, 0],
    ['output was added above it', ECHO, ECHO_BLOCK, ['noise-a', 'noise-b', ...ECHO], undefined, 2],
    ['rows were trimmed above it', ['older', ...ECHO], ['echo hello', 1, 2, 4], ECHO, undefined, -1],
    ['its text is gone', ECHO, ECHO_BLOCK, lines(200, 'unrelated'), undefined, null],
    ['it moved past the scan window', ['prompt> make', 'building'], ['make', 0, 1, 2], [...lines(200, 'older'), 'prompt> make', 'building'], undefined, null],
    ['it moved within the resize scan window', ['prompt> make', 'building'], ['make', 0, 1, 2], [...lines(200, 'older'), 'prompt> make', 'building'], RESIZE_REANCHOR_SCAN_ROWS, 200],
    ['a narrower pane clips its row', LONG_PROMPT, ['seq', 0, 1, 2], LONG_PROMPT.map((line) => line.slice(0, 30)), undefined, 0],
    ['a tiny pane leaves too little text to tell', ['prompt> unique-command-here', 'out'], ['unique', 0, 1, 2], ['pro', 'out'], undefined, null],
  ])('when %s', (_name, recorded, block, now, window, delta) => {
    const [recordedBlock] = record(recorded, [block]).blocks();

    expect(reanchorDelta(recordedBlock, access(now), window)).toBe(delta);
  });
});

describe('extractBlock', () => {
  it.each<[string, string[], string[], { command: string; output: string } | null]>([
    ['takes the command and its output without trailing blanks', ECHO, ECHO, { command: 'echo hello', output: 'hello\nworld' }],
    ['follows the block when the buffer shifted', ECHO, ['noise-a', 'noise-b', ...ECHO], { command: 'echo hello', output: 'hello\nworld' }],
    ['refuses when the block’s text is gone', ECHO, lines(200, 'unrelated'), null],
  ])('%s', (_name, recorded, now, extracted) => {
    const [block] = record(recorded, [ECHO_BLOCK]).blocks();

    expect(extractBlock(block, access(now))).toEqual(extracted);
  });
});

describe('TerminalBlockStore.reanchorOnResize', () => {
  it.each<[string, string[], Recorded[], string[], 'ok' | 'all-stale', Recorded[]]>([
    ['keeps blocks in place when rows did not move', TWO, TWO_BLOCKS, TWO, 'ok', TWO_BLOCKS],
    ['shifts every block by a height-only change', TWO, TWO_BLOCKS, ['x', 'y', ...TWO], 'ok', [['a', 2, 3, 4], ['b', 4, 5, 6]]],
    ['drops a block whose text is gone and keeps the survivor', ['prompt> keep', 'out-keep', 'prompt> gone', 'out-gone'], [['keep', 0, 1, 2], ['gone', 2, 3, 4]], ['prompt> keep', 'out-keep'], 'ok', [['keep', 0, 1, 2]]],
    ['reports all-stale when every block is gone', TWO, TWO_BLOCKS, lines(50, 'unrelated'), 'all-stale', []],
    ['remaps a large height shift instead of dropping', ['prompt> tall', 'out'], [['tall', 0, 1, 2]], [...lines(200, 's'), 'prompt> tall', 'out'], 'ok', [['tall', 200, 201, 202]]],
    ['matches rows clipped by a narrower pane', LONG_PROMPT, [['seq', 0, 1, 2]], LONG_PROMPT.map((line) => line.slice(0, 30)), 'ok', [['seq', 0, 1, 2]]],
    ['refuses a tiny overlap that would match almost anything', ['prompt> unique-command-here', 'out'], [['unique', 0, 1, 2]], ['pro', 'out'], 'all-stale', []],
  ])('%s', (_name, recorded, blocks, now, result, after) => {
    const store = record(recorded, blocks);

    expect(store.reanchorOnResize(access(now))).toBe(result);
    expect(store.blocks().map((b) => [b.command, b.promptRow, b.outputStartRow, b.endRow])).toEqual(after);
    expect(store.blocks().map((b) => b.anchorRow)).toEqual(after.map(([, promptRow]) => promptRow));
  });
});

describe('TerminalBlockStore.blockAtAnchored', () => {
  it.each<[string, string[], string[], number, string | null]>([
    ['the prompt row of a block', ECHO, ECHO, 0, 'echo hello'],
    ['an output row of a block', ECHO, ECHO, 2, 'echo hello'],
    ['the row after a block ends', ECHO, ECHO, 3, null],
    ['the prompt row after a shift', ECHO, ['x', 'y', ...ECHO], 2, 'echo hello'],
    ['a row above a shifted block', ECHO, ['x', 'y', ...ECHO], 0, null],
    ['any row once the block’s text is gone', ECHO, lines(50, 'unrelated'), 1, null],
  ])('finds the block at %s', (_name, recorded, now, row, command) => {
    const store = record(recorded, [ECHO_BLOCK]);

    expect(store.blockAtAnchored(row, access(now))?.command ?? null).toBe(command);
  });
});

describe('blockViewportSpanAnchored', () => {
  const BUFFER = lines(400, 'row');

  it.each<[string, string[], Recorded, string[], number, number, BlockViewportSpan | null]>([
    ['a fully visible block', BUFFER, ['x', 3, 4, 8], BUFFER, 0, 24, { startRow: 3, endRow: 7, visible: true, spansViewport: false }],
    ['a tall block whose end is in view', BUFFER, ['make', 3, 4, 207], BUFFER, 183, 27, { startRow: -180, endRow: 23, visible: true, spansViewport: false }],
    ['a tall block covering the viewport', BUFFER, ['make', 3, 4, 207], BUFFER, 100, 27, { startRow: -97, endRow: 106, visible: true, spansViewport: true }],
    ['a block scrolled out of view', BUFFER, ['x', 3, 4, 8], BUFFER, 50, 24, { startRow: -47, endRow: -43, visible: false, spansViewport: false }],
    ['a block after the buffer shifted', ['prompt> a', 'out-a'], ['a', 0, 1, 2], ['x', 'y', 'prompt> a', 'out-a'], 2, 24, { startRow: 0, endRow: 1, visible: true, spansViewport: false }],
    ['a block whose text a reflow replaced, rather than a wrong box', ['prompt> make', 'building'], ['make', 0, 3, 720], lines(394, 'reflowed'), 341, 53, null],
  ])('%s', (_name, recorded, block, now, firstViewportRow, viewportRows, span) => {
    const [recordedBlock] = record(recorded, [block]).blocks();

    expect(blockViewportSpanAnchored(recordedBlock, access(now), firstViewportRow, viewportRows)).toEqual(span);
  });
});

describe('TerminalBlockStore.seed', () => {
  const RESTORED = ['prompt> make test', 'building', 'ok', '', 'prompt> ls', 'a  b'];
  const rowText = (row: number) => RESTORED[row] ?? '';
  const MAKE: SeededBlock = { id: 5, pending: false, promptRow: 0, inputRow: 0, inputCol: 8, outputStartRow: 1, endRow: 3, command: 'make test', exitCode: 0 };

  const END: Osc133Marker = { kind: 'command-end', exitCode: 0 };

  it.each<[string, Recorded[], SeededBlock[], [Osc133Marker, number][], object[]]>([
    ['lands a completed block where the restored buffer holds it', [], [MAKE], [], [
      { id: 5, command: 'make test', promptRow: 0, outputStartRow: 1, endRow: 3, exitCode: 0, anchorText: 'prompt> make test', inputStart: { row: 0, col: 8 } },
    ]],
    ['re-arms a pending block for the next live command-end', [], [{ id: 9, pending: true, promptRow: 4, inputRow: 4, inputCol: 8, outputStartRow: 5, command: 'ls' }], [[END, 6]], [
      { id: 9, command: 'ls', promptRow: 4, outputStartRow: 5, endRow: 6 },
    ]],
    ['numbers live blocks above every seeded id', [], [
      { id: 3, pending: false, promptRow: 0, inputRow: 0, outputStartRow: 1, endRow: 2, command: 'a', exitCode: 0 },
      { id: 7, pending: false, promptRow: 2, inputRow: 2, outputStartRow: 3, endRow: 4, command: 'b', exitCode: 0 },
    ], [[{ kind: 'prompt-start' }, 4], [{ kind: 'pre-exec', cmdline: 'c' }, 5], [END, 6]], [{ id: 3 }, { id: 7 }, { id: 8, command: 'c' }]],
    ['replaces what the store held, since a restore is authoritative', [ECHO_BLOCK], [{ ...MAKE, id: 2, command: 'seeded' }], [], [{ id: 2, command: 'seeded' }]],
    ['drops a completed block without output rows', [], [{ id: 1, pending: false, promptRow: 0, inputRow: 0, command: 'no-output' }], [], []],
  ])('%s', (_name, before, seeded, live, after) => {
    const store = record(ECHO, before);
    store.seed(seeded, rowText);
    for (const [marker, row] of live) store.applyMarker(marker, { row, col: 0 }, rowText);

    expect(store.blocks()).toMatchObject(after);
  });

  it('extracts a seeded block’s output from the restored buffer', () => {
    const store = new TerminalBlockStore();
    store.seed([MAKE], rowText);

    expect(extractBlock(store.blocks()[0], access(RESTORED))).toEqual({ command: 'make test', output: 'building\nok' });
  });
});

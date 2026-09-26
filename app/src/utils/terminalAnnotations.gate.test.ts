import { describe, expect, it } from 'vitest';
import { TerminalAnnotationStore, type MessageRowAccess } from './terminalAnnotations';

function render(markdown: string, cols: number, marker = '⏺ '): string[] {
  const indent = ' '.repeat(marker.length);
  const rows: string[] = [];
  for (const paragraph of markdown.split('\n')) {
    let line = rows.length === 0 ? marker : indent;
    let empty = true;
    for (const word of paragraph.split(/\s+/).filter(Boolean)) {
      if (!empty && line.length + 1 + word.length > cols) {
        rows.push(line);
        line = indent;
        empty = true;
      }
      line += empty ? word : ` ${word}`;
      empty = false;
    }
    rows.push(line);
  }
  return rows;
}

const MESSAGE = 'A soft line wrap moves overflowing text onto the next visual line '
  + 'without inserting a real line break. The wrap can shift when the available '
  + 'width changes.';
const ANCHOR = 'visual line without inserting a real';

class FakeGrid implements MessageRowAccess {
  rows: string[];
  colCount: number;

  constructor(rows: string[], cols = 62) {
    this.rows = rows;
    this.colCount = cols;
  }

  cols(): number {
    return this.colCount;
  }

  totalRows(): number {
    return this.rows.length;
  }

  rowText(bufferRow: number): string {
    return this.rows[bufferRow] ?? '';
  }

  rowTextRange(bufferRow: number, startCol: number, endCol: number): string {
    return (this.rows[bufferRow] ?? '').slice(startCol, endCol);
  }
}

const CHROME = '› Use /skills to list available skills';
const AT_62 = render(MESSAGE, 62);
const PADDING = Array.from({ length: 400 }, (_, i) => `build output line ${i}`);
const screen = (rows: string[], cols = 62) => ({ rows, cols });

function markedStore(layers: number) {
  const store = new TerminalAnnotationStore();
  store.setMessages([{ key: 'turn-1', markdown: MESSAGE }]);
  const start = MESSAGE.indexOf(ANCHOR);
  const ids = Array.from({ length: layers }, (_, layer) => store.add('turn-1', start, start + ANCHOR.length, 'clarify-this', `layer ${layer}`)!.id);
  return { store, top: ids[ids.length - 1] };
}

describe('projecting a mark onto the terminal', () => {
  it.each([
    { shown: 'the rows showing the text', screens: [screen(AT_62)], layers: 1, painted: 'all' },
    { shown: 'the rows after a width reflow', screens: [screen(AT_62), screen(render(MESSAGE, 34), 34)], layers: 1, painted: 'all' },
    { shown: 'rows scrolled deep into the buffer', screens: [screen([...PADDING, ...AT_62, ...PADDING])], layers: 1, painted: 'all' },
    { shown: 'rows the viewport clipped short', screens: [screen(AT_62.slice(1))], layers: 1, painted: 'part' },
    { shown: 'rows the TUI has since overwritten', screens: [screen(AT_62), screen(AT_62.map(() => CHROME))], layers: 1, painted: 'none' },
    { shown: 'two marks on the same text', screens: [screen(AT_62)], layers: 2, painted: 'all' },
  ] as const)('paints $painted of the text on $shown, and resolves a click to the top mark', ({ screens, layers, painted }) => {
    const { store, top } = markedStore(layers);
    const grid = new FakeGrid([...screens[0].rows], screens[0].cols);
    let washes = store.project(grid);
    const firstCell = washes[washes.length - 1]?.rows[0];
    for (const next of screens.slice(1)) {
      if (next.cols !== grid.colCount) store.noteGeometryChange();
      grid.rows = [...next.rows];
      grid.colCount = next.cols;
      washes = store.project(grid);
    }

    if (painted === 'none') {
      expect(washes).toEqual([]);
      expect(store.annotationAt(grid, firstCell.row, firstCell.startCol)).toBeNull();
      return;
    }
    const wash = washes[washes.length - 1];
    const text = wash.rows.map((range) => grid.rowTextRange(range.row, range.startCol, range.endCol)).join(' ').replace(/\s+/g, ' ').trim();
    if (painted === 'all') expect(text).toBe(ANCHOR);
    else expect(ANCHOR).toContain(text);
    const [cell] = wash.rows;
    expect([
      store.annotationAt(grid, cell.row, cell.startCol),
      store.annotationAt(grid, cell.row, cell.endCol - 1),
      store.annotationAt(grid, cell.row, cell.startCol - 1),
      store.annotationAt(grid, cell.row, cell.endCol),
    ]).toEqual([top, top, null, null]);
  });
});

const OLDER = 'The older answer mentions a retry wrapper around the call.';
const LINKED = 'Evidence: [source](/Users/tester/src/services-pilot/a/b/Source.java:19). The conclusion after the source remains annotatable.';
const VISUAL = AT_62.findIndex((row) => row.includes('visual'));

describe('anchoring a drag over the terminal', () => {
  it.each([
    {
      drag: 'agent prose across two rows',
      messages: [MESSAGE],
      rows: AT_62,
      from: [VISUAL, 'visual'], to: [VISUAL + 1, 'real'],
      anchor: { messageKey: 'turn-1', quote: ANCHOR },
    },
    {
      drag: 'the TUI’s own chrome',
      messages: [MESSAGE],
      rows: [CHROME, ...AT_62],
      from: [0, '›'], to: [0, 'available skills'],
      anchor: null,
    },
    {
      drag: 'an older turn above the latest',
      messages: [OLDER, MESSAGE],
      rows: [...render(OLDER, 62), '', ...AT_62],
      from: [0, 'retry'], to: [0, 'retry wrapper'],
      anchor: { messageKey: 'turn-1', quote: 'retry wrapper' },
    },
    {
      drag: 'prose after a link rendered as a shortened path',
      messages: [LINKED],
      rows: ['• Evidence: a/b/Source.java:19.', '  The conclusion after the source remains annotatable.'],
      from: [1, 'conclusion'], to: [1, 'conclusion after the source'],
      anchor: { messageKey: 'turn-1', quote: 'conclusion after the source' },
    },
    {
      drag: 'the user’s prompt echoing prose after a shortened path',
      messages: [LINKED],
      rows: ['• Evidence: a/b/Source.java:19.', '› conclusion after the source please'],
      from: [1, 'conclusion'], to: [1, 'conclusion after the source'],
      anchor: null,
    },
  ] as const)('anchors a drag over $drag', ({ messages, rows, from, to, anchor }) => {
    const turns = messages.map((markdown, index) => ({ key: `turn-${index + 1}`, markdown }));
    const store = new TerminalAnnotationStore();
    store.setMessages(turns);

    const result = store.anchorForSelection(new FakeGrid([...rows]), {
      startRow: from[0],
      startCol: rows[from[0]].indexOf(from[1]),
      endRow: to[0],
      endCol: rows[to[0]].indexOf(to[1]) + to[1].length,
    });

    expect(result && { messageKey: result.messageKey, quote: result.quote }).toEqual(anchor);
    if (result) expect(turns.find((turn) => turn.key === result.messageKey)!.markdown.slice(result.start, result.end)).toBe(result.quote);
  });
});

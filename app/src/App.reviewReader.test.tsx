import { act, fireEvent, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openMarkdownTiles } from './test/appFixtures';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import type { ReviewBundle, ReviewRow } from './types/generated';

const DOC = '/notes/review.md';
const REPO = '/repo';

const DOCUMENT = `---
attn: review
frame: brief
repository: ${REPO}
base: 4be1c0d
head: 9f3a7e2
---

# Name the sum

Add used to return its sum directly.

## Add keeps its result

### calc.go:3-6

- L4!: The sum gets a name.
- L5: Then it is returned.

A paragraph between the stops.

### util.go:1-2

- L2: The helper.

## Not shown

- go.sum: checksums only.
`;

const row = (kind: string, old: number | undefined, now: number | undefined, text: string): ReviewRow => ({
  kind,
  ...(old !== undefined ? { old } : {}),
  ...(now !== undefined ? { now } : {}),
  text,
});

const CALC_ROWS = [
  row(' ', 3, 3, 'func Add(a, b int) int {'),
  row('-', 4, undefined, '\treturn a + b'),
  row('+', undefined, 4, '\tsum := a + b'),
  row('+', undefined, 5, '\treturn sum'),
  row(' ', 5, 6, '}'),
];

const CALC_FILE = [
  row(' ', 1, 1, 'package calc'),
  row(' ', 2, 2, ''),
  ...CALC_ROWS,
  row(' ', 6, 7, ''),
  row(' ', 7, 8, 'func Sub(a, b int) int {'),
  row('-', 8, undefined, '\treturn a - b'),
  row('+', undefined, 9, '\treturn b - a'),
  row(' ', 9, 10, '}'),
];

const BUNDLE: ReviewBundle = {
  frame: 'brief',
  repository: REPO,
  base: '4be1c0d',
  head: '9f3a7e2',
  stops: [
    {
      heading_line: 15, path: 'calc.go', from: 3, to: 6, now_from: 3, now_to: 6, changed_since_presented: false, file_hash: 'h1',
      rows: CALC_ROWS,
      notes: [
        { line: 17, from: 4, to: 4, now_from: 4, now_to: 4, look: true, text: 'The sum gets a name.' },
        { line: 18, from: 5, to: 5, now_from: 5, now_to: 5, look: false, text: 'Then it is returned.' },
      ],
    },
    {
      heading_line: 22, path: 'util.go', from: 1, to: 2, now_from: 1, now_to: 2, changed_since_presented: false, file_hash: 'h2',
      rows: [row(' ', 1, 1, 'package calc'), row('+', undefined, 2, 'func helper() {}')],
      notes: [{ line: 24, from: 2, to: 2, now_from: 2, now_to: 2, look: false, text: 'The helper.' }],
    },
  ],
  files: [
    { path: 'calc.go', status: 'modified', added: 3, removed: 2, binary: false },
    { path: 'util.go', status: 'modified', added: 1, removed: 0, binary: false },
    { path: 'go.sum', status: 'modified', added: 1, removed: 1, binary: false },
  ],
  flows: [],
  not_shown: [{ line: 28, path: 'go.sum', reason: 'checksums only.' }],
  navigation: [],
  problems: [],
};

const UTIL_FILE = [row(' ', 1, 1, 'package calc'), row('+', undefined, 2, 'func helper() {}')];

function serveRepository(daemon: ScriptedDaemon) {
  daemon.on('review_file', ({ path }) => {
    if (path === 'calc.go') return { event: 'review_file_result', success: true, path, rows: CALC_FILE, hash: 'h1' };
    if (path === 'util.go') return { event: 'review_file_result', success: true, path, rows: UTIL_FILE, hash: 'h2' };
    return { event: 'review_file_result', success: true, path, rows: [row(' ', 1, 1, `// ${path}`)], hash: 'h3' };
  });
  daemon.on('review_search', ({ query }) => ({
    event: 'review_search_result',
    success: true,
    files: query === 'calc' ? ['calc.go'] : [],
    hits: [{ path: 'calc.go', line: 9, text: '\treturn b - a' }],
  }));
}

async function openReview({ review = BUNDLE, content = DOCUMENT }: { review?: ReviewBundle | null; content?: string } = {}) {
  const view = await openMarkdownTiles('', { path: DOC, script: serveRepository });
  const desktopId = view.daemon.arrangement.profile.current_desktop_id;
  view.daemon.emit({ event: 'desktop_tile_content', desktop_id: desktopId, tile_id: 'tile-a', tile_kind: 'markdown', path: DOC, content, ...(review ? { review } : {}) });
  await view.daemon.idle();
  const tile = view.tile();
  const body = tile.querySelector<HTMLElement>('.desktop-dock-tile-body')!;
  body.focus();
  const press = async (key: string, init: KeyboardEventInit = {}) => {
    fireEvent.keyDown(tile.ownerDocument.activeElement ?? body, { key, ...init });
    await act(() => vi.advanceTimersByTimeAsync(0));
    await view.daemon.idle();
  };
  return { ...view, reader: within(tile), tileElement: tile, press };
}

const cursor = (scope: HTMLElement) => scope.querySelector<HTMLElement>('.review-doc .review-cursor');
const fileView = (scope: HTMLElement) => scope.querySelector<HTMLElement>('[data-review-file-view]');
const fileCursorText = (scope: HTMLElement) => fileView(scope)?.querySelector<HTMLElement>('.review-cursor')?.textContent;
const stopRows = (stop: Element) => Array.from(stop.querySelectorAll('.review-row')).map((element) => element.textContent);

describe('App review reader', () => {
  it('renders each stop in place of its heading and notes, with the change, line numbers and agent notes under their lines', async () => {
    const { tileElement, reader } = await openReview();

    const stops = Array.from(tileElement.querySelectorAll('.review-stop'));
    expect(stops.map((stop) => stop.getAttribute('data-path'))).toEqual(['calc.go', 'util.go']);
    expect(stopRows(stops[0])).toEqual(['33func Add(a, b int) int {', '4−\treturn a + b', '4+\tsum := a + b', '5+\treturn sum', '56}']);
    expect(reader.queryByRole('heading', { name: 'calc.go:3-6' })).toBeNull();
    expect(reader.queryByText('- L4!: The sum gets a name.')).toBeNull();

    const notes = Array.from(stops[0].querySelectorAll('.review-rows > *')).map((element) => (
      element.classList.contains('review-note') ? `note ${element.textContent}` : `row ${element.getAttribute('data-line') ?? '-'}`
    ));
    expect(notes).toEqual(['row 3', 'row -', 'row 4', 'note look hereL4The sum gets a name.', 'row 5', 'note agentL5Then it is returned.', 'row 6']);
    expect(stops[0].querySelector('.review-note--look')).toHaveTextContent('The sum gets a name.');

    expect(reader.getByText('A paragraph between the stops.')).toBeInTheDocument();
    expect(reader.getByRole('heading', { name: 'Add keeps its result' })).toBeInTheDocument();
    const summary = reader.getByRole('region', { name: 'The change' });
    expect(within(summary).getAllByText(/calc\.go|util\.go|go\.sum/).map((element) => element.closest('.review-file-row')?.getAttribute('data-path'))).toEqual(['calc.go', 'util.go', 'go.sum']);
    expect(tileElement.querySelector('.review-not-shown')).toHaveTextContent('go.sumchecksums only.');
  });

  it('moves the cursor by block and line with up and down, and by stop with left and right', async () => {
    const { tileElement, press } = await openReview();

    await press('ArrowDown');
    expect(cursor(tileElement)).toHaveAttribute('data-path', 'calc.go');
    expect(cursor(tileElement)?.closest('.review-summary')).not.toBeNull();

    await press('ArrowRight');
    expect(cursor(tileElement)?.closest('.review-stop')).toHaveAttribute('data-path', 'calc.go');
    expect(cursor(tileElement)).toHaveAttribute('data-line', '3');
    await press('ArrowDown');
    await press('ArrowDown');
    expect(cursor(tileElement)).toHaveAttribute('data-line', '4');

    await press('ArrowRight');
    expect(cursor(tileElement)?.closest('.review-stop')).toHaveAttribute('data-path', 'util.go');
    await press('ArrowUp');
    expect(cursor(tileElement)).toHaveTextContent('A paragraph between the stops.');
    await press('ArrowLeft');
    expect(cursor(tileElement)?.closest('.review-stop')).toHaveAttribute('data-path', 'calc.go');
  });

  it('keeps the cursor on its row when the document changes above it', async () => {
    const { tileElement, press, daemon } = await openReview();
    await press('ArrowRight');
    await press('ArrowRight');
    await press('ArrowDown');
    expect(cursor(tileElement)?.closest('.review-stop')).toHaveAttribute('data-path', 'util.go');
    expect(cursor(tileElement)).toHaveAttribute('data-line', '2');

    const shift = (line: number) => line + 2;
    const edited: ReviewBundle = {
      ...BUNDLE,
      stops: BUNDLE.stops.map((stop) => ({ ...stop, heading_line: shift(stop.heading_line), notes: stop.notes.map((note) => ({ ...note, line: shift(note.line) })) })),
      not_shown: BUNDLE.not_shown.map((entry) => ({ ...entry, line: shift(entry.line) })),
    };
    const content = DOCUMENT.replace('Add used to return its sum directly.\n', 'Add used to return its sum directly.\n\nAn agent added this paragraph.\n');
    act(() => daemon.emit({ event: 'desktop_tile_content', desktop_id: daemon.arrangement.profile.current_desktop_id, tile_id: 'tile-a', tile_kind: 'markdown', path: DOC, content, review: edited }));
    await daemon.idle();
    expect(tileElement).toHaveTextContent('An agent added this paragraph.');
    expect(cursor(tileElement)?.closest('.review-stop')).toHaveAttribute('data-path', 'util.go');
    expect(cursor(tileElement)).toHaveAttribute('data-line', '2');

    await press('ArrowUp');
    expect(cursor(tileElement)).toHaveAttribute('data-line', '1');
  });

  it('opens the whole file at the cursor, moves between its changes, goes back and closes', async () => {
    const { tileElement, daemon, press } = await openReview();
    await press('ArrowRight');
    await press('ArrowDown');
    await press('ArrowDown');

    await press('Enter');
    expect(daemon.sentOf('review_file').map(({ document, path, host }) => [document, path, host])).toEqual([[DOC, 'calc.go', undefined]]);
    const view = fileView(tileElement)!;
    expect(view).toHaveTextContent('calc.go');
    expect(fileCursorText(tileElement)).toBe('4+\tsum := a + b');
    expect(view.querySelectorAll('.review-row--in-range').length).toBeGreaterThan(0);

    await press('ArrowRight');
    expect(fileCursorText(tileElement)).toBe('8−\treturn a - b');
    await press('ArrowLeft');
    expect(fileCursorText(tileElement)).toBe('4−\treturn a + b');

    await press('/');
    fireEvent.change(within(tileElement).getByRole('textbox', { name: 'Search' }), { target: { value: 'sub' } });
    await act(() => vi.advanceTimersByTimeAsync(0));
    await daemon.idle();
    fireEvent.keyDown(within(tileElement).getByRole('textbox', { name: 'Search' }), { key: 'Enter' });
    await daemon.idle();
    expect(fileCursorText(tileElement)).toBe('9+\treturn b - a');
    expect(fileView(tileElement)?.querySelector('button')).not.toBeDisabled();

    await press('o', { ctrlKey: true });
    expect(fileCursorText(tileElement)).toBe('4−\treturn a + b');
    await press('Escape');
    expect(fileView(tileElement)).toBeNull();
    expect(cursor(tileElement)).toHaveAttribute('data-line', '4');
  });

  it('leaves Escape to the rest of the app while focus is outside the review tile', async () => {
    const { tileElement, press } = await openReview();
    await press('ArrowRight');
    await press('Enter');
    expect(fileView(tileElement)).not.toBeNull();

    const outside = Array.from(tileElement.ownerDocument.querySelectorAll<HTMLElement>('button')).find((button) => !tileElement.contains(button) && !button.contains(tileElement))!;
    act(() => outside.focus());
    const escape = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
    act(() => { outside.dispatchEvent(escape); });
    expect(escape.defaultPrevented).toBe(false);
    expect(fileView(tileElement)).not.toBeNull();

    act(() => tileElement.querySelector<HTMLElement>('.desktop-dock-tile-body')!.focus());
    await press('Escape');
    expect(fileView(tileElement)).toBeNull();
  });

  it('searches the repository by name and text, and opens what it finds', async () => {
    const { tileElement, daemon, press, reader } = await openReview();

    await press('/');
    const input = reader.getByRole('textbox', { name: 'Search' });
    fireEvent.change(input, { target: { value: 'calc' } });
    await act(() => vi.advanceTimersByTimeAsync(0));
    await daemon.idle();
    expect(daemon.sentOf('review_search').map(({ document, query }) => [document, query])).toEqual([[DOC, 'calc']]);
    const options = reader.getAllByRole('option').map((option) => option.textContent);
    expect(options).toEqual(['calc.gofile', '\treturn b - acalc.go:9'.trim()]);

    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.keyDown(input, { key: 'Enter' });
    await daemon.idle();
    expect(reader.queryByRole('dialog', { name: 'Search the repository' })).toBeNull();
    expect(fileCursorText(tileElement)).toBe('9+\treturn b - a');
  });

  it('shows more lines around a stop with e, fills the tile with z, and opens a Not shown file', async () => {
    const { tileElement, daemon, press } = await openReview();
    await press('ArrowRight');

    await press('e');
    const calc = tileElement.querySelector('.review-stop[data-path="calc.go"]')!;
    expect(stopRows(calc)[0]).toBe('11package calc');
    expect(stopRows(calc).slice(-1)[0]).toBe('910}');

    await press('z');
    expect(fileView(tileElement)).toHaveClass('review-file-view--filled');
    await press('z');
    expect(fileView(tileElement)).not.toHaveClass('review-file-view--filled');
    await press('Escape');

    const goSum = tileElement.querySelector<HTMLElement>('.review-not-shown .review-file-row')!;
    fireEvent.click(goSum);
    await daemon.idle();
    expect(daemon.sentOf('review_file').slice(-1)[0]?.path).toBe('go.sum');
    expect(fileView(tileElement)).toHaveTextContent('// go.sum');
  });

  it('shows the keys in a key line and a sheet', async () => {
    const { reader, press } = await openReview();
    expect(reader.getByLabelText('Review keys')).toHaveTextContent('stop');
    await press('?');
    expect(reader.getByRole('dialog', { name: 'Review keys' })).toHaveTextContent('Fill the tile with the file');
    await press('Escape');
    expect(reader.queryByRole('dialog', { name: 'Review keys' })).toBeNull();
  });

  it('leaves a printable key to the comment while text is selected', async () => {
    const { tileElement, daemon, press } = await openReview();
    const paragraph = within(tileElement).getByText('A paragraph between the stops.');
    const range = document.createRange();
    range.selectNodeContents(paragraph);
    window.getSelection()!.removeAllRanges();
    window.getSelection()!.addRange(range);
    fireEvent.mouseUp(paragraph);
    await daemon.idle();

    await press('e');
    expect(document.querySelector('.md-annotation-popover textarea')).not.toBeNull();
    expect(daemon.sentOf('review_file')).toEqual([]);
  });

  it('renders a review document as plain markdown with no keys when the tile carries no review', async () => {
    const { tileElement, daemon, press, reader } = await openReview({ review: null });

    expect(tileElement.querySelector('.review-stop')).toBeNull();
    expect(reader.getByRole('heading', { name: 'calc.go:3-6' })).toBeInTheDocument();
    expect(reader.queryByLabelText('Review keys')).toBeNull();
    await press('ArrowRight');
    await press('Enter');
    await press('/');
    await press('2');
    await press('3');
    expect(daemon.sentOf('review_file')).toEqual([]);
    expect(reader.queryByRole('textbox', { name: 'Search' })).toBeNull();
    expect(tileElement.querySelector('[data-review-file-view], .review-map')).toBeNull();
  });
});

const MAP_DOCUMENT = `${DOCUMENT}
\`\`\`mermaid before
sequenceDiagram
  participant poll as Poll
  participant calc as Calc
  poll->>calc: Add(a, b)
  %% stop: calc.go:3-6
\`\`\`

\`\`\`mermaid after
sequenceDiagram
  participant poll as Poll
  participant calc as Calc
  participant util as Util
  poll->>calc: Add(a, b)
  %% stop: calc.go:4-5
  calc->>util: helper()
  %% stop: util.go:1-2
  util-->>calc: returns
\`\`\`

\`\`\`mermaid
sequenceDiagram
  loop every poll
    poll->>calc: Add(a, b)
  end
\`\`\`
`;

const participant = (id: string, label: string) => ({ id, label, actor: false });
const message = (line: number, from: string, to: string, text: string, stopLine?: number) => ({
  line, from, to, arrow: '->>', text, ...(stopLine !== undefined ? { stop_line: stopLine } : {}),
});

const MAP_BUNDLE: ReviewBundle = {
  ...BUNDLE,
  frame: 'map',
  flows: [
    { line: 31, side: 'before', drawn: true, participants: [participant('poll', 'Poll'), participant('calc', 'Calc')], messages: [message(35, 'poll', 'calc', 'Add(a, b)', 15)] },
    {
      line: 39, side: 'after', drawn: true,
      participants: [participant('poll', 'Poll'), participant('calc', 'Calc'), participant('util', 'Util')],
      messages: [message(44, 'poll', 'calc', 'Add(a, b)', 15), message(46, 'calc', 'util', 'helper()', 22), message(48, 'util', 'calc', 'returns')],
    },
    { line: 51, drawn: false, participants: [participant('poll', 'poll'), participant('calc', 'calc')], messages: [message(54, 'poll', 'calc', 'Add(a, b)')] },
  ],
};

const frameTabs = (scope: HTMLElement) => Array.from(scope.querySelectorAll('.review-frames [role="tab"]')).map((tab) => `${tab.textContent}${tab.getAttribute('aria-selected') === 'true' ? ' *' : ''}`);
const script = (scope: HTMLElement) => scope.querySelector<HTMLElement>('.review-frame--walkthrough > .review-doc');
const stageStop = (scope: HTMLElement) => script(scope)?.querySelector('.review-stop--current')?.getAttribute('data-path');
const diagrams = (scope: HTMLElement) => Array.from(scope.querySelectorAll('.review-map [data-review-flow]')).map((svg) => svg.getAttribute('data-review-flow'));
const currentMessage = (scope: HTMLElement) => scope.querySelector('.review-map [aria-current="true"][data-review-message]')?.getAttribute('data-review-message');
const mapDetail = (scope: HTMLElement) => scope.querySelector<HTMLElement>('.review-map-detail');
const mapCursor = (scope: HTMLElement) => mapDetail(scope)?.querySelector<HTMLElement>('.review-cursor');

describe('App review reader frames', () => {
  it('opens in the Walkthrough the document picks: the stops as a script beside the file of the current stop', async () => {
    const { tileElement, daemon, press } = await openReview({ review: { ...BUNDLE, frame: 'walkthrough' } });

    expect(frameTabs(tileElement)).toEqual(['1Brief', '2Walkthrough *']);
    expect(stageStop(tileElement)).toBe('calc.go');
    expect(script(tileElement)).toHaveTextContent('Add keeps its result');
    expect(script(tileElement)?.querySelector('.review-stop[data-path="calc.go"] .review-row')).toBeNull();
    expect(fileView(tileElement)).toHaveClass('review-file-view--stage');
    expect(fileCursorText(tileElement)).toBe('4+\tsum := a + b');
    expect(fileView(tileElement)?.querySelector('.review-file-view-row--look')?.textContent).toBe('4+\tsum := a + b');
    expect(script(tileElement)?.querySelector('.review-note-at')).toHaveTextContent('The sum gets a name.');

    await press('ArrowDown');
    expect(fileCursorText(tileElement)).toBe('5+\treturn sum');
    expect(script(tileElement)?.querySelector('.review-note-at')).toHaveTextContent('Then it is returned.');

    await press('ArrowRight');
    expect(stageStop(tileElement)).toBe('util.go');
    expect(fileCursorText(tileElement)).toBe('2+func helper() {}');
    await press('ArrowRight');
    expect(stageStop(tileElement)).toBe('util.go');
    await press('ArrowLeft');
    expect(stageStop(tileElement)).toBe('calc.go');
    expect(daemon.sentOf('review_file').map(({ path }) => path)).toEqual(['calc.go', 'util.go', 'calc.go']);

    await press('z');
    expect(tileElement.querySelector('.review-frame--script-hidden')).not.toBeNull();
    await press('z');
    expect(tileElement.querySelector('.review-frame--script-hidden')).toBeNull();

    await press('o', { ctrlKey: true });
    await press('Escape');
    expect(fileView(tileElement)).toHaveClass('review-file-view--stage');
    expect(stageStop(tileElement)).toBe('calc.go');
  });

  it('opens in the Map the document picks and walks the messages tied to stops, with the code of each beside the diagram', async () => {
    const { tileElement, press } = await openReview({ review: MAP_BUNDLE, content: MAP_DOCUMENT });

    expect(frameTabs(tileElement)).toEqual(['1Brief', '2Walkthrough', '3Map *']);
    expect(diagrams(tileElement)).toEqual(['39']);
    expect(currentMessage(tileElement)).toBe('44');
    expect(mapDetail(tileElement)).toHaveTextContent('Message 1 of 2 · after the change');
    expect(mapDetail(tileElement)?.querySelector('.review-stop')).toHaveAttribute('data-path', 'calc.go');
    expect(mapCursor(tileElement)).toHaveAttribute('data-line', '3');

    await press('ArrowDown');
    await press('ArrowDown');
    expect(mapCursor(tileElement)).toHaveAttribute('data-line', '4');

    await press('ArrowRight');
    expect(currentMessage(tileElement)).toBe('46');
    expect(mapDetail(tileElement)).toHaveTextContent('helper()Calc → Util');
    expect(mapDetail(tileElement)?.querySelector('.review-stop')).toHaveAttribute('data-path', 'util.go');
    await press('ArrowRight');
    expect(currentMessage(tileElement)).toBe('46');

    await press('Enter');
    expect(fileView(tileElement)).toHaveTextContent('util.go');
    await press('Escape');

    await press('b');
    expect(diagrams(tileElement)).toEqual(['31']);
    expect(currentMessage(tileElement)).toBe('35');
    expect(mapDetail(tileElement)).toHaveTextContent('Message 1 of 1 · before the change');
    await press('b');
    expect(diagrams(tileElement)).toEqual(['39']);
    expect(currentMessage(tileElement)).toBe('44');
  });

  it('switches frames with 1, 2 and 3 and keeps the stop and line the cursor is on', async () => {
    const { tileElement, press } = await openReview({ review: { ...MAP_BUNDLE, frame: 'brief' }, content: MAP_DOCUMENT });
    expect(frameTabs(tileElement)).toEqual(['1Brief *', '2Walkthrough', '3Map']);
    await press('ArrowRight');
    await press('ArrowRight');
    await press('ArrowDown');
    expect(cursor(tileElement)).toHaveAttribute('data-line', '2');

    await press('2');
    expect(stageStop(tileElement)).toBe('util.go');
    expect(fileCursorText(tileElement)).toBe('2+func helper() {}');

    await press('ArrowLeft');
    await press('ArrowDown');
    expect(fileCursorText(tileElement)).toBe('5+\treturn sum');
    await press('3');
    expect(currentMessage(tileElement)).toBe('44');
    expect(mapCursor(tileElement)).toHaveAttribute('data-line', '5');

    await press('ArrowRight');
    await press('1');
    expect(tileElement.querySelector('.review-map')).toBeNull();
    expect(cursor(tileElement)?.closest('.review-stop')).toHaveAttribute('data-path', 'util.go');
    expect(cursor(tileElement)).toHaveAttribute('data-line', '1');
  });

  it('leaves out a frame the document cannot fill, and shows a diagram outside the drawn subset as a mermaid figure', async () => {
    const undrawn: ReviewBundle = { ...MAP_BUNDLE, flows: [MAP_BUNDLE.flows[2]] };
    const { tileElement, press } = await openReview({ review: undrawn, content: MAP_DOCUMENT });

    expect(frameTabs(tileElement)).toEqual(['1Brief *', '2Walkthrough']);
    await press('3');
    expect(tileElement.querySelector('.review-map')).toBeNull();
    await act(() => vi.dynamicImportSettled());
    expect(tileElement.querySelectorAll('.review-doc .markdown-mermaid-frame')).toHaveLength(3);
    expect(tileElement.querySelector('.review-seq')).toBeNull();
  });
});

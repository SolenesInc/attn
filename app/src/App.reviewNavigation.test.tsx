import { act, fireEvent, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openMarkdownTiles } from './test/appFixtures';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import type { CodeNavLocation, ReviewBundle, ReviewNavigation, ReviewRow } from './types/generated';

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

### calc.go:3-6
`;

const row = (kind: string, old: number | undefined, now: number | undefined, text: string): ReviewRow => ({
  kind,
  ...(old !== undefined ? { old } : {}),
  ...(now !== undefined ? { now, head: now } : {}),
  text,
});

const CALC_ROWS = [
  row(' ', 3, 3, 'func Add(a, b int) int {'),
  row('-', 4, undefined, '\treturn a + b'),
  row('+', undefined, 4, '\tsum := a + b'),
  row('+', undefined, 5, '\treturn sum'),
  row(' ', 5, 6, '}'),
];

const bundleWith = (navigation: ReviewNavigation[] = [{ language: 'go', state: 'ready' }]): ReviewBundle => ({
  frame: 'brief',
  repository: REPO,
  base: '4be1c0d',
  head: '9f3a7e2',
  stops: [{
    heading_line: 11, path: 'calc.go', from: 3, to: 6, now_from: 3, now_to: 6, changed_since_presented: false, file_hash: 'h1',
    rows: CALC_ROWS, notes: [],
  }],
  files: [{ path: 'calc.go', status: 'modified', added: 2, removed: 1, binary: false }],
  flows: [],
  not_shown: [],
  navigation,
  problems: [],
});

const at = (path: string, line: number, character: number, text: string, outside = false): CodeNavLocation => ({
  path, line, character, end_line: line, end_character: character + 3, text, outside,
});

function serveFiles(daemon: ScriptedDaemon) {
  daemon.on('markdown_annotations_get', ({ document_uri, source_kind }) => ({
    event: 'markdown_annotations_get_result', document_uri, source_kind, success: true, annotations: [], generation: 1,
  }));
  daemon.on('markdown_annotations_save', ({ document_uri, source_kind, upserts }) => ({
    event: 'markdown_annotations_save_result', document_uri, source_kind, success: true, annotations: upserts, generation: 2,
  }));
  daemon.on('review_file', ({ path }) => ({
    event: 'review_file_result', success: true, path, rows: Array.from({ length: 12 }, (_, index) => row(' ', index + 1, index + 1, `// ${path} line ${index + 1}`)), hash: 'h',
  }));
}

async function openReview(review = bundleWith()) {
  const view = await openMarkdownTiles('', { path: DOC, session: { state: 'idle' }, script: serveFiles });
  const show = async (shown: ReviewBundle) => {
    view.daemon.emit({ event: 'desktop_tile_content', desktop_id: view.daemon.arrangement.profile.current_desktop_id, tile_id: 'tile-a', tile_kind: 'markdown', path: DOC, content: DOCUMENT, review: shown });
    await view.daemon.idle();
  };
  await show(review);
  const tile = view.tile();
  const body = tile.querySelector<HTMLElement>('.desktop-dock-tile-body')!;
  body.focus();
  const press = async (key: string) => {
    fireEvent.keyDown(tile.ownerDocument.activeElement ?? body, { key });
    await act(() => vi.advanceTimersByTimeAsync(0));
    await view.daemon.idle();
  };
  return { ...view, reader: within(tile), tileElement: tile, press, show };
}

const answering = (locations: CodeNavLocation[], text?: string) => ({ event: 'code_nav_result' as const, success: true, language: 'go', locations, ...(text ? { text } : {}) });
const labelsOn = (scope: HTMLElement) => Array.from(scope.querySelectorAll('.review-cursor [data-label]')).map((label) => label.getAttribute('data-label'));
const fileView = (scope: HTMLElement) => scope.querySelector<HTMLElement>('[data-review-file-view]');
const fileCursorText = (scope: HTMLElement) => fileView(scope)?.querySelector<HTMLElement>('.review-cursor')?.textContent;

describe('App review navigation', () => {
  it('labels the names on the line with f and goes to the definition of the one whose label is typed', async () => {
    const { tileElement, daemon, press } = await openReview();
    daemon.on('code_nav', () => answering([at('util.go', 7, 5, 'func helper() {}')]));
    await press('ArrowRight');

    await press('f');
    expect(labelsOn(tileElement)).toEqual(['a', 's', 'd', 'f', 'g', 'h']);
    expect(tileElement.querySelector('.review-cursor .review-row-text')).toHaveTextContent('func Add(a, b int) int {');
    expect(daemon.sentOf('code_nav')).toEqual([]);

    await press('s');
    expect(daemon.sentOf('code_nav').map(({ document, path, line, character, kind }) => ({ document, path, line, character, kind })))
      .toEqual([{ document: DOC, path: 'calc.go', line: 3, character: 5, kind: 'definition' }]);
    expect(labelsOn(tileElement)).toEqual([]);
    expect(fileView(tileElement)).toHaveTextContent('util.go');
    expect(fileCursorText(tileElement)).toContain('util.go line 7');
  });

  it('labels the names on the file view’s line, as the Walkthrough shows it, and asks about the one picked', async () => {
    const { tileElement, daemon, press } = await openReview();
    daemon.on('code_nav', () => answering([at('calc.go', 9, 0, 'func line() {}')]));
    await press('2');
    expect(fileCursorText(tileElement)).toContain('calc.go line 4');

    await press('f');
    expect(Array.from(fileView(tileElement)!.querySelectorAll('.review-cursor [data-label]')).map((label) => label.getAttribute('data-label'))).toEqual(['a', 's', 'd']);
    await press('d');
    expect(daemon.sentOf('code_nav').map(({ path, line, character }) => ({ path, line, character }))).toEqual([{ path: 'calc.go', line: 4, character: 11 }]);
    expect(fileCursorText(tileElement)).toContain('calc.go line 9');
  });

  it('lists the uses of a name with f on its own definition, and opens the one picked from thousands', async () => {
    const { tileElement, daemon, press, reader } = await openReview();
    const uses = Array.from({ length: 3000 }, (_, index) => at(`pkg${index}.go`, index + 1, 2, `\tAdd(${index}, 1)`));
    daemon.on('code_nav', ({ kind }) => (kind === 'definition' ? answering([at('calc.go', 3, 5, 'func Add(a, b int) int {')]) : answering(uses)));
    await press('ArrowRight');
    await press('f');
    await press('s');

    expect(daemon.sentOf('code_nav').map(({ kind, character }) => [kind, character])).toEqual([['definition', 5], ['references', 5]]);
    const list = reader.getByRole('dialog', { name: 'Uses of Add' });
    expect(list).toHaveTextContent('3000');
    expect(within(list).getAllByRole('option').length).toBeLessThan(100);
    expect(fileView(tileElement)).toBeNull();

    await press('ArrowDown');
    await press('Enter');
    expect(reader.queryByRole('dialog', { name: 'Uses of Add' })).toBeNull();
    expect(daemon.sentOf('review_file').slice(-1)[0]?.path).toBe('pkg1.go');
    expect(fileCursorText(tileElement)).toContain('pkg1.go line 2');
  });

  it('offers an answer that arrived over 1.5 s after the question once the cursor moved, and follows one when it did not', async () => {
    const { tileElement, daemon, press, reader } = await openReview();
    daemon.on('code_nav', () => undefined);
    await press('ArrowRight');
    await press('ArrowDown');
    await press('ArrowDown');
    await press('ArrowDown');
    expect(tileElement.querySelector('.review-cursor')).toHaveAttribute('data-line', '5');

    await press('f');
    expect(labelsOn(tileElement)).toEqual(['a', 's']);
    await press('s');
    expect(reader.getByRole('status')).toHaveTextContent('Finding the definition of sum…');
    await press('ArrowUp');
    await act(() => vi.advanceTimersByTimeAsync(2000));
    act(() => daemon.replyTo(daemon.sentOf('code_nav')[0], answering([at('calc.go', 4, 1, '\tsum := a + b')])));
    await daemon.idle();
    expect(fileView(tileElement)).toBeNull();
    const offer = reader.getByRole('dialog', { name: 'Definition of sum' });
    expect(offer).toHaveTextContent('arrived after you moved on');
    await press('Enter');
    expect(fileCursorText(tileElement)).toContain('calc.go line 4');

    await press('Escape');
    await press('f');
    await press('s');
    await act(() => vi.advanceTimersByTimeAsync(2000));
    act(() => daemon.replyTo(daemon.sentOf('code_nav')[1], answering([at('calc.go', 4, 1, '\tsum := a + b')])));
    await daemon.idle();
    expect(reader.queryByRole('dialog', { name: 'Definition of sum' })).toBeNull();
    expect(fileCursorText(tileElement)).toContain('calc.go line 4');
  });

  it.each([
    ['the search', '/', 'Search the repository'],
    ['the edit box', 'i', 'Edit the file'],
    ['the composer', 'c', 'Write a note'],
  ])('offers a definition that arrives while %s is open, and leaves the view as it is', async (_what, key, dialog) => {
    const { tileElement, daemon, press, reader } = await openReview();
    daemon.on('code_nav', () => undefined);
    await press('ArrowRight');
    await press('f');
    await press('s');
    await press(key);
    const open = reader.getByRole('dialog', { name: new RegExp(dialog) });

    act(() => daemon.replyTo(daemon.sentOf('code_nav')[0], answering([at('util.go', 7, 5, 'func helper() {}')])));
    await daemon.idle();
    expect(fileView(tileElement)).toBeNull();
    expect(daemon.sentOf('review_file').map(({ path }) => path)).not.toContain('util.go');
    expect(open).toBeInTheDocument();
    expect(reader.getByRole('dialog', { name: 'Definition of Add' })).toHaveTextContent('arrived after you moved on');
  });

  it('opens nothing for i, c or a in a file outside the repository, and says why', async () => {
    const { tileElement, daemon, press, reader } = await openReview();
    daemon.on('code_nav', () => answering([at('/usr/lib/go/src/builtin/builtin.go', 90, 5, 'type int int', true)]));
    await press('ArrowRight');
    await press('f');
    await press('g');
    expect(fileView(tileElement)).toHaveTextContent('outside the repository · read-only');
    expect(reader.getByLabelText('Review keys')).not.toHaveTextContent('edit');

    for (const key of ['i', 'c', 'a']) {
      await press(key);
      expect(reader.queryByRole('dialog')).toBeNull();
      expect(reader.getByRole('status')).toHaveTextContent('/usr/lib/go/src/builtin/builtin.go is outside the repository, so it takes no edits or notes.');
    }
    await press('?');
    expect(reader.getByRole('dialog', { name: 'Review keys' })).toHaveTextContent('This file is outside the repository and read-only');
  });

  it('keeps expanded lines and reads no file when only a language server’s state changes', async () => {
    const { tileElement, daemon, press, show, reader } = await openReview(bundleWith([{ language: 'go', state: 'starting' }]));
    daemon.on('review_file', ({ path }) => ({
      event: 'review_file_result', success: true, path, hash: 'h1',
      rows: [row(' ', 1, 1, 'package calc'), row(' ', 2, 2, ''), ...CALC_ROWS, row(' ', 6, 7, ''), row(' ', 7, 8, 'func Sub(a, b int) int {')],
    }));
    const rows = () => tileElement.querySelectorAll('.review-stop[data-path="calc.go"] [data-review-cursor="row"]').length;
    await press('ArrowRight');
    await press('e');
    expect(rows()).toBe(9);
    const reads = daemon.sentOf('review_file').length;

    await show(bundleWith([{ language: 'go', state: 'ready' }]));
    expect(reader.getByLabelText('Language servers')).toHaveTextContent('go ready');
    expect(rows()).toBe(9);
    expect(daemon.sentOf('review_file')).toHaveLength(reads);

    await show({ ...bundleWith(), head: 'a1b2c3d' });
    expect(rows()).toBe(9);
    const changed = bundleWith();
    await show({ ...changed, stops: [{ ...changed.stops[0], file_hash: 'h2' }] });
    expect(rows()).toBe(5);
  });

  it('shows the type with h, lists uses with r, and opens a definition outside the repository read-only', async () => {
    const { tileElement, daemon, press, reader } = await openReview();
    daemon.on('code_nav', ({ kind }) => {
      if (kind === 'hover') return answering([], 'func Add(a int, b int) int');
      if (kind === 'references') return answering([at('use.go', 9, 8, '\treturn Add(1, 2)')]);
      return answering([at('/usr/lib/go/src/builtin/builtin.go', 90, 5, 'type int int', true)]);
    });
    await press('ArrowRight');

    await press('h');
    await press('s');
    expect(reader.getByRole('dialog', { name: 'Type of Add' })).toHaveTextContent('func Add(a int, b int) int');
    await press('Escape');

    await press('r');
    await press('s');
    expect(within(reader.getByRole('dialog', { name: 'Uses of Add' })).getByRole('option')).toHaveTextContent('use.go:9');
    await press('Escape');

    await press('f');
    await press('g');
    expect(daemon.sentOf('review_file').slice(-1)[0]?.path).toBe('/usr/lib/go/src/builtin/builtin.go');
    expect(fileView(tileElement)).toHaveTextContent('outside the repository · read-only');
  });

  it('says why a question got no answer, and asks nothing about a removed line or prose', async () => {
    const { daemon, press, reader } = await openReview();
    daemon.on('code_nav', () => ({ event: 'code_nav_result' as const, success: false, error: 'gopls is not in /data/code-navigation/bin or on PATH' }));

    await press('f');
    expect(reader.getByRole('status')).toHaveTextContent('Put the cursor on a line of code');
    await press('ArrowRight');
    await press('ArrowDown');
    await press('f');
    expect(reader.getByRole('status')).toHaveTextContent('This line was removed');
    await press('ArrowDown');
    await press('f');
    await press('a');
    expect(reader.getByRole('alert')).toHaveTextContent('gopls is not in /data/code-navigation/bin or on PATH');
    expect(daemon.sentOf('code_nav')).toHaveLength(1);
  });

  it('shows each language server’s state in the key line, with what a loading one is doing', async () => {
    const { reader } = await openReview(bundleWith([
      { language: 'go', state: 'ready' },
      { language: 'rust', state: 'starting', detail: 'Indexing' },
      { language: 'zig', state: 'missing', detail: 'zls is not in /data/code-navigation/bin or on PATH' },
    ]));
    const states = reader.getByLabelText('Language servers');
    expect(Array.from(states.children).map((state) => state.textContent)).toEqual(['go ready', 'rust loading: Indexing', 'zig no server']);
    expect(within(states).getByText('zig no server')).toHaveAttribute('title', 'zls is not in /data/code-navigation/bin or on PATH');
    expect(reader.getByLabelText('Review keys')).toHaveTextContent('definition');
  });
});

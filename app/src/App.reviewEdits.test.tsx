import { act, fireEvent, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openMarkdownTiles } from './test/appFixtures';
import type { CommandMessage, EventMessage } from './test/protocol';
import type { ReviewBundle, ReviewRow } from './types/generated';

type WireAnnotation = EventMessage<'markdown_annotations_get_result'>['annotations'][number];

const DOC = '/notes/review.md';

const DOCUMENT = `---
attn: review
frame: brief
repository: /repo
base: 4be1c0d
head: 9f3a7e2
---

# Name the sum

Add used to return its sum directly.

## Add keeps its result

### calc.go:3-10

- L4!: The sum gets a name.
`;

const row = (kind: string, old: number | undefined, now: number | undefined, text: string, head?: number): ReviewRow => ({
  kind,
  ...(old !== undefined ? { old } : {}),
  ...(now !== undefined ? { now } : {}),
  ...(head !== undefined ? { head } : {}),
  text,
});

/** calc.go as rows: the change from base to its lines now, each with its line at head. */
function calcRows(lines: readonly string[], heads: readonly (number | undefined)[]): ReviewRow[] {
  const added = lines.slice(3, lines.length - 5);
  return [
    ...lines.slice(0, 3).map((text, index) => row(' ', index + 1, index + 1, text, heads[index])),
    row('-', 4, undefined, '\treturn a + b'),
    ...added.map((text, index) => row('+', undefined, 4 + index, text, heads[3 + index])),
    ...lines.slice(3 + added.length).map((text, index) => row(' ', 5 + index, 4 + added.length + index, text, heads[3 + added.length + index])),
  ];
}

function bundleOf(rows: ReviewRow[], hash: string, linesAbove = 0): ReviewBundle {
  const last = rows[rows.length - 1].now!;
  return {
    frame: 'brief',
    repository: '/repo',
    base: '4be1c0d',
    head: '9f3a7e2',
    stops: [{
      heading_line: 15 + linesAbove, path: 'calc.go', from: 3, to: 10, now_from: 3, now_to: last, changed_since_presented: false, file_hash: hash,
      rows: rows.filter((candidate) => (candidate.now ?? 4) >= 3),
      notes: [{ line: 17 + linesAbove, from: 4, to: 4, now_from: 4, now_to: 4, look: true, text: 'The sum gets a name.' }],
    }],
    files: [{ path: 'calc.go', status: 'modified', added: 2, removed: 1, binary: false }],
    flows: [],
    not_shown: [],
    navigation: [],
    problems: [],
  };
}

/** The file on the review's machine: review_file reads it, review_edit writes it while its hash holds. */
const HEAD = ['package calc', '', 'func Add(a, b int) int {', '\tsum := a + b', '\treturn sum', '}', '', 'const query = `', 'SELECT 1', '`'];

/** Each line's line at head, as the daemon gives it: through the longest run of lines head and now share. */
function headsOf(lines: readonly string[]): Array<number | undefined> {
  const table = Array.from({ length: HEAD.length + 1 }, () => Array.from({ length: lines.length + 1 }, () => 0));
  for (let i = HEAD.length - 1; i >= 0; i -= 1) {
    for (let j = lines.length - 1; j >= 0; j -= 1) {
      table[i][j] = HEAD[i] === lines[j] ? table[i + 1][j + 1] + 1 : Math.max(table[i + 1][j], table[i][j + 1]);
    }
  }
  const heads: Array<number | undefined> = lines.map(() => undefined);
  for (let i = 0, j = 0; i < HEAD.length && j < lines.length;) {
    if (HEAD[i] === lines[j]) heads[j++] = ++i;
    else if (table[i + 1][j] >= table[i][j + 1]) i += 1;
    else j += 1;
  }
  return heads;
}

class ReviewedFile {
  lines = [...HEAD];
  version = 1;
  conflictNext = false;

  get hash() {
    return `h${this.version}`;
  }

  get added() {
    return this.lines.slice(3, this.lines.length - 5);
  }

  rows() {
    return calcRows(this.lines, headsOf(this.lines));
  }

  /** Lines written after head, by the user's edit or by the agent. */
  write(from: number, to: number, text: string) {
    this.lines.splice(from - 1, to - from + 1, ...text.split('\n'));
    this.version += 1;
  }

  edit({ base_hash, from, to, text }: CommandMessage<'review_edit'>) {
    if (this.conflictNext || base_hash !== this.hash) {
      this.conflictNext = false;
      this.version += 1;
      return { event: 'review_edit_result' as const, success: true, conflict: true, current_hash: this.hash };
    }
    this.write(from, to, text);
    return { event: 'review_edit_result' as const, success: true, conflict: false, hash: this.hash };
  }
}

class Draft {
  annotations: WireAnnotation[] = [];
  generation = 1;

  change({ upserts, removed_ids }: { upserts: WireAnnotation[]; removed_ids: string[] }) {
    const kept = this.annotations.filter((mark) => !removed_ids.includes(mark.id));
    for (const upsert of upserts) {
      const at = kept.findIndex((mark) => mark.id === upsert.id);
      if (at >= 0) kept[at] = upsert;
      else kept.push(upsert);
    }
    this.annotations = kept;
    this.generation += 1;
    return { annotations: this.annotations, generation: this.generation };
  }
}

async function openReview({ withBundle = true } = {}) {
  const draft = new Draft();
  const file = new ReviewedFile();
  const view = await openMarkdownTiles('', {
    path: DOC,
    session: { state: 'idle' },
    script: (daemon) => {
      daemon.on('markdown_annotations_get', ({ document_uri, source_kind }) => ({
        event: 'markdown_annotations_get_result', document_uri, source_kind, success: true, annotations: draft.annotations, generation: draft.generation,
      }));
      daemon.on('markdown_annotations_save', (save) => ({
        event: 'markdown_annotations_save_result', document_uri: save.document_uri, source_kind: save.source_kind, success: true, ...draft.change(save),
      }));
      daemon.on('markdown_annotations_submit', () => undefined);
      daemon.on('review_file', ({ path }) => ({ event: 'review_file_result', success: true, path, rows: file.rows(), hash: file.hash }));
      daemon.on('review_edit', (edit) => file.edit(edit));
    },
  });
  const desktopId = view.daemon.arrangement.profile.current_desktop_id;
  const show = async (content = DOCUMENT) => {
    view.daemon.emit({
      event: 'desktop_tile_content', desktop_id: desktopId, tile_id: 'tile-a', tile_kind: 'markdown', path: DOC, content,
      ...(withBundle ? { review: bundleOf(file.rows(), file.hash, content.split('\n').length - DOCUMENT.split('\n').length) } : {}),
    });
    await view.daemon.idle();
  };
  await show();
  const tile = view.tile();
  tile.querySelector<HTMLElement>('.desktop-dock-tile-body')!.focus();
  const settle = async () => {
    await act(() => vi.advanceTimersByTimeAsync(500));
    await view.daemon.idle();
  };
  const press = async (key: string, init: KeyboardEventInit = {}) => {
    fireEvent.keyDown(tile.ownerDocument.activeElement ?? tile, { key, ...init });
    await settle();
  };
  const editBox = () => within(tile).getByRole('dialog', { name: 'Edit the file' });
  const type = async (text: string, init: KeyboardEventInit = {}) => {
    const input = within(editBox()).getByRole('textbox');
    fireEvent.change(input, { target: { value: text } });
    fireEvent.keyDown(input, { key: 'Enter', ...init });
    await settle();
  };
  const cursorLine = () => tile.querySelector('.review-doc .review-cursor')?.getAttribute('data-line');
  const toRow = async (line: number) => {
    await press('ArrowRight');
    const passed: Array<string | null | undefined> = [];
    while (cursorLine() !== String(line)) {
      if (passed.length > 40) throw new Error(`the cursor never reached line ${line}; it passed ${passed.join(', ')}`);
      passed.push(cursorLine());
      await press(Number(cursorLine()) > line ? 'ArrowUp' : 'ArrowDown');
    }
  };
  return { ...view, draft, file, tile, show, press, type, editBox, toRow, cursorLine, settle };
}

describe('App review edits', () => {
  it('edits the line at the cursor, saves it to the file against the hash it read, and lists it under the line', async () => {
    const { tile, press, type, editBox, toRow, daemon, draft, show } = await openReview();
    await toRow(4);
    await press('i');
    expect(editBox()).toHaveTextContent('calc.go: line 4');
    expect(within(editBox()).getByRole('textbox')).toHaveValue('\tsum := a + b');

    await type('\ttotal := a + b');
    expect(daemon.sentOf('review_edit')).toEqual([expect.objectContaining({ document: DOC, path: 'calc.go', base_hash: 'h1', from: 4, to: 4, text: '\ttotal := a + b' })]);
    expect(within(tile).queryByRole('dialog')).toBeNull();
    expect(draft.annotations).toEqual([expect.objectContaining({
      type: 'edit', edit_was: '\tsum := a + b', edit_now: '\ttotal := a + b',
      anchor: expect.objectContaining({ file: 'calc.go', file_start_line: 4, file_end_line: 4, start_line: 15 }),
    })]);

    await show();
    expect(tile.querySelector('.review-doc [data-line="4"]')).toHaveTextContent('total := a + b');
    expect(tile.querySelector('.review-mark--edit')).toHaveTextContent('you editedL4Saved to the file. It was: sum := a + b');
  });

  it('takes a whole raw string of several lines, and Option-Up takes in the line above', async () => {
    const { press, editBox, toRow } = await openReview();
    await toRow(9);
    await press('i');
    expect(editBox()).toHaveTextContent('calc.go: this string, 3 lines');
    expect(within(editBox()).getByRole('textbox')).toHaveValue('const query = `\nSELECT 1\n`');

    fireEvent.keyDown(within(editBox()).getByRole('textbox'), { key: 'ArrowUp', altKey: true });
    await press('Shift');
    expect(editBox()).toHaveTextContent('calc.go: lines 7–10');
    expect(within(editBox()).getByRole('textbox')).toHaveValue('\nconst query = `\nSELECT 1\n`');
  });

  it('refuses an edit of a file that changed underneath, shows why, and records nothing', async () => {
    const { tile, press, type, editBox, toRow, daemon, draft, file } = await openReview();
    await toRow(5);
    await press('i');
    file.conflictNext = true;
    await type('\treturn total');
    expect(daemon.sentOf('review_edit')).toHaveLength(1);
    expect(editBox()).toHaveTextContent('calc.go changed since this edit opened, so nothing was written');
    expect(within(editBox()).getByRole('textbox')).toHaveValue('\treturn total');
    expect(draft.annotations).toEqual([]);
    await press('Escape');
    expect(within(tile).queryByRole('dialog')).toBeNull();
  });

  it('undoes an edit with u: writes the old text back where the edit stands and drops it from the review', async () => {
    const { tile, press, type, toRow, daemon, draft, show, file } = await openReview();
    await toRow(4);
    await press('i');
    await type('\ttotal := a + b\n\t// named', { metaKey: true });
    await show();
    expect(file.added).toEqual(['\ttotal := a + b', '\t// named', '\treturn sum']);

    await toRow(5);
    await press('u');
    const [, undo] = daemon.sentOf('review_edit');
    expect(undo).toMatchObject({ path: 'calc.go', base_hash: 'h2', from: 4, to: 5, text: '\tsum := a + b' });
    expect(file.added).toEqual(['\tsum := a + b', '\treturn sum']);
    expect(draft.annotations).toEqual([]);
    await show();
    expect(tile.querySelector('.review-mark--edit')).toBeNull();
  });

  it('keeps typed text above a line Option-Down takes in, even from a blank line', async () => {
    const { press, editBox, toRow } = await openReview();
    await toRow(7);
    await press('i');
    const input = within(editBox()).getByRole('textbox');
    fireEvent.change(input, { target: { value: '// the query' } });
    fireEvent.keyDown(input, { key: 'ArrowDown', altKey: true });
    await press('Shift');
    expect(within(editBox()).getByRole('textbox')).toHaveValue('// the query\nconst query = `');
  });

  it('undoes the newest edit of a line, and moves an edit the file no longer holds to the moved notes, where dropping it writes nothing', async () => {
    const { tile, press, type, toRow, daemon, draft, show, file } = await openReview();
    await toRow(4);
    await press('i');
    await type('\ttotal := a + b');
    await show();
    await press('i');
    await type('\tresult := a + b');
    await show();

    await press('u');
    expect(daemon.sentOf('review_edit')[2]).toMatchObject({ from: 4, to: 4, text: '\ttotal := a + b' });
    expect(file.added[0]).toBe('\ttotal := a + b');
    expect(draft.annotations).toEqual([expect.objectContaining({ edit_was: '\tsum := a + b', edit_now: '\ttotal := a + b' })]);
    await show();
    expect(tile.querySelector('.review-doc [data-review-mark]')).toHaveTextContent('It was: sum := a + b');

    file.write(4, 4, '\tagent := a + b');
    await show();
    const moved = within(tile).getByRole('region', { name: 'Moved notes' });
    expect(moved).toHaveTextContent('It was: sum := a + b');
    await press('u');
    expect(daemon.sentOf('review_edit')).toHaveLength(3);
    fireEvent.click(within(moved).getByTitle('Drop from the review; the file stays as it is'));
    await press('Shift');
    expect(draft.annotations).toEqual([]);
    expect(daemon.sentOf('review_edit')).toHaveLength(3);
    expect(file.added[0]).toBe('\tagent := a + b');
  });

  it('shows an edit at once in the open file, outside any stop, and undoes the newer of two edits there', async () => {
    const { tile, press, type, toRow, daemon, file } = await openReview();
    await toRow(4);
    await press('Enter');
    const fileCursor = () => tile.querySelector('.review-file-view .review-cursor');
    while (!fileCursor()?.textContent?.includes('package')) await press('ArrowUp');

    await press('i');
    await type('package sums');
    expect(fileCursor()).toHaveTextContent('package sums');
    await press('i');
    await type('package totals');
    expect(fileCursor()).toHaveTextContent('package totals');
    expect(tile.querySelector('.review-file-view')).toHaveTextContent('you edited');

    await press('u');
    expect(daemon.sentOf('review_edit')[2]).toMatchObject({ from: 1, to: 1, text: 'package sums' });
    expect(file.lines[0]).toBe('package sums');
    expect(fileCursor()).toHaveTextContent('package sums');
  });

  it('never moves an edit onto another line with the same text, and u there writes nothing', async () => {
    const { tile, press, type, toRow, daemon, draft, show, file } = await openReview();
    await toRow(4);
    await press('i');
    await type('}');
    await show();
    await press('i');
    await type('\tdone := true');
    await show();
    expect(file.lines.filter((line) => line === '}')).toHaveLength(1);

    const moved = within(tile).getByRole('region', { name: 'Moved notes' });
    expect(moved).toHaveTextContent('It was: sum := a + b');
    await toRow(6);
    expect(tile.querySelector('.review-doc .review-cursor')).toHaveTextContent('}');
    await press('u');
    expect(daemon.sentOf('review_edit')).toHaveLength(2);
    expect(file.lines[5]).toBe('}');
    expect(draft.annotations).toHaveLength(2);
  });

  it('keeps an edit on its line when the line above is edited and the agent adds lines above both, and u finds it there', async () => {
    const { tile, press, type, toRow, daemon, show, file } = await openReview();
    await toRow(5);
    await press('i');
    await type('\treturn sum // named');
    await show();
    await toRow(4);
    await press('i');
    await type('\ttotal := a + b');
    file.write(1, 1, '// Package calc adds.\npackage calc');
    await show();
    expect(within(tile).queryByRole('region', { name: 'Moved notes' })).toBeNull();
    expect(Array.from(tile.querySelectorAll('.review-doc [data-review-mark]')).map((mark) => mark.textContent)).toEqual([
      expect.stringContaining('L4Saved to the file. It was: \tsum := a + b'),
      expect.stringContaining('L5Saved to the file. It was: \treturn sum'),
    ]);

    await toRow(6);
    await press('u');
    expect(daemon.sentOf('review_edit')[2]).toMatchObject({ from: 6, to: 6, text: '\treturn sum' });
    expect(file.lines.slice(4, 6)).toEqual(['\ttotal := a + b', '\treturn sum']);
  });

  it('edits a line of a CRLF file without its carriage return', async () => {
    const { press, editBox, toRow, file, show } = await openReview();
    file.write(4, 5, '\tsum := a + b\r\n\treturn sum\r');
    await show();
    await toRow(4);
    await press('i');
    expect(within(editBox()).getByRole('textbox')).toHaveValue('\tsum := a + b');
  });

  it('keeps the cursor on the edited line when the edit changes the stop under it', async () => {
    const { tile, press, type, toRow, show, cursorLine } = await openReview();
    await toRow(5);
    await press('i');
    await type('\t// the sum\n\treturn sum', { metaKey: true });
    await show(DOCUMENT.replace('Add used to return its sum directly.', 'Add used to return its sum directly.\n\nNow it is named.'));
    expect(tile).toHaveTextContent('Now it is named.');
    expect(cursorLine()).toBe('5');
    expect(tile.querySelector('.review-doc .review-cursor')).toHaveTextContent('// the sum');

    await press('Enter');
    await press('i');
    await type('\t// the named sum');
    await show();
    expect(tile.querySelector('.review-file-view .review-cursor')).toHaveTextContent('// the named sum');
  });

  it('lists edits apart from notes in the send', async () => {
    const { tile, press, type, toRow } = await openReview();
    await toRow(4);
    await press('i');
    await type('\ttotal := a + b');
    await press('Enter', { metaKey: true });
    const sheet = within(tile).getByRole('dialog', { name: 'Send the review' });
    expect(sheet).toHaveTextContent('Your edits, saved to the files');
    expect(sheet).toHaveTextContent('calc.go:4edit');
  });

  it('offers no edit on a document without a review bundle', async () => {
    const { tile, press, daemon } = await openReview({ withBundle: false });
    await press('i');
    await press('u');
    expect(within(tile).queryByRole('dialog')).toBeNull();
    expect(daemon.sentOf('review_file')).toEqual([]);
    expect(daemon.sentOf('review_edit')).toEqual([]);
  });
});

import { act, fireEvent, within } from '@testing-library/react';
import { describe, expect, it, onTestFinished, vi } from 'vitest';
import { openMarkdownTiles } from './test/appFixtures';
import { stubNavigatorPlatform } from './test/platformStub';
import type { CommandMessage, EventMessage } from './test/protocol';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import type { ReviewBundle, ReviewRow } from './types/generated';

type WireAnnotation = EventMessage<'markdown_annotations_get_result'>['annotations'][number];

const DOC = '/notes/review.md';
const DOC_URI = 'attn://file/%2Fnotes%2Freview.md';

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

### calc.go:3-6

- L4!: The sum gets a name.
`;

const row = (kind: string, old: number | undefined, now: number | undefined, text: string, head: number | null = kind === '-' ? null : now ?? null): ReviewRow => ({
  kind,
  ...(old !== undefined ? { old } : {}),
  ...(now !== undefined ? { now } : {}),
  ...(head !== null ? { head } : {}),
  text,
});

const BUNDLE: ReviewBundle = {
  frame: 'brief',
  repository: '/repo',
  base: '4be1c0d',
  head: '9f3a7e2',
  stops: [{
    heading_line: 15, path: 'calc.go', from: 3, to: 6, now_from: 3, now_to: 6, changed_since_presented: false, file_hash: 'h1',
    rows: [
      row(' ', 3, 3, 'func Add(a, b int) int {'),
      row('-', 4, undefined, '\treturn a + b'),
      row('+', undefined, 4, '\tsum := a + b'),
      row('+', undefined, 5, '\treturn sum'),
      row(' ', 5, 6, '}'),
    ],
    notes: [{ line: 17, from: 4, to: 4, now_from: 4, now_to: 4, look: true, text: 'The sum gets a name.' }],
  }],
  files: [{ path: 'calc.go', status: 'modified', added: 2, removed: 1, binary: false }],
  flows: [],
  not_shown: [],
  navigation: [],
  problems: [],
};

// The daemon's draft for the review: saves change it mark by mark, every reply carries it whole.
class Draft {
  annotations: WireAnnotation[] = [];
  generation = 1;

  change({ upserts, removed_ids }: { upserts: WireAnnotation[]; removed_ids: string[] }) {
    const kept = this.annotations.filter((mark) => !removed_ids.includes(mark.id));
    for (const { answer: _ignored, ...upsert } of upserts) {
      const at = kept.findIndex((mark) => mark.id === upsert.id);
      if (at >= 0) kept[at] = { ...upsert, ...(kept[at].answer === undefined ? {} : { answer: kept[at].answer }) };
      else kept.push(upsert);
    }
    this.annotations = kept;
    this.generation += 1;
    return this.reply();
  }

  answer(id: string, text: string) {
    this.annotations = this.annotations.map((mark) => (mark.id === id ? { ...mark, answer: text } : mark));
    this.generation += 1;
  }

  reply() {
    return { annotations: this.annotations, generation: this.generation };
  }
}

const FILE_TOP = [row(' ', 1, 1, 'package calc'), row(' ', 2, 2, '')];

async function openReview({ review = BUNDLE as ReviewBundle | null } = {}) {
  const draft = new Draft();
  let presented = review ?? BUNDLE;
  const view = await openMarkdownTiles('', {
    path: DOC,
    session: { state: 'idle' },
    script: (daemon) => {
      daemon.on('markdown_annotations_get', ({ document_uri, source_kind }) => ({
        event: 'markdown_annotations_get_result', document_uri, source_kind, success: true, ...draft.reply(),
      }));
      daemon.on('markdown_annotations_save', (save) => ({
        event: 'markdown_annotations_save_result', document_uri: save.document_uri, source_kind: save.source_kind, success: true, ...draft.change(save),
      }));
      daemon.on('markdown_annotations_submit', () => undefined);
      daemon.on('review_file', ({ path }) => ({ event: 'review_file_result', success: true, path, rows: [...FILE_TOP, ...presented.stops[0].rows], hash: 'h1' }));
    },
  });
  const desktopId = view.daemon.arrangement.profile.current_desktop_id;
  const show = async (fields: Record<string, unknown> = {}) => {
    if (fields.review) presented = fields.review as ReviewBundle;
    view.daemon.emit({ event: 'desktop_tile_content', desktop_id: desktopId, tile_id: 'tile-a', tile_kind: 'markdown', path: DOC, content: DOCUMENT, ...(review ? { review } : {}), ...fields });
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
  const write = async (text: string) => {
    const input = within(tile).getByRole('textbox');
    fireEvent.change(input, { target: { value: text } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await settle();
  };
  const toRow = async (line: number) => {
    await press('ArrowRight');
    while (tile.querySelector('.review-doc .review-cursor')?.getAttribute('data-line') !== String(line)) await press('ArrowDown');
  };
  return { ...view, draft, tile, show, press, write, toRow, settle };
}

const marksUnder = (tile: HTMLElement) => Array.from(tile.querySelectorAll('.review-rows > *')).map((element) => (
  element.classList.contains('review-mark') ? `mark ${element.textContent}` : element.classList.contains('review-note') ? 'note' : `row ${element.getAttribute('data-line') ?? '-'}`
));

function answerSubmit(daemon: ScriptedDaemon, submit: CommandMessage<'markdown_annotations_submit'>, draft: Draft, spent: boolean) {
  if (spent) {
    draft.annotations = [];
    draft.generation += 1;
  }
  daemon.replyTo(submit, {
    event: 'markdown_annotations_submit_result', request_id: submit.request_id, document_uri: submit.document_uri, source_kind: submit.source_kind,
    success: true, status: 'delivered', ...draft.reply(),
  });
}

describe('App review marks', () => {
  it('writes a note on the selected lines of a stop, quoting them, and shows it under the last line', async () => {
    const { tile, press, write, toRow, daemon } = await openReview();
    await toRow(4);
    await press('ArrowDown', { shiftKey: true });
    expect(Array.from(tile.querySelectorAll('.review-doc .review-selected')).map((element) => element.getAttribute('data-line'))).toEqual(['4', '5']);

    await press('c');
    expect(within(tile).getByRole('dialog', { name: 'Write a note' })).toHaveTextContent('calc.go:4–5');
    await write('Return a + b directly.');

    const [save] = daemon.sentOf('markdown_annotations_save');
    expect(save.upserts).toEqual([expect.objectContaining({
      type: 'comment',
      text: 'Return a + b directly.',
      anchor: expect.objectContaining({ block_id: '', start_line: 15, exact: '\tsum := a + b\n\treturn sum', file: 'calc.go', file_start_line: 4, file_end_line: 5 }),
    })]);
    expect(daemon.sentOf('markdown_annotations_submit')).toEqual([]);
    expect(marksUnder(tile)).toEqual(['row 3', 'row -', 'row 4', 'note', 'row 5', 'mark youL4–5Return a + b directly.×', 'row 6']);
    expect(tile.querySelector('.review-selected')).toBeNull();
  });

  it('quotes a removed line with its diff sign and places it at the head line it sat before', async () => {
    const { tile, press, write, toRow, daemon } = await openReview();
    await toRow(3);
    await press('ArrowDown');
    await press('c');
    expect(within(tile).getByRole('dialog', { name: 'Write a note' })).toHaveTextContent('calc.go:4');
    await write('Keep this line.');
    const [save] = daemon.sentOf('markdown_annotations_save');
    expect(save.upserts[0].anchor).toMatchObject({ exact: '-\treturn a + b', file: 'calc.go', file_start_line: 4, file_end_line: 4 });
  });

  it('writes a note on the selected lines of the file the Walkthrough shows', async () => {
    const { tile, press, write, daemon } = await openReview({ review: { ...BUNDLE, frame: 'walkthrough' } });
    expect(tile.querySelector('.review-file-view .review-cursor')).toHaveTextContent('sum := a + b');
    await press('ArrowDown', { shiftKey: true });
    await press('c');
    expect(within(tile).getByRole('dialog', { name: 'Write a note' })).toHaveTextContent('calc.go:4–5');
    await write('Return a + b directly.');
    const [save] = daemon.sentOf('markdown_annotations_save');
    expect(save.upserts[0].anchor).toMatchObject({ start_line: 15, exact: '\tsum := a + b\n\treturn sum', file: 'calc.go', file_start_line: 4, file_end_line: 5 });
    expect(tile.querySelector('.review-file-view-marks')).toHaveTextContent('Return a + b directly.');
  });

  it('keeps the file view cursor on its marked line when the agent adds a copy of that line above it', async () => {
    const walkthrough = { ...BUNDLE, frame: 'walkthrough' };
    const { tile, press, write, show, settle } = await openReview({ review: walkthrough });
    await press('ArrowDown');
    expect(tile.querySelector('.review-file-view .review-cursor')).toHaveTextContent('return sum');
    await press('c');
    await write('Return a + b directly.');

    const copied = [
      row(' ', 3, 3, 'func Add(a, b int) int {'),
      row('-', 4, undefined, '\treturn a + b'),
      row('+', undefined, 4, '\tsum := a + b'),
      row('+', undefined, 5, '\treturn sum', null),
      row('+', undefined, 6, '\treturn sum', 5),
      row(' ', 5, 7, '}', 6),
    ];
    await show({ review: { ...walkthrough, stops: [{ ...BUNDLE.stops[0], file_hash: 'h2', now_to: 7, rows: copied }] } });
    await settle();
    expect(tile.querySelector('.review-file-view .review-cursor')).toHaveClass('review-file-view-row--marked');
    expect(tile.querySelector('.review-file-view-marks')).toHaveTextContent('Return a + b directly.');
  });

  it('closes the composer first on Escape, then the selection, then the file view', async () => {
    const { tile, press, toRow } = await openReview();
    await toRow(4);
    await press('Enter');
    await press('ArrowDown', { shiftKey: true });
    await press('c');
    expect(within(tile).getByRole('dialog', { name: 'Write a note' })).toHaveTextContent('calc.go:4–5');

    await press('Escape');
    expect(within(tile).queryByRole('dialog')).toBeNull();
    expect(tile.querySelectorAll('.review-file-view .review-selected')).toHaveLength(2);
    await press('Escape');
    expect(tile.querySelector('.review-file-view .review-selected')).toBeNull();
    expect(tile.querySelector('.review-file-view')).not.toBeNull();
    await press('Escape');
    expect(tile.querySelector('.review-file-view')).toBeNull();
  });

  it('keeps a mark on its line when the document changes above the stop', async () => {
    const { tile, press, write, toRow, show } = await openReview();
    await toRow(4);
    await press('ArrowDown', { shiftKey: true });
    await press('c');
    await write('Return a + b directly.');

    const moved = { ...BUNDLE, stops: BUNDLE.stops.map((stop) => ({ ...stop, heading_line: stop.heading_line + 2, notes: stop.notes.map((note) => ({ ...note, line: note.line + 2 })) })) };
    await show({ content: DOCUMENT.replace('Add used to return its sum directly.', 'Add used to return its sum directly.\n\nIt reads better with a name.'), review: moved });
    expect(tile).toHaveTextContent('It reads better with a name.');
    expect(marksUnder(tile)).toEqual(['row 3', 'row -', 'row 4', 'note', 'row 5', 'mark youL4–5Return a + b directly.×', 'row 6']);
  });

  it('keeps a mark on its lines at head when the agent adds lines above them', async () => {
    const { tile, press, write, toRow, show, settle, daemon } = await openReview();
    await toRow(4);
    await press('ArrowDown', { shiftKey: true });
    await press('c');
    await write('Return a + b directly.');
    const saves = daemon.sentOf('markdown_annotations_save').length;

    const edited = [
      row(' ', 3, 3, 'func Add(a, b int) int {'),
      row('+', undefined, 4, '\t// Add names its sum.', null),
      row('-', 4, undefined, '\treturn a + b'),
      row('+', undefined, 5, '\tsum := a + b', 4),
      row('+', undefined, 6, '\treturn sum', 5),
      row(' ', 5, 7, '}', 6),
    ];
    const notes = BUNDLE.stops[0].notes.map((note) => ({ ...note, now_from: 5, now_to: 5 }));
    await show({ review: { ...BUNDLE, stops: [{ ...BUNDLE.stops[0], now_to: 7, rows: edited, notes }] } });
    await settle();
    expect(marksUnder(tile)).toEqual(['row 3', 'row 4', 'row -', 'row 5', 'note', 'row 6', 'mark youL4–5Return a + b directly.×', 'row 7']);
    expect(daemon.sentOf('markdown_annotations_save')).toHaveLength(saves);
  });

  it('keeps an answer that arrives while the reader still holds an unsaved copy of its question', async () => {
    const { tile, press, write, show, settle, daemon, draft } = await openReview();
    while (!tile.querySelector('.review-doc .review-cursor[data-block-id]')) await press('ArrowDown');
    await press('a');
    await write('Why name it?');
    const [submit] = daemon.sentOf('markdown_annotations_submit');
    answerSubmit(daemon, submit, draft, false);
    await daemon.idle();
    const [question] = draft.annotations;
    expect(question).toMatchObject({ type: 'question', anchor: { block_id: expect.any(String) } });

    draft.answer(question.id, 'So the return reads as a value.');
    await show({ content: DOCUMENT.replace('head: 9f3a7e2', 'head: 1c2d3e4'), annotations_generation: draft.generation });
    await act(() => vi.advanceTimersByTimeAsync(1000));
    await daemon.idle();
    fireEvent.click(within(tile).getByRole('button', { name: /^Notes/ }));
    await settle();
    expect(draft.annotations[0].answer).toBe('So the return reads as a value.');
    expect(tile).toHaveTextContent('So the return reads as a value.');
  });

  it('keeps a mark on context lines shown around a stop, and shows it as moved when those lines change', async () => {
    const { tile, press, write, toRow, show, settle, daemon } = await openReview();
    await toRow(3);
    await press('e');
    while (tile.querySelector('.review-doc .review-cursor')?.getAttribute('data-line') !== '1') await press('ArrowUp');
    await press('c');
    await write('Name the package for what it does.');
    expect(marksUnder(tile).slice(0, 2)).toEqual(['row 1', 'mark youL1Name the package for what it does.×']);

    daemon.on('review_file', ({ path }) => ({
      event: 'review_file_result', success: true, path, hash: 'h2',
      rows: [row('-', 1, undefined, 'package calc'), row('+', undefined, 1, 'package arith', null), row(' ', 2, 2, ''), ...BUNDLE.stops[0].rows],
    }));
    await show({ review: { ...BUNDLE, stops: [{ ...BUNDLE.stops[0], file_hash: 'h2' }] } });
    await settle();
    expect(within(tile).getByRole('region', { name: 'Moved notes' })).toHaveTextContent('Name the package for what it does.');
  });

  it('shows a mark as moved when its code is gone, and sends it marked as moved', async () => {
    const { tile, press, write, toRow, show, daemon } = await openReview();
    await toRow(4);
    await press('ArrowDown', { shiftKey: true });
    await press('c');
    await write('Return a + b directly.');
    const [mark] = daemon.sentOf('markdown_annotations_save')[0].upserts;

    const reverted = [row(' ', 3, 3, 'func Add(a, b int) int {'), row(' ', 4, 4, '\treturn a + b', null), row(' ', 5, 5, '}', 6)];
    await show({ review: { ...BUNDLE, stops: [{ ...BUNDLE.stops[0], now_to: 5, rows: reverted, notes: [] }] } });
    expect(tile.querySelector('.review-rows .review-mark')).toBeNull();
    expect(within(tile).getByRole('region', { name: 'Moved notes' })).toHaveTextContent('calc.go:4–5');
    expect(within(tile).getByRole('region', { name: 'Moved notes' })).toHaveTextContent('Return a + b directly.');

    await press('Enter', { metaKey: true });
    await press('Enter');
    expect(daemon.sentOf('markdown_annotations_submit')).toEqual([expect.objectContaining({ verdict: 'request_changes', orphaned_ids: [mark.id] })]);
  });

  it('asks a question at once, keeps it, and shows the answer when the daemon says the draft moved', async () => {
    const { tile, press, write, toRow, daemon, draft, show } = await openReview();
    await toRow(5);
    await press('a');
    expect(within(tile).getByRole('dialog', { name: 'Ask a question' })).toHaveTextContent('Sent to s1 at once');
    await write('Why name it?');

    const [submit] = daemon.sentOf('markdown_annotations_submit');
    const [question] = draft.annotations;
    expect(question).toMatchObject({ type: 'question', text: 'Why name it?', anchor: { file: 'calc.go', file_start_line: 5, file_end_line: 5 } });
    expect(submit).toMatchObject({ target_session_id: 's1', only_ids: [question.id] });
    expect(submit.verdict).toBeUndefined();
    answerSubmit(daemon, submit, draft, false);
    await daemon.idle();
    expect(tile.querySelector('.review-mark')).toHaveTextContent('Asked — waiting for the answer');

    const reads = daemon.sentOf('markdown_annotations_get').length;
    draft.answer(question.id, 'So the return reads as a value.');
    await show({ annotations_generation: draft.generation });
    expect(daemon.sentOf('markdown_annotations_get')).toHaveLength(reads + 1);
    expect(tile.querySelector('.review-mark .review-mark-answer')).toHaveTextContent('So the return reads as a value.');
  });

  it('sends the review on Cmd+Enter with the verdict picked in the sheet', async () => {
    const { tile, press, write, toRow, daemon, draft } = await openReview();
    await toRow(4);
    await press('c');
    await write('Return a + b directly.');

    await press('Enter', { metaKey: true });
    const sheet = within(tile).getByRole('dialog', { name: 'Send the review' });
    expect(sheet).toHaveTextContent('calc.go:4');
    expect(within(sheet).getByRole('radio', { name: /Request changes/ })).toHaveAttribute('aria-checked', 'true');
    await press('a');
    expect(within(sheet).getByRole('radio', { name: /Approve/ })).toHaveAttribute('aria-checked', 'true');
    await press('Enter');

    const [submit] = daemon.sentOf('markdown_annotations_submit');
    expect(submit).toMatchObject({ document_uri: DOC_URI, target_session_id: 's1', verdict: 'approve' });
    expect(within(tile).queryByRole('dialog', { name: 'Send the review' })).toBeNull();
    answerSubmit(daemon, submit, draft, true);
    await daemon.idle();
    expect(tile.querySelector('.review-mark')).toBeNull();
  });

  it('clears the sent marks when the review was delivered but its sent ref could not be written', async () => {
    const { tile, press, write, toRow, daemon, draft } = await openReview();
    await toRow(4);
    await press('c');
    await write('Return a + b directly.');
    await press('Enter', { metaKey: true });
    await press('Enter');
    const [submit] = daemon.sentOf('markdown_annotations_submit');
    draft.annotations = [];
    draft.generation += 1;
    daemon.replyTo(submit, {
      event: 'markdown_annotations_submit_result', request_id: submit.request_id, document_uri: submit.document_uri, source_kind: submit.source_kind,
      success: true, status: 'delivered', error: 'delivered; the next review start cannot show what changed since: no repository', ...draft.reply(),
    });
    await daemon.idle();
    expect(tile.querySelector('.review-mark')).toBeNull();
    expect(tile).toHaveTextContent('the next review start cannot show what changed since');
  });

  it('sends a verdict with no notes', async () => {
    const { tile, press, daemon } = await openReview();
    await press('Enter', { metaKey: true });
    expect(within(tile).getByRole('dialog', { name: 'Send the review' })).toHaveTextContent('No notes: the verdict goes alone.');
    await press('Enter');
    expect(daemon.sentOf('markdown_annotations_submit')).toEqual([expect.objectContaining({ verdict: 'approve' })]);
  });

  it('names the send key as Linux binds it, in the key line and the key sheet, and sends on it', async () => {
    onTestFinished(stubNavigatorPlatform('Linux x86_64'));
    const { tile, press, daemon } = await openReview();
    const keyLine = within(tile).getByLabelText('Review keys');
    expect(keyLine).toHaveTextContent('Ctrl+Shift+⏎ send');
    expect(keyLine).not.toHaveTextContent('⌘');
    await press('?');
    expect(within(tile).getByRole('dialog', { name: 'Review keys' })).toHaveTextContent('Ctrl+Shift+⏎Send the review');
    await press('?');
    await press('Enter', { ctrlKey: true, shiftKey: true });
    expect(within(tile).getByRole('dialog', { name: 'Send the review' })).toBeInTheDocument();
    await press('Enter');
    expect(daemon.sentOf('markdown_annotations_submit')).toEqual([expect.objectContaining({ verdict: 'approve' })]);
  });

  it('offers no marks, questions or verdicts on a document without a review bundle', async () => {
    const { tile, press, daemon } = await openReview({ review: null });
    await press('c');
    await press('a');
    await press('Enter', { metaKey: true });
    expect(within(tile).queryByRole('dialog')).toBeNull();
    expect(daemon.sentOf('markdown_annotations_submit')).toEqual([]);
  });
});

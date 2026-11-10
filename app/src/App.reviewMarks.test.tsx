import { act, fireEvent, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openMarkdownTiles } from './test/appFixtures';
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

const row = (kind: string, old: number | undefined, now: number | undefined, text: string): ReviewRow => ({
  kind,
  ...(old !== undefined ? { old } : {}),
  ...(now !== undefined ? { now } : {}),
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
    for (const upsert of upserts) {
      const at = kept.findIndex((mark) => mark.id === upsert.id);
      if (at >= 0) kept[at] = upsert;
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

async function openReview({ review = BUNDLE as ReviewBundle | null } = {}) {
  const draft = new Draft();
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
    },
  });
  const desktopId = view.daemon.arrangement.profile.current_desktop_id;
  const show = async (fields: Record<string, unknown> = {}) => {
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

  it('sends the review on Cmd+Enter with the verdict picked in the sheet and the head the user read', async () => {
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
    expect(submit).toMatchObject({ document_uri: DOC_URI, target_session_id: 's1', verdict: 'approve', head: '9f3a7e2' });
    expect(within(tile).queryByRole('dialog', { name: 'Send the review' })).toBeNull();
    answerSubmit(daemon, submit, draft, true);
    await daemon.idle();
    expect(tile.querySelector('.review-mark')).toBeNull();
  });

  it('sends a verdict with no notes', async () => {
    const { tile, press, daemon } = await openReview();
    await press('Enter', { metaKey: true });
    expect(within(tile).getByRole('dialog', { name: 'Send the review' })).toHaveTextContent('No notes: the verdict goes alone.');
    await press('Enter');
    expect(daemon.sentOf('markdown_annotations_submit')).toEqual([expect.objectContaining({ verdict: 'approve', head: '9f3a7e2' })]);
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

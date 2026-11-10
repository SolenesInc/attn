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

# Explain the guide

The guide says how a review reads.

### docs/guide.md:1-9

- L3: Reworded.
`;

const row = (kind: string, old: number | undefined, now: number | undefined, text: string): ReviewRow => ({
  kind,
  ...(old !== undefined ? { old } : {}),
  ...(now !== undefined ? { now } : {}),
  text,
});

/** docs/guide.md now: a heading, a reworded paragraph, a rewritten one, and a tight list with a reworded item. */
const GUIDE_NOW = [
  '# Reviews',
  '',
  'Press `c` to leave a note on the line.',
  '',
  'Questions reach the agent at once and come back answered.',
  '',
  '- one',
  '- two',
  '- three',
];

function guideRows(now: readonly string[]): ReviewRow[] {
  return [
    row(' ', 1, 1, now[0]),
    row(' ', 2, 2, now[1]),
    row('-', 3, undefined, 'Press `c` to write a note on a line.'),
    row('+', undefined, 3, now[2]),
    row(' ', 4, 4, now[3]),
    row('-', 5, undefined, 'Nothing else is sent until you send the review.'),
    row('+', undefined, 5, now[4]),
    ...now.slice(5).flatMap((text, index) => (text === '- two'
      ? [row('-', 6 + index, undefined, '- to'), row('+', undefined, 6 + index, text)]
      : [row(' ', 6 + index, 6 + index, text)])),
  ];
}

function bundleOf(now: readonly string[], linesAbove = 0): ReviewBundle {
  return {
    frame: 'brief',
    repository: '/repo',
    base: '4be1c0d',
    head: '9f3a7e2',
    stops: [{
      heading_line: 13 + linesAbove, path: 'docs/guide.md', from: 1, to: 9, now_from: 1, now_to: now.length, changed_since_presented: false, file_hash: 'h1',
      rows: guideRows(now),
      notes: [{ line: 15 + linesAbove, from: 3, to: 3, now_from: 3, now_to: 3, look: false, text: 'Reworded.' }],
    }],
    files: [{ path: 'docs/guide.md', status: 'modified', added: 2, removed: 2, binary: false }],
    flows: [],
    not_shown: [],
    navigation: [],
    problems: [],
  };
}

async function openReview({ host }: { host?: string } = {}) {
  let annotations: WireAnnotation[] = [];
  let generation = 1;
  let guide = [...GUIDE_NOW];
  let version = 1;
  const view = await openMarkdownTiles('', {
    path: DOC,
    ...(host ? { host } : {}),
    session: { state: 'idle' },
    script: (daemon) => {
      daemon.on('markdown_annotations_get', ({ document_uri, source_kind }) => ({
        event: 'markdown_annotations_get_result', document_uri, source_kind, success: true, annotations, generation,
      }));
      daemon.on('markdown_annotations_save', (save) => {
        annotations = [...annotations.filter((mark) => !save.removed_ids.includes(mark.id) && !save.upserts.some((upsert) => upsert.id === mark.id)), ...save.upserts];
        generation += 1;
        return { event: 'markdown_annotations_save_result', document_uri: save.document_uri, source_kind: save.source_kind, success: true, annotations, generation };
      });
      daemon.on('review_file', ({ path }) => ({ event: 'review_file_result', success: true, path, rows: guideRows(guide), hash: `h${version}` }));
      daemon.on('review_edit', ({ from, to, text }: CommandMessage<'review_edit'>) => {
        guide = [...guide.slice(0, from - 1), ...text.split('\n'), ...guide.slice(to)];
        version += 1;
        return { event: 'review_edit_result', success: true, conflict: false, hash: `h${version}` };
      });
    },
  });
  const desktopId = view.daemon.arrangement.profile.current_desktop_id;
  const show = async (content = DOCUMENT) => {
    view.daemon.emit({
      event: 'desktop_tile_content', desktop_id: desktopId, tile_id: 'tile-a', tile_kind: 'markdown', path: DOC, content,
      review: bundleOf(guide, content.split('\n').length - DOCUMENT.split('\n').length), ...(host ? { host } : {}),
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
  const cursor = () => tile.querySelector('.review-doc .review-cursor');
  const toBlock = async (line: number) => {
    await press('ArrowRight');
    while (cursor()?.getAttribute('data-line') !== String(line)) await press('ArrowDown');
  };
  return { ...view, tile, show, press, toBlock, cursor, annotations: () => annotations };
}

describe('App review document stops', () => {
  it('renders a markdown stop as a document: a light rewording marks its words, a rewrite strikes the old text above the new', async () => {
    const { tile } = await openReview();
    const blocks = Array.from(tile.querySelectorAll('.review-stop--doc .review-doc-block'));
    expect(blocks.map((block) => [block.getAttribute('data-line'), block.classList.contains('review-doc-block--changed')])).toEqual([
      ['1', false], ['3', true], ['5', true], ['7', false], ['8', true], ['9', false],
    ]);
    expect(blocks[3].querySelector('del')).toBeNull();
    expect(blocks[4].querySelector('.review-doc-was')).toHaveTextContent('to');
    expect(blocks[4].querySelector('.review-doc-was + div')).toHaveTextContent('two');
    const reworded = blocks[1];
    expect(Array.from(reworded.querySelectorAll('ins')).map((word) => word.textContent)).toEqual(['leave', 'the']);
    expect(Array.from(reworded.querySelectorAll('del')).map((word) => word.textContent)).toEqual(['write', 'a']);
    expect(reworded.querySelector('code')).toHaveTextContent('c');
    expect(blocks[0].querySelector('h1')).toHaveTextContent('Reviews');

    const rewritten = blocks[2];
    expect(rewritten.querySelector('.review-doc-was')).toHaveTextContent('Nothing else is sent until you send the review.');
    expect(rewritten.querySelector('.review-doc-was + div')).toHaveTextContent('Questions reach the agent at once and come back answered.');
    expect(tile.querySelector('.review-stop--doc .review-note')).toHaveTextContent('Reworded.');
  });

  it('edits a block as markdown with i and keeps the cursor on it when the stop and the document change', async () => {
    const { tile, press, toBlock, cursor, daemon, annotations, show } = await openReview();
    await toBlock(3);
    await press('i');
    const box = within(tile).getByRole('dialog', { name: 'Edit the file' });
    expect(box).toHaveTextContent('docs/guide.md: this block, as markdown');
    const input = within(box).getByRole('textbox');
    expect(input).toHaveValue('Press `c` to leave a note on the line.');

    fireEvent.change(input, { target: { value: 'Press `c` to leave a note.\nIt waits for the send.' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(within(tile).getByRole('dialog', { name: 'Edit the file' })).toBeInTheDocument();
    fireEvent.keyDown(input, { key: 'Enter', metaKey: true });
    await press('Shift');
    expect(daemon.sentOf('review_edit')).toEqual([expect.objectContaining({ path: 'docs/guide.md', base_hash: 'h1', from: 3, to: 3, text: 'Press `c` to leave a note.\nIt waits for the send.' })]);
    expect(annotations()).toEqual([expect.objectContaining({
      type: 'edit', edit_was: 'Press `c` to leave a note on the line.', edit_now: 'Press `c` to leave a note.\nIt waits for the send.',
      anchor: expect.objectContaining({ file: 'docs/guide.md', file_start_line: 3, file_end_line: 4 }),
    })]);

    await show(DOCUMENT.replace('The guide says how a review reads.', 'The guide says how a review reads.\n\nIt changed today.'));
    expect(tile).toHaveTextContent('It changed today.');
    expect(cursor()?.getAttribute('data-line')).toBe('3');
    expect(cursor()).toHaveTextContent('It waits for the send.');
    expect(tile.querySelector('.review-mark--edit')).toHaveTextContent('you editedL3–4');
  });

  it('writes a note on a whole block', async () => {
    const { tile, press, toBlock, annotations } = await openReview();
    await toBlock(5);
    await press('c');
    const input = within(within(tile).getByRole('dialog', { name: 'Write a note' })).getByRole('textbox');
    fireEvent.change(input, { target: { value: 'Say when.' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await press('Shift');
    expect(annotations()).toEqual([expect.objectContaining({
      type: 'comment', text: 'Say when.', anchor: expect.objectContaining({ file: 'docs/guide.md', file_start_line: 5, file_end_line: 5, start_line: 13 }),
    })]);
  });

  it('opens a document on this machine in the editor tile, rooted at the repository', async () => {
    const { press, toBlock, daemon } = await openReview();
    await toBlock(5);
    await press('o');
    expect(daemon.sentOf('desktop_dock_tile')).toEqual([expect.objectContaining({
      tile_kind: 'notebook', tile_params: JSON.stringify({ root: '/repo', path: 'docs/guide.md' }),
    })]);
  });

  it('offers no editor tile for a document on an outpost', async () => {
    const { tile, press, toBlock, daemon } = await openReview({ host: 'outpost-1' });
    expect(tile.querySelector('.review-stop--doc')?.textContent).not.toContain('editor');
    await toBlock(5);
    await press('o');
    expect(daemon.sentOf('desktop_dock_tile')).toEqual([]);
  });
});

import { act, fireEvent, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openMarkdownTiles } from './test/appFixtures';
import type { EventMessage } from './test/protocol';
import type { ReviewBundle, ReviewRow } from './types/generated';

type WireAnnotation = EventMessage<'markdown_annotations_get_result'>['annotations'][number];

const DOC = '/notes/review.md';

const DOCUMENT = `---
attn: review
frame: map
repository: /repo
base: 4be1c0d
head: 9f3a7e2
---

# Name the sum

Add used to return its sum directly.

## Add keeps its result

Add names the sum before it returns it.

### calc.go:3-6

- L4!: The sum gets a name.

## The helper joins

The helper is new, and only Add calls it.

### util.go:1-2

- L2: The helper.

## The map

\`\`\`mermaid after
sequenceDiagram
  participant poll as Poll<br/>poll.go
  participant calc as Calc<br/>calc.go
  participant util as Util
  Note over poll,util: Add now calls the helper.
  poll->>calc: Add(a, b)
  %% stop: calc.go:3-6
  Note over poll,calc: names the sum
  critical inside Add
    calc->>util: helper()
    %% stop: util.go:1-2
    Note right of util: new
  end
  util--xcalc: no answer
\`\`\`

## Not shown

- go.sum: checksums only.
`;

const lineOf = (content: string, text: string) => content.split('\n').findIndex((line) => line.includes(text)) + 1;

const row = (kind: string, old: number | undefined, now: number | undefined, text: string): ReviewRow => ({
  kind, ...(old !== undefined ? { old } : {}), ...(now !== undefined ? { now, head: now } : {}), text,
});

/** The bundle the daemon reads from a version of the document. */
function bundleOf(content: string): ReviewBundle {
  const at = (text: string) => lineOf(content, text);
  const participant = (id: string, label: string) => ({ id, label, actor: false });
  return {
    frame: 'map',
    repository: '/repo',
    base: '4be1c0d',
    head: '9f3a7e2',
    stops: [
      {
        heading_line: at('### calc.go'), path: 'calc.go', from: 3, to: 6, now_from: 3, now_to: 6, changed_since_presented: false, file_hash: 'h1',
        rows: [row(' ', 3, 3, 'func Add(a, b int) int {'), row('-', 4, undefined, '\treturn a + b'), row('+', undefined, 4, '\tsum := a + b'), row('+', undefined, 5, '\treturn sum'), row(' ', 5, 6, '}')],
        notes: [{ line: at('- L4!'), from: 4, to: 4, now_from: 4, now_to: 4, look: true, text: 'The sum gets a name.' }],
      },
      {
        heading_line: at('### util.go'), path: 'util.go', from: 1, to: 2, now_from: 1, now_to: 2, changed_since_presented: false, file_hash: 'h2',
        rows: [row(' ', 1, 1, 'package calc'), row('+', undefined, 2, 'func helper() {}')],
        notes: [{ line: at('- L2:'), from: 2, to: 2, now_from: 2, now_to: 2, look: false, text: 'The helper.' }],
      },
    ],
    files: [
      { path: 'calc.go', status: 'modified', added: 2, removed: 1, binary: false },
      { path: 'util.go', status: 'modified', added: 1, removed: 0, binary: false },
      { path: 'go.sum', status: 'modified', added: 1, removed: 1, binary: false },
    ],
    flows: [{
      line: at('```mermaid after'), side: 'after', drawn: true,
      participants: [participant('poll', 'Poll<br/>poll.go'), participant('calc', 'Calc<br/>calc.go'), participant('util', 'Util')],
      note: 'Add now calls the helper.',
      messages: [
        { line: at('poll->>calc'), from: 'poll', to: 'calc', arrow: '->>', text: 'Add(a, b)', stop_line: at('### calc.go'), note: 'names the sum' },
        { line: at('calc->>util'), from: 'calc', to: 'util', arrow: '->>', text: 'helper()', stop_line: at('### util.go'), note: 'new' },
        { line: at('util--xcalc'), from: 'util', to: 'calc', arrow: '--x', text: 'no answer' },
      ],
      groups: [{ line: at('critical inside Add'), kind: 'critical', label: 'inside Add', first_message: at('calc->>util'), last_message: at('calc->>util') }],
    }],
    not_shown: [{ line: at('- go.sum'), path: 'go.sum', reason: 'checksums only.' }],
    navigation: [],
    problems: [],
  };
}

async function openMap() {
  let draft: WireAnnotation[] = [];
  let generation = 1;
  const view = await openMarkdownTiles('', {
    path: DOC,
    session: { state: 'idle' },
    script: (daemon) => {
      daemon.on('markdown_annotations_get', ({ document_uri, source_kind }) => ({
        event: 'markdown_annotations_get_result', document_uri, source_kind, success: true, annotations: draft, generation,
      }));
      daemon.on('markdown_annotations_save', (save) => {
        draft = [...draft.filter((mark) => !save.removed_ids.includes(mark.id) && !save.upserts.some((upsert) => upsert.id === mark.id)), ...save.upserts];
        generation += 1;
        return { event: 'markdown_annotations_save_result', document_uri: save.document_uri, source_kind: save.source_kind, success: true, annotations: draft, generation };
      });
      daemon.on('review_file', ({ path }) => ({ event: 'review_file_result', success: true, path, rows: [row(' ', 1, 1, `// ${path}`)], hash: `h-${path}` }));
    },
  });
  const show = async (content: string, review = bundleOf(content)) => {
    view.daemon.emit({ event: 'desktop_tile_content', desktop_id: view.daemon.arrangement.profile.current_desktop_id, tile_id: 'tile-a', tile_kind: 'markdown', path: DOC, content, review });
    await view.daemon.idle();
  };
  await show(DOCUMENT);
  const tile = view.tile();
  tile.querySelector<HTMLElement>('.desktop-dock-tile-body')!.focus();
  const settle = async () => {
    await act(() => vi.advanceTimersByTimeAsync(500));
    await view.daemon.idle();
  };
  const press = async (key: string) => {
    fireEvent.keyDown(tile.ownerDocument.activeElement ?? tile, { key });
    await settle();
  };
  const write = async (text: string) => {
    const input = within(tile).getByRole('textbox');
    fireEvent.change(input, { target: { value: text } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await settle();
  };
  return { ...view, tile, show, press, write, draft: () => draft };
}

const arrowOn = (svg: HTMLElement, source: string) => svg.querySelector(`[data-review-message="${lineOf(DOCUMENT, source)}"]`);
const diagram = (tile: HTMLElement) => tile.querySelector<HTMLElement>('.review-map-diagrams')!;
const arrowLine = (tile: HTMLElement) => tile.querySelector<HTMLElement>('.review-map-message');
const detail = (tile: HTMLElement) => tile.querySelector<HTMLElement>('.review-frame--map > .review-doc')!;
/** The document blocks the Map's detail pane shows, by their text. */
const shownBlocks = (tile: HTMLElement) => Array.from(detail(tile).querySelector('.md-reader-card')!.children)
  .filter((block) => !block.hasAttribute('data-review-outside'))
  .map((block) => (block.hasAttribute('data-review-stop-head') ? `stop ${block.getAttribute('data-path')}` : block.textContent));
const cursor = (tile: HTMLElement) => tile.querySelector<HTMLElement>('.review-doc .review-cursor');
const prose = (tile: HTMLElement, text: string) => Array.from(tile.querySelectorAll<HTMLElement>('.review-doc p')).find((paragraph) => paragraph.textContent === text);

describe('App review map', () => {
  it('tells the story beside the diagram: captions, groups and the step that holds the arrow’s stop', async () => {
    const { tile, press } = await openMap();

    const svg = diagram(tile);
    expect(arrowOn(svg, 'poll->>calc')?.querySelector('.review-seq-caption')).toHaveTextContent('names the sum');
    expect(arrowOn(svg, 'calc->>util')?.querySelector('.review-seq-caption')).toHaveTextContent('new');
    expect(svg.querySelector(`[data-review-group="${lineOf(DOCUMENT, 'critical inside')}"]`)).toHaveTextContent('critical inside Add');
    expect(Array.from(svg.querySelectorAll('.review-seq-participant')).map((participant) => participant.textContent)).toEqual(['Pollpoll.go', 'Calccalc.go', 'Util']);
    expect(arrowOn(svg, 'util--xcalc')).toHaveClass('review-seq-message--lost');
    expect(svg.querySelector('figcaption')).toHaveTextContent('Add now calls the helper.');
    expect(within(svg.querySelector<HTMLElement>('.review-map-not-shown')!).getByText('checksums only.')).toBeInTheDocument();

    expect(arrowLine(tile)).toHaveTextContent('Arrow 1 of 2Add(a, b)Poll → Calc');
    expect(shownBlocks(tile)).toEqual(['Add keeps its result', 'Add names the sum before it returns it.', 'stop calc.go']);
    expect(cursor(tile)?.closest('.review-stop')).toHaveAttribute('data-path', 'calc.go');

    await press('ArrowRight');
    expect(arrowLine(tile)).toHaveTextContent('Arrow 2 of 2helper()Calc → Util');
    expect(shownBlocks(tile)).toEqual(['The helper joins', 'The helper is new, and only Add calls it.', 'stop util.go']);
    expect(cursor(tile)?.closest('.review-stop')).toHaveAttribute('data-path', 'util.go');

    while (cursor(tile) !== prose(tile, 'The helper is new, and only Add calls it.')) await press('ArrowUp');
    await press('ArrowUp');
    await press('ArrowUp');
    expect(cursor(tile)?.textContent).toBe('The helper joins');
    await press('ArrowDown');
    await press('ArrowDown');
    await press('ArrowDown');
    await press('ArrowDown');
    await press('ArrowDown');
    expect(cursor(tile)?.closest('.review-stop')).toHaveAttribute('data-path', 'util.go');
  });

  it('keeps a note written on the step’s prose in the Map on that prose in the Brief', async () => {
    const { tile, press, write, draft } = await openMap();
    await press('ArrowRight');
    while (cursor(tile) !== prose(tile, 'The helper is new, and only Add calls it.')) await press('ArrowUp');

    await press('c');
    await write('Why only Add?');
    expect(draft()).toEqual([expect.objectContaining({ type: 'comment', text: 'Why only Add?', anchor: expect.objectContaining({ exact: 'The helper is new, and only Add calls it.' }) })]);
    expect(prose(tile, 'The helper is new, and only Add calls it.')?.querySelector('[data-md-mark]')).toHaveTextContent('The helper is new, and only Add calls it.');

    await press('1');
    expect(tile.querySelector('[data-review-outside]')).toBeNull();
    expect(prose(tile, 'The helper is new, and only Add calls it.')?.querySelector('[data-md-mark]')).toHaveTextContent('The helper is new, and only Add calls it.');
    expect(prose(tile, 'Add names the sum before it returns it.')?.querySelector('[data-md-mark]')).toBeNull();
  });

  it('draws a section’s divider above a block that opens on the section’s first arrow', async () => {
    const content = DOCUMENT.replace('  critical inside Add\n', '  alt the sum is new\n').replace('    Note right of util: new\n  end\n', '    Note right of util: new\n  else the sum is known\n    loop each retry\n      calc->>util: helper again\n    end\n  end\n');
    const { tile, show } = await openMap();
    const at = (text: string) => lineOf(content, text);
    const review = bundleOf(content);
    const [flow] = review.flows;
    flow.messages.splice(2, 0, { line: at('calc->>util: helper again'), from: 'calc', to: 'util', arrow: '->>', text: 'helper again' });
    flow.groups = [
      { line: at('alt the sum'), kind: 'alt', label: 'the sum is new', first_message: at('calc->>util: helper()'), last_message: at('helper again') },
      { line: at('else the sum'), kind: 'else', label: 'the sum is known', first_message: at('helper again'), last_message: at('helper again'), block: at('alt the sum') },
      { line: at('loop each retry'), kind: 'loop', label: 'each retry', first_message: at('helper again'), last_message: at('helper again') },
    ];
    await show(content, review);

    const svg = diagram(tile);
    const divider = Number(svg.querySelector(`[data-review-group="${at('else the sum')}"] line`)?.getAttribute('y1'));
    const loop = Number(svg.querySelector(`[data-review-group="${at('loop each retry')}"] rect`)?.getAttribute('y'));
    const alt = Number(svg.querySelector(`[data-review-group="${at('alt the sum')}"] rect`)?.getAttribute('y'));
    expect(alt).toBeLessThan(divider);
    expect(divider).toBeLessThan(loop);
  });

  it('keeps the Map on its arrow and step when the document changes above it', async () => {
    const { tile, press, show } = await openMap();
    await press('ArrowRight');
    expect(arrowLine(tile)).toHaveTextContent('Arrow 2 of 2');

    await show(DOCUMENT.replace('Add used to return its sum directly.', 'Add used to return its sum directly.\n\nIt reads better with a name.'));
    expect(arrowLine(tile)).toHaveTextContent('Arrow 2 of 2helper()');
    expect(shownBlocks(tile)).toEqual(['The helper joins', 'The helper is new, and only Add calls it.', 'stop util.go']);
    expect(cursor(tile)?.closest('.review-stop')).toHaveAttribute('data-path', 'util.go');
  });
});

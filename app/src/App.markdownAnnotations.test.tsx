import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, onTestFinished, vi } from 'vitest';
import { openMarkdownTiles, openTiles } from './test/appFixtures';
import { daemonSeed, seedDocument } from './test/daemonFixtures';
import type { CommandMessage, EventMessage } from './test/protocol';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const SEED_URI = 'attn://seed/s-7k3f9m';

const PLAN = daemonSeed('s-7k3f9m', { title: 'parser plan', body: '# Plan\n\nShip the parser fix.', status: 'planted' });

function serveSeedPlan(daemon: ScriptedDaemon) {
  daemon.on('seed_document_get', () => ({
    event: 'seed_document_get_result',
    success: true,
    document: seedDocument(PLAN),
  }));
  daemon.on('markdown_annotations_get', ({ document_uri, source_kind }) => ({
    event: 'markdown_annotations_get_result',
    document_uri,
    source_kind,
    success: true,
    annotations: [],
    generation: 5,
  }));
}

async function openSeedPlan() {
  return openTiles([{ tile_id: 'tile-seed', tile_kind: 'seed', tile_params: PLAN.id }], { initialState: { seeds: [PLAN] }, script: serveSeedPlan });
}

async function addOverallNote(daemon: ScriptedDaemon, text: string) {
  const tile = within(document.querySelector<HTMLElement>('[data-pane-id="tile-seed"]')!);
  fireEvent.click(tile.getByRole('button', { name: 'Overall note' }));
  fireEvent.change(screen.getByPlaceholderText('Add an overall note...'), { target: { value: text } });
  fireEvent.click(screen.getByRole('button', { name: 'Add' }));
  await act(() => vi.advanceTimersByTimeAsync(500));
  await daemon.idle();
}

function answerSave(daemon: ScriptedDaemon, save: CommandMessage<'markdown_annotations_save'>, outcome: { success: boolean; stale?: boolean }) {
  daemon.emit({
    event: 'markdown_annotations_save_result',
    request_id: save.request_id,
    document_uri: save.document_uri,
    source_kind: save.source_kind,
    generation: save.generation,
    ...outcome,
  });
}

type WireAnnotation = EventMessage<'markdown_annotations_get_result'>['annotations'][number];

const DOC = '# Plan\n\nFirst paragraph with target words inside it.\n\nSecond block of plain prose here.\n\n```js\nconst a = 1;\n```\n';
const DOC_HASH = '4ed154d5';

const TARGET_WORDS = { block_id: 'b1-paragraph', start_line: 3, end_line: 3, exact: 'target words', prefix: 'First paragraph with ', suffix: ' inside it.', start: 21, end: 33, content_hash: DOC_HASH };
const PLAIN_PROSE = { block_id: 'b2-paragraph', start_line: 5, end_line: 5, exact: 'plain prose', prefix: 'Second block of ', suffix: ' here.', start: 16, end: 27, content_hash: DOC_HASH };

function stored(id: string, anchor: typeof TARGET_WORDS | null, fields: Partial<WireAnnotation> = {}): WireAnnotation {
  return { id, type: 'comment', text: `${id} note`, created_at: 1, ...(anchor ? { anchor } : {}), ...fields } as WireAnnotation;
}

let documentCount = 0;

interface DocumentOptions {
  content?: string;
  annotations?: WireAnnotation[];
  generation?: number;
  tiles?: string[];
  workspaceId?: string;
  path?: string;
  getReply?: 'answer' | 'hold' | 'fail';
}

async function openDocument({
  content = DOC,
  annotations = [],
  generation = 5,
  tiles = ['tile-a'],
  workspaceId = 'ws',
  path = `/tmp/doc-${++documentCount}.md`,
  getReply = 'answer',
}: DocumentOptions = {}) {
  const view = await openMarkdownTiles(content, {
    path,
    tileIds: tiles,
    workspace: { id: workspaceId },
    session: { state: 'idle' },
    script: (daemon) => {
      daemon.on('markdown_annotations_get', ({ document_uri, source_kind }) => (
        getReply === 'hold'
          ? undefined
          : getReply === 'fail'
            ? { event: 'markdown_annotations_get_result', document_uri, source_kind, success: false, error: 'daemon busy', annotations: [], generation: 0 }
            : { event: 'markdown_annotations_get_result', document_uri, source_kind, success: true, annotations, generation }
      ));
      daemon.on('markdown_annotations_save', ({ document_uri, source_kind, generation: saved }) => ({
        event: 'markdown_annotations_save_result', document_uri, source_kind, success: true, generation: saved,
      }));
      daemon.on('markdown_annotations_clear', ({ document_uri, source_kind, generation: cleared }) => ({
        event: 'markdown_annotations_clear_result', document_uri, source_kind, success: true, generation: cleared,
      }));
    },
  });
  const reader = (tileId = tiles[0]) => view.tile(tileId).querySelector<HTMLElement>('.md-reader')!;
  return { ...view, reader, path };
}

function textPoint(scope: Element, needle: string, at: 'start' | 'end' = 'start') {
  const walker = document.createTreeWalker(scope, NodeFilter.SHOW_TEXT);
  let node: Node | null;
  while ((node = walker.nextNode())) {
    const index = (node as Text).data.indexOf(needle);
    if (index >= 0) return { node, offset: at === 'start' ? index : index + needle.length };
  }
  throw new Error(`no text ${JSON.stringify(needle)}`);
}

async function selectRange(daemon: ScriptedDaemon, start: { node: Node; offset: number }, end: { node: Node; offset: number }) {
  const range = document.createRange();
  range.setStart(start.node, start.offset);
  range.setEnd(end.node, end.offset);
  const selection = window.getSelection()!;
  selection.removeAllRanges();
  selection.addRange(range);
  fireEvent.mouseUp(start.node.nodeType === Node.TEXT_NODE ? start.node.parentElement! : start.node);
  await daemon.idle();
}

async function select(daemon: ScriptedDaemon, scope: Element, needle: string) {
  await selectRange(daemon, textPoint(scope, needle), textPoint(scope, needle, 'end'));
}

async function settleSaves(daemon: ScriptedDaemon) {
  await act(() => vi.advanceTimersByTimeAsync(500));
  await daemon.idle();
}

const toolbar = () => document.querySelector<HTMLElement>('.md-selection-toolbar');
const toolbarButton = (title: string) => within(toolbar()!).getByTitle(title);
const composer = () => document.querySelector<HTMLTextAreaElement>('.md-annotation-popover textarea');
const picker = () => document.querySelector<HTMLElement>('.md-quick-label-picker');
const pickerRows = () => Array.from(picker()!.querySelectorAll<HTMLElement>('.md-quick-label-row'));
const sidebar = () => document.querySelector<HTMLElement>('.md-annotations-sidebar');
const cards = () => Array.from(sidebar()!.querySelectorAll<HTMLElement>('.md-annotation-card'));
const saves = (daemon: ScriptedDaemon) => daemon.sentOf('markdown_annotations_save');
const lastSaved = (daemon: ScriptedDaemon) => saves(daemon).slice(-1)[0]?.annotations ?? [];
const paintedMarks = (scope: Element) =>
  Array.from(scope.querySelectorAll<HTMLElement>('[data-md-mark]'))
    .filter((mark) => !mark.dataset.mdMark!.startsWith('md-'))
    .map((mark) => mark.textContent);
const provisionalMark = (scope: Element) => scope.querySelector('[data-md-mark="md-pending-selection"]');

async function openInspector(tile: HTMLElement) {
  fireEvent.click(within(tile).getByRole('button', { name: /^Notes \d+$/ }));
}

async function typeComment(daemon: ScriptedDaemon, key: string) {
  fireEvent.keyDown(window, { key });
  await act(() => vi.advanceTimersByTimeAsync(0));
  await daemon.idle();
}

describe('App markdown annotations', () => {
  it('saves a seed document’s marks under the seed’s identity, and reads them again when the save was stale', async () => {
    const { daemon } = await openSeedPlan();
    expect(daemon.sentOf('markdown_annotations_get')).toEqual([
      expect.objectContaining({ document_uri: SEED_URI, source_kind: 'seed', seed_id: PLAN.id }),
    ]);

    await addOverallNote(daemon, 'Split the fix from the refactor.');
    const [save] = daemon.sentOf('markdown_annotations_save');
    expect(save).toMatchObject({
      document_uri: SEED_URI,
      source_kind: 'seed',
      seed_id: PLAN.id,
      generation: 6,
      annotations: [expect.objectContaining({ type: 'global', text: 'Split the fix from the refactor.' })],
    });

    answerSave(daemon, save, { success: false, stale: true });
    await daemon.idle();

    expect(daemon.sentOf('markdown_annotations_get')).toHaveLength(2);
  });

  it('lets a newer save stand when the answer to the one it overtook arrives late', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { daemon } = await openSeedPlan();

    await addOverallNote(daemon, 'First overall note');
    await addOverallNote(daemon, 'Second overall note');
    const [first, second] = daemon.sentOf('markdown_annotations_save');
    expect(first.request_id).not.toBe(second.request_id);

    answerSave(daemon, first, { success: false, stale: true });
    answerSave(daemon, second, { success: true });
    await daemon.idle();

    expect(daemon.sentOf('markdown_annotations_get')).toHaveLength(1);
  });

  it('reads a document’s marks once while two tiles show it', async () => {
    const { daemon } = await openMarkdownTiles('# Doc\n\nBody.', { path: '/tmp/doc.md', tileIds: ['tile-a', 'tile-b'] });

    expect(daemon.sentOf('markdown_annotations_get')).toEqual([
      expect.objectContaining({ document_uri: 'attn://file/ws/%2Ftmp%2Fdoc.md', source_kind: 'file' }),
    ]);
  });

  it('escapes the workspace and path in a file’s document identity', async () => {
    const { daemon } = await openDocument({ workspaceId: 'remote/ws:1', path: '/tmp/a plan.md' });

    expect(daemon.sentOf('markdown_annotations_get')).toEqual([
      expect.objectContaining({ document_uri: 'attn://file/remote%2Fws%3A1/%2Ftmp%2Fa%20plan.md', workspace_id: 'remote/ws:1', path: '/tmp/a plan.md' }),
    ]);
  });
});

describe('App markdown annotations selection', () => {
  it('offers the toolbar over a selection, and Delete saves a deletion of exactly that text with no composer', async () => {
    const { daemon, reader } = await openDocument();

    await select(daemon, reader(), 'target words');

    expect(provisionalMark(reader())).toHaveTextContent('target words');
    expect(Array.from(toolbar()!.querySelectorAll('button'), (button) => button.title)).toEqual([
      'Delete',
      'Comment',
      'Quick label',
      'I agree',
      'This is wrong',
      'Clarify this',
      'Cancel',
    ]);

    fireEvent.click(toolbarButton('Delete'));
    await settleSaves(daemon);

    expect(lastSaved(daemon)).toEqual([expect.objectContaining({ type: 'deletion', anchor: TARGET_WORDS })]);
    expect(composer()).toBeNull();
    expect(toolbar()).toBeNull();
    expect(provisionalMark(reader())).toBeNull();
    expect(paintedMarks(reader())).toEqual(['target words']);
  });

  it.each([
    { closing: 'Escape', close: () => fireEvent.keyDown(window, { key: 'Escape' }) },
    { closing: 'Cancel', close: () => fireEvent.click(toolbarButton('Cancel')) },
    { closing: 'a press outside', close: () => fireEvent.pointerDown(document.body) },
  ])('closes the toolbar and its provisional mark on $closing', async ({ close }) => {
    const { daemon, reader } = await openDocument();
    await select(daemon, reader(), 'target words');
    fireEvent.pointerDown(toolbar()!);
    expect(toolbar()).toBeInTheDocument();

    close();
    await daemon.idle();

    expect(toolbar()).toBeNull();
    expect(provisionalMark(reader())).toBeNull();
  });

  it('closes the toolbar once the selection scrolls out of view', async () => {
    const { daemon, reader } = await openDocument();
    await select(daemon, reader(), 'target words');

    const offscreen = vi.spyOn(Range.prototype, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, window.innerHeight + 10, 40, 16));
    onTestFinished(() => offscreen.mockRestore());
    fireEvent.scroll(window);
    await daemon.idle();

    expect(toolbar()).toBeNull();
  });

  it('trims whitespace off a selection’s edges and keeps a selection across blocks to its first block', async () => {
    const { daemon, reader } = await openDocument();

    await select(daemon, reader(), ' target words ');
    fireEvent.click(toolbarButton('Delete'));
    await selectRange(daemon, textPoint(reader(), 'inside it.'), textPoint(reader(), 'Second block', 'end'));
    fireEvent.click(toolbarButton('Delete'));
    await settleSaves(daemon);

    expect(lastSaved(daemon).map((annotation) => annotation.anchor?.exact)).toEqual(['target words', 'inside it.']);
  });

  it.each([
    { selection: 'a collapsed selection', pick: (reader: HTMLElement) => [textPoint(reader, 'target'), textPoint(reader, 'target')] },
    { selection: 'only whitespace', pick: (reader: HTMLElement) => [textPoint(reader, ' target'), { ...textPoint(reader, ' target'), offset: textPoint(reader, ' target').offset + 1 }] },
    { selection: 'a selection ending on the copy button', pick: (reader: HTMLElement) => [textPoint(reader, 'const'), { node: reader.querySelector('button[title="Copy code"]')!, offset: 0 }] },
    { selection: 'a selection ending outside the reader', pick: (reader: HTMLElement) => [textPoint(reader, 'target'), textPoint(reader.closest('.workspace-dock-tile')!.querySelector('.workspace-dock-tile-title')!, 'Plan', 'end')] },
  ])('offers nothing for $selection', async ({ pick }) => {
    const { daemon, reader } = await openDocument();
    const [start, end] = pick(reader());

    await selectRange(daemon, start, end);

    expect(toolbar()).toBeNull();
    expect(provisionalMark(reader())).toBeNull();
  });

  it('saves the ranged text of a selection in a fence', async () => {
    const { daemon, reader } = await openDocument();

    await select(daemon, reader().querySelector('pre')!, 'const');
    fireEvent.click(toolbarButton('Delete'));
    await settleSaves(daemon);

    expect(lastSaved(daemon)).toEqual([expect.objectContaining({ type: 'deletion', anchor: expect.objectContaining({ exact: 'const', start: 0 }) })]);
  });

  it('offers the toolbar over a hovered code block, and Delete marks the whole block', async () => {
    const { daemon, reader } = await openDocument();

    fireEvent.pointerOver(reader().querySelector('.md-codeblock pre')!);
    fireEvent.click(toolbarButton('Delete'));
    await settleSaves(daemon);

    expect(lastSaved(daemon)).toEqual([expect.objectContaining({ type: 'deletion', anchor: expect.objectContaining({ exact: 'const a = 1;', start: 0 }) })]);
  });
});

describe('App markdown annotations labels', () => {
  it('lists the nine picker labels in order with their digits, and saves the one clicked', async () => {
    const { daemon, reader } = await openDocument();
    await select(daemon, reader(), 'target words');

    fireEvent.click(toolbarButton('Quick label'));
    await daemon.idle();
    expect(pickerRows().map((row) => row.textContent)).toEqual([
      '💯Exactly this1',
      '😕I don\'t love this2',
      '🔍Verify this3',
      '🧾Show the receipt4',
      '🔬Give me an example5',
      '🔄Consider alternatives6',
      '🪓Cut this7',
      '🪙Your call8',
      '🙋Ask me first9',
    ]);

    fireEvent.click(pickerRows()[1]);
    await settleSaves(daemon);

    expect(lastSaved(daemon)).toEqual([expect.objectContaining({ type: 'comment', quick_label_id: 'dont-love-this', anchor: TARGET_WORDS })]);
    expect(lastSaved(daemon)[0]).not.toHaveProperty('text');
    expect(picker()).toBeNull();
  });

  it('keeps the picker open through the click that opened it, and closes it on the next press outside or Escape', async () => {
    const { daemon, reader } = await openDocument();
    await select(daemon, reader(), 'target words');

    fireEvent.click(toolbarButton('Quick label'));
    await daemon.idle();
    fireEvent.pointerDown(document.body);
    expect(toolbar()).toBeInTheDocument();
    expect(picker()).toBeNull();

    fireEvent.click(toolbarButton('Quick label'));
    await daemon.idle();
    fireEvent.keyDown(window, { key: 'Escape' });
    await settleSaves(daemon);

    expect(picker()).toBeNull();
    expect(toolbar()).toBeInTheDocument();
    expect(saves(daemon)).toEqual([]);
  });

  it.each([
    { keys: 'the digit 3 in the picker', open: true, press: { code: 'Digit3', key: '3' }, saved: 'verify-this' },
    { keys: 'Alt+3 in the picker', open: true, press: { code: 'Digit3', key: '3', altKey: true }, saved: 'verify-this' },
    { keys: 'the digit 0 in the picker', open: true, press: { code: 'Digit0', key: '0' }, saved: null },
    { keys: 'Cmd+3 in the picker', open: true, press: { code: 'Digit3', key: '3', metaKey: true }, saved: null },
    { keys: 'Alt+2 on the toolbar', open: false, press: { code: 'Digit2', key: '2', altKey: true }, saved: 'dont-love-this' },
    { keys: 'Alt+0 on the toolbar', open: false, press: { code: 'Digit0', key: '0', altKey: true }, saved: null },
  ])('labels the selection by $keys', async ({ open, press, saved }) => {
    const { daemon, reader } = await openDocument();
    await select(daemon, reader(), 'target words');
    if (open) {
      fireEvent.click(toolbarButton('Quick label'));
      await daemon.idle();
    }

    fireEvent.keyDown(window, press);
    await settleSaves(daemon);

    expect(lastSaved(daemon).map((annotation) => annotation.quick_label_id)).toEqual(saved ? [saved] : []);
  });
});

describe('App markdown annotations composer', () => {
  it.each([
    { key: 'h', opens: true },
    { key: 'h', init: { isComposing: true }, opens: false },
    { key: 'h', init: { ctrlKey: true }, opens: false },
    { key: 'h', init: { metaKey: true }, opens: false },
    { key: 'h', init: { altKey: true }, opens: false },
    { key: 'Tab', opens: false },
    { key: 'Enter', opens: false },
    { key: 'ArrowLeft', opens: false },
  ])('starts a comment on $key $init only for a plain printable key', async ({ key, init, opens }) => {
    const { daemon, reader } = await openDocument();
    await select(daemon, reader(), 'target words');

    fireEvent.keyDown(window, { key, ...init });
    await act(() => vi.advanceTimersByTimeAsync(0));

    expect(composer()?.value ?? null).toBe(opens ? key : null);
  });

  it('leaves typing in a field or in the open picker alone', async () => {
    const { daemon, reader } = await openDocument();
    await select(daemon, reader(), 'target words');

    fireEvent.keyDown(screen.getByRole('textbox', { name: 'Terminal input' }), { key: 'h' });
    fireEvent.click(toolbarButton('Quick label'));
    await daemon.idle();
    fireEvent.keyDown(window, { key: 'h' });
    await act(() => vi.advanceTimersByTimeAsync(0));

    expect(composer()).toBeNull();
  });

  it.each([
    { chord: 'Cmd+Enter', init: { metaKey: true } },
    { chord: 'Ctrl+Enter', init: { ctrlKey: true } },
  ])('saves a comment typed over a selection on $chord, quoting the selection', async ({ init }) => {
    const { daemon, reader } = await openDocument();
    await select(daemon, reader(), 'target words');

    await typeComment(daemon, 'h');
    expect(toolbar()).toBeNull();
    expect(document.querySelector('.md-annotation-popover')).toHaveTextContent('"target words"');
    fireEvent.change(composer()!, { target: { value: 'hmm, rephrase this' } });
    fireEvent.keyDown(composer()!, { key: 'Enter', ...init });
    await settleSaves(daemon);

    expect(lastSaved(daemon)).toEqual([expect.objectContaining({ type: 'comment', text: 'hmm, rephrase this', anchor: TARGET_WORDS })]);
    expect(composer()).toBeNull();
  });

  it('refuses to save an empty comment, and Escape discards the composer', async () => {
    const { daemon, reader } = await openDocument();
    await select(daemon, reader(), 'target words');
    await typeComment(daemon, 'h');

    fireEvent.change(composer()!, { target: { value: '   ' } });
    fireEvent.keyDown(composer()!, { key: 'Enter', metaKey: true });
    await settleSaves(daemon);
    expect(composer()).toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'Escape' });
    await settleSaves(daemon);
    expect(composer()).toBeNull();
    expect(saves(daemon)).toEqual([]);
  });

  it('closes an empty composer on a press outside, but keeps one with text through presses inside and clicks in the document', async () => {
    const { daemon, reader } = await openDocument();
    await select(daemon, reader(), 'target words');
    await typeComment(daemon, 'x');

    fireEvent.pointerDown(composer()!);
    fireEvent.pointerDown(document.body);
    fireEvent.mouseUp(textPoint(reader(), 'plain prose').node.parentElement!);
    await daemon.idle();
    expect(composer()).toHaveValue('x');
    fireEvent.keyDown(composer()!, { key: 'Enter', metaKey: true });
    await settleSaves(daemon);
    expect(lastSaved(daemon)).toEqual([expect.objectContaining({ text: 'x', anchor: TARGET_WORDS })]);

    await select(daemon, reader(), 'plain prose');
    fireEvent.click(toolbarButton('Comment'));
    await act(() => vi.advanceTimersByTimeAsync(0));
    fireEvent.pointerDown(document.body);
    expect(composer()).toBeNull();
  });

  it('keeps a draft for its own selection across closing and reopening, and not for another selection', async () => {
    const { daemon, reader } = await openDocument();
    const reopen = async (needle: string) => {
      await select(daemon, reader(), needle);
      fireEvent.click(toolbarButton('Comment'));
      await act(() => vi.advanceTimersByTimeAsync(0));
    };
    await reopen('target words');
    fireEvent.change(composer()!, { target: { value: 'draft for target' } });
    fireEvent.keyDown(window, { key: 'Escape' });

    await reopen('plain prose');
    expect(composer()).toHaveValue('');
    fireEvent.keyDown(window, { key: 'Escape' });

    await reopen('target words');
    expect(composer()).toHaveValue('draft for target');
    fireEvent.keyDown(composer()!, { key: 'Enter', metaKey: true });

    await reopen('target words');
    expect(composer()).toHaveValue('');
  });

  it('dismisses the composer before the inspector on Escape', async () => {
    const { tile } = await openDocument();
    await openInspector(tile());
    fireEvent.click(within(sidebar()!).getByRole('button', { name: '+ Overall note' }));
    await act(() => vi.advanceTimersByTimeAsync(0));
    expect(document.querySelector('.md-annotation-popover')).toHaveTextContent('Overall Note');

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(composer()).toBeNull();
    expect(sidebar()).toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(sidebar()).toBeNull();
  });
});

describe('App markdown annotations inspector', () => {
  it('stays closed while the user annotates, and lists the marks in document order with overall notes last', async () => {
    const { daemon, tile, reader } = await openDocument({
      annotations: [
        stored('later-global', null, { type: 'global', text: 'overall two', created_at: 9 }),
        stored('prose', PLAIN_PROSE, { quick_label_id: 'future-label', text: undefined }),
        stored('early-global', null, { type: 'global', text: 'overall one', created_at: 2 }),
        stored('target', TARGET_WORDS, { type: 'deletion', text: undefined }),
      ],
    });
    await select(daemon, reader(), 'Second block');
    fireEvent.click(toolbarButton('Delete'));
    expect(sidebar()).toBeNull();

    await openInspector(tile());

    expect(cards().map((card) => card.textContent)).toEqual([
      'deletion"target words"',
      'deletion"Second block"',
      'future-label"plain prose"',
      'globaloverall one',
      'globaloverall two',
    ]);
    fireEvent.click(within(sidebar()!).getByTitle('Close review notes'));
    expect(sidebar()).toBeNull();
  });

  it('says there is nothing yet when a document has no marks', async () => {
    const { tile } = await openDocument();

    await openInspector(tile());

    expect(within(sidebar()!).getByTitle('Close review notes')).toHaveTextContent('0');
    expect(sidebar()).toHaveTextContent('No annotations yet.');
  });

  it('removes a mark from its card without focusing it, and clears the draft when the last one goes', async () => {
    const { daemon, tile, reader } = await openDocument({ annotations: [stored('target', TARGET_WORDS), stored('prose', PLAIN_PROSE)] });
    await openInspector(tile());

    fireEvent.click(within(cards()[0]).getByTitle('Remove annotation'));
    await settleSaves(daemon);
    expect(lastSaved(daemon).map((annotation) => annotation.id)).toEqual(['prose']);
    expect(reader().querySelector('[data-md-mark="md-focus-glow"]')).toBeNull();
    expect(sidebar()).toBeInTheDocument();

    fireEvent.click(within(cards()[0]).getByTitle('Remove annotation'));
    await settleSaves(daemon);
    expect(saves(daemon)).toHaveLength(1);
    expect(daemon.sentOf('markdown_annotations_clear')).toEqual([expect.objectContaining({ generation: 7 })]);
    expect(paintedMarks(reader())).toEqual([]);
  });

  it('clears everything only on a confirmed second click, and disarms the confirm after a beat', async () => {
    const { daemon, tile, reader } = await openDocument({ annotations: [stored('target', TARGET_WORDS)] });
    await openInspector(tile());
    const clearAll = () => within(sidebar()!).getByRole('button', { name: /Clear all|Confirm\?/ });

    fireEvent.click(clearAll());
    expect(clearAll()).toHaveTextContent('Confirm?');
    await act(() => vi.advanceTimersByTimeAsync(3000));
    expect(clearAll()).toHaveTextContent('Clear all');
    fireEvent.click(clearAll());
    await daemon.idle();
    expect(daemon.sentOf('markdown_annotations_clear')).toEqual([]);

    fireEvent.click(clearAll());
    await daemon.idle();

    expect(daemon.sentOf('markdown_annotations_clear')).toEqual([expect.objectContaining({ generation: 6 })]);
    expect(saves(daemon)).toEqual([]);
    expect(paintedMarks(reader())).toEqual([]);
  });

  it('focuses a mark from its card or from the document', async () => {
    const { tile, reader } = await openDocument({ annotations: [stored('target', TARGET_WORDS), stored('prose', PLAIN_PROSE)] });
    await openInspector(tile());

    fireEvent.click(cards()[1]);
    expect(sidebar()).toBeNull();
    expect(reader().querySelector('[data-md-mark="md-focus-glow"]')).toHaveTextContent('plain prose');

    fireEvent.click(reader().querySelector('[data-md-mark="target"]')!);
    await openInspector(tile());
    expect(cards()[0]).toHaveAttribute('aria-current', 'true');
    expect(cards()[1]).not.toHaveAttribute('aria-current');
  });
});

describe('App markdown annotations persistence', () => {
  it('paints the marks the daemon holds for the document, without re-saving them', async () => {
    const { daemon, reader } = await openDocument({ annotations: [stored('target', TARGET_WORDS)], generation: 3 });
    await settleSaves(daemon);

    expect(reader().querySelector('[data-md-mark="target"]')).toHaveTextContent('target words');
    expect(saves(daemon)).toEqual([]);
    expect(daemon.sentOf('markdown_annotations_clear')).toEqual([]);
  });

  it('paints a mark across inline code without changing the text, and restores the text when it is removed', async () => {
    const { daemon, reader, tile } = await openDocument({ content: 'Call `parse()` before the retry.\n' });
    const documentText = () => reader().querySelector('.md-reader-card')!.textContent;
    const text = documentText();

    await selectRange(daemon, textPoint(reader(), 'Call'), textPoint(reader(), 'before', 'end'));
    fireEvent.click(toolbarButton('Delete'));
    await settleSaves(daemon);
    expect(paintedMarks(reader()).join('')).toBe('Call parse() before');
    expect(documentText()).toBe(text);

    await openInspector(tile());
    fireEvent.click(within(cards()[0]).getByTitle('Remove annotation'));
    await settleSaves(daemon);
    expect(paintedMarks(reader())).toEqual([]);
    expect(documentText()).toBe(text);
  });

  it('never paints over a code block’s copy button', async () => {
    const { daemon, reader } = await openDocument();

    const fence = reader().querySelector('pre')!;
    await selectRange(daemon, textPoint(fence, 'const'), textPoint(fence, ';', 'end'));
    fireEvent.click(toolbarButton('Delete'));
    await settleSaves(daemon);

    expect(paintedMarks(reader()).join('')).toBe('const a = 1;');
    expect(reader().querySelector('button[title="Copy code"] [data-md-mark]')).toBeNull();
  });

  it('keeps one tile’s marks painted when the user marks the same document in another tile', async () => {
    const { daemon, reader } = await openDocument({ annotations: [stored('target', TARGET_WORDS)], tiles: ['tile-a', 'tile-b'] });

    await select(daemon, reader('tile-b'), 'plain prose');
    fireEvent.click(toolbarButton('Delete'));
    await settleSaves(daemon);

    expect(paintedMarks(reader('tile-a'))).toContain('target words');
    expect(paintedMarks(reader('tile-b'))).toEqual(expect.arrayContaining(['target words', 'plain prose']));
  });

  it('saves after a pause under the generation past the one it read, coalescing quick edits', async () => {
    const { daemon, reader } = await openDocument({ generation: 5 });

    await select(daemon, reader(), 'target words');
    fireEvent.click(toolbarButton('Delete'));
    await act(() => vi.advanceTimersByTimeAsync(200));
    await select(daemon, reader(), 'plain prose');
    fireEvent.click(toolbarButton('Delete'));
    await act(() => vi.advanceTimersByTimeAsync(499));
    await daemon.idle();
    expect(saves(daemon)).toEqual([]);
    await settleSaves(daemon);

    expect(saves(daemon)).toEqual([expect.objectContaining({ generation: 6, annotations: [expect.anything(), expect.anything()] })]);
  });

  it('keeps a mark on its text when lines are added above it, and saves the moved anchor', async () => {
    const { daemon, show, reader } = await openDocument({ annotations: [stored('target', TARGET_WORDS)], generation: 1 });

    await show(`A brand new intro paragraph.\n\n${DOC}`);
    await settleSaves(daemon);

    expect(reader().querySelector('[data-md-mark="target"]')).toHaveTextContent('target words');
    expect(saves(daemon)).toEqual([expect.objectContaining({
      generation: 2,
      annotations: [expect.objectContaining({ anchor: expect.objectContaining({ exact: 'target words', start_line: 5 }) })],
    })]);
    expect(lastSaved(daemon)[0].anchor!.content_hash).not.toBe(DOC_HASH);
  });

  it('lists a mark whose text was removed as moved, with its last known line, and paints nothing', async () => {
    const { show, tile, reader } = await openDocument({ annotations: [stored('target', TARGET_WORDS)] });

    await show('# Plan\n\nCompletely different first paragraph.\n\nSecond block of plain prose here.\n');
    await openInspector(tile());

    expect(paintedMarks(reader())).toEqual([]);
    expect(cards()[0]).toHaveTextContent('⚠ moved');
    expect(cards()[0]).toHaveTextContent('~line 3 (moved)');
  });

  it('saves a pending edit when its tile closes before the pause ends', async () => {
    const { daemon, reader, layout } = await openDocument();
    await select(daemon, reader(), 'target words');
    fireEvent.click(toolbarButton('Delete'));

    await layout([]);

    expect(saves(daemon)).toEqual([expect.objectContaining({ annotations: [expect.objectContaining({ anchor: TARGET_WORDS })] })]);
  });

  it('saves nothing until the stored marks are read, then keeps what the user marked meanwhile', async () => {
    const { daemon, reader } = await openDocument({ getReply: 'hold' });
    await select(daemon, reader(), 'plain prose');
    fireEvent.click(toolbarButton('Delete'));
    await settleSaves(daemon);
    expect(saves(daemon)).toEqual([]);

    const [get] = daemon.sentOf('markdown_annotations_get');
    daemon.replyTo(get, { event: 'markdown_annotations_get_result', request_id: get.request_id, document_uri: get.document_uri, source_kind: get.source_kind, success: true, annotations: [stored('target', TARGET_WORDS)], generation: 4 });
    await settleSaves(daemon);

    expect(saves(daemon)).toEqual([expect.objectContaining({ generation: 5 })]);
    expect(lastSaved(daemon).map((annotation) => annotation.anchor?.exact).sort()).toEqual(['plain prose', 'target words']);
  });

  it('reads the marks again after a failed read, saving nothing until it succeeds, then saves the union', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { daemon, reader } = await openDocument({ getReply: 'fail' });
    await select(daemon, reader(), 'plain prose');
    fireEvent.click(toolbarButton('Delete'));
    await settleSaves(daemon);
    expect(saves(daemon)).toEqual([]);

    daemon.on('markdown_annotations_get', ({ document_uri, source_kind }) => ({
      event: 'markdown_annotations_get_result', document_uri, source_kind, success: true, annotations: [stored('target', TARGET_WORDS)], generation: 4,
    }));
    await act(() => vi.advanceTimersByTimeAsync(2000));
    await settleSaves(daemon);

    expect(daemon.sentOf('markdown_annotations_get')).toHaveLength(2);
    expect(saves(daemon)).toEqual([expect.objectContaining({ generation: 5 })]);
    expect(lastSaved(daemon).map((annotation) => annotation.anchor?.exact).sort()).toEqual(['plain prose', 'target words']);
  });

  it('sends a failed save again rather than dropping the marks', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { daemon, reader } = await openDocument();
    daemon.on('markdown_annotations_save', ({ document_uri, source_kind, generation }) => ({
      event: 'markdown_annotations_save_result', document_uri, source_kind, success: false, error: 'database is locked', generation,
    }));
    await select(daemon, reader(), 'target words');
    fireEvent.click(toolbarButton('Delete'));
    await settleSaves(daemon);
    expect(saves(daemon)).toHaveLength(1);

    await act(() => vi.advanceTimersByTimeAsync(5000));
    await daemon.idle();

    expect(saves(daemon)).toHaveLength(2);
    expect(saves(daemon)[1].generation).toBeGreaterThan(saves(daemon)[0].generation);
    expect(lastSaved(daemon)).toEqual([expect.objectContaining({ anchor: TARGET_WORDS })]);
  });
});

describe('App markdown annotations sending', () => {
  function holdSubmit(daemon: ScriptedDaemon) {
    daemon.on('markdown_annotations_submit', () => undefined);
  }

  function answerSubmit(daemon: ScriptedDaemon, submit: CommandMessage<'markdown_annotations_submit'>, fields: { status: string; generation?: number; error?: string }) {
    daemon.replyTo(submit, {
      event: 'markdown_annotations_submit_result',
      request_id: submit.request_id,
      document_uri: submit.document_uri,
      source_kind: submit.source_kind,
      success: fields.status === 'delivered',
      ...fields,
    });
  }

  it('saves the last edit before sending, waits for that save, and after delivery starts the next draft past the delivered generation', async () => {
    const { daemon, reader, tile } = await openDocument();
    holdSubmit(daemon);
    daemon.on('markdown_annotations_save', () => undefined);
    await select(daemon, reader(), 'target words');
    fireEvent.click(toolbarButton('Delete'));

    fireEvent.click(within(tile()).getByRole('button', { name: 'Send 1 to s1' }));
    await daemon.idle();
    const [save] = saves(daemon);
    expect(save).toMatchObject({ generation: 6 });
    expect(daemon.sentOf('markdown_annotations_submit')).toEqual([]);

    answerSave(daemon, save, { success: true });
    await daemon.idle();
    const [submit] = daemon.sentOf('markdown_annotations_submit');
    expect(submit).toMatchObject({ target_session_id: 's1', orphaned_ids: [] });

    answerSubmit(daemon, submit, { status: 'delivered', generation: 9 });
    await daemon.idle();
    expect(paintedMarks(reader())).toEqual([]);
    expect(daemon.sentOf('markdown_annotations_clear')).toEqual([]);

    daemon.on('markdown_annotations_save', ({ document_uri, source_kind, generation }) => ({
      event: 'markdown_annotations_save_result', document_uri, source_kind, success: true, generation,
    }));
    await select(daemon, reader(), 'plain prose');
    fireEvent.click(toolbarButton('Delete'));
    await settleSaves(daemon);
    expect(saves(daemon).slice(-1)[0]).toMatchObject({ generation: 10 });
  });

  it('keeps the delivered marks gone when a stale answer to a save made during the send arrives late', async () => {
    const { daemon, reader, tile } = await openDocument();
    holdSubmit(daemon);
    daemon.on('markdown_annotations_save', () => undefined);
    await select(daemon, reader(), 'target words');
    fireEvent.click(toolbarButton('Delete'));
    await settleSaves(daemon);
    answerSave(daemon, saves(daemon)[0], { success: true });
    fireEvent.click(within(tile()).getByRole('button', { name: 'Send 1 to s1' }));
    await daemon.idle();
    await select(daemon, reader(), 'plain prose');
    fireEvent.click(toolbarButton('Delete'));
    await settleSaves(daemon);
    const [, lateSave] = saves(daemon);

    answerSubmit(daemon, daemon.sentOf('markdown_annotations_submit')[0], { status: 'delivered', generation: 9 });
    await daemon.idle();
    answerSave(daemon, lateSave, { success: false, stale: true });
    await daemon.idle();

    expect(paintedMarks(reader())).toEqual([]);
  });

  it('refuses to send marks before the stored ones have been read', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { daemon, reader, tile } = await openDocument({ getReply: 'fail' });
    holdSubmit(daemon);
    await select(daemon, reader(), 'target words');
    fireEvent.click(toolbarButton('Delete'));
    fireEvent.click(within(tile()).getByRole('button', { name: 'Send 1 to s1' }));
    await daemon.idle();

    expect(daemon.sentOf('markdown_annotations_submit')).toEqual([]);
    expect(within(tile()).getByRole('status')).toHaveTextContent('Annotations are still syncing');
  });

  it('keeps a delivered send’s warning on screen when the daemon could not clear the draft', async () => {
    const { daemon, reader, tile } = await openDocument();
    holdSubmit(daemon);
    await select(daemon, reader(), 'target words');
    fireEvent.click(toolbarButton('Delete'));
    fireEvent.click(within(tile()).getByRole('button', { name: 'Send 1 to s1' }));
    await daemon.idle();

    answerSubmit(daemon, daemon.sentOf('markdown_annotations_submit')[0], { status: 'delivered', error: 'draft clear failed' });
    await act(() => vi.advanceTimersByTimeAsync(10_000));

    expect(within(tile()).getByRole('status')).toHaveTextContent('draft clear failed');
  });
});

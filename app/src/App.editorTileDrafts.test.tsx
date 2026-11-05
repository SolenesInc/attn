import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openSession } from './test/appFixtures';
import { daemonSession } from './test/daemonFixtures';
import { laidOutDesktop, pane, split } from './test/desktopLayouts';
import { fakeRects, sizeTerminals } from './test/layout';
import type { CommandMessage } from './test/protocol';
import { gesture, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { serveSettings } from './test/settings';

const NOTEBOOK = { 'notebook.root.effective': '/notebook' };
const SHELF_ON = { ...NOTEBOOK, shelf_layout_enabled: 'true', narrowest_tile_cols: '66' };
const EDITOR = { type: 'tile', tile_id: 'tile-note', tile_kind: 'notebook', tile_params: JSON.stringify({ path: 'notes.txt' }) };
const TREE = split('a', 'vertical', [EDITOR, split('b', 'vertical', [pane('s1'), split('c', 'vertical', [pane('s2'), pane('s3')])])]);
const SAVED = { event: 'fs_write_result', success: true, result: { path: 'notes.txt', conflict: false, hash: 'h2' } } as const;

type WriteReply = (command: CommandMessage<'fs_write'>) => Record<string, unknown> | undefined;

async function renderRow(settings: Record<string, string>, areaWidth: number, writeReply: WriteReply = () => SAVED) {
  sizeTerminals((terminal) => ({
    clientWidth: (areaWidth * parseFloat(terminal.closest<HTMLElement>('[data-pane-id]')?.style.width || '100')) / 100,
    clientHeight: 900,
  }));
  fakeRects((element) => {
    if (element.classList.contains('session-terminal-panes')) return new DOMRect(0, 0, areaWidth, 900);
    if (element.classList.contains('desktop-shelf')) return new DOMRect(0, 0, areaWidth, 30);
    return null;
  });
  const disk = { content: 'original', hash: 'h1' };
  const script = (daemon: ScriptedDaemon) => {
    serveSettings(daemon, settings);
    daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true, last_seq: 0 }));
    daemon.on('fs_list', () => ({ event: 'fs_list_result', success: true, entries: [{ path: 'notes.txt', name: 'notes.txt', is_dir: false, size: 8 }] }));
    daemon.on('fs_read', ({ path }) => ({ event: 'fs_read_result', success: true, result: { path, ...disk } }));
    daemon.on('fs_write', (command) => {
      const reply = writeReply(command);
      if (reply === SAVED) Object.assign(disk, { content: command.content, hash: 'h2' });
      return reply as never;
    });
    daemon.on('notebook_backlinks', () => ({ event: 'notebook_backlinks_result', success: true, entries: [] }));
    daemon.on('fs_index', ({ root }) => ({ event: 'fs_index_result', success: true, root: root ?? '/notebook', files: ['notes.txt'], truncated: false }));
  };
  const { daemon } = await renderApp({
    initialState: {
      settings,
      sessions: ['s1', 's2', 's3'].map((id) => daemonSession(id, { state: 'idle' })),
      desktops: [{ ...laidOutDesktop(TREE, ['s1', 's2', 's3']), active_pane_id: 'tile-note' }],
    },
    script,
  });
  await openSession(daemon, 's1');
  await daemon.idle();
  return { daemon, disk };
}

const field = () => screen.getByRole('textbox', { name: 'File contents' }) as HTMLTextAreaElement;
const editorGone = () => screen.queryByRole('textbox', { name: 'File contents' });
const writes = (daemon: ScriptedDaemon) => daemon.sentOf('fs_write').map(({ content, base_hash }) => [content, base_hash]);

async function edit(daemon: ScriptedDaemon, content: string) {
  await gesture(daemon, () => fireEvent.change(field(), { target: { value: content } }));
}

async function clickChip(daemon: ScriptedDaemon, id: string) {
  await gesture(daemon, () => fireEvent.click(screen.getByTestId(`shelf-chip-${id}`)));
}

async function moveTime(daemon: ScriptedDaemon, ms: number) {
  await act(() => vi.advanceTimersByTimeAsync(ms));
  await daemon.idle();
}

describe('Editor tile drafts', () => {
  it('saves an edit made just before the tile goes to the shelf, and shows it on return', async () => {
    const { daemon, disk } = await renderRow(SHELF_ON, 2000);
    await edit(daemon, 'original edited');

    await clickChip(daemon, 'pane-s3');
    expect(editorGone()).toBeNull();
    expect(writes(daemon)).toEqual([['original edited', 'h1']]);
    expect(disk.content).toBe('original edited');

    await clickChip(daemon, 'tile-note');
    expect(field()).toHaveValue('original edited');
    await moveTime(daemon, 1000);
    expect(writes(daemon)).toHaveLength(1);
  });

  it('saves an edit made just before the tile folds to a sliver, and shows it on return', async () => {
    const { daemon, disk } = await renderRow(NOTEBOOK, 900);
    const expand = (paneId: string) => document.querySelector<HTMLElement>(`[data-pane-id="${paneId}"][data-pane-suspended] button`)!;
    await gesture(daemon, () => fireEvent.click(expand('tile-note')));
    await edit(daemon, 'original edited');

    await gesture(daemon, () => fireEvent.click(expand('pane-s3')));
    expect(editorGone()).toBeNull();
    expect(writes(daemon)).toEqual([['original edited', 'h1']]);
    expect(disk.content).toBe('original edited');

    await gesture(daemon, () => fireEvent.click(expand('tile-note')));
    expect(field()).toHaveValue('original edited');
  });

  it('brings back the draft and the conflict when the save conflicts while on the shelf', async () => {
    let conflicted = true;
    const { daemon, disk } = await renderRow(SHELF_ON, 2000, ({ base_hash }) =>
      conflicted && base_hash === 'h1'
        ? { event: 'fs_write_result', success: true, result: { path: 'notes.txt', conflict: true, current_hash: 'h9' } }
        : SAVED);
    await edit(daemon, 'original edited');
    await clickChip(daemon, 'pane-s3');
    expect(disk.content).toBe('original');

    await clickChip(daemon, 'tile-note');
    expect(field()).toHaveValue('original edited');
    expect(screen.getByRole('alert')).toHaveTextContent('This file changed on disk since you opened it.');
    await moveTime(daemon, 1000);
    expect(writes(daemon)).toEqual([['original edited', 'h1']]);

    conflicted = false;
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Overwrite anyway' })));
    expect(writes(daemon)).toEqual([['original edited', 'h1'], ['original edited', 'h9']]);
    expect(disk.content).toBe('original edited');
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('keeps a conflicted draft through a second trip to the shelf', async () => {
    const { daemon } = await renderRow(SHELF_ON, 2000, () =>
      ({ event: 'fs_write_result', success: true, result: { path: 'notes.txt', conflict: true, current_hash: 'h9' } }));
    await edit(daemon, 'original edited');
    await clickChip(daemon, 'pane-s3');
    await clickChip(daemon, 'tile-note');
    await edit(daemon, 'original edited again');

    await clickChip(daemon, 'pane-s3');
    await clickChip(daemon, 'tile-note');

    expect(field()).toHaveValue('original edited again');
    expect(screen.getByRole('alert')).toHaveTextContent('This file changed on disk since you opened it.');
  });

  it('brings back the draft and the error when the save fails on the shelf, then saves it', async () => {
    let failing = true;
    const { daemon, disk } = await renderRow(SHELF_ON, 2000, () =>
      failing ? { event: 'fs_write_result', success: false, error: 'disk full' } : SAVED);
    await edit(daemon, 'original edited');
    await clickChip(daemon, 'pane-s3');
    await moveTime(daemon, 1000);
    expect(writes(daemon)).toHaveLength(1);

    failing = false;
    await clickChip(daemon, 'tile-note');
    expect(field()).toHaveValue('original edited');
    expect(screen.getByRole('alert')).toHaveTextContent('disk full');

    await moveTime(daemon, 700);
    expect(disk.content).toBe('original edited');
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.getByRole('status')).toHaveTextContent('Saved');
  });

  it('takes over the save still in flight when the tile returns', async () => {
    const { daemon, disk } = await renderRow(SHELF_ON, 2000, () => undefined);
    await edit(daemon, 'original edited');
    await clickChip(daemon, 'pane-s3');
    await clickChip(daemon, 'tile-note');

    expect(field()).toHaveValue('original edited');
    expect(screen.getByRole('status')).toHaveTextContent('Saving');
    await moveTime(daemon, 1000);
    expect(writes(daemon)).toHaveLength(1);

    await gesture(daemon, () => {
      daemon.replyTo(daemon.sentOf('fs_write')[0], SAVED);
      disk.content = 'original edited';
    });
    expect(screen.getByRole('status')).toHaveTextContent('Saved');
    await moveTime(daemon, 1000);
    expect(writes(daemon)).toHaveLength(1);
  });

  it('waits for an autosave in flight instead of conflicting with it when the tile leaves', async () => {
    const { daemon, disk } = await renderRow(SHELF_ON, 2000, ({ content }) => (content === 'first' ? undefined : SAVED));
    await edit(daemon, 'first');
    await moveTime(daemon, 700);
    expect(writes(daemon)).toEqual([['first', 'h1']]);
    await edit(daemon, 'second');

    await clickChip(daemon, 'pane-s3');
    expect(writes(daemon)).toEqual([['first', 'h1']]);

    await gesture(daemon, () => daemon.replyTo(daemon.sentOf('fs_write')[0], SAVED));
    expect(writes(daemon)).toEqual([['first', 'h1'], ['second', 'h2']]);
    expect(disk.content).toBe('second');
  });
});

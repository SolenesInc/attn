import { act, fireEvent, screen, within } from '@testing-library/react';
import { EditorView } from '@codemirror/view';
import { describe, expect, it, vi } from 'vitest';
import { openTiles, stubTextLayout } from './test/appFixtures';
import type { CommandMessage } from './test/protocol';
import { pressShortcut } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const NOTEBOOK_ROOT = '/notebook';
const NOTE = '# Plan\n\nship the parser';

interface NotebookTileSpec {
  id: string;
  root?: string;
  path?: string;
}

function notebookTile({ id, root, path = 'plan.md' }: NotebookTileSpec) {
  return { tile_id: id, tile_kind: 'notebook', tile_params: JSON.stringify({ ...(root ? { root } : {}), path }) };
}

function serveFolders(daemon: ScriptedDaemon, resolve: (root: string) => string = (root) => root) {
  daemon.on('fs_list', () => ({ event: 'fs_list_result', success: true, entries: [{ path: 'plan.md', name: 'plan.md', is_dir: false, size: 12 }] }));
  daemon.on('fs_read', ({ path, root }) => ({ event: 'fs_read_result', success: true, result: { path, content: `${NOTE}\n\nfrom ${root ?? 'the notebook'}`, hash: 'h1' } }));
  daemon.on('fs_write', ({ path }) => ({ event: 'fs_write_result', success: true, result: { path, conflict: false, hash: 'h2' } }));
  daemon.on('notebook_backlinks', () => ({ event: 'notebook_backlinks_result', success: true, entries: [] }));
  daemon.on('fs_index', ({ root }) => ({ event: 'fs_index_result', success: true, root: root ?? NOTEBOOK_ROOT, files: ['plan.md'], truncated: false }));
  daemon.on('fs_watch', ({ root }) => ({ event: 'fs_watch_result', success: true, root: resolve(root!) }));
  daemon.on('fs_unwatch', ({ root }) => ({ event: 'fs_unwatch_result', success: true, root }));
  daemon.on('workspace_layout_update_tile', ({ workspace_id, tile_id }) => ({
    event: 'workspace_layout_action_result', action: 'workspace_layout_update_tile', workspace_id, tile_id, success: true,
  }));
}

async function openNotebookTiles(tiles: NotebookTileSpec[], script: (daemon: ScriptedDaemon) => void = serveFolders, { persisted = false } = {}) {
  const view = await openTiles(tiles.map(notebookTile), {
    workspace: { directory: '/repo' },
    session: { directory: '/repo' },
    initialState: { settings: { 'notebook.root.effective': NOTEBOOK_ROOT } },
    persisted,
    script,
  });
  return { ...view, layout: (next: NotebookTileSpec[]) => view.layout(next.map(notebookTile)) };
}

const watches = (daemon: ScriptedDaemon) =>
  daemon.sent.filter((command) => command.cmd === 'fs_watch' || command.cmd === 'fs_unwatch').map((command) => [command.cmd, (command as CommandMessage<'fs_watch'>).root]);

function tileEditor(tile: HTMLElement) {
  return EditorView.findFromDOM(tile.querySelector<HTMLElement>('.cm-content')!)!;
}

function focusAndOpenFinder(tile: HTMLElement) {
  const surface = tile.querySelector<HTMLElement>('.notebook-surface')!;
  surface.focus();
  pressShortcut('file.open', surface);
}

async function selectNote(daemon: ScriptedDaemon, tile: HTMLElement) {
  act(() => tileEditor(tile).dispatch({ selection: { anchor: 2, head: 6 } }));
  await daemon.idle();
}

describe('App notebook tile', () => {
  it('watches a folder outside the notebook, reads through it, and offers no notebook-only features', async () => {
    stubTextLayout();
    const { daemon, tile, layout } = await openNotebookTiles([{ id: 'tile-other', root: '/tmp/other' }], (daemon) => serveFolders(daemon, () => '/private/tmp/other'));

    expect(watches(daemon)).toEqual([['fs_watch', '/tmp/other']]);
    expect(within(tile('tile-other')).getByRole('main')).toHaveTextContent('from /tmp/other');
    expect(daemon.sentOf('notebook_backlinks')).toEqual([]);
    expect(tile('tile-other').querySelector('[aria-label="Context"]')).toBeNull();
    await selectNote(daemon, tile('tile-other'));
    expect(screen.queryByRole('button', { name: 'Send to chief' })).toBeNull();

    await layout([]);

    expect(watches(daemon)).toEqual([['fs_watch', '/tmp/other'], ['fs_unwatch', '/private/tmp/other']]);
  });

  it('watches nothing for tiles on the notebook, and offers backlinks and send to chief there', async () => {
    stubTextLayout();
    const { daemon, tile } = await openNotebookTiles([{ id: 'tile-default' }, { id: 'tile-notebook', root: NOTEBOOK_ROOT }]);

    expect(watches(daemon)).toEqual([]);
    expect(daemon.sentOf('notebook_backlinks')).toEqual([
      expect.objectContaining({ path: 'plan.md' }),
      expect.objectContaining({ path: 'plan.md' }),
    ]);
    for (const id of ['tile-default', 'tile-notebook']) {
      expect(tile(id).querySelector('[aria-label="Context"]')).toHaveTextContent('Backlinks');
      await selectNote(daemon, tile(id));
      expect(within(tile(id)).getByRole('button', { name: 'Send to chief' })).toBeInTheDocument();
    }
  });

  it('unwatches a folder whose watch answers only after its tile closed', async () => {
    const { daemon, layout } = await openNotebookTiles([{ id: 'tile-other', root: '/tmp/other' }], (daemon) => {
      serveFolders(daemon);
      daemon.on('fs_watch', () => undefined);
    });
    const [watch] = daemon.sentOf('fs_watch');

    await layout([]);
    daemon.replyTo(watch, { event: 'fs_watch_result', request_id: watch.request_id, success: true, root: '/private/tmp/other' });
    await daemon.idle();

    expect(watches(daemon)).toEqual([['fs_watch', '/tmp/other'], ['fs_unwatch', '/private/tmp/other']]);
  });

  it('moves its watch when the tile is pointed at another folder', async () => {
    const { daemon, layout } = await openNotebookTiles([{ id: 'tile-other', root: '/tmp/a' }]);

    await layout([{ id: 'tile-other', root: '/tmp/b' }]);

    expect(watches(daemon)).toEqual([['fs_watch', '/tmp/a'], ['fs_unwatch', '/tmp/a'], ['fs_watch', '/tmp/b']]);
  });

  it('watches its folder again after the daemon reconnects', async () => {
    const { daemon } = await openNotebookTiles([{ id: 'tile-other', root: '/tmp/a' }], serveFolders, { persisted: true });

    const reconnected = await daemon.reconnect();
    await daemon.idle();

    expect(reconnected.sent).toContainEqual(expect.objectContaining({ cmd: 'fs_watch', root: '/tmp/a' }));
  });

  it('still lists and shows notes when the daemon cannot watch the folder', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { tile } = await openNotebookTiles([{ id: 'tile-other', root: '/tmp/other' }], (daemon) => {
      serveFolders(daemon);
      daemon.on('fs_watch', () => ({ event: 'fs_watch_result', success: false, error: 'watch cap reached' }));
    });

    expect(within(tile('tile-other')).getByRole('treeitem', { name: 'plan.md' })).toBeInTheDocument();
    expect(within(tile('tile-other')).getByRole('main')).toHaveTextContent('ship the parser');
  });

  it('refreshes only the tiles rooted where a change happened', async () => {
    const { daemon } = await openNotebookTiles([{ id: 'tile-a', root: '/repo-a' }, { id: 'tile-b', root: '/repo-b' }]);
    const lists = (root: string) => daemon.sentOf('fs_list').filter((list) => list.root === root).length;
    const [a, b] = [lists('/repo-a'), lists('/repo-b')];

    daemon.emit({ event: 'fs_changed', root: '/repo-a', origin: 'disk', paths: ['plan.md'] });
    await daemon.idle();

    expect(lists('/repo-a')).toBe(a + 1);
    expect(lists('/repo-b')).toBe(b);
  });

  it('finds the files the daemon indexed for a folder, even when the index was cut short', async () => {
    const { daemon, tile } = await openNotebookTiles([{ id: 'tile-repo', root: '/repo' }], (daemon) => {
      serveFolders(daemon);
      daemon.on('fs_index', ({ root }) => ({ event: 'fs_index_result', success: true, root: root!, files: ['README.md', 'docs/plan.md'], truncated: true }));
    });
    vi.spyOn(console, 'warn').mockImplementation(() => {});

    focusAndOpenFinder(tile('tile-repo'));
    await daemon.idle();

    expect(daemon.sentOf('fs_index')).toEqual([expect.objectContaining({ root: '/repo' })]);
    expect(within(within(tile('tile-repo')).getByRole('dialog', { name: 'Find a note' })).getAllByRole('option').map((option) => option.textContent)).toEqual([
      'README.mdREADME.md',
      'plan.mddocs/plan.md',
    ]);
  });

  it('opens the finder of the tile that holds focus', async () => {
    const { daemon, tile } = await openNotebookTiles([{ id: 'tile-a' }, { id: 'tile-b', root: NOTEBOOK_ROOT }]);

    focusAndOpenFinder(tile('tile-b'));
    await daemon.idle();

    expect(within(tile('tile-b')).getByRole('dialog', { name: 'Find a note' })).toBeInTheDocument();
    expect(within(tile('tile-a')).queryByRole('dialog', { name: 'Find a note' })).toBeNull();
  });
});

describe('App notebook tile root', () => {
  function editNote(tile: HTMLElement) {
    const editor = tileEditor(tile);
    act(() => editor.dispatch({ changes: { from: editor.state.doc.length, insert: ' today' } }));
  }

  async function chooseRoot(daemon: ScriptedDaemon, tile: HTMLElement, root: string) {
    fireEvent.change(within(tile).getByRole('combobox', { name: 'Editor root' }), { target: { value: root } });
    await daemon.idle();
  }

  const retargets = (daemon: ScriptedDaemon) =>
    daemon.sentOf('workspace_layout_update_tile').filter((update) => update.tile_params === JSON.stringify({ root: '/repo' }));

  it('saves an unsaved edit to the old folder before switching the tile to another', async () => {
    const { daemon, tile } = await openNotebookTiles([{ id: 'tile-notes' }]);
    editNote(tile('tile-notes'));

    await chooseRoot(daemon, tile('tile-notes'), '/repo');

    const write = daemon.sent.findIndex((command) => command.cmd === 'fs_write');
    const retarget = daemon.sent.indexOf(retargets(daemon)[0]);
    expect(daemon.sentOf('fs_write')).toEqual([expect.objectContaining({ path: 'plan.md', base_hash: 'h1', content: `${NOTE}\n\nfrom the notebook today` })]);
    expect(daemon.sentOf('fs_write')[0]).not.toHaveProperty('root');
    expect(write).toBeLessThan(retarget);
  });

  it('keeps the tile on its folder with the conflict shown when that save conflicts', async () => {
    const { daemon, tile } = await openNotebookTiles([{ id: 'tile-notes' }]);
    daemon.on('fs_write', ({ path }) => ({ event: 'fs_write_result', success: true, result: { path, conflict: true, current_hash: 'hX' } }));
    editNote(tile('tile-notes'));

    await chooseRoot(daemon, tile('tile-notes'), '/repo');

    expect(retargets(daemon)).toEqual([]);
    expect(within(tile('tile-notes')).getByRole('alert')).toHaveTextContent('This file changed on disk since you opened it.');
  });

  it('switches a tile with nothing unsaved without writing', async () => {
    const { daemon, tile } = await openNotebookTiles([{ id: 'tile-notes' }]);

    await chooseRoot(daemon, tile('tile-notes'), '/repo');

    expect(daemon.sentOf('fs_write')).toEqual([]);
    expect(retargets(daemon)).toHaveLength(1);
  });
});

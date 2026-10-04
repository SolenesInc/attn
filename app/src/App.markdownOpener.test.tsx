import { act, fireEvent, screen, within } from '@testing-library/react';
import { openPath } from '@tauri-apps/plugin-opener';
import { describe, expect, it, vi } from 'vitest';
import { agentPane, daemonSession, daemonDesktop, dockTiles, type DaemonSession } from './test/daemonFixtures';
import { pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const NOTEBOOK_ROOT = '/notebook';
const RECENTS = [
  { path: '/repo/docs/plan.md', last_at: '2026-07-24T10:00:00Z', count: 3, source: 'open' },
  { path: '/other/journal.md', last_at: '2026-07-23T10:00:00Z', count: 1, source: 'open' },
];

function desktopFor(session: DaemonSession, tiles: Array<{ tile_id: string; tile_kind: string; tile_params: string }> = []) {
  return daemonDesktop(
    'ws',
    { root: dockTiles({ type: 'pane', pane_id: `pane-${session.id}` }, tiles), panes: [agentPane(session.id, 'ws')] },
    { name: 'ws' },
  );
}

function serveOpener(daemon: ScriptedDaemon, index: { files: string[]; truncated?: boolean } | 'hold' = { files: ['docs/design.md', 'README.md', 'docs/plan.md'] }) {
  daemon.on('recent_files', () => ({ event: 'recent_files_result', success: true, files: RECENTS }));
  daemon.on('fs_index', ({ root }) => (
    index === 'hold' ? undefined : { event: 'fs_index_result', success: true, root: root!, files: index.files, truncated: index.truncated ?? false }
  ));
  daemon.on('browse_directory', ({ input_path }) => ({
    event: 'browse_directory_result',
    success: true,
    input_path,
    directory: '/home/victor/notes',
    home_path: '/home/victor',
    entries: [
      { name: 'archive', path: '/home/victor/notes/archive', is_dir: true },
      { name: 'ideas.md', path: '/home/victor/notes/ideas.md', is_dir: false },
    ],
  }));
  daemon.on('open_markdown', ({ path }) => ({ event: 'open_markdown_result', success: true, path, tile_id: 'tile-opened' }));
}

interface OpenerOptions {
  session?: DaemonSession | null;
  notebookRoot?: string;
  index?: Parameters<typeof serveOpener>[1];
}

async function openOpener({ session = daemonSession('s1', { directory: '/repo' }), notebookRoot = NOTEBOOK_ROOT, index }: OpenerOptions = {}) {
  const view = await renderApp({
    initialState: {
      settings: notebookRoot ? { 'notebook.root.effective': notebookRoot } : {},
      sessions: session ? [session] : [],
      desktops: session ? [desktopFor(session)] : [],
      ...(session?.endpoint_id ? { endpoints: [{ id: session.endpoint_id, name: 'gpu-box', ssh_target: 'user@gpu-box', status: 'connected', enabled: true }] } : {}),
    },
  });
  serveOpener(view.daemon, index);
  if (session) {
    fireEvent.click(screen.getByRole('button', { name: `Open ${session.id}` }));
    await view.daemon.idle();
  }
  pressShortcut('file.open');
  await view.daemon.idle();
  return view;
}

const opener = () => within(screen.getByRole('dialog', { name: 'Open a markdown file' }));
const openerInput = () => opener().getByRole('combobox');
const rows = () => opener().queryAllByRole('option').map((row) => row.textContent);

async function type(daemon: ScriptedDaemon, query: string) {
  fireEvent.change(openerInput(), { target: { value: query } });
  await act(() => vi.advanceTimersByTimeAsync(300));
  await daemon.idle();
}

async function press(daemon: ScriptedDaemon, key: string) {
  fireEvent.keyDown(openerInput(), { key });
  await daemon.idle();
}

describe('App markdown opener', () => {
  it('lists the recent files at once, labelled relative to the session’s folder, and takes focus', async () => {
    const { daemon } = await openOpener();

    expect(rows()).toEqual(['plan.mddocs/plan.md', 'journal.md/other/journal.md']);
    expect(openerInput()).toHaveFocus();
    expect(daemon.sentOf('recent_files')).toEqual([expect.objectContaining({ root: '/repo' })]);
    expect(daemon.sentOf('fs_index')).toEqual([expect.objectContaining({ root: '/repo', extensions: ['md'] })]);
  });

  it('lists the recents before the index answers, then ranks both in one list with each file once', async () => {
    const { daemon } = await openOpener({ index: 'hold' });
    expect(rows()).toHaveLength(2);

    const [index] = daemon.sentOf('fs_index');
    daemon.replyTo(index, { event: 'fs_index_result', request_id: index.request_id, success: true, root: '/repo', files: ['docs/design.md', 'docs/plan.md'], truncated: false });
    await type(daemon, 'd');

    expect(rows()).toEqual(expect.arrayContaining(['plan.mddocs/plan.md', 'design.mddocs/design.md']));
    expect(rows().filter((row) => row === 'plan.mddocs/plan.md')).toHaveLength(1);
  });

  it('opens the highlighted file by its absolute path, in the session', async () => {
    const { daemon } = await openOpener();

    await press(daemon, 'ArrowDown');
    await press(daemon, 'ArrowDown');
    await press(daemon, 'ArrowUp');
    await press(daemon, 'ArrowDown');
    await press(daemon, 'Enter');

    expect(daemon.sentOf('open_markdown')).toEqual([expect.objectContaining({ path: '/other/journal.md', session_id: 's1' })]);
    expect(screen.queryByRole('dialog', { name: 'Open a markdown file' })).toBeNull();
  });

  it('keeps the highlight on a row that still exists as the list shrinks, and opens nothing when nothing matches', async () => {
    const { daemon } = await openOpener();
    await type(daemon, 'md');
    await press(daemon, 'ArrowDown');
    await press(daemon, 'ArrowDown');
    await press(daemon, 'ArrowDown');

    await type(daemon, 'docs/d');
    await press(daemon, 'Enter');
    expect(daemon.sentOf('open_markdown')).toEqual([expect.objectContaining({ path: '/repo/docs/design.md' })]);

    pressShortcut('file.open');
    await daemon.idle();
    await type(daemon, 'zzz');
    expect(opener().getByText('No files match. Type a path to browse.')).toBeInTheDocument();
    await press(daemon, 'Enter');
    expect(daemon.sentOf('open_markdown')).toHaveLength(1);
  });

  it('says the index is capped when nothing matches a capped index', async () => {
    const { daemon } = await openOpener({ index: { files: [], truncated: true } });

    await type(daemon, 'zzzz');

    expect(opener().getByText(/index is capped/)).toBeInTheDocument();
  });

  it('browses a typed directory, opening files by their absolute path and descending into folders', async () => {
    const { daemon } = await openOpener();

    await type(daemon, 'docs/plan');
    expect(daemon.sentOf('browse_directory')).toEqual([]);

    await type(daemon, '~/notes/');
    expect(daemon.sentOf('browse_directory').slice(-1)[0]).toMatchObject({ input_path: '~/notes/', extensions: ['md'] });
    expect(rows()).toEqual(['archive/~/notes/archive', 'ideas.md~/notes/ideas.md']);

    await press(daemon, 'Enter');
    expect(openerInput()).toHaveValue('~/notes/archive/');
    expect(daemon.sentOf('open_markdown')).toEqual([]);

    await type(daemon, '~/notes/');
    await press(daemon, 'ArrowDown');
    await press(daemon, 'Enter');
    expect(daemon.sentOf('open_markdown')).toEqual([expect.objectContaining({ path: '/home/victor/notes/ideas.md' })]);
  });

  it('resolves a relative path against the session’s folder', async () => {
    const { daemon } = await openOpener();

    await type(daemon, './docs/');

    expect(daemon.sentOf('browse_directory').slice(-1)[0]).toMatchObject({ input_path: '/repo/docs/' });
  });

  it('closes on Escape without dismissing what is open beneath it, which the next Escape closes', async () => {
    const session = daemonSession('s1', { directory: '/repo' });
    const view = await renderApp({
      initialState: {
        sessions: [session],
        desktops: [desktopFor(session, [{ tile_id: 'tile-doc', tile_kind: 'markdown', tile_params: '/repo/doc.md' }])],
      },
    });
    serveOpener(view.daemon);
    fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
    await view.daemon.idle();
    view.daemon.emit({ event: 'desktop_tile_content', desktop_id: 'ws', tile_id: 'tile-doc', tile_kind: 'markdown', path: '/repo/doc.md', content: '![chart](chart.png)' });
    await view.daemon.idle();
    fireEvent.click(screen.getByRole('img', { name: 'chart' }));
    pressShortcut('file.open');
    await view.daemon.idle();

    fireEvent.keyDown(openerInput(), { key: 'Escape' });

    expect(screen.queryByRole('dialog', { name: 'Open a markdown file' })).toBeNull();
    expect(document.querySelector('.md-lightbox')).toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'Escape' });

    expect(document.querySelector('.md-lightbox')).toBeNull();
  });

  it('closes on a press outside its box, but not on one inside it', async () => {
    const { daemon } = await openOpener();

    fireEvent.mouseDown(openerInput());
    expect(screen.getByRole('dialog', { name: 'Open a markdown file' })).toBeInTheDocument();

    fireEvent.mouseDown(screen.getByRole('dialog', { name: 'Open a markdown file' }));
    await daemon.idle();
    expect(screen.queryByRole('dialog', { name: 'Open a markdown file' })).toBeNull();
  });
});

describe('App markdown opener target', () => {
  it('indexes the notebook when no session is selected', async () => {
    const { daemon } = await openOpener({ session: null });

    expect(daemon.sentOf('fs_index')).toEqual([expect.objectContaining({ root: NOTEBOOK_ROOT })]);
    expect(rows()).toEqual(['plan.md/repo/docs/plan.md', 'journal.md/other/journal.md']);
  });

  it('indexes the notebook for a local session without a folder, still opening in that session', async () => {
    const { daemon } = await openOpener({ session: daemonSession('s1', { directory: '' }) });
    await press(daemon, 'Enter');

    expect(daemon.sentOf('fs_index')).toEqual([expect.objectContaining({ root: NOTEBOOK_ROOT })]);
    expect(daemon.sentOf('open_markdown')).toEqual([expect.objectContaining({ session_id: 's1' })]);
  });

  it.each([
    { notebook: 'the notebook', notebookRoot: NOTEBOOK_ROOT, indexed: [NOTEBOOK_ROOT] },
    { notebook: 'no notebook', notebookRoot: '', indexed: [] },
  ])('never indexes or opens into a remote session’s folder, falling back to $notebook', async ({ notebookRoot, indexed }) => {
    const { daemon } = await openOpener({
      session: daemonSession('s1', { directory: '/repo', endpoint_id: 'ep-1' }),
      notebookRoot,
    });
    expect(daemon.sentOf('fs_index').map((index) => index.root)).toEqual(indexed);
    expect(rows()).toHaveLength(2);

    await press(daemon, 'Enter');

    expect(daemon.sentOf('open_markdown')).toEqual([]);
    expect(openPath).toHaveBeenCalledWith('/repo/docs/plan.md');
  });

  it('roots a notebook opened from a shown document tile in the folder of the agent the tile was opened for', async () => {
    const view = await renderApp({
      initialState: {
        settings: { 'notebook.root.effective': NOTEBOOK_ROOT },
        sessions: [daemonSession('s1', { directory: '/repo' }), daemonSession('s2', { directory: '/elsewhere' })],
        desktops: [
          daemonDesktop('ws', {
            root: dockTiles({ type: 'pane', pane_id: 'pane-s2' }, [
              { tile_id: 'tile-plan', tile_kind: 'markdown', tile_params: '/repo/docs/plan.md', tile_session_id: 's1' },
            ]),
            panes: [agentPane('s2', 'ws')],
          }, { name: 'ws', active_pane_id: 'tile-plan' }),
        ],
      },
    });
    await view.daemon.idle();
    fireEvent.click(screen.getByRole('button', { name: 'Open ws' }));
    await view.daemon.idle();

    pressShortcut('notebook.openTile');
    await view.daemon.idle();

    expect(view.daemon.sentOf('desktop_dock_tile')).toEqual([expect.objectContaining({ tile_kind: 'notebook', tile_params: '{"root":"/repo"}' })]);
  });

  it('indexes the folder of the agent a shown document tile was opened for, never a borrowed agent', async () => {
    const view = await renderApp({
      initialState: {
        settings: { 'notebook.root.effective': NOTEBOOK_ROOT },
        sessions: [daemonSession('s1', { directory: '/repo' }), daemonSession('s2', { directory: '/elsewhere' })],
        desktops: [
          daemonDesktop('ws', {
            root: dockTiles({ type: 'pane', pane_id: 'pane-s2' }, [
              { tile_id: 'tile-plan', tile_kind: 'markdown', tile_params: '/repo/docs/plan.md', tile_session_id: 's1' },
              { tile_id: 'tile-loose', tile_kind: 'markdown', tile_params: '/tmp/loose.md' },
            ]),
            panes: [agentPane('s2', 'ws')],
          }, { name: 'ws', active_pane_id: 'tile-plan' }),
        ],
      },
    });
    serveOpener(view.daemon);
    await view.daemon.idle();
    fireEvent.click(screen.getByRole('button', { name: 'Open ws' }));
    await view.daemon.idle();

    pressShortcut('file.open');
    await view.daemon.idle();
    expect(view.daemon.sentOf('fs_index').map((index) => index.root)).toEqual(['/repo']);
    await press(view.daemon, 'Escape');

    fireEvent.mouseDown(document.querySelector('[data-pane-id="tile-loose"]')!);
    await view.daemon.idle();
    pressShortcut('file.open');
    await view.daemon.idle();
    expect(view.daemon.sentOf('fs_index').map((index) => index.root)).toEqual(['/repo', NOTEBOOK_ROOT]);
  });

  it('skips the index when there is no folder at all', async () => {
    const { daemon } = await openOpener({ session: null, notebookRoot: '' });

    expect(daemon.sentOf('fs_index')).toEqual([]);
    expect(rows()).toHaveLength(2);
  });
});

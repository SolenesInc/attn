import { fireEvent, screen, within } from '@testing-library/react';
import { invoke } from '@tauri-apps/api/core';
import { describe, expect, it, vi } from 'vitest';
import { agentPane, daemonDesktop, daemonSession, defaultProfile, dockTiles, soloDesktop, type DaemonSession } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const STUDIO = { id: 'studio', name: 'Studio', address: 'studio:2200', state: 'connected' as const, state_since: '2026-10-18T18:00:00Z' };
const REFUSAL = "is on Studio; opening files on an outpost isn't supported yet";

function remoteShell(id: string, directory: string): DaemonSession {
  return daemonSession(id, { agent: 'shell', state: 'idle', directory, host: STUDIO.id });
}

async function connectStudio(daemon: ScriptedDaemon) {
  daemon.emit({ event: 'outposts_updated', outposts: [STUDIO] });
  await daemon.idle();
}

async function runCommand(daemon: ScriptedDaemon, title: string) {
  await gesture(daemon, () => pressShortcut('ui.commandPalette'));
  const palette = screen.getByRole('dialog');
  const option = within(palette).getAllByRole('option').find((entry) => entry.textContent?.includes(title));
  expect(option, `"${title}" in the command palette`).toBeDefined();
  await gesture(daemon, () => fireEvent.mouseDown(option!));
}

async function commandAt(session: DaemonSession, title: string) {
  const view = await renderApp({ initialState: {
    settings: { 'notebook.root.effective': '/notebook' },
    sessions: [session],
    desktops: [soloDesktop(session.id)],
  } });
  await connectStudio(view.daemon);
  fireEvent.click(screen.getByRole('button', { name: `Open ${session.id}` }));
  await view.daemon.idle();
  vi.mocked(invoke).mockClear();
  await runCommand(view.daemon, title);
  return view;
}

function editorInvokes() {
  return vi.mocked(invoke).mock.calls.filter(([command]) => command === 'open_in_editor');
}

describe('paths of a session on an outpost', () => {
  it('fails Open in editor naming the host instead of opening the Mac folder with that name', async () => {
    await commandAt(remoteShell('s1', '/tmp'), 'Open in editor');

    expect(editorInvokes()).toEqual([]);
    expect(screen.getByText(`/tmp ${REFUSAL}`)).toBeInTheDocument();
  });

  it('still opens a local session folder in the editor', async () => {
    await commandAt(daemonSession('s1', { directory: '/repo' }), 'Open in editor');

    expect(editorInvokes()).toEqual([['open_in_editor', expect.objectContaining({ cwd: '/repo' })]]);
  });

  it('opens the editor tile on the notebook root, not the Mac folder with the shell’s name', async () => {
    const { daemon } = await commandAt(remoteShell('s1', '/home/victor/project'), 'Open Editor tile');

    expect(daemon.sentOf('desktop_dock_tile').map(({ tile_kind, tile_params }) => [tile_kind, tile_params])).toEqual([['notebook', undefined]]);
  });

  it('roots the markdown opener at the notebook root, never indexing the Mac folder with the shell’s name', async () => {
    const view = await renderApp({ initialState: {
      settings: { 'notebook.root.effective': '/notebook' },
      sessions: [remoteShell('s1', '/home/victor/project')],
      desktops: [soloDesktop('s1')],
    } });
    view.daemon.on('recent_files', () => ({ event: 'recent_files_result', success: true, files: [] }));
    view.daemon.on('fs_index', ({ root }) => ({ event: 'fs_index_result', success: true, root: root!, files: [], truncated: false }));
    await connectStudio(view.daemon);
    fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
    await view.daemon.idle();

    pressShortcut('file.open');
    await view.daemon.idle();

    expect(view.daemon.sentOf('fs_index').map(({ root }) => root)).toEqual(['/notebook']);
    expect(view.daemon.sentOf('recent_files').map(({ root }) => root)).toEqual(['/notebook']);
  });

  it('does not offer the shell’s folder as an editor tile root', async () => {
    const { daemon } = await renderApp({ initialState: {
      settings: { 'notebook.root.effective': '/notebook' },
      sessions: [remoteShell('s1', '/home/victor/project')],
      profiles: [defaultProfile('d1')],
      desktops: [daemonDesktop('d1', {
        root: dockTiles({ type: 'pane', pane_id: 'pane-s1' }, [{ tile_id: 'tile-notebook', tile_kind: 'notebook', tile_params: '', tile_session_id: 's1' }]),
        panes: [agentPane('s1', 'd1')],
      }, { active_pane_id: 'tile-notebook', shortcut_slot: 1, name: 'd1' })],
    } });
    await connectStudio(daemon);
    await gesture(daemon, () => pressShortcut('desktop.select1'));

    const options = Array.from(screen.getByRole('combobox', { name: 'Editor root' }).querySelectorAll('option'), (option) => option.textContent);
    expect(options.some((text) => text?.startsWith('Desktop'))).toBe(false);
  });
});

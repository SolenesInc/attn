import { fireEvent, screen, within } from '@testing-library/react';
import { invoke } from '@tauri-apps/api/core';
import { onOpenUrl } from '@tauri-apps/plugin-deep-link';
import { act } from 'react';
import { describe, expect, it, vi } from 'vitest';
import {
  agentPane,
  daemonDesktop,
  daemonSession,
  daemonSeed,
  defaultProfile,
  dockTiles,
  soloDesktop,
  type DaemonDesktop,
} from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const README = { tile_id: 'tile-readme', tile_kind: 'markdown', tile_params: '/tmp/project/README.md' };

function agentBesideReadme(sessionId: string, desktopId: string, slot: number): DaemonDesktop {
  return daemonDesktop(desktopId, {
    root: dockTiles({ type: 'pane', pane_id: `pane-${sessionId}` }, [README]),
    panes: [agentPane(sessionId, desktopId)],
  }, { active_pane_id: `pane-${sessionId}`, shortcut_slot: slot, name: desktopId });
}

function readmeDesktop(desktopId: string, slot: number): DaemonDesktop {
  return daemonDesktop(desktopId, { root: { type: 'tile', ...README } }, { active_pane_id: README.tile_id, shortcut_slot: slot, name: desktopId });
}

async function renderDesktops() {
  return renderApp({ initialState: {
    sessions: [daemonSession('s1', { directory: '/tmp/repo' }), daemonSession('s2', { directory: '/tmp/repo' })],
    profiles: [defaultProfile('d1')],
    desktops: [agentBesideReadme('s1', 'd1', 1), readmeDesktop('d2', 2), soloDesktop('s2', { id: 'd3', shortcut_slot: 3 })],
  } });
}

function shownLeaf(): string | null {
  return document.querySelector('[data-session-visible="1"]')?.getAttribute('data-active-leaf-id') ?? null;
}

function mountedDesktops(): string[] {
  return Array.from(document.querySelectorAll('[data-session-terminal-desktop]'), (desktop) => desktop.getAttribute('data-session-terminal-desktop')!);
}

function tileEl(tileId = README.tile_id) {
  return document.querySelector<HTMLElement>(`[data-session-visible="1"] [data-pane-id="${tileId}"]`)!;
}

async function commandTitles(daemon: ScriptedDaemon): Promise<string[]> {
  await gesture(daemon, () => pressShortcut('ui.commandPalette'));
  const palette = screen.getByRole('dialog');
  const titles = within(palette).getAllByRole('option').map((option) => option.textContent ?? '');
  await gesture(daemon, () => fireEvent.keyDown(within(palette).getByRole('combobox'), { key: 'Escape' }));
  return titles;
}

function shows(daemon: ScriptedDaemon): string[] {
  return daemon.sent.flatMap((command) => {
    if (command.cmd === 'desktop_show_session') return [`session:${command.session_id}`];
    if (command.cmd === 'desktop_show_leaf') return [`leaf:${command.desktop_id}/${command.leaf_id}`];
    return [];
  });
}

describe('what the active leaf offers', () => {
  it('uses the garden title for a seed tile in the desktop sidebar', async () => {
    const seed = { tile_id: 'tile-seed', tile_kind: 'seed', tile_params: 's-named' };
    await renderApp({ initialState: {
      sessions: [daemonSession('s1')],
      profiles: [defaultProfile('d1')],
      desktops: [daemonDesktop('d1', {
        root: dockTiles({ type: 'pane', pane_id: 'pane-s1' }, [seed]),
        panes: [agentPane('s1', 'd1')],
      }, { active_pane_id: 'pane-s1', shortcut_slot: 1 })],
      seeds: [daemonSeed('s-named', { title: 'Native client plan' })],
    } });

    expect(screen.getByTestId('sidebar-tile-d1-tile-seed')).toHaveTextContent('Native client plan');
  });

  it('names tile kinds and seed titles in the palette and bar peek, then shows a picked seed once', async () => {
    const named = { tile_id: 'tile-seed', tile_kind: 'seed', tile_params: 's-named', tile_session_id: 's1' };
    const missing = { tile_id: 'tile-missing', tile_kind: 'seed', tile_params: 's-missing' };
    const markdown = { tile_id: 'tile-doc', tile_kind: 'markdown', tile_params: '/tmp/notes.md' };
    const browser = { tile_id: 'tile-web', tile_kind: 'browser', tile_params: 'https://example.com/path' };
    const desktop = daemonDesktop('d1', {
      root: dockTiles({ type: 'pane', pane_id: 'pane-s1' }, [named, missing, markdown, browser]),
      panes: [agentPane('s1', 'd1')],
    }, { active_pane_id: 'pane-s1', shortcut_slot: 1 });
    const { daemon } = await renderApp({ initialState: {
      settings: { queue_mode_enabled: 'true' },
      sessions: [daemonSession('s1', { turn_owed: true, turn_opened_at: '2026-09-28T10:00:00Z' })],
      profiles: [defaultProfile('d1')],
      desktops: [desktop],
      seeds: [daemonSeed('s-named', { title: 'Native client plan' })],
    } });

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Collapse sidebar' })));
    await gesture(daemon, () => fireEvent.pointerEnter(screen.getByTestId('queue-bar-waiting')));
    const peek = screen.getByTestId('queue-bar-waiting-peek');
    expect(within(peek).getByTestId('queue-bar-peek-tile:d1:tile-seed')).toHaveTextContent('SEEDNative client plan⌘1');
    expect(within(peek).getByTestId('queue-bar-peek-tile:d1:tile-missing')).toHaveTextContent('SEEDs-missing⌘1');

    await gesture(daemon, () => pressShortcut('ui.actionMenu'));
    const palette = screen.getByRole('dialog', { name: 'Agents' });
    expect(within(palette).getByTestId('palette-tile-tile-seed')).toHaveTextContent('SEEDNative client plan⌘1');
    expect(within(palette).getByTestId('palette-tile-tile-missing')).toHaveTextContent('SEEDs-missing⌘1');
    expect(within(palette).getByTestId('palette-tile-tile-doc')).toHaveTextContent('DOCnotes.md⌘1');
    expect(within(palette).getByTestId('palette-tile-tile-web')).toHaveTextContent('WEBexample.com⌘1');

    const search = within(palette).getByRole('combobox');
    fireEvent.change(search, { target: { value: 'seed Native' } });
    expect(within(palette).getAllByRole('option')).toHaveLength(1);
    await gesture(daemon, () => fireEvent.mouseDown(within(palette).getByTestId('palette-tile-tile-seed').closest('[role="option"]')!));
    expect(shows(daemon)).toEqual(['leaf:d1/tile-seed']);
  });

  it('lists this profile’s agents and tiles in the agents palette and shows a picked tile with one request', async () => {
    const { daemon } = await renderDesktops();

    await gesture(daemon, () => pressShortcut('ui.actionMenu'));
    const palette = screen.getByRole('dialog', { name: 'Agents' });
    const options = within(palette).getAllByRole('option').map((option) => option.textContent ?? '');
    expect(options.filter((text) => /^s\d/.test(text)).map((text) => text.slice(0, 2))).toEqual(['s1', 's2']);
    expect(options.filter((text) => text.includes('README.md'))).toHaveLength(2);

    const pick = within(palette).getAllByRole('option').find((option) => option.textContent?.includes('README.md') && option.textContent.includes('⌘2'))!;
    await gesture(daemon, () => fireEvent.mouseDown(pick));

    expect(shows(daemon)).toEqual(['leaf:d2/tile-readme']);
    expect(screen.queryByRole('dialog', { name: 'Agents' })).toBeNull();
    expect(shownLeaf()).toBe('tile-readme');
  });

  it('offers agent commands while an agent leaf is shown, and not on a tile or at Home', async () => {
    const { daemon } = await renderDesktops();
    await gesture(daemon, () => pressShortcut('desktop.select1'));

    const onAgent = await commandTitles(daemon);
    expect(onAgent.some((text) => text.includes('Show workflow runs'))).toBe(true);
    expect(onAgent.some((text) => text.includes('Open in editor'))).toBe(true);

    await gesture(daemon, () => fireEvent.mouseDown(tileEl()));
    expect(shownLeaf()).toBe('tile-readme');
    const onTile = await commandTitles(daemon);
    expect(onTile.some((text) => text.includes('workflow runs'))).toBe(false);
    expect(onTile.some((text) => text.includes('Open in editor'))).toBe(false);

    await gesture(daemon, () => pressShortcut('session.goToDashboard'));
    const atHome = await commandTitles(daemon);
    expect(atHome.some((text) => text.includes('workflow runs'))).toBe(false);
    expect(atHome.some((text) => text.includes('Open in editor'))).toBe(false);
  });

  it('opens the remote folder of the agent a shown tile is bound to in the editor', async () => {
    const { daemon } = await renderApp({ initialState: {
      settings: { editor_executable: 'zed' },
      endpoints: [{ id: 'ep-1', name: 'gpu-box', ssh_target: 'user@gpu-box', status: 'connected', enabled: true }],
      sessions: [daemonSession('r1', { directory: '/srv/project', endpoint_id: 'ep-1' })],
      profiles: [defaultProfile('d1')],
      desktops: [daemonDesktop('d1', {
        root: dockTiles({ type: 'pane', pane_id: 'pane-r1' }, [{ ...README, tile_session_id: 'r1' }]),
        panes: [agentPane('r1', 'd1')],
      }, { active_pane_id: README.tile_id, shortcut_slot: 1, name: 'd1' })],
    } });
    await gesture(daemon, () => pressShortcut('desktop.select1'));
    expect(shownLeaf()).toBe(README.tile_id);

    await gesture(daemon, () => pressShortcut('ui.commandPalette'));
    const search = within(screen.getByRole('dialog')).getByRole('combobox');
    fireEvent.change(search, { target: { value: '>Open in editor' } });
    await gesture(daemon, () => fireEvent.keyDown(search, { key: 'Enter' }));

    expect(vi.mocked(invoke)).toHaveBeenCalledWith('open_in_editor', expect.objectContaining({ cwd: '/srv/project', remoteTarget: 'user@gpu-box' }));
  });

  it('offers the folder of the agent a shown notebook tile is bound to as its root', async () => {
    const { daemon } = await renderApp({ initialState: {
      settings: { 'notebook.root.effective': '/notebook' },
      sessions: [daemonSession('s1', { directory: '/tmp/repo' })],
      profiles: [defaultProfile('d1')],
      desktops: [daemonDesktop('d1', {
        root: dockTiles({ type: 'pane', pane_id: 'pane-s1' }, [{ tile_id: 'tile-notebook', tile_kind: 'notebook', tile_params: '', tile_session_id: 's1' }]),
        panes: [agentPane('s1', 'd1')],
      }, { active_pane_id: 'tile-notebook', shortcut_slot: 1, name: 'd1' })],
    } });
    await gesture(daemon, () => pressShortcut('desktop.select1'));
    expect(shownLeaf()).toBe('tile-notebook');

    const options = Array.from(screen.getByRole('combobox', { name: 'Editor root' }).querySelectorAll('option'), (option) => option.textContent);
    expect(options).toContain('Desktop — repo');
  });

  it('offers the folder of the agent a notebook tile is bound to even when that agent sits on another desktop', async () => {
    const { daemon } = await renderApp({ initialState: {
      settings: { 'notebook.root.effective': '/notebook' },
      sessions: [daemonSession('s1', { directory: '/tmp/repo' }), daemonSession('s2', { directory: '/tmp/elsewhere' })],
      profiles: [defaultProfile('d1')],
      desktops: [
        daemonDesktop('d1', {
          root: dockTiles({ type: 'pane', pane_id: 'pane-s2' }, [{ tile_id: 'tile-notebook', tile_kind: 'notebook', tile_params: '', tile_session_id: 's1' }]),
          panes: [agentPane('s2', 'd1')],
        }, { active_pane_id: 'tile-notebook', shortcut_slot: 1, name: 'd1' }),
        soloDesktop('s1', { id: 'd2', shortcut_slot: 2 }),
      ],
    } });
    await gesture(daemon, () => pressShortcut('desktop.select1'));
    expect(shownLeaf()).toBe('tile-notebook');

    const options = Array.from(screen.getByRole('combobox', { name: 'Editor root' }).querySelectorAll('option'), (option) => option.textContent);
    expect(options).toContain('Desktop — repo');
  });

  it('walks the automation runs that need the user and settles the one shown, but nothing on a tile', async () => {
    const docs = { run_id: 'r', definition_id: 'docs', definition_name: 'nightly docs', trigger_type: 'schedule' };
    const run = (id: string, hour: number, owed: boolean) =>
      daemonSession(id, { automation: docs, turn_owed: owed, turn_opened_at: `2026-09-26T${String(hour).padStart(2, '0')}:00:00Z`, state: 'waiting_input' });
    const { daemon } = await renderApp({ initialState: {
      sessions: [daemonSession('s1'), run('r1', 9, true), run('r2', 10, true)],
      profiles: [defaultProfile('d1')],
      desktops: [agentBesideReadme('s1', 'd1', 1), soloDesktop('r1', { id: 'dr1' }), soloDesktop('r2', { id: 'dr2' })],
    } });
    await gesture(daemon, () => pressShortcut('desktop.select1'));
    await gesture(daemon, () => fireEvent.mouseDown(tileEl()));

    await gesture(daemon, () => pressShortcut('session.settle'));
    expect(daemon.sentOf('settle_turn')).toEqual([]);

    await gesture(daemon, () => pressShortcut('session.nextRun'));
    expect(shows(daemon).slice(-1)).toEqual(['session:r1']);
    expect(screen.getByText(/nightly docs · run 1 of 2 needing you/)).toBeInTheDocument();

    (document.activeElement as HTMLElement | null)?.blur();
    await gesture(daemon, () => pressShortcut('session.settle'));
    expect(daemon.sentOf('settle_turn')).toEqual([{ cmd: 'settle_turn', session_id: 'r1' }]);

    await gesture(daemon, () => pressShortcut('session.nextRun'));
    expect(shows(daemon).slice(-1)).toEqual(['session:r2']);
  });
});

describe('desktops', () => {
  it('switches desktops through the daemon, shows the desktop’s own active leaf and keeps the one it left mounted', async () => {
    const { daemon } = await renderDesktops();
    await gesture(daemon, () => pressShortcut('desktop.select1'));

    await gesture(daemon, () => pressShortcut('desktop.select2'));

    expect(daemon.sentOf('desktop_set_current').slice(-1)).toEqual([expect.objectContaining({ desktop_id: 'd2' })]);
    expect(shows(daemon)).toEqual([]);
    expect(shownLeaf()).toBe('tile-readme');
    expect(mountedDesktops()).toEqual(expect.arrayContaining(['d1', 'd2']));
    expect(mountedDesktops()).not.toContain('d3');
  });

  it('shows no desktop as active at Home and leaves the daemon’s leaf alone', async () => {
    const { daemon } = await renderDesktops();
    await gesture(daemon, () => pressShortcut('desktop.select1'));
    expect(shownLeaf()).toBe('pane-s1');

    await gesture(daemon, () => pressShortcut('session.goToDashboard'));

    expect(shownLeaf()).toBeNull();
    expect(shows(daemon)).toEqual([]);
    expect(daemon.sentOf('desktop_set_active_pane')).toEqual([]);
  });

  it('shows an agent a deep link names when the daemon reported it after the app mounted', async () => {
    const { daemon } = await renderApp({ initialState: {
      sessions: [daemonSession('s1')],
      profiles: [defaultProfile('d1')],
      desktops: [agentBesideReadme('s1', 'd1', 1)],
    } });
    await gesture(daemon, () => daemon.emit({ event: 'sessions_updated', sessions: [daemonSession('s1'), daemonSession('late', { directory: '/tmp/late' })] }));

    await gesture(daemon, () => act(() => vi.mocked(onOpenUrl).mock.lastCall![0](['attn://spawn?cwd=%2Ftmp%2Flate'])));

    expect(shows(daemon)).toEqual(['session:late']);
    expect(shownLeaf()).toBe('pane-late');
  });
});

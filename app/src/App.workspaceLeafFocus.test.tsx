import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openMarkdownTiles } from './test/appFixtures';
import { agentWorkspace, daemonSession, workspaceWithTiles } from './test/daemonFixtures';
import { pressShortcut } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const NOTES = { tile_id: 'tile-a', tile_kind: 'markdown', tile_params: '/tmp/s1/NOTES.md', tile_session_id: 's1' };

async function openPaneBesideNotes() {
  const view = await openMarkdownTiles('# Notes', {
    path: NOTES.tile_params,
    initialState: {
      sessions: [daemonSession('s1', { workspace_id: 'ws', state: 'idle' }), daemonSession('s2', { state: 'idle' })],
      workspaces: [workspaceWithTiles([]), agentWorkspace('s2')],
    },
  });
  const perform = async (gesture: () => void) => {
    gesture();
    await view.daemon.idle();
  };
  const press = (id: Parameters<typeof pressShortcut>[0]) => perform(() => pressShortcut(id, document.activeElement!));
  return { ...view, perform, press };
}

const surface = () => document.querySelector<HTMLElement>('[data-session-terminal-workspace="ws"]')!;
const agentPaneEl = () => document.querySelector<HTMLElement>('[data-pane-id="pane-s1"]');
const tileBody = () => document.querySelector<HTMLElement>('[data-pane-id="tile-a"] .workspace-dock-tile-body');
const closed = (daemon: ScriptedDaemon) => daemon.sent.filter((command) => (
  command.cmd === 'workspace_layout_close_pane' || command.cmd === 'workspace_layout_undock_tile'
));

describe('App workspace leaf focus', () => {
  it('makes a clicked leaf the active one and gives it the keyboard', async () => {
    const { perform, tile } = await openPaneBesideNotes();

    await perform(() => fireEvent.mouseDown(agentPaneEl()!));
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'pane-s1');
    expect(screen.getByRole('textbox', { name: 'Terminal input' })).toHaveFocus();

    await perform(() => fireEvent.mouseDown(tile()));
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'tile-a');
    expect(tileBody()).toHaveFocus();

    await perform(() => fireEvent.mouseDown(agentPaneEl()!));
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'pane-s1');
  });

  it('closes the focused leaf on ⌘W: it undocks a tile, and closes a pane', async () => {
    const { daemon, perform, press, tile } = await openPaneBesideNotes();

    await perform(() => fireEvent.mouseDown(tile()));
    await press('terminal.close');
    expect(closed(daemon)).toEqual([expect.objectContaining({ cmd: 'workspace_layout_undock_tile', workspace_id: 'ws', tile_id: 'tile-a' })]);

    await perform(() => fireEvent.mouseDown(agentPaneEl()!));
    await press('terminal.close');
    expect(closed(daemon).slice(1)).toEqual([expect.objectContaining({ cmd: 'workspace_layout_close_pane', workspace_id: 'ws', pane_id: 'pane-s1' })]);
  });

  it('closes the pane on ⌘W after Focus utility terminal took the keyboard back from a tile', async () => {
    const { daemon, perform, press, tile } = await openPaneBesideNotes();

    await perform(() => fireEvent.mouseDown(tile()));
    await press('terminal.open');
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'pane-s1');
    await press('terminal.close');

    expect(closed(daemon)).toEqual([expect.objectContaining({ cmd: 'workspace_layout_close_pane', pane_id: 'pane-s1' })]);
  });

  it('zooms then maximizes the focused tile, and keeps the keyboard in it when the terminal comes back', async () => {
    const { perform, press, tile } = await openPaneBesideNotes();
    await perform(() => fireEvent.mouseDown(tile()));

    await press('terminal.toggleZoom');
    expect(surface()).toHaveAttribute('data-zoomed-pane-id', 'tile-a');

    await press('terminal.toggleMaximize');
    expect(surface()).toHaveAttribute('data-maximized-pane-id', 'tile-a');
    expect(surface()).toHaveAttribute('data-zoomed-pane-id', '');
    expect(agentPaneEl()).toBeNull();

    await press('terminal.toggleMaximize');
    expect(agentPaneEl()).not.toBeNull();
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'tile-a');
    expect(tileBody()).toHaveFocus();
  });

  it('forgets a maximized tile once it leaves the layout', async () => {
    const { perform, press, tile, layout } = await openPaneBesideNotes();
    await perform(() => fireEvent.mouseDown(tile()));
    await press('terminal.toggleMaximize');
    expect(surface()).toHaveAttribute('data-maximized-pane-id', 'tile-a');

    await layout([]);
    await layout([NOTES]);

    expect(surface()).toHaveAttribute('data-maximized-pane-id', '');
    expect(agentPaneEl()).not.toBeNull();
  });

  it('keeps a maximized tile across selection changes, but a maximized agent pane only while its session stays selected', async () => {
    const { perform, press, tile, daemon } = await openPaneBesideNotes();
    const select = (label: string) => perform(() => fireEvent.click(screen.getByRole('button', { name: `Open ${label}` })));

    await perform(() => fireEvent.mouseDown(tile()));
    await press('terminal.toggleMaximize');
    await select('s2');
    await select('s1');
    expect(surface()).toHaveAttribute('data-maximized-pane-id', 'tile-a');

    await press('terminal.toggleMaximize');
    await perform(() => fireEvent.mouseDown(agentPaneEl()!));
    await press('terminal.toggleMaximize');
    expect(surface()).toHaveAttribute('data-maximized-pane-id', 'pane-s1');

    await select('s2');
    await select('s1');
    expect(surface()).toHaveAttribute('data-maximized-pane-id', '');

    await press('terminal.toggleMaximize');
    expect(surface()).toHaveAttribute('data-maximized-pane-id', 'pane-s1');
    await perform(() => pressShortcut('session.goToDashboard'));
    await select('s1');
    expect(surface()).toHaveAttribute('data-maximized-pane-id', '');
    expect(daemon.sentOf('workspace_layout_close_pane')).toEqual([]);
  });
});

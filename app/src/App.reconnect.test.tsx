import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentPane, daemonSession, daemonWorkspace } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';
import { initialState } from './test/scriptedDaemon';

const THEME = {
  cmd: 'set_terminal_theme',
  foreground: '#d4d4d4',
  background: '#1e1e1e',
  cursor: '#d4d4d4',
  ansi_palette: [
    '#000000', '#cd3131', '#0dbc79', '#e5e510',
    '#2472c8', '#bc3fbc', '#11a8cd', '#e5e5e5',
    '#666666', '#f14c4c', '#23d18b', '#f5f543',
    '#3b8eea', '#d670d6', '#29b8db', '#ffffff',
  ],
};

describe('App daemon reconnect', () => {
  it('holds its terminal theme until the daemon handshake completes', async () => {
    const { daemon } = await renderApp({ initialState: false });
    await daemon.idle();
    expect(daemon.sentOf('set_terminal_theme')).toEqual([]);

    daemon.emit(initialState());
    await daemon.idle();

    expect(daemon.sentOf('set_terminal_theme')).toEqual([THEME]);
  });

  it('tells a reconnected daemon its selection and theme, and asks again for what it shows', async () => {
    const workspace = daemonWorkspace('ws', {
      root: {
        type: 'split',
        split_id: 'split-a',
        direction: 'vertical',
        ratio: 0.5,
        children: [
          { type: 'pane', pane_id: 'pane-s1' },
          { type: 'tile', tile_id: 'tile-md', tile_kind: 'markdown', tile_params: '/tmp/notes.md' },
        ],
      },
      panes: [agentPane('s1', 'ws')],
    }, { title: 's1' });
    const { daemon } = await renderApp({
      initialState: { sessions: [daemonSession('s1', { workspace_id: 'ws' })], workspaces: [workspace] },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
    await daemon.idle();

    const reconnected = await daemon.reconnect();
    await daemon.idle();

    expect(reconnected.sent).toEqual(expect.arrayContaining([
      { cmd: 'session_selected', id: 's1' },
      { cmd: 'workspace_selected', workspace_id: 'ws' },
      THEME,
      { cmd: 'workspace_tile_content_get', workspace_id: 'ws', tile_id: 'tile-md' },
      expect.objectContaining({ cmd: 'session_messages_get', session_id: 's1' }),
    ]));
  });
});

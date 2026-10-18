import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentPane, daemonSession, daemonDesktop, DEFAULT_PROFILE_ID } from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';
import { initialState } from './test/scriptedDaemon';
import { openSettings } from './test/settings';

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

const LIGHT_THEME = {
  cmd: 'set_terminal_theme',
  foreground: '#3b3b3b',
  background: '#ffffff',
  cursor: '#3b3b3b',
  ansi_palette: [
    '#000000', '#cd3131', '#00bc00', '#949800',
    '#0451a5', '#bc05bc', '#0598bc', '#555555',
    '#666666', '#cd3131', '#14ce14', '#b5ba00',
    '#0451a5', '#bc05bc', '#0598bc', '#a5a5a5',
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

  it('sends only the latest terminal colors after the reconnect handshake', async () => {
    const { daemon } = await renderApp();
    await openSettings(daemon);
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-nav-general')));
    await gesture(daemon, () => daemon.disconnect());
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Light' })));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Dark' })));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Light' })));
    daemon.on('client_hello', () => undefined);

    const reconnected = await daemon.reconnect();
    await daemon.idle();
    expect(reconnected.sent.filter(({ cmd }) => cmd === 'set_terminal_theme')).toEqual([]);

    reconnected.emit(initialState());
    await daemon.idle();
    expect(reconnected.sent.filter(({ cmd }) => cmd === 'set_terminal_theme')).toEqual([LIGHT_THEME]);
  });

  it('tells a reconnected daemon its profile and theme, and asks again for what it shows', async () => {
    const desktop = daemonDesktop('ws', {
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
    }, { name: 'notes desk' });
    const { daemon } = await renderApp({
      initialState: { sessions: [daemonSession('s1')], desktops: [desktop] },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
    await daemon.idle();

    const reconnected = await daemon.reconnect();
    await daemon.idle();

    expect(reconnected.sent).toEqual(expect.arrayContaining([
      expect.objectContaining({ cmd: 'client_hello', profile_id: DEFAULT_PROFILE_ID }),
      THEME,
      expect.objectContaining({ cmd: 'session_messages_get', session_id: 's1' }),
    ]));
  });
});
